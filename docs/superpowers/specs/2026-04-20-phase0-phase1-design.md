# Phase 0 + Phase 1 Design Spec
**Date:** 2026-04-20  
**Scope:** Teams UI Unblock (Phase 0) + governance_users + Generic OIDC/SSO (Phase 1, excluding RBAC)  
**Status:** Approved — all 4 sections locked

---

## Section 1 — Phase 0: Teams UI Unblock

### Problem

`ui/app/workspace/governance/teams/page.tsx` currently imports from `@enterprise/components/user-groups/teamsView`, which falls back to a "not available" placeholder on non-enterprise builds. The backend implementation is complete (`governance_teams`, Teams CRUD endpoints). The Teams table and dialog components already exist in the UI codebase but are not wired up.

### Existing Components

| File | Status |
|------|--------|
| `ui/app/workspace/governance/views/teamsTable.tsx` | Exists, renders team list |
| `ui/app/workspace/governance/views/teamDialog.tsx` | Exists, create/edit dialog |
| `ui/app/workspace/governance/teams/page.tsx` | Broken — imports `@enterprise` fallback |

### Fix

Replace the `@enterprise` import in `governance/teams/page.tsx` with direct imports of `teamsTable.tsx` and `teamDialog.tsx`. No new components, no new backend work.

```tsx
// Before
import TeamsView from "@enterprise/components/user-groups/teamsView";

// After
import { TeamsTable } from "@/app/workspace/governance/views/teamsTable";
import { TeamDialog } from "@/app/workspace/governance/views/teamDialog";
```

Wire `TeamsTable` and `TeamDialog` into the page layout matching the pattern used by other governance list pages (Virtual Keys, Users).

### Acceptance Criteria

- Teams page renders team list without `@enterprise` dependency
- Create/edit/delete team actions work end-to-end
- No new files created; only `governance/teams/page.tsx` modified

---

## Section 2 — governance_users Table & User Management API

### Schema

New table in framework config store, alongside existing `governance_teams`, `governance_customers`:

```sql
CREATE TABLE governance_users (
    id          TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    email       TEXT NOT NULL UNIQUE,
    name        TEXT NOT NULL DEFAULT '',
    team_id     TEXT REFERENCES governance_teams(id) ON DELETE SET NULL,
    budget_id   TEXT REFERENCES governance_budgets(id) ON DELETE SET NULL,
    rate_limit_id TEXT REFERENCES governance_rate_limits(id) ON DELETE SET NULL,
    auth_method TEXT NOT NULL DEFAULT 'password',  -- 'password' | 'oidc'
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_governance_users_email ON governance_users(email);
CREATE INDEX idx_governance_users_team_id ON governance_users(team_id);
```

### Go Table Struct

```go
// framework/configstore/tables/governance_users.go
type GovernanceUsersTable struct {
    ID           string     `gorm:"primaryKey" json:"id"`
    Email        string     `gorm:"type:text;not null;uniqueIndex" json:"email"`
    Name         string     `gorm:"type:text;not null;default:''" json:"name"`
    TeamID       *string    `gorm:"type:text;index" json:"team_id,omitempty"`
    BudgetID     *string    `gorm:"type:text" json:"budget_id,omitempty"`
    RateLimitID  *string    `gorm:"type:text" json:"rate_limit_id,omitempty"`
    AuthMethod   string     `gorm:"type:text;not null;default:'password'" json:"auth_method"`
    CreatedAt    time.Time  `gorm:"not null" json:"created_at"`
    UpdatedAt    time.Time  `gorm:"not null" json:"updated_at"`
}

func (GovernanceUsersTable) TableName() string { return "governance_users" }
```

### Config Store Interface

```go
// framework/configstore/store.go additions
type ConfigStore interface {
    // ... existing methods ...

    // User management
    CreateUser(ctx context.Context, user *tables.GovernanceUsersTable) error
    GetUser(ctx context.Context, id string) (*tables.GovernanceUsersTable, error)
    GetUserByEmail(ctx context.Context, email string) (*tables.GovernanceUsersTable, error)
    ListUsers(ctx context.Context, search string, limit, offset int) ([]*tables.GovernanceUsersTable, int64, error)
    UpdateUser(ctx context.Context, id string, updates map[string]any) (*tables.GovernanceUsersTable, error)
    DeleteUser(ctx context.Context, id string) error
    UpsertUserByEmail(ctx context.Context, email, name, authMethod string) (*tables.GovernanceUsersTable, error)
}
```

`UpsertUserByEmail` is used by the SSO callback to auto-provision users — insert on first login, update name on subsequent logins.

### Write Path: Transactional Budget/Rate Limit Cascade

When deleting a user, cascade-delete their owned budget and rate limit within a single transaction:

```go
func (s *SQLiteConfigStore) DeleteUser(ctx context.Context, id string) error {
    return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
        var user tables.GovernanceUsersTable
        if err := tx.First(&user, "id = ?", id).Error; err != nil {
            return err
        }
        if user.BudgetID != nil {
            tx.Delete(&tables.TableBudget{}, "id = ?", *user.BudgetID)
        }
        if user.RateLimitID != nil {
            tx.Delete(&tables.TableRateLimit{}, "id = ?", *user.RateLimitID)
        }
        return tx.Delete(&user).Error
    })
}
```

### HTTP Endpoints

All under `/api/governance/users` (transport layer, not governance plugin):

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/governance/users` | List users; `?search=`, `?limit=`, `?offset=` |
| POST | `/api/governance/users` | Create user |
| GET | `/api/governance/users/:id` | Get user |
| PUT | `/api/governance/users/:id` | Update user |
| DELETE | `/api/governance/users/:id` | Delete user + cascade budget/rate limit |

### Governance Plugin Integration

The existing in-memory `UserGovernance` store (`plugins/governance/store.go`) already handles budget/rate limit enforcement at inference time. Once `governance_users` is persisted, the governance plugin's `CreateUserGovernanceInMemory` / `UpdateUserGovernanceInMemory` / `DeleteUserGovernanceInMemory` calls become DB-backed by loading from `governance_users` at startup and syncing on writes.

No changes to the inference path — plugin continues to use in-memory struct for hot-path lookups; DB is the source of truth for persistence across restarts.

### UI

`ui/app/workspace/governance/users/page.tsx` — replace placeholder with:
- User list table (email, name, team, budget, rate limit columns)
- Create/edit dialog (email, name, team dropdown, optional budget/rate limit assignment)
- Delete with confirmation

Pattern mirrors existing Virtual Keys page.

### Acceptance Criteria

- `governance_users` table created via GORM AutoMigrate
- Full CRUD via `/api/governance/users`
- Email uniqueness enforced at DB level; API returns 409 on duplicate
- Delete cascades budget/rate limit in one transaction
- Search by email/name works (`?search=foo`)
- UI lists, creates, edits, deletes users

---

## Section 3 — Sessions Migration + Generic OIDC Engine

### Sessions Table Migration

Add two columns to existing `sessions` table:

```sql
ALTER TABLE sessions ADD COLUMN user_id TEXT NULL REFERENCES governance_users(id) ON DELETE SET NULL;
ALTER TABLE sessions ADD COLUMN auth_method TEXT NOT NULL DEFAULT 'password';
```

Update `SessionsTable` struct:

```go
type SessionsTable struct {
    // ... existing fields ...
    UserID     *string `gorm:"type:text;index" json:"user_id,omitempty"`
    AuthMethod string  `gorm:"type:text;not null;default:'password'" json:"auth_method"`
}
```

### SSO Nonces Table

```sql
CREATE TABLE governance_sso_nonces (
    state        TEXT PRIMARY KEY,
    code_verifier TEXT NOT NULL,
    provider     TEXT NOT NULL,
    expires_at   DATETIME NOT NULL
);
CREATE INDEX idx_governance_sso_nonces_expires ON governance_sso_nonces(expires_at);
```

Nonces expire after 10 minutes. A background cleanup goroutine deletes expired rows on startup.

### Generic OIDC Engine

```go
// transports/bifrost-http/handlers/sso.go

type OIDCProvider interface {
    Name() string
    // ExtractUserInfo maps raw OIDC claims → (email, name, groups)
    // config is passed so adapters can use role_claim_key / group_claim_key
    ExtractUserInfo(claims map[string]any, config SSOConfig) (email, name string, groups []string, err error)
}

type SSOHandler struct {
    config      SSOConfig        // loaded from governance_sso_configs
    provider    OIDCProvider     // Okta or Entra adapter
    configStore configstore.ConfigStore
    jwksCache   *JWKSCache
}
```

#### Authorization Code Flow with PKCE

**Step 1 — Initiate (`GET /api/sso/login?provider=okta`):**

```
1. Generate state (32-byte random hex)
2. Generate code_verifier (32-byte random, base64url)
3. Compute code_challenge = base64url(SHA256(code_verifier))  [S256]
4. Store (state, code_verifier, provider, expires_at=now+10m) in governance_sso_nonces
5. Build authorization URL:
   {issuer_url}/oauth/v2/authorize
     ?response_type=code
     &client_id={client_id}
     &redirect_uri={callback_url}
     &scope=openid profile email
     &state={state}
     &code_challenge={code_challenge}
     &code_challenge_method=S256
6. Redirect browser to authorization URL
```

**Step 2 — Callback (`GET /api/sso/callback?code=...&state=...`):**

```
1. Validate state:  look up governance_sso_nonces by state; 404 if missing or expired
2. Delete nonce row (single-use)
3. Exchange code for tokens:
   POST {issuer_url}/oauth/v2/token
     code, client_id, client_secret, redirect_uri, code_verifier
4. Fetch JWKS from {issuer_url}/.well-known/jwks.json  (cached 1h)
5. Verify id_token signature + iss/aud/exp claims
6. Decode id_token claims
7. Call provider.ExtractUserInfo(claims, ssoConfig) → email, name, groups
8. UpsertUserByEmail(email, name, "oidc") → governance_users row
9. Create/reuse session:
   - If existing non-expired session for user_id exists: update UpdatedAt
   - Else: INSERT new session with user_id + auth_method='oidc'
10. Set session cookie; redirect to /workspace
```

#### Idempotent Callback

If the same OIDC callback fires twice (browser back/forward), step 1 fails on the second attempt (nonce already deleted), returning 400. The existing session remains valid.

#### JWKS Cache

```go
type JWKSCache struct {
    mu      sync.RWMutex
    entries map[string]*jwksCacheEntry  // keyed by jwks_uri
}

type jwksCacheEntry struct {
    keys      []jose.JSONWebKey
    fetchedAt time.Time
}

const jwksTTL = 1 * time.Hour
```

On cache miss or TTL expiry: fetch JWKS with 5s timeout, store result. Cache is process-local; no distributed invalidation needed.

### `is-auth-enabled` Response Extension

Existing endpoint `GET /api/session/is-auth-enabled` currently returns:

```json
{ "is_auth_enabled": true, "has_valid_token": false }
```

Extended to:

```json
{
  "is_auth_enabled": true,
  "has_valid_token": false,
  "sso_enabled": true
}
```

`sso_enabled` = `true` if any row in `governance_sso_configs` has `enabled = true`. Computed at request time (single DB count query). No new endpoint — one additional field on existing response. Existing `is_auth_enabled` and `has_valid_token` fields are unchanged.

Login page reads this response and conditionally renders the "Sign in with SSO" button. If `sso_enabled` is false or absent, the button is hidden.

### Acceptance Criteria

- Sessions table migration runs without data loss
- `governance_sso_nonces` table created with TTL index
- PKCE flow completes end-to-end (Okta + Entra)
- Replay attack blocked (nonce single-use)
- JWKS verified (signature + iss/aud/exp)
- `UpsertUserByEmail` creates user on first SSO login, updates name on subsequent logins
- `is-auth-enabled` response includes `sso_enabled` field alongside existing `is_auth_enabled` + `has_valid_token`
- Login page shows "Sign in with SSO" button only when `sso_enabled: true`

---

## Section 4 — Provider Adapters + SSO Config UI

### Provider Adapters

Generic OIDC engine + provider-specific adapters implementing `OIDCProvider`:

```go
// transports/bifrost-http/handlers/sso_adapters.go

type OktaAdapter struct{}

func (OktaAdapter) Name() string { return "okta" }

func (OktaAdapter) ExtractUserInfo(claims map[string]any, config SSOConfig) (email, name string, groups []string, err error) {
    email, _ = claims["email"].(string)
    name, _ = claims["name"].(string)
    // Use config.RoleClaimKey / config.GroupClaimKey if set; else default "groups"
    groupKey := "groups"
    if config.GroupClaimKey != "" {
        groupKey = config.GroupClaimKey
    }
    if rawGroups, ok := claims[groupKey].([]any); ok {
        for _, g := range rawGroups {
            if gs, ok := g.(string); ok {
                groups = append(groups, gs)
            }
        }
    }
    if email == "" {
        return "", "", nil, fmt.Errorf("okta: missing email claim")
    }
    return email, name, groups, nil
}

type EntraAdapter struct{}

func (EntraAdapter) Name() string { return "entra" }

func (EntraAdapter) ExtractUserInfo(claims map[string]any, config SSOConfig) (email, name string, groups []string, err error) {
    // Entra uses "preferred_username" or "upn" for email
    email, _ = claims["preferred_username"].(string)
    if email == "" {
        email, _ = claims["upn"].(string)
    }
    name, _ = claims["name"].(string)
    // Entra group IDs come as UUIDs in "groups" claim by default
    groupKey := "groups"
    if config.GroupClaimKey != "" {
        groupKey = config.GroupClaimKey
    }
    if rawGroups, ok := claims[groupKey].([]any); ok {
        for _, g := range rawGroups {
            if gs, ok := g.(string); ok {
                groups = append(groups, gs)
            }
        }
    }
    if email == "" {
        return "", "", nil, fmt.Errorf("entra: missing email/UPN claim")
    }
    return email, name, groups, nil
}
```

**Provider registry:**

```go
var providerRegistry = map[string]OIDCProvider{
    "okta":  OktaAdapter{},
    "entra": EntraAdapter{},
    // future: "google", "github", "generic"
}
```

Adding a new provider = implement `OIDCProvider`, register one line. No engine changes.

### SSO Config Table

```sql
CREATE TABLE governance_sso_configs (
    id               TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    provider         TEXT NOT NULL,          -- 'okta' | 'entra'
    issuer_url       TEXT NOT NULL,
    client_id        TEXT NOT NULL,
    client_secret    TEXT NOT NULL,          -- encrypted at rest (existing encrypt package)
    role_claim_key   TEXT NOT NULL DEFAULT '',
    group_claim_key  TEXT NOT NULL DEFAULT '',
    enabled          INTEGER NOT NULL DEFAULT 1,
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

`client_secret` uses the existing `encrypt` package (same as other secrets in the codebase). `BeforeSave` / `AfterFind` hooks on the GORM model.

### SSO Config Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/governance/sso/configs` | List all SSO configs |
| POST | `/api/governance/sso/configs` | Create SSO config |
| PUT | `/api/governance/sso/configs/:id` | Update config |
| DELETE | `/api/governance/sso/configs/:id` | Delete config |
| POST | `/api/governance/sso/configs/:id/test` | Test connection (SSRF-guarded) |

### SSRF Guard on Test-Connection

`POST /api/governance/sso/configs/:id/test` fetches `{issuer_url}/.well-known/openid-configuration` to validate the provider is reachable. Guard:

```go
func validateIssuerURL(rawURL string) error {
    u, err := url.Parse(rawURL)
    if err != nil {
        return fmt.Errorf("invalid URL: %w", err)
    }
    if u.Scheme != "https" {
        return fmt.Errorf("issuer URL must use HTTPS")
    }
    host := u.Hostname()
    ips, err := net.LookupHost(host)
    if err != nil {
        return fmt.Errorf("cannot resolve host: %w", err)
    }
    for _, ipStr := range ips {
        ip := net.ParseIP(ipStr)
        if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
            return fmt.Errorf("issuer URL resolves to private/loopback address")
        }
    }
    return nil
}

// HTTP client used for test-connection and JWKS fetch:
var safeHTTPClient = &http.Client{
    Timeout: 5 * time.Second,
    CheckRedirect: func(req *http.Request, via []*http.Request) error {
        if len(via) >= 1 {
            return http.ErrUseLastResponse  // max 1 redirect
        }
        return nil
    },
}
```

Response body capped at 64 KB (`io.LimitReader`).

### UI: User Provisioning Page

`/workspace/scim` (existing route, `ui/app/workspace/scim/page.tsx`) — replace current SCIM-only content with tabbed layout:

**Tab 1: SSO / IdP Settings**
- Provider dropdown (Okta / Microsoft Entra)
- Fields: Issuer URL, Client ID, Client Secret (masked input)
- Advanced: Role Claim Key, Group Claim Key (collapsed by default)
- "Test Connection" button → calls `/api/governance/sso/configs/:id/test`
- Enable/disable toggle

**Tab 2: SCIM** (placeholder for Phase 2)
- Greyed out with "Coming soon" badge

The existing `ui/app/workspace/scim/page.tsx` gains a tab wrapper; existing SCIM content moves into Tab 2.

### Login Page SSO Button

`ui/app/login/page.tsx` (or equivalent):

```tsx
// Fetch on mount — reuses existing is-auth-enabled call
const { is_auth_enabled, sso_enabled } = await fetch("/api/session/is-auth-enabled").then(r => r.json());

// Render
{sso_enabled && (
  <Button variant="outline" onClick={() => window.location.href = "/api/sso/login"}>
    Sign in with SSO
  </Button>
)}
```

`GET /api/sso/login` is provider-agnostic: the backend reads `governance_sso_configs`, picks the first enabled config, and initiates the PKCE flow for that provider. The login page never names a provider. If no enabled config exists, the endpoint returns 404 and the button is already hidden (because `sso_enabled` is false).

### Acceptance Criteria

- Okta and Entra adapters extract email/name/groups correctly with config-driven claim keys
- New provider = implement interface + 1 line registration, no engine changes
- `governance_sso_configs` table with encrypted `client_secret`
- Full CRUD for SSO configs via `/api/governance/sso/configs`
- Test-connection endpoint rejects non-HTTPS, private IPs, >1 redirect, >64KB responses
- User Provisioning page has "SSO / IdP Settings" and "SCIM" tabs
- Login page shows "Sign in with SSO" button only when `sso_enabled: true`

---

## Migration Order

1. `governance_users` table (no dependencies)
2. `governance_sso_configs` table (no dependencies)
3. `governance_sso_nonces` table (no dependencies)
4. `sessions` table — add `user_id` (FK to `governance_users`) + `auth_method` columns

GORM AutoMigrate handles all migrations. The `sessions` migration must run after `governance_users` exists due to the FK constraint.

---

## Out of Scope (Phase 2+)

- RBAC (roles, permissions, role assignments)
- SCIM provisioning (automated user sync from IdP directory)
- Group-to-role mapping from OIDC groups claim
- Multi-provider login (provider selection UI)
- MFA / WebAuthn
- Audit logging of SSO events
- Business Units governance
