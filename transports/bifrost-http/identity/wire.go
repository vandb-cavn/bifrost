// Package identity is a fork-owned overlay that adds multi-user auth + RBAC to
// open-source Bifrost without modifying core auth/session code. The OSS core
// calls into it via two lines in the server bootstrap (see FORK_PATCHES.md).
package identity

import (
	"context"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
)

// Middlewares returns the overlay's HTTP middlewares, in apply order:
// IdentityMiddleware (login-intercept + attach user) then RBACMiddleware.
// They must be appended to the API middleware chain the core applies to all
// /api/* routes, so RBAC can govern core routes too.
func Middlewares(store configstore.ConfigStore, authEnabled func() bool) []schemas.BifrostHTTPMiddleware {
	return nil // filled in Task 4 + 5
}

// Wire runs the overlay's DB migration and registers its routes on the shared
// router. Call once during bootstrap, after core routes are registered.
func Wire(ctx context.Context, r *router.Router, store configstore.ConfigStore) error {
	return nil // filled in Task 2 + 6
}
