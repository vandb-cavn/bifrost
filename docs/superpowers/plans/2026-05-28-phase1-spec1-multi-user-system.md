# Multi-User System (Phase 1, Spec 1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the single-admin model in Bifrost with a real `users` table, link sessions to users, and expose user management + auth settings API endpoints — all without logging anyone out.

**Architecture:** A dialect-aware DB migration creates the `users` table and back-fills existing `sessions` rows to the new admin user. `validateSession` is refactored from a bool-returning helper to one that returns the full `TableUser`, which is attached to each authenticated request context. New `/api/users` and `/api/auth/settings` routes handle user CRUD.

**Tech Stack:** Go, GORM (SQLite + Postgres), fasthttp router, bcrypt via `encrypt` package, React/TypeScript (login form).

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `framework/configstore/tables/users.go` | Create | `TableUser` struct |
| `framework/configstore/tables/sessions.go` | Modify | Add `UserID *string` field |
| `framework/configstore/tables/config.go` | Modify | Add `ConfigSessionExpiryHoursKey` constant |
| `framework/configstore/clientconfig.go` | Modify | Add `SessionExpiryHours int` to `AuthConfig` |
| `framework/configstore/store.go` | Modify | Add user CRUD + `DeleteSessionsForUser` to `ConfigStore` interface |
| `core/schemas/bifrost.go` | Modify | Add `BifrostContextKeyCurrentUser` context key |
| `framework/configstore/migrations.go` | Modify | Add `migrationAddUsersTableAndSessionUserID` + call it |
| `framework/configstore/rdb.go` | Modify | Implement user CRUD; update `GetAuthConfig`/`UpdateAuthConfig` |
| `transports/bifrost-http/handlers/middlewares.go` | Modify | Refactor `validateSession`; attach user to request context |
| `transports/bifrost-http/handlers/session.go` | Modify | Email login; `session_expiry_hours`; `/api/auth/settings` |
| `transports/bifrost-http/handlers/users.go` | Create | All `/api/users` endpoints |
| `transports/bifrost-http/server/server.go` | Modify | Register `UserHandler` |
| `ui/app/_fallbacks/enterprise/components/login/loginView.tsx` | Modify | `username` → `email` field |
| `ui/lib/store/apis/sessionApi.ts` | Modify | `LoginRequest.username` → `email` |

---

## Task 1: Create `TableUser` struct

**Files:**
- Create: `framework/configstore/tables/users.go`

- [ ] **Step 1: Write the failing test**

Add to `framework/configstore/migrations_test.go`:

```go
func TestTableUser_HasExpectedColumns(t *testing.T) {
    db := setupTestDB(t)
    err := db.AutoMigrate(&tables.TableUser{})
    require.NoError(t, err)

    cols := []string{"id", "email", "name", "role", "password_hash", "is_active", "last_login_at", "created_at", "updated_at"}
    for _, col := range cols {
        exists, err := hasColumn(db, "users", col)
        require.NoError(t, err)
        assert.True(t, exists, "column %s should exist in users table", col)
    }
}
```

- [ ] **Step 2: Run test — verify it fails**

```
cd framework/configstore && go test -run TestTableUser_HasExpectedColumns -v
```
Expected: FAIL — `tables.TableUser` undefined.

- [ ] **Step 3: Create `framework/configstore/tables/users.go`**

```go
package tables

import "time"

// TableUser represents a Bifrost dashboard user.
type TableUser struct {
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

func (TableUser) TableName() string { return "users" }
```

- [ ] **Step 4: Run test — verify it passes**

```
cd framework/configstore && go test -run TestTableUser_HasExpectedColumns -v
```
Expected: PASS.

- [ ] **Step 5: Compile check**

```
cd framework/configstore && go build ./...
```
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add framework/configstore/tables/users.go framework/configstore/migrations_test.go
git commit -m "feat(users): add TableUser struct"
```

---

## Task 2: Add `UserID` to `SessionsTable` and add config constant

**Files:**
- Modify: `framework/configstore/tables/sessions.go:12`
- Modify: `framework/configstore/tables/config.go`

- [ ] **Step 1: Add `ConfigSessionExpiryHoursKey` constant to `tables/config.go`**

After the existing constants block (line 12), add:

```go
ConfigSessionExpiryHoursKey     = "session_expiry_hours"
```

The full constants block becomes:
```go
const (
    ConfigAdminUsernameKey          = "admin_username"
    ConfigAdminPasswordKey          = "admin_password"
    ConfigIsAuthEnabledKey          = "is_auth_enabled"
    ConfigDisableAuthOnInferenceKey = "disable_auth_on_inference"
    ConfigProxyKey                  = "proxy_config"
    ConfigRestartRequiredKey        = "restart_required"
    ConfigHeaderFilterKey           = "header_filter_config"
    ConfigSessionExpiryHoursKey     = "session_expiry_hours"
)
```

- [ ] **Step 2: Add `UserID *string` to `SessionsTable`**

In `framework/configstore/tables/sessions.go`, add after `TokenHash` field:

```go
UserID *string `gorm:"type:varchar(255)" json:"-"`
```

The full struct becomes:
```go
type SessionsTable struct {
    ID               int        `gorm:"primaryKey;autoIncrement" json:"id"`
    Token            string     `gorm:"type:text;not null;uniqueIndex" json:"token"`
    ExpiresAt        time.Time  `gorm:"index;not null" json:"expires_at,omitempty"`
    CreatedAt        time.Time  `gorm:"index;not null" json:"created_at"`
    UpdatedAt        time.Time  `gorm:"index;not null" json:"updated_at"`
    EncryptionStatus string     `gorm:"type:varchar(20);default:'plain_text'" json:"-"`
    TokenHash        string     `gorm:"type:varchar(64);index:idx_session_token_hash,unique" json:"-"`
    UserID           *string    `gorm:"type:varchar(255)" json:"-"`
}
```

- [ ] **Step 3: Build check**

```
cd framework/configstore && go build ./...
```
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add framework/configstore/tables/sessions.go framework/configstore/tables/config.go
git commit -m "feat(users): add UserID to sessions, add session_expiry_hours config key"
```

---

## Task 3: Extend `AuthConfig` struct and `ConfigStore` interface

**Files:**
- Modify: `framework/configstore/clientconfig.go:1364`
- Modify: `framework/configstore/store.go:242`
- Modify: `core/schemas/bifrost.go:191`

- [ ] **Step 1: Add `SessionExpiryHours` to `AuthConfig`**

In `framework/configstore/clientconfig.go`, change the `AuthConfig` struct at line 1364:

```go
type AuthConfig struct {
    AdminUserName          *schemas.EnvVar `json:"admin_username"`
    AdminPassword          *schemas.EnvVar `json:"admin_password"`
    IsEnabled              bool            `json:"is_enabled"`
    DisableAuthOnInference bool            `json:"disable_auth_on_inference"`
    SessionExpiryHours     int             `json:"session_expiry_hours"`
}
```

- [ ] **Step 2: Add user CRUD methods to `ConfigStore` interface**

In `framework/configstore/store.go`, add after the Session CRUD block (after `FlushSessions`, around line 259):

```go
// User CRUD
GetUserByID(ctx context.Context, id string) (*tables.TableUser, error)
GetUserByEmail(ctx context.Context, email string) (*tables.TableUser, error)
ListUsers(ctx context.Context) ([]tables.TableUser, error)
CreateUser(ctx context.Context, user *tables.TableUser) error
UpdateUser(ctx context.Context, user *tables.TableUser) error
UpdateUserActive(ctx context.Context, userID string, active bool) error
UpdateUserRole(ctx context.Context, userID string, role string) error
UpdateUserPassword(ctx context.Context, userID string, passwordHash string) error
UpdateUserLastLogin(ctx context.Context, userID string) error
DeleteSessionsForUser(ctx context.Context, userID string, excludeToken *string) error
```

- [ ] **Step 3: Add `BifrostContextKeyCurrentUser` to schemas**

In `core/schemas/bifrost.go`, in the `const` block that starts at line 191, add after `BifrostContextKeySessionToken`:

```go
BifrostContextKeyCurrentUser BifrostContextKey = "bifrost-current-user" // *tables.TableUser — set by auth middleware after session validation
```

Note: this is a string key; handlers store and retrieve `*tables.TableUser` values using this key.

- [ ] **Step 4: Build check — expected errors**

```
cd framework/configstore && go build ./...
```
Expected: errors because `RDBConfigStore` no longer satisfies `ConfigStore` (missing new methods). This is expected — we fix it in Task 5.

- [ ] **Step 5: Commit**

```bash
git add framework/configstore/clientconfig.go framework/configstore/store.go core/schemas/bifrost.go
git commit -m "feat(users): extend AuthConfig and ConfigStore interface with user CRUD"
```

---

## Task 4: DB migration

**Files:**
- Modify: `framework/configstore/migrations.go` (add function + call at bottom of `RunMigration`)

- [ ] **Step 1: Write migration test**

Add to `framework/configstore/migrations_test.go`:

```go
func TestMigrationAddUsersTableAndSessionUserID_BranchA(t *testing.T) {
    // Branch A: admin credentials exist
    db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
    require.NoError(t, err)

    // Pre-seed: run all migrations up to sessions table
    ctx := context.Background()
    err = TriggerMigrations(ctx, db)
    require.NoError(t, err)

    // Seed admin credentials in governance_config
    require.NoError(t, db.Exec("INSERT INTO governance_config (key, value) VALUES (?, ?) ON CONFLICT DO NOTHING",
        tables.ConfigAdminUsernameKey, "admin").Error)
    require.NoError(t, db.Exec("INSERT INTO governance_config (key, value) VALUES (?, ?) ON CONFLICT DO NOTHING",
        tables.ConfigAdminPasswordKey, "$2a$10$fakehash").Error)

    // Run only the new migration
    err = migrationAddUsersTableAndSessionUserID(ctx, db)
    require.NoError(t, err)

    // users table exists
    assert.True(t, db.Migrator().HasTable(&tables.TableUser{}))

    // admin user inserted
    var user tables.TableUser
    err = db.Where("email = ?", "admin@localhost").First(&user).Error
    require.NoError(t, err)
    assert.Equal(t, "admin", user.Role)
    assert.Equal(t, "admin", user.Name)
    assert.True(t, user.IsActive)
    require.NotNil(t, user.PasswordHash)
    assert.Equal(t, "$2a$10$fakehash", *user.PasswordHash)

    // sessions.user_id column exists
    exists, err := hasColumn(db, "sessions", "user_id")
    require.NoError(t, err)
    assert.True(t, exists)
}

func TestMigrationAddUsersTableAndSessionUserID_BranchB(t *testing.T) {
    // Branch B: no admin credentials
    db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
    require.NoError(t, err)

    ctx := context.Background()
    err = TriggerMigrations(ctx, db)
    require.NoError(t, err)

    err = migrationAddUsersTableAndSessionUserID(ctx, db)
    require.NoError(t, err)

    assert.True(t, db.Migrator().HasTable(&tables.TableUser{}))

    var count int64
    db.Model(&tables.TableUser{}).Count(&count)
    assert.Equal(t, int64(0), count)

    exists, err := hasColumn(db, "sessions", "user_id")
    require.NoError(t, err)
    assert.True(t, exists)
}

func TestMigrationAddUsersTableAndSessionUserID_Idempotent(t *testing.T) {
    db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
    require.NoError(t, err)

    ctx := context.Background()
    err = TriggerMigrations(ctx, db)
    require.NoError(t, err)

    // Run twice — should not error
    err = migrationAddUsersTableAndSessionUserID(ctx, db)
    require.NoError(t, err)
    err = migrationAddUsersTableAndSessionUserID(ctx, db)
    require.NoError(t, err)
}
```

- [ ] **Step 2: Run tests — verify they fail**

```
cd framework/configstore && go test -run "TestMigrationAddUsersTableAndSessionUserID" -v
```
Expected: FAIL — function `migrationAddUsersTableAndSessionUserID` not yet defined.

- [ ] **Step 3: Add the migration function**

Add to end of `framework/configstore/migrations.go` (before the final `}`):

```go
// migrationAddUsersTableAndSessionUserID creates the users table and adds the
// user_id column to sessions. Branch A (admin credentials present) inserts the
// existing admin as the first user and back-fills all sessions. Branch B (no
// credentials) creates the empty table and a nullable column only.
func migrationAddUsersTableAndSessionUserID(ctx context.Context, db *gorm.DB) error {
    m := migrator.New(db, migrator.DefaultOptions, []*migrator.Migration{{
        ID: "add_users_table_and_session_user_id",
        Migrate: func(tx *gorm.DB) error {
            tx = tx.WithContext(ctx)
            mig := tx.Migrator()

            // 1. Create users table
            if !mig.HasTable(&tables.TableUser{}) {
                if err := mig.CreateTable(&tables.TableUser{}); err != nil {
                    return fmt.Errorf("failed to create users table: %w", err)
                }
            }

            // 2. Detect Branch A: admin credentials present in governance_config
            var adminUsername, adminPassword string
            tx.Model(&tables.TableGovernanceConfig{}).
                Where("key = ?", tables.ConfigAdminUsernameKey).
                Pluck("value", &adminUsername)
            tx.Model(&tables.TableGovernanceConfig{}).
                Where("key = ?", tables.ConfigAdminPasswordKey).
                Pluck("value", &adminPassword)

            var adminUUID string
            if adminUsername != "" && adminPassword != "" {
                // Check if admin user already inserted (idempotent re-run)
                var existing tables.TableUser
                err := tx.Where("email = ?", "admin@localhost").First(&existing).Error
                if err == nil {
                    adminUUID = existing.ID
                } else {
                    adminUUID = uuid.New().String()
                    now := time.Now()
                    if err := tx.Create(&tables.TableUser{
                        ID:           adminUUID,
                        Email:        "admin@localhost",
                        Name:         adminUsername,
                        Role:         "admin",
                        PasswordHash: &adminPassword,
                        IsActive:     true,
                        CreatedAt:    now,
                        UpdatedAt:    now,
                    }).Error; err != nil {
                        return fmt.Errorf("failed to insert admin user: %w", err)
                    }
                }
            }

            // 3. Add user_id column to sessions
            if !mig.HasColumn(&tables.SessionsTable{}, "user_id") {
                if err := tx.Exec("ALTER TABLE sessions ADD COLUMN user_id VARCHAR(255)").Error; err != nil {
                    return fmt.Errorf("failed to add user_id column to sessions: %w", err)
                }
            }

            // 4. Branch A: back-fill existing sessions to admin user
            if adminUUID != "" {
                if err := tx.Exec(
                    "UPDATE sessions SET user_id = ? WHERE user_id IS NULL", adminUUID,
                ).Error; err != nil {
                    return fmt.Errorf("failed to backfill session user_id: %w", err)
                }
            }

            // 5. Postgres only: enforce NOT NULL on sessions.user_id (Branch A only)
            if adminUUID != "" && tx.Dialector.Name() == "postgres" {
                if err := tx.Exec(
                    "ALTER TABLE sessions ALTER COLUMN user_id SET NOT NULL",
                ).Error; err != nil {
                    return fmt.Errorf("failed to set sessions.user_id NOT NULL: %w", err)
                }
            }

            return nil
        },
        Rollback: func(tx *gorm.DB) error {
            tx = tx.WithContext(ctx)
            if tx.Migrator().HasColumn(&tables.SessionsTable{}, "user_id") {
                if err := tx.Exec("ALTER TABLE sessions DROP COLUMN user_id").Error; err != nil {
                    return fmt.Errorf("failed to drop sessions.user_id: %w", err)
                }
            }
            if tx.Migrator().HasTable(&tables.TableUser{}) {
                if err := tx.Migrator().DropTable(&tables.TableUser{}); err != nil {
                    return err
                }
            }
            return nil
        },
    }})
    if err := m.Migrate(); err != nil {
        return fmt.Errorf("error running add_users_table_and_session_user_id migration: %s", err.Error())
    }
    return nil
}
```

- [ ] **Step 4: Call the migration in `RunMigration`**

In `migrations.go`, find the end of `RunMigration` (just before the final `return nil`), add:

```go
if err := migrationAddUsersTableAndSessionUserID(ctx, db); err != nil {
    return err
}
```

- [ ] **Step 5: Check imports in migrations.go are sufficient**

The new function uses `uuid` and `time`. Verify `"github.com/google/uuid"` and `"time"` are already in the import block (they are — check by grepping). If not, add them.

```
grep -n '"github.com/google/uuid"\|"time"' framework/configstore/migrations.go | head -5
```

- [ ] **Step 6: Run migration tests**

```
cd framework/configstore && go test -run "TestMigrationAddUsersTableAndSessionUserID" -v
```
Expected: all 3 tests PASS.

- [ ] **Step 7: Run full migration suite to ensure no regressions**

```
cd framework/configstore && go test -run "TestTriggerMigrations|TestFullMigration" -v -timeout 60s
```
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add framework/configstore/migrations.go framework/configstore/migrations_test.go
git commit -m "feat(users): add migration for users table and sessions.user_id"
```

---

## Task 5: Implement user CRUD in `rdb.go` + update auth config

**Files:**
- Modify: `framework/configstore/rdb.go` (append new methods; edit `GetAuthConfig`/`UpdateAuthConfig`)

- [ ] **Step 1: Add sentinel errors**

Near the top of `rdb.go`, after the existing `var ErrNotFound` declaration, add:

```go
var (
    ErrLastAdmin    = fmt.Errorf("cannot remove the last admin")
    ErrLastActiveAdmin = fmt.Errorf("cannot deactivate the last admin")
)
```

(Verify that `ErrNotFound` is already declared and find its exact location with `grep -n "ErrNotFound" framework/configstore/rdb.go | head -3`.)

- [ ] **Step 2: Update `GetAuthConfig` to read `session_expiry_hours`**

Find `GetAuthConfig` (around line 4247). After reading `disableAuthOnInference`, add the following block before the `if username == nil || password == nil` guard:

```go
var sessionExpiryHoursStr string
if err := s.DB().WithContext(ctx).
    Model(&tables.TableGovernanceConfig{}).
    Where("key = ?", tables.ConfigSessionExpiryHoursKey).
    Pluck("value", &sessionExpiryHoursStr).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
    return nil, err
}
sessionExpiryHours := 720 // default 30 days
if sessionExpiryHoursStr != "" {
    if v, err := strconv.Atoi(sessionExpiryHoursStr); err == nil && v > 0 {
        sessionExpiryHours = v
    }
}
```

Then update the returned `AuthConfig` to include the new field:

```go
return &AuthConfig{
    AdminUserName:          schemas.NewEnvVar(*username),
    AdminPassword:          schemas.NewEnvVar(*password),
    IsEnabled:              isEnabled,
    DisableAuthOnInference: disableAuthOnInference,
    SessionExpiryHours:     sessionExpiryHours,
}, nil
```

- [ ] **Step 3: Update `UpdateAuthConfig` to write `session_expiry_hours`**

Find `UpdateAuthConfig` (around line 4288). Inside the transaction, after the four existing `tx.Save` calls, add:

```go
if config.SessionExpiryHours > 0 {
    if err := tx.Save(&tables.TableGovernanceConfig{
        Key:   tables.ConfigSessionExpiryHoursKey,
        Value: strconv.Itoa(config.SessionExpiryHours),
    }).Error; err != nil {
        return err
    }
}
```

- [ ] **Step 4: Add user CRUD methods at end of `rdb.go`**

Append the following methods:

```go
// GetUserByID retrieves a user by primary key.
func (s *RDBConfigStore) GetUserByID(ctx context.Context, id string) (*tables.TableUser, error) {
    var user tables.TableUser
    if err := s.DB().WithContext(ctx).First(&user, "id = ?", id).Error; err != nil {
        if errors.Is(err, gorm.ErrRecordNotFound) {
            return nil, nil
        }
        return nil, err
    }
    return &user, nil
}

// GetUserByEmail retrieves a user by email (case-insensitive).
func (s *RDBConfigStore) GetUserByEmail(ctx context.Context, email string) (*tables.TableUser, error) {
    var user tables.TableUser
    if err := s.DB().WithContext(ctx).Where("email = ?", strings.ToLower(strings.TrimSpace(email))).First(&user).Error; err != nil {
        if errors.Is(err, gorm.ErrRecordNotFound) {
            return nil, nil
        }
        return nil, err
    }
    return &user, nil
}

// ListUsers returns all users ordered by created_at ascending.
func (s *RDBConfigStore) ListUsers(ctx context.Context) ([]tables.TableUser, error) {
    var users []tables.TableUser
    if err := s.DB().WithContext(ctx).Order("created_at asc").Find(&users).Error; err != nil {
        return nil, err
    }
    return users, nil
}

// CreateUser inserts a new user row.
func (s *RDBConfigStore) CreateUser(ctx context.Context, user *tables.TableUser) error {
    return s.DB().WithContext(ctx).Create(user).Error
}

// UpdateUser saves changed fields on an existing user.
func (s *RDBConfigStore) UpdateUser(ctx context.Context, user *tables.TableUser) error {
    return s.DB().WithContext(ctx).Save(user).Error
}

// UpdateUserActive deactivates or re-activates a user.
// If deactivating: runs inside a transaction with a row-level lock (Postgres)
// to prevent concurrent requests from both passing the last-admin guard.
func (s *RDBConfigStore) UpdateUserActive(ctx context.Context, userID string, active bool) error {
    return s.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
        if !active {
            // Acquire lock on all active admin rows before counting
            if tx.Dialector.Name() == "postgres" {
                if err := tx.Exec("SELECT id FROM users WHERE role = 'admin' AND is_active = true FOR UPDATE").Error; err != nil {
                    return err
                }
            }
            var adminCount int64
            if err := tx.Model(&tables.TableUser{}).
                Where("role = ? AND is_active = ?", "admin", true).
                Count(&adminCount).Error; err != nil {
                return err
            }
            if adminCount <= 1 {
                var u tables.TableUser
                if err := tx.First(&u, "id = ?", userID).Error; err != nil {
                    return err
                }
                if u.Role == "admin" {
                    return ErrLastActiveAdmin
                }
            }
        }
        return tx.Model(&tables.TableUser{}).Where("id = ?", userID).Updates(map[string]any{
            "is_active":  active,
            "updated_at": time.Now(),
        }).Error
    })
}

// UpdateUserRole changes a user's role.
// Runs inside a transaction with a lock to prevent concurrent last-admin demotion.
func (s *RDBConfigStore) UpdateUserRole(ctx context.Context, userID string, role string) error {
    return s.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
        var u tables.TableUser
        if err := tx.First(&u, "id = ?", userID).Error; err != nil {
            return err
        }
        if u.Role == "admin" && role != "admin" {
            // Acquire lock before counting
            if tx.Dialector.Name() == "postgres" {
                if err := tx.Exec("SELECT id FROM users WHERE role = 'admin' AND is_active = true FOR UPDATE").Error; err != nil {
                    return err
                }
            }
            var adminCount int64
            if err := tx.Model(&tables.TableUser{}).
                Where("role = ? AND is_active = ?", "admin", true).
                Count(&adminCount).Error; err != nil {
                return err
            }
            if adminCount <= 1 {
                return ErrLastAdmin
            }
        }
        return tx.Model(&tables.TableUser{}).Where("id = ?", userID).Updates(map[string]any{
            "role":       role,
            "updated_at": time.Now(),
        }).Error
    })
}

// UpdateUserPassword sets a new bcrypt password hash for a user.
func (s *RDBConfigStore) UpdateUserPassword(ctx context.Context, userID string, passwordHash string) error {
    return s.DB().WithContext(ctx).Model(&tables.TableUser{}).Where("id = ?", userID).Updates(map[string]any{
        "password_hash": passwordHash,
        "updated_at":    time.Now(),
    }).Error
}

// UpdateUserLastLogin stamps last_login_at = now() for the given user.
func (s *RDBConfigStore) UpdateUserLastLogin(ctx context.Context, userID string) error {
    now := time.Now()
    return s.DB().WithContext(ctx).Model(&tables.TableUser{}).Where("id = ?", userID).Updates(map[string]any{
        "last_login_at": now,
        "updated_at":    now,
    }).Error
}

// DeleteSessionsForUser deletes all sessions for a user. If excludeToken is set,
// that session is kept (used when a user changes their own password).
func (s *RDBConfigStore) DeleteSessionsForUser(ctx context.Context, userID string, excludeToken *string) error {
    q := s.DB().WithContext(ctx).Where("user_id = ?", userID)
    if excludeToken != nil {
        // Sessions store the hashed token; look up by token hash
        hash := encrypt.HashSHA256(*excludeToken)
        q = q.Where("token_hash != ?", hash)
    }
    return q.Delete(&tables.SessionsTable{}).Error
}
```

- [ ] **Step 5: Verify imports in rdb.go**

The new code uses `strings`, `strconv`, `time`, and `encrypt`. Check they are imported:
```
grep -n '"strings"\|"strconv"\|"time"\|"encrypt"' framework/configstore/rdb.go | head -5
```
`strings` and `strconv` are already present. `time` is present. For `encrypt`, check the import path:
```
grep -rn "framework/encrypt" framework/configstore/rdb.go | head -3
```
If missing, add `"github.com/maximhq/bifrost/framework/encrypt"` to the import block.

- [ ] **Step 6: Build check — errors should now be gone**

```
cd framework/configstore && go build ./...
```
Expected: no errors (all `ConfigStore` interface methods implemented).

- [ ] **Step 7: Write CRUD unit tests**

Add to `framework/configstore/migrations_test.go`:

```go
func TestUserCRUD(t *testing.T) {
    db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
    require.NoError(t, err)

    ctx := context.Background()
    require.NoError(t, TriggerMigrations(ctx, db))

    store, err := NewRDBConfigStore(ctx, db)
    require.NoError(t, err)

    t.Run("CreateAndGetByEmail", func(t *testing.T) {
        hash := "$2a$10$fakehash"
        user := &tables.TableUser{
            ID:           "u1",
            Email:        "alice@example.com",
            Name:         "Alice",
            Role:         "operator",
            PasswordHash: &hash,
            IsActive:     true,
            CreatedAt:    time.Now(),
            UpdatedAt:    time.Now(),
        }
        require.NoError(t, store.CreateUser(ctx, user))

        got, err := store.GetUserByEmail(ctx, "ALICE@EXAMPLE.COM") // case-insensitive
        require.NoError(t, err)
        require.NotNil(t, got)
        assert.Equal(t, "u1", got.ID)
        assert.Equal(t, "alice@example.com", got.Email)
    })

    t.Run("ListUsers", func(t *testing.T) {
        users, err := store.ListUsers(ctx)
        require.NoError(t, err)
        assert.GreaterOrEqual(t, len(users), 1)
    })

    t.Run("UpdateUserActive_LastAdminGuard", func(t *testing.T) {
        adminHash := "$2a$10$admin"
        admin := &tables.TableUser{
            ID: "admin1", Email: "admin@localhost", Name: "Admin",
            Role: "admin", PasswordHash: &adminHash, IsActive: true,
            CreatedAt: time.Now(), UpdatedAt: time.Now(),
        }
        require.NoError(t, store.CreateUser(ctx, admin))

        // Deactivating the only admin must fail
        err := store.UpdateUserActive(ctx, "admin1", false)
        assert.Error(t, err)
        assert.ErrorIs(t, err, ErrLastActiveAdmin)
    })
}
```

- [ ] **Step 8: Run user CRUD tests**

```
cd framework/configstore && go test -run TestUserCRUD -v
```
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add framework/configstore/rdb.go framework/configstore/migrations_test.go
git commit -m "feat(users): implement user CRUD and update auth config in rdb.go"
```

---

## Task 6: Refactor `validateSession` and attach user to request context

**Files:**
- Modify: `transports/bifrost-http/handlers/middlewares.go:644`
- Modify: `transports/bifrost-http/handlers/oauth2.go:151`

- [ ] **Step 1: Write test for the new validateSession behavior**

Add to `transports/bifrost-http/handlers/middlewares_test.go` (create test if needed, look at existing patterns first):

```go
// This is a compile-time test — validateSession signature is checked indirectly
// via the isValidSession helper added below. No mock needed since we test integration
// via the login+session flow tests in session_test.go (Task 7).
```

(Skip unit test here — the refactor is wiring code. Integration coverage comes from the login tests in Task 7.)

- [ ] **Step 2: Replace `validateSession` function**

In `handlers/middlewares.go`, replace the function at line 644:

**Before:**
```go
func validateSession(_ *fasthttp.RequestCtx, store configstore.ConfigStore, token string) bool {
    session, err := store.GetSession(context.Background(), token)
    if err != nil || session == nil {
        return false
    }
    if session.ExpiresAt.Before(time.Now()) {
        return false
    }
    return true
}
```

**After:**
```go
// validateSession looks up the session token, loads the associated active user,
// and returns both. Returns (nil, nil, nil) if the session is not found/expired
// (a soft miss, not an error). Returns a non-nil error only for DB failures.
func validateSession(_ *fasthttp.RequestCtx, store configstore.ConfigStore, token string) (*tables.TableUser, error) {
    session, err := store.GetSession(context.Background(), token)
    if err != nil {
        return nil, fmt.Errorf("session lookup: %w", err)
    }
    if session == nil || session.ExpiresAt.Before(time.Now()) {
        return nil, nil
    }
    if session.UserID == nil {
        return nil, nil
    }
    user, err := store.GetUserByID(context.Background(), *session.UserID)
    if err != nil {
        return nil, fmt.Errorf("user lookup: %w", err)
    }
    if user == nil || !user.IsActive {
        return nil, nil
    }
    return user, nil
}
```

- [ ] **Step 3: Update all call sites in `AuthMiddleware.middleware()`**

There are 4 call sites inside `middleware()`. Replace each `validateSession(...)` bool check with the new pattern. The pattern to use:

```go
// OLD:
if validateSession(ctx, m.store, token) {
    ctx.SetUserValue(schemas.BifrostContextKeySessionToken, token)
    ctx.SetUserValue(schemas.IsLocalAdminContextKey, true)
    next(ctx)
    return
}
SendError(ctx, fasthttp.StatusUnauthorized, "Unauthorized")
return

// NEW (one call per site):
user, err := validateSession(ctx, m.store, token)
if err != nil {
    logger.Error("session validation error: %v", err)
    SendError(ctx, fasthttp.StatusUnauthorized, "Unauthorized")
    return
}
if user == nil {
    SendError(ctx, fasthttp.StatusUnauthorized, "Unauthorized")
    return
}
ctx.SetUserValue(schemas.BifrostContextKeySessionToken, token)
ctx.SetUserValue(schemas.BifrostContextKeyCurrentUser, user)
next(ctx)
return
```

Apply this pattern to all 4 locations:
1. WS ticket path (~line 893): replaces `validateSession(ctx, m.store, sessionToken)` check
2. `?token=` fallback (~line 904): replaces `validateSession(ctx, m.store, token)` check
3. Cookie WS fallback (~line 914): replaces `validateSession(ctx, m.store, cookieToken)` check
4. Bearer token path (~line 985): replaces `validateSession(ctx, m.store, token)` check

Remove the `ctx.SetUserValue(schemas.IsLocalAdminContextKey, true)` lines from these call sites (those were placeholders; real user is now in context).

Keep the `IsLocalAdmin = true` only in the two places where auth is disabled (lines 856-864) and in the Basic auth path (line 1017 — this is for inference-only clients that don't have user accounts yet).

- [ ] **Step 4: Update `oauth2.go` call site**

In `handlers/oauth2.go` at line 151, the `perUserCallbackRedirect` function calls `validateSession`. Replace:

```go
authenticated := cookieToken != "" && validateSession(ctx, store, cookieToken)
```

With:

```go
authenticated := func() bool {
    if cookieToken == "" {
        return false
    }
    user, err := validateSession(ctx, store, cookieToken)
    return err == nil && user != nil
}()
```

- [ ] **Step 5: Build check**

```
cd transports/bifrost-http && go build ./...
```
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add transports/bifrost-http/handlers/middlewares.go transports/bifrost-http/handlers/oauth2.go
git commit -m "feat(users): refactor validateSession to return TableUser, attach user to request context"
```

---

## Task 7: Update `session.go` — email login, session expiry from config, auth settings

**Files:**
- Modify: `transports/bifrost-http/handlers/session.go`

- [ ] **Step 1: Update `RegisterRoutes` to add auth settings routes**

In `session.go`, change `RegisterRoutes`:

```go
func (h *SessionHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
    r.POST("/api/session/login", lib.ChainMiddlewares(h.login, middlewares...))
    r.POST("/api/session/logout", lib.ChainMiddlewares(h.logout, middlewares...))
    r.GET("/api/session/is-auth-enabled", lib.ChainMiddlewares(h.isAuthEnabled, middlewares...))
    r.POST("/api/session/ws-ticket", lib.ChainMiddlewares(h.issueWSTicket, middlewares...))
    r.GET("/api/auth/settings", lib.ChainMiddlewares(h.getAuthSettings, middlewares...))
    r.PUT("/api/auth/settings", lib.ChainMiddlewares(h.updateAuthSettings, middlewares...))
}
```

- [ ] **Step 2: Replace the `login` handler**

Replace the entire `login` function with:

```go
// login handles POST /api/session/login — 4-step email-based login flow.
func (h *SessionHandler) login(ctx *fasthttp.RequestCtx) {
    if h.configStore == nil {
        SendError(ctx, fasthttp.StatusForbidden, "Authentication is not enabled")
        return
    }
    payload := struct {
        Email    string `json:"email"`
        Password string `json:"password"`
    }{}
    if err := json.Unmarshal(ctx.PostBody(), &payload); err != nil {
        SendError(ctx, fasthttp.StatusBadRequest, fmt.Sprintf("Invalid request format: %v", err))
        return
    }

    authConfig, err := h.configStore.GetAuthConfig(ctx)
    if err != nil {
        SendError(ctx, fasthttp.StatusInternalServerError, fmt.Sprintf("Failed to get auth config: %v", err))
        return
    }
    if authConfig == nil || !authConfig.IsEnabled {
        SendError(ctx, fasthttp.StatusForbidden, "Authentication is not enabled")
        return
    }

    // Step 1: normalize email, look up user
    email := strings.ToLower(strings.TrimSpace(payload.Email))
    user, err := h.configStore.GetUserByEmail(ctx, email)
    if err != nil {
        SendError(ctx, fasthttp.StatusInternalServerError, "Internal error")
        return
    }

    // Step 2: not found or inactive → same 401 (no user enumeration)
    if user == nil || !user.IsActive {
        SendError(ctx, fasthttp.StatusUnauthorized, "Invalid email or password")
        return
    }
    if user.PasswordHash == nil {
        SendError(ctx, fasthttp.StatusUnauthorized, "Invalid email or password")
        return
    }

    // Step 3: verify password
    match, err := encrypt.CompareHash(*user.PasswordHash, payload.Password)
    if err != nil || !match {
        SendError(ctx, fasthttp.StatusUnauthorized, "Invalid email or password")
        return
    }

    // Step 4: create session with configurable expiry
    expiryHours := authConfig.SessionExpiryHours
    if expiryHours <= 0 {
        expiryHours = 720 // default 30 days
    }
    expiry := time.Now().Add(time.Duration(expiryHours) * time.Hour)
    token := uuid.New().String()
    session := &tables.SessionsTable{
        Token:     token,
        UserID:    &user.ID,
        ExpiresAt: expiry,
        CreatedAt: time.Now(),
        UpdatedAt: time.Now(),
    }
    if err := h.configStore.CreateSession(ctx, session); err != nil {
        SendError(ctx, fasthttp.StatusInternalServerError, fmt.Sprintf("Failed to create session: %v", err))
        return
    }
    _ = h.configStore.UpdateUserLastLogin(ctx, user.ID)

    // Set session cookie
    cookie := fasthttp.AcquireCookie()
    defer fasthttp.ReleaseCookie(cookie)
    cookie.SetKey("token")
    cookie.SetValue(token)
    cookie.SetExpire(expiry)
    cookie.SetPath("/")
    cookie.SetHTTPOnly(true)
    cookie.SetSameSite(fasthttp.CookieSameSiteLaxMode)
    if string(ctx.Request.Header.Peek("X-Forwarded-Proto")) == "https" {
        cookie.SetSecure(true)
    }
    ctx.Response.Header.SetCookie(cookie)

    SendJSON(ctx, map[string]any{
        "message": "Login successful",
        "user": map[string]any{
            "id":    user.ID,
            "email": user.Email,
            "name":  user.Name,
            "role":  user.Role,
        },
    })
}
```

- [ ] **Step 3: Add `getAuthSettings` and `updateAuthSettings` handlers**

Append to `session.go`:

```go
// getAuthSettings handles GET /api/auth/settings
func (h *SessionHandler) getAuthSettings(ctx *fasthttp.RequestCtx) {
    if h.configStore == nil {
        SendError(ctx, fasthttp.StatusServiceUnavailable, "Config store not available")
        return
    }
    authConfig, err := h.configStore.GetAuthConfig(ctx)
    if err != nil {
        SendError(ctx, fasthttp.StatusInternalServerError, fmt.Sprintf("Failed to get auth config: %v", err))
        return
    }
    expiryHours := 720
    isEnabled := false
    if authConfig != nil {
        isEnabled = authConfig.IsEnabled
        if authConfig.SessionExpiryHours > 0 {
            expiryHours = authConfig.SessionExpiryHours
        }
    }
    SendJSON(ctx, map[string]any{
        "session_expiry_hours": expiryHours,
        "is_auth_enabled":      isEnabled,
    })
}

// updateAuthSettings handles PUT /api/auth/settings
func (h *SessionHandler) updateAuthSettings(ctx *fasthttp.RequestCtx) {
    if h.configStore == nil {
        SendError(ctx, fasthttp.StatusServiceUnavailable, "Config store not available")
        return
    }
    payload := struct {
        SessionExpiryHours *int `json:"session_expiry_hours"`
    }{}
    if err := json.Unmarshal(ctx.PostBody(), &payload); err != nil {
        SendError(ctx, fasthttp.StatusBadRequest, fmt.Sprintf("Invalid request format: %v", err))
        return
    }
    if payload.SessionExpiryHours == nil {
        SendError(ctx, fasthttp.StatusBadRequest, "session_expiry_hours is required")
        return
    }
    hours := *payload.SessionExpiryHours
    if hours < 1 || hours > 8760 {
        SendError(ctx, fasthttp.StatusBadRequest, "session_expiry_hours must be between 1 and 8760")
        return
    }
    authConfig, err := h.configStore.GetAuthConfig(ctx)
    if err != nil {
        SendError(ctx, fasthttp.StatusInternalServerError, fmt.Sprintf("Failed to get auth config: %v", err))
        return
    }
    if authConfig == nil {
        authConfig = &configstore.AuthConfig{}
    }
    authConfig.SessionExpiryHours = hours
    if err := h.configStore.UpdateAuthConfig(ctx, authConfig); err != nil {
        SendError(ctx, fasthttp.StatusInternalServerError, fmt.Sprintf("Failed to update auth settings: %v", err))
        return
    }
    SendJSON(ctx, map[string]any{
        "session_expiry_hours": hours,
    })
}
```

- [ ] **Step 4: Build check**

```
cd transports/bifrost-http && go build ./...
```
Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add transports/bifrost-http/handlers/session.go
git commit -m "feat(users): email-based login, session expiry from config, auth settings endpoints"
```

---

## Task 8: Create `users.go` handler and register it

**Files:**
- Create: `transports/bifrost-http/handlers/users.go`
- Modify: `transports/bifrost-http/server/server.go:1748`

- [ ] **Step 1: Create `handlers/users.go`**

```go
package handlers

import (
    "encoding/json"
    "fmt"
    "strings"
    "time"

    "github.com/fasthttp/router"
    "github.com/google/uuid"
    "github.com/maximhq/bifrost/core/schemas"
    "github.com/maximhq/bifrost/framework/configstore"
    "github.com/maximhq/bifrost/framework/configstore/tables"
    "github.com/maximhq/bifrost/framework/encrypt"
    "github.com/maximhq/bifrost/transports/bifrost-http/lib"
    "github.com/valyala/fasthttp"
)

// UserHandler manages HTTP requests for user management operations.
type UserHandler struct {
    configStore configstore.ConfigStore
}

// NewUserHandler creates a new user handler instance.
func NewUserHandler(configStore configstore.ConfigStore) *UserHandler {
    return &UserHandler{configStore: configStore}
}

// RegisterRoutes registers the user management routes.
func (h *UserHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
    r.GET("/api/users", lib.ChainMiddlewares(h.listUsers, middlewares...))
    r.POST("/api/users", lib.ChainMiddlewares(h.createUser, middlewares...))
    r.GET("/api/users/me", lib.ChainMiddlewares(h.getMe, middlewares...))
    r.GET("/api/users/{id}", lib.ChainMiddlewares(h.getUser, middlewares...))
    r.PUT("/api/users/{id}", lib.ChainMiddlewares(h.updateUser, middlewares...))
    r.PUT("/api/users/{id}/role", lib.ChainMiddlewares(h.updateRole, middlewares...))
    r.PUT("/api/users/{id}/password", lib.ChainMiddlewares(h.updatePassword, middlewares...))
    r.PUT("/api/users/{id}/active", lib.ChainMiddlewares(h.updateActive, middlewares...))
}

// currentUser extracts the authenticated user from the request context.
// Returns nil if no user is attached (e.g., auth disabled).
func currentUser(ctx *fasthttp.RequestCtx) *tables.TableUser {
    user, _ := ctx.UserValue(schemas.BifrostContextKeyCurrentUser).(*tables.TableUser)
    return user
}

// normalizeEmail lowercases and trims whitespace, returns "" if invalid.
func normalizeEmail(email string) string {
    e := strings.ToLower(strings.TrimSpace(email))
    if !strings.Contains(e, "@") || e == "" {
        return ""
    }
    return e
}

// listUsers handles GET /api/users — returns all users.
func (h *UserHandler) listUsers(ctx *fasthttp.RequestCtx) {
    users, err := h.configStore.ListUsers(ctx)
    if err != nil {
        SendError(ctx, fasthttp.StatusInternalServerError, fmt.Sprintf("Failed to list users: %v", err))
        return
    }
    SendJSON(ctx, map[string]any{"users": users})
}

// createUser handles POST /api/users — creates a new user.
func (h *UserHandler) createUser(ctx *fasthttp.RequestCtx) {
    payload := struct {
        Email    string `json:"email"`
        Name     string `json:"name"`
        Role     string `json:"role"`
        Password string `json:"password"`
    }{}
    if err := json.Unmarshal(ctx.PostBody(), &payload); err != nil {
        SendError(ctx, fasthttp.StatusBadRequest, fmt.Sprintf("Invalid request format: %v", err))
        return
    }
    email := normalizeEmail(payload.Email)
    if email == "" {
        SendError(ctx, fasthttp.StatusBadRequest, "invalid email address")
        return
    }
    if payload.Name == "" {
        SendError(ctx, fasthttp.StatusBadRequest, "name is required")
        return
    }
    if len(payload.Password) < 8 {
        SendError(ctx, fasthttp.StatusBadRequest, "password must be at least 8 characters")
        return
    }
    if payload.Role != "admin" && payload.Role != "operator" && payload.Role != "viewer" {
        SendError(ctx, fasthttp.StatusBadRequest, "role must be one of: admin, operator, viewer")
        return
    }

    // Check email uniqueness
    existing, err := h.configStore.GetUserByEmail(ctx, email)
    if err != nil {
        SendError(ctx, fasthttp.StatusInternalServerError, "Failed to check email uniqueness")
        return
    }
    if existing != nil {
        SendError(ctx, fasthttp.StatusConflict, "email already in use")
        return
    }

    hash, err := encrypt.Hash(payload.Password)
    if err != nil {
        SendError(ctx, fasthttp.StatusInternalServerError, "Failed to hash password")
        return
    }
    now := time.Now()
    user := &tables.TableUser{
        ID:           uuid.New().String(),
        Email:        email,
        Name:         payload.Name,
        Role:         payload.Role,
        PasswordHash: &hash,
        IsActive:     true,
        CreatedAt:    now,
        UpdatedAt:    now,
    }
    if err := h.configStore.CreateUser(ctx, user); err != nil {
        SendError(ctx, fasthttp.StatusInternalServerError, fmt.Sprintf("Failed to create user: %v", err))
        return
    }
    SendJSONWithStatus(ctx, user, fasthttp.StatusCreated)
}

// getMe handles GET /api/users/me — returns the current authenticated user.
func (h *UserHandler) getMe(ctx *fasthttp.RequestCtx) {
    user := currentUser(ctx)
    if user == nil {
        SendError(ctx, fasthttp.StatusUnauthorized, "Unauthorized")
        return
    }
    SendJSON(ctx, user)
}

// getUser handles GET /api/users/:id — returns a single user.
func (h *UserHandler) getUser(ctx *fasthttp.RequestCtx) {
    id := ctx.UserValue("id").(string)
    user, err := h.configStore.GetUserByID(ctx, id)
    if err != nil {
        SendError(ctx, fasthttp.StatusInternalServerError, fmt.Sprintf("Failed to get user: %v", err))
        return
    }
    if user == nil {
        SendError(ctx, fasthttp.StatusNotFound, "user not found")
        return
    }
    SendJSON(ctx, user)
}

// updateUser handles PUT /api/users/:id — updates name and/or email.
func (h *UserHandler) updateUser(ctx *fasthttp.RequestCtx) {
    id := ctx.UserValue("id").(string)
    payload := struct {
        Name  *string `json:"name"`
        Email *string `json:"email"`
    }{}
    if err := json.Unmarshal(ctx.PostBody(), &payload); err != nil {
        SendError(ctx, fasthttp.StatusBadRequest, fmt.Sprintf("Invalid request format: %v", err))
        return
    }
    user, err := h.configStore.GetUserByID(ctx, id)
    if err != nil || user == nil {
        SendError(ctx, fasthttp.StatusNotFound, "user not found")
        return
    }
    if payload.Name != nil && *payload.Name != "" {
        user.Name = *payload.Name
    }
    if payload.Email != nil {
        email := normalizeEmail(*payload.Email)
        if email == "" {
            SendError(ctx, fasthttp.StatusBadRequest, "invalid email address")
            return
        }
        existing, err := h.configStore.GetUserByEmail(ctx, email)
        if err != nil {
            SendError(ctx, fasthttp.StatusInternalServerError, "Failed to check email")
            return
        }
        if existing != nil && existing.ID != id {
            SendError(ctx, fasthttp.StatusConflict, "email already in use")
            return
        }
        user.Email = email
    }
    user.UpdatedAt = time.Now()
    if err := h.configStore.UpdateUser(ctx, user); err != nil {
        SendError(ctx, fasthttp.StatusInternalServerError, fmt.Sprintf("Failed to update user: %v", err))
        return
    }
    SendJSON(ctx, user)
}

// updateRole handles PUT /api/users/:id/role
func (h *UserHandler) updateRole(ctx *fasthttp.RequestCtx) {
    id := ctx.UserValue("id").(string)
    caller := currentUser(ctx)

    payload := struct {
        Role string `json:"role"`
    }{}
    if err := json.Unmarshal(ctx.PostBody(), &payload); err != nil {
        SendError(ctx, fasthttp.StatusBadRequest, fmt.Sprintf("Invalid request format: %v", err))
        return
    }
    if payload.Role != "admin" && payload.Role != "operator" && payload.Role != "viewer" {
        SendError(ctx, fasthttp.StatusBadRequest, "role must be one of: admin, operator, viewer")
        return
    }
    if caller != nil && caller.ID == id {
        SendError(ctx, fasthttp.StatusBadRequest, "cannot change your own role")
        return
    }

    if err := h.configStore.UpdateUserRole(ctx, id, payload.Role); err != nil {
        if err == configstore.ErrLastAdmin {
            SendError(ctx, fasthttp.StatusBadRequest, "cannot remove the last admin")
            return
        }
        SendError(ctx, fasthttp.StatusInternalServerError, fmt.Sprintf("Failed to update role: %v", err))
        return
    }
    SendJSON(ctx, map[string]any{"message": "role updated"})
}

// updatePassword handles PUT /api/users/:id/password — two modes:
// admin reset (new_password only) and self-change (current_password + new_password).
func (h *UserHandler) updatePassword(ctx *fasthttp.RequestCtx) {
    id := ctx.UserValue("id").(string)
    caller := currentUser(ctx)

    payload := struct {
        CurrentPassword *string `json:"current_password"`
        NewPassword     string  `json:"new_password"`
    }{}
    if err := json.Unmarshal(ctx.PostBody(), &payload); err != nil {
        SendError(ctx, fasthttp.StatusBadRequest, fmt.Sprintf("Invalid request format: %v", err))
        return
    }
    if len(payload.NewPassword) < 8 {
        SendError(ctx, fasthttp.StatusBadRequest, "password must be at least 8 characters")
        return
    }

    isSelf := caller != nil && caller.ID == id

    if isSelf {
        // Self-change: verify current password
        if payload.CurrentPassword == nil || *payload.CurrentPassword == "" {
            SendError(ctx, fasthttp.StatusBadRequest, "current_password is required when changing your own password")
            return
        }
        user, err := h.configStore.GetUserByID(ctx, id)
        if err != nil || user == nil {
            SendError(ctx, fasthttp.StatusNotFound, "user not found")
            return
        }
        if user.PasswordHash == nil {
            SendError(ctx, fasthttp.StatusBadRequest, "user has no password set")
            return
        }
        match, err := encrypt.CompareHash(*user.PasswordHash, *payload.CurrentPassword)
        if err != nil || !match {
            SendError(ctx, fasthttp.StatusUnauthorized, "current password is incorrect")
            return
        }
    }

    hash, err := encrypt.Hash(payload.NewPassword)
    if err != nil {
        SendError(ctx, fasthttp.StatusInternalServerError, "Failed to hash password")
        return
    }
    if err := h.configStore.UpdateUserPassword(ctx, id, hash); err != nil {
        SendError(ctx, fasthttp.StatusInternalServerError, fmt.Sprintf("Failed to update password: %v", err))
        return
    }

    // Invalidate sessions per spec
    sessionToken, _ := ctx.UserValue(schemas.BifrostContextKeySessionToken).(string)
    if isSelf && sessionToken != "" {
        // Keep current session, delete all others
        _ = h.configStore.DeleteSessionsForUser(ctx, id, &sessionToken)
    } else {
        // Admin reset: delete ALL sessions for that user
        _ = h.configStore.DeleteSessionsForUser(ctx, id, nil)
    }

    SendJSON(ctx, map[string]any{"message": "password updated"})
}

// updateActive handles PUT /api/users/:id/active
func (h *UserHandler) updateActive(ctx *fasthttp.RequestCtx) {
    id := ctx.UserValue("id").(string)
    caller := currentUser(ctx)

    payload := struct {
        Active bool `json:"active"`
    }{}
    if err := json.Unmarshal(ctx.PostBody(), &payload); err != nil {
        SendError(ctx, fasthttp.StatusBadRequest, fmt.Sprintf("Invalid request format: %v", err))
        return
    }
    if caller != nil && caller.ID == id && !payload.Active {
        SendError(ctx, fasthttp.StatusBadRequest, "cannot deactivate yourself")
        return
    }

    if err := h.configStore.UpdateUserActive(ctx, id, payload.Active); err != nil {
        if err == configstore.ErrLastActiveAdmin {
            SendError(ctx, fasthttp.StatusBadRequest, "cannot deactivate the last admin")
            return
        }
        SendError(ctx, fasthttp.StatusInternalServerError, fmt.Sprintf("Failed to update user: %v", err))
        return
    }
    SendJSON(ctx, map[string]any{"message": "user updated"})
}
```

- [ ] **Step 2: Register `UserHandler` in `server.go`**

In `transports/bifrost-http/server/server.go`, near line 1748 (after `sessionHandler` creation), add:

```go
userHandler := handlers.NewUserHandler(s.Config.ConfigStore)
```

Then register it (after the `sessionHandler` block, around line 1760):

```go
if userHandler != nil {
    userHandler.RegisterRoutes(s.Router, middlewares...)
}
```

- [ ] **Step 3: Build check**

```
cd transports/bifrost-http && go build ./...
```
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add transports/bifrost-http/handlers/users.go transports/bifrost-http/server/server.go
git commit -m "feat(users): add user management handler and register routes"
```

---

## Task 9: Update UI — email login field

**Files:**
- Modify: `ui/app/_fallbacks/enterprise/components/login/loginView.tsx`
- Modify: `ui/lib/store/apis/sessionApi.ts`

- [ ] **Step 1: Update `LoginRequest` in `sessionApi.ts`**

In `ui/lib/store/apis/sessionApi.ts`, change:

```ts
export interface LoginRequest {
    email: string;
    password: string;
}
```

(Remove `username: string`, add `email: string`.)

- [ ] **Step 2: Update `loginView.tsx`**

In `ui/app/_fallbacks/enterprise/components/login/loginView.tsx`:

1. Rename state: `const [email, setEmail] = useState("");` (was `username`)
2. Update the form submit call: `await login({ email, password }).unwrap();`
3. Update the input field:

```tsx
<div className="space-y-2">
    <Label htmlFor="email" className="text-sm font-medium">
        Email
    </Label>
    <Input
        id="email"
        type="email"
        placeholder="Enter your email"
        value={email}
        onChange={(e) => setEmail(e.target.value)}
        required
        className="text-sm"
        autoComplete="email"
    />
</div>
```

The full diff from the original:
- `const [username, setUsername] = useState("")` → `const [email, setEmail] = useState("")`
- `await login({ username, password }).unwrap()` → `await login({ email, password }).unwrap()`
- `<Label htmlFor="username">Username</Label>` → `<Label htmlFor="email">Email</Label>`
- `<Input id="username" type="text" placeholder="Enter your username" value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="username" />` → `<Input id="email" type="email" placeholder="Enter your email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="email" />`

- [ ] **Step 3: Build check (UI)**

```
cd ui && npm run build
```
Expected: no TypeScript errors. Fix any TS type errors if they appear (the `LoginRequest` type change propagates automatically).

- [ ] **Step 4: Commit**

```bash
git add ui/app/_fallbacks/enterprise/components/login/loginView.tsx ui/lib/store/apis/sessionApi.ts
git commit -m "feat(users): change login form field from username to email"
```

---

## Task 10: End-to-end smoke test

- [ ] **Step 1: Start the server locally**

```
cd transports && go run . --config ../deploy/test/config/config.docker.json
```
(Or whatever the local dev start command is.)

- [ ] **Step 2: Test migration ran**

Check the server logs for migration output. Expected: no migration errors.

- [ ] **Step 3: Test login with email**

```bash
curl -c cookies.txt -X POST http://localhost:8080/api/session/login \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@localhost","password":"<your-admin-password>"}'
```
Expected: `{"message":"Login successful","user":{"id":"...","email":"admin@localhost","name":"admin","role":"admin"}}`

- [ ] **Step 4: Test GET /api/users/me**

```bash
curl -b cookies.txt http://localhost:8080/api/users/me
```
Expected: JSON with current user object.

- [ ] **Step 5: Test GET /api/auth/settings**

```bash
curl -b cookies.txt http://localhost:8080/api/auth/settings
```
Expected: `{"session_expiry_hours":720,"is_auth_enabled":true}`

- [ ] **Step 6: Test POST /api/users (create user)**

```bash
curl -b cookies.txt -X POST http://localhost:8080/api/users \
  -H "Content-Type: application/json" \
  -d '{"email":"alice@test.com","name":"Alice","role":"operator","password":"mypassword123"}'
```
Expected: 201 with new user object (no `password_hash` field in response).

- [ ] **Step 7: Commit final notes**

```bash
git commit --allow-empty -m "chore: phase1-spec1 multi-user system implementation complete"
```

---

## Self-Review Checklist

The following spec requirements are covered by tasks above:

| Spec Requirement | Task |
|---|---|
| `users` table with all fields | Task 1 |
| `sessions.user_id` nullable *string | Task 2 |
| Migration Branch A (backfill admin) | Task 4 |
| Migration Branch B (empty table) | Task 4 |
| Postgres NOT NULL enforcement | Task 4 |
| `session_expiry_hours` config key | Task 2, 3, 5, 7 |
| 6-step authenticated request flow | Task 6 |
| `validateSession` returns `TableUser` | Task 6 |
| User attached to request context | Task 6 |
| 4-step email login | Task 7 |
| Session expiry from config (not hardcoded) | Task 7 |
| `GET/PUT /api/auth/settings` | Task 7 |
| All 8 `/api/users` endpoints | Task 8 |
| Last-admin guard with transaction lock | Task 5 |
| Cannot change own role guard | Task 8 |
| Cannot deactivate self guard | Task 8 |
| Admin resets password → delete all sessions | Task 8 |
| Self password change → keep current session | Task 8 |
| Password ≥ 8 chars validation | Task 8 |
| Email normalization (lowercase, trim) | Task 5, 8 |
| `password_hash` never in JSON | Task 1 (`json:"-"`) |
| UI login field username → email | Task 9 |
| `UserHandler` registered in server | Task 8 |
