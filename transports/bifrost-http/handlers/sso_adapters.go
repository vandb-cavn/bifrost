// Package handlers provides HTTP request handlers for the Bifrost HTTP transport.
package handlers

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/framework/configstore/tables"
)

// OIDCProvider extracts user identity from raw OIDC claims.
type OIDCProvider interface {
	Name() string
	ExtractUserInfo(claims map[string]any, cfg *tables.TableGovernanceSSOConfig) (email, name string, groups []string, err error)
}

var providerRegistry = map[string]OIDCProvider{
	"okta":  OktaAdapter{},
	"entra": EntraAdapter{},
}

// OktaAdapter handles Okta-specific claim extraction.
type OktaAdapter struct{}

func (OktaAdapter) Name() string { return "okta" }

func (OktaAdapter) ExtractUserInfo(claims map[string]any, cfg *tables.TableGovernanceSSOConfig) (string, string, []string, error) {
	email, _ := claims["email"].(string)
	name, _ := claims["name"].(string)

	groupKey := "groups"
	if cfg != nil && cfg.GroupClaimKey != "" {
		groupKey = cfg.GroupClaimKey
	}
	groups := extractStringSliceClaim(claims, groupKey)

	if email == "" {
		return "", "", nil, fmt.Errorf("okta: missing email claim")
	}
	return email, name, groups, nil
}

// EntraAdapter handles Microsoft Entra ID-specific claim extraction.
type EntraAdapter struct{}

func (EntraAdapter) Name() string { return "entra" }

func (EntraAdapter) ExtractUserInfo(claims map[string]any, cfg *tables.TableGovernanceSSOConfig) (string, string, []string, error) {
	email, _ := claims["preferred_username"].(string)
	if email == "" {
		email, _ = claims["upn"].(string)
	}
	name, _ := claims["name"].(string)

	groupKey := "groups"
	if cfg != nil && cfg.GroupClaimKey != "" {
		groupKey = cfg.GroupClaimKey
	}
	groups := extractStringSliceClaim(claims, groupKey)

	if email == "" {
		return "", "", nil, fmt.Errorf("entra: missing email/UPN claim")
	}
	return email, name, groups, nil
}

func extractStringSliceClaim(claims map[string]any, key string) []string {
	raw, ok := claims[key].([]any)
	if !ok {
		return nil
	}
	groups := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok && s != "" {
			groups = append(groups, s)
		}
	}
	return groups
}

// generateState returns a cryptographically random URL-safe token.
func generateState() (string, error) {
	return generateOpaqueToken(32)
}

// generateCodeVerifier returns a PKCE code verifier.
func generateCodeVerifier() (string, error) {
	return generateOpaqueToken(32)
}

// codeChallenge derives the S256 PKCE challenge from a verifier.
func codeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func normalizeProviderName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
