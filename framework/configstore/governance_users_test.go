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
		&tables.TableTeam{},
		&tables.TableUser{},
		&tables.TableBudget{},
		&tables.TableRateLimit{},
	)
	require.NoError(t, err)

	return configstore.NewRDBConfigStoreForTest(db)
}

func TestCreateAndGetUser(t *testing.T) {
	store := setupUsersTestDB(t)
	ctx := context.Background()

	now := time.Now().UTC()
	user := &tables.TableUser{
		ID:         "user-1",
		Email:      "alice@example.com",
		Name:       "Alice",
		AuthMethod: "password",
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	require.NoError(t, store.CreateUser(ctx, user))

	got, err := store.GetUserByEmail(ctx, "alice@example.com")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, user.ID, got.ID)
	assert.Equal(t, "Alice", got.Name)
	assert.Equal(t, "password", got.AuthMethod)
}

func TestUpsertUserByEmail_CreateOnFirstLogin(t *testing.T) {
	store := setupUsersTestDB(t)
	ctx := context.Background()

	user, err := store.UpsertUserByEmail(ctx, "bob@example.com", "Bob", "oidc")
	require.NoError(t, err)
	require.NotNil(t, user)
	assert.NotEmpty(t, user.ID)
	assert.Equal(t, "bob@example.com", user.Email)
	assert.Equal(t, "Bob", user.Name)
	assert.Equal(t, "oidc", user.AuthMethod)
}

func TestUpsertUserByEmail_UpdateNameOnSubsequentLogin(t *testing.T) {
	store := setupUsersTestDB(t)
	ctx := context.Background()

	first, err := store.UpsertUserByEmail(ctx, "carol@example.com", "Carol", "oidc")
	require.NoError(t, err)

	second, err := store.UpsertUserByEmail(ctx, "carol@example.com", "Carol Updated", "oidc")
	require.NoError(t, err)
	require.NotNil(t, first)
	require.NotNil(t, second)
	assert.Equal(t, first.ID, second.ID)
	assert.Equal(t, "Carol Updated", second.Name)
}

func TestListUsers_Search(t *testing.T) {
	store := setupUsersTestDB(t)
	ctx := context.Background()

	for _, email := range []string{"alice@x.com", "bob@x.com", "charlie@x.com"} {
		_, err := store.UpsertUserByEmail(ctx, email, email, "password")
		require.NoError(t, err)
	}

	users, total, err := store.ListUsers(ctx, "alice", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, users, 1)
	assert.Equal(t, "alice@x.com", users[0].Email)
}

func TestDeleteUser_CascadesBudgetAndRateLimit(t *testing.T) {
	store := setupUsersTestDB(t)
	ctx := context.Background()

	budgetID := "budget-1"
	rateLimitID := "rate-limit-1"
	now := time.Now().UTC()

	require.NoError(t, store.DB().Create(&tables.TableBudget{
		ID:            budgetID,
		MaxLimit:      100,
		ResetDuration: "1d",
		LastReset:     now,
		CurrentUsage:  0,
		CreatedAt:     now,
		UpdatedAt:     now,
	}).Error)
	require.NoError(t, store.DB().Create(&tables.TableRateLimit{
		ID:                  rateLimitID,
		TokenCurrentUsage:   0,
		RequestCurrentUsage: 0,
		CreatedAt:           now,
		UpdatedAt:           now,
	}).Error)

	user, err := store.UpsertUserByEmail(ctx, "dave@x.com", "Dave", "password")
	require.NoError(t, err)

	updated, err := store.UpdateUser(ctx, user.ID, map[string]any{
		"budget_id":     budgetID,
		"rate_limit_id": rateLimitID,
	})
	require.NoError(t, err)
	require.NotNil(t, updated)
	assert.Equal(t, budgetID, *updated.BudgetID)
	assert.Equal(t, rateLimitID, *updated.RateLimitID)

	require.NoError(t, store.DeleteUser(ctx, user.ID))

	_, err = store.GetUser(ctx, user.ID)
	assert.ErrorIs(t, err, configstore.ErrNotFound)

	var budget tables.TableBudget
	err = store.DB().First(&budget, "id = ?", budgetID).Error
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound, "owned budget should be deleted")

	var rateLimit tables.TableRateLimit
	err = store.DB().First(&rateLimit, "id = ?", rateLimitID).Error
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound, "owned rate limit should be deleted")
}
