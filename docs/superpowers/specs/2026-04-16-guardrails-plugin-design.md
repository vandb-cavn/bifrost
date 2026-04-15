# Guardrails Plugin Implementation Plan

## Goal

Implement a Guardrails plugin for Bifrost OSS that enforces content safety and policy rules on LLM requests and responses. Rules are defined using CEL expressions as triggers, optionally backed by external provider profiles (AWS Bedrock, Azure Content Safety, GraySwan, Patronus AI) for deep content evaluation. Mirrors the enterprise Guardrails feature architecture.

## Architecture

The plugin introduces two first-class entities — **Rules** and **Profiles** — stored in the framework DB, managed via admin CRUD APIs, and cached in-memory by the plugin. Rules define *when* to evaluate (CEL trigger), *what* to do (block/warn), and *which* profiles to invoke. Profiles define *how* to evaluate using external provider credentials.

The plugin implements `LLMPlugin` + `HTTPTransportPlugin`. Per-request guardrail attachment is supported via `bifrost_config.guardrails` body field or `x-bf-guardrail-ids` header. Config sync propagates rule/profile changes to peer nodes via Redis streams.

**Tech Stack:** `github.com/google/cel-go`, GORM (existing), `net/http` for external provider calls, existing `PublishingConfigStore` + Redis stream sync.

---

## Section 1: Rule Model + Profile Model

### DB Tables

```go
// framework/configstore/tables/guardrail_rule.go

type TableGuardrailRule struct {
    ID            string    `gorm:"primaryKey;type:varchar(255)"`
    Name          string    `gorm:"type:varchar(255);not null"`
    Description   string    `gorm:"type:text"`
    Enabled       bool      `gorm:"not null;default:true"`
    CelExpression string    `gorm:"type:text;not null"` // CEL trigger condition
    ApplyTo       string    `gorm:"type:varchar(10);not null"` // "input"|"output"|"both"
    Action        string    `gorm:"type:varchar(10);not null"` // "block"|"warn"
    SamplingRate  int       `gorm:"not null;default:100"`      // 0–100, percent of requests to evaluate
    TimeoutMs     int       `gorm:"not null;default:5000"`     // timeout for profile calls
    Priority      int       `gorm:"not null;default:0;index"`  // lower = evaluated first
    Scope         string    `gorm:"type:varchar(50);not null"` // "global"|"virtual_key"|"team"
    ScopeID       *string   `gorm:"type:varchar(255)"`         // nil for global
    BlockMessage  string    `gorm:"type:text"`                 // message returned on block/warn

    // many-to-many via guardrail_rule_profiles join table
    Profiles []TableGuardrailProfile `gorm:"many2many:guardrail_rule_profiles"`

    CreatedAt time.Time `gorm:"index;not null"`
    UpdatedAt time.Time `gorm:"index;not null"`
}

func (TableGuardrailRule) TableName() string { return "guardrail_rules" }
```

```go
// framework/configstore/tables/guardrail_profile.go

type TableGuardrailProfile struct {
    ID           string `gorm:"primaryKey;type:varchar(255)"`
    Name         string `gorm:"type:varchar(255);not null"`
    ProviderName string `gorm:"type:varchar(50);not null"` // "bedrock"|"azure"|"grayswan"|"patronus_ai"
    Enabled      bool   `gorm:"not null;default:true"`
    ConfigJSON   string `gorm:"type:text"` // provider credentials + settings; encrypted at rest via BeforeSave/AfterFind (same pattern as TablePlugin)

    EncryptionStatus string `gorm:"type:varchar(20);default:'plain_text'" json:"-"`

    CreatedAt time.Time `gorm:"index;not null"`
    UpdatedAt time.Time `gorm:"index;not null"`
}

func (TableGuardrailProfile) TableName() string { return "guardrail_profiles" }

// BeforeSave encrypts ConfigJSON if encrypt.IsEnabled() — same pattern as TablePlugin.
// AfterFind decrypts ConfigJSON before use.
```

### CEL Trigger Context

CEL expressions are **pre-filters** that determine whether a rule fires. If CEL returns `true`:
- Rule with no profiles: CEL condition IS the violation (used for keyword/pattern blocking)
- Rule with profiles: profiles are invoked to perform deep content evaluation

**Input rule context:**
```
request.messages  // list<{role: string, content: string}>
request.model     // string
```

**Output rule context:**
```
output.content       // string (first choice content)
output.finish_reason // string
```

**Example expressions:**
```cel
// Always apply (run profiles on all requests)
true

// Block keyword in any message
request.messages.exists(m, m.content.contains("confidential"))

// Apply only to GPT-4 models
request.model.startsWith("gpt-4")

// Block if any user message exceeds 1000 chars
request.messages.filter(m, m.role == "user").map(m, m.content.size()).sum() > 1000
```

---

## Section 2: Plugin Hook Flow

### Per-request attachment (HTTPTransportPreHook)

```
1. Read bifrost_config.guardrails from request body:
   { "input": ["profile-id-1"], "output": ["profile-id-2"] }
   OR x-bf-guardrail-ids header (comma-separated profile IDs for both)
2. Store parsed IDs in BifrostContext under key "bf-guardrail-input-profiles"
   and "bf-guardrail-output-profiles"
```

### Input check (PreLLMHook)

```
1. Resolve rules:
   - global scope rules
   - rules scoped to current virtual_key or team (from BifrostContext)
2. Filter: enabled=true, apply_to ∈ {"input", "both"}
3. Sort by Priority ascending
4. For each rule:
   a. Sampling: skip if rand(0,100) > rule.SamplingRate
   b. Eval CEL with { request.messages, request.model }
   c. CEL = false → skip
   d. CEL = true, no profiles → violation
   e. CEL = true, has profiles:
      - Also include per-request profile IDs from BifrostContext
      - Call each enabled profile client with timeout (rule.TimeoutMs)
      - Any profile returns violation → violation
   f. violation + action=block →
        return LLMPluginShortCircuit{Error: &BifrostError{
            StatusCode:     ptr(446),
            IsBifrostError: true,
            AllowFallbacks: ptr(false),
            Error:          &ErrorField{Message: rule.BlockMessage},
        }}
   g. violation + action=warn →
        set BifrostContext[guardrailWarnedKey] = true  // typed const: guardrailWarnedKey BifrostContextKey = "bf-guardrail-warned"
        continue (request proceeds to provider)
```

### Output check (PostLLMHook)

```
1. Resolve output rules (same scope resolution)
2. Filter: apply_to ∈ {"output", "both"}
3. For each rule:
   a. CEL context = { output.content, output.finish_reason }
   b. Same CEL + profile evaluation flow as input
   c. violation + action=block →
        return BifrostError{StatusCode: ptr(446)}
   d. violation + action=warn →
        set BifrostContext["bf-guardrail-warned"] = true
```

### Warn → HTTP 246 (HTTPTransportPostHook)

```
Read BifrostContext[guardrailWarnedKey]
→ if true: set resp.StatusCode = 246
```

Typed context key constants defined in `main.go`:
```go
const (
    guardrailWarnedKey        schemas.BifrostContextKey = "bf-guardrail-warned"
    guardrailInputProfilesKey schemas.BifrostContextKey = "bf-guardrail-input-profiles"
    guardrailOutputProfilesKey schemas.BifrostContextKey = "bf-guardrail-output-profiles"
)
```

### Profile evaluation dispatch

Each profile is evaluated by a provider-specific client:

| ProviderName   | Client         | Violation signal                              |
|----------------|----------------|-----------------------------------------------|
| `bedrock`      | `bedrock.go`   | AWS Bedrock Guardrail API → `action=BLOCKED`  |
| `azure`        | `azure.go`     | Azure Content Safety → severity ≥ threshold   |
| `grayswan`     | `grayswan.go`  | GraySwan API → violation score ≥ threshold    |
| `patronus_ai`  | `patronus.go`  | Patronus AI → evaluation fails                |

Profile client interface:
```go
type ProfileClient interface {
    Evaluate(ctx context.Context, content string) (violated bool, reason string, err error)
}
```

---

## Section 3: Admin API + Config Sync

### HTTP Handlers

```
# Rules CRUD
GET    /api/guardrails/rules
POST   /api/guardrails/rules
GET    /api/guardrails/rules/:id
PUT    /api/guardrails/rules/:id
DELETE /api/guardrails/rules/:id

# Profiles CRUD
GET    /api/guardrails/profiles
POST   /api/guardrails/profiles
GET    /api/guardrails/profiles/:id
PUT    /api/guardrails/profiles/:id
DELETE /api/guardrails/profiles/:id

# Link/unlink profile to rule
POST   /api/guardrails/rules/:id/profiles/:profile_id
DELETE /api/guardrails/rules/:id/profiles/:profile_id
```

All handlers in `transports/bifrost-http/server/guardrails_handlers.go`. Pattern follows existing routing rules handlers.

### ConfigStore Methods

```go
// framework/configstore/guardrail_methods.go

GetGuardrailRules(ctx context.Context) ([]*TableGuardrailRule, error)
GetGuardrailRuleByID(ctx context.Context, id string) (*TableGuardrailRule, error)
CreateGuardrailRule(ctx context.Context, rule *TableGuardrailRule) error
UpdateGuardrailRule(ctx context.Context, rule *TableGuardrailRule) error
DeleteGuardrailRule(ctx context.Context, id string) error

GetGuardrailProfiles(ctx context.Context) ([]*TableGuardrailProfile, error)
GetGuardrailProfileByID(ctx context.Context, id string) (*TableGuardrailProfile, error)
CreateGuardrailProfile(ctx context.Context, profile *TableGuardrailProfile) error
UpdateGuardrailProfile(ctx context.Context, profile *TableGuardrailProfile) error
DeleteGuardrailProfile(ctx context.Context, id string) error

LinkGuardrailProfile(ctx context.Context, ruleID, profileID string) error
UnlinkGuardrailProfile(ctx context.Context, ruleID, profileID string) error
```

`PublishingConfigStore` wraps these methods and emits after commit:

```go
// After CreateGuardrailRule / UpdateGuardrailRule:
ConfigSyncEvent{Type: "guardrail_rule", Action: "upsert", ID: rule.ID}

// After DeleteGuardrailRule:
ConfigSyncEvent{Type: "guardrail_rule", Action: "delete", ID: id}

// After CreateGuardrailProfile / UpdateGuardrailProfile:
ConfigSyncEvent{Type: "guardrail_profile", Action: "upsert", ID: profile.ID}

// After DeleteGuardrailProfile:
ConfigSyncEvent{Type: "guardrail_profile", Action: "delete", ID: id}

// After Link/UnlinkGuardrailProfile:
ConfigSyncEvent{Type: "guardrail_rule", Action: "upsert", ID: ruleID}
// (peer reloads rule with full preloaded Profiles)
```

### Peer node sync (handleConfigSyncEvent)

```go
case "guardrail_rule":
    if event.Action == "delete" {
        s.GuardrailsPlugin.DeleteRule(event.ID)
    } else {
        rule, err := s.Config.ConfigStore.GetGuardrailRuleByID(ctx, event.ID)
        // rule includes preloaded Profiles via GORM eager load
        s.GuardrailsPlugin.UpsertRule(rule)
    }
case "guardrail_profile":
    if event.Action == "delete" {
        s.GuardrailsPlugin.DeleteProfile(event.ID)
    } else {
        profile, err := s.Config.ConfigStore.GetGuardrailProfileByID(ctx, event.ID)
        s.GuardrailsPlugin.UpsertProfile(profile)
    }
```

### FullReload additions

```go
rules, err := s.Config.ConfigStore.GetGuardrailRules(ctx)
s.GuardrailsPlugin.ReloadRules(rules)

profiles, err := s.Config.ConfigStore.GetGuardrailProfiles(ctx)
s.GuardrailsPlugin.ReloadProfiles(profiles)
```

---

## Section 4: File Structure + Testing

### Files

```
plugins/guardrails/
├── main.go              # Plugin struct, Init (receives ConfigStore + builds CEL env + profile clients from DB), Cleanup
├── hooks.go             # PreLLMHook, PostLLMHook, HTTPTransportPreHook, HTTPTransportPostHook
├── rules_cache.go       # In-memory rule/profile cache; scope indexing (global, virtual_key, team)
│                        # Methods: ReloadRules, ReloadProfiles, UpsertRule, DeleteRule,
│                        #          UpsertProfile, DeleteProfile, GetInputRules, GetOutputRules
├── cel_evaluator.go     # CEL env setup; Evaluate(expr string, vars map) (bool, error)
├── providers.go         # ProfileClient interface + factory (newProfileClient by provider name)
├── bedrock.go           # AWS Bedrock Guardrails HTTP client
├── azure.go             # Azure Content Safety HTTP client
├── grayswan.go          # GraySwan API client
├── patronus.go          # Patronus AI API client
└── hooks_test.go        # Integration tests for hook flows

framework/configstore/tables/
├── guardrail_rule.go    # TableGuardrailRule (+ GORM migrations)
└── guardrail_profile.go # TableGuardrailProfile (+ GORM migrations)

framework/configstore/
└── guardrail_methods.go # ConfigStore interface + RDB implementation for guardrail CRUD

transports/bifrost-http/server/
└── guardrails_handlers.go # HTTP handlers for rules + profiles CRUD + link/unlink
```

### Testing Strategy

**`cel_evaluator_test.go`** — unit tests, no external deps:
- `true` expression always returns true
- Keyword match: `request.messages.exists(m, m.content.contains("bomb"))` — true/false cases
- Model filter: `request.model.startsWith("gpt-4")` — match/no-match
- Invalid CEL expression → error at Init time, not at eval time

**`rules_cache_test.go`**:
- Global rules appear for all requests
- `virtual_key` scoped rule only appears when BifrostContext has matching key
- `team` scoped rule only appears for matching team
- Sampling rate=0 → rule never evaluated; rate=100 → always evaluated
- Per-request profile IDs merged with rule profiles (no duplicates)

**`hooks_test.go`** — mock configstore + mock profile clients:
- CEL-only block (no profiles): PreLLMHook returns 446 short-circuit
- CEL-only warn (no profiles): request continues, HTTPTransportPostHook sets 246
- CEL false: rule skipped, no block
- Profile violation → block
- Profile no-violation → pass
- Profile timeout → treated as pass (fail-open), warning logged
- Output block: PostLLMHook returns 446 error
- `apply_to=input` rule not evaluated in PostLLMHook
- `apply_to=output` rule not evaluated in PreLLMHook
- `apply_to=both` rule evaluated in both hooks

**`providers_test.go`** — mock HTTP server:
- Bedrock returns `action=BLOCKED` → `violated=true`
- Azure returns `severity >= threshold` → `violated=true`
- HTTP 500 from provider → treated as pass (fail-open)

**`guardrail_methods_test.go`** (framework/configstore):
- CRUD round-trip: create → get → update → delete
- Link/unlink profile to rule
- GetGuardrailRuleByID preloads Profiles

---

## Out of Scope

- WASM plugin support (native Go only)
- Streaming response guardrails (output check applies to complete response only)
- Async guardrail evaluation (`async: true` in enterprise — OSS always sync)
- `redact` action (enterprise warns with potential redaction — OSS warn is pass-through only)
- UI implementation (UI skeleton at `ui/app/workspace/guardrails/` already exists; wiring to API is a separate task)
