// Package handlers provides HTTP request handlers for the Bifrost HTTP transport.
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

	"github.com/fasthttp/router"
	"github.com/go-jose/go-jose/v4"
	josejwt "github.com/go-jose/go-jose/v4/jwt"
	"github.com/google/uuid"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
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

// SSOHandler manages SSO login, callback, and config CRUD endpoints.
type SSOHandler struct {
	configStore    configstore.ConfigStore
	governanceSync UserGovernanceSync

	jwksMu         sync.RWMutex
	jwksCache      map[string]*jwksCacheEntry
	discoveryMu    sync.RWMutex
	discoveryCache map[string]*discoveryCacheEntry
}

// NewSSOHandler creates a new SSO handler instance.
func NewSSOHandler(cs configstore.ConfigStore, govSync UserGovernanceSync) *SSOHandler {
	h := &SSOHandler{
		configStore:    cs,
		governanceSync: govSync,
		jwksCache:      make(map[string]*jwksCacheEntry),
		discoveryCache: make(map[string]*discoveryCacheEntry),
	}
	if cs != nil {
		go func() {
			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()
			for range ticker.C {
				_ = cs.DeleteExpiredSSONonces(context.Background())
			}
		}()
	}
	return h
}

// RegisterRoutes registers public SSO routes and authenticated governance routes.
func (h *SSOHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.GET("/api/sso/login", h.initiate)
	r.GET("/api/sso/callback", h.callback)

	if h.configStore == nil {
		return
	}

	r.GET("/api/governance/sso/configs", lib.ChainMiddlewares(h.listConfigs, middlewares...))
	r.POST("/api/governance/sso/configs", lib.ChainMiddlewares(h.createConfig, middlewares...))
	r.PUT("/api/governance/sso/configs/{id}", lib.ChainMiddlewares(h.updateConfig, middlewares...))
	r.DELETE("/api/governance/sso/configs/{id}", lib.ChainMiddlewares(h.deleteConfig, middlewares...))
	r.POST("/api/governance/sso/configs/{id}/test", lib.ChainMiddlewares(h.testConfig, middlewares...))
}

func requestScheme(ctx *fasthttp.RequestCtx) string {
	if ctx.IsTLS() || strings.EqualFold(string(ctx.Request.Header.Peek("X-Forwarded-Proto")), "https") {
		return "https"
	}
	return "http"
}

func requestBaseURL(ctx *fasthttp.RequestCtx) string {
	return fmt.Sprintf("%s://%s", requestScheme(ctx), ctx.Host())
}

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
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OIDC discovery returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery read failed: %w", err)
	}

	var doc oidcDiscovery
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("OIDC discovery parse failed: %w", err)
	}
	if doc.AuthorizationEndpoint == "" || doc.TokenEndpoint == "" || doc.JwksURI == "" || doc.Issuer == "" {
		return nil, fmt.Errorf("OIDC discovery missing required fields")
	}

	h.discoveryMu.Lock()
	h.discoveryCache[issuerURL] = &discoveryCacheEntry{doc: doc, fetchedAt: time.Now()}
	h.discoveryMu.Unlock()

	return &doc, nil
}

func (h *SSOHandler) fetchJWKS(jwksURI string) ([]jose.JSONWebKey, error) {
	h.jwksMu.RLock()
	entry := h.jwksCache[jwksURI]
	h.jwksMu.RUnlock()

	if entry != nil && time.Since(entry.fetchedAt) < jwksTTL {
		return entry.keys, nil
	}

	resp, err := safeHTTPClient.Get(jwksURI)
	if err != nil {
		return nil, fmt.Errorf("JWKS fetch failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JWKS endpoint returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("JWKS read failed: %w", err)
	}

	var keySet jose.JSONWebKeySet
	if err := json.Unmarshal(body, &keySet); err != nil {
		return nil, fmt.Errorf("JWKS parse failed: %w", err)
	}

	h.jwksMu.Lock()
	h.jwksCache[jwksURI] = &jwksCacheEntry{keys: keySet.Keys, fetchedAt: time.Now()}
	h.jwksMu.Unlock()

	return keySet.Keys, nil
}

var validateIssuerURL = validateIssuerURLImpl

func validateIssuerURLImpl(rawURL string) error {
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
		if ip.IsLoopback() {
			return fmt.Errorf("issuer URL must not resolve to a loopback address")
		}
	}
	return nil
}

func safeConfigResponse(cfg *tables.TableGovernanceSSOConfig) map[string]any {
	if cfg == nil {
		return nil
	}
	allowedGroups, err := cfg.GetAllowedGroups()
	if err != nil {
		logger.Error("sso: allowed_groups malformed in safeConfigResponse: %v", err)
		allowedGroups = []string{} // not nil — keeps response type consistent
	} else if allowedGroups == nil {
		allowedGroups = []string{} // ensure it's an empty array, not null
	}
	return map[string]any{
		"id":              cfg.ID,
		"provider":        cfg.Provider,
		"issuer_url":      cfg.IssuerURL,
		"client_id":       cfg.ClientID,
		"role_claim_key":  cfg.RoleClaimKey,
		"group_claim_key": cfg.GroupClaimKey,
		"allowed_groups":  allowedGroups,
		"enabled":         cfg.Enabled,
		"created_at":      cfg.CreatedAt,
		"updated_at":      cfg.UpdatedAt,
	}
}

func (h *SSOHandler) listConfigs(ctx *fasthttp.RequestCtx) {
	cfgs, err := h.configStore.ListSSOConfigs(ctx)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	safe := make([]map[string]any, 0, len(cfgs))
	for _, cfg := range cfgs {
		safe = append(safe, safeConfigResponse(cfg))
	}
	SendJSON(ctx, map[string]any{"configs": safe})
}

func (h *SSOHandler) createConfig(ctx *fasthttp.RequestCtx) {
	var body struct {
		Provider      string   `json:"provider"`
		IssuerURL     string   `json:"issuer_url"`
		ClientID      string   `json:"client_id"`
		ClientSecret  string   `json:"client_secret"`
		RoleClaimKey  string   `json:"role_claim_key"`
		GroupClaimKey string   `json:"group_claim_key"`
		AllowedGroups []string `json:"allowed_groups"`
	}
	if err := json.Unmarshal(ctx.PostBody(), &body); err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, "invalid request body")
		return
	}

	body.Provider = normalizeProviderName(body.Provider)
	if _, ok := providerRegistry[body.Provider]; !ok {
		SendError(ctx, fasthttp.StatusBadRequest, "unsupported provider")
		return
	}
	if err := validateIssuerURL(body.IssuerURL); err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	if body.ClientID == "" || body.ClientSecret == "" {
		SendError(ctx, fasthttp.StatusBadRequest, "client_id and client_secret are required")
		return
	}

	now := time.Now().UTC()
	cfg := &tables.TableGovernanceSSOConfig{
		ID:            uuid.NewString(),
		Provider:      body.Provider,
		IssuerURL:     body.IssuerURL,
		ClientID:      body.ClientID,
		ClientSecret:  body.ClientSecret,
		RoleClaimKey:  body.RoleClaimKey,
		GroupClaimKey: body.GroupClaimKey,
		Enabled:       false,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	cfg.SetAllowedGroups(body.AllowedGroups)
	if err := h.configStore.CreateSSOConfig(ctx, cfg); err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	cfg.ClientSecret = ""
	SendJSONWithStatus(ctx, map[string]any{"config": safeConfigResponse(cfg)}, fasthttp.StatusCreated)
}

func (h *SSOHandler) updateConfig(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	if _, err := h.configStore.GetSSOConfig(ctx, id); err != nil {
		if isNotFound(err) {
			SendError(ctx, fasthttp.StatusNotFound, "config not found")
			return
		}
		SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}

	var updates map[string]any
	if err := json.Unmarshal(ctx.PostBody(), &updates); err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, "invalid request body")
		return
	}

	if provider, ok := updates["provider"].(string); ok {
		updates["provider"] = normalizeProviderName(provider)
		if _, ok := providerRegistry[updates["provider"].(string)]; !ok {
			SendError(ctx, fasthttp.StatusBadRequest, "unsupported provider")
			return
		}
	}
	if issuerURL, ok := updates["issuer_url"].(string); ok {
		if err := validateIssuerURL(issuerURL); err != nil {
			SendError(ctx, fasthttp.StatusBadRequest, err.Error())
			return
		}
	}
	if clientID, ok := updates["client_id"].(string); ok && clientID == "" {
		SendError(ctx, fasthttp.StatusBadRequest, "client_id cannot be empty")
		return
	}
	if clientSecret, ok := updates["client_secret"].(string); ok && clientSecret == "" {
		SendError(ctx, fasthttp.StatusBadRequest, "client_secret cannot be empty")
		return
	}
	if allowedGroups, hasKey := updates["allowed_groups"]; hasKey {
		if _, ok := allowedGroups.([]any); !ok {
			SendError(ctx, fasthttp.StatusBadRequest, "allowed_groups must be an array")
			return
		}
	}

	enableRequested, enablePresent := updates["enabled"].(bool)

	if enablePresent && enableRequested {
		delete(updates, "enabled")
		if err := h.configStore.EnableSSOConfig(ctx, id); err != nil {
			if isNotFound(err) {
				SendError(ctx, fasthttp.StatusNotFound, "config not found")
				return
			}
			SendError(ctx, fasthttp.StatusInternalServerError, err.Error())
			return
		}
	}

	updates["updated_at"] = time.Now().UTC()
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
	SendJSON(ctx, map[string]any{"config": safeConfigResponse(cfg)})
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
	discoveryURL := strings.TrimRight(cfg.IssuerURL, "/") + "/.well-known/openid-configuration"
	resp, err := safeHTTPClient.Get(discoveryURL)
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

	providerName := normalizeProviderName(cfg.Provider)
	if _, ok := providerRegistry[providerName]; !ok {
		SendError(ctx, fasthttp.StatusBadRequest, "unsupported provider: "+cfg.Provider)
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
	nonce, err := generateState()
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "failed to generate nonce")
		return
	}

	callbackURL := fmt.Sprintf("%s://%s/api/sso/callback", requestScheme(ctx), ctx.Host())
	if err := h.configStore.CreateSSONonce(ctx, state, verifier, nonce, providerName, time.Now().Add(10*time.Minute)); err != nil {
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
		url.QueryEscape(codeChallenge(verifier)),
	)
	ctx.Redirect(authURL, fasthttp.StatusFound)
}

func (h *SSOHandler) callback(ctx *fasthttp.RequestCtx) {
	state := string(ctx.QueryArgs().Peek("state"))
	code := string(ctx.QueryArgs().Peek("code"))
	if state == "" || code == "" {
		SendError(ctx, fasthttp.StatusBadRequest, "missing state or code")
		return
	}

	nonceRow, err := h.configStore.ConsumeAndDeleteSSONonce(ctx, state)
	if err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, "invalid or expired state")
		return
	}

	cfg, err := h.configStore.GetActiveSSOConfig(ctx)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "SSO not configured")
		return
	}
	if normalizeProviderName(cfg.Provider) != normalizeProviderName(nonceRow.Provider) {
		SendError(ctx, fasthttp.StatusBadRequest, "SSO provider mismatch")
		return
	}

	callbackURL := fmt.Sprintf("%s://%s/api/sso/callback", requestScheme(ctx), ctx.Host())
	claims, err := h.exchangeAndVerify(ctx, cfg, code, nonceRow.CodeVerifier, callbackURL, nonceRow.Nonce)
	if err != nil {
		SendError(ctx, fasthttp.StatusUnauthorized, fmt.Sprintf("token verification failed: %v", err))
		return
	}

	provider, ok := providerRegistry[normalizeProviderName(cfg.Provider)]
	if !ok {
		SendError(ctx, fasthttp.StatusInternalServerError, "unsupported provider: "+cfg.Provider)
		return
	}
	email, name, groups, err := provider.ExtractUserInfo(claims, cfg)
	if err != nil {
		SendError(ctx, fasthttp.StatusUnauthorized, fmt.Sprintf("claim extraction failed: %v", err))
		return
	}
	email = strings.TrimSpace(email)
	if email == "" {
		SendError(ctx, fasthttp.StatusUnauthorized, "missing email claim")
		return
	}

	// Group filter: deny login if allowed_groups configured and user not in any.
	// Fail-closed: if allowed_groups is set but malformed, deny login.
	allowedGroups, err := cfg.GetAllowedGroups()
	if err != nil {
		logger.Error("sso: allowed_groups malformed, denying login: %v", err)
		SendError(ctx, fasthttp.StatusUnauthorized, "not authorized for this application")
		return
	}
	if len(allowedGroups) > 0 {
		allowedSet := make(map[string]bool, len(allowedGroups))
		for _, g := range allowedGroups {
			allowedSet[g] = true // already lowercased by SetAllowedGroups
		}
		allowed := false
		for _, g := range groups {
			if allowedSet[strings.ToLower(strings.TrimSpace(g))] {
				allowed = true
				break
			}
		}
		if !allowed {
			SendError(ctx, fasthttp.StatusUnauthorized, "not authorized for this application")
			return
		}
	}

	user, err := h.configStore.UpsertUserByEmail(ctx, email, name, "oidc")
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "failed to provision user")
		return
	}
	if h.governanceSync != nil {
		h.governanceSync.UpdateUserGovernanceInMemory(user.ID, user.Budget, user.RateLimit)
	}

	session, err := h.createSession(ctx, user.ID)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "failed to create session")
		return
	}

	cookie := fasthttp.AcquireCookie()
	defer fasthttp.ReleaseCookie(cookie)
	cookie.SetKey("token")
	cookie.SetValue(session.Token)
	cookie.SetPath("/")
	cookie.SetHTTPOnly(true)
	cookie.SetSameSite(fasthttp.CookieSameSiteLaxMode)
	cookie.SetExpire(session.ExpiresAt)
	if requestScheme(ctx) == "https" {
		cookie.SetSecure(true)
	}
	ctx.Response.Header.SetCookie(cookie)

	ctx.Redirect("/workspace", fasthttp.StatusFound)
}

func (h *SSOHandler) exchangeAndVerify(ctx context.Context, cfg *tables.TableGovernanceSSOConfig, code, verifier, callbackURL, expectedNonce string) (map[string]any, error) {
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
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return nil, fmt.Errorf("token exchange returned %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("token response read failed: %w", err)
	}

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

	tok, err := josejwt.ParseSigned(tokenResp.IDToken, []jose.SignatureAlgorithm{
		jose.RS256, jose.RS384, jose.RS512,
		jose.PS256, jose.PS384, jose.PS512,
		jose.ES256, jose.ES384, jose.ES512,
		jose.EdDSA,
	})
	if err != nil {
		return nil, fmt.Errorf("JWT parse failed: %w", err)
	}

	var claims map[string]any
	for _, key := range keys {
		var parsed map[string]any
		if err := tok.Claims(key, &parsed); err == nil {
			claims = parsed
			break
		}
	}
	if claims == nil {
		return nil, fmt.Errorf("JWT signature verification failed")
	}

	if iss, _ := claims["iss"].(string); iss != discovery.Issuer {
		return nil, fmt.Errorf("issuer mismatch: got %q, want %q", iss, discovery.Issuer)
	}
	if !audienceMatches(claims["aud"], cfg.ClientID) {
		return nil, fmt.Errorf("audience mismatch")
	}
	exp, ok := claims["exp"].(float64)
	if !ok {
		return nil, fmt.Errorf("missing exp claim")
	}
	if time.Now().Unix() > int64(exp) {
		return nil, fmt.Errorf("token expired")
	}
	if nonce, _ := claims["nonce"].(string); nonce != expectedNonce {
		return nil, fmt.Errorf("nonce mismatch")
	}
	return claims, nil
}

func audienceMatches(raw any, expected string) bool {
	switch v := raw.(type) {
	case string:
		return v == expected
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s == expected {
				return true
			}
		}
	}
	return false
}

func (h *SSOHandler) createSession(ctx context.Context, userID string) (*tables.SessionsTable, error) {
	token, err := generateState()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	session := &tables.SessionsTable{
		Token:      token,
		UserID:     &userID,
		AuthMethod: "oidc",
		ExpiresAt:  now.Add(24 * time.Hour),
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := h.configStore.CreateSession(ctx, session); err != nil {
		return nil, err
	}
	return session, nil
}

var safeHTTPClient = &http.Client{
	Timeout: 5 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 1 {
			return http.ErrUseLastResponse
		}
		return nil
	},
}
