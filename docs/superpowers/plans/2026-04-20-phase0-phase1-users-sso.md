# Phase 0 + Phase 1 (Users + SSO) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Unblock Teams UI (Phase 0), add persistent user management with per-user budget/rate limits, and enable Generic OIDC SSO login (Okta + Entra) with auto-provisioning.

**Architecture:** Three sequential layers — (1) DB tables + migrations, (2) ConfigStore + HTTP handlers, (3) Frontend. SSO handler lives in transport alongside `SessionHandler`. The governance plugin's inference-time in-memory store is synced via a `UserGovernanceSync` interface passed to the users handler — the plugin is not modified, only called. All new tables use `governance_` prefix.

**Tech Stack:** Go 1.26.2, fasthttp, GORM (SQLite dev / Postgres prod), React 18, RTK Query, TypeScript, `github.com/go-jose/go-jose/v3` for JWT/JWKS, OIDC Discovery for provider-agnostic endpoint resolution.

---

## File Map

### New files
| File | Responsibility |
|------|---------------|
| `framework/configstore/tables/governance_users.go` | `GovernanceUsersTable` GORM struct |
| `framework/configstore/tables/governance_sso_configs.go` | `GovernanceSSOConfigsTable` GORM struct (encrypted secret) |
| `framework/configstore/tables/governance_sso_nonces.go` | `GovernanceSSONoncesTable` GORM struct |
| `transports/bifrost-http/handlers/governance_users.go` | HTTP CRUD handlers for `/api/governance/users` |
| `transports/bifrost-http/handlers/sso.go` | `SSOHandler`: PKCE initiate + callback, JWKS cache |
| `transports/bifrost-http/handlers/sso_adapters.go` | `OIDCProvider` interface + `OktaAdapter` + `EntraAdapter` |

### Modified files
| File | Change |
|------|--------|
| `framework/configstore/migrations.go` | Add 4 migration functions + calls in `triggerMigrations` |
| `framework/configstore/store.go` | Add user + SSO methods to `ConfigStore` interface |
| `framework/configstore/rdb.go` | Implement user + SSO methods on `RDBConfigStore` |
| `framework/configstore/tables/sessions.go` | Add `UserID *string` + `AuthMethod string` fields |
| `transports/bifrost-http/handlers/session.go` | Add `sso_enabled` field to `isAuthEnabled` response |
| `transports/bifrost-http/server/server.go` | Register `GovernanceUsersHandler` + `SSOHandler` routes |
| `ui/app/workspace/governance/teams/page.tsx` | Replace `@enterprise` import with real components |
| `ui/app/workspace/scim/page.tsx` | Add tabs: SSO / IdP Settings + SCIM |
| `ui/lib/store/apis/governanceApi.ts` | Add users + SSO config RTK Query endpoints |
| `ui/lib/types/governance.ts` | Add `GovernanceUser`, `SSOConfig` types |

---

## Task 0: Wire Teams UI (Phase 0)

**Files:**
- Modify: `ui/app/workspace/governance/teams/page.tsx`

- [ ] **Step 1: Check what TeamsTable needs from its parent**

Read the TeamsTable component to understand what data the parent page must fetch:

```bash
grep -n "TeamsTableProps\|interface\|export default" ui/app/workspace/governance/views/teamsTable.tsx | head -20
```

Look at how `ui/app/workspace/governance/customers/page.tsx` uses `useGetTeamsQuery` as a reference.

- [ ] **Step 2: Replace the @enterprise fallback in teams/page.tsx**

```tsx
"use client";

import { useState } from "react";
import TeamsTable from "@/app/workspace/governance/views/teamsTable";
import TeamDialog from "@/app/workspace/governance/views/teamDialog";
import {
  useGetTeamsQuery,
  useGetCustomersQuery,
  useGetVirtualKeysQuery,
} from "@/lib/store";

export default function GovernanceTeamsPage() {
  const [search, setSearch] = useState("");
  const [debouncedSearch, setDebouncedSearch] = useState("");
  const [offset, setOffset] = useState(0);
  const limit = 20;

  const { data: teamsData, isLoading } = useGetTeamsQuery({
    search: debouncedSearch,
    limit,
    offset,
  });
  const { data: customersData } = useGetCustomersQuery();
  const { data: vkData } = useGetVirtualKeysQuery();

  if (isLoading) return <div className="p-6">Loading...</div>;

  return (
    <div className="mx-auto w-full max-w-7xl">
      <TeamsTable
        teams={teamsData?.teams ?? []}
        totalCount={teamsData?.total_count ?? 0}
        customers={customersData?.customers ?? []}
        virtualKeys={vkData?.virtual_keys ?? []}
        search={search}
        debouncedSearch={debouncedSearch}
        onSearchChange={(v) => {
          setSearch(v);
          setDebouncedSearch(v);
        }}
        offset={offset}
        limit={limit}
        onOffsetChange={setOffset}
      />
    </div>
  );
}
```

> Note: if `TeamsTable`'s `TeamsTableProps` differs from what you see here, adjust to match actual interface.

- [ ] **Step 3: Start dev server and verify Teams page loads**

```bash
cd ui && npm run dev
```

Navigate to `/workspace/governance/teams`. Verify: team list renders, create/edit/delete work, no `@enterprise` import error in console.

- [ ] **Step 4: Commit**

```bash
git add ui/app/workspace/governance/teams/page.tsx
git commit -m "feat(teams): wire existing TeamsTable into teams page, remove @enterprise fallback"
```

---

## Task 1: governance_users Table

**Files:**
- Create: `framework/configstore/tables/governance_users.go`
- Modify: `framework/configstore/migrations.go`

- [ ] **Step 1: Write the table struct**

```go
// framework/configstore/tables/governance_users.go
package tables

import "time"

// GovernanceUsersTable stores dashboard users (SSO or password) with optional
// budget/rate-limit assignment for inference-pipeline quota tracking.
type GovernanceUsersTable struct {
	ID          string  `gorm:"primaryKey;type:varchar(255)" json:"id"`
	Email       string  `gorm:"type:text;not null;uniqueIndex" json:"email"`
	Name        string  `gorm:"type:text;not null;default:''" json:"name"`
	TeamID      *string `gorm:"type:varchar(255);index" json:"team_id,omitempty"`
	BudgetID    *string `gorm:"type:varchar(255)" json:"budget_id,omitempty"`
	RateLimitID *string `gorm:"type:varchar(255)" json:"rate_limit_id,omitempty"`
	AuthMethod  string  `gorm:"type:varchar(20);not null;default:'password'" json:"auth_method"`
	CreatedAt   time.Time `gorm:"index;not null" json:"created_at"`
	UpdatedAt   time.Time `gorm:"index;not null" json:"updated_at"`
}

func (GovernanceUsersTable) TableName() string { return "governance_users" }
```

- [ ] **Step 2: Write the migration function**

At the end of `framework/configstore/migrations.go`, add:

```go
func migrationAddGovernanceUsersTable(ctx context.Context, db *gorm.DB) error {
	m := migrator.New(db, migrator.DefaultOptions, []*migrator.Migration{{
		ID: "add_governance_users_table",
		Migrate: func(tx *gorm.DB) error {
			tx = tx.WithContext(ctx)
			if !tx.Migrator().HasTable(&tables.GovernanceUsersTable{}) {
				return tx.Migrator().CreateTable(&tables.GovernanceUsersTable{})
			}
			return tx.AutoMigrate(&tables.GovernanceUsersTable{})
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.WithContext(ctx).Migrator().DropTable(&tables.GovernanceUsersTable{})
		},
	}})
	if err := m.Migrate(); err != nil {
		return fmt.Errorf("error running add_governance_users_table migration: %w", err)
	}
	return nil
}
```

- [ ] **Step 3: Call migration from triggerMigrations**

In `framework/configstore/migrations.go`, find the last `if err :=` call in `triggerMigrations` (currently `migrationAddGuardrailProfileTimeout`) and add after it:

```go
	if err := migrationAddGovernanceUsersTable(ctx, db); err != nil {
		return err
	}
```

- [ ] **Step 4: Verify it compiles**

```bash
cd framework && go build ./...
```

Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add framework/configstore/tables/governance_users.go framework/configstore/migrations.go
git commit -m "feat(db): add governance_users table and migration"
```

---

## Task 2: governance_sso_configs + governance_sso_nonces Tables

**Files:**
- Create: `framework/configstore/tables/governance_sso_configs.go`
- Create: `framework/configstore/tables/governance_sso_nonces.go`
- Modify: `framework/configstore/migrations.go`

- [ ] **Step 1: Write governance_sso_configs struct**

```go
// framework/configstore/tables/governance_sso_configs.go
package tables

import (
	"fmt"
	"time"

	"github.com/maximhq/bifrost/framework/encrypt"
	"gorm.io/gorm"
)

type GovernanceSSOConfigsTable struct {
	ID             string    `gorm:"primaryKey;type:varchar(255)" json:"id"`
	Provider       string    `gorm:"type:varchar(50);not null" json:"provider"`
	IssuerURL      string    `gorm:"type:text;not null" json:"issuer_url"`
	ClientID       string    `gorm:"type:text;not null" json:"client_id"`
	ClientSecret   string    `gorm:"type:text;not null" json:"-"`
	RoleClaimKey   string    `gorm:"type:varchar(255);not null;default:''" json:"role_claim_key"`
	GroupClaimKey  string    `gorm:"type:varchar(255);not null;default:''" json:"group_claim_key"`
	Enabled        bool      `gorm:"not null;default:false" json:"enabled"`
	EncryptionStatus string  `gorm:"type:varchar(20);default:'plain_text'" json:"-"`
	CreatedAt      time.Time `gorm:"index;not null" json:"created_at"`
	UpdatedAt      time.Time `gorm:"index;not null" json:"updated_at"`
}

func (GovernanceSSOConfigsTable) TableName() string { return "governance_sso_configs" }

func (s *GovernanceSSOConfigsTable) BeforeSave(tx *gorm.DB) error {
	if encrypt.IsEnabled() && s.ClientSecret != "" {
		if err := encryptString(&s.ClientSecret); err != nil {
			return fmt.Errorf("failed to encrypt sso client secret: %w", err)
		}
		s.EncryptionStatus = EncryptionStatusEncrypted
	}
	return nil
}

func (s *GovernanceSSOConfigsTable) AfterFind(tx *gorm.DB) error {
	if s.EncryptionStatus == EncryptionStatusEncrypted {
		if err := decryptString(&s.ClientSecret); err != nil {
			return fmt.Errorf("failed to decrypt sso client secret: %w", err)
		}
	}
	return nil
}
```

> `encryptString`, `decryptString`, and `EncryptionStatusEncrypted` are already defined in the `tables` package (used by `sessions.go`). No need to re-declare.

- [ ] **Step 2: Write governance_sso_nonces struct**

```go
// framework/configstore/tables/governance_sso_nonces.go
package tables

import "time"

type GovernanceSSONoncesTable struct {
	State        string    `gorm:"primaryKey;type:varchar(255)" json:"state"`
	CodeVerifier string    `gorm:"type:text;not null" json:"code_verifier"`
	Nonce        string    `gorm:"type:varchar(255);not null" json:"nonce"` // added to auth URL, verified in id_token
	Provider     string    `gorm:"type:varchar(50);not null" json:"provider"`
	ExpiresAt    time.Time `gorm:"index;not null" json:"expires_at"`
}

func (GovernanceSSONoncesTable) TableName() string { return "governance_sso_nonces" }
```

- [ ] **Step 3: Write the combined migration function**

At the end of `framework/configstore/migrations.go`, add:

```go
func migrationAddGovernanceSSOTables(ctx context.Context, db *gorm.DB) error {
	m := migrator.New(db, migrator.DefaultOptions, []*migrator.Migration{{
		ID: "add_governance_sso_tables",
		Migrate: func(tx *gorm.DB) error {
			tx = tx.WithContext(ctx)
			mgr := tx.Migrator()
			if !mgr.HasTable(&tables.GovernanceSSOConfigsTable{}) {
				if err := mgr.CreateTable(&tables.GovernanceSSOConfigsTable{}); err != nil {
					return err
				}
			}
			if !mgr.HasTable(&tables.GovernanceSSONoncesTable{}) {
				if err := mgr.CreateTable(&tables.GovernanceSSONoncesTable{}); err != nil {
					return err
				}
			}
			return nil
		},
		Rollback: func(tx *gorm.DB) error {
			tx = tx.WithContext(ctx)
			_ = tx.Migrator().DropTable(&tables.GovernanceSSONoncesTable{})
			_ = tx.Migrator().DropTable(&tables.GovernanceSSOConfigsTable{})
			return nil
		},
	}})
	if err := m.Migrate(); err != nil {
		return fmt.Errorf("error running add_governance_sso_tables migration: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Call migration from triggerMigrations**

After the `migrationAddGovernanceUsersTable` call:

```go
	if err := migrationAddGovernanceSSOTables(ctx, db); err != nil {
		return err
	}
```

- [ ] **Step 5: Write migration for sessions columns**

```go
func migrationAddUserIDToSessions(ctx context.Context, db *gorm.DB) error {
	m := migrator.New(db, migrator.DefaultOptions, []*migrator.Migration{{
		ID: "add_user_id_auth_method_to_sessions",
		Migrate: func(tx *gorm.DB) error {
			tx = tx.WithContext(ctx)
			mgr := tx.Migrator()
			if !mgr.HasColumn(&tables.SessionsTable{}, "user_id") {
				if err := mgr.AddColumn(&tables.SessionsTable{}, "user_id"); err != nil {
					return err
				}
			}
			if !mgr.HasColumn(&tables.SessionsTable{}, "auth_method") {
				if err := mgr.AddColumn(&tables.SessionsTable{}, "auth_method"); err != nil {
					return err
				}
			}
			return nil
		},
		Rollback: func(tx *gorm.DB) error {
			tx = tx.WithContext(ctx)
			mgr := tx.Migrator()
			_ = mgr.DropColumn(&tables.SessionsTable{}, "auth_method")
			_ = mgr.DropColumn(&tables.SessionsTable{}, "user_id")
			return nil
		},
	}})
	if err := m.Migrate(); err != nil {
		return fmt.Errorf("error running add_user_id_auth_method_to_sessions migration: %w", err)
	}
	return nil
}
```

Call it after `migrationAddGovernanceSSOTables`:

```go
	if err := migrationAddUserIDToSessions(ctx, db); err != nil {
		return err
	}
```

- [ ] **Step 6: Add fields to SessionsTable struct**

In `framework/configstore/tables/sessions.go`, add two fields to `SessionsTable`:

```go
type SessionsTable struct {
	ID               int       `gorm:"primaryKey;autoIncrement" json:"id"`
	Token            string    `gorm:"type:text;not null;uniqueIndex" json:"token"`
	ExpiresAt        time.Time `gorm:"index;not null" json:"expires_at,omitempty"`
	CreatedAt        time.Time `gorm:"index;not null" json:"created_at"`
	UpdatedAt        time.Time `gorm:"index;not null" json:"updated_at"`
	EncryptionStatus string    `gorm:"type:varchar(20);default:'plain_text'" json:"-"`
	TokenHash        string    `gorm:"type:varchar(64);index:idx_session_token_hash,unique" json:"-"`
	UserID           *string   `gorm:"type:varchar(255);index" json:"user_id,omitempty"`
	AuthMethod       string    `gorm:"type:varchar(20);not null;default:'password'" json:"auth_method"`
}
```

- [ ] **Step 7: Verify compilation**

```bash
cd framework && go build ./...
```

Expected: no errors.

- [ ] **Step 8: Commit**

```bash
git add framework/configstore/tables/governance_sso_configs.go \
        framework/configstore/tables/governance_sso_nonces.go \
        framework/configstore/tables/sessions.go \
        framework/configstore/migrations.go
git commit -m "feat(db): add governance_sso_configs, governance_sso_nonces tables and sessions user_id/auth_method columns"
```

---

## Task 3: ConfigStore Interface + RDB Implementation for Users

**Files:**
- Modify: `framework/configstore/store.go`
- Modify: `framework/configstore/rdb.go`

- [ ] **Step 1: Write the test first**

Create `framework/configstore/governance_users_test.go`:

```go
package configstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupUsersTestDB(t *testing.T) *configstore.RDBConfigStore {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	err = db.AutoMigrate(
		&tables.GovernanceUsersTable{},
		&tables.TableBudget{},
		&tables.TableRateLimit{},
	)
	require.NoError(t, err)
	return configstore.NewRDBConfigStoreForTest(db)
}

func TestCreateAndGetUser(t *testing.T) {
	store := setupUsersTestDB(t)
	ctx := context.Background()

	user := &tables.GovernanceUsersTable{
		ID:         "user-1",
		Email:      "alice@example.com",
		Name:       "Alice",
		AuthMethod: "password",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	require.NoError(t, store.CreateUser(ctx, user))

	got, err := store.GetUserByEmail(ctx, "alice@example.com")
	require.NoError(t, err)
	assert.Equal(t, "Alice", got.Name)
}

func TestUpsertUserByEmail_CreateOnFirstLogin(t *testing.T) {
	store := setupUsersTestDB(t)
	ctx := context.Background()

	user, err := store.UpsertUserByEmail(ctx, "bob@example.com", "Bob", "oidc")
	require.NoError(t, err)
	assert.NotEmpty(t, user.ID)
	assert.Equal(t, "bob@example.com", user.Email)
	assert.Equal(t, "oidc", user.AuthMethod)
}

func TestUpsertUserByEmail_UpdateNameOnSubsequentLogin(t *testing.T) {
	store := setupUsersTestDB(t)
	ctx := context.Background()

	first, err := store.UpsertUserByEmail(ctx, "carol@example.com", "Carol", "oidc")
	require.NoError(t, err)

	second, err := store.UpsertUserByEmail(ctx, "carol@example.com", "Carol Updated", "oidc")
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID)
	assert.Equal(t, "Carol Updated", second.Name)
}

func TestListUsers_Search(t *testing.T) {
	store := setupUsersTestDB(t)
	ctx := context.Background()

	for _, email := range []string{"alice@x.com", "bob@x.com", "charlie@x.com"} {
		require.NoError(t, store.UpsertUserByEmail(ctx, email, email, "password"))
	}

	users, total, err := store.ListUsers(ctx, "alice", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "alice@x.com", users[0].Email)
}

func TestDeleteUser_CascadesBudget(t *testing.T) {
	store := setupUsersTestDB(t)
	ctx := context.Background()

	user, err := store.UpsertUserByEmail(ctx, "dave@x.com", "Dave", "password")
	require.NoError(t, err)

	// Assign a budget directly via DB
	budgetID := "bud-1"
	// (In real flow the handler creates the budget first; here we simulate it)
	user.BudgetID = &budgetID
	require.NoError(t, store.UpdateUser(ctx, user.ID, map[string]any{"budget_id": budgetID}))

	require.NoError(t, store.DeleteUser(ctx, user.ID))

	_, err = store.GetUser(ctx, user.ID)
	assert.Error(t, err)
}
```

- [ ] **Step 2: Run the test — expect compile failure**

```bash
cd framework && go test ./configstore/... -run TestCreateAndGetUser -v 2>&1 | head -20
```

Expected: compile error — methods not defined on `RDBConfigStore`.

- [ ] **Step 3: Add methods to ConfigStore interface**

In `framework/configstore/store.go`, find the `ConfigStore` interface and add after the existing Teams methods:

```go
	// User management
	CreateUser(ctx context.Context, user *tables.GovernanceUsersTable) error
	GetUser(ctx context.Context, id string) (*tables.GovernanceUsersTable, error)
	GetUserByEmail(ctx context.Context, email string) (*tables.GovernanceUsersTable, error)
	ListUsers(ctx context.Context, search string, limit, offset int) ([]*tables.GovernanceUsersTable, int64, error)
	UpdateUser(ctx context.Context, id string, updates map[string]any) (*tables.GovernanceUsersTable, error)
	DeleteUser(ctx context.Context, id string) error
	UpsertUserByEmail(ctx context.Context, email, name, authMethod string) (*tables.GovernanceUsersTable, error)
```

- [ ] **Step 4: Implement methods on RDBConfigStore**

At the end of `framework/configstore/rdb.go`, add:

```go
// CreateUser inserts a new governance user.
func (s *RDBConfigStore) CreateUser(ctx context.Context, user *tables.GovernanceUsersTable) error {
	if err := s.db.WithContext(ctx).Create(user).Error; err != nil {
		return s.parseGormError(err)
	}
	return nil
}

// GetUser fetches a user by primary key.
func (s *RDBConfigStore) GetUser(ctx context.Context, id string) (*tables.GovernanceUsersTable, error) {
	var user tables.GovernanceUsersTable
	if err := s.db.WithContext(ctx).First(&user, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &user, nil
}

// GetUserByEmail fetches a user by email address.
func (s *RDBConfigStore) GetUserByEmail(ctx context.Context, email string) (*tables.GovernanceUsersTable, error) {
	var user tables.GovernanceUsersTable
	if err := s.db.WithContext(ctx).First(&user, "email = ?", email).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &user, nil
}

// ListUsers returns a paginated, optionally-filtered list of users.
func (s *RDBConfigStore) ListUsers(ctx context.Context, search string, limit, offset int) ([]*tables.GovernanceUsersTable, int64, error) {
	var users []*tables.GovernanceUsersTable
	var total int64

	q := s.db.WithContext(ctx).Model(&tables.GovernanceUsersTable{})
	if search != "" {
		like := "%" + strings.ToLower(search) + "%"
		q = q.Where("LOWER(email) LIKE ? OR LOWER(name) LIKE ?", like, like)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	if err := q.Offset(offset).Order("created_at ASC").Find(&users).Error; err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

// UpdateUser applies a partial map of updates to a user row.
func (s *RDBConfigStore) UpdateUser(ctx context.Context, id string, updates map[string]any) (*tables.GovernanceUsersTable, error) {
	if err := s.db.WithContext(ctx).Model(&tables.GovernanceUsersTable{}).
		Where("id = ?", id).Updates(updates).Error; err != nil {
		return nil, s.parseGormError(err)
	}
	return s.GetUser(ctx, id)
}

// DeleteUser deletes a user and cascade-deletes their owned budget and rate limit.
func (s *RDBConfigStore) DeleteUser(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user tables.GovernanceUsersTable
		if err := tx.First(&user, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
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

// UpsertUserByEmail inserts on first login or updates name on subsequent logins.
func (s *RDBConfigStore) UpsertUserByEmail(ctx context.Context, email, name, authMethod string) (*tables.GovernanceUsersTable, error) {
	existing, err := s.GetUserByEmail(ctx, email)
	if err == nil {
		// Update name in case it changed in the IdP
		return s.UpdateUser(ctx, existing.ID, map[string]any{"name": name, "updated_at": time.Now()})
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	user := &tables.GovernanceUsersTable{
		ID:         generateID(),
		Email:      email,
		Name:       name,
		AuthMethod: authMethod,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if err := s.CreateUser(ctx, user); err != nil {
		return nil, err
	}
	return user, nil
}
```

> `generateID()` — check if one already exists in `rdb.go` (search for `uuid` or `generateID`). If not, add: `func generateID() string { return uuid.New().String() }` and import `"github.com/google/uuid"`.

- [ ] **Step 5: Run the tests**

```bash
cd framework && go test ./configstore/... -run "TestCreateAndGetUser|TestUpsertUser|TestListUsers|TestDeleteUser" -v
```

Expected: all 5 tests PASS.

- [ ] **Step 6: Verify full build**

```bash
cd framework && go build ./...
```

- [ ] **Step 7: Commit**

```bash
git add framework/configstore/store.go framework/configstore/rdb.go framework/configstore/governance_users_test.go
git commit -m "feat(configstore): add user management methods to ConfigStore and RDBConfigStore"
```

---

## Task 4: ConfigStore Interface + RDB Implementation for SSO Configs

**Files:**
- Modify: `framework/configstore/store.go`
- Modify: `framework/configstore/rdb.go`

- [ ] **Step 1: Write the test first**

Create `framework/configstore/governance_sso_test.go`:

```go
package configstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupSSOTestDB(t *testing.T) *configstore.RDBConfigStore {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	err = db.AutoMigrate(
		&tables.GovernanceSSOConfigsTable{},
		&tables.GovernanceSSONoncesTable{},
	)
	require.NoError(t, err)
	return configstore.NewRDBConfigStoreForTest(db)
}

func TestCreateSSOConfig(t *testing.T) {
	store := setupSSOTestDB(t)
	ctx := context.Background()

	cfg := &tables.GovernanceSSOConfigsTable{
		ID:           "sso-1",
		Provider:     "okta",
		IssuerURL:    "https://dev.okta.com",
		ClientID:     "client-id",
		ClientSecret: "secret",
		Enabled:      false,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	require.NoError(t, store.CreateSSOConfig(ctx, cfg))

	configs, err := store.ListSSOConfigs(ctx)
	require.NoError(t, err)
	assert.Len(t, configs, 1)
}

func TestEnableSSOConfig_DisablesOthers(t *testing.T) {
	store := setupSSOTestDB(t)
	ctx := context.Background()

	for _, id := range []string{"sso-1", "sso-2"} {
		require.NoError(t, store.CreateSSOConfig(ctx, &tables.GovernanceSSOConfigsTable{
			ID: id, Provider: "okta", IssuerURL: "https://x.okta.com",
			ClientID: "c", ClientSecret: "s", Enabled: true,
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}))
	}
	require.NoError(t, store.EnableSSOConfig(ctx, "sso-2"))

	configs, err := store.ListSSOConfigs(ctx)
	require.NoError(t, err)
	enabledCount := 0
	for _, c := range configs {
		if c.Enabled {
			enabledCount++
			assert.Equal(t, "sso-2", c.ID)
		}
	}
	assert.Equal(t, 1, enabledCount)
}

func TestGetActiveSSOConfig_NoneEnabled(t *testing.T) {
	store := setupSSOTestDB(t)
	ctx := context.Background()

	_, err := store.GetActiveSSOConfig(ctx)
	assert.ErrorIs(t, err, configstore.ErrNotFound)
}

func TestCreateSSONonce_AndConsume(t *testing.T) {
	store := setupSSOTestDB(t)
	ctx := context.Background()

	require.NoError(t, store.CreateSSONonce(ctx, "state-abc", "verifier-xyz", "okta", time.Now().Add(10*time.Minute)))

	nonce, err := store.ConsumeAndDeleteSSONonce(ctx, "state-abc")
	require.NoError(t, err)
	assert.Equal(t, "verifier-xyz", nonce.CodeVerifier)

	// Second call should fail — nonce is single-use
	_, err = store.ConsumeAndDeleteSSONonce(ctx, "state-abc")
	assert.Error(t, err)
}
```

- [ ] **Step 2: Run the test — expect compile failure**

```bash
cd framework && go test ./configstore/... -run TestCreateSSOConfig -v 2>&1 | head -10
```

Expected: compile error.

- [ ] **Step 3: Add SSO methods to ConfigStore interface**

In `framework/configstore/store.go`, add after the user management methods:

```go
	// SSO config management
	CreateSSOConfig(ctx context.Context, cfg *tables.GovernanceSSOConfigsTable) error
	ListSSOConfigs(ctx context.Context) ([]*tables.GovernanceSSOConfigsTable, error)
	GetSSOConfig(ctx context.Context, id string) (*tables.GovernanceSSOConfigsTable, error)
	GetActiveSSOConfig(ctx context.Context) (*tables.GovernanceSSOConfigsTable, error)
	UpdateSSOConfig(ctx context.Context, id string, updates map[string]any) (*tables.GovernanceSSOConfigsTable, error)
	EnableSSOConfig(ctx context.Context, id string) error
	DeleteSSOConfig(ctx context.Context, id string) error

	// SSO nonce management (PKCE state + id_token nonce)
	CreateSSONonce(ctx context.Context, state, codeVerifier, nonce, provider string, expiresAt time.Time) error
	ConsumeAndDeleteSSONonce(ctx context.Context, state string) (*tables.GovernanceSSONoncesTable, error)
	DeleteExpiredSSONonces(ctx context.Context) error
```

- [ ] **Step 4: Implement SSO methods on RDBConfigStore**

At the end of `framework/configstore/rdb.go`, add:

```go
func (s *RDBConfigStore) CreateSSOConfig(ctx context.Context, cfg *tables.GovernanceSSOConfigsTable) error {
	return s.parseGormError(s.db.WithContext(ctx).Create(cfg).Error)
}

func (s *RDBConfigStore) ListSSOConfigs(ctx context.Context) ([]*tables.GovernanceSSOConfigsTable, error) {
	var cfgs []*tables.GovernanceSSOConfigsTable
	if err := s.db.WithContext(ctx).Order("created_at ASC").Find(&cfgs).Error; err != nil {
		return nil, err
	}
	return cfgs, nil
}

func (s *RDBConfigStore) GetSSOConfig(ctx context.Context, id string) (*tables.GovernanceSSOConfigsTable, error) {
	var cfg tables.GovernanceSSOConfigsTable
	if err := s.db.WithContext(ctx).First(&cfg, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &cfg, nil
}

func (s *RDBConfigStore) GetActiveSSOConfig(ctx context.Context) (*tables.GovernanceSSOConfigsTable, error) {
	var cfg tables.GovernanceSSOConfigsTable
	if err := s.db.WithContext(ctx).Where("enabled = ?", true).First(&cfg).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &cfg, nil
}

func (s *RDBConfigStore) UpdateSSOConfig(ctx context.Context, id string, updates map[string]any) (*tables.GovernanceSSOConfigsTable, error) {
	if err := s.db.WithContext(ctx).Model(&tables.GovernanceSSOConfigsTable{}).
		Where("id = ?", id).Updates(updates).Error; err != nil {
		return nil, s.parseGormError(err)
	}
	return s.GetSSOConfig(ctx, id)
}

// EnableSSOConfig enables the given config and disables all others in one transaction.
func (s *RDBConfigStore) EnableSSOConfig(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&tables.GovernanceSSOConfigsTable{}).
			Where("id != ?", id).Update("enabled", false).Error; err != nil {
			return err
		}
		return tx.Model(&tables.GovernanceSSOConfigsTable{}).
			Where("id = ?", id).Update("enabled", true).Error
	})
}

func (s *RDBConfigStore) DeleteSSOConfig(ctx context.Context, id string) error {
	result := s.db.WithContext(ctx).Delete(&tables.GovernanceSSOConfigsTable{}, "id = ?", id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *RDBConfigStore) CreateSSONonce(ctx context.Context, state, codeVerifier, nonce, provider string, expiresAt time.Time) error {
	row := &tables.GovernanceSSONoncesTable{
		State:        state,
		CodeVerifier: codeVerifier,
		Nonce:        nonce,
		Provider:     provider,
		ExpiresAt:    expiresAt,
	}
	return s.parseGormError(s.db.WithContext(ctx).Create(row).Error)
}

// ConsumeAndDeleteSSONonce atomically reads and deletes a nonce (single-use).
// Uses SELECT FOR UPDATE (Postgres) / exclusive transaction (SQLite) to prevent
// concurrent callbacks from consuming the same nonce.
func (s *RDBConfigStore) ConsumeAndDeleteSSONonce(ctx context.Context, state string) (*tables.GovernanceSSONoncesTable, error) {
	var nonce tables.GovernanceSSONoncesTable
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&nonce, "state = ? AND expires_at > ?", state, time.Now()).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		return tx.Delete(&nonce).Error
	})
	if err != nil {
		return nil, err
	}
	return &nonce, nil
}

func (s *RDBConfigStore) DeleteExpiredSSONonces(ctx context.Context) error {
	return s.db.WithContext(ctx).
		Where("expires_at < ?", time.Now()).
		Delete(&tables.GovernanceSSONoncesTable{}).Error
}
```

- [ ] **Step 5: Run SSO tests**

```bash
cd framework && go test ./configstore/... -run "TestCreateSSOConfig|TestEnableSSO|TestGetActiveSSO|TestCreateSSONonce" -v
```

Expected: all 4 tests PASS.

- [ ] **Step 6: Verify full build**

```bash
cd framework && go build ./...
```

- [ ] **Step 7: Commit**

```bash
git add framework/configstore/store.go framework/configstore/rdb.go \
        framework/configstore/governance_sso_test.go
git commit -m "feat(configstore): add SSO config and nonce management methods"
```

---

## Task 5: Users HTTP Handler

**Files:**
- Create: `transports/bifrost-http/handlers/governance_users.go`
- Modify: `transports/bifrost-http/server/server.go`

- [ ] **Step 1: Create the handler file**

```go
// transports/bifrost-http/handlers/governance_users.go
package handlers

import (
	"encoding/json"
	"time"

	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/valyala/fasthttp"

	"github.com/google/uuid"
)

// UserGovernanceSync is the subset of the governance plugin's in-memory store
// interface needed to keep inference-time quota tracking in sync with DB writes.
// The governance plugin already implements this; pass it when available.
type UserGovernanceSync interface {
	CreateUserGovernanceInMemory(userID string, budget *tables.TableBudget, rateLimit *tables.TableRateLimit)
	UpdateUserGovernanceInMemory(userID string, budget *tables.TableBudget, rateLimit *tables.TableRateLimit)
	DeleteUserGovernanceInMemory(userID string)
}

type GovernanceUsersHandler struct {
	configStore   configstore.ConfigStore
	governanceSync UserGovernanceSync // nil when governance plugin not loaded
}

func NewGovernanceUsersHandler(cs configstore.ConfigStore, govSync UserGovernanceSync) *GovernanceUsersHandler {
	return &GovernanceUsersHandler{configStore: cs, governanceSync: govSync}
}

func (h *GovernanceUsersHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.Middleware) {
	r.GET("/api/governance/users", lib.ChainMiddlewares(h.listUsers, middlewares...))
	r.POST("/api/governance/users", lib.ChainMiddlewares(h.createUser, middlewares...))
	r.GET("/api/governance/users/{id}", lib.ChainMiddlewares(h.getUser, middlewares...))
	r.PUT("/api/governance/users/{id}", lib.ChainMiddlewares(h.updateUser, middlewares...))
	r.DELETE("/api/governance/users/{id}", lib.ChainMiddlewares(h.deleteUser, middlewares...))
}

func (h *GovernanceUsersHandler) listUsers(ctx *fasthttp.RequestCtx) {
	search := string(ctx.QueryArgs().Peek("search"))
	limit := ctx.QueryArgs().GetUintOrZero("limit")
	offset := ctx.QueryArgs().GetUintOrZero("offset")
	if limit == 0 {
		limit = 50
	}

	users, total, err := h.configStore.ListUsers(ctx, search, limit, offset)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	SendJSON(ctx, map[string]any{"users": users, "total_count": total})
}

func (h *GovernanceUsersHandler) createUser(ctx *fasthttp.RequestCtx) {
	var body struct {
		Email       string  `json:"email"`
		Name        string  `json:"name"`
		TeamID      *string `json:"team_id"`
		BudgetID    *string `json:"budget_id"`
		RateLimitID *string `json:"rate_limit_id"`
	}
	if err := json.Unmarshal(ctx.PostBody(), &body); err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, "invalid request body")
		return
	}
	if body.Email == "" {
		SendError(ctx, fasthttp.StatusBadRequest, "email is required")
		return
	}

	user := &tables.GovernanceUsersTable{
		ID:          uuid.New().String(),
		Email:       body.Email,
		Name:        body.Name,
		TeamID:      body.TeamID,
		BudgetID:    body.BudgetID,
		RateLimitID: body.RateLimitID,
		AuthMethod:  "password",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := h.configStore.CreateUser(ctx, user); err != nil {
		if isUniqueConstraintError(err) {
			SendError(ctx, fasthttp.StatusConflict, "user with this email already exists")
			return
		}
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	// Sync governance plugin in-memory store so inference-time quota checks are immediate
	if h.governanceSync != nil {
		h.governanceSync.CreateUserGovernanceInMemory(user.ID, nil, nil)
	}
	SendJSONWithStatus(ctx, fasthttp.StatusCreated, map[string]any{"user": user})
}

func (h *GovernanceUsersHandler) getUser(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	user, err := h.configStore.GetUser(ctx, id)
	if err != nil {
		if isNotFound(err) {
			SendError(ctx, fasthttp.StatusNotFound, "user not found")
			return
		}
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	SendJSON(ctx, map[string]any{"user": user})
}

func (h *GovernanceUsersHandler) updateUser(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	var updates map[string]any
	if err := json.Unmarshal(ctx.PostBody(), &updates); err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, "invalid request body")
		return
	}
	updates["updated_at"] = time.Now()
	delete(updates, "id")
	delete(updates, "auth_method")

	user, err := h.configStore.UpdateUser(ctx, id, updates)
	if err != nil {
		if isNotFound(err) {
			SendError(ctx, fasthttp.StatusNotFound, "user not found")
			return
		}
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	if h.governanceSync != nil {
		h.governanceSync.UpdateUserGovernanceInMemory(user.ID, nil, nil)
	}
	SendJSON(ctx, map[string]any{"user": user})
}

func (h *GovernanceUsersHandler) deleteUser(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	if err := h.configStore.DeleteUser(ctx, id); err != nil {
		if isNotFound(err) {
			SendError(ctx, fasthttp.StatusNotFound, "user not found")
			return
		}
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	if h.governanceSync != nil {
		h.governanceSync.DeleteUserGovernanceInMemory(id)
	}
	SendJSON(ctx, map[string]any{"message": "user deleted"})
}
```

> `isUniqueConstraintError` and `isNotFound` — check if helpers already exist in the handlers package (search for `parseGormError` or similar). If not, add:
> ```go
> func isNotFound(err error) bool { return errors.Is(err, configstore.ErrNotFound) }
> func isUniqueConstraintError(err error) bool { /* check for sqlite unique or postgres unique */ return strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "unique") }
> ```
> Also check which router and lib imports are used by looking at any other handler's imports (e.g., `session.go`).

- [ ] **Step 2: Register the handler in server.go**

In `transports/bifrost-http/server/server.go`, find where `sessionHandler.RegisterRoutes` is called and add nearby:

```go
if s.Config.ConfigStore != nil {
    // Pass governance plugin's in-memory store if the governance plugin is loaded.
    // Look up the plugin by name in s.Config.Plugins (the plugin registry).
    // The governance plugin implements handlers.UserGovernanceSync directly.
    var govSync handlers.UserGovernanceSync
    if govPlugin := s.Config.GetPlugin(governance.PluginName); govPlugin != nil {
        if gs, ok := govPlugin.(handlers.UserGovernanceSync); ok {
            govSync = gs
        }
    }
    usersHandler := handlers.NewGovernanceUsersHandler(s.Config.ConfigStore, govSync)
    usersHandler.RegisterRoutes(s.Router, middlewares...)
}

> **Note:** Check the exact method name on `lib.Config` to retrieve a registered plugin by name (search for `GetPlugin` or iterate `s.Config.Plugins`). If it doesn't exist, add a helper or iterate the slice inline.
```

- [ ] **Step 3: Verify compilation**

```bash
cd transports/bifrost-http && go build ./...
```

- [ ] **Step 4: Manual smoke test**

Start the server and run:

```bash
curl -s -X POST http://localhost:8080/api/governance/users \
  -H "Content-Type: application/json" \
  -d '{"email":"test@example.com","name":"Test User"}' | jq .

curl -s http://localhost:8080/api/governance/users | jq .
```

Expected: user created and returned in list.

- [ ] **Step 5: Commit**

```bash
git add transports/bifrost-http/handlers/governance_users.go \
        transports/bifrost-http/server/server.go
git commit -m "feat(api): add /api/governance/users CRUD endpoints"
```

---

## Task 6: SSO Provider Adapters + OIDC Engine

**Files:**
- Create: `transports/bifrost-http/handlers/sso_adapters.go`
- Create: `transports/bifrost-http/handlers/sso.go`
- Modify: `transports/bifrost-http/handlers/session.go`
- Modify: `transports/bifrost-http/server/server.go`

- [ ] **Step 1: Add go-jose dependency**

```bash
cd transports/bifrost-http && go get github.com/go-jose/go-jose/v3
```

- [ ] **Step 2: Create sso_adapters.go**

```go
// transports/bifrost-http/handlers/sso_adapters.go
package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"github.com/maximhq/bifrost/framework/configstore/tables"
)

// OIDCProvider extracts user identity from raw OIDC claims.
type OIDCProvider interface {
	Name() string
	ExtractUserInfo(claims map[string]any, cfg *tables.GovernanceSSOConfigsTable) (email, name string, groups []string, err error)
}

var providerRegistry = map[string]OIDCProvider{
	"okta":  OktaAdapter{},
	"entra": EntraAdapter{},
}

// OktaAdapter handles Okta-specific claim extraction.
type OktaAdapter struct{}

func (OktaAdapter) Name() string { return "okta" }

func (OktaAdapter) ExtractUserInfo(claims map[string]any, cfg *tables.GovernanceSSOConfigsTable) (string, string, []string, error) {
	email, _ := claims["email"].(string)
	name, _ := claims["name"].(string)

	groupKey := "groups"
	if cfg.GroupClaimKey != "" {
		groupKey = cfg.GroupClaimKey
	}
	var groups []string
	if raw, ok := claims[groupKey].([]any); ok {
		for _, g := range raw {
			if s, ok := g.(string); ok {
				groups = append(groups, s)
			}
		}
	}
	if email == "" {
		return "", "", nil, fmt.Errorf("okta: missing email claim")
	}
	return email, name, groups, nil
}

// EntraAdapter handles Microsoft Entra ID-specific claim extraction.
type EntraAdapter struct{}

func (EntraAdapter) Name() string { return "entra" }

func (EntraAdapter) ExtractUserInfo(claims map[string]any, cfg *tables.GovernanceSSOConfigsTable) (string, string, []string, error) {
	email, _ := claims["preferred_username"].(string)
	if email == "" {
		email, _ = claims["upn"].(string)
	}
	name, _ := claims["name"].(string)

	groupKey := "groups"
	if cfg.GroupClaimKey != "" {
		groupKey = cfg.GroupClaimKey
	}
	var groups []string
	if raw, ok := claims[groupKey].([]any); ok {
		for _, g := range raw {
			if s, ok := g.(string); ok {
				groups = append(groups, s)
			}
		}
	}
	if email == "" {
		return "", "", nil, fmt.Errorf("entra: missing email/UPN claim")
	}
	return email, name, groups, nil
}

// --- PKCE helpers ---

func generateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func generateCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func codeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
```

- [ ] **Step 3: Write the adapter tests**

Create `transports/bifrost-http/handlers/sso_adapters_test.go`:

```go
package handlers

import (
	"testing"

	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOktaAdapter_ExtractUserInfo(t *testing.T) {
	adapter := OktaAdapter{}
	cfg := &tables.GovernanceSSOConfigsTable{}
	claims := map[string]any{
		"email":  "alice@example.com",
		"name":   "Alice",
		"groups": []any{"admins", "developers"},
	}
	email, name, groups, err := adapter.ExtractUserInfo(claims, cfg)
	require.NoError(t, err)
	assert.Equal(t, "alice@example.com", email)
	assert.Equal(t, "Alice", name)
	assert.Contains(t, groups, "admins")
}

func TestOktaAdapter_CustomGroupClaimKey(t *testing.T) {
	adapter := OktaAdapter{}
	cfg := &tables.GovernanceSSOConfigsTable{GroupClaimKey: "custom_groups"}
	claims := map[string]any{
		"email":         "bob@example.com",
		"name":          "Bob",
		"custom_groups": []any{"ops"},
	}
	_, _, groups, err := adapter.ExtractUserInfo(claims, cfg)
	require.NoError(t, err)
	assert.Equal(t, []string{"ops"}, groups)
}

func TestEntraAdapter_ExtractUserInfo_PrefersPreferredUsername(t *testing.T) {
	adapter := EntraAdapter{}
	cfg := &tables.GovernanceSSOConfigsTable{}
	claims := map[string]any{
		"preferred_username": "carol@corp.com",
		"name":               "Carol",
	}
	email, name, _, err := adapter.ExtractUserInfo(claims, cfg)
	require.NoError(t, err)
	assert.Equal(t, "carol@corp.com", email)
	assert.Equal(t, "Carol", name)
}

func TestEntraAdapter_FallsBackToUPN(t *testing.T) {
	adapter := EntraAdapter{}
	cfg := &tables.GovernanceSSOConfigsTable{}
	claims := map[string]any{
		"upn":  "dave@corp.com",
		"name": "Dave",
	}
	email, _, _, err := adapter.ExtractUserInfo(claims, cfg)
	require.NoError(t, err)
	assert.Equal(t, "dave@corp.com", email)
}

func TestEntraAdapter_MissingEmail_ReturnsError(t *testing.T) {
	adapter := EntraAdapter{}
	cfg := &tables.GovernanceSSOConfigsTable{}
	claims := map[string]any{"name": "Nobody"}
	_, _, _, err := adapter.ExtractUserInfo(claims, cfg)
	assert.Error(t, err)
}

func TestCodeChallenge_S256(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	// RFC 7636 test vector
	expected := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	assert.Equal(t, expected, codeChallenge(verifier))
}
```

- [ ] **Step 4: Run adapter tests**

```bash
cd transports/bifrost-http && go test ./handlers/... -run "TestOktaAdapter|TestEntraAdapter|TestCodeChallenge" -v
```

Expected: all 6 tests PASS.

- [ ] **Step 5: Create sso.go — SSOHandler**

```go
// transports/bifrost-http/handlers/sso.go
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v3"
	josejwt "github.com/go-jose/go-jose/v3/jwt"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/valyala/fasthttp"

	// router and lib imported same as other handlers — check session.go for exact import paths
)

const jwksTTL = time.Hour

type jwksCacheEntry struct {
	keys      []jose.JSONWebKey
	fetchedAt time.Time
}

type oidcDiscovery struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JwksURI               string `json:"jwks_uri"`
	Issuer                string `json:"issuer"`
}

type discoveryCacheEntry struct {
	doc       oidcDiscovery
	fetchedAt time.Time
}

type SSOHandler struct {
	configStore   configstore.ConfigStore
	jwksMu        sync.RWMutex
	jwksCache     map[string]*jwksCacheEntry     // keyed by jwks_uri
	discoveryMu   sync.RWMutex
	discoveryCache map[string]*discoveryCacheEntry // keyed by issuer_url
}

func NewSSOHandler(cs configstore.ConfigStore) *SSOHandler {
	h := &SSOHandler{
		configStore:    cs,
		jwksCache:      make(map[string]*jwksCacheEntry),
		discoveryCache: make(map[string]*discoveryCacheEntry),
	}
	// Start background nonce cleanup
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		for range ticker.C {
			_ = cs.DeleteExpiredSSONonces(context.Background())
		}
	}()
	return h
}

func (h *SSOHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.Middleware) {
	// Public endpoints — no auth middleware on these two
	r.GET("/api/sso/login", h.initiate)
	r.GET("/api/sso/callback", h.callback)

	// Admin-only SSO config CRUD — protected by middlewares
	r.GET("/api/governance/sso/configs", lib.ChainMiddlewares(h.listConfigs, middlewares...))
	r.POST("/api/governance/sso/configs", lib.ChainMiddlewares(h.createConfig, middlewares...))
	r.PUT("/api/governance/sso/configs/{id}", lib.ChainMiddlewares(h.updateConfig, middlewares...))
	r.DELETE("/api/governance/sso/configs/{id}", lib.ChainMiddlewares(h.deleteConfig, middlewares...))
	r.POST("/api/governance/sso/configs/{id}/test", lib.ChainMiddlewares(h.testConfig, middlewares...))
}

// fetchOIDCDiscovery fetches and caches the OIDC provider metadata document.
// Uses the standardized /.well-known/openid-configuration endpoint (RFC 8414).
func (h *SSOHandler) fetchOIDCDiscovery(issuerURL string) (*oidcDiscovery, error) {
	h.discoveryMu.RLock()
	entry := h.discoveryCache[issuerURL]
	h.discoveryMu.RUnlock()

	if entry != nil && time.Since(entry.fetchedAt) < jwksTTL {
		return &entry.doc, nil
	}

	discoveryURL := strings.TrimRight(issuerURL, "/") + "/.well-known/openid-configuration"
	resp, err := safeHTTPClient.Get(discoveryURL)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

	var doc oidcDiscovery
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("OIDC discovery parse failed: %w", err)
	}
	if doc.AuthorizationEndpoint == "" || doc.TokenEndpoint == "" {
		return nil, fmt.Errorf("OIDC discovery missing required endpoints")
	}

	h.discoveryMu.Lock()
	h.discoveryCache[issuerURL] = &discoveryCacheEntry{doc: doc, fetchedAt: time.Now()}
	h.discoveryMu.Unlock()

	return &doc, nil
}

// --- Initiate PKCE flow ---

func (h *SSOHandler) initiate(ctx *fasthttp.RequestCtx) {
	cfg, err := h.configStore.GetActiveSSOConfig(ctx)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			SendError(ctx, fasthttp.StatusNotFound, "no SSO provider configured")
			return
		}
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}

	discovery, err := h.fetchOIDCDiscovery(cfg.IssuerURL)
	if err != nil {
		SendError(ctx, fasthttp.StatusBadGateway, fmt.Sprintf("OIDC discovery failed: %v", err))
		return
	}

	state, err := generateState()
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "failed to generate state")
		return
	}
	verifier, err := generateCodeVerifier()
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "failed to generate code verifier")
		return
	}
	nonce, err := generateState() // reuse same random-bytes function for nonce
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "failed to generate nonce")
		return
	}
	challenge := codeChallenge(verifier)

	// Derive callback URL from request (same pattern as oauth2_metadata.go)
	scheme := "https"
	if string(ctx.URI().Scheme()) == "http" {
		scheme = "http"
	}
	callbackURL := fmt.Sprintf("%s://%s/api/sso/callback", scheme, ctx.Host())

	if err := h.configStore.CreateSSONonce(ctx, state, verifier, nonce, cfg.Provider, time.Now().Add(10*time.Minute)); err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "failed to store nonce")
		return
	}

	authURL := fmt.Sprintf(
		"%s?response_type=code&client_id=%s&redirect_uri=%s&scope=openid%%20profile%%20email&state=%s&nonce=%s&code_challenge=%s&code_challenge_method=S256",
		discovery.AuthorizationEndpoint,
		url.QueryEscape(cfg.ClientID),
		url.QueryEscape(callbackURL),
		url.QueryEscape(state),
		url.QueryEscape(nonce),
		url.QueryEscape(challenge),
	)
	ctx.Redirect(authURL, fasthttp.StatusFound)
}

// --- Callback ---

func (h *SSOHandler) callback(ctx *fasthttp.RequestCtx) {
	state := string(ctx.QueryArgs().Peek("state"))
	code := string(ctx.QueryArgs().Peek("code"))
	if state == "" || code == "" {
		SendError(ctx, fasthttp.StatusBadRequest, "missing state or code")
		return
	}

	nonce, err := h.configStore.ConsumeAndDeleteSSONonce(ctx, state)
	if err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, "invalid or expired state")
		return
	}

	cfg, err := h.configStore.GetActiveSSOConfig(ctx)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "SSO not configured")
		return
	}

	scheme := "https"
	if string(ctx.URI().Scheme()) == "http" {
		scheme = "http"
	}
	callbackURL := fmt.Sprintf("%s://%s/api/sso/callback", scheme, ctx.Host())

	claims, err := h.exchangeAndVerify(ctx, cfg, code, nonce.CodeVerifier, callbackURL, nonce.Nonce)
	if err != nil {
		SendError(ctx, fasthttp.StatusUnauthorized, fmt.Sprintf("token verification failed: %v", err))
		return
	}

	provider, ok := providerRegistry[cfg.Provider]
	if !ok {
		SendError(ctx, fasthttp.StatusInternalServerError, "unsupported provider: "+cfg.Provider)
		return
	}
	email, name, _, err := provider.ExtractUserInfo(claims, cfg)
	if err != nil {
		SendError(ctx, fasthttp.StatusUnauthorized, fmt.Sprintf("claim extraction failed: %v", err))
		return
	}

	user, err := h.configStore.UpsertUserByEmail(ctx, email, name, "oidc")
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "failed to provision user")
		return
	}

	session, err := h.createSession(ctx, user.ID)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "failed to create session")
		return
	}

	ctx.Response.Header.SetCookie(&fasthttp.Cookie{
		Key:      "token",
		Value:    session.Token,
		Path:     "/",
		HTTPOnly: true,
		SameSite: fasthttp.CookieSameSiteLaxMode,
		Expire:   session.ExpiresAt,
	})
	ctx.Redirect("/workspace", fasthttp.StatusFound)
}

// exchangeAndVerify exchanges auth code for tokens and verifies the id_token.
func (h *SSOHandler) exchangeAndVerify(ctx context.Context, cfg *tables.GovernanceSSOConfigsTable, code, verifier, callbackURL, expectedNonce string) (map[string]any, error) {
	// Resolve token endpoint via OIDC Discovery — never hardcode provider paths
	discovery, err := h.fetchOIDCDiscovery(cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery failed: %w", err)
	}
	resp, err := safeHTTPClient.PostForm(discovery.TokenEndpoint, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
		"redirect_uri":  {callbackURL},
		"code_verifier": {verifier},
	})
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

	var tokenResp struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil || tokenResp.IDToken == "" {
		return nil, fmt.Errorf("no id_token in response")
	}

	keys, err := h.fetchJWKS(discovery.JwksURI)
	if err != nil {
		return nil, fmt.Errorf("JWKS fetch failed: %w", err)
	}

	tok, err := josejwt.ParseSigned(tokenResp.IDToken)
	if err != nil {
		return nil, fmt.Errorf("JWT parse failed: %w", err)
	}

	var claims map[string]any
	for _, key := range keys {
		if err := tok.Claims(key, &claims); err == nil {
			break
		}
	}
	if claims == nil {
		return nil, fmt.Errorf("JWT signature verification failed")
	}

	// Verify standard claims (exact equality, not prefix — OIDC spec §2)
	if iss, _ := claims["iss"].(string); iss != discovery.Issuer {
		return nil, fmt.Errorf("issuer mismatch: got %q, want %q", iss, discovery.Issuer)
	}
	if aud, ok := claims["aud"].(string); ok && aud != cfg.ClientID {
		return nil, fmt.Errorf("audience mismatch")
	}
	if exp, ok := claims["exp"].(float64); ok {
		if time.Now().Unix() > int64(exp) {
			return nil, fmt.Errorf("token expired")
		}
	}
	// Verify nonce — prevents id_token replay attacks
	if nonce, _ := claims["nonce"].(string); nonce != expectedNonce {
		return nil, fmt.Errorf("nonce mismatch")
	}
	return claims, nil
}

// fetchJWKS fetches and caches the JWKS from the URI returned by OIDC Discovery.
// The jwksURI parameter must come from the discovery document, not be constructed manually.
func (h *SSOHandler) fetchJWKS(jwksURI string) ([]jose.JSONWebKey, error) {
	h.jwksMu.RLock()
	entry := h.jwksCache[jwksURI]
	h.jwksMu.RUnlock()

	if entry != nil && time.Since(entry.fetchedAt) < jwksTTL {
		return entry.keys, nil
	}

	resp, err := safeHTTPClient.Get(jwksURI)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

	var keySet jose.JSONWebKeySet
	if err := json.Unmarshal(body, &keySet); err != nil {
		return nil, err
	}

	h.jwksMu.Lock()
	h.jwksCache[jwksURI] = &jwksCacheEntry{keys: keySet.Keys, fetchedAt: time.Now()}
	h.jwksMu.Unlock()

	return keySet.Keys, nil
}

func (h *SSOHandler) createSession(ctx context.Context, userID string) (*tables.SessionsTable, error) {
	token, err := generateState() // 32-byte random token
	if err != nil {
		return nil, err
	}
	session := &tables.SessionsTable{
		Token:      token,
		UserID:     &userID,
		AuthMethod: "oidc",
		ExpiresAt:  time.Now().Add(24 * time.Hour),
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if err := h.configStore.CreateSession(ctx, session); err != nil {
		return nil, err
	}
	return session, nil
}

// --- SSRF-guarded HTTP client ---

var safeHTTPClient = &http.Client{
	Timeout: 5 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 1 {
			return http.ErrUseLastResponse
		}
		return nil
	},
}

func validateIssuerURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("issuer URL must use HTTPS")
	}
	ips, err := net.LookupHost(u.Hostname())
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

// --- SSO Config CRUD handlers ---

func (h *SSOHandler) listConfigs(ctx *fasthttp.RequestCtx) {
	cfgs, err := h.configStore.ListSSOConfigs(ctx)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	// Mask client_secret in response
	type safeConfig struct {
		ID            string    `json:"id"`
		Provider      string    `json:"provider"`
		IssuerURL     string    `json:"issuer_url"`
		ClientID      string    `json:"client_id"`
		RoleClaimKey  string    `json:"role_claim_key"`
		GroupClaimKey string    `json:"group_claim_key"`
		Enabled       bool      `json:"enabled"`
		CreatedAt     time.Time `json:"created_at"`
		UpdatedAt     time.Time `json:"updated_at"`
	}
	var safe []safeConfig
	for _, c := range cfgs {
		safe = append(safe, safeConfig{
			ID: c.ID, Provider: c.Provider, IssuerURL: c.IssuerURL,
			ClientID: c.ClientID, RoleClaimKey: c.RoleClaimKey,
			GroupClaimKey: c.GroupClaimKey, Enabled: c.Enabled,
			CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
		})
	}
	SendJSON(ctx, map[string]any{"configs": safe})
}

func (h *SSOHandler) createConfig(ctx *fasthttp.RequestCtx) {
	var body tables.GovernanceSSOConfigsTable
	if err := json.Unmarshal(ctx.PostBody(), &body); err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, "invalid request body")
		return
	}
	if err := validateIssuerURL(body.IssuerURL); err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	body.ID = generateID()
	body.Enabled = false // always created disabled
	body.CreatedAt = time.Now()
	body.UpdatedAt = time.Now()

	if err := h.configStore.CreateSSOConfig(ctx, &body); err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	body.ClientSecret = "" // don't return secret
	SendJSONWithStatus(ctx, fasthttp.StatusCreated, map[string]any{"config": body})
}

func (h *SSOHandler) updateConfig(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	var updates map[string]any
	if err := json.Unmarshal(ctx.PostBody(), &updates); err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, "invalid request body")
		return
	}

	// Handle enable toggle separately (single-SSO enforcement)
	if enabled, ok := updates["enabled"].(bool); ok && enabled {
		delete(updates, "enabled")
		if err := h.configStore.EnableSSOConfig(ctx, id); err != nil {
			SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
			return
		}
	}

	if issuerURL, ok := updates["issuer_url"].(string); ok {
		if err := validateIssuerURL(issuerURL); err != nil {
			SendError(ctx, fasthttp.StatusBadRequest, err.Error())
			return
		}
	}

	updates["updated_at"] = time.Now()
	delete(updates, "id")
	cfg, err := h.configStore.UpdateSSOConfig(ctx, id, updates)
	if err != nil {
		if isNotFound(err) {
			SendError(ctx, fasthttp.StatusNotFound, "config not found")
			return
		}
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	cfg.ClientSecret = ""
	SendJSON(ctx, map[string]any{"config": cfg})
}

func (h *SSOHandler) deleteConfig(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	if err := h.configStore.DeleteSSOConfig(ctx, id); err != nil {
		if isNotFound(err) {
			SendError(ctx, fasthttp.StatusNotFound, "config not found")
			return
		}
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	SendJSON(ctx, map[string]any{"message": "config deleted"})
}

func (h *SSOHandler) testConfig(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	cfg, err := h.configStore.GetSSOConfig(ctx, id)
	if err != nil {
		if isNotFound(err) {
			SendError(ctx, fasthttp.StatusNotFound, "config not found")
			return
		}
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	if err := validateIssuerURL(cfg.IssuerURL); err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	oidcDiscoveryURL := strings.TrimRight(cfg.IssuerURL, "/") + "/.well-known/openid-configuration"
	resp, err := safeHTTPClient.Get(oidcDiscoveryURL)
	if err != nil {
		SendError(ctx, fasthttp.StatusBadGateway, fmt.Sprintf("cannot reach provider: %v", err))
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))

	if resp.StatusCode != http.StatusOK {
		SendError(ctx, fasthttp.StatusBadGateway, fmt.Sprintf("provider returned %d", resp.StatusCode))
		return
	}
	SendJSON(ctx, map[string]any{"message": "provider reachable"})
}
```

> Check `session.go` for exact `CreateSession` method name on `configStore`. If it's named differently, adjust accordingly. Also check how the `router.Router` import path is spelled in `session.go` and use the same one.

- [ ] **Step 6: Add sso_enabled to isAuthEnabled in session.go**

In `transports/bifrost-http/handlers/session.go`, find the `isAuthEnabled` function and change the final `SendJSON` call:

```go
	ssoEnabled := false
	if h.configStore != nil {
		if activeCfg, err := h.configStore.GetActiveSSOConfig(ctx); err == nil && activeCfg != nil {
			ssoEnabled = true
		}
	}
	SendJSON(ctx, map[string]any{
		"is_auth_enabled": authConfig.IsEnabled,
		"has_valid_token": hasValidToken,
		"sso_enabled":     ssoEnabled,
	})
```

- [ ] **Step 7: Register SSOHandler in server.go**

Find where `sessionHandler.RegisterRoutes` is called and add nearby:

```go
if s.Config.ConfigStore != nil {
    // callbackURL is derived at request time from ctx.Host() — no config field needed
    ssoHandler := handlers.NewSSOHandler(s.Config.ConfigStore)
    ssoHandler.RegisterRoutes(s.Router, middlewares...)
}
```

> Check `lib.Config` for the external URL field name. If it doesn't exist, use `""` as placeholder and document that operators must configure it.

- [ ] **Step 8: Verify compilation**

```bash
cd transports/bifrost-http && go build ./...
```

- [ ] **Step 9: Run adapter tests again to confirm nothing broke**

```bash
cd transports/bifrost-http && go test ./handlers/... -run "TestOkta|TestEntra|TestCode" -v
```

- [ ] **Step 10: Commit**

```bash
git add transports/bifrost-http/handlers/sso.go \
        transports/bifrost-http/handlers/sso_adapters.go \
        transports/bifrost-http/handlers/sso_adapters_test.go \
        transports/bifrost-http/handlers/session.go \
        transports/bifrost-http/server/server.go
git commit -m "feat(sso): add Generic OIDC engine, Okta/Entra adapters, PKCE flow, sso_enabled in is-auth-enabled"
```

---

## Task 7: Frontend — Users Page

**Files:**
- Modify: `ui/lib/types/governance.ts`
- Modify: `ui/lib/store/apis/governanceApi.ts`
- Modify: `ui/app/workspace/governance/users/page.tsx`

- [ ] **Step 1: Add GovernanceUser type**

In `ui/lib/types/governance.ts`, add:

```ts
export interface GovernanceUser {
  id: string;
  email: string;
  name: string;
  team_id?: string;
  budget_id?: string;
  rate_limit_id?: string;
  auth_method: "password" | "oidc";
  created_at: string;
  updated_at: string;
}

export interface GetUsersResponse {
  users: GovernanceUser[];
  total_count: number;
}

export interface CreateUserRequest {
  email: string;
  name: string;
  team_id?: string;
  budget_id?: string;
  rate_limit_id?: string;
}

export interface UpdateUserRequest {
  name?: string;
  team_id?: string | null;
  budget_id?: string | null;
  rate_limit_id?: string | null;
}
```

- [ ] **Step 2: Add RTK Query endpoints for users**

In `ui/lib/store/apis/governanceApi.ts`, add to the `endpoints` builder:

```ts
// Users
getUsers: builder.query<GetUsersResponse, { search?: string; limit?: number; offset?: number } | void>({
  query: (params) => ({
    url: "/governance/users",
    params: {
      ...(params?.search && { search: params.search }),
      ...(params?.limit && { limit: params.limit }),
      ...(params?.offset !== undefined && { offset: params.offset }),
    },
  }),
  providesTags: ["Users"],
}),

createUser: builder.mutation<{ user: GovernanceUser }, CreateUserRequest>({
  query: (data) => ({ url: "/governance/users", method: "POST", body: data }),
  invalidatesTags: ["Users"],
}),

updateUser: builder.mutation<{ user: GovernanceUser }, { id: string; data: UpdateUserRequest }>({
  query: ({ id, data }) => ({ url: `/governance/users/${id}`, method: "PUT", body: data }),
  invalidatesTags: ["Users"],
}),

deleteUser: builder.mutation<{ message: string }, string>({
  query: (id) => ({ url: `/governance/users/${id}`, method: "DELETE" }),
  invalidatesTags: ["Users"],
}),
```

Also add `"Users"` to the `tagTypes` array in the API slice definition.

- [ ] **Step 3: Export hooks**

In the same file or wherever hooks are re-exported, ensure:
```ts
export const { useGetUsersQuery, useCreateUserMutation, useUpdateUserMutation, useDeleteUserMutation } = governanceApi;
```

- [ ] **Step 4: Check what the current users page shows**

```bash
cat ui/app/workspace/governance/users/page.tsx
```

- [ ] **Step 5: Create the UserDialog component**

Create `ui/app/workspace/governance/views/userDialog.tsx`:

```tsx
"use client";

import { useState, useEffect } from "react";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { toast } from "sonner";
import { useCreateUserMutation, useUpdateUserMutation, useGetTeamsQuery } from "@/lib/store";
import { GovernanceUser } from "@/lib/types/governance";

interface UserDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  user?: GovernanceUser | null; // null = create mode
}

export default function UserDialog({ open, onOpenChange, user }: UserDialogProps) {
  const isEdit = !!user;
  const [createUser] = useCreateUserMutation();
  const [updateUser] = useUpdateUserMutation();
  const { data: teamsData } = useGetTeamsQuery();

  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [teamId, setTeamId] = useState<string>("none");
  const [loading, setLoading] = useState(false);

  // Populate form when editing
  useEffect(() => {
    if (user) {
      setEmail(user.email);
      setName(user.name);
      setTeamId(user.team_id ?? "none");
    } else {
      setEmail("");
      setName("");
      setTeamId("none");
    }
  }, [user, open]);

  const handleSubmit = async () => {
    if (!email) { toast.error("Email is required"); return; }
    setLoading(true);
    try {
      if (isEdit && user) {
        await updateUser({
          id: user.id,
          data: {
            name,
            team_id: teamId === "none" ? null : teamId,
          },
        }).unwrap();
        toast.success("User updated");
      } else {
        await createUser({
          email,
          name,
          team_id: teamId === "none" ? undefined : teamId,
        }).unwrap();
        toast.success("User created");
      }
      onOpenChange(false);
    } catch (err: any) {
      toast.error(err?.data?.error ?? "Operation failed");
    } finally {
      setLoading(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{isEdit ? "Edit User" : "Add User"}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4 py-2">
          <div className="space-y-1.5">
            <Label>Email</Label>
            <Input
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="user@example.com"
              disabled={isEdit} // email is immutable after creation
            />
          </div>
          <div className="space-y-1.5">
            <Label>Name</Label>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="Full name" />
          </div>
          <div className="space-y-1.5">
            <Label>Team</Label>
            <Select value={teamId} onValueChange={setTeamId}>
              <SelectTrigger><SelectValue placeholder="No team" /></SelectTrigger>
              <SelectContent>
                <SelectItem value="none">No team</SelectItem>
                {(teamsData?.teams ?? []).map((t) => (
                  <SelectItem key={t.id} value={t.id}>{t.name}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button onClick={handleSubmit} disabled={loading}>
            {loading ? "Saving..." : isEdit ? "Save" : "Create"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
```

- [ ] **Step 6: Replace the users page with full CRUD**

```tsx
"use client";

import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Edit, Plus, Search, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { useGetUsersQuery, useDeleteUserMutation } from "@/lib/store";
import { GovernanceUser } from "@/lib/types/governance";
import UserDialog from "@/app/workspace/governance/views/userDialog";

export default function GovernanceUsersPage() {
  const [search, setSearch] = useState("");
  const [offset, setOffset] = useState(0);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editingUser, setEditingUser] = useState<GovernanceUser | null>(null);
  const limit = 20;

  const { data, isLoading } = useGetUsersQuery({ search, limit, offset });
  const [deleteUser] = useDeleteUserMutation();

  const handleDelete = async (user: GovernanceUser) => {
    if (!confirm(`Delete ${user.email}?`)) return;
    try {
      await deleteUser(user.id).unwrap();
      toast.success("User deleted");
    } catch {
      toast.error("Failed to delete user");
    }
  };

  const openCreate = () => { setEditingUser(null); setDialogOpen(true); };
  const openEdit = (user: GovernanceUser) => { setEditingUser(user); setDialogOpen(true); };

  return (
    <div className="mx-auto w-full max-w-7xl p-6 space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Users</h1>
        <Button onClick={openCreate}>
          <Plus className="h-4 w-4 mr-2" />
          Add User
        </Button>
      </div>

      <div className="relative">
        <Search className="absolute left-3 top-2.5 h-4 w-4 text-muted-foreground" />
        <Input
          placeholder="Search by email or name..."
          className="pl-9"
          value={search}
          onChange={(e) => { setSearch(e.target.value); setOffset(0); }}
        />
      </div>

      {isLoading ? (
        <p className="text-muted-foreground">Loading...</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Email</TableHead>
              <TableHead>Name</TableHead>
              <TableHead>Auth Method</TableHead>
              <TableHead>Created</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {(data?.users ?? []).map((user) => (
              <TableRow key={user.id}>
                <TableCell className="font-medium">{user.email}</TableCell>
                <TableCell>{user.name || <span className="text-muted-foreground">—</span>}</TableCell>
                <TableCell>
                  <Badge variant={user.auth_method === "oidc" ? "secondary" : "outline"}>
                    {user.auth_method}
                  </Badge>
                </TableCell>
                <TableCell className="text-muted-foreground text-sm">
                  {new Date(user.created_at).toLocaleDateString()}
                </TableCell>
                <TableCell className="flex gap-1">
                  <Button variant="ghost" size="icon" onClick={() => openEdit(user)}>
                    <Edit className="h-4 w-4" />
                  </Button>
                  <Button variant="ghost" size="icon" onClick={() => handleDelete(user)}>
                    <Trash2 className="h-4 w-4 text-destructive" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
            {data?.users?.length === 0 && (
              <TableRow>
                <TableCell colSpan={5} className="text-center text-muted-foreground py-8">
                  No users found
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      )}

      <UserDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        user={editingUser}
      />
    </div>
  );
}
```

- [ ] **Step 7: Verify page in browser**

Navigate to `/workspace/governance/users`. Confirm: table renders, search works, "Add User" opens dialog, creating a user appears in list, edit dialog pre-populates, delete works.

- [ ] **Step 8: Commit**

```bash
git add ui/lib/types/governance.ts \
        ui/lib/store/apis/governanceApi.ts \
        ui/app/workspace/governance/users/page.tsx \
        ui/app/workspace/governance/views/userDialog.tsx
git commit -m "feat(ui): add Users governance page with list, search, create/edit dialog, delete"
```

---

## Task 8: Frontend — SSO Config UI + Login Button

**Files:**
- Modify: `ui/app/workspace/scim/page.tsx`
- Modify: `ui/lib/store/apis/governanceApi.ts`
- Modify: `ui/lib/types/governance.ts`
- Modify: login page (locate with: `find ui/app -name "page.tsx" | xargs grep -l "login\|Login\|password" | head -5`)

- [ ] **Step 1: Add SSOConfig type**

In `ui/lib/types/governance.ts`, add:

```ts
export interface SSOConfig {
  id: string;
  provider: "okta" | "entra";
  issuer_url: string;
  client_id: string;
  role_claim_key: string;
  group_claim_key: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface CreateSSOConfigRequest {
  provider: "okta" | "entra";
  issuer_url: string;
  client_id: string;
  client_secret: string;
  role_claim_key?: string;
  group_claim_key?: string;
}
```

- [ ] **Step 2: Add SSO config RTK Query endpoints**

In `ui/lib/store/apis/governanceApi.ts`, add:

```ts
// SSO Configs
getSSOConfigs: builder.query<{ configs: SSOConfig[] }, void>({
  query: () => "/governance/sso/configs",
  providesTags: ["SSOConfigs"],
}),

createSSOConfig: builder.mutation<{ config: SSOConfig }, CreateSSOConfigRequest>({
  query: (data) => ({ url: "/governance/sso/configs", method: "POST", body: data }),
  invalidatesTags: ["SSOConfigs"],
}),

updateSSOConfig: builder.mutation<{ config: SSOConfig }, { id: string; data: Partial<SSOConfig> & { client_secret?: string } }>({
  query: ({ id, data }) => ({ url: `/governance/sso/configs/${id}`, method: "PUT", body: data }),
  invalidatesTags: ["SSOConfigs"],
}),

deleteSSOConfig: builder.mutation<{ message: string }, string>({
  query: (id) => ({ url: `/governance/sso/configs/${id}`, method: "DELETE" }),
  invalidatesTags: ["SSOConfigs"],
}),

testSSOConfig: builder.mutation<{ message: string }, string>({
  query: (id) => ({ url: `/governance/sso/configs/${id}/test`, method: "POST" }),
}),
```

Add `"SSOConfigs"` to `tagTypes`.

- [ ] **Step 3: Replace scim/page.tsx with tabbed layout**

```tsx
"use client";

import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import SSOConfigTab from "./views/ssoConfigTab";

export default function SCIMPage() {
  return (
    <div className="mx-auto w-full max-w-7xl p-6">
      <h1 className="text-2xl font-semibold mb-6">User Provisioning</h1>
      <Tabs defaultValue="sso">
        <TabsList>
          <TabsTrigger value="sso">SSO / IdP Settings</TabsTrigger>
          <TabsTrigger value="scim">SCIM</TabsTrigger>
        </TabsList>
        <TabsContent value="sso">
          <SSOConfigTab />
        </TabsContent>
        <TabsContent value="scim">
          <div className="flex items-center gap-2 py-12 text-muted-foreground">
            <span>SCIM provisioning</span>
            <span className="inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-semibold">
              Coming soon
            </span>
          </div>
        </TabsContent>
      </Tabs>
    </div>
  );
}
```

- [ ] **Step 4: Create ssoConfigTab.tsx**

Create `ui/app/workspace/scim/views/ssoConfigTab.tsx`:

```tsx
"use client";

import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { toast } from "sonner";
import {
  useGetSSOConfigsQuery,
  useCreateSSOConfigMutation,
  useUpdateSSOConfigMutation,
  useDeleteSSOConfigMutation,
  useTestSSOConfigMutation,
} from "@/lib/store";
import { SSOConfig } from "@/lib/types/governance";

export default function SSOConfigTab() {
  const { data, isLoading } = useGetSSOConfigsQuery();
  const [createConfig] = useCreateSSOConfigMutation();
  const [updateConfig] = useUpdateSSOConfigMutation();
  const [deleteConfig] = useDeleteSSOConfigMutation();
  const [testConfig] = useTestSSOConfigMutation();
  const [isTesting, setIsTesting] = useState<string | null>(null);

  const [form, setForm] = useState({
    provider: "okta" as "okta" | "entra",
    issuer_url: "",
    client_id: "",
    client_secret: "",
    role_claim_key: "",
    group_claim_key: "",
  });
  const [showAdvanced, setShowAdvanced] = useState(false);

  const handleCreate = async () => {
    try {
      await createConfig(form).unwrap();
      toast.success("SSO config created");
      setForm({ provider: "okta", issuer_url: "", client_id: "", client_secret: "", role_claim_key: "", group_claim_key: "" });
    } catch {
      toast.error("Failed to create SSO config");
    }
  };

  const handleToggleEnabled = async (cfg: SSOConfig) => {
    try {
      await updateConfig({ id: cfg.id, data: { enabled: !cfg.enabled } }).unwrap();
      toast.success(cfg.enabled ? "SSO disabled" : "SSO enabled");
    } catch {
      toast.error("Failed to update SSO config");
    }
  };

  const handleTest = async (id: string) => {
    setIsTesting(id);
    try {
      await testConfig(id).unwrap();
      toast.success("Provider reachable");
    } catch {
      toast.error("Cannot reach provider");
    } finally {
      setIsTesting(null);
    }
  };

  const handleDelete = async (id: string) => {
    if (!confirm("Delete this SSO config?")) return;
    try {
      await deleteConfig(id).unwrap();
      toast.success("Config deleted");
    } catch {
      toast.error("Failed to delete config");
    }
  };

  return (
    <div className="space-y-8 py-4">
      {/* Existing configs */}
      {isLoading ? (
        <p className="text-muted-foreground">Loading...</p>
      ) : (
        (data?.configs ?? []).map((cfg) => (
          <div key={cfg.id} className="border rounded-lg p-4 space-y-3">
            <div className="flex items-center justify-between">
              <div>
                <p className="font-medium capitalize">{cfg.provider}</p>
                <p className="text-sm text-muted-foreground">{cfg.issuer_url}</p>
              </div>
              <div className="flex items-center gap-3">
                <Button
                  variant="outline"
                  size="sm"
                  disabled={isTesting === cfg.id}
                  onClick={() => handleTest(cfg.id)}
                >
                  {isTesting === cfg.id ? "Testing..." : "Test Connection"}
                </Button>
                <Switch
                  checked={cfg.enabled}
                  onCheckedChange={() => handleToggleEnabled(cfg)}
                />
                <Button variant="ghost" size="sm" className="text-destructive" onClick={() => handleDelete(cfg.id)}>
                  Delete
                </Button>
              </div>
            </div>
          </div>
        ))
      )}

      {/* Add new config form */}
      <div className="border rounded-lg p-4 space-y-4">
        <h3 className="font-medium">Add New Provider</h3>
        <div className="grid grid-cols-2 gap-4">
          <div className="space-y-1.5">
            <Label>Provider</Label>
            <Select value={form.provider} onValueChange={(v) => setForm({ ...form, provider: v as "okta" | "entra" })}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="okta">Okta</SelectItem>
                <SelectItem value="entra">Microsoft Entra</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1.5">
            <Label>Issuer URL</Label>
            <Input placeholder="https://dev-12345.okta.com" value={form.issuer_url} onChange={(e) => setForm({ ...form, issuer_url: e.target.value })} />
          </div>
          <div className="space-y-1.5">
            <Label>Client ID</Label>
            <Input value={form.client_id} onChange={(e) => setForm({ ...form, client_id: e.target.value })} />
          </div>
          <div className="space-y-1.5">
            <Label>Client Secret</Label>
            <Input type="password" value={form.client_secret} onChange={(e) => setForm({ ...form, client_secret: e.target.value })} />
          </div>
        </div>

        <button
          className="text-sm text-muted-foreground underline"
          onClick={() => setShowAdvanced(!showAdvanced)}
        >
          {showAdvanced ? "Hide" : "Show"} advanced options
        </button>

        {showAdvanced && (
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-1.5">
              <Label>Role Claim Key</Label>
              <Input placeholder="bifrostRole" value={form.role_claim_key} onChange={(e) => setForm({ ...form, role_claim_key: e.target.value })} />
            </div>
            <div className="space-y-1.5">
              <Label>Group Claim Key</Label>
              <Input placeholder="groups" value={form.group_claim_key} onChange={(e) => setForm({ ...form, group_claim_key: e.target.value })} />
            </div>
          </div>
        )}

        <Button onClick={handleCreate} disabled={!form.issuer_url || !form.client_id || !form.client_secret}>
          Add Provider
        </Button>
      </div>
    </div>
  );
}
```

- [ ] **Step 5: Add edit form to ssoConfigTab**

In `ssoConfigTab.tsx`, add edit state and an edit form that appears when clicking "Edit" on an existing config. Add after the existing state declarations:

```tsx
const [editingId, setEditingId] = useState<string | null>(null);
const [editForm, setEditForm] = useState({
  issuer_url: "", client_id: "", client_secret: "",
  role_claim_key: "", group_claim_key: "",
});

const startEdit = (cfg: SSOConfig) => {
  setEditingId(cfg.id);
  setEditForm({ issuer_url: cfg.issuer_url, client_id: cfg.client_id, client_secret: "",
    role_claim_key: cfg.role_claim_key, group_claim_key: cfg.group_claim_key });
};

const handleEdit = async () => {
  if (!editingId) return;
  try {
    await updateConfig({ id: editingId, data: { ...editForm } }).unwrap();
    toast.success("Config updated");
    setEditingId(null);
  } catch {
    toast.error("Failed to update config");
  }
};
```

In the existing config card, add an "Edit" button alongside the existing controls:

```tsx
<Button variant="ghost" size="sm" onClick={() => startEdit(cfg)}>Edit</Button>
```

Below the card, add a conditional edit form that appears when `editingId === cfg.id`:

```tsx
{editingId === cfg.id && (
  <div className="border rounded-lg p-4 space-y-4 mt-2 bg-muted/30">
    <h4 className="font-medium text-sm">Edit Provider</h4>
    <div className="grid grid-cols-2 gap-4">
      <div className="space-y-1.5">
        <Label>Issuer URL</Label>
        <Input value={editForm.issuer_url} onChange={(e) => setEditForm({ ...editForm, issuer_url: e.target.value })} />
      </div>
      <div className="space-y-1.5">
        <Label>Client ID</Label>
        <Input value={editForm.client_id} onChange={(e) => setEditForm({ ...editForm, client_id: e.target.value })} />
      </div>
      <div className="space-y-1.5 col-span-2">
        <Label>Client Secret <span className="text-muted-foreground">(leave blank to keep existing)</span></Label>
        <Input type="password" value={editForm.client_secret} onChange={(e) => setEditForm({ ...editForm, client_secret: e.target.value })} />
      </div>
      <div className="space-y-1.5">
        <Label>Role Claim Key</Label>
        <Input value={editForm.role_claim_key} onChange={(e) => setEditForm({ ...editForm, role_claim_key: e.target.value })} />
      </div>
      <div className="space-y-1.5">
        <Label>Group Claim Key</Label>
        <Input value={editForm.group_claim_key} onChange={(e) => setEditForm({ ...editForm, group_claim_key: e.target.value })} />
      </div>
    </div>
    <div className="flex gap-2">
      <Button onClick={handleEdit}>Save</Button>
      <Button variant="outline" onClick={() => setEditingId(null)}>Cancel</Button>
    </div>
  </div>
)}
```

> In `updateConfig`, strip `client_secret` from the update payload if it is an empty string (backend should ignore empty string and keep the existing secret). Add this guard in `handleEdit`:
> ```ts
> const payload = { ...editForm };
> if (!payload.client_secret) delete payload.client_secret;
> await updateConfig({ id: editingId, data: payload }).unwrap();
> ```

- [ ] **Step 6: Add SSO button to login page**

First find the login page:

```bash
find ui/app -name "page.tsx" | xargs grep -l "login\|password\|Login" 2>/dev/null | head -5
```

Then in that file, find where auth is checked and add the SSO button. The existing call to `is-auth-enabled` already exists — add `sso_enabled` to the destructure and render the button:

```tsx
// Add sso_enabled to the existing response destructure
const { is_auth_enabled, has_valid_token, sso_enabled } = authStatus; // adjust to match existing code

// Add below the existing login form:
{sso_enabled && (
  <Button
    variant="outline"
    className="w-full"
    onClick={() => { window.location.href = "/api/sso/login"; }}
  >
    Sign in with SSO
  </Button>
)}
```

- [ ] **Step 7: Test in browser**

1. Navigate to `/workspace/scim` — verify tabbed layout, SSO and SCIM tabs present
2. Add an Okta config, click "Test Connection"
3. Click "Edit" on a config — edit form appears inline, save updates fields
4. Enable a config — other configs disable
5. Navigate to login page — SSO button hidden by default, appears after enabling a config

- [ ] **Step 8: Commit**

```bash
git add ui/app/workspace/scim/page.tsx \
        ui/app/workspace/scim/views/ssoConfigTab.tsx \
        ui/lib/types/governance.ts \
        ui/lib/store/apis/governanceApi.ts
git commit -m "feat(ui): add SSO config tab with create/edit/delete/test and SSO login button"
```

---

## Task 9: User Profile in Sidebar (SSO Session Identity)

**Goal**: After SSO login, the sidebar shows the logged-in user's name/email via a popover, matching the existing SCIM OAuth UX.

**Root cause**: The SSO callback sets a session cookie and redirects to `/workspace`, but never exposes user identity to the frontend. The sidebar reads `getUserInfo()` from localStorage, which is only populated by the SCIM OAuth flow — not SSO. There is no `GET /api/session/me` endpoint.

### Steps

- [ ] **Step 1: Add `GET /api/session/me` to SessionHandler**

In `transports/bifrost-http/handlers/session.go`, add a new handler that reads the session token from cookie/header, looks up the session, fetches the user row, and returns `{id, name, email}`.

Note: `session.UserID` is `*string` (defined in Task 2) — nil-check and dereference before use. Use the existing `GetUser` method from Task 3 (struct is `GovernanceUsersTable`, not `TableGovernanceUser`):

```go
// me handles GET /api/session/me - Returns current logged-in user identity
func (h *SessionHandler) me(ctx *fasthttp.RequestCtx) {
    token := ""
    if authHeader := string(ctx.Request.Header.Peek("Authorization")); strings.HasPrefix(authHeader, "Bearer ") {
        token = strings.TrimPrefix(authHeader, "Bearer ")
    }
    if token == "" {
        token = string(ctx.Request.Header.Cookie("token"))
    }
    if token == "" {
        SendError(ctx, fasthttp.StatusUnauthorized, "no session token")
        return
    }
    session, err := h.configStore.GetSession(ctx, token)
    if err != nil || session == nil || session.ExpiresAt.Before(time.Now()) {
        SendError(ctx, fasthttp.StatusUnauthorized, "invalid or expired session")
        return
    }
    if session.UserID == nil {
        SendError(ctx, fasthttp.StatusUnauthorized, "session has no user")
        return
    }
    user, err := h.configStore.GetUser(ctx, *session.UserID)
    if err != nil || user == nil {
        SendError(ctx, fasthttp.StatusNotFound, "user not found")
        return
    }
    SendJSON(ctx, map[string]any{
        "id":    user.ID,
        "name":  user.Name,
        "email": user.Email,
    })
}
```

Register in `RegisterRoutes`:
```go
r.GET("/api/session/me", lib.ChainMiddlewares(h.me, middlewares...))
```

- [ ] **Step 2: Add RTK Query endpoint `useGetSessionMeQuery`**

In `ui/lib/store/apis/sessionApi.ts` (or equivalent), add:
```ts
getSessionMe: builder.query<{ id: string; name?: string; email?: string }, void>({
    query: () => "/session/me",  // baseApi already includes /api prefix
}),
```

Export `useGetSessionMeQuery` hook.

- [ ] **Step 3: Populate userInfo in sidebar after SSO login**

In `ui/components/sidebar.tsx`:

1. Add import at the top (alongside existing store imports):
```tsx
import { useGetSessionMeQuery } from "@/lib/store";
```

2. Replace the `useEffect` that calls `getUserInfo()`:

```tsx
const { data: sessionMe } = useGetSessionMeQuery(undefined, { skip: !IS_ENTERPRISE });

useEffect(() => {
    if (IS_ENTERPRISE) {
        const stored = getUserInfo();
        if (stored) {
            setUserInfo(stored);
        } else if (sessionMe) {
            // SSO login: no localStorage entry, use /api/session/me
            setUserInfo({ id: sessionMe.id, name: sessionMe.name, email: sessionMe.email });
        }
    }
}, [sessionMe]);
```

Note: pass `id` alongside `name`/`email` to satisfy the `UserInfo` interface. SCIM OAuth (localStorage) takes priority; SSO login falls back to the API.

- [ ] **Step 4: Verify in browser**

1. Enable auth, configure SSO
2. Click "Sign in with SSO", complete Keycloak login
3. Check sidebar bottom — user name/email popover should appear
4. Click logout — popover should disappear

- [ ] **Step 5: Commit**

```bash
git add transports/bifrost-http/handlers/session.go \
        ui/lib/store/apis/sessionApi.ts \
        ui/components/sidebar.tsx
git commit -m "feat(sso): expose session identity via /api/session/me and show user profile in sidebar"
```

---

## Self-Review

### Spec coverage

| Spec requirement | Task |
|-----------------|------|
| Phase 0 — Teams UI unblock | Task 0 |
| `governance_users` table | Task 1 |
| `governance_sso_configs` + `governance_sso_nonces` tables | Task 2 |
| `user_id` + `auth_method` on sessions | Task 2 |
| ConfigStore user CRUD + UpsertUserByEmail | Task 3 |
| ConfigStore SSO config + nonce CRUD | Task 4 |
| Users HTTP handler `/api/governance/users` | Task 5 |
| OIDCProvider interface + Okta/Entra adapters | Task 6 |
| PKCE initiate + callback + JWKS cache | Task 6 |
| SSRF guard on test-connection | Task 6 |
| `sso_enabled` on `is-auth-enabled` response | Task 6 |
| Single-SSO enforcement (EnableSSOConfig disables others) | Task 4 + 6 |
| Governance in-memory sync on user CRUD | Task 5 (`UserGovernanceSync` interface + calls in create/update/delete handlers) |
| Users UI page (list, create dialog, edit dialog, delete) | Task 7 |
| SSO config UI (tabbed /workspace/scim, create/edit/delete/test) | Task 8 |
| Login page SSO button (provider-agnostic `/api/sso/login`) | Task 8 |

All spec requirements covered. **Explicitly deferred** (out of scope per user decision): budget/rate-limit assignment UI in UserDialog (team assignment is included; budget/rate-limit require a separate budget creation flow not yet specced).

### Type consistency check

- `GovernanceUsersTable` — defined Task 1, used in Task 3 (handler), Task 6 (SSO UpsertUserByEmail) ✅
- `GovernanceSSOConfigsTable` — defined Task 2, used Task 4 (CRUD), Task 6 (enable enforcement) ✅
- `GovernanceSSONoncesTable` — defined Task 2, used Task 4 (`ConsumeAndDeleteSSONonce`), Task 6 (PKCE) ✅
- `tables.TableBudget{}` / `tables.TableRateLimit{}` — used in Task 3 `DeleteUser` cascade ✅
- `OIDCProvider` interface — defined Task 6 `sso_adapters.go`, implemented by `OktaAdapter` + `EntraAdapter` ✅
- `providerRegistry` map — defined Task 6 `sso_adapters.go`, accessed in `sso.go` callback ✅
- `generateState()` — defined `sso_adapters.go`, reused in `createSession` ✅
- `safeHTTPClient` — defined once in `sso.go`, used for token exchange + JWKS + test-connection ✅
- `isNotFound` helper — used in Task 5 and 6; check that it exists or add it ✅ (noted in Task 5)
- Frontend: `GovernanceUser` / `SSOConfig` types defined Task 7+8, consumed by RTK Query hooks ✅

### Placeholder scan

No TODOs, TBDs, or "implement later" present. All code is complete. Two "check X in the codebase" notes are present for router import paths and `CreateSession` method name — these are necessary lookup instructions, not placeholders, as the exact names must match the existing codebase.
