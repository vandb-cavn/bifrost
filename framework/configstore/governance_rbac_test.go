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

func setupRBACTestDB(t *testing.T) *configstore.RDBConfigStore {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	err = db.AutoMigrate(
		&tables.TableUser{},
		&tables.TableTeam{},
		&tables.TableBudget{},
		&tables.TableRateLimit{},
		&tables.TableRole{},
		&tables.TablePermission{},
		&tables.TableRolePermission{},
		&tables.TableUserRole{},
	)
	require.NoError(t, err)
	return configstore.NewRDBConfigStoreForTest(db)
}

func TestCreateAndListRoles(t *testing.T) {
	store := setupRBACTestDB(t)
	ctx := context.Background()

	now := time.Now().UTC()
	role := &tables.TableRole{
		ID:        "role-1",
		Name:      "Custom Role",
		IsSystem:  false,
		CreatedAt: now,
		UpdatedAt: now,
	}
	require.NoError(t, store.CreateRole(ctx, role))

	roles, err := store.ListRoles(ctx)
	require.NoError(t, err)
	assert.Len(t, roles, 1)
	assert.Equal(t, "Custom Role", roles[0].Name)
}

func TestGetRole_NotFound(t *testing.T) {
	store := setupRBACTestDB(t)
	ctx := context.Background()

	_, err := store.GetRole(ctx, "nonexistent")
	assert.ErrorIs(t, err, configstore.ErrNotFound)
}

func TestSetAndGetRolePermissions(t *testing.T) {
	store := setupRBACTestDB(t)
	ctx := context.Background()

	now := time.Now().UTC()
	require.NoError(t, store.CreateRole(ctx, &tables.TableRole{ID: "r1", Name: "R1", CreatedAt: now, UpdatedAt: now}))

	// Seed two permissions directly
	db := store.TestDB()
	require.NoError(t, db.Create(&tables.TablePermission{ID: "vk_view", Resource: "VirtualKeys", Operation: "View"}).Error)
	require.NoError(t, db.Create(&tables.TablePermission{ID: "vk_create", Resource: "VirtualKeys", Operation: "Create"}).Error)

	require.NoError(t, store.SetRolePermissions(ctx, "r1", []string{"vk_view", "vk_create"}))

	perms, err := store.GetRolePermissions(ctx, "r1")
	require.NoError(t, err)
	assert.Len(t, perms, 2)
}

func TestSetRolePermissions_Replaces(t *testing.T) {
	store := setupRBACTestDB(t)
	ctx := context.Background()

	now := time.Now().UTC()
	require.NoError(t, store.CreateRole(ctx, &tables.TableRole{ID: "r1", Name: "R1", CreatedAt: now, UpdatedAt: now}))

	db := store.TestDB()
	require.NoError(t, db.Create(&tables.TablePermission{ID: "vk_view", Resource: "VirtualKeys", Operation: "View"}).Error)
	require.NoError(t, db.Create(&tables.TablePermission{ID: "vk_create", Resource: "VirtualKeys", Operation: "Create"}).Error)

	require.NoError(t, store.SetRolePermissions(ctx, "r1", []string{"vk_view", "vk_create"}))
	// Replace with only one
	require.NoError(t, store.SetRolePermissions(ctx, "r1", []string{"vk_view"}))

	perms, err := store.GetRolePermissions(ctx, "r1")
	require.NoError(t, err)
	assert.Len(t, perms, 1)
	assert.Equal(t, "View", perms[0].Operation)
}

func TestAssignAndGetUserRoles(t *testing.T) {
	store := setupRBACTestDB(t)
	ctx := context.Background()

	now := time.Now().UTC()
	require.NoError(t, store.CreateRole(ctx, &tables.TableRole{ID: "r1", Name: "Admin", CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, store.CreateUser(ctx, &tables.TableUser{ID: "u1", Email: "a@b.com", AuthMethod: "oidc", CreatedAt: now, UpdatedAt: now}))

	require.NoError(t, store.AssignUserRole(ctx, "u1", "r1"))

	roles, err := store.GetUserRoles(ctx, "u1")
	require.NoError(t, err)
	assert.Len(t, roles, 1)
	assert.Equal(t, "Admin", roles[0].Name)
}

func TestRemoveUserRole(t *testing.T) {
	store := setupRBACTestDB(t)
	ctx := context.Background()

	now := time.Now().UTC()
	require.NoError(t, store.CreateRole(ctx, &tables.TableRole{ID: "r1", Name: "Admin", CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, store.CreateUser(ctx, &tables.TableUser{ID: "u1", Email: "a@b.com", AuthMethod: "oidc", CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, store.AssignUserRole(ctx, "u1", "r1"))
	require.NoError(t, store.RemoveUserRole(ctx, "u1", "r1"))

	roles, err := store.GetUserRoles(ctx, "u1")
	require.NoError(t, err)
	assert.Empty(t, roles)
}

func TestGetUserPermissions_MergesAcrossRoles(t *testing.T) {
	store := setupRBACTestDB(t)
	ctx := context.Background()

	now := time.Now().UTC()
	require.NoError(t, store.CreateRole(ctx, &tables.TableRole{ID: "r1", Name: "R1", CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, store.CreateRole(ctx, &tables.TableRole{ID: "r2", Name: "R2", CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, store.CreateUser(ctx, &tables.TableUser{ID: "u1", Email: "a@b.com", AuthMethod: "oidc", CreatedAt: now, UpdatedAt: now}))

	db := store.TestDB()
	require.NoError(t, db.Create(&tables.TablePermission{ID: "vk_view", Resource: "VirtualKeys", Operation: "View"}).Error)
	require.NoError(t, db.Create(&tables.TablePermission{ID: "logs_view", Resource: "Logs", Operation: "View"}).Error)

	require.NoError(t, store.SetRolePermissions(ctx, "r1", []string{"vk_view"}))
	require.NoError(t, store.SetRolePermissions(ctx, "r2", []string{"logs_view"}))
	require.NoError(t, store.AssignUserRole(ctx, "u1", "r1"))
	require.NoError(t, store.AssignUserRole(ctx, "u1", "r2"))

	perms, err := store.GetUserPermissions(ctx, "u1")
	require.NoError(t, err)
	assert.Len(t, perms, 2)
}

func TestDeleteRole_SystemRoleBlocked(t *testing.T) {
	store := setupRBACTestDB(t)
	ctx := context.Background()

	now := time.Now().UTC()
	require.NoError(t, store.CreateRole(ctx, &tables.TableRole{ID: "sys-1", Name: "Admin", IsSystem: true, CreatedAt: now, UpdatedAt: now}))

	err := store.DeleteRole(ctx, "sys-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "system role")
}

func TestUpdateRole_CannotChangeIsSystem(t *testing.T) {
	store := setupRBACTestDB(t)
	ctx := context.Background()

	now := time.Now().UTC()
	require.NoError(t, store.CreateRole(ctx, &tables.TableRole{ID: "r1", Name: "Admin", IsSystem: true, CreatedAt: now, UpdatedAt: now}))

	// Try to update is_system to false
	updated, err := store.UpdateRole(ctx, "r1", map[string]any{"is_system": false, "name": "New Name"})
	require.NoError(t, err)
	assert.True(t, updated.IsSystem, "is_system should not be changeable via UpdateRole")
	assert.Equal(t, "New Name", updated.Name)
}

func TestAssignUserRole_Idempotent(t *testing.T) {
	store := setupRBACTestDB(t)
	ctx := context.Background()

	now := time.Now().UTC()
	require.NoError(t, store.CreateRole(ctx, &tables.TableRole{ID: "r1", Name: "Admin", CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, store.CreateUser(ctx, &tables.TableUser{ID: "u1", Email: "a@b.com", AuthMethod: "oidc", CreatedAt: now, UpdatedAt: now}))

	// Assign twice
	require.NoError(t, store.AssignUserRole(ctx, "u1", "r1"))
	require.NoError(t, store.AssignUserRole(ctx, "u1", "r1"))

	roles, err := store.GetUserRoles(ctx, "u1")
	require.NoError(t, err)
	assert.Len(t, roles, 1)
}
