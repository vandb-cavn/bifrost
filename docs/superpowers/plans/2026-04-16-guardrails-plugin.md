# Guardrails Plugin Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement a Guardrails plugin for Bifrost OSS that enforces content safety rules using CEL expressions and optional external provider profiles (Bedrock, Azure, GraySwan, Patronus AI).

**Architecture:** Rules (CEL trigger + scope + action) are stored in the DB, cached in-memory with pre-compiled `cel.Program`, and evaluated in `PreLLMHook`/`PostLLMHook`. Profiles store encrypted provider credentials and implement a `ProfileClient` interface. Config sync via Redis streams propagates changes to peer nodes. The plugin is registered as `PluginPlacementBuiltin` (after governance) in the server's builtin plugin chain.

**Task 7 refinements (output CEL context & profile timeouts):**

1. **Output CEL / `request.messages`:** Define **`guardrailRequestMessagesKey`** on `BifrostContext`. In **`PreLLMHook`**, extract chat messages from the inbound request (same shape as input CEL) and **`SetValue`** them under this key. In **`PostLLMHook`**, read the key and pass those messages into the CEL variable map as **`request.messages`** (alongside `request.model` and `output.*`). This lets **output-scoped** rules compare assistant text to the original user/system messages (e.g. hallucination or consistency checks). Optionally use **`guardrailRequestModelKey`** for the inbound model string so `request.model` is populated before the response overwrites it with the provider-reported model when available.

2. **Timeout vs request deadline:** In **`evaluateProfiles`**, derive the profile-call timeout from **`rule.TimeoutMs`**, then if **`ctx.Deadline()`** is set, compute **`remaining := time.Until(deadline)`** and set **`timeout = min(ruleTimeout, remaining)`** (clamp negative remaining to `0`). Use **`context.WithTimeout(ctx, timeout)`** (parent = request `BifrostContext`) so guardrails cannot run longer than the remaining request budget and inherit cancellation.

**Tech Stack:** `github.com/google/cel-go` (already in governance go.mod), GORM, `net/http`, existing `PublishingConfigStore` + Redis stream sync, `framework/encrypt`.

---

## File Map

**New files:**
- `framework/configstore/tables/guardrail_rule.go` — `TableGuardrailRule` GORM model
- `framework/configstore/tables/guardrail_profile.go` — `TableGuardrailProfile` GORM model (encrypted)
- `framework/configstore/guardrail_methods.go` — ConfigStore interface additions + RDB implementation
- `plugins/guardrails/go.mod` — module definition
- `plugins/guardrails/main.go` — plugin struct, `Init`, `Cleanup`, context key constants
- `plugins/guardrails/cel_evaluator.go` — CEL env + `Evaluate(program, vars)`
- `plugins/guardrails/rules_cache.go` — in-memory cache with `sync.RWMutex`, CEL pre-compilation
- `plugins/guardrails/providers.go` — `ProfileClient` interface + factory
- `plugins/guardrails/bedrock.go` — AWS Bedrock Guardrails client
- `plugins/guardrails/azure.go` — Azure Content Safety client
- `plugins/guardrails/grayswan.go` — GraySwan API client
- `plugins/guardrails/patronus.go` — Patronus AI client
- `plugins/guardrails/model_armor.go` — Google Cloud Model Armor client (REST, oauth2 ADC/service-account)
- `plugins/guardrails/hooks.go` — `PreLLMHook`, `PostLLMHook`, `HTTPTransportPreHook`, `HTTPTransportPostHook` (audit log every block/warn at INFO)
- `transports/bifrost-http/server/guardrails_handlers.go` — HTTP CRUD handlers
- `plugins/guardrails/cel_evaluator_test.go`
- `plugins/guardrails/rules_cache_test.go`
- `plugins/guardrails/hooks_test.go`
- `plugins/guardrails/providers_test.go`
- `framework/configstore/guardrail_methods_test.go`

**Modified files:**
- `framework/configstore/migrations.go` — add `migrationAddGuardrailsTables` call at end of `triggerMigrations`
- `framework/configstore/store.go` — add guardrail methods to `ConfigStore` interface
- `framework/configstore/publishing_config_store.go` — wrap guardrail methods with event publishing
- `transports/bifrost-http/server/server.go` — add `handleConfigSyncEvent` cases + `FullReload` additions
- `transports/bifrost-http/server/plugins.go` — add `guardrails` case to `loadBuiltinPlugin`
- `transports/bifrost-http/server/server.go` — register guardrails HTTP routes

---

### Task 1: DB Tables + Migration

**Files:**
- Create: `framework/configstore/tables/guardrail_rule.go`
- Create: `framework/configstore/tables/guardrail_profile.go`
- Modify: `framework/configstore/migrations.go`

- [ ] **Step 1: Write `guardrail_rule.go`**

```go
// framework/configstore/tables/guardrail_rule.go
package tables

import "time"

// TableGuardrailRule defines when and how content is evaluated for policy violations.
type TableGuardrailRule struct {
	ID            string    `gorm:"primaryKey;type:varchar(255)" json:"id"`
	Name          string    `gorm:"type:varchar(255);not null" json:"name"`
	Description   string    `gorm:"type:text" json:"description"`
	Enabled       bool      `gorm:"not null;default:true" json:"enabled"`
	CelExpression string    `gorm:"type:text;not null" json:"cel_expression"`
	ApplyTo       string    `gorm:"type:varchar(10);not null" json:"apply_to"`    // "input"|"output"|"both"
	Action        string    `gorm:"type:varchar(10);not null" json:"action"`      // "block"|"warn"
	SamplingRate  int       `gorm:"not null;default:100" json:"sampling_rate"`    // 0–100
	TimeoutMs     int       `gorm:"not null;default:5000" json:"timeout_ms"`
	Priority      int       `gorm:"type:int;not null;default:0;index" json:"priority"`
	Scope         string    `gorm:"type:varchar(50);not null" json:"scope"`       // "global"|"virtual_key"|"team"
	ScopeID       *string   `gorm:"type:varchar(255)" json:"scope_id"`
	BlockMessage  string    `gorm:"type:text" json:"block_message"`
	FailOpen      bool      `gorm:"not null;default:true" json:"fail_open"`

	// many-to-many via guardrail_rule_profiles join table; delete rule → delete join rows
	Profiles []TableGuardrailProfile `gorm:"many2many:guardrail_rule_profiles;constraint:OnDelete:CASCADE" json:"profiles,omitempty"`

	CreatedAt time.Time `gorm:"index;not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"index;not null" json:"updated_at"`
}

func (TableGuardrailRule) TableName() string { return "guardrail_rules" }
```

- [ ] **Step 2: Write `guardrail_profile.go`**

```go
// framework/configstore/tables/guardrail_profile.go
package tables

import (
	"fmt"
	"time"

	"github.com/maximhq/bifrost/framework/encrypt"
	"gorm.io/gorm"
)

// TableGuardrailProfile stores credentials for an external content-safety provider.
// ConfigJSON is encrypted at rest using the same pattern as TablePlugin.
type TableGuardrailProfile struct {
	ID               string    `gorm:"primaryKey;type:varchar(255)" json:"id"`
	Name             string    `gorm:"type:varchar(255);not null" json:"name"`
	ProviderName     string    `gorm:"type:varchar(50);not null" json:"provider_name"` // "bedrock"|"azure"|"grayswan"|"patronus_ai"
	Enabled          bool      `gorm:"not null;default:true" json:"enabled"`
	ConfigJSON       string    `gorm:"type:text" json:"-"`
	EncryptionStatus string    `gorm:"type:varchar(20);default:'plain_text'" json:"-"`
	CreatedAt        time.Time `gorm:"index;not null" json:"created_at"`
	UpdatedAt        time.Time `gorm:"index;not null" json:"updated_at"`
}

func (TableGuardrailProfile) TableName() string { return "guardrail_profiles" }

// BeforeSave encrypts ConfigJSON if encryption is enabled.
func (p *TableGuardrailProfile) BeforeSave(tx *gorm.DB) error {
	if encrypt.IsEnabled() && p.ConfigJSON != "" && p.ConfigJSON != "{}" {
		encrypted, err := encrypt.Encrypt(p.ConfigJSON)
		if err != nil {
			return fmt.Errorf("failed to encrypt guardrail profile config: %w", err)
		}
		p.ConfigJSON = encrypted
		p.EncryptionStatus = EncryptionStatusEncrypted
	}
	return nil
}

// AfterFind decrypts ConfigJSON if it was encrypted.
func (p *TableGuardrailProfile) AfterFind(tx *gorm.DB) error {
	if p.EncryptionStatus == EncryptionStatusEncrypted && p.ConfigJSON != "" {
		decrypted, err := encrypt.Decrypt(p.ConfigJSON)
		if err != nil {
			return fmt.Errorf("failed to decrypt guardrail profile config: %w", err)
		}
		p.ConfigJSON = decrypted
	}
	return nil
}
```

- [ ] **Step 3: Add migration to `migrations.go`**

At the end of `triggerMigrations` (after the last `if err :=` block, before the `return nil`), add:

```go
	if err := migrationAddGuardrailsTables(ctx, db); err != nil {
		return err
	}
```

Then add the migration function at the bottom of the file:

```go
// migrationAddGuardrailsTables creates guardrail_rules, guardrail_profiles,
// and the guardrail_rule_profiles join table.
func migrationAddGuardrailsTables(ctx context.Context, db *gorm.DB) error {
	m := migrator.New(db, migrator.DefaultOptions, []*migrator.Migration{{
		ID: "add_guardrails_tables",
		Migrate: func(tx *gorm.DB) error {
			tx = tx.WithContext(ctx)
			mgr := tx.Migrator()
			if !mgr.HasTable(&tables.TableGuardrailProfile{}) {
				if err := mgr.CreateTable(&tables.TableGuardrailProfile{}); err != nil {
					return err
				}
			}
			if !mgr.HasTable(&tables.TableGuardrailRule{}) {
				if err := mgr.CreateTable(&tables.TableGuardrailRule{}); err != nil {
					return err
				}
			}
			// GORM auto-creates the many2many join table when AutoMigrate is called
			// on the owning side (TableGuardrailRule) which has the Profiles field.
			return tx.AutoMigrate(&tables.TableGuardrailRule{})
		},
		Rollback: func(tx *gorm.DB) error {
			tx = tx.WithContext(ctx)
			mgr := tx.Migrator()
			_ = mgr.DropTable("guardrail_rule_profiles")
			_ = mgr.DropTable(&tables.TableGuardrailRule{})
			_ = mgr.DropTable(&tables.TableGuardrailProfile{})
			return nil
		},
	}})
	if err := m.Migrate(); err != nil {
		return fmt.Errorf("error running add_guardrails_tables migration: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Verify framework compiles**

```bash
cd /path/to/bifost2/framework && go build ./...
```

Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add framework/configstore/tables/guardrail_rule.go \
        framework/configstore/tables/guardrail_profile.go \
        framework/configstore/migrations.go
git commit -m "feat(configstore): add guardrail_rules and guardrail_profiles DB tables with migration"
```

---

### Task 2: ConfigStore Interface + RDB Implementation

**Files:**
- Modify: `framework/configstore/store.go` (add interface methods)
- Create: `framework/configstore/guardrail_methods.go` (RDB implementation)
- Create: `framework/configstore/guardrail_methods_test.go`

- [ ] **Step 1: Write failing test**

```go
// framework/configstore/guardrail_methods_test.go
package configstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupGuardrailStore(t *testing.T) configstore.ConfigStore {
	t.Helper()
	store := setupTestStore(t) // reuse existing test helper
	return store
}

func TestGuardrailProfileCRUD(t *testing.T) {
	store := setupGuardrailStore(t)
	ctx := context.Background()

	profile := &tables.TableGuardrailProfile{
		ID:           uuid.New().String(),
		Name:         "test-profile",
		ProviderName: "bedrock",
		Enabled:      true,
		ConfigJSON:   `{"region":"us-east-1","guardrail_id":"abc123"}`,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	require.NoError(t, store.CreateGuardrailProfile(ctx, profile))

	got, err := store.GetGuardrailProfileByID(ctx, profile.ID)
	require.NoError(t, err)
	assert.Equal(t, profile.Name, got.Name)
	assert.Equal(t, profile.ProviderName, got.ProviderName)

	got.Name = "updated-profile"
	require.NoError(t, store.UpdateGuardrailProfile(ctx, got))

	updated, err := store.GetGuardrailProfileByID(ctx, profile.ID)
	require.NoError(t, err)
	assert.Equal(t, "updated-profile", updated.Name)

	require.NoError(t, store.DeleteGuardrailProfile(ctx, profile.ID))
	_, err = store.GetGuardrailProfileByID(ctx, profile.ID)
	assert.Error(t, err)
}

func TestGuardrailRuleCRUD(t *testing.T) {
	store := setupGuardrailStore(t)
	ctx := context.Background()

	rule := &tables.TableGuardrailRule{
		ID:            uuid.New().String(),
		Name:          "test-rule",
		Enabled:       true,
		CelExpression: "true",
		ApplyTo:       "input",
		Action:        "block",
		SamplingRate:  100,
		TimeoutMs:     5000,
		Priority:      0,
		Scope:         "global",
		FailOpen:      true,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	require.NoError(t, store.CreateGuardrailRule(ctx, rule))

	got, err := store.GetGuardrailRuleByID(ctx, rule.ID)
	require.NoError(t, err)
	assert.Equal(t, rule.Name, got.Name)

	require.NoError(t, store.DeleteGuardrailRule(ctx, rule.ID))
}

func TestGuardrailLinkUnlinkProfile(t *testing.T) {
	store := setupGuardrailStore(t)
	ctx := context.Background()

	profile := &tables.TableGuardrailProfile{
		ID: uuid.New().String(), Name: "p1", ProviderName: "azure",
		Enabled: true, ConfigJSON: `{}`, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, store.CreateGuardrailProfile(ctx, profile))

	rule := &tables.TableGuardrailRule{
		ID: uuid.New().String(), Name: "r1", Enabled: true,
		CelExpression: "true", ApplyTo: "input", Action: "block",
		SamplingRate: 100, TimeoutMs: 5000, Scope: "global", FailOpen: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, store.CreateGuardrailRule(ctx, rule))

	require.NoError(t, store.LinkGuardrailProfile(ctx, rule.ID, profile.ID))

	got, err := store.GetGuardrailRuleByID(ctx, rule.ID)
	require.NoError(t, err)
	require.Len(t, got.Profiles, 1)
	assert.Equal(t, profile.ID, got.Profiles[0].ID)

	require.NoError(t, store.UnlinkGuardrailProfile(ctx, rule.ID, profile.ID))

	got, err = store.GetGuardrailRuleByID(ctx, rule.ID)
	require.NoError(t, err)
	assert.Empty(t, got.Profiles)
}
```

- [ ] **Step 2: Run test — expect compile failure**

```bash
cd framework && go test ./configstore/... -run TestGuardrailProfile 2>&1 | head -20
```

Expected: compile error — methods undefined on ConfigStore.

- [ ] **Step 3: Add methods to ConfigStore interface in `store.go`**

Find the interface definition in `framework/configstore/store.go` and add after the routing rule methods:

```go
// Guardrail rules
GetGuardrailRules(ctx context.Context) ([]*tables.TableGuardrailRule, error)
GetGuardrailRuleByID(ctx context.Context, id string) (*tables.TableGuardrailRule, error)
CreateGuardrailRule(ctx context.Context, rule *tables.TableGuardrailRule, error)
UpdateGuardrailRule(ctx context.Context, rule *tables.TableGuardrailRule, error)
DeleteGuardrailRule(ctx context.Context, id string) error

// Guardrail profiles
GetGuardrailProfiles(ctx context.Context) ([]*tables.TableGuardrailProfile, error)
GetGuardrailProfileByID(ctx context.Context, id string) (*tables.TableGuardrailProfile, error)
CreateGuardrailProfile(ctx context.Context, profile *tables.TableGuardrailProfile, error)
UpdateGuardrailProfile(ctx context.Context, profile *tables.TableGuardrailProfile, error)
DeleteGuardrailProfile(ctx context.Context, id string) error

// Link/unlink profile ↔ rule (many-to-many)
LinkGuardrailProfile(ctx context.Context, ruleID, profileID string) error
UnlinkGuardrailProfile(ctx context.Context, ruleID, profileID string) error
```

- [ ] **Step 4: Write RDB implementation**

```go
// framework/configstore/guardrail_methods.go
package configstore

import (
	"context"
	"fmt"

	"github.com/maximhq/bifrost/framework/configstore/tables"
	"gorm.io/gorm"
)

// ---------- Guardrail Rules ----------

func (s *RDBConfigStore) GetGuardrailRules(ctx context.Context) ([]*tables.TableGuardrailRule, error) {
	var rules []*tables.TableGuardrailRule
	if err := s.db.WithContext(ctx).
		Preload("Profiles").
		Order("priority ASC, created_at DESC").
		Find(&rules).Error; err != nil {
		return nil, err
	}
	return rules, nil
}

func (s *RDBConfigStore) GetGuardrailRuleByID(ctx context.Context, id string) (*tables.TableGuardrailRule, error) {
	var rule tables.TableGuardrailRule
	if err := s.db.WithContext(ctx).
		Preload("Profiles").
		Where("id = ?", id).
		First(&rule).Error; err != nil {
		return nil, fmt.Errorf("guardrail rule %q not found: %w", id, err)
	}
	return &rule, nil
}

func (s *RDBConfigStore) CreateGuardrailRule(ctx context.Context, rule *tables.TableGuardrailRule) error {
	return s.db.WithContext(ctx).Create(rule).Error
}

func (s *RDBConfigStore) UpdateGuardrailRule(ctx context.Context, rule *tables.TableGuardrailRule) error {
	return s.db.WithContext(ctx).Save(rule).Error
}

func (s *RDBConfigStore) DeleteGuardrailRule(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Where("id = ?", id).Delete(&tables.TableGuardrailRule{}).Error
}

// ---------- Guardrail Profiles ----------

func (s *RDBConfigStore) GetGuardrailProfiles(ctx context.Context) ([]*tables.TableGuardrailProfile, error) {
	var profiles []*tables.TableGuardrailProfile
	if err := s.db.WithContext(ctx).
		Order("created_at DESC").
		Find(&profiles).Error; err != nil {
		return nil, err
	}
	return profiles, nil
}

func (s *RDBConfigStore) GetGuardrailProfileByID(ctx context.Context, id string) (*tables.TableGuardrailProfile, error) {
	var profile tables.TableGuardrailProfile
	if err := s.db.WithContext(ctx).Where("id = ?", id).First(&profile).Error; err != nil {
		return nil, fmt.Errorf("guardrail profile %q not found: %w", id, err)
	}
	return &profile, nil
}

func (s *RDBConfigStore) CreateGuardrailProfile(ctx context.Context, profile *tables.TableGuardrailProfile) error {
	return s.db.WithContext(ctx).Create(profile).Error
}

func (s *RDBConfigStore) UpdateGuardrailProfile(ctx context.Context, profile *tables.TableGuardrailProfile) error {
	return s.db.WithContext(ctx).Save(profile).Error
}

func (s *RDBConfigStore) DeleteGuardrailProfile(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Where("id = ?", id).Delete(&tables.TableGuardrailProfile{}).Error
}

// ---------- Link / Unlink ----------

func (s *RDBConfigStore) LinkGuardrailProfile(ctx context.Context, ruleID, profileID string) error {
	rule, err := s.GetGuardrailRuleByID(ctx, ruleID)
	if err != nil {
		return err
	}
	profile, err := s.GetGuardrailProfileByID(ctx, profileID)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Model(rule).Association("Profiles").Append(profile)
}

func (s *RDBConfigStore) UnlinkGuardrailProfile(ctx context.Context, ruleID, profileID string) error {
	rule, err := s.GetGuardrailRuleByID(ctx, ruleID)
	if err != nil {
		return err
	}
	profile, err := s.GetGuardrailProfileByID(ctx, profileID)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Model(rule).Association("Profiles").Delete(profile)
}
```

- [ ] **Step 5: Run test — expect pass**

```bash
cd framework && go test ./configstore/... -run "TestGuardrailProfile|TestGuardrailRule|TestGuardrailLink" -v
```

Expected: PASS (3 tests).

- [ ] **Step 6: Commit**

```bash
git add framework/configstore/store.go \
        framework/configstore/guardrail_methods.go \
        framework/configstore/guardrail_methods_test.go
git commit -m "feat(configstore): guardrail CRUD methods + interface + tests"
```

---

### Task 3: PublishingConfigStore Wrappers

**Files:**
- Modify: `framework/configstore/publishing_config_store.go`

- [ ] **Step 1: Add wrappers at end of file**

```go
// framework/configstore/publishing_config_store.go — append these methods

func (pcs *PublishingConfigStore) CreateGuardrailRule(ctx context.Context, rule *tables.TableGuardrailRule) error {
	if err := pcs.ConfigStore.CreateGuardrailRule(ctx, rule); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "guardrail_rule", Action: "upsert", ID: rule.ID}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateGuardrailRule(ctx context.Context, rule *tables.TableGuardrailRule) error {
	if err := pcs.ConfigStore.UpdateGuardrailRule(ctx, rule); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "guardrail_rule", Action: "upsert", ID: rule.ID}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) DeleteGuardrailRule(ctx context.Context, id string) error {
	if err := pcs.ConfigStore.DeleteGuardrailRule(ctx, id); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "guardrail_rule", Action: "delete", ID: id}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) CreateGuardrailProfile(ctx context.Context, profile *tables.TableGuardrailProfile) error {
	if err := pcs.ConfigStore.CreateGuardrailProfile(ctx, profile); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "guardrail_profile", Action: "upsert", ID: profile.ID}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateGuardrailProfile(ctx context.Context, profile *tables.TableGuardrailProfile) error {
	if err := pcs.ConfigStore.UpdateGuardrailProfile(ctx, profile); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "guardrail_profile", Action: "upsert", ID: profile.ID}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) DeleteGuardrailProfile(ctx context.Context, id string) error {
	if err := pcs.ConfigStore.DeleteGuardrailProfile(ctx, id); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "guardrail_profile", Action: "delete", ID: id}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) LinkGuardrailProfile(ctx context.Context, ruleID, profileID string) error {
	if err := pcs.ConfigStore.LinkGuardrailProfile(ctx, ruleID, profileID); err != nil {
		return err
	}
	// Reload the rule so peers get fresh profile associations
	scheduleEvent(ctx, ConfigSyncEvent{Type: "guardrail_rule", Action: "upsert", ID: ruleID}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UnlinkGuardrailProfile(ctx context.Context, ruleID, profileID string) error {
	if err := pcs.ConfigStore.UnlinkGuardrailProfile(ctx, ruleID, profileID); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "guardrail_rule", Action: "upsert", ID: ruleID}, pcs.syncer, pcs.nodeID)
	return nil
}
```

- [ ] **Step 2: Verify framework compiles**

```bash
cd framework && go build ./...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add framework/configstore/publishing_config_store.go
git commit -m "feat(configstore): publishing wrappers for guardrail CRUD emit sync events"
```

---

### Task 4: Plugin Module + CEL Evaluator

**Files:**
- Create: `plugins/guardrails/go.mod`
- Create: `plugins/guardrails/cel_evaluator.go`
- Create: `plugins/guardrails/cel_evaluator_test.go`

- [ ] **Step 1: Write failing test**

```go
// plugins/guardrails/cel_evaluator_test.go
package guardrails

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCELEvaluator_TrueAlwaysFires(t *testing.T) {
	env, err := newCELEnv()
	require.NoError(t, err)

	prog, err := compileExpression(env, "true")
	require.NoError(t, err)

	result, err := evalProgram(prog, map[string]interface{}{
		"request": map[string]interface{}{
			"messages": []interface{}{},
			"model":    "gpt-4o",
		},
	})
	require.NoError(t, err)
	assert.True(t, result)
}

func TestCELEvaluator_KeywordBlock(t *testing.T) {
	env, err := newCELEnv()
	require.NoError(t, err)

	prog, err := compileExpression(env, `request.messages.exists(m, m.content.contains("bomb"))`)
	require.NoError(t, err)

	hit, err := evalProgram(prog, map[string]interface{}{
		"request": map[string]interface{}{
			"messages": []interface{}{
				map[string]interface{}{"role": "user", "content": "how to make a bomb"},
			},
			"model": "gpt-4o",
		},
	})
	require.NoError(t, err)
	assert.True(t, hit)

	miss, err := evalProgram(prog, map[string]interface{}{
		"request": map[string]interface{}{
			"messages": []interface{}{
				map[string]interface{}{"role": "user", "content": "hello world"},
			},
			"model": "gpt-4o",
		},
	})
	require.NoError(t, err)
	assert.False(t, miss)
}

func TestCELEvaluator_ModelFilter(t *testing.T) {
	env, err := newCELEnv()
	require.NoError(t, err)

	prog, err := compileExpression(env, `request.model.startsWith("gpt-4")`)
	require.NoError(t, err)

	match, err := evalProgram(prog, map[string]interface{}{
		"request": map[string]interface{}{"messages": []interface{}{}, "model": "gpt-4o"},
	})
	require.NoError(t, err)
	assert.True(t, match)

	noMatch, err := evalProgram(prog, map[string]interface{}{
		"request": map[string]interface{}{"messages": []interface{}{}, "model": "claude-3-sonnet"},
	})
	require.NoError(t, err)
	assert.False(t, noMatch)
}

func TestCELEvaluator_InvalidExpressionErrors(t *testing.T) {
	env, err := newCELEnv()
	require.NoError(t, err)

	_, err = compileExpression(env, `this is not valid CEL !!!`)
	assert.Error(t, err)
}

func TestCELEvaluator_OutputContext(t *testing.T) {
	env, err := newCELEnv()
	require.NoError(t, err)

	prog, err := compileExpression(env, `output.content.contains("error")`)
	require.NoError(t, err)

	result, err := evalProgram(prog, map[string]interface{}{
		"request": map[string]interface{}{"messages": []interface{}{}, "model": "gpt-4o"},
		"output":  map[string]interface{}{"content": "an error occurred", "finish_reason": "stop"},
	})
	require.NoError(t, err)
	assert.True(t, result)
}
```

- [ ] **Step 2: Run test — expect compile failure**

```bash
cd plugins/guardrails && go test ./... -run TestCELEvaluator 2>&1 | head -5
```

Expected: package not found / compile error.

- [ ] **Step 3: Create `go.mod`**

```
module github.com/maximhq/bifrost/plugins/guardrails

go 1.23.0

require (
	github.com/google/cel-go v0.26.1
	github.com/google/uuid v1.6.0
	github.com/maximhq/bifrost/core v1.5.2
	github.com/maximhq/bifrost/framework v1.3.2
	github.com/stretchr/testify v1.11.1
)

replace github.com/maximhq/bifrost/framework => ../../framework
replace github.com/maximhq/bifrost/core => ../../core
```

Run `cd plugins/guardrails && go mod tidy` after writing the first `.go` file.

- [ ] **Step 4: Write `cel_evaluator.go`**

```go
// plugins/guardrails/cel_evaluator.go
package guardrails

import (
	"fmt"

	"github.com/google/cel-go/cel"
)

// newCELEnv creates the singleton CEL environment for guardrail expression evaluation.
// Variables:
//   - request: map<string, dyn> — always present; has "messages" (list) and "model" (string)
//   - output:  map<string, dyn> — present in output-rule evaluation; has "content" and "finish_reason"
func newCELEnv() (*cel.Env, error) {
	return cel.NewEnv(
		cel.Variable("request", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("output", cel.MapType(cel.StringType, cel.DynType)),
	)
}

// compileExpression compiles a CEL expression string into a cel.Program.
// Returns an error if the expression has syntax or type errors.
// The returned cel.Program is safe for concurrent use.
func compileExpression(env *cel.Env, expr string) (cel.Program, error) {
	ast, issues := env.Compile(expr)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("CEL compile error: %w", issues.Err())
	}
	prog, err := env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("CEL program error: %w", err)
	}
	return prog, nil
}

// evalProgram evaluates a pre-compiled CEL program with the given variable bindings.
// vars must contain at least "request"; "output" is optional for input-only rules.
// Returns the boolean result of the expression.
func evalProgram(prog cel.Program, vars map[string]interface{}) (bool, error) {
	// Provide empty output map if not set, so output.content etc. don't panic
	if _, ok := vars["output"]; !ok {
		vars["output"] = map[string]interface{}{}
	}
	out, _, err := prog.Eval(vars)
	if err != nil {
		return false, fmt.Errorf("CEL eval error: %w", err)
	}
	result, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("CEL expression must return bool, got %T", out.Value())
	}
	return result, nil
}
```

- [ ] **Step 5: Run `go mod tidy` then tests**

```bash
cd plugins/guardrails && go mod tidy && go test ./... -run TestCELEvaluator -v
```

Expected: 5 tests PASS.

- [ ] **Step 6: Commit**

```bash
git add plugins/guardrails/go.mod plugins/guardrails/go.sum \
        plugins/guardrails/cel_evaluator.go plugins/guardrails/cel_evaluator_test.go
git commit -m "feat(guardrails): CEL evaluator with env, compile, eval helpers + tests"
```

---

### Task 5: Rules Cache

**Files:**
- Create: `plugins/guardrails/rules_cache.go`
- Create: `plugins/guardrails/rules_cache_test.go`

- [ ] **Step 1: Write failing test**

```go
// plugins/guardrails/rules_cache_test.go
package guardrails

import (
	"testing"
	"time"

	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeRule(id, scope, scopeID, applyTo, action string, enabled bool) *configstoreTables.TableGuardrailRule {
	return &configstoreTables.TableGuardrailRule{
		ID:            id,
		Name:          "rule-" + id,
		Enabled:       enabled,
		CelExpression: "true",
		ApplyTo:       applyTo,
		Action:        action,
		SamplingRate:  100,
		TimeoutMs:     5000,
		Priority:      0,
		Scope:         scope,
		ScopeID:       scopeIDPtr(scopeID),
		FailOpen:      true,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
}

func scopeIDPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func newTestCache(t *testing.T) *rulesCache {
	t.Helper()
	env, err := newCELEnv()
	require.NoError(t, err)
	return newRulesCache(env)
}

func TestRulesCache_GlobalRulesReturnedForAll(t *testing.T) {
	c := newTestCache(t)
	rule := makeRule("r1", "global", "", "input", "block", true)
	c.upsertRule(rule)

	rules := c.getInputRules("", "")
	require.Len(t, rules, 1)
	assert.Equal(t, "r1", rules[0].rule.ID)
}

func TestRulesCache_VirtualKeyScopedRuleFiltered(t *testing.T) {
	c := newTestCache(t)
	c.upsertRule(makeRule("r1", "virtual_key", "vk-abc", "input", "block", true))

	// No match for different VK
	assert.Empty(t, c.getInputRules("vk-xyz", ""))
	// Match for correct VK
	rules := c.getInputRules("vk-abc", "")
	require.Len(t, rules, 1)
	assert.Equal(t, "r1", rules[0].rule.ID)
}

func TestRulesCache_TeamScopedRuleFiltered(t *testing.T) {
	c := newTestCache(t)
	c.upsertRule(makeRule("r1", "team", "team-1", "both", "warn", true))

	assert.Empty(t, c.getOutputRules("", "team-2"))
	rules := c.getOutputRules("", "team-1")
	require.Len(t, rules, 1)
}

func TestRulesCache_DisabledRuleExcluded(t *testing.T) {
	c := newTestCache(t)
	c.upsertRule(makeRule("r1", "global", "", "input", "block", false))
	assert.Empty(t, c.getInputRules("", ""))
}

func TestRulesCache_ApplyToFilter(t *testing.T) {
	c := newTestCache(t)
	c.upsertRule(makeRule("r-input", "global", "", "input", "block", true))
	c.upsertRule(makeRule("r-output", "global", "", "output", "block", true))
	c.upsertRule(makeRule("r-both", "global", "", "both", "block", true))

	inputRules := c.getInputRules("", "")
	assert.Len(t, inputRules, 2) // r-input + r-both

	outputRules := c.getOutputRules("", "")
	assert.Len(t, outputRules, 2) // r-output + r-both
}

func TestRulesCache_DeleteRule(t *testing.T) {
	c := newTestCache(t)
	c.upsertRule(makeRule("r1", "global", "", "input", "block", true))
	c.deleteRule("r1")
	assert.Empty(t, c.getInputRules("", ""))
}

func TestRulesCache_InvalidCELRuleSkipped(t *testing.T) {
	c := newTestCache(t)
	rule := makeRule("r1", "global", "", "input", "block", true)
	rule.CelExpression = "this is invalid CEL !!!"
	c.upsertRule(rule) // should not panic; should log and skip
	assert.Empty(t, c.getInputRules("", ""))
}

func TestRulesCache_UpsertUpdatesExisting(t *testing.T) {
	c := newTestCache(t)
	rule := makeRule("r1", "global", "", "input", "block", true)
	c.upsertRule(rule)
	rule.Action = "warn"
	c.upsertRule(rule)

	rules := c.getInputRules("", "")
	require.Len(t, rules, 1)
	assert.Equal(t, "warn", rules[0].rule.Action)
}
```

- [ ] **Step 2: Run test — expect compile failure**

```bash
cd plugins/guardrails && go test ./... -run TestRulesCache 2>&1 | head -10
```

- [ ] **Step 3: Write `rules_cache.go`**

```go
// plugins/guardrails/rules_cache.go
package guardrails

import (
	"fmt"
	"sort"
	"sync"

	"github.com/google/cel-go/cel"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
)

// cachedRule holds a guardrail rule alongside its pre-compiled CEL program.
// The program is compiled once at upsert time and reused for every request.
type cachedRule struct {
	rule    *configstoreTables.TableGuardrailRule
	program cel.Program
}

// rulesCache is the in-memory store for guardrail rules and profiles.
// Protected by sync.RWMutex: RLock for hot-path reads, Lock for sync-event writes.
type rulesCache struct {
	mu       sync.RWMutex
	rules    map[string]*cachedRule                        // ruleID → cachedRule
	profiles map[string]*configstoreTables.TableGuardrailProfile // profileID → profile
	celEnv   *cel.Env
}

func newRulesCache(env *cel.Env) *rulesCache {
	return &rulesCache{
		rules:    make(map[string]*cachedRule),
		profiles: make(map[string]*configstoreTables.TableGuardrailProfile),
		celEnv:   env,
	}
}

// upsertRule adds or replaces a rule in the cache.
// CEL expression is compiled at this point; if it fails, the rule is skipped (logged externally).
func (c *rulesCache) upsertRule(rule *configstoreTables.TableGuardrailRule) error {
	if !rule.Enabled {
		c.mu.Lock()
		delete(c.rules, rule.ID)
		c.mu.Unlock()
		return nil
	}
	prog, err := compileExpression(c.celEnv, rule.CelExpression)
	if err != nil {
		return fmt.Errorf("rule %q CEL compile failed (rule disabled): %w", rule.ID, err)
	}
	c.mu.Lock()
	c.rules[rule.ID] = &cachedRule{rule: rule, program: prog}
	c.mu.Unlock()
	return nil
}

func (c *rulesCache) deleteRule(id string) {
	c.mu.Lock()
	delete(c.rules, id)
	c.mu.Unlock()
}

func (c *rulesCache) upsertProfile(profile *configstoreTables.TableGuardrailProfile) {
	c.mu.Lock()
	if profile.Enabled {
		c.profiles[profile.ID] = profile
	} else {
		delete(c.profiles, profile.ID)
	}
	c.mu.Unlock()
}

func (c *rulesCache) deleteProfile(id string) {
	c.mu.Lock()
	delete(c.profiles, id)
	c.mu.Unlock()
}

func (c *rulesCache) getProfile(id string) *configstoreTables.TableGuardrailProfile {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.profiles[id]
}

// getInputRules returns enabled rules applicable to input evaluation for the given scope IDs,
// sorted by Priority ascending. vkID and teamID may be empty (global requests).
func (c *rulesCache) getInputRules(vkID, teamID string) []*cachedRule {
	return c.getRulesByApplyTo([]string{"input", "both"}, vkID, teamID)
}

// getOutputRules returns enabled rules applicable to output evaluation.
func (c *rulesCache) getOutputRules(vkID, teamID string) []*cachedRule {
	return c.getRulesByApplyTo([]string{"output", "both"}, vkID, teamID)
}

func (c *rulesCache) getRulesByApplyTo(applyTo []string, vkID, teamID string) []*cachedRule {
	c.mu.RLock()
	defer c.mu.RUnlock()

	applySet := make(map[string]struct{}, len(applyTo))
	for _, a := range applyTo {
		applySet[a] = struct{}{}
	}

	var result []*cachedRule
	for _, cr := range c.rules {
		if _, ok := applySet[cr.rule.ApplyTo]; !ok {
			continue
		}
		if !matchesScope(cr.rule, vkID, teamID) {
			continue
		}
		result = append(result, cr)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].rule.Priority < result[j].rule.Priority
	})
	return result
}

func matchesScope(rule *configstoreTables.TableGuardrailRule, vkID, teamID string) bool {
	switch rule.Scope {
	case "global":
		return true
	case "virtual_key":
		return rule.ScopeID != nil && *rule.ScopeID == vkID
	case "team":
		return rule.ScopeID != nil && *rule.ScopeID == teamID
	default:
		return false
	}
}

// reloadRules replaces all rules atomically. Called on FullReload.
func (c *rulesCache) reloadRules(rules []*configstoreTables.TableGuardrailRule) {
	newMap := make(map[string]*cachedRule, len(rules))
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		prog, err := compileExpression(c.celEnv, rule.CelExpression)
		if err != nil {
			// Log externally via plugin logger; skip invalid rules
			continue
		}
		newMap[rule.ID] = &cachedRule{rule: rule, program: prog}
	}
	c.mu.Lock()
	c.rules = newMap
	c.mu.Unlock()
}

// reloadProfiles replaces all profiles atomically.
func (c *rulesCache) reloadProfiles(profiles []*configstoreTables.TableGuardrailProfile) {
	newMap := make(map[string]*configstoreTables.TableGuardrailProfile, len(profiles))
	for _, p := range profiles {
		if p.Enabled {
			newMap[p.ID] = p
		}
	}
	c.mu.Lock()
	c.profiles = newMap
	c.mu.Unlock()
}
```

- [ ] **Step 4: Run tests**

```bash
cd plugins/guardrails && go test ./... -run TestRulesCache -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add plugins/guardrails/rules_cache.go plugins/guardrails/rules_cache_test.go
git commit -m "feat(guardrails): in-memory rules cache with CEL pre-compilation + tests"
```

---

### Task 6: Profile Clients

**Files:**
- Create: `plugins/guardrails/providers.go`
- Create: `plugins/guardrails/bedrock.go`
- Create: `plugins/guardrails/azure.go`
- Create: `plugins/guardrails/grayswan.go`
- Create: `plugins/guardrails/patronus.go`
- Create: `plugins/guardrails/model_armor.go`
- Create: `plugins/guardrails/providers_test.go`

- [ ] **Step 1: Write failing test**

```go
// plugins/guardrails/providers_test.go
package guardrails

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBedrockClient_Blocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"action": "BLOCKED",
			"outputs": []map[string]interface{}{
				{"text": "Content blocked by guardrail"},
			},
		})
	}))
	defer srv.Close()

	client := &bedrockClient{
		endpoint:    srv.URL,
		guardrailID: "test-id",
		version:     "DRAFT",
		httpClient:  srv.Client(),
	}

	violated, reason, err := client.Evaluate(context.Background(), "how to make explosives")
	require.NoError(t, err)
	assert.True(t, violated)
	assert.NotEmpty(t, reason)
}

func TestBedrockClient_NotBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"action": "NONE",
			"outputs": []map[string]interface{}{
				{"text": "Hello world"},
			},
		})
	}))
	defer srv.Close()

	client := &bedrockClient{
		endpoint:    srv.URL,
		guardrailID: "test-id",
		version:     "DRAFT",
		httpClient:  srv.Client(),
	}

	violated, _, err := client.Evaluate(context.Background(), "hello world")
	require.NoError(t, err)
	assert.False(t, violated)
}

func TestBedrockClient_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := &bedrockClient{
		endpoint:    srv.URL,
		guardrailID: "test-id",
		version:     "DRAFT",
		httpClient:  srv.Client(),
	}

	_, _, err := client.Evaluate(context.Background(), "test")
	assert.Error(t, err)
}

func TestBedrockClient_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{not valid json`))
	}))
	defer srv.Close()

	client := &bedrockClient{
		endpoint:    srv.URL,
		guardrailID: "test-id",
		version:     "DRAFT",
		httpClient:  srv.Client(),
	}

	_, _, err := client.Evaluate(context.Background(), "test")
	assert.Error(t, err)
}

func TestAzureClient_Blocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"categoriesAnalysis": []map[string]interface{}{
				{"category": "Violence", "severity": 6},
			},
		})
	}))
	defer srv.Close()

	client := &azureClient{
		endpoint:         srv.URL,
		apiKey:           "test-key",
		severityThreshold: 4,
		httpClient:       srv.Client(),
	}

	violated, reason, err := client.Evaluate(context.Background(), "violent content")
	require.NoError(t, err)
	assert.True(t, violated)
	assert.NotEmpty(t, reason)
}

func TestAzureClient_BelowThreshold(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"categoriesAnalysis": []map[string]interface{}{
				{"category": "Violence", "severity": 2},
			},
		})
	}))
	defer srv.Close()

	client := &azureClient{
		endpoint:         srv.URL,
		apiKey:           "test-key",
		severityThreshold: 4,
		httpClient:       srv.Client(),
	}

	violated, _, err := client.Evaluate(context.Background(), "mild content")
	require.NoError(t, err)
	assert.False(t, violated)
}
```

- [ ] **Step 2: Run test — expect compile failure**

```bash
cd plugins/guardrails && go test ./... -run TestBedrockClient 2>&1 | head -10
```

- [ ] **Step 3: Write `providers.go`**

Uses a registry pattern so future providers can be added without touching this file.

```go
// plugins/guardrails/providers.go
package guardrails

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
)

// ProfileClient evaluates content against an external safety provider.
type ProfileClient interface {
	// Evaluate checks the content. Returns (violated, reason, err).
	// err is non-nil on transport/parse failures (caller applies FailOpen logic).
	Evaluate(ctx context.Context, content string) (violated bool, reason string, err error)
}

// ProviderFactory constructs a ProfileClient from a decoded ConfigJSON map.
type ProviderFactory func(cfg map[string]interface{}, hc *http.Client) (ProfileClient, error)

var (
	registryMu      sync.RWMutex
	providerRegistry = map[string]ProviderFactory{}
)

// RegisterProvider registers a factory under the given provider name.
// Call this in an init() function to add new providers without modifying this file.
func RegisterProvider(name string, factory ProviderFactory) {
	registryMu.Lock()
	providerRegistry[name] = factory
	registryMu.Unlock()
}

// init registers all built-in providers.
func init() {
	RegisterProvider("bedrock",     func(cfg map[string]interface{}, hc *http.Client) (ProfileClient, error) { return newBedrockClient(cfg, hc) })
	RegisterProvider("azure",       func(cfg map[string]interface{}, hc *http.Client) (ProfileClient, error) { return newAzureClient(cfg, hc) })
	RegisterProvider("grayswan",    func(cfg map[string]interface{}, hc *http.Client) (ProfileClient, error) { return newGraySwanClient(cfg, hc) })
	RegisterProvider("patronus_ai", func(cfg map[string]interface{}, hc *http.Client) (ProfileClient, error) { return newPatronusClient(cfg, hc) })
	RegisterProvider("model_armor", func(cfg map[string]interface{}, hc *http.Client) (ProfileClient, error) { return newModelArmorClient(cfg, hc) })
}

// newProfileClient builds the appropriate ProfileClient from a DB profile row.
func newProfileClient(profile *configstoreTables.TableGuardrailProfile) (ProfileClient, error) {
	var cfg map[string]interface{}
	if err := json.Unmarshal([]byte(profile.ConfigJSON), &cfg); err != nil {
		return nil, fmt.Errorf("invalid ConfigJSON for profile %q: %w", profile.ID, err)
	}
	registryMu.RLock()
	factory, ok := providerRegistry[profile.ProviderName]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown guardrail provider: %q (register it with RegisterProvider)", profile.ProviderName)
	}
	return factory(cfg, &http.Client{})
}

// strField extracts a required string field from a config map.
func strField(cfg map[string]interface{}, key string) (string, error) {
	v, ok := cfg[key]
	if !ok {
		return "", fmt.Errorf("missing required field %q", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("field %q must be a string", key)
	}
	return s, nil
}

// intFieldOr extracts an int field or returns the default.
func intFieldOr(cfg map[string]interface{}, key string, defaultVal int) int {
	v, ok := cfg[key]
	if !ok {
		return defaultVal
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return defaultVal
}
```

- [ ] **Step 4: Write `bedrock.go`**

```go
// plugins/guardrails/bedrock.go
package guardrails

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// bedrockClient calls the AWS Bedrock ApplyGuardrail API.
// ConfigJSON fields: endpoint (string), guardrail_id (string), version (string, default "DRAFT").
type bedrockClient struct {
	endpoint    string
	guardrailID string
	version     string
	httpClient  *http.Client
}

func newBedrockClient(cfg map[string]interface{}, hc *http.Client) (*bedrockClient, error) {
	endpoint, err := strField(cfg, "endpoint")
	if err != nil {
		return nil, err
	}
	guardrailID, err := strField(cfg, "guardrail_id")
	if err != nil {
		return nil, err
	}
	version, _ := cfg["version"].(string)
	if version == "" {
		version = "DRAFT"
	}
	return &bedrockClient{endpoint: endpoint, guardrailID: guardrailID, version: version, httpClient: hc}, nil
}

func (c *bedrockClient) Evaluate(ctx context.Context, content string) (bool, string, error) {
	payload := map[string]interface{}{
		"source": "INPUT",
		"content": []map[string]interface{}{
			{"text": map[string]interface{}{"text": content}},
		},
	}
	body, _ := json.Marshal(payload)

	url := fmt.Sprintf("%s/guardrails/%s/apply?guardrailVersion=%s", c.endpoint, c.guardrailID, c.version)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return false, "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, "", fmt.Errorf("bedrock request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, "", fmt.Errorf("bedrock returned HTTP %d", resp.StatusCode)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, "", err
	}

	var result struct {
		Action  string `json:"action"`
		Outputs []struct {
			Text string `json:"text"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return false, "", fmt.Errorf("bedrock response parse error: %w", err)
	}

	if result.Action == "BLOCKED" {
		reason := "blocked by Bedrock guardrail"
		if len(result.Outputs) > 0 {
			reason = result.Outputs[0].Text
		}
		return true, reason, nil
	}
	return false, "", nil
}
```

- [ ] **Step 5: Write `azure.go`**

```go
// plugins/guardrails/azure.go
package guardrails

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// azureClient calls Azure Content Safety analyzeText API.
// ConfigJSON fields: endpoint (string), api_key (string), severity_threshold (int, default 4).
type azureClient struct {
	endpoint          string
	apiKey            string
	severityThreshold int
	httpClient        *http.Client
}

func newAzureClient(cfg map[string]interface{}, hc *http.Client) (*azureClient, error) {
	endpoint, err := strField(cfg, "endpoint")
	if err != nil {
		return nil, err
	}
	apiKey, err := strField(cfg, "api_key")
	if err != nil {
		return nil, err
	}
	threshold := intFieldOr(cfg, "severity_threshold", 4)
	return &azureClient{endpoint: endpoint, apiKey: apiKey, severityThreshold: threshold, httpClient: hc}, nil
}

func (c *azureClient) Evaluate(ctx context.Context, content string) (bool, string, error) {
	payload := map[string]interface{}{
		"text": content,
		"categories": []string{"Hate", "Violence", "Sexual", "SelfHarm"},
	}
	body, _ := json.Marshal(payload)

	url := fmt.Sprintf("%s/contentsafety/text:analyze?api-version=2023-10-01", c.endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return false, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Ocp-Apim-Subscription-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, "", fmt.Errorf("azure request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, "", fmt.Errorf("azure returned HTTP %d", resp.StatusCode)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, "", err
	}

	var result struct {
		CategoriesAnalysis []struct {
			Category string `json:"category"`
			Severity int    `json:"severity"`
		} `json:"categoriesAnalysis"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return false, "", fmt.Errorf("azure response parse error: %w", err)
	}

	for _, cat := range result.CategoriesAnalysis {
		if cat.Severity >= c.severityThreshold {
			return true, fmt.Sprintf("Azure Content Safety: %s severity %d ≥ threshold %d",
				cat.Category, cat.Severity, c.severityThreshold), nil
		}
	}
	return false, "", nil
}
```

- [ ] **Step 6: Write `grayswan.go`**

```go
// plugins/guardrails/grayswan.go
package guardrails

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// graySwanClient calls the GraySwan safety API.
// ConfigJSON fields: endpoint (string), api_key (string), score_threshold (float, default 0.5).
type graySwanClient struct {
	endpoint       string
	apiKey         string
	scoreThreshold float64
	httpClient     *http.Client
}

func newGraySwanClient(cfg map[string]interface{}, hc *http.Client) (*graySwanClient, error) {
	endpoint, err := strField(cfg, "endpoint")
	if err != nil {
		return nil, err
	}
	apiKey, err := strField(cfg, "api_key")
	if err != nil {
		return nil, err
	}
	threshold := 0.5
	if v, ok := cfg["score_threshold"].(float64); ok {
		threshold = v
	}
	return &graySwanClient{endpoint: endpoint, apiKey: apiKey, scoreThreshold: threshold, httpClient: hc}, nil
}

func (c *graySwanClient) Evaluate(ctx context.Context, content string) (bool, string, error) {
	payload := map[string]interface{}{"text": content}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/evaluate", bytes.NewReader(body))
	if err != nil {
		return false, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, "", fmt.Errorf("grayswan request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, "", fmt.Errorf("grayswan returned HTTP %d", resp.StatusCode)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, "", err
	}

	var result struct {
		Score   float64 `json:"score"`
		Reason  string  `json:"reason"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return false, "", fmt.Errorf("grayswan response parse error: %w", err)
	}

	if result.Score >= c.scoreThreshold {
		return true, fmt.Sprintf("GraySwan violation score %.2f ≥ threshold %.2f: %s",
			result.Score, c.scoreThreshold, result.Reason), nil
	}
	return false, "", nil
}
```

- [ ] **Step 7: Write `patronus.go`**

```go
// plugins/guardrails/patronus.go
package guardrails

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// patronusClient calls the Patronus AI evaluation API.
// ConfigJSON fields: endpoint (string), api_key (string), evaluator (string, default "lynx").
type patronusClient struct {
	endpoint   string
	apiKey     string
	evaluator  string
	httpClient *http.Client
}

func newPatronusClient(cfg map[string]interface{}, hc *http.Client) (*patronusClient, error) {
	endpoint, err := strField(cfg, "endpoint")
	if err != nil {
		return nil, err
	}
	apiKey, err := strField(cfg, "api_key")
	if err != nil {
		return nil, err
	}
	evaluator, _ := cfg["evaluator"].(string)
	if evaluator == "" {
		evaluator = "lynx"
	}
	return &patronusClient{endpoint: endpoint, apiKey: apiKey, evaluator: evaluator, httpClient: hc}, nil
}

func (c *patronusClient) Evaluate(ctx context.Context, content string) (bool, string, error) {
	payload := map[string]interface{}{
		"evaluators": []map[string]interface{}{{"evaluator": c.evaluator}},
		"evaluated_model_output": content,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/v1/evaluate", bytes.NewReader(body))
	if err != nil {
		return false, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, "", fmt.Errorf("patronus request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, "", fmt.Errorf("patronus returned HTTP %d", resp.StatusCode)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, "", err
	}

	var result struct {
		Results []struct {
			Pass   bool   `json:"pass"`
			Reason string `json:"reason"`
		} `json:"results"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return false, "", fmt.Errorf("patronus response parse error: %w", err)
	}

	for _, r := range result.Results {
		if !r.Pass {
			return true, fmt.Sprintf("Patronus AI evaluation failed: %s", r.Reason), nil
		}
	}
	return false, "", nil
}
```

- [ ] **Step 8: Write `model_armor.go`**

Google Cloud Model Armor client. Uses REST API (no GCP SDK dependency — avoids adding heavy transitive deps).

ConfigJSON fields: `project_id` (string), `location` (string, e.g. `"us-central1"`), `template_id` (string), and optionally `credentials_json` (base64-encoded service account key JSON; omit to use Application Default Credentials).

```go
// plugins/guardrails/model_armor.go
package guardrails

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const modelArmorScope = "https://www.googleapis.com/auth/cloud-platform"

// modelArmorClient calls the Google Cloud Model Armor sanitize API.
// Endpoint: https://modelarmor.{location}.rep.googleapis.com/v1/projects/{project_id}/locations/{location}/templates/{template_id}:sanitizeUserPrompt
// Violation: sanitizationResult.filterMatchState == "MATCH_FOUND"
type modelArmorClient struct {
	projectID  string
	location   string
	templateID string
	httpClient *http.Client // oauth2-wrapped transport
}

func newModelArmorClient(cfg map[string]interface{}, _ *http.Client) (*modelArmorClient, error) {
	projectID, err := strField(cfg, "project_id")
	if err != nil {
		return nil, err
	}
	location, err := strField(cfg, "location")
	if err != nil {
		return nil, err
	}
	templateID, err := strField(cfg, "template_id")
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	var ts oauth2.TokenSource
	if credsB64, ok := cfg["credentials_json"].(string); ok && credsB64 != "" {
		credsJSON, err := base64.StdEncoding.DecodeString(credsB64)
		if err != nil {
			return nil, fmt.Errorf("model_armor: credentials_json base64 decode failed: %w", err)
		}
		creds, err := google.CredentialsFromJSON(ctx, credsJSON, modelArmorScope)
		if err != nil {
			return nil, fmt.Errorf("model_armor: credentials_json parse failed: %w", err)
		}
		ts = creds.TokenSource
	} else {
		// Application Default Credentials
		ts, err = google.DefaultTokenSource(ctx, modelArmorScope)
		if err != nil {
			return nil, fmt.Errorf("model_armor: ADC token source failed: %w", err)
		}
	}

	return &modelArmorClient{
		projectID:  projectID,
		location:   location,
		templateID: templateID,
		httpClient: oauth2.NewClient(ctx, ts),
	}, nil
}

func (c *modelArmorClient) Evaluate(ctx context.Context, content string) (bool, string, error) {
	return c.sanitize(ctx, content, "sanitizeUserPrompt")
}

// EvaluateResponse sanitizes a model response (used for output rules).
func (c *modelArmorClient) EvaluateResponse(ctx context.Context, content string) (bool, string, error) {
	return c.sanitize(ctx, content, "sanitizeModelResponse")
}

func (c *modelArmorClient) sanitize(ctx context.Context, content, method string) (bool, string, error) {
	// Request body differs by method
	var payload map[string]interface{}
	if method == "sanitizeUserPrompt" {
		payload = map[string]interface{}{
			"userPromptData": map[string]interface{}{"text": content},
		}
	} else {
		payload = map[string]interface{}{
			"modelResponseData": map[string]interface{}{"text": content},
		}
	}

	body, _ := json.Marshal(payload)
	url := fmt.Sprintf(
		"https://modelarmor.%s.rep.googleapis.com/v1/projects/%s/locations/%s/templates/%s:%s",
		c.location, c.projectID, c.location, c.templateID, method,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return false, "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, "", fmt.Errorf("model_armor request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return false, "", fmt.Errorf("model_armor returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, "", err
	}

	var result struct {
		SanitizationResult struct {
			FilterMatchState string `json:"filterMatchState"`
			FilterResults    map[string]json.RawMessage `json:"filterResults"`
		} `json:"sanitizationResult"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return false, "", fmt.Errorf("model_armor response parse error: %w", err)
	}

	if result.SanitizationResult.FilterMatchState == "MATCH_FOUND" {
		// Identify which filters matched for the reason string
		var matched []string
		for filterName := range result.SanitizationResult.FilterResults {
			matched = append(matched, filterName)
		}
		return true, fmt.Sprintf("Google Cloud Model Armor violation — filters: %v", matched), nil
	}
	return false, "", nil
}
```

Add `golang.org/x/oauth2` to `go.mod`:
```bash
cd plugins/guardrails && go get golang.org/x/oauth2 && go mod tidy
```

- [ ] **Step 9: Add Model Armor test**

```go
// In providers_test.go — add these cases

func TestModelArmorClient_Blocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"sanitizationResult": map[string]interface{}{
				"filterMatchState": "MATCH_FOUND",
				"invocationResult": "SUCCESS",
				"filterResults": map[string]interface{}{
					"rai": map[string]interface{}{},
				},
			},
		})
	}))
	defer srv.Close()

	// Build client with a pre-configured HTTP client that points at our test server.
	// We inject the httpClient directly instead of going through newModelArmorClient
	// (which would try to fetch GCP credentials).
	client := &modelArmorClient{
		projectID:  "proj",
		location:   "us-central1",
		templateID: "tpl",
		httpClient: &http.Client{Transport: &prefixRoundTripper{base: srv.URL, inner: http.DefaultTransport}},
	}

	violated, reason, err := client.Evaluate(context.Background(), "dangerous content")
	require.NoError(t, err)
	assert.True(t, violated)
	assert.Contains(t, reason, "Model Armor")
}

func TestModelArmorClient_NoMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"sanitizationResult": map[string]interface{}{
				"filterMatchState": "NO_MATCH_FOUND",
				"invocationResult": "SUCCESS",
			},
		})
	}))
	defer srv.Close()

	client := &modelArmorClient{
		projectID: "proj", location: "us-central1", templateID: "tpl",
		httpClient: &http.Client{Transport: &prefixRoundTripper{base: srv.URL, inner: http.DefaultTransport}},
	}

	violated, _, err := client.Evaluate(context.Background(), "safe content")
	require.NoError(t, err)
	assert.False(t, violated)
}

func TestModelArmorClient_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{not valid`))
	}))
	defer srv.Close()

	client := &modelArmorClient{
		projectID: "proj", location: "us-central1", templateID: "tpl",
		httpClient: &http.Client{Transport: &prefixRoundTripper{base: srv.URL, inner: http.DefaultTransport}},
	}

	_, _, err := client.Evaluate(context.Background(), "test")
	assert.Error(t, err)
}

// prefixRoundTripper rewrites the host of every request to point at the test server.
type prefixRoundTripper struct {
	base  string
	inner http.RoundTripper
}

func (p *prefixRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req2 := req.Clone(req.Context())
	req2.URL.Scheme = "http"
	req2.URL.Host = req.URL.Host // keep path/query; Host is set by the URL itself
	// Replace the entire URL host+scheme with the test server base
	newURL := p.base + req.URL.Path
	if req.URL.RawQuery != "" {
		newURL += "?" + req.URL.RawQuery
	}
	var err error
	req2.URL, err = req2.URL.Parse(newURL)
	if err != nil {
		return nil, err
	}
	return p.inner.RoundTrip(req2)
}
```

- [ ] **Step 10: Run all provider tests**

```bash
cd plugins/guardrails && go test ./... -run "TestBedrockClient|TestAzureClient|TestModelArmor" -v
```

Expected: all PASS.

- [ ] **Step 11: Commit**
git add plugins/guardrails/providers.go plugins/guardrails/bedrock.go \
        plugins/guardrails/azure.go plugins/guardrails/grayswan.go \
        plugins/guardrails/patronus.go plugins/guardrails/model_armor.go \
        plugins/guardrails/providers_test.go plugins/guardrails/go.mod plugins/guardrails/go.sum
git commit -m "feat(guardrails): registry-pattern ProfileClient + Bedrock/Azure/GraySwan/Patronus/ModelArmor + tests"

---

### Task 7: Plugin Main + Hooks

**Files:**
- Create: `plugins/guardrails/main.go`
- Create: `plugins/guardrails/hooks.go`
- Create: `plugins/guardrails/hooks_test.go`

**Design (must implement):**

- **Output CEL:** `guardrailRequestMessagesKey` + optional `guardrailRequestModelKey` — see **Task 7 refinements** in the preamble above.
- **Profiles timeout:** Cap per-rule timeout by remaining `ctx.Deadline()` inside **`evaluateProfiles`** — see preamble.

- [ ] **Step 1: Write failing hooks test**

```go
// plugins/guardrails/hooks_test.go
package guardrails

import (
	"context"
	"testing"
	"time"
	"fmt"

	"github.com/google/uuid"
	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockProfileClient is a test double that returns a configurable result.
type mockProfileClient struct {
	violated bool
	reason   string
	err      error
}

func (m *mockProfileClient) Evaluate(_ context.Context, _ string) (bool, string, error) {
	return m.violated, m.reason, m.err
}

func newTestPlugin(t *testing.T) *GuardrailsPlugin {
	t.Helper()
	env, err := newCELEnv()
	require.NoError(t, err)
	p := &GuardrailsPlugin{
		cache:   newRulesCache(env),
		clients: make(map[string]ProfileClient),
		logger:  &noopLogger{},
	}
	return p
}

type noopLogger struct{}
func (n *noopLogger) Debug(format string, args ...interface{}) {}
func (n *noopLogger) Info(format string, args ...interface{})  {}
func (n *noopLogger) Warn(format string, args ...interface{})  {}
func (n *noopLogger) Error(format string, args ...interface{}) {}

func makeBifrostContext() *schemas.BifrostContext {
	return schemas.NewBifrostContext(context.Background(), nil)
}

func makeBifrostRequest(model, content string) *schemas.BifrostRequest {
	return &schemas.BifrostRequest{
		BifrostParams: schemas.BifrostParams{Model: model},
		Input: schemas.Input{
			ChatCompletionInput: &schemas.ChatCompletionInput{
				Messages: []schemas.Message{
					{Role: schemas.RoleUser, Content: &schemas.Content{Text: bifrost.Ptr(content)}},
				},
			},
		},
	}
}

func TestHooks_CELOnlyBlock(t *testing.T) {
	p := newTestPlugin(t)
	rule := &configstoreTables.TableGuardrailRule{
		ID: uuid.New().String(), Name: "block-bomb", Enabled: true,
		CelExpression: `request.messages.exists(m, m.content.contains("bomb"))`,
		ApplyTo: "input", Action: "block", SamplingRate: 100,
		TimeoutMs: 5000, Scope: "global", FailOpen: true,
		BlockMessage: "Content blocked", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, p.cache.upsertRule(rule))

	ctx := makeBifrostContext()
	req := makeBifrostRequest("gpt-4o", "how to make a bomb")
	_, sc, err := p.PreLLMHook(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, sc)
	require.NotNil(t, sc.Error)
	assert.Equal(t, 446, *sc.Error.StatusCode)
}

func TestHooks_CELFalseAllowsRequest(t *testing.T) {
	p := newTestPlugin(t)
	rule := &configstoreTables.TableGuardrailRule{
		ID: uuid.New().String(), Name: "block-bomb", Enabled: true,
		CelExpression: `request.messages.exists(m, m.content.contains("bomb"))`,
		ApplyTo: "input", Action: "block", SamplingRate: 100,
		TimeoutMs: 5000, Scope: "global", FailOpen: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, p.cache.upsertRule(rule))

	ctx := makeBifrostContext()
	req := makeBifrostRequest("gpt-4o", "hello world")
	got, sc, err := p.PreLLMHook(ctx, req)
	require.NoError(t, err)
	assert.Nil(t, sc)
	assert.Equal(t, req, got)
}

func TestHooks_WarnSetsContextAndDoesNotBlock(t *testing.T) {
	p := newTestPlugin(t)
	rule := &configstoreTables.TableGuardrailRule{
		ID: uuid.New().String(), Name: "warn-rule", Enabled: true,
		CelExpression: "true",
		ApplyTo: "input", Action: "warn", SamplingRate: 100,
		TimeoutMs: 5000, Scope: "global", FailOpen: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, p.cache.upsertRule(rule))

	ctx := makeBifrostContext()
	req := makeBifrostRequest("gpt-4o", "hello")
	_, sc, err := p.PreLLMHook(ctx, req)
	require.NoError(t, err)
	assert.Nil(t, sc) // warn does not block

	warned, _ := ctx.Get(guardrailWarnedKey)
	assert.Equal(t, true, warned)
}

func TestHooks_WarnSetsHTTP246(t *testing.T) {
	p := newTestPlugin(t)
	ctx := makeBifrostContext()
	ctx.SetValue(guardrailWarnedKey, true)

	httpResp := &schemas.HTTPResponse{StatusCode: 200}
	err := p.HTTPTransportPostHook(ctx, nil, httpResp)
	require.NoError(t, err)
	assert.Equal(t, 246, httpResp.StatusCode)
}

func TestHooks_ProfileViolationBlocks(t *testing.T) {
	p := newTestPlugin(t)
	profileID := uuid.New().String()
	p.clients[profileID] = &mockProfileClient{violated: true, reason: "policy violation"}

	profile := &configstoreTables.TableGuardrailProfile{
		ID: profileID, Name: "mock-profile", ProviderName: "bedrock", Enabled: true,
		ConfigJSON: "{}", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	p.cache.upsertProfile(profile)

	rule := &configstoreTables.TableGuardrailRule{
		ID: uuid.New().String(), Name: "profile-rule", Enabled: true,
		CelExpression: "true", Profiles: []configstoreTables.TableGuardrailProfile{*profile},
		ApplyTo: "input", Action: "block", SamplingRate: 100,
		TimeoutMs: 5000, Scope: "global", FailOpen: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, p.cache.upsertRule(rule))

	ctx := makeBifrostContext()
	req := makeBifrostRequest("gpt-4o", "test content")
	_, sc, err := p.PreLLMHook(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, sc)
	assert.Equal(t, 446, *sc.Error.StatusCode)
}

func TestHooks_ProfileErrorFailOpen(t *testing.T) {
	p := newTestPlugin(t)
	profileID := uuid.New().String()
	p.clients[profileID] = &mockProfileClient{err: fmt.Errorf("timeout")}

	profile := &configstoreTables.TableGuardrailProfile{
		ID: profileID, Name: "mock-profile", ProviderName: "bedrock", Enabled: true,
		ConfigJSON: "{}", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	p.cache.upsertProfile(profile)

	rule := &configstoreTables.TableGuardrailRule{
		ID: uuid.New().String(), Name: "fail-open-rule", Enabled: true,
		CelExpression: "true", Profiles: []configstoreTables.TableGuardrailProfile{*profile},
		ApplyTo: "input", Action: "block", SamplingRate: 100,
		TimeoutMs: 5000, Scope: "global", FailOpen: true, // true = pass on error
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, p.cache.upsertRule(rule))

	ctx := makeBifrostContext()
	req := makeBifrostRequest("gpt-4o", "test")
	_, sc, err := p.PreLLMHook(ctx, req)
	require.NoError(t, err)
	assert.Nil(t, sc) // fail-open → request passes
}

func TestHooks_ProfileErrorFailClosed(t *testing.T) {
	p := newTestPlugin(t)
	profileID := uuid.New().String()
	p.clients[profileID] = &mockProfileClient{err: fmt.Errorf("timeout")}

	profile := &configstoreTables.TableGuardrailProfile{
		ID: profileID, Name: "mock-profile", ProviderName: "bedrock", Enabled: true,
		ConfigJSON: "{}", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	p.cache.upsertProfile(profile)

	rule := &configstoreTables.TableGuardrailRule{
		ID: uuid.New().String(), Name: "fail-closed-rule", Enabled: true,
		CelExpression: "true", Profiles: []configstoreTables.TableGuardrailProfile{*profile},
		ApplyTo: "input", Action: "block", SamplingRate: 100,
		TimeoutMs: 5000, Scope: "global", FailOpen: false, // false = block on error
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, p.cache.upsertRule(rule))

	ctx := makeBifrostContext()
	req := makeBifrostRequest("gpt-4o", "test")
	_, sc, err := p.PreLLMHook(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, sc) // fail-closed → blocked
	assert.Equal(t, 446, *sc.Error.StatusCode)
}

func TestHooks_InputRuleNotEvaluatedInPostHook(t *testing.T) {
	p := newTestPlugin(t)
	rule := &configstoreTables.TableGuardrailRule{
		ID: uuid.New().String(), Name: "input-only", Enabled: true,
		CelExpression: "true", ApplyTo: "input", Action: "block",
		SamplingRate: 100, TimeoutMs: 5000, Scope: "global", FailOpen: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, p.cache.upsertRule(rule))

	ctx := makeBifrostContext()
	resp := &schemas.BifrostResponse{}
	gotResp, bifrostErr, err := p.PostLLMHook(ctx, resp, nil)
	require.NoError(t, err)
	assert.Nil(t, bifrostErr)
	assert.Equal(t, resp, gotResp)
}
```

- [ ] **Step 2: Run test — expect compile failure**

```bash
cd plugins/guardrails && go test ./... -run TestHooks 2>&1 | head -10
```

- [ ] **Step 3: Write `main.go`**

```go
// plugins/guardrails/main.go
package guardrails

import (
	"context"
	"fmt"

	"github.com/google/cel-go/cel"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
)

const PluginName = "guardrails"

// Typed context keys
const (
	guardrailWarnedKey          schemas.BifrostContextKey = "bf-guardrail-warned"
	guardrailInputProfilesKey   schemas.BifrostContextKey = "bf-guardrail-input-profiles"
	guardrailOutputProfilesKey  schemas.BifrostContextKey = "bf-guardrail-output-profiles"
	guardrailRequestMessagesKey schemas.BifrostContextKey = "bf-guardrail-req-messages"
	guardrailRequestModelKey    schemas.BifrostContextKey = "bf-guardrail-req-model"
)

// GuardrailsPlugin enforces content-safety rules via CEL + optional external providers.
type GuardrailsPlugin struct {
	cache       *rulesCache
	clients     map[string]ProfileClient // profileID → client
	celEnv      *cel.Env
	configStore configstore.ConfigStore
	logger      schemas.Logger
}

// Init creates and initializes the GuardrailsPlugin.
// Loads all rules and profiles from the config store and builds profile clients.
func Init(ctx context.Context, cs configstore.ConfigStore, logger schemas.Logger) (*GuardrailsPlugin, error) {
	env, err := newCELEnv()
	if err != nil {
		return nil, fmt.Errorf("guardrails: CEL env init failed: %w", err)
	}

	p := &GuardrailsPlugin{
		cache:       newRulesCache(env),
		clients:     make(map[string]ProfileClient),
		celEnv:      env,
		configStore: cs,
		logger:      logger,
	}

	// Load profiles first (rules reference them)
	profiles, err := cs.GetGuardrailProfiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("guardrails: load profiles: %w", err)
	}
	p.ReloadProfiles(profiles)

	// Load rules
	rules, err := cs.GetGuardrailRules(ctx)
	if err != nil {
		return nil, fmt.Errorf("guardrails: load rules: %w", err)
	}
	p.ReloadRules(rules)

	return p, nil
}

func (p *GuardrailsPlugin) GetName() string { return PluginName }

func (p *GuardrailsPlugin) Cleanup() error { return nil }

// UpsertRule adds or updates a single rule in the cache (called on config sync events).
func (p *GuardrailsPlugin) UpsertRule(rule *configstoreTables.TableGuardrailRule) {
	if err := p.cache.upsertRule(rule); err != nil {
		p.logger.Warn("[guardrails] rule %q CEL compile failed, rule disabled: %v", rule.ID, err)
	}
}

// DeleteRule removes a rule from the cache.
func (p *GuardrailsPlugin) DeleteRule(id string) {
	p.cache.deleteRule(id)
}

// UpsertProfile adds or updates a profile and its client.
func (p *GuardrailsPlugin) UpsertProfile(profile *configstoreTables.TableGuardrailProfile) {
	p.cache.upsertProfile(profile)
	if profile.Enabled {
		client, err := newProfileClient(profile)
		if err != nil {
			p.logger.Warn("[guardrails] profile %q client build failed: %v", profile.ID, err)
			return
		}
		p.clients[profile.ID] = client
	} else {
		delete(p.clients, profile.ID)
	}
}

// DeleteProfile removes a profile from the cache and client map.
func (p *GuardrailsPlugin) DeleteProfile(id string) {
	p.cache.deleteProfile(id)
	delete(p.clients, id)
}

// ReloadRules replaces all rules atomically (called on FullReload).
func (p *GuardrailsPlugin) ReloadRules(rules []*configstoreTables.TableGuardrailRule) {
	p.cache.reloadRules(rules)
}

// ReloadProfiles replaces all profiles and rebuilds all clients atomically.
func (p *GuardrailsPlugin) ReloadProfiles(profiles []*configstoreTables.TableGuardrailProfile) {
	p.cache.reloadProfiles(profiles)
	newClients := make(map[string]ProfileClient, len(profiles))
	for _, prof := range profiles {
		if !prof.Enabled {
			continue
		}
		client, err := newProfileClient(prof)
		if err != nil {
			p.logger.Warn("[guardrails] profile %q client build failed: %v", prof.ID, err)
			continue
		}
		newClients[prof.ID] = client
	}
	p.clients = newClients
}

// getVKAndTeamFromContext extracts virtual key ID and team ID from BifrostContext.
func getVKAndTeamFromContext(ctx *schemas.BifrostContext) (vkID, teamID string) {
	if v, ok := ctx.Get("bifrost-governance-virtual-key-id"); ok {
		vkID, _ = v.(string)
	}
	if v, ok := ctx.Get("bifrost-governance-team-id"); ok {
		teamID, _ = v.(string)
	}
	return
}
```

- [ ] **Step 4: Write `hooks.go`**

```go
// plugins/guardrails/hooks.go
package guardrails

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
)

// --- HTTPTransportPreHook ---
// Parses per-request guardrail attachment from body or header.

func (p *GuardrailsPlugin) HTTPTransportPreHook(ctx *schemas.BifrostContext, req *schemas.HTTPRequest) (*schemas.HTTPResponse, error) {
	// x-bf-guardrail-ids header: comma-separated profile IDs applied to both input and output
	if header := req.CaseInsensitiveHeaderLookup("x-bf-guardrail-ids"); header != "" {
		ids := splitTrim(header, ",")
		ctx.SetValue(guardrailInputProfilesKey, ids)
		ctx.SetValue(guardrailOutputProfilesKey, ids)
	}
	// bifrost_config.guardrails parsed from body is handled by the SDK integration layer
	// and stored under guardrailInputProfilesKey / guardrailOutputProfilesKey by convention.
	// No additional parsing needed here.
	return nil, nil
}

func (p *GuardrailsPlugin) HTTPTransportPostHook(ctx *schemas.BifrostContext, req *schemas.HTTPRequest, resp *schemas.HTTPResponse) error {
	if warned, ok := ctx.Get(guardrailWarnedKey); ok && warned == true {
		resp.StatusCode = 246
	}
	return nil
}

func (p *GuardrailsPlugin) HTTPTransportStreamChunkHook(ctx *schemas.BifrostContext, req *schemas.HTTPRequest, chunk *schemas.BifrostStreamChunk) (*schemas.BifrostStreamChunk, error) {
	return chunk, nil
}

// --- PreLLMHook ---

func (p *GuardrailsPlugin) PreLLMHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	vkID, teamID := getVKAndTeamFromContext(ctx)
	rules := p.cache.getInputRules(vkID, teamID)

	messages := extractMessages(req)
	ctx.SetValue(guardrailRequestMessagesKey, messages)
	ctx.SetValue(guardrailRequestModelKey, req.BifrostParams.Model) // or modelFromRequest(req); see implementation

	celVars := buildInputCELVars(req, messages)

	for _, cr := range rules {
		sc, err := p.evaluateRule(ctx, cr, celVars, extractInputContent(req))
		if err != nil {
			p.logger.Warn("[guardrails] rule %q eval error: %v", cr.rule.ID, err)
			continue
		}
		if sc != nil {
			return req, sc, nil
		}
	}
	return req, nil, nil
}

// --- PostLLMHook ---

func (p *GuardrailsPlugin) PostLLMHook(ctx *schemas.BifrostContext, resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	vkID, teamID := getVKAndTeamFromContext(ctx)
	rules := p.cache.getOutputRules(vkID, teamID)
	if len(rules) == 0 || resp == nil {
		return resp, bifrostErr, nil
	}

	var reqMessages []interface{}
	if v := ctx.Value(guardrailRequestMessagesKey); v != nil {
		reqMessages, _ = v.([]interface{})
	}

	content, finishReason := extractOutputContent(resp)
	celVars := buildOutputCELVars(ctx, resp, content, finishReason)

	for _, cr := range rules {
		sc, err := p.evaluateRule(ctx, cr, celVars, content)
		if err != nil {
			p.logger.Warn("[guardrails] output rule %q eval error: %v", cr.rule.ID, err)
			continue
		}
		if sc != nil {
			if sc.Error != nil {
				return nil, sc.Error, nil
			}
		}
	}
	return resp, bifrostErr, nil
}

// evaluateRule runs CEL + optional profile evaluation for a single rule.
// Returns non-nil LLMPluginShortCircuit if the rule triggers a block, or sets warn flag.
func (p *GuardrailsPlugin) evaluateRule(ctx *schemas.BifrostContext, cr *cachedRule, celVars map[string]interface{}, content string) (*schemas.LLMPluginShortCircuit, error) {
	rule := cr.rule

	// Sampling check
	if rule.SamplingRate < 100 && rand.IntN(100) >= rule.SamplingRate {
		return nil, nil
	}

	// CEL evaluation
	triggered, err := evalProgram(cr.program, celVars)
	if err != nil {
		return nil, fmt.Errorf("CEL eval: %w", err)
	}
	if !triggered {
		return nil, nil
	}

	// No profiles: CEL condition IS the violation
	if len(rule.Profiles) == 0 {
		return p.applyAction(ctx, rule), nil
	}

	// With profiles: call each enabled profile
	violated, reason := p.evaluateProfiles(ctx, rule, content)
	if violated {
		p.logger.Info("[guardrails] rule %q violated: %s", rule.ID, reason)
		return p.applyAction(ctx, rule), nil
	}
	return nil, nil
}

// evaluateProfiles calls each profile client with a timeout capped by the request deadline.
func (p *GuardrailsPlugin) evaluateProfiles(ctx *schemas.BifrostContext, rule *configstoreTables.TableGuardrailRule, content string) (bool, string) {
	timeout := time.Duration(rule.TimeoutMs) * time.Millisecond
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining < timeout {
			timeout = remaining
		}
	}
	if timeout < 0 {
		timeout = 0
	}

	evalCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for _, profile := range rule.Profiles {
		if !profile.Enabled {
			continue
		}
		client, ok := p.clients[profile.ID]
		if !ok {
			p.logger.Warn("[guardrails] no client for profile %q", profile.ID)
			if !rule.FailOpen {
				return true, "profile client not found (fail-closed)"
			}
			continue
		}

		violated, reason, err := client.Evaluate(evalCtx, content)
		if err != nil {
			p.logger.Warn("[guardrails] profile %q eval error: %v", profile.ID, err)
			if !rule.FailOpen {
				return true, fmt.Sprintf("profile error (fail-closed): %v", err)
			}
			continue
		}
		if violated {
			return true, reason
		}
	}
	return false, ""
}

// applyAction applies the rule action (block returns a ShortCircuit, warn sets context flag).
func (p *GuardrailsPlugin) applyAction(ctx *schemas.BifrostContext, rule *configstoreTables.TableGuardrailRule) *schemas.LLMPluginShortCircuit {
	switch rule.Action {
	case "block":
		msg := rule.BlockMessage
		if msg == "" {
			msg = "Request blocked by guardrail policy"
		}
		return &schemas.LLMPluginShortCircuit{
			Error: &schemas.BifrostError{
				StatusCode:     bifrost.Ptr(446),
				IsBifrostError: true,
				AllowFallbacks: bifrost.Ptr(false),
				Error: &schemas.ErrorField{
					Message: msg,
					Type:    bifrost.Ptr("guardrail_violation"),
				},
			},
		}
	case "warn":
		ctx.SetValue(guardrailWarnedKey, true)
		return nil
	}
	return nil
}

// ---------- CEL variable builders ----------

func buildInputCELVars(req *schemas.BifrostRequest, messages []interface{}) map[string]interface{} {
	return map[string]interface{}{
		"request": map[string]interface{}{
			"messages": messages,
			"model":    req.BifrostParams.Model,
		},
		"output": map[string]interface{}{},
	}
}

func buildOutputCELVars(ctx *schemas.BifrostContext, resp *schemas.BifrostResponse, content, finishReason string) map[string]interface{} {
	var reqMessages interface{} = []interface{}{}
	if v := ctx.Value(guardrailRequestMessagesKey); v != nil {
		if msgs, ok := v.([]interface{}); ok && len(msgs) > 0 {
			reqMessages = msgs
		}
	}
	model := ""
	if v := ctx.Value(guardrailRequestModelKey); v != nil {
		model, _ = v.(string)
	}
	if resp != nil && resp.ChatResponse != nil && resp.ChatResponse.Model != "" {
		model = resp.ChatResponse.Model
	}
	return map[string]interface{}{
		"request": map[string]interface{}{
			"messages": reqMessages,
			"model":    model,
		},
		"output": map[string]interface{}{
			"content":       content,
			"finish_reason": finishReason,
		},
	}
}

func extractMessages(req *schemas.BifrostRequest) []interface{} {
	if req.Input.ChatCompletionInput == nil {
		return nil
	}
	var msgs []interface{}
	for _, m := range req.Input.ChatCompletionInput.Messages {
		content := ""
		if m.Content != nil && m.Content.Text != nil {
			content = *m.Content.Text
		}
		msgs = append(msgs, map[string]interface{}{
			"role":    string(m.Role),
			"content": content,
		})
	}
	return msgs
}

func extractInputContent(req *schemas.BifrostRequest) string {
	if req.Input.ChatCompletionInput == nil {
		return ""
	}
	var parts []string
	for _, m := range req.Input.ChatCompletionInput.Messages {
		if m.Content != nil && m.Content.Text != nil {
			parts = append(parts, *m.Content.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func extractOutputContent(resp *schemas.BifrostResponse) (content, finishReason string) {
	if resp == nil || resp.Choices == nil {
		return "", ""
	}
	for _, choice := range resp.Choices {
		if choice.Message.Content != nil && choice.Message.Content.Text != nil {
			content = *choice.Message.Content.Text
		}
		if choice.FinishReason != nil {
			finishReason = string(*choice.FinishReason)
		}
		break
	}
	return
}

func splitTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			result = append(result, t)
		}
	}
	return result
}
```

---

### Task 8: HTTP Handlers

**Files:**
- Create: `transports/bifrost-http/server/guardrails_handlers.go`
- Modify: `transports/bifrost-http/server/server.go` (register routes)

- [ ] **Step 1: Write `guardrails_handlers.go`**

Follow the routing rules handler pattern (see `transports/bifrost-http/server/routing_rules_handlers.go`).

```go
// transports/bifrost-http/server/guardrails_handlers.go
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	guardrailsplugin "github.com/maximhq/bifrost/plugins/guardrails"
	"github.com/valyala/fasthttp"
)

// --- Rules ---

func (s *BifrostHTTPServer) handleListGuardrailRules(ctx *fasthttp.RequestCtx) {
	rules, err := s.Config.ConfigStore.GetGuardrailRules(context.Background())
	if err != nil {
		writeJSONError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(ctx, http.StatusOK, rules)
}

func (s *BifrostHTTPServer) handleGetGuardrailRule(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	rule, err := s.Config.ConfigStore.GetGuardrailRuleByID(context.Background(), id)
	if err != nil {
		writeJSONError(ctx, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(ctx, http.StatusOK, rule)
}

func (s *BifrostHTTPServer) handleCreateGuardrailRule(ctx *fasthttp.RequestCtx) {
	var rule tables.TableGuardrailRule
	if err := json.Unmarshal(ctx.Request.Body(), &rule); err != nil {
		writeJSONError(ctx, http.StatusBadRequest, err.Error())
		return
	}
	if rule.ID == "" {
		rule.ID = uuid.New().String()
	}
	now := time.Now()
	rule.CreatedAt = now
	rule.UpdatedAt = now

	if err := s.Config.ConfigStore.CreateGuardrailRule(context.Background(), &rule); err != nil {
		writeJSONError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(ctx, http.StatusCreated, rule)
}

func (s *BifrostHTTPServer) handleUpdateGuardrailRule(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	var rule tables.TableGuardrailRule
	if err := json.Unmarshal(ctx.Request.Body(), &rule); err != nil {
		writeJSONError(ctx, http.StatusBadRequest, err.Error())
		return
	}
	rule.ID = id
	rule.UpdatedAt = time.Now()

	if err := s.Config.ConfigStore.UpdateGuardrailRule(context.Background(), &rule); err != nil {
		writeJSONError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(ctx, http.StatusOK, rule)
}

func (s *BifrostHTTPServer) handleDeleteGuardrailRule(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	if err := s.Config.ConfigStore.DeleteGuardrailRule(context.Background(), id); err != nil {
		writeJSONError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	ctx.SetStatusCode(http.StatusNoContent)
}

// --- Profiles ---

func (s *BifrostHTTPServer) handleListGuardrailProfiles(ctx *fasthttp.RequestCtx) {
	profiles, err := s.Config.ConfigStore.GetGuardrailProfiles(context.Background())
	if err != nil {
		writeJSONError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(ctx, http.StatusOK, profiles)
}

func (s *BifrostHTTPServer) handleGetGuardrailProfile(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	profile, err := s.Config.ConfigStore.GetGuardrailProfileByID(context.Background(), id)
	if err != nil {
		writeJSONError(ctx, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(ctx, http.StatusOK, profile)
}

func (s *BifrostHTTPServer) handleCreateGuardrailProfile(ctx *fasthttp.RequestCtx) {
	var profile tables.TableGuardrailProfile
	if err := json.Unmarshal(ctx.Request.Body(), &profile); err != nil {
		writeJSONError(ctx, http.StatusBadRequest, err.Error())
		return
	}
	if profile.ID == "" {
		profile.ID = uuid.New().String()
	}
	now := time.Now()
	profile.CreatedAt = now
	profile.UpdatedAt = now

	if err := s.Config.ConfigStore.CreateGuardrailProfile(context.Background(), &profile); err != nil {
		writeJSONError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(ctx, http.StatusCreated, profile)
}

func (s *BifrostHTTPServer) handleUpdateGuardrailProfile(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	var profile tables.TableGuardrailProfile
	if err := json.Unmarshal(ctx.Request.Body(), &profile); err != nil {
		writeJSONError(ctx, http.StatusBadRequest, err.Error())
		return
	}
	profile.ID = id
	profile.UpdatedAt = time.Now()

	if err := s.Config.ConfigStore.UpdateGuardrailProfile(context.Background(), &profile); err != nil {
		writeJSONError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(ctx, http.StatusOK, profile)
}

func (s *BifrostHTTPServer) handleDeleteGuardrailProfile(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	if err := s.Config.ConfigStore.DeleteGuardrailProfile(context.Background(), id); err != nil {
		writeJSONError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	ctx.SetStatusCode(http.StatusNoContent)
}

// --- Link / Unlink ---

func (s *BifrostHTTPServer) handleLinkGuardrailProfile(ctx *fasthttp.RequestCtx) {
	ruleID := ctx.UserValue("id").(string)
	profileID := ctx.UserValue("profile_id").(string)
	if err := s.Config.ConfigStore.LinkGuardrailProfile(context.Background(), ruleID, profileID); err != nil {
		writeJSONError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	ctx.SetStatusCode(http.StatusNoContent)
}

func (s *BifrostHTTPServer) handleUnlinkGuardrailProfile(ctx *fasthttp.RequestCtx) {
	ruleID := ctx.UserValue("id").(string)
	profileID := ctx.UserValue("profile_id").(string)
	if err := s.Config.ConfigStore.UnlinkGuardrailProfile(context.Background(), ruleID, profileID); err != nil {
		writeJSONError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	ctx.SetStatusCode(http.StatusNoContent)
}

// --- Validate CEL ---

type validateRuleRequest struct {
	CelExpression string                 `json:"cel_expression"`
	ApplyTo       string                 `json:"apply_to"`
	Sample        map[string]interface{} `json:"sample"`
}

type validateRuleResponse struct {
	Valid  bool    `json:"valid"`
	Result *bool   `json:"result,omitempty"`
	Error  *string `json:"error,omitempty"`
}

func (s *BifrostHTTPServer) handleValidateGuardrailRule(ctx *fasthttp.RequestCtx) {
	var req validateRuleRequest
	if err := json.Unmarshal(ctx.Request.Body(), &req); err != nil {
		writeJSONError(ctx, http.StatusBadRequest, err.Error())
		return
	}

	env, err := guardrailsplugin.NewCELEnvPublic()
	if err != nil {
		writeJSONError(ctx, http.StatusInternalServerError, err.Error())
		return
	}

	prog, err := guardrailsplugin.CompileExpressionPublic(env, req.CelExpression)
	if err != nil {
		errStr := err.Error()
		writeJSON(ctx, http.StatusOK, validateRuleResponse{Valid: false, Error: &errStr})
		return
	}

	vars := map[string]interface{}{
		"request": req.Sample,
		"output":  map[string]interface{}{},
	}
	result, err := guardrailsplugin.EvalProgramPublic(prog, vars)
	if err != nil {
		errStr := err.Error()
		writeJSON(ctx, http.StatusOK, validateRuleResponse{Valid: true, Error: &errStr})
		return
	}

	writeJSON(ctx, http.StatusOK, validateRuleResponse{Valid: true, Result: &result})
}
```

Note: `NewCELEnvPublic`, `CompileExpressionPublic`, `EvalProgramPublic` are thin exported wrappers around the unexported `newCELEnv`, `compileExpression`, `evalProgram` functions. Add these to `cel_evaluator.go`:

```go
// Public wrappers for use by HTTP handlers (validate endpoint).
func NewCELEnvPublic() (*cel.Env, error)           { return newCELEnv() }
func CompileExpressionPublic(env *cel.Env, expr string) (cel.Program, error) {
	return compileExpression(env, expr)
}
func EvalProgramPublic(prog cel.Program, vars map[string]interface{}) (bool, error) {
	return evalProgram(prog, vars)
}
```

- [ ] **Step 2: Register routes in `server.go`**

Find the route registration section in `server.go` (look for existing `/api/routing-rules` routes) and add:

```go
// Guardrail Rules
s.router.GET("/api/guardrails/rules", s.handleListGuardrailRules)
s.router.POST("/api/guardrails/rules", s.handleCreateGuardrailRule)
s.router.POST("/api/guardrails/rules/validate", s.handleValidateGuardrailRule)
s.router.GET("/api/guardrails/rules/{id}", s.handleGetGuardrailRule)
s.router.PUT("/api/guardrails/rules/{id}", s.handleUpdateGuardrailRule)
s.router.DELETE("/api/guardrails/rules/{id}", s.handleDeleteGuardrailRule)
// Guardrail Profiles
s.router.GET("/api/guardrails/profiles", s.handleListGuardrailProfiles)
s.router.POST("/api/guardrails/profiles", s.handleCreateGuardrailProfile)
s.router.GET("/api/guardrails/profiles/{id}", s.handleGetGuardrailProfile)
s.router.PUT("/api/guardrails/profiles/{id}", s.handleUpdateGuardrailProfile)
s.router.DELETE("/api/guardrails/profiles/{id}", s.handleDeleteGuardrailProfile)
// Guardrail Link/Unlink
s.router.POST("/api/guardrails/rules/{id}/profiles/{profile_id}", s.handleLinkGuardrailProfile)
s.router.DELETE("/api/guardrails/rules/{id}/profiles/{profile_id}", s.handleUnlinkGuardrailProfile)
```

- [ ] **Step 3: Build transport**

```bash
cd transports/bifrost-http && go build ./...
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add transports/bifrost-http/server/guardrails_handlers.go \
        transports/bifrost-http/server/server.go \
        plugins/guardrails/cel_evaluator.go
git commit -m "feat(server): guardrails HTTP CRUD handlers + route registration"
```

---

### Task 9: Config Sync + FullReload

**Files:**
- Modify: `transports/bifrost-http/server/server.go`

- [ ] **Step 1: Add `handleConfigSyncEvent` cases**

In `handleConfigSyncEvent`, after the `"routing_rule"` case, add:

```go
case "guardrail_rule":
	gp, err := s.getGuardrailsPlugin()
	if err != nil {
		logger.Warn("handleConfigSyncEvent: guardrails plugin not loaded: %v", err)
		return
	}
	if event.Action == "delete" {
		gp.DeleteRule(event.ID)
	} else {
		rule, err := s.Config.ConfigStore.GetGuardrailRuleByID(context.Background(), event.ID)
		if err != nil {
			logger.Warn("handleConfigSyncEvent: GetGuardrailRuleByID %s failed: %v", event.ID, err)
			return
		}
		gp.UpsertRule(rule)
	}

case "guardrail_profile":
	gp, err := s.getGuardrailsPlugin()
	if err != nil {
		logger.Warn("handleConfigSyncEvent: guardrails plugin not loaded: %v", err)
		return
	}
	if event.Action == "delete" {
		gp.DeleteProfile(event.ID)
	} else {
		profile, err := s.Config.ConfigStore.GetGuardrailProfileByID(context.Background(), event.ID)
		if err != nil {
			logger.Warn("handleConfigSyncEvent: GetGuardrailProfileByID %s failed: %v", event.ID, err)
			return
		}
		gp.UpsertProfile(profile)
	}
```

- [ ] **Step 2: Add `getGuardrailsPlugin` helper to `server.go`**

```go
func (s *BifrostHTTPServer) getGuardrailsPlugin() (*guardrailsplugin.GuardrailsPlugin, error) {
	plugin, err := s.Config.GetPlugin(guardrailsplugin.PluginName)
	if err != nil {
		return nil, err
	}
	gp, ok := plugin.(*guardrailsplugin.GuardrailsPlugin)
	if !ok {
		return nil, fmt.Errorf("guardrails plugin type mismatch")
	}
	return gp, nil
}
```

- [ ] **Step 3: Add FullReload block**

In `FullReload`, after the routing rules block, add:

```go
// Guardrail rules + profiles reload
if gp, err := s.getGuardrailsPlugin(); err == nil {
	if rules, err := s.Config.ConfigStore.GetGuardrailRules(ctx); err == nil {
		gp.ReloadRules(rules)
	} else {
		logger.Warn("FullReload: failed to list guardrail rules: %v", err)
	}
	if profiles, err := s.Config.ConfigStore.GetGuardrailProfiles(ctx); err == nil {
		gp.ReloadProfiles(profiles)
	} else {
		logger.Warn("FullReload: failed to list guardrail profiles: %v", err)
	}
}
```

- [ ] **Step 4: Build transport**

```bash
cd transports/bifrost-http && go build ./...
```

Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add transports/bifrost-http/server/server.go
git commit -m "feat(server): guardrails config sync event handling + FullReload integration"
```

---

### Task 10: Plugin Registration

**Files:**
- Modify: `transports/bifrost-http/server/plugins.go`

- [ ] **Step 1: Add guardrails import + loadBuiltinPlugin case**

In `plugins.go`, add the import:
```go
guardrailsplugin "github.com/maximhq/bifrost/plugins/guardrails"
```

In `loadBuiltinPlugin`, add before the `default` case:

```go
case guardrailsplugin.PluginName:
	return guardrailsplugin.Init(ctx, bifrostConfig.ConfigStore, logger)
```

- [ ] **Step 2: Add guardrails to `loadBuiltinPlugins` order**

In `loadBuiltinPlugins`, after governance is loaded, add:

```go
// Guardrails — must run after governance so BifrostContext has VK/team IDs
if err := s.registerBuiltinPlugin(ctx, guardrailsplugin.PluginName, builtinPlacement, nil); err != nil {
	return err
}
```

(Use the same pattern as other built-in plugins in that function.)

- [ ] **Step 3: Add `guardrails` to `IsBuiltinPlugin`**

Find `IsBuiltinPlugin` in `transports/bifrost-http/lib/` (or wherever it's defined) and add `guardrailsplugin.PluginName`.

```bash
grep -r "IsBuiltinPlugin" transports/bifrost-http/ --include="*.go" -l
```

Add `guardrailsplugin.PluginName` to the list of built-in plugin names.

- [ ] **Step 4: Add guardrails module to go.mod of bifrost-http**

```bash
cd transports/bifrost-http && go get github.com/maximhq/bifrost/plugins/guardrails
go mod tidy
```

Or add manually to the `replace` block:
```
replace github.com/maximhq/bifrost/plugins/guardrails => ../../plugins/guardrails
```

- [ ] **Step 5: Build full transport**

```bash
cd transports/bifrost-http && go build ./...
```

Expected: no errors.

- [ ] **Step 6: Run all guardrail tests**

```bash
cd plugins/guardrails && go test ./... -v
cd framework && go test ./configstore/... -run "Guardrail" -v
```

Expected: all PASS.

- [ ] **Step 7: Commit**

```bash
git add transports/bifrost-http/server/plugins.go \
        transports/bifrost-http/go.mod transports/bifrost-http/go.sum
git commit -m "feat(server): register guardrails as built-in plugin + IsBuiltinPlugin entry"
```
