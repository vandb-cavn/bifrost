package handlers

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

func TestOktaAdapter_ExtractUserInfo(t *testing.T) {
	adapter := OktaAdapter{}
	cfg := &tables.TableGovernanceSSOConfig{}
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
	cfg := &tables.TableGovernanceSSOConfig{GroupClaimKey: "custom_groups"}
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
	cfg := &tables.TableGovernanceSSOConfig{}
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
	cfg := &tables.TableGovernanceSSOConfig{}
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
	cfg := &tables.TableGovernanceSSOConfig{}
	claims := map[string]any{"name": "Nobody"}

	_, _, _, err := adapter.ExtractUserInfo(claims, cfg)
	assert.Error(t, err)
}

func TestGoogleAdapter_ExtractUserInfo(t *testing.T) {
	adapter := GoogleAdapter{}
	cfg := &tables.TableGovernanceSSOConfig{}
	claims := map[string]any{
		"email":  "alice@gmail.com",
		"name":   "Alice",
		"groups": []any{"admins", "developers"},
	}

	email, name, groups, err := adapter.ExtractUserInfo(claims, cfg)
	require.NoError(t, err)
	assert.Equal(t, "alice@gmail.com", email)
	assert.Equal(t, "Alice", name)
	assert.Contains(t, groups, "admins")
}

func TestGoogleAdapter_MissingEmail_ReturnsError(t *testing.T) {
	adapter := GoogleAdapter{}
	cfg := &tables.TableGovernanceSSOConfig{}
	claims := map[string]any{"name": "Nobody"}

	_, _, _, err := adapter.ExtractUserInfo(claims, cfg)
	assert.Error(t, err)
}

func TestKeycloakAdapter_ExtractUserInfo(t *testing.T) {
	adapter := KeycloakAdapter{}
	cfg := &tables.TableGovernanceSSOConfig{}
	claims := map[string]any{
		"email":  "bob@corp.com",
		"name":   "Bob",
		"groups": []any{"/admins", "/developers"},
	}

	email, name, groups, err := adapter.ExtractUserInfo(claims, cfg)
	require.NoError(t, err)
	assert.Equal(t, "bob@corp.com", email)
	assert.Equal(t, "Bob", name)
	assert.Contains(t, groups, "/admins")
}

func TestKeycloakAdapter_CustomGroupClaimKey(t *testing.T) {
	adapter := KeycloakAdapter{}
	cfg := &tables.TableGovernanceSSOConfig{GroupClaimKey: "roles"}
	claims := map[string]any{
		"email": "carol@corp.com",
		"name":  "Carol",
		"roles": []any{"admin"},
	}

	_, _, groups, err := adapter.ExtractUserInfo(claims, cfg)
	require.NoError(t, err)
	assert.Equal(t, []string{"admin"}, groups)
}

func TestKeycloakAdapter_MissingEmail_ReturnsError(t *testing.T) {
	adapter := KeycloakAdapter{}
	cfg := &tables.TableGovernanceSSOConfig{}
	claims := map[string]any{"name": "Nobody"}

	_, _, _, err := adapter.ExtractUserInfo(claims, cfg)
	assert.Error(t, err)
}

func TestGenericOIDCAdapter_ExtractUserInfo(t *testing.T) {
	adapter := GenericOIDCAdapter{}
	cfg := &tables.TableGovernanceSSOConfig{}
	claims := map[string]any{
		"email": "dave@example.com",
		"name":  "Dave",
	}

	email, name, groups, err := adapter.ExtractUserInfo(claims, cfg)
	require.NoError(t, err)
	assert.Equal(t, "dave@example.com", email)
	assert.Equal(t, "Dave", name)
	assert.Empty(t, groups)
}

func TestGenericOIDCAdapter_CustomGroupClaim(t *testing.T) {
	adapter := GenericOIDCAdapter{}
	cfg := &tables.TableGovernanceSSOConfig{GroupClaimKey: "team_memberships"}
	claims := map[string]any{
		"email":            "eve@example.com",
		"name":             "Eve",
		"team_memberships": []any{"eng", "infra"},
	}

	_, _, groups, err := adapter.ExtractUserInfo(claims, cfg)
	require.NoError(t, err)
	assert.Equal(t, []string{"eng", "infra"}, groups)
}

func TestGenericOIDCAdapter_MissingEmail_ReturnsError(t *testing.T) {
	adapter := GenericOIDCAdapter{}
	cfg := &tables.TableGovernanceSSOConfig{}
	claims := map[string]any{"sub": "user-123"}

	_, _, _, err := adapter.ExtractUserInfo(claims, cfg)
	assert.Error(t, err)
}

func TestCodeChallenge_S256(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	expected := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"

	assert.Equal(t, expected, codeChallenge(verifier))
}

type mockConfigStoreForSSO struct {
	configstore.ConfigStore

	createdConfig  *tables.TableGovernanceSSOConfig
	existingConfig *tables.TableGovernanceSSOConfig
	lastUpdateID   string
	lastUpdates    map[string]any
}

func (m *mockConfigStoreForSSO) CreateSSOConfig(_ context.Context, cfg *tables.TableGovernanceSSOConfig) error {
	if cfg != nil {
		copyCfg := *cfg
		m.createdConfig = &copyCfg
	} else {
		m.createdConfig = nil
	}
	return nil
}

func (m *mockConfigStoreForSSO) GetSSOConfig(_ context.Context, id string) (*tables.TableGovernanceSSOConfig, error) {
	if m.existingConfig != nil && m.existingConfig.ID == id {
		return m.existingConfig, nil
	}
	return nil, configstore.ErrNotFound
}

func (m *mockConfigStoreForSSO) UpdateSSOConfig(_ context.Context, id string, updates map[string]any) (*tables.TableGovernanceSSOConfig, error) {
	m.lastUpdateID = id
	m.lastUpdates = make(map[string]any, len(updates))
	for k, v := range updates {
		m.lastUpdates[k] = v
	}
	if m.existingConfig != nil && m.existingConfig.ID == id {
		if provider, ok := updates["provider"].(string); ok {
			m.existingConfig.Provider = provider
		}
		if issuerURL, ok := updates["issuer_url"].(string); ok {
			m.existingConfig.IssuerURL = issuerURL
		}
		if clientID, ok := updates["client_id"].(string); ok {
			m.existingConfig.ClientID = clientID
		}
		return m.existingConfig, nil
	}
	return nil, configstore.ErrNotFound
}

func stubValidateIssuerURL(t *testing.T) {
	t.Helper()
	original := validateIssuerURL
	validateIssuerURL = func(string) error { return nil }
	t.Cleanup(func() {
		validateIssuerURL = original
	})
}

func TestCreateConfig_AllowsNewProviders(t *testing.T) {
	SetLogger(&mockLogger{})
	stubValidateIssuerURL(t)

	store := &mockConfigStoreForSSO{}
	h := &SSOHandler{configStore: store}

	for _, provider := range []string{"google", "keycloak", "oidc"} {
		t.Run(provider, func(t *testing.T) {
			store.createdConfig = nil

			body, err := json.Marshal(map[string]any{
				"provider":      provider,
				"issuer_url":    "https://issuer.example.com",
				"client_id":     "cid",
				"client_secret": "secret",
			})
			require.NoError(t, err)

			ctx := &fasthttp.RequestCtx{}
			ctx.Request.Header.SetMethod("POST")
			ctx.Request.SetRequestURI("/api/governance/sso/configs")
			ctx.Request.SetBody(body)

			h.createConfig(ctx)

			require.Equal(t, fasthttp.StatusCreated, ctx.Response.StatusCode())
			require.NotNil(t, store.createdConfig)
			assert.Equal(t, provider, store.createdConfig.Provider)
			assert.Equal(t, "cid", store.createdConfig.ClientID)
			assert.Equal(t, "secret", store.createdConfig.ClientSecret)

			var resp map[string]any
			require.NoError(t, json.Unmarshal(ctx.Response.Body(), &resp))
			cfg, ok := resp["config"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, provider, cfg["provider"])
		})
	}
}

func TestUpdateConfig_AllowsNewProviders(t *testing.T) {
	SetLogger(&mockLogger{})
	stubValidateIssuerURL(t)

	store := &mockConfigStoreForSSO{
		existingConfig: &tables.TableGovernanceSSOConfig{
			ID:           "cfg-1",
			Provider:     "okta",
			IssuerURL:    "https://issuer.example.com",
			ClientID:     "cid",
			CreatedAt:    time.Now().UTC(),
			UpdatedAt:    time.Now().UTC(),
			Enabled:      false,
			ClientSecret: "secret",
		},
	}
	h := &SSOHandler{configStore: store}

	for _, provider := range []string{"google", "keycloak", "oidc"} {
		t.Run(provider, func(t *testing.T) {
			store.lastUpdates = nil

			body, err := json.Marshal(map[string]any{
				"provider":      provider,
				"issuer_url":    "https://issuer.example.com",
				"client_id":     "cid",
				"client_secret": "secret",
			})
			require.NoError(t, err)

			ctx := &fasthttp.RequestCtx{}
			ctx.Request.Header.SetMethod("PUT")
			ctx.Request.SetRequestURI("/api/governance/sso/configs/cfg-1")
			ctx.Request.SetBody(body)
			ctx.SetUserValue("id", "cfg-1")

			h.updateConfig(ctx)

			require.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode())
			require.NotNil(t, store.lastUpdates)
			assert.Equal(t, provider, store.lastUpdates["provider"])

			var resp map[string]any
			require.NoError(t, json.Unmarshal(ctx.Response.Body(), &resp))
			cfg, ok := resp["config"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, provider, cfg["provider"])
		})
	}
}

func TestCreateConfig_UnknownProvider_RejectsAtRegistry(t *testing.T) {
	SetLogger(&mockLogger{})
	stubValidateIssuerURL(t)

	h := &SSOHandler{configStore: &mockConfigStoreForSSO{}}

	body, err := json.Marshal(map[string]any{
		"provider":      "unknown_idp",
		"issuer_url":    "https://issuer.example.com",
		"client_id":     "cid",
		"client_secret": "secret",
	})
	require.NoError(t, err)

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("POST")
	ctx.Request.SetRequestURI("/api/governance/sso/configs")
	ctx.Request.SetBody(body)

	h.createConfig(ctx)

	assert.Equal(t, fasthttp.StatusBadRequest, ctx.Response.StatusCode())
	assert.Contains(t, string(ctx.Response.Body()), "unsupported provider")
}
