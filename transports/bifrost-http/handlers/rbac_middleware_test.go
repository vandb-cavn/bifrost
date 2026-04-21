package handlers

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/valyala/fasthttp"
)

// mockConfigStoreForRBAC implements only the RBAC and session methods needed.
type mockConfigStoreForRBAC struct {
	configstore.ConfigStore
	session *configstoreTables.SessionsTable
	perms   []*configstoreTables.TablePermission
}

func (m *mockConfigStoreForRBAC) GetSession(_ context.Context, _ string) (*configstoreTables.SessionsTable, error) {
	return m.session, nil
}

func (m *mockConfigStoreForRBAC) GetUserPermissions(_ context.Context, _ string) ([]*configstoreTables.TablePermission, error) {
	return m.perms, nil
}

// TestRBACMiddleware_NullUserID_AllowsAll verifies legacy single-admin sessions pass through.
func TestRBACMiddleware_NullUserID_AllowsAll(t *testing.T) {
	SetLogger(&mockLogger{})
	store := &mockConfigStoreForRBAC{
		session: &configstoreTables.SessionsTable{Token: "tok", UserID: nil},
	}
	rbac := NewRBACMiddleware(store)
	mw := rbac.RequirePermission("Settings", "Update")

	var called bool
	handler := mw(func(ctx *fasthttp.RequestCtx) { called = true })

	ctx := &fasthttp.RequestCtx{}
	ctx.SetUserValue(schemas.BifrostContextKeySessionToken, "tok")
	handler(ctx)

	if !called {
		t.Fatal("expected handler to be called for null user_id session")
	}
}

// TestRBACMiddleware_NoSession_NoToken_AllowsAll verifies requests with no session token pass through.
func TestRBACMiddleware_NoSession_NoToken_AllowsAll(t *testing.T) {
	SetLogger(&mockLogger{})
	store := &mockConfigStoreForRBAC{}
	rbac := NewRBACMiddleware(store)
	mw := rbac.RequirePermission("Settings", "Update")

	var called bool
	handler := mw(func(ctx *fasthttp.RequestCtx) { called = true })

	ctx := &fasthttp.RequestCtx{}
	// No session token set
	handler(ctx)

	if !called {
		t.Fatal("expected handler to be called when no session token present")
	}
}

// TestRBACMiddleware_HasPermission_Allows verifies a user with the required permission passes.
func TestRBACMiddleware_HasPermission_Allows(t *testing.T) {
	SetLogger(&mockLogger{})
	userID := "user-1"
	store := &mockConfigStoreForRBAC{
		session: &configstoreTables.SessionsTable{Token: "tok", UserID: &userID},
		perms:   []*configstoreTables.TablePermission{{ID: "settings_update", Resource: "Settings", Operation: "Update"}},
	}
	rbac := NewRBACMiddleware(store)
	mw := rbac.RequirePermission("Settings", "Update")

	var called bool
	handler := mw(func(ctx *fasthttp.RequestCtx) { called = true })

	ctx := &fasthttp.RequestCtx{}
	ctx.SetUserValue(schemas.BifrostContextKeySessionToken, "tok")
	handler(ctx)

	if !called {
		t.Fatal("expected handler to be called for user with permission")
	}
}

// TestRBACMiddleware_MissingPermission_Returns403 verifies a user without the required permission gets 403.
func TestRBACMiddleware_MissingPermission_Returns403(t *testing.T) {
	SetLogger(&mockLogger{})
	userID := "user-1"
	store := &mockConfigStoreForRBAC{
		session: &configstoreTables.SessionsTable{Token: "tok", UserID: &userID},
		perms:   []*configstoreTables.TablePermission{{ID: "vk_view", Resource: "VirtualKeys", Operation: "View"}},
	}
	rbac := NewRBACMiddleware(store)
	mw := rbac.RequirePermission("Settings", "Update")

	handler := mw(func(ctx *fasthttp.RequestCtx) {
		t.Fatal("handler should not be called")
	})

	ctx := &fasthttp.RequestCtx{}
	ctx.SetUserValue(schemas.BifrostContextKeySessionToken, "tok")
	handler(ctx)

	if ctx.Response.StatusCode() != fasthttp.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var body map[string]any
	if err := json.Unmarshal(ctx.Response.Body(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
}
