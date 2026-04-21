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
		&tables.TableGovernanceSSOConfig{},
		&tables.TableGovernanceSSONonce{},
	)
	require.NoError(t, err)

	return configstore.NewRDBConfigStoreForTest(db)
}

func TestCreateAndListSSOConfigs(t *testing.T) {
	store := setupSSOTestDB(t)
	ctx := context.Background()

	now := time.Now().UTC()
	cfg := &tables.TableGovernanceSSOConfig{
		ID:            "sso-1",
		Provider:      "okta",
		IssuerURL:     "https://dev-okta.example.com",
		ClientID:      "client-id",
		ClientSecret:  "client-secret",
		RoleClaimKey:  "role",
		GroupClaimKey: "groups",
		Enabled:       false,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	require.NoError(t, store.CreateSSOConfig(ctx, cfg))

	configs, err := store.ListSSOConfigs(ctx)
	require.NoError(t, err)
	require.Len(t, configs, 1)
	assert.Equal(t, "sso-1", configs[0].ID)
	assert.Equal(t, "client-secret", configs[0].ClientSecret)
}

func TestEnableSSOConfig_DisablesOthers(t *testing.T) {
	store := setupSSOTestDB(t)
	ctx := context.Background()

	for _, item := range []struct {
		id      string
		enabled bool
		created time.Time
	}{
		{id: "sso-1", enabled: true, created: time.Now().Add(-2 * time.Minute).UTC()},
		{id: "sso-2", enabled: true, created: time.Now().UTC()},
	} {
		require.NoError(t, store.CreateSSOConfig(ctx, &tables.TableGovernanceSSOConfig{
			ID:           item.id,
			Provider:     "okta",
			IssuerURL:    "https://dev-okta.example.com",
			ClientID:     "client-id",
			ClientSecret: "client-secret",
			Enabled:      item.enabled,
			CreatedAt:    item.created,
			UpdatedAt:    item.created,
		}))
	}

	require.NoError(t, store.EnableSSOConfig(ctx, "sso-2"))

	configs, err := store.ListSSOConfigs(ctx)
	require.NoError(t, err)
	enabledCount := 0
	for _, cfg := range configs {
		if cfg.Enabled {
			enabledCount++
			assert.Equal(t, "sso-2", cfg.ID)
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

	expiresAt := time.Now().Add(10 * time.Minute).UTC()
	require.NoError(t, store.CreateSSONonce(ctx, "state-abc", "verifier-xyz", "nonce-123", "okta", expiresAt))

	nonce, err := store.ConsumeAndDeleteSSONonce(ctx, "state-abc")
	require.NoError(t, err)
	require.NotNil(t, nonce)
	assert.Equal(t, "verifier-xyz", nonce.CodeVerifier)
	assert.Equal(t, "nonce-123", nonce.Nonce)

	_, err = store.ConsumeAndDeleteSSONonce(ctx, "state-abc")
	assert.ErrorIs(t, err, configstore.ErrNotFound)
}

func TestDeleteExpiredSSONonces(t *testing.T) {
	store := setupSSOTestDB(t)
	ctx := context.Background()

	require.NoError(t, store.CreateSSONonce(ctx, "expired-state", "verifier-1", "nonce-1", "okta", time.Now().Add(-10*time.Minute).UTC()))
	require.NoError(t, store.CreateSSONonce(ctx, "active-state", "verifier-2", "nonce-2", "okta", time.Now().Add(10*time.Minute).UTC()))

	require.NoError(t, store.DeleteExpiredSSONonces(ctx))

	_, err := store.ConsumeAndDeleteSSONonce(ctx, "expired-state")
	assert.ErrorIs(t, err, configstore.ErrNotFound)

	nonce, err := store.ConsumeAndDeleteSSONonce(ctx, "active-state")
	require.NoError(t, err)
	assert.Equal(t, "nonce-2", nonce.Nonce)
}
