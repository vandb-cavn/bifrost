package handlers

import (
	"sync"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/valyala/fasthttp"
)

const rbacCacheTTL = 60 * time.Second

type rbacCacheEntry struct {
	// permissions is a set: "Resource:Operation" → true
	permissions map[string]bool
	expiresAt   time.Time
}

// RBACMiddleware resolves session → user → permissions and gates handler access.
// Sessions with a NULL user_id (legacy single-admin) bypass all permission checks.
type RBACMiddleware struct {
	store configstore.ConfigStore
	cache sync.Map // userID (string) → *rbacCacheEntry
}

// NewRBACMiddleware creates a new RBAC middleware backed by the given store.
func NewRBACMiddleware(store configstore.ConfigStore) *RBACMiddleware {
	return &RBACMiddleware{store: store}
}

// RequirePermission returns a middleware that allows a request only when the session user
// has the specified resource+operation permission. Legacy admin sessions (no user_id) always pass.
func (m *RBACMiddleware) RequirePermission(resource, operation string) schemas.BifrostHTTPMiddleware {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) {
			token, _ := ctx.UserValue(schemas.BifrostContextKeySessionToken).(string)
			if token == "" {
				// No session token: auth middleware already passed, treat as legacy admin.
				next(ctx)
				return
			}

			session, err := m.store.GetSession(ctx, token)
			if err != nil {
				SendError(ctx, fasthttp.StatusInternalServerError, "session lookup failed")
				return
			}
			if session == nil || session.UserID == nil {
				// NULL user_id = legacy single-admin → allow all.
				next(ctx)
				return
			}

			userID := *session.UserID
			if !m.hasPermission(ctx, userID, resource, operation) {
				SendError(ctx, fasthttp.StatusForbidden, "insufficient permissions")
				return
			}
			next(ctx)
		}
	}
}

func (m *RBACMiddleware) hasPermission(ctx *fasthttp.RequestCtx, userID, resource, operation string) bool {
	entry := m.loadCache(userID)
	if entry == nil {
		perms, err := m.store.GetUserPermissions(ctx, userID)
		if err != nil {
			return false
		}
		entry = m.storeCache(userID, perms)
	}
	return entry.permissions[resource+":"+operation]
}

func (m *RBACMiddleware) loadCache(userID string) *rbacCacheEntry {
	val, ok := m.cache.Load(userID)
	if !ok {
		return nil
	}
	entry := val.(*rbacCacheEntry)
	if time.Now().After(entry.expiresAt) {
		m.cache.Delete(userID)
		return nil
	}
	return entry
}

func (m *RBACMiddleware) storeCache(userID string, perms []*tables.TablePermission) *rbacCacheEntry {
	permMap := make(map[string]bool, len(perms))
	for _, p := range perms {
		permMap[p.Resource+":"+p.Operation] = true
	}
	entry := &rbacCacheEntry{
		permissions: permMap,
		expiresAt:   time.Now().Add(rbacCacheTTL),
	}
	m.cache.Store(userID, entry)
	return entry
}

// InvalidateUser removes the cached permission set for a user.
func (m *RBACMiddleware) InvalidateUser(userID string) {
	m.cache.Delete(userID)
}

// InvalidateAll clears the entire permission cache.
func (m *RBACMiddleware) InvalidateAll() {
	m.cache.Range(func(k, _ any) bool {
		m.cache.Delete(k)
		return true
	})
}
