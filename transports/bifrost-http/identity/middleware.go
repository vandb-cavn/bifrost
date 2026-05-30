package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/encrypt"
	"github.com/valyala/fasthttp"
)

// ctxKeyUser carries the authenticated overlay user (own key; no core edit).
const ctxKeyUser schemas.BifrostContextKey = "identity-authenticated-user"
const IsLocalAdminContextKey schemas.BifrostContextKey = "is_local_admin"


func sendJSON(ctx *fasthttp.RequestCtx, code int, v any) {
	ctx.Response.Header.SetContentType("application/json")
	ctx.SetStatusCode(code)
	_ = json.NewEncoder(ctx).Encode(v)
}

func sendErr(ctx *fasthttp.RequestCtx, code int, msg string) {
	sendJSON(ctx, code, map[string]string{"error": msg})
}

func tokenFromRequest(ctx *fasthttp.RequestCtx) string {
	if h := string(ctx.Request.Header.Peek("Authorization")); h != "" {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return string(ctx.Request.Header.Cookie("token"))
}

func userFrom(ctx *fasthttp.RequestCtx) *IdentityUser {
	if u, ok := ctx.UserValue(ctxKeyUser).(*IdentityUser); ok {
		return u
	}
	return nil
}

// IdentityMiddleware (a) intercepts POST /api/session/login and serves email
// login itself (so the core username handler is never reached — no double
// login, no core edit), and (b) attaches the overlay user to the context for
// all other authenticated /api/* requests.
func (o *Overlay) IdentityMiddleware() schemas.BifrostHTTPMiddleware {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) {
			path := string(ctx.Path())
			if path == "/api/session/login" && string(ctx.Method()) == "POST" {
				o.handleLogin(ctx)
				return // do not fall through to the core login handler
			}
			if strings.HasPrefix(path, "/api/") {
				if tok := tokenFromRequest(ctx); tok != "" {
					if u, err := o.store.UserForToken(context.Background(), tok); err == nil && u != nil {
						ctx.SetUserValue(ctxKeyUser, u)
					}
				}
			}
			next(ctx)
		}
	}
}

// handleLogin implements the 4-step email login (spec §3.7) and maps the
// resulting core session token to the user.
func (o *Overlay) handleLogin(ctx *fasthttp.RequestCtx) {
	if !o.authEnabled() {
		sendErr(ctx, fasthttp.StatusForbidden, "Authentication is not enabled")
		return
	}
	var p struct{ Email, Password string }
	if err := json.Unmarshal(ctx.PostBody(), &p); err != nil {
		sendErr(ctx, fasthttp.StatusBadRequest, "invalid request format")
		return
	}
	email := strings.ToLower(strings.TrimSpace(p.Email))
	u, err := o.store.GetUserByEmail(context.Background(), email)
	if err != nil {
		sendErr(ctx, fasthttp.StatusInternalServerError, "login failed")
		return
	}
	if u == nil || !u.IsActive || u.PasswordHash == nil { // identical 401 — no enumeration
		sendErr(ctx, fasthttp.StatusUnauthorized, "invalid email or password")
		return
	}
	ok, err := encrypt.CompareHash(*u.PasswordHash, p.Password)
	if err != nil {
		sendErr(ctx, fasthttp.StatusInternalServerError, "login failed")
		return
	}
	if !ok {
		sendErr(ctx, fasthttp.StatusUnauthorized, "invalid email or password")
		return
	}

	expiry := time.Duration(o.sessionExpiryHours()) * time.Hour
	token := uuid.New().String()
	// Create the CORE session so core AuthMiddleware authenticates future requests.
	if err := o.configStore.CreateSession(context.Background(), &tables.SessionsTable{
		Token: token, ExpiresAt: time.Now().Add(expiry), CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		sendErr(ctx, fasthttp.StatusInternalServerError, "failed to create session")
		return
	}
	if err := o.store.MapSession(context.Background(), token, u.ID); err != nil {
		sendErr(ctx, fasthttp.StatusInternalServerError, "failed to map session")
		return
	}
	now := time.Now()
	u.LastLoginAt = &now
	_ = o.store.UpdateUser(context.Background(), u)

	cookie := fasthttp.AcquireCookie()
	defer fasthttp.ReleaseCookie(cookie)
	cookie.SetKey("token")
	cookie.SetValue(token)
	cookie.SetExpire(time.Now().Add(expiry))
	cookie.SetPath("/")
	cookie.SetHTTPOnly(true)
	cookie.SetSameSite(fasthttp.CookieSameSiteLaxMode)
	if string(ctx.Request.Header.Peek("X-Forwarded-Proto")) == "https" {
		cookie.SetSecure(true)
	}
	ctx.Response.Header.SetCookie(cookie)

	sendJSON(ctx, fasthttp.StatusOK, map[string]any{
		"message": "Login successful",
		"user":    map[string]any{"id": u.ID, "email": u.Email, "name": u.Name, "role": u.Role},
	})
}

// Overlay bundles the overlay's dependencies.
type Overlay struct {
	store       *Store
	configStore configstore.ConfigStore
	authEnabled func() bool
}

func (o *Overlay) sessionExpiryHours() int {
	var val string
	err := o.store.db.Table("governance_config").Where("key = ?", "session_expiry_hours").Select("value").Scan(&val).Error
	if err == nil && val != "" {
		var hrs int
		if _, err := fmt.Sscanf(val, "%d", &hrs); err == nil && hrs > 0 {
			return hrs
		}
	}
	return 720
}

func hashPassword(pw string) (string, error)     { return encrypt.Hash(pw) }
func compareHash(hash, pw string) (bool, error)  { return encrypt.CompareHash(hash, pw) }

func roleRank(role string) int {
	switch role {
	case RoleAdmin:
		return 3
	case RoleOperator:
		return 2
	case RoleViewer:
		return 1
	default:
		return 0
	}
}

// requiredRank maps (method, path) → minimum role rank. Fail-closed for unknown
// /api/* routes (mutation→admin, read→operator). Self user-routes return viewer
// rank so the handler can enforce self-vs-admin.
func requiredRank(method, path string) int {
	mutation := method != "GET"
	if path == "/api/users/me" {
		return roleRank(RoleViewer)
	}
	if strings.HasPrefix(path, "/api/users/") {
		if strings.HasSuffix(path, "/role") || strings.HasSuffix(path, "/active") {
			return roleRank(RoleAdmin)
		}
		return roleRank(RoleViewer) // handler enforces self-or-admin
	}
	for _, p := range []string{"/api/users", "/api/auth", "/api/config", "/api/scim"} {
		if path == p || strings.HasPrefix(path, p+"/") {
			return roleRank(RoleAdmin)
		}
	}
	for _, p := range []string{
		"/api/providers", "/api/keys", "/api/governance", "/api/mcp", "/api/mcp-logs",
		"/api/plugins", "/api/proxy-config", "/api/cache", "/api/models", "/api/pricing",
		"/api/prompt-repo", "/api/feature-flags", "/api/oauth",
	} {
		if path == p || strings.HasPrefix(path, p+"/") {
			if mutation {
				return roleRank(RoleOperator)
			}
			return roleRank(RoleViewer)
		}
	}
	if path == "/api/logs" || strings.HasPrefix(path, "/api/logs/") {
		return roleRank(RoleViewer)
	}
	if mutation {
		return roleRank(RoleAdmin)
	}
	return roleRank(RoleOperator)
}

// RBACMiddleware authorizes /api/* by role. Runs after IdentityMiddleware.
func (o *Overlay) RBACMiddleware() schemas.BifrostHTTPMiddleware {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) {
			if !o.authEnabled() {
				next(ctx)
				return
			}
			// Core marks local-admin (auth disabled / bootstrap) — bypass.
			if v, ok := ctx.UserValue(IsLocalAdminContextKey).(bool); ok && v {
				next(ctx)
				return
			}
			path := string(ctx.Path())
			if !strings.HasPrefix(path, "/api/") {
				next(ctx)
				return
			}
			u := userFrom(ctx)
			if u == nil {
				next(ctx) // public/whitelisted /api routes (login, health) carry no user
				return
			}
			if roleRank(u.Role) < requiredRank(string(ctx.Method()), path) {
				sendErr(ctx, fasthttp.StatusForbidden, "forbidden: this action requires a higher role")
				return
			}
			next(ctx)
		}
	}
}

// IsViewer reports whether the request's overlay user is a viewer.
func IsViewer(ctx *fasthttp.RequestCtx) bool {
	u := userFrom(ctx)
	return u != nil && u.Role == RoleViewer
}

// MaskSecret returns a masked form (last 4 chars).
func MaskSecret(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return "****" + s[len(s)-4:]
}

// SetUserCtx sets the identity user on the fasthttp context.
func SetUserCtx(ctx *fasthttp.RequestCtx, u *IdentityUser) {
	ctx.SetUserValue(ctxKeyUser, u)
}
