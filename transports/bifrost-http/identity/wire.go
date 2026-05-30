package identity

import (
	"context"
	"fmt"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"gorm.io/gorm"
)

// newOverlay extracts the live *gorm.DB from the concrete RDBConfigStore.
func newOverlay(store configstore.ConfigStore, authEnabled func() bool) (*Overlay, error) {
	rdb, ok := store.(*configstore.RDBConfigStore)
	if !ok {
		return nil, fmt.Errorf("identity overlay requires *RDBConfigStore, got %T", store)
	}
	return &Overlay{store: NewStore(rdb.DB()), configStore: store, authEnabled: authEnabled}, nil
}

// Middlewares returns the overlay's HTTP middlewares, in apply order:
// IdentityMiddleware (login-intercept + attach user) then RBACMiddleware.
// They must be appended to the API middleware chain the core applies to all
// /api/* routes, so RBAC can govern core routes too.
func Middlewares(store configstore.ConfigStore, authEnabled func() bool) []schemas.BifrostHTTPMiddleware {
	o, err := newOverlay(store, authEnabled)
	if err != nil {
		return nil // store not RDB-backed (e.g. some tests) → overlay inactive
	}
	return []schemas.BifrostHTTPMiddleware{o.IdentityMiddleware(), o.RBACMiddleware()}
}

// Wire runs the overlay's DB migration and registers its routes on the shared
// router. Call once during bootstrap, after core routes are registered.
func Wire(ctx context.Context, r *router.Router, store configstore.ConfigStore, authEnabled func() bool, middlewares ...schemas.BifrostHTTPMiddleware) error {
	// 1. Migration (own tables) via the core-provided connection.
	if err := store.RunMigration(ctx, func(ctx context.Context, db *gorm.DB) error {
		return Migrate(ctx, db)
	}); err != nil {
		return fmt.Errorf("identity migration failed: %w", err)
	}
	// 2. Routes on the shared router.
	o, err := newOverlay(store, authEnabled)
	if err != nil {
		return err
	}
	// Wrap overlay routes with the passed-in middlewares chain (which includes core AuthMiddleware
	// and the overlay's identity/RBAC middlewares).
	r.GET("/api/users", lib.ChainMiddlewares(o.listUsers, middlewares...))
	r.POST("/api/users", lib.ChainMiddlewares(o.createUser, middlewares...))
	r.GET("/api/users/me", lib.ChainMiddlewares(o.me, middlewares...))
	r.GET("/api/users/{id}", lib.ChainMiddlewares(o.getUser, middlewares...))
	r.PUT("/api/users/{id}", lib.ChainMiddlewares(o.updateUser, middlewares...))
	r.PUT("/api/users/{id}/role", lib.ChainMiddlewares(o.setRole, middlewares...))
	r.PUT("/api/users/{id}/password", lib.ChainMiddlewares(o.setPassword, middlewares...))
	r.PUT("/api/users/{id}/active", lib.ChainMiddlewares(o.setActive, middlewares...))
	r.GET("/api/auth/settings", lib.ChainMiddlewares(o.getAuthSettings, middlewares...))
	r.PUT("/api/auth/settings", lib.ChainMiddlewares(o.putAuthSettings, middlewares...))
	return nil
}
