# Phase 1 — Identity Foundation Implementation Plan (Overlay Architecture)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.
>
> **Supersedes** `2026-05-30-phase1-identity-foundation.md` (the "native" plan). Same spec, same behavior — different *placement*. This version isolates everything into a fork-owned `identity` package so upstream merges stay clean.

**Goal:** Add multi-user + RBAC to a **fork** of open-source Bifrost as a self-contained overlay, so the OSS core is touched in as few, as stable, places as possible (target: ~2 wiring lines + 1 small masking patch).

**Architecture:** A new package `transports/bifrost-http/identity/` owns its own tables (`identity_users`, `identity_sessions`), its own DB migration (run via `ConfigStore.RunMigration`), its own HTTP middlewares (login-intercept + user-context + RBAC), and its own handlers (registered on the public `s.Router`). The OSS core "calls in" through exactly two appended lines in the server bootstrap. We do **not** touch core `sessions.go`, the `ConfigStore` interface, `middlewares.go`, or `session.go`. We do **not** use the `IsEnterprise` flag — that belongs to upstream's proprietary enterprise edition.

**Tech Stack:** Go, GORM (SQLite + Postgres), `gormigrate`, fasthttp + `fasthttp/router`, bcrypt (`framework/encrypt`), Next.js (`ui/`). Tests: `testing` + `testify`, in-memory SQLite.

**Source spec:** `docs/superpowers/specs/2026-05-30-phase1-identity-foundation-spec.md` (behavior is unchanged; only the implementation location differs).

**Resolved decisions:** RBAC is role-only (scoping → Phase 2); fail-closed for unmapped `/api/*`; migrated admin email = `admin@localhost`; login via middleware-intercept (0 core edit); viewer key-masking via a small `providers.go` carry-patch.

---

## Seam Map (verified against the codebase — why this works)

| Need | OSS seam used | Core edit |
|------|---------------|-----------|
| Run our migration | `ConfigStore.RunMigration(ctx, fn)` — interface method, `store.go:443`; built for "downstream consumers" | **0** |
| Runtime DB access | type-assert `ConfigStore` → `*RDBConfigStore`, call `.DB()` (`rdb.go:301`) | **0** |
| Register our routes | `s.Router` is a **public** field (`server.go:135`) | **0** |
| Email login (replace username) | our middleware intercepts `/api/session/login` before the core handler runs | **0** |
| Persist users + session→user map | our own tables `identity_users`, `identity_sessions` | **0** (no `sessions.go`/interface change) |
| **RBAC over core routes** | our middleware must enter the `apiMiddlewares` slice core applies (`server.go:~2049,2053`) | **~1 line (irreducible)** |
| Wire migration + routes | one call `identity.Wire(...)` in bootstrap | **~1 line** |
| Mask key values from viewer | transforms core handler output | **1 small patch in `providers.go`** |

**Import-cycle safety:** `identity` depends only on `core/schemas`, `framework/configstore` (+ `tables`, `encrypt`), `transports/bifrost-http/lib`, and fasthttp/router/uuid. It does **not** import `server` or `handlers`. The `server` package imports `identity`. No cycle.

---

## File Structure

**Create (all fork-owned, conflict-free):**
- `transports/bifrost-http/identity/tables.go` — `IdentityUser`, `IdentitySession`, role consts
- `transports/bifrost-http/identity/store.go` — `Store` over `*gorm.DB`: user CRUD + session-map + admin count
- `transports/bifrost-http/identity/migration.go` — table creation + single-admin migration + session backfill
- `transports/bifrost-http/identity/middleware.go` — `IdentityMiddleware` (login-intercept + user-context) + `RBACMiddleware` + permission map
- `transports/bifrost-http/identity/handlers.go` — `/api/users*` + `/api/auth/settings` handlers
- `transports/bifrost-http/identity/wire.go` — `Middlewares(...)` + `Wire(...)` entrypoints
- `transports/bifrost-http/identity/*_test.go` — tests

**Modify (the entire core footprint):**
- `transports/bifrost-http/server/server.go` — 2 appended lines (the hook) [FORK PATCH #1]
- `transports/bifrost-http/handlers/providers.go` — viewer key masking [FORK PATCH #2]
- `ui/` — login field `username`→`email` + role-aware controls [FORK PATCH #3]
- `FORK_PATCHES.md` (create) — documents the 3 patches so rebases are mechanical

---

## Task 1: Package scaffold + the core hook (no-op, prove wiring)

**Files:**
- Create: `transports/bifrost-http/identity/wire.go`
- Modify: `transports/bifrost-http/server/server.go` (~line 2049 and after `RegisterAPIRoutes`)
- Create: `FORK_PATCHES.md`

- [ ] **Step 1: Create the package entrypoints as no-ops**

`transports/bifrost-http/identity/wire.go`:

```go
// Package identity is a fork-owned overlay that adds multi-user auth + RBAC to
// open-source Bifrost without modifying core auth/session code. The OSS core
// calls into it via two lines in the server bootstrap (see FORK_PATCHES.md).
package identity

import (
	"context"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
)

// Middlewares returns the overlay's HTTP middlewares, in apply order:
// IdentityMiddleware (login-intercept + attach user) then RBACMiddleware.
// They must be appended to the API middleware chain the core applies to all
// /api/* routes, so RBAC can govern core routes too.
func Middlewares(store configstore.ConfigStore, authEnabled func() bool) []schemas.BifrostHTTPMiddleware {
	return nil // filled in Task 4 + 5
}

// Wire runs the overlay's DB migration and registers its routes on the shared
// router. Call once during bootstrap, after core routes are registered.
func Wire(ctx context.Context, r *router.Router, store configstore.ConfigStore) error {
	return nil // filled in Task 2 + 6
}
```

- [ ] **Step 2: Add the two hook lines to the bootstrap [FORK PATCH #1]**

In `transports/bifrost-http/server/server.go`, in the bootstrap where `apiMiddlewares` is built (~line 2048), inside the existing `if ctx.Value(schemas.BifrostContextKeyIsEnterprise) == nil {` block, append after the core auth middleware:

```go
		if ctx.Value(schemas.BifrostContextKeyIsEnterprise) == nil {
			apiMiddlewares = append(apiMiddlewares, s.AuthMiddleware.APIMiddleware())
			// FORK PATCH #1a: overlay identity/RBAC middleware (see FORK_PATCHES.md)
			apiMiddlewares = append(apiMiddlewares, identity.Middlewares(s.Config.ConfigStore, func() bool {
				cfg := s.AuthMiddleware != nil
				return cfg
			})...)
		}
```

And after the `s.RegisterAPIRoutes(s.Ctx, s, apiMiddlewares...)` call succeeds (~line 2053), add:

```go
	// FORK PATCH #1b: overlay routes + migration (see FORK_PATCHES.md)
	if err := identity.Wire(s.Ctx, s.Router, s.Config.ConfigStore); err != nil {
		return fmt.Errorf("failed to wire identity overlay: %v", err)
	}
```

Add the import `identity "github.com/maximhq/bifrost/transports/bifrost-http/identity"` to `server.go`.

> Keep these lines together and clearly commented as fork patches — they are the only core edits in the request/bootstrap path and the only ones that can conflict on upstream merges.

- [ ] **Step 3: Document the patch**

Create `FORK_PATCHES.md` at repo root:

```markdown
# Fork Patches (carry across upstream merges)

This fork adds multi-user + RBAC as an overlay package `transports/bifrost-http/identity/`.
The only edits to upstream files are listed here. On `git merge upstream/main`, re-apply these.

## Patch #1 — wire the overlay (transports/bifrost-http/server/server.go)
- In bootstrap, inside `if IsEnterprise == nil`, append `identity.Middlewares(...)` to `apiMiddlewares`.
- After `RegisterAPIRoutes(...)`, call `identity.Wire(s.Ctx, s.Router, s.Config.ConfigStore)`.
- Add the `identity` import.

## Patch #2 — viewer key masking (transports/bifrost-http/handlers/providers.go)
- See Task 7. Mask raw key values when the request user is a viewer.

## Patch #3 — UI (ui/)
- Login form sends `email` instead of `username`; dashboard gates controls by role.
```

- [ ] **Step 4: Verify it builds and boots**

Run: `go build ./...`
Expected: compiles (no-op overlay wired in).

- [ ] **Step 5: Commit**

```bash
git add transports/bifrost-http/identity/wire.go transports/bifrost-http/server/server.go FORK_PATCHES.md
git commit -m "feat(identity): scaffold overlay package + core wiring hooks"
```

---

## Task 2: Tables + migration (own tables, via RunMigration)

**Files:**
- Create: `transports/bifrost-http/identity/tables.go`
- Create: `transports/bifrost-http/identity/migration.go`
- Test: `transports/bifrost-http/identity/migration_test.go`

- [ ] **Step 1: Define the overlay tables**

`transports/bifrost-http/identity/tables.go`:

```go
package identity

import "time"

const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"
)

var ValidRoles = map[string]bool{RoleAdmin: true, RoleOperator: true, RoleViewer: true}

// IdentityUser is a named dashboard user (fork-owned; does not touch core tables).
type IdentityUser struct {
	ID           string     `gorm:"primaryKey;type:varchar(255)" json:"id"`
	Email        string     `gorm:"type:varchar(255);not null;uniqueIndex" json:"email"`
	Name         string     `gorm:"type:varchar(255);not null" json:"name"`
	Role         string     `gorm:"type:varchar(50);not null;default:'viewer'" json:"role"`
	PasswordHash *string    `gorm:"type:text" json:"-"`
	IsActive     bool       `gorm:"not null;default:true" json:"is_active"`
	LastLoginAt  *time.Time `gorm:"index" json:"last_login_at,omitempty"`
	CreatedAt    time.Time  `gorm:"index;not null" json:"created_at"`
	UpdatedAt    time.Time  `gorm:"not null" json:"updated_at"`
}

func (IdentityUser) TableName() string { return "identity_users" }

// IdentitySession maps a core session token (by its SHA-256 hash) to a user.
// We mirror the core token_hash so we never store the raw token and can look
// up the user for a request that core's AuthMiddleware already authenticated.
type IdentitySession struct {
	TokenHash string    `gorm:"primaryKey;type:varchar(64)" json:"-"`
	UserID    string    `gorm:"type:varchar(255);index;not null" json:"user_id"`
	CreatedAt time.Time `gorm:"not null" json:"created_at"`
}

func (IdentitySession) TableName() string { return "identity_sessions" }
```

- [ ] **Step 2: Write the migration (create tables, migrate admin, backfill sessions)**

`transports/bifrost-http/identity/migration.go`:

```go
package identity

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/encrypt"
	"gorm.io/gorm"
)

// Migrate creates the overlay tables and, on first run, migrates the legacy
// single admin from governance_config and backfills existing core sessions so
// the current admin is NOT logged out. Idempotent.
func Migrate(ctx context.Context, db *gorm.DB) error {
	db = db.WithContext(ctx)
	m := db.Migrator()
	if !m.HasTable(&IdentityUser{}) {
		if err := m.CreateTable(&IdentityUser{}); err != nil {
			return err
		}
	}
	if !m.HasTable(&IdentitySession{}) {
		if err := m.CreateTable(&IdentitySession{}); err != nil {
			return err
		}
	}

	// Already migrated?
	var existing IdentityUser
	err := db.First(&existing, "email = ?", "admin@localhost").Error
	if err == nil {
		return nil // admin already seeded
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	// Read legacy admin from governance_config.
	var username, password *string
	if err := db.First(&tables.TableGovernanceConfig{}, "key = ?", tables.ConfigAdminUsernameKey).Select("value").Scan(&username).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	if err := db.First(&tables.TableGovernanceConfig{}, "key = ?", tables.ConfigAdminPasswordKey).Select("value").Scan(&password).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	if username == nil || password == nil || *username == "" || *password == "" {
		return nil // Branch B: no legacy admin (auth disabled / fresh) — tables ready, nothing to seed
	}

	adminID := uuid.New().String()
	pw := *password // already a bcrypt hash — DO NOT re-hash
	now := time.Now()
	admin := IdentityUser{ID: adminID, Email: "admin@localhost", Name: *username, Role: RoleAdmin, PasswordHash: &pw, IsActive: true, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&admin).Error; err != nil {
		return err
	}

	// Backfill: map every existing core session to the migrated admin so the
	// current admin stays logged in. We read the core sessions table directly.
	var sessions []tables.SessionsTable
	if err := db.Find(&sessions).Error; err != nil {
		return err
	}
	for _, s := range sessions {
		if s.TokenHash == "" {
			continue // legacy plaintext-only session; cannot map, will require re-login
		}
		row := IdentitySession{TokenHash: s.TokenHash, UserID: adminID, CreatedAt: now}
		if err := db.Where("token_hash = ?", s.TokenHash).FirstOrCreate(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

var _ = encrypt.HashSHA256 // encrypt used by Store (kept consistent across files)
```

- [ ] **Step 3: Write the failing test**

`transports/bifrost-http/identity/migration_test.go`:

```go
package identity

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/encrypt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&tables.TableGovernanceConfig{}, &tables.SessionsTable{}))
	return db
}

func TestMigrate_BranchA_MigratesAdmin_AndBackfillsSession(t *testing.T) {
	db := newDB(t)
	hash, err := encrypt.Hash("secret123")
	require.NoError(t, err)
	require.NoError(t, db.Create(&tables.TableGovernanceConfig{Key: tables.ConfigAdminUsernameKey, Value: "rootadmin"}).Error)
	require.NoError(t, db.Create(&tables.TableGovernanceConfig{Key: tables.ConfigAdminPasswordKey, Value: hash}).Error)
	// existing core session (BeforeSave sets token_hash from the token)
	require.NoError(t, db.Create(&tables.SessionsTable{Token: "tok", ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(), UpdatedAt: time.Now()}).Error)

	require.NoError(t, Migrate(context.Background(), db))

	var admin IdentityUser
	require.NoError(t, db.First(&admin, "email = ?", "admin@localhost").Error)
	assert.Equal(t, RoleAdmin, admin.Role)
	ok, err := encrypt.CompareHash(*admin.PasswordHash, "secret123") // verbatim copy, no double-hash
	require.NoError(t, err)
	assert.True(t, ok)

	th := encrypt.HashSHA256("tok")
	var mapped IdentitySession
	require.NoError(t, db.First(&mapped, "token_hash = ?", th).Error)
	assert.Equal(t, admin.ID, mapped.UserID)

	// idempotent
	require.NoError(t, Migrate(context.Background(), db))
	var n int64
	require.NoError(t, db.Model(&IdentityUser{}).Count(&n).Error)
	assert.Equal(t, int64(1), n)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./transports/bifrost-http/identity/ -run TestMigrate_BranchA -v`
Expected: PASS (admin migrated, password verifies, session mapped, idempotent).

- [ ] **Step 5: Commit**

```bash
git add transports/bifrost-http/identity/tables.go transports/bifrost-http/identity/migration.go transports/bifrost-http/identity/migration_test.go
git commit -m "feat(identity): overlay tables + idempotent single-admin migration"
```

---

## Task 3: Store (user CRUD + session map, over *gorm.DB)

**Files:**
- Create: `transports/bifrost-http/identity/store.go`
- Test: `transports/bifrost-http/identity/store_test.go`

- [ ] **Step 1: Write the Store**

`transports/bifrost-http/identity/store.go`:

```go
package identity

import (
	"context"
	"errors"
	"time"

	"github.com/maximhq/bifrost/framework/encrypt"
	"gorm.io/gorm"
)

// Store is the overlay's persistence over the shared DB. Obtain *gorm.DB by
// type-asserting the ConfigStore to *RDBConfigStore (see wire.go).
type Store struct{ db *gorm.DB }

func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

func (s *Store) GetUserByID(ctx context.Context, id string) (*IdentityUser, error) {
	var u IdentityUser
	if err := s.db.WithContext(ctx).First(&u, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*IdentityUser, error) {
	var u IdentityUser
	if err := s.db.WithContext(ctx).First(&u, "email = ?", email).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

func (s *Store) ListUsers(ctx context.Context) ([]IdentityUser, error) {
	var users []IdentityUser
	return users, s.db.WithContext(ctx).Order("created_at asc").Find(&users).Error
}

func (s *Store) CreateUser(ctx context.Context, u *IdentityUser) error {
	return s.db.WithContext(ctx).Create(u).Error
}

func (s *Store) UpdateUser(ctx context.Context, u *IdentityUser) error {
	u.UpdatedAt = time.Now()
	return s.db.WithContext(ctx).Save(u).Error
}

func (s *Store) CountActiveAdmins(ctx context.Context) (int64, error) {
	var n int64
	return n, s.db.WithContext(ctx).Model(&IdentityUser{}).
		Where("role = ? AND is_active = ?", RoleAdmin, true).Count(&n).Error
}

// MapSession records token_hash → user_id (called by our login handler).
func (s *Store) MapSession(ctx context.Context, token, userID string) error {
	return s.db.WithContext(ctx).Create(&IdentitySession{
		TokenHash: encrypt.HashSHA256(token), UserID: userID, CreatedAt: time.Now(),
	}).Error
}

// UserForToken resolves an active user from a session token via the map.
func (s *Store) UserForToken(ctx context.Context, token string) (*IdentityUser, error) {
	var m IdentitySession
	if err := s.db.WithContext(ctx).First(&m, "token_hash = ?", encrypt.HashSHA256(token)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	u, err := s.GetUserByID(ctx, m.UserID)
	if err != nil || u == nil || !u.IsActive {
		return nil, err
	}
	return u, nil
}

// UnmapAllForUser removes all session maps for a user (password change / reset).
func (s *Store) UnmapAllForUser(ctx context.Context, userID string) error {
	return s.db.WithContext(ctx).Where("user_id = ?", userID).Delete(&IdentitySession{}).Error
}
```

- [ ] **Step 2: Write the failing test**

`transports/bifrost-http/identity/store_test.go`:

```go
package identity

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newStore(t *testing.T) *Store {
	db := newDB(t)
	require.NoError(t, db.AutoMigrate(&IdentityUser{}, &IdentitySession{}))
	return NewStore(db)
}

func TestStore_UserAndSessionMap(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	require.NoError(t, s.CreateUser(ctx, &IdentityUser{ID: "u1", Email: "a@x.com", Name: "A", Role: RoleOperator, IsActive: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}))

	require.NoError(t, s.MapSession(ctx, "tok", "u1"))
	u, err := s.UserForToken(ctx, "tok")
	require.NoError(t, err)
	require.NotNil(t, u)
	assert.Equal(t, RoleOperator, u.Role)

	// deactivate → not resolvable
	u.IsActive = false
	require.NoError(t, s.UpdateUser(ctx, u))
	u2, err := s.UserForToken(ctx, "tok")
	require.NoError(t, err)
	assert.Nil(t, u2)

	n, err := s.CountActiveAdmins(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)
}
```

- [ ] **Step 3: Run test to verify it passes**

Run: `go test ./transports/bifrost-http/identity/ -run TestStore_UserAndSessionMap -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add transports/bifrost-http/identity/store.go transports/bifrost-http/identity/store_test.go
git commit -m "feat(identity): overlay store (user CRUD + token→user session map)"
```

---

## Task 4: Identity middleware (login-intercept + user-context)

**Files:**
- Create: `transports/bifrost-http/identity/middleware.go`
- Test: `transports/bifrost-http/identity/middleware_test.go`

- [ ] **Step 1: Write the response helpers + token extraction + IdentityMiddleware**

`transports/bifrost-http/identity/middleware.go`:

```go
package identity

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/encrypt"
	"github.com/valyala/fasthttp"
)

// ctxKeyUser carries the authenticated overlay user (own key; no core edit).
const ctxKeyUser schemas.BifrostContextKey = "identity-authenticated-user"

func sendJSON(ctx *fasthttp.RequestCtx, code int, v any) {
	ctx.Response.Header.SetContentType("application/json")
	ctx.SetStatusCode(code)
	_ = json.NewEncoder(ctx).Encode(v)
}
func sendErr(ctx *fasthttp.RequestCtx, code int, msg string) {
	sendJSON(ctx, code, map[string]string{"error": msg})
}

func tokenFromRequest(ctx *fasthttp.RequestCtx) string {
	if h := string(ctx.Request.Header.Peek("Authorization")); h != "" {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return string(ctx.Request.Header.Cookie("token"))
}

func userFrom(ctx *fasthttp.RequestCtx) *IdentityUser {
	if u, ok := ctx.UserValue(ctxKeyUser).(*IdentityUser); ok {
		return u
	}
	return nil
}

// IdentityMiddleware (a) intercepts POST /api/session/login and serves email
// login itself (so the core username handler is never reached — no double
// login, no core edit), and (b) attaches the overlay user to the context for
// all other authenticated /api/* requests.
func (o *Overlay) IdentityMiddleware() schemas.BifrostHTTPMiddleware {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) {
			path := string(ctx.Path())
			if path == "/api/session/login" && string(ctx.Method()) == "POST" {
				o.handleLogin(ctx)
				return // do not fall through to the core login handler
			}
			if strings.HasPrefix(path, "/api/") {
				if tok := tokenFromRequest(ctx); tok != "" {
					if u, err := o.store.UserForToken(ctx, tok); err == nil && u != nil {
						ctx.SetUserValue(ctxKeyUser, u)
					}
				}
			}
			next(ctx)
		}
	}
}

// handleLogin implements the 4-step email login (spec §3.7) and maps the
// resulting core session token to the user.
func (o *Overlay) handleLogin(ctx *fasthttp.RequestCtx) {
	if !o.authEnabled() {
		sendErr(ctx, fasthttp.StatusForbidden, "Authentication is not enabled")
		return
	}
	var p struct{ Email, Password string }
	if err := json.Unmarshal(ctx.PostBody(), &p); err != nil {
		sendErr(ctx, fasthttp.StatusBadRequest, "invalid request format")
		return
	}
	email := strings.ToLower(strings.TrimSpace(p.Email))
	u, err := o.store.GetUserByEmail(ctx, email)
	if err != nil {
		sendErr(ctx, fasthttp.StatusInternalServerError, "login failed")
		return
	}
	if u == nil || !u.IsActive || u.PasswordHash == nil { // identical 401 — no enumeration
		sendErr(ctx, fasthttp.StatusUnauthorized, "invalid email or password")
		return
	}
	ok, err := encrypt.CompareHash(*u.PasswordHash, p.Password)
	if err != nil {
		sendErr(ctx, fasthttp.StatusInternalServerError, "login failed")
		return
	}
	if !ok {
		sendErr(ctx, fasthttp.StatusUnauthorized, "invalid email or password")
		return
	}

	expiry := time.Duration(o.sessionExpiryHours()) * time.Hour
	token := uuid.New().String()
	// Create the CORE session so core AuthMiddleware authenticates future requests.
	if err := o.configStore.CreateSession(ctx, &tables.SessionsTable{
		Token: token, ExpiresAt: time.Now().Add(expiry), CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		sendErr(ctx, fasthttp.StatusInternalServerError, "failed to create session")
		return
	}
	if err := o.store.MapSession(ctx, token, u.ID); err != nil {
		sendErr(ctx, fasthttp.StatusInternalServerError, "failed to map session")
		return
	}
	now := time.Now()
	u.LastLoginAt = &now
	_ = o.store.UpdateUser(ctx, u)

	cookie := fasthttp.AcquireCookie()
	defer fasthttp.ReleaseCookie(cookie)
	cookie.SetKey("token")
	cookie.SetValue(token)
	cookie.SetExpire(time.Now().Add(expiry))
	cookie.SetPath("/")
	cookie.SetHTTPOnly(true)
	cookie.SetSameSite(fasthttp.CookieSameSiteLaxMode)
	if string(ctx.Request.Header.Peek("X-Forwarded-Proto")) == "https" {
		cookie.SetSecure(true)
	}
	ctx.Response.Header.SetCookie(cookie)

	sendJSON(ctx, fasthttp.StatusOK, map[string]any{
		"message": "Login successful",
		"user":    map[string]any{"id": u.ID, "email": u.Email, "name": u.Name, "role": u.Role},
	})
}

// Overlay bundles the overlay's dependencies.
type Overlay struct {
	store       *Store
	configStore configstore.ConfigStore
	authEnabled func() bool
}

func (o *Overlay) sessionExpiryHours() int {
	cfg, err := o.configStore.GetAuthConfig(context.Background())
	if err == nil && cfg != nil && cfg.SessionExpiryHours > 0 {
		return cfg.SessionExpiryHours
	}
	return 720
}
```

> `AuthConfig.SessionExpiryHours` does not exist in core and we are not adding it. `sessionExpiryHours()` falls back to 720; configurable expiry is read from a governance-config key by the auth-settings handler (Task 6) and could be surfaced here later. For Phase 1, 720 default + the settings endpoint writing the key is sufficient. (If you want live override now, read the `session_expiry_hours` key directly here via the store's DB.)

- [ ] **Step 2: Write the failing test**

`transports/bifrost-http/identity/middleware_test.go`:

```go
package identity

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/framework/encrypt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

func TestIdentityMiddleware_LoginIntercept(t *testing.T) {
	o := newOverlayUnderTest(t) // helper: Overlay over in-memory store, authEnabled=true
	ctx := context.Background()
	hash, _ := encrypt.Hash("secret123")
	require.NoError(t, o.store.CreateUser(ctx, &IdentityUser{ID: "a1", Email: "admin@localhost", Name: "A", Role: RoleAdmin, PasswordHash: &hash, IsActive: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}))

	coreCalled := false
	h := o.IdentityMiddleware()(func(c *fasthttp.RequestCtx) { coreCalled = true })

	rc := &fasthttp.RequestCtx{}
	rc.Request.Header.SetMethod("POST")
	rc.Request.SetRequestURI("/api/session/login")
	rc.Request.SetBody([]byte(`{"email":"admin@localhost","password":"secret123"}`))
	h(rc)

	assert.False(t, coreCalled) // core login handler bypassed
	assert.Equal(t, 200, rc.Response.StatusCode())
	assert.Contains(t, string(rc.Response.Body()), `"role":"admin"`)
}
```

Provide `newOverlayUnderTest` constructing `&Overlay{store: newStore(t)-equivalent, configStore: <in-memory RDBConfigStore or a small fake implementing CreateSession+GetAuthConfig>, authEnabled: func() bool { return true }}`. For `configStore.CreateSession`, a minimal fake that records the session is enough.

- [ ] **Step 3: Run test to verify it passes**

Run: `go test ./transports/bifrost-http/identity/ -run TestIdentityMiddleware_LoginIntercept -v`
Expected: PASS (core handler not called; 200 + role).

- [ ] **Step 4: Commit**

```bash
git add transports/bifrost-http/identity/middleware.go transports/bifrost-http/identity/middleware_test.go
git commit -m "feat(identity): login-intercept middleware + user-context attach"
```

---

## Task 5: RBAC middleware + permission map

**Files:**
- Modify: `transports/bifrost-http/identity/middleware.go` (append RBAC + permission map)
- Test: `transports/bifrost-http/identity/rbac_test.go`

- [ ] **Step 1: Add role rank, permission map, and the RBAC middleware**

Append to `transports/bifrost-http/identity/middleware.go`:

```go
func roleRank(role string) int {
	switch role {
	case RoleAdmin:
		return 3
	case RoleOperator:
		return 2
	case RoleViewer:
		return 1
	default:
		return 0
	}
}

// requiredRank maps (method, path) → minimum role rank. Fail-closed for unknown
// /api/* routes (mutation→admin, read→operator). Self user-routes return viewer
// rank so the handler can enforce self-vs-admin.
func requiredRank(method, path string) int {
	mutation := method != "GET"
	if path == "/api/users/me" {
		return roleRank(RoleViewer)
	}
	if strings.HasPrefix(path, "/api/users/") {
		if strings.HasSuffix(path, "/role") || strings.HasSuffix(path, "/active") {
			return roleRank(RoleAdmin)
		}
		return roleRank(RoleViewer) // handler enforces self-or-admin
	}
	for _, p := range []string{"/api/users", "/api/auth", "/api/config", "/api/scim"} {
		if path == p || strings.HasPrefix(path, p+"/") {
			return roleRank(RoleAdmin)
		}
	}
	for _, p := range []string{
		"/api/providers", "/api/keys", "/api/governance", "/api/mcp", "/api/mcp-logs",
		"/api/plugins", "/api/proxy-config", "/api/cache", "/api/models", "/api/pricing",
		"/api/prompt-repo", "/api/feature-flags", "/api/oauth",
	} {
		if path == p || strings.HasPrefix(path, p+"/") {
			if mutation {
				return roleRank(RoleOperator)
			}
			return roleRank(RoleViewer)
		}
	}
	if path == "/api/logs" || strings.HasPrefix(path, "/api/logs/") {
		return roleRank(RoleViewer)
	}
	if mutation {
		return roleRank(RoleAdmin)
	}
	return roleRank(RoleOperator)
}

// RBACMiddleware authorizes /api/* by role. Runs after IdentityMiddleware.
func (o *Overlay) RBACMiddleware() schemas.BifrostHTTPMiddleware {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) {
			if !o.authEnabled() {
				next(ctx)
				return
			}
			// Core marks local-admin (auth disabled / bootstrap) — bypass.
			if v, ok := ctx.UserValue(schemas.IsLocalAdminContextKey).(bool); ok && v {
				next(ctx)
				return
			}
			path := string(ctx.Path())
			if !strings.HasPrefix(path, "/api/") {
				next(ctx)
				return
			}
			u := userFrom(ctx)
			if u == nil {
				next(ctx) // public/whitelisted /api routes (login, health) carry no user
				return
			}
			if roleRank(u.Role) < requiredRank(string(ctx.Method()), path) {
				sendErr(ctx, fasthttp.StatusForbidden, "forbidden: this action requires a higher role")
				return
			}
			next(ctx)
		}
	}
}
```

- [ ] **Step 2: Write the failing test (matrix + security regression)**

`transports/bifrost-http/identity/rbac_test.go`:

```go
package identity

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func allowed(role, method, path string) bool { return roleRank(role) >= requiredRank(method, path) }

func TestRBACMatrix(t *testing.T) {
	cases := []struct {
		role, method, path string
		want               bool
	}{
		{RoleOperator, "GET", "/api/users", false},
		{RoleOperator, "POST", "/api/users", false},
		{RoleOperator, "PUT", "/api/config", false},        // SECURITY: cannot disable auth
		{RoleOperator, "POST", "/api/brand-new-thing", false}, // fail-closed
		{RoleAdmin, "POST", "/api/brand-new-thing", true},
		{RoleViewer, "GET", "/api/providers", true},
		{RoleViewer, "POST", "/api/providers", false},
		{RoleOperator, "POST", "/api/providers", true},
		{RoleViewer, "GET", "/api/users/me", true},
		{RoleOperator, "GET", "/api/users/u123", true},
		{RoleOperator, "PUT", "/api/users/u123/role", false},
		{RoleAdmin, "PUT", "/api/users/u123/role", true},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, allowed(c.role, c.method, c.path), "%s %s %s", c.role, c.method, c.path)
	}
}
```

- [ ] **Step 3: Run test to verify it passes**

Run: `go test ./transports/bifrost-http/identity/ -run TestRBACMatrix -v`
Expected: PASS (all cases, incl. operator→`/api/config` denied).

- [ ] **Step 4: Commit**

```bash
git add transports/bifrost-http/identity/middleware.go transports/bifrost-http/identity/rbac_test.go
git commit -m "feat(identity): RBAC middleware with fail-closed permission map"
```

---

## Task 6: Handlers + Wire() (routes + migration)

**Files:**
- Create: `transports/bifrost-http/identity/handlers.go`
- Modify: `transports/bifrost-http/identity/wire.go` (fill in `Middlewares` + `Wire`)
- Test: `transports/bifrost-http/identity/handlers_test.go`

- [ ] **Step 1: Write the handlers**

`transports/bifrost-http/identity/handlers.go` — `/api/users*`, `/api/users/me`, `/api/auth/settings`. Uses `o.store`. Self-vs-admin enforced here (middleware lets self-routes through):

```go
package identity

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
)

func (o *Overlay) listUsers(ctx *fasthttp.RequestCtx) {
	users, err := o.store.ListUsers(ctx)
	if err != nil {
		sendErr(ctx, 500, "failed to list users")
		return
	}
	sendJSON(ctx, 200, map[string]any{"users": users})
}

func (o *Overlay) me(ctx *fasthttp.RequestCtx) {
	if u := userFrom(ctx); u != nil {
		sendJSON(ctx, 200, u)
		return
	}
	sendErr(ctx, 401, "no authenticated user")
}

func (o *Overlay) createUser(ctx *fasthttp.RequestCtx) {
	var p struct{ Email, Name, Role, Password string }
	if err := json.Unmarshal(ctx.PostBody(), &p); err != nil {
		sendErr(ctx, 400, "invalid request format")
		return
	}
	email := strings.ToLower(strings.TrimSpace(p.Email))
	if email == "" || !strings.Contains(email, "@") {
		sendErr(ctx, 400, "invalid email")
		return
	}
	if !ValidRoles[p.Role] {
		sendErr(ctx, 400, "role must be one of: admin, operator, viewer")
		return
	}
	if len(p.Password) < 8 {
		sendErr(ctx, 400, "password must be at least 8 characters")
		return
	}
	if ex, _ := o.store.GetUserByEmail(ctx, email); ex != nil {
		sendErr(ctx, 409, "email already in use")
		return
	}
	hash, err := hashPassword(p.Password)
	if err != nil {
		sendErr(ctx, 500, "failed to hash password")
		return
	}
	now := time.Now()
	u := &IdentityUser{ID: uuid.New().String(), Email: email, Name: p.Name, Role: p.Role, PasswordHash: &hash, IsActive: true, CreatedAt: now, UpdatedAt: now}
	if err := o.store.CreateUser(ctx, u); err != nil {
		sendErr(ctx, 500, "failed to create user")
		return
	}
	sendJSON(ctx, 200, u)
}

func (o *Overlay) getUser(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	if !o.selfOrAdmin(ctx, id) {
		sendErr(ctx, 403, "forbidden: admin or self only")
		return
	}
	u, err := o.store.GetUserByID(ctx, id)
	if err != nil || u == nil {
		sendErr(ctx, 404, "user not found")
		return
	}
	sendJSON(ctx, 200, u)
}

func (o *Overlay) updateUser(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	if !o.selfOrAdmin(ctx, id) {
		sendErr(ctx, 403, "forbidden: admin or self only")
		return
	}
	u, err := o.store.GetUserByID(ctx, id)
	if err != nil || u == nil {
		sendErr(ctx, 404, "user not found")
		return
	}
	var p struct{ Name, Email string } // role/is_active are NOT mutable here
	if err := json.Unmarshal(ctx.PostBody(), &p); err != nil {
		sendErr(ctx, 400, "invalid request format")
		return
	}
	if p.Name != "" {
		u.Name = p.Name
	}
	if p.Email != "" {
		email := strings.ToLower(strings.TrimSpace(p.Email))
		if other, _ := o.store.GetUserByEmail(ctx, email); other != nil && other.ID != u.ID {
			sendErr(ctx, 409, "email already in use")
			return
		}
		u.Email = email
	}
	if err := o.store.UpdateUser(ctx, u); err != nil {
		sendErr(ctx, 500, "failed to update user")
		return
	}
	sendJSON(ctx, 200, u)
}

func (o *Overlay) setRole(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	if c := userFrom(ctx); c != nil && c.ID == id {
		sendErr(ctx, 400, "cannot change your own role")
		return
	}
	var p struct{ Role string }
	if err := json.Unmarshal(ctx.PostBody(), &p); err != nil || !ValidRoles[p.Role] {
		sendErr(ctx, 400, "role must be one of: admin, operator, viewer")
		return
	}
	u, err := o.store.GetUserByID(ctx, id)
	if err != nil || u == nil {
		sendErr(ctx, 404, "user not found")
		return
	}
	if u.Role == RoleAdmin && p.Role != RoleAdmin {
		if n, _ := o.store.CountActiveAdmins(ctx); n <= 1 {
			sendErr(ctx, 400, "cannot remove the last admin")
			return
		}
	}
	u.Role = p.Role
	if err := o.store.UpdateUser(ctx, u); err != nil {
		sendErr(ctx, 500, "failed to update role")
		return
	}
	sendJSON(ctx, 200, u)
}

func (o *Overlay) setActive(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	if c := userFrom(ctx); c != nil && c.ID == id {
		sendErr(ctx, 400, "cannot deactivate yourself")
		return
	}
	var p struct{ Active bool }
	if err := json.Unmarshal(ctx.PostBody(), &p); err != nil {
		sendErr(ctx, 400, "invalid request format")
		return
	}
	u, err := o.store.GetUserByID(ctx, id)
	if err != nil || u == nil {
		sendErr(ctx, 404, "user not found")
		return
	}
	if !p.Active && u.Role == RoleAdmin {
		if n, _ := o.store.CountActiveAdmins(ctx); n <= 1 {
			sendErr(ctx, 400, "cannot deactivate the last admin")
			return
		}
	}
	u.IsActive = p.Active
	if err := o.store.UpdateUser(ctx, u); err != nil {
		sendErr(ctx, 500, "failed to update user")
		return
	}
	sendJSON(ctx, 200, u)
}

func (o *Overlay) setPassword(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	caller := userFrom(ctx)
	isAdmin := caller == nil || caller.Role == RoleAdmin
	isSelf := caller != nil && caller.ID == id
	if !isAdmin && !isSelf {
		sendErr(ctx, 403, "forbidden: admin or self only")
		return
	}
	u, err := o.store.GetUserByID(ctx, id)
	if err != nil || u == nil {
		sendErr(ctx, 404, "user not found")
		return
	}
	var p struct{ CurrentPassword, NewPassword string }
	if err := json.Unmarshal(ctx.PostBody(), &p); err != nil {
		sendErr(ctx, 400, "invalid request format")
		return
	}
	if len(p.NewPassword) < 8 {
		sendErr(ctx, 400, "password must be at least 8 characters")
		return
	}
	if isSelf && !isAdmin {
		if u.PasswordHash == nil {
			sendErr(ctx, 400, "no password set")
			return
		}
		ok, err := compareHash(*u.PasswordHash, p.CurrentPassword)
		if err != nil {
			sendErr(ctx, 500, "error")
			return
		}
		if !ok {
			sendErr(ctx, 401, "current password is incorrect")
			return
		}
	}
	hash, err := hashPassword(p.NewPassword)
	if err != nil {
		sendErr(ctx, 500, "failed to hash password")
		return
	}
	u.PasswordHash = &hash
	if err := o.store.UpdateUser(ctx, u); err != nil {
		sendErr(ctx, 500, "failed to update password")
		return
	}
	_ = o.store.UnmapAllForUser(ctx, u.ID) // invalidate this user's sessions
	sendJSON(ctx, 200, map[string]any{"message": "password updated"})
}

func (o *Overlay) selfOrAdmin(ctx *fasthttp.RequestCtx, id string) bool {
	c := userFrom(ctx)
	return c == nil || c.Role == RoleAdmin || c.ID == id
}
```

Add `hashPassword`/`compareHash` thin wrappers in `middleware.go` (so `encrypt` stays imported once):

```go
func hashPassword(pw string) (string, error)     { return encrypt.Hash(pw) }
func compareHash(hash, pw string) (bool, error)  { return encrypt.CompareHash(hash, pw) }
```

- [ ] **Step 2: Fill in `Middlewares` and `Wire`**

Replace `transports/bifrost-http/identity/wire.go` bodies:

```go
package identity

import (
	"context"
	"fmt"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
)

// dbFromStore extracts the live *gorm.DB from the concrete RDBConfigStore.
func newOverlay(store configstore.ConfigStore, authEnabled func() bool) (*Overlay, error) {
	rdb, ok := store.(*configstore.RDBConfigStore)
	if !ok {
		return nil, fmt.Errorf("identity overlay requires *RDBConfigStore, got %T", store)
	}
	return &Overlay{store: NewStore(rdb.DB()), configStore: store, authEnabled: authEnabled}, nil
}

func Middlewares(store configstore.ConfigStore, authEnabled func() bool) []schemas.BifrostHTTPMiddleware {
	o, err := newOverlay(store, authEnabled)
	if err != nil {
		return nil // store not RDB-backed (e.g. some tests) → overlay inactive
	}
	return []schemas.BifrostHTTPMiddleware{o.IdentityMiddleware(), o.RBACMiddleware()}
}

func Wire(ctx context.Context, r *router.Router, store configstore.ConfigStore) error {
	// 1. Migration (own tables) via the core-provided throwaway connection.
	if err := store.RunMigration(ctx, func(ctx context.Context, db *gorm.DB) error {
		return Migrate(ctx, db)
	}); err != nil {
		return fmt.Errorf("identity migration failed: %w", err)
	}
	// 2. Routes on the shared router.
	o, err := newOverlay(store, func() bool { return true })
	if err != nil {
		return err
	}
	mw := []schemas.BifrostHTTPMiddleware{} // routes inherit the chain already applied to /api/*
	r.GET("/api/users", lib.ChainMiddlewares(o.listUsers, mw...))
	r.POST("/api/users", lib.ChainMiddlewares(o.createUser, mw...))
	r.GET("/api/users/me", lib.ChainMiddlewares(o.me, mw...))
	r.GET("/api/users/{id}", lib.ChainMiddlewares(o.getUser, mw...))
	r.PUT("/api/users/{id}", lib.ChainMiddlewares(o.updateUser, mw...))
	r.PUT("/api/users/{id}/role", lib.ChainMiddlewares(o.setRole, mw...))
	r.PUT("/api/users/{id}/password", lib.ChainMiddlewares(o.setPassword, mw...))
	r.PUT("/api/users/{id}/active", lib.ChainMiddlewares(o.setActive, mw...))
	r.GET("/api/auth/settings", lib.ChainMiddlewares(o.getAuthSettings, mw...))
	r.PUT("/api/auth/settings", lib.ChainMiddlewares(o.putAuthSettings, mw...))
	return nil
}
```

Add the `gorm.io/gorm` import to `wire.go`. Add `getAuthSettings`/`putAuthSettings` to `handlers.go` reading/writing the `session_expiry_hours` governance-config key via `o.configStore.UpdateConfig` (key/value) — validate 1..8760, admin-gated by the permission map.

> **Route inheritance note:** routes registered in `Wire` are added to `s.Router` directly. They are NOT wrapped by `apiMiddlewares` (those wrap only the handlers registered inside `RegisterAPIRoutes`). Therefore the overlay routes must be wrapped explicitly: pass the overlay's own `IdentityMiddleware()`+`RBACMiddleware()` (and the core `commonMiddlewares` if needed) as `mw` here, OR register them from inside the chain. **Decision for this plan:** wrap overlay routes with the overlay middlewares so `/api/users` etc. are authenticated+authorized consistently. Update `mw` to `o.IdentityMiddleware(), o.RBACMiddleware()` and ensure the user is attached (IdentityMiddleware needs the token, which it reads from the request — works standalone). Confirm at execution that the core `AuthMiddleware` also wraps these (if not, the overlay's own token check still gates them).

- [ ] **Step 3: Write the failing test (guards)**

`transports/bifrost-http/identity/handlers_test.go`: cover create guards (short password, bad role, duplicate email), last-admin demotion blocked, self-deactivate blocked, and that `updateUser` ignores a `role` field. Use the in-memory store + set `ctxKeyUser` on the request ctx + set `{id}` via `ctx.SetUserValue("id", ...)`.

```go
func TestCreateUser_Guards(t *testing.T) {
	o := newOverlayUnderTest(t)
	rc := &fasthttp.RequestCtx{}
	rc.Request.SetBody([]byte(`{"email":"b@x.com","name":"B","role":"viewer","password":"short"}`))
	rc.SetUserValue(ctxKeyUser, &IdentityUser{Role: RoleAdmin})
	o.createUser(rc)
	assert.Equal(t, 400, rc.Response.StatusCode())
}
```

- [ ] **Step 4: Run tests + build**

Run: `go test ./transports/bifrost-http/identity/ -v` then `go build ./...`
Expected: PASS, clean build (overlay fully wired).

- [ ] **Step 5: Commit**

```bash
git add transports/bifrost-http/identity/handlers.go transports/bifrost-http/identity/wire.go transports/bifrost-http/identity/handlers_test.go
git commit -m "feat(identity): user + auth-settings handlers, route + migration wiring"
```

---

## Task 7: Viewer key masking [FORK PATCH #2]

**Files:**
- Modify: `transports/bifrost-http/handlers/providers.go`
- Test: `transports/bifrost-http/handlers/providers_test.go`

> This is the one place the overlay must transform core handler output, so it is a documented carry-patch (not in the `identity` package). Keep the diff minimal and recorded in `FORK_PATCHES.md`.

- [ ] **Step 1: Read the provider/key list+get responses**

Open `providers.go`, find the handlers that serialize key values (list keys / get provider). Identify the response struct + the field carrying the raw key value.

- [ ] **Step 2: Mask when the request user is a viewer**

The overlay exposes a helper (add to `identity/middleware.go`, exported):

```go
// IsViewer reports whether the request's overlay user is a viewer.
func IsViewer(ctx *fasthttp.RequestCtx) bool {
	u := userFrom(ctx)
	return u != nil && u.Role == RoleViewer
}

// MaskSecret returns a masked form (last 4 chars).
func MaskSecret(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return "****" + s[len(s)-4:]
}
```

In the provider/key response handler, before sending, add:

```go
	// FORK PATCH #2: hide raw key values from viewers (see FORK_PATCHES.md)
	if identity.IsViewer(ctx) {
		for i := range resp.Keys { // adapt to the real struct/field
			resp.Keys[i].Value = identity.MaskSecret(resp.Keys[i].Value)
		}
	}
```

Add the `identity` import to `providers.go`.

- [ ] **Step 3: Write + run the test**

In `providers_test.go`, assert a viewer-in-context request returns `****<last4>` and not the raw value; an operator gets the full value.
Run: `go test ./transports/bifrost-http/handlers/ -run TestKeys_MaskedForViewer -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add transports/bifrost-http/handlers/providers.go transports/bifrost-http/handlers/providers_test.go transports/bifrost-http/identity/middleware.go FORK_PATCHES.md
git commit -m "feat(identity): mask raw key values from viewers [fork patch]"
```

---

## Task 8: UI — email login + role gating [FORK PATCH #3]

**Files:**
- Modify: login form component in `ui/`; dashboard shell.

- [ ] **Step 1:** In the login form, change the field + request body key `username` → `email`, label "Email". Body becomes `{ "email", "password" }`.
- [ ] **Step 2:** On dashboard load, call `GET /api/users/me`; store `role`.
- [ ] **Step 3:** Hide/disable mutating controls when `role === 'viewer'`; hide user-management + auth-settings UI unless `role === 'admin'`. (Cosmetic — server enforces.)
- [ ] **Step 4:** `cd ui && npm run build` → succeeds.
- [ ] **Step 5:** Commit:

```bash
git add ui/ FORK_PATCHES.md
git commit -m "feat(ui): email login + role-aware controls [fork patch]"
```

---

## Task 9: Full verification + fork-patch audit

- [ ] **Step 1: Run suites**

```bash
go test ./transports/bifrost-http/identity/... ./transports/bifrost-http/handlers/...
go build ./...
cd ui && npm run build
```
Expected: all PASS, clean build.

- [ ] **Step 2: Manual upgrade smoke**

Seed a DB with legacy `admin_username`/`admin_password`, boot once → confirm: overlay migration runs; existing admin logs in with `admin@localhost` + old password; a pre-existing session still works (session backfilled into `identity_sessions`); admin creates operator + viewer; operator → 403 on `/api/users`; operator → 403 on `PUT /api/config`; viewer → 403 on mutations + masked keys.

- [ ] **Step 3: Confirm the core footprint is exactly the 3 patches**

```bash
git diff --stat main -- ':!transports/bifrost-http/identity' ':!docs'
```
Expected: only `server.go` (Patch #1), `providers.go` (Patch #2), `ui/` (Patch #3), and `FORK_PATCHES.md`. If anything else in core changed, reconsider — the overlay goal is violated.

- [ ] **Step 4: Commit any doc updates**

```bash
git add FORK_PATCHES.md
git commit -m "docs: finalize fork patch audit for phase 1 identity overlay"
```

---

## Self-Review (against the spec + overlay goal)

**Spec coverage:** users + migration → Task 2; store/CRUD → Task 3; email login (no enumeration, configurable expiry) → Task 4; RBAC role map incl. admin-only `/api/config` + fail-closed + self carve-out → Task 5; user/auth-settings API → Task 6; viewer key masking → Task 7; UI → Task 8; "don't log out existing admin" → Task 2 session backfill into `identity_sessions`. Inference exemption → RBAC only touches `/api/*`, never `/v1/*`.

**Overlay goal:** core footprint reduced to 3 documented patches (`server.go` 2 lines, `providers.go` masking, `ui/`); no edits to core `sessions.go`, `ConfigStore` interface, `middlewares.go`, or `session.go`. `IsEnterprise` untouched.

**Known trade-offs / execution-time confirmations:**
- `Middlewares`/`Wire` type-assert `ConfigStore` → `*RDBConfigStore`; if a deployment uses a different store, the overlay is inactive (returns nil) — acceptable for this fork, but confirm the runtime store is RDB-backed.
- Overlay-route middleware wrapping (Task 6 Step 2 note) must be confirmed against how `apiMiddlewares` actually reach `s.Router`-registered routes; wrap overlay routes with the overlay middlewares to be safe.
- Self password-change clears all that user's session maps (not all-but-current) — matches the prior plan's accepted simplification.
- Task 7 field names + Task 8 component path require in-repo confirmation at execution (flagged in those tasks).
- Logout: core's logout deletes the core session; the `identity_sessions` row is left orphaned (harmless; optionally GC later or intercept logout in `IdentityMiddleware`).
