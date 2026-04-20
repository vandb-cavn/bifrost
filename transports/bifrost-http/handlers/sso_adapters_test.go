package handlers

import (
	"testing"

	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestCodeChallenge_S256(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	expected := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"

	assert.Equal(t, expected, codeChallenge(verifier))
}
