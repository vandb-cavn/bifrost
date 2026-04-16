package configstore

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupGuardrailStore(t *testing.T) ConfigStore {
	t.Helper()
	return setupRDBTestStore(t)
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
