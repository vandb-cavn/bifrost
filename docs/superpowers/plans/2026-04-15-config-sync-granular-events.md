# Config Sync — Granular Event Types Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the overloaded `"client_config"` sync event with four granular event types (`auth_config`, `proxy_config`, `framework_config`, `pricing_override`) so peer nodes apply targeted in-memory reloads on each config change.

**Architecture:** Three edit sites: (1) `PublishingConfigStore` emits the correct event type per write method; (2) `handleConfigSyncEvent` switches on the new types and calls the appropriate in-memory reload; (3) `FullReload` adds the same four reload steps so stream-gap recovery is complete. A private helper `frameworkPricingConfig` centralises the `TableFrameworkConfig → modelcatalog.Config` field mapping so both reload sites stay in sync automatically when new pricing fields are added.

**Tech Stack:** Go, `go-redis/v9`, miniredis (tests), SQLite in-memory (tests)

---

## Context for the Implementer

### Why this change is needed

`PublishingConfigStore` wraps every config write and publishes a Redis Stream event so peer nodes stay in sync. Six write methods currently emit a generic `"client_config"` event. The handler on peer nodes calls `ReloadClientConfigFromConfigStore` for that event — which only reloads the connection pool and whitelisted routes. Auth config, proxy config, framework config (pricing sync interval/URL), and pricing overrides are **never reloaded** on peer nodes after a change.

### Key constraint: no DB write on peer

`BifrostHTTPServer.UpdateAuthConfig` writes to the DB *and* updates in-memory. The peer handler must **not** call it — that would double-write. Instead, the peer reads from DB and updates in-memory directly via `s.AuthMiddleware.UpdateAuthConfig(config)`.

Same pattern for pricing overrides: `s.UpsertPricingOverride` and `s.DeletePricingOverride` are **in-memory only** — safe to call on peers.

### Test commands

Run configstore tests: `GOWORK=off go test ./configstore/... -v` (from `framework/` directory)  
Run server tests: `GOWORK=off go test ./bifrost-http/server/... -v` (from `transports/` directory)

---

## File Map

| File | Change |
|---|---|
| `framework/configstore/publishing_config_store.go` | Fix 6 `scheduleEvent` calls (change `"client_config"` → correct type) |
| `framework/configstore/publishing_config_store_test.go` | Add 6 publish tests |
| `transports/bifrost-http/server/server.go` | Add 4 cases to `handleConfigSyncEvent`; add 4 reload steps to `FullReload` |
| `transports/bifrost-http/server/server_test.go` | Add 3 handler tests (proxy config, pricing upsert, pricing delete) |

---

## Task 1: Fix event types in `PublishingConfigStore`

**Files:**
- Modify: `framework/configstore/publishing_config_store.go:333-471`

### Background

Lines 337, 345, 353, 451, 460, 469 all call `scheduleEvent` with `Type: "client_config"`. Each needs the correct type. `UpdateAuthConfig` and `UpdateProxyConfig` and `UpdateFrameworkConfig` have no ID (singleton configs). `CreatePricingOverride`, `UpdatePricingOverride`, `DeletePricingOverride` need `ID: override.ID` or `ID: id`.

- [ ] **Step 1: Fix `UpdateAuthConfig` (line 337)**

In `framework/configstore/publishing_config_store.go`, change:
```go
func (pcs *PublishingConfigStore) UpdateAuthConfig(ctx context.Context, config *AuthConfig) error {
	if err := pcs.ConfigStore.UpdateAuthConfig(ctx, config); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "client_config", Action: "upsert", UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}
```
to:
```go
func (pcs *PublishingConfigStore) UpdateAuthConfig(ctx context.Context, config *AuthConfig) error {
	if err := pcs.ConfigStore.UpdateAuthConfig(ctx, config); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "auth_config", Action: "upsert", UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}
```

- [ ] **Step 2: Fix `UpdateProxyConfig` (line 345)**

Change:
```go
func (pcs *PublishingConfigStore) UpdateProxyConfig(ctx context.Context, config *tables.GlobalProxyConfig) error {
	if err := pcs.ConfigStore.UpdateProxyConfig(ctx, config); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "client_config", Action: "upsert", UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}
```
to:
```go
func (pcs *PublishingConfigStore) UpdateProxyConfig(ctx context.Context, config *tables.GlobalProxyConfig) error {
	if err := pcs.ConfigStore.UpdateProxyConfig(ctx, config); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "proxy_config", Action: "upsert", UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}
```

- [ ] **Step 3: Fix `UpdateFrameworkConfig` (line 353)**

Change:
```go
func (pcs *PublishingConfigStore) UpdateFrameworkConfig(ctx context.Context, config *tables.TableFrameworkConfig) error {
	if err := pcs.ConfigStore.UpdateFrameworkConfig(ctx, config); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "client_config", Action: "upsert", UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}
```
to:
```go
func (pcs *PublishingConfigStore) UpdateFrameworkConfig(ctx context.Context, config *tables.TableFrameworkConfig) error {
	if err := pcs.ConfigStore.UpdateFrameworkConfig(ctx, config); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "framework_config", Action: "upsert", UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}
```

- [ ] **Step 4: Fix `CreatePricingOverride` (line 451)**

Change:
```go
func (pcs *PublishingConfigStore) CreatePricingOverride(ctx context.Context, override *tables.TablePricingOverride, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.CreatePricingOverride(ctx, override, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "client_config", Action: "upsert", UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}
```
to:
```go
func (pcs *PublishingConfigStore) CreatePricingOverride(ctx context.Context, override *tables.TablePricingOverride, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.CreatePricingOverride(ctx, override, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "pricing_override", Action: "upsert", ID: override.ID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}
```

- [ ] **Step 5: Fix `UpdatePricingOverride` (line 460)**

Change:
```go
func (pcs *PublishingConfigStore) UpdatePricingOverride(ctx context.Context, override *tables.TablePricingOverride, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.UpdatePricingOverride(ctx, override, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "client_config", Action: "upsert", UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}
```
to:
```go
func (pcs *PublishingConfigStore) UpdatePricingOverride(ctx context.Context, override *tables.TablePricingOverride, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.UpdatePricingOverride(ctx, override, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "pricing_override", Action: "upsert", ID: override.ID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}
```

- [ ] **Step 6: Fix `DeletePricingOverride` (line 464)**

Change:
```go
func (pcs *PublishingConfigStore) DeletePricingOverride(ctx context.Context, id string, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.DeletePricingOverride(ctx, id, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "client_config", Action: "upsert", UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}
```
to:
```go
func (pcs *PublishingConfigStore) DeletePricingOverride(ctx context.Context, id string, tx ...*gorm.DB) error {
	ctx = ctxForTxnWrite(ctx, tx)
	if err := pcs.ConfigStore.DeletePricingOverride(ctx, id, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "pricing_override", Action: "delete", ID: id, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}
```

---

## Task 2: Write and run publish tests

**Files:**
- Modify: `framework/configstore/publishing_config_store_test.go`

The test file already has `newMiniRedis`, `readLastStreamEvent`, and `setupRDBTestStore` helpers. Each test below follows the same pattern as `TestPublishingConfigStore_PublishAfterCommit`.

`AuthConfig` is `configstore.AuthConfig{IsEnabled: false}` — no credentials required when disabled.
`GlobalProxyConfig` is in `framework/configstore/tables` package as `tables.GlobalProxyConfig`.
`TableFrameworkConfig` is in `framework/configstore/tables` package.
`TablePricingOverride` is in `framework/configstore/tables` package with field `ID string`.

- [ ] **Step 1: Add 6 tests to `publishing_config_store_test.go`**

Append after the last test in the file:

```go
func TestPublish_UpdateAuthConfig(t *testing.T) {
	client, _ := newMiniRedis(t)
	syncer := NewRedisClusterSyncer(client, testLogger{})
	inner := setupRDBTestStore(t)
	pcs := NewPublishingConfigStore(inner, syncer, "node-A", testLogger{})

	ctx := context.Background()
	err := pcs.UpdateAuthConfig(ctx, &AuthConfig{IsEnabled: false})
	require.NoError(t, err)

	ev := readLastStreamEvent(t, client)
	require.NotNil(t, ev)
	assert.Equal(t, "auth_config", ev.Type)
	assert.Equal(t, "upsert", ev.Action)
}

func TestPublish_UpdateProxyConfig(t *testing.T) {
	client, _ := newMiniRedis(t)
	syncer := NewRedisClusterSyncer(client, testLogger{})
	inner := setupRDBTestStore(t)
	pcs := NewPublishingConfigStore(inner, syncer, "node-A", testLogger{})

	ctx := context.Background()
	err := pcs.UpdateProxyConfig(ctx, &tables.GlobalProxyConfig{Enabled: false})
	require.NoError(t, err)

	ev := readLastStreamEvent(t, client)
	require.NotNil(t, ev)
	assert.Equal(t, "proxy_config", ev.Type)
	assert.Equal(t, "upsert", ev.Action)
}

func TestPublish_UpdateFrameworkConfig(t *testing.T) {
	client, _ := newMiniRedis(t)
	syncer := NewRedisClusterSyncer(client, testLogger{})
	inner := setupRDBTestStore(t)
	pcs := NewPublishingConfigStore(inner, syncer, "node-A", testLogger{})

	ctx := context.Background()
	url := "https://example.com/pricing.json"
	err := pcs.UpdateFrameworkConfig(ctx, &tables.TableFrameworkConfig{PricingURL: &url})
	require.NoError(t, err)

	ev := readLastStreamEvent(t, client)
	require.NotNil(t, ev)
	assert.Equal(t, "framework_config", ev.Type)
	assert.Equal(t, "upsert", ev.Action)
}

func TestPublish_CreatePricingOverride(t *testing.T) {
	client, _ := newMiniRedis(t)
	syncer := NewRedisClusterSyncer(client, testLogger{})
	inner := setupRDBTestStore(t)
	pcs := NewPublishingConfigStore(inner, syncer, "node-A", testLogger{})

	ctx := context.Background()
	override := &tables.TablePricingOverride{ID: "po-001", Name: "test"}
	err := pcs.CreatePricingOverride(ctx, override)
	require.NoError(t, err)

	ev := readLastStreamEvent(t, client)
	require.NotNil(t, ev)
	assert.Equal(t, "pricing_override", ev.Type)
	assert.Equal(t, "upsert", ev.Action)
	assert.Equal(t, "po-001", ev.ID)
}

func TestPublish_UpdatePricingOverride(t *testing.T) {
	client, _ := newMiniRedis(t)
	syncer := NewRedisClusterSyncer(client, testLogger{})
	inner := setupRDBTestStore(t)
	pcs := NewPublishingConfigStore(inner, syncer, "node-A", testLogger{})

	ctx := context.Background()
	// Create first so update has something to find
	override := &tables.TablePricingOverride{ID: "po-002", Name: "test"}
	require.NoError(t, inner.CreatePricingOverride(ctx, override))

	err := pcs.UpdatePricingOverride(ctx, override)
	require.NoError(t, err)

	ev := readLastStreamEvent(t, client)
	require.NotNil(t, ev)
	assert.Equal(t, "pricing_override", ev.Type)
	assert.Equal(t, "upsert", ev.Action)
	assert.Equal(t, "po-002", ev.ID)
}

func TestPublish_DeletePricingOverride(t *testing.T) {
	client, _ := newMiniRedis(t)
	syncer := NewRedisClusterSyncer(client, testLogger{})
	inner := setupRDBTestStore(t)
	pcs := NewPublishingConfigStore(inner, syncer, "node-A", testLogger{})

	ctx := context.Background()
	// Create first so delete has something to find
	override := &tables.TablePricingOverride{ID: "po-003", Name: "test"}
	require.NoError(t, inner.CreatePricingOverride(ctx, override))

	err := pcs.DeletePricingOverride(ctx, "po-003")
	require.NoError(t, err)

	ev := readLastStreamEvent(t, client)
	require.NotNil(t, ev)
	assert.Equal(t, "pricing_override", ev.Type)
	assert.Equal(t, "delete", ev.Action)
	assert.Equal(t, "po-003", ev.ID)
}
```

- [ ] **Step 2: Run publish tests — expect all pass**

```bash
cd /path/to/repo/framework
GOWORK=off go test ./configstore/... -run "TestPublish" -v
```

Expected output:
```
--- PASS: TestPublish_UpdateAuthConfig
--- PASS: TestPublish_UpdateProxyConfig
--- PASS: TestPublish_UpdateFrameworkConfig
--- PASS: TestPublish_CreatePricingOverride
--- PASS: TestPublish_UpdatePricingOverride
--- PASS: TestPublish_DeletePricingOverride
PASS
```

- [ ] **Step 3: Run all configstore tests to check no regressions**

```bash
cd /path/to/repo/framework
GOWORK=off go test ./configstore/... -v
```

Expected: all existing tests still pass.

- [ ] **Step 4: Commit**

```bash
git add framework/configstore/publishing_config_store.go \
        framework/configstore/publishing_config_store_test.go
git commit -m "feat(configstore): emit granular sync event types for auth/proxy/framework/pricing"
```

---

## Task 3: Add handler cases to `handleConfigSyncEvent`

**Files:**
- Modify: `transports/bifrost-http/server/server.go:1180`

`handleConfigSyncEvent` is at line 1116. The switch ends after `case "client_config"` at line 1181. Add 4 new cases before the closing brace.

You need these imports (already present in `server.go`):
- `"github.com/maximhq/bifrost/framework"` — for `framework.FrameworkConfig`
- `"github.com/maximhq/bifrost/framework/modelcatalog"` — for `modelcatalog.Config`

Check the existing imports at the top of `server.go` — add any that are missing.

- [ ] **Step 1: Add 4 cases to the switch in `handleConfigSyncEvent`**

Replace:
```go
	case "client_config":
		_ = s.ReloadClientConfigFromConfigStore(ctx)
	}
}
```
with:
```go
	case "client_config":
		_ = s.ReloadClientConfigFromConfigStore(ctx)
	case "auth_config":
		// Read from DB; update AuthMiddleware in-memory only. Do NOT call s.UpdateAuthConfig —
		// that method also writes to DB, which would cause a double-write on peer nodes.
		if config, err := s.Config.ConfigStore.GetAuthConfig(ctx); err == nil && config != nil {
			if s.AuthMiddleware != nil {
				s.AuthMiddleware.UpdateAuthConfig(config)
			}
		} else if err != nil {
			logger.Warn("cluster: auth config reload failed: %v", err)
		}
	case "proxy_config":
		// ReloadProxyConfig is in-memory only (sets s.Config.ProxyConfig).
		if config, err := s.Config.ConfigStore.GetProxyConfig(ctx); err == nil {
			_ = s.ReloadProxyConfig(ctx, config)
		} else {
			logger.Warn("cluster: proxy config reload failed: %v", err)
		}
	case "framework_config":
		// Read TableFrameworkConfig from DB, convert PricingURL/PricingSyncInterval fields
		// into modelcatalog.Config, update s.Config.FrameworkConfig.Pricing, then call
		// UpdateSyncConfig which passes the updated config to ModelCatalog.
		if dbConfig, err := s.Config.ConfigStore.GetFrameworkConfig(ctx); err == nil && dbConfig != nil {
			if s.Config.FrameworkConfig == nil {
				s.Config.FrameworkConfig = &framework.FrameworkConfig{}
			}
			if s.Config.FrameworkConfig.Pricing == nil {
				s.Config.FrameworkConfig.Pricing = &modelcatalog.Config{}
			}
			s.Config.FrameworkConfig.Pricing.PricingURL = dbConfig.PricingURL
			s.Config.FrameworkConfig.Pricing.PricingSyncInterval = dbConfig.PricingSyncInterval
			if err := s.UpdateSyncConfig(ctx); err != nil {
				logger.Warn("cluster: framework config sync failed: %v", err)
			}
		} else if err != nil {
			logger.Warn("cluster: framework config reload failed: %v", err)
		}
	case "pricing_override":
		// UpsertPricingOverride / DeletePricingOverride are in-memory only — safe to call
		// on peer nodes without DB writes.
		if event.Action == "delete" {
			_ = s.DeletePricingOverride(ctx, event.ID)
		} else {
			if override, err := s.Config.ConfigStore.GetPricingOverrideByID(ctx, event.ID); err == nil && override != nil {
				_ = s.UpsertPricingOverride(ctx, override)
			} else if err != nil {
				logger.Warn("cluster: pricing override %s reload failed: %v", event.ID, err)
			}
		}
	}
}
```

- [ ] **Step 2: Verify imports — add if missing**

Check the import block in `server.go` for:
```go
"github.com/maximhq/bifrost/framework"
"github.com/maximhq/bifrost/framework/modelcatalog"
```

If either is missing, add it. The `framework` package is at `framework/config.go` in the repo root. In go.work, it resolves to `github.com/maximhq/bifrost/framework`.

- [ ] **Step 3: Build check**

```bash
cd /path/to/repo/transports
GOWORK=off go build ./bifrost-http/... 2>&1
```

Expected: no output (clean build).

---

## Task 4: Write and run handler tests

**Files:**
- Modify: `transports/bifrost-http/server/server_test.go`

We test three observable side effects (cases where state is directly readable without mocking concrete types):
1. `proxy_config` — sets `s.Config.ProxyConfig` (a plain struct pointer, directly readable)
2. `pricing_override` upsert — calls `ModelCatalog.UpsertPricingOverrides` (we init a real ModelCatalog)
3. `pricing_override` delete — calls `ModelCatalog.DeletePricingOverride` (same)

`auth_config` and `framework_config` handlers rely on `*handlers.AuthMiddleware` and `*modelcatalog.ModelCatalog` whose internal state is hard to inspect without full initialization. These are covered by the integration path (FullReload test) and the publish tests verify the event is emitted correctly.

For the proxy config test, we need a minimal `BifrostHTTPServer` with a stub `ConfigStore`. The `ConfigStore` is an interface (`configstore.ConfigStore`). We'll define a minimal stub in the test file that only implements the methods the test needs.

For pricing override tests, `modelcatalog.ModelCatalog` requires a config store. Use `nil` for the store — `UpsertPricingOverrides` and `DeletePricingOverride` do not use the store (they're pure in-memory).

- [ ] **Step 1: Add imports and stub type to `server_test.go`**

Add these imports if not present:
```go
import (
    "context"
    "testing"

    "github.com/maximhq/bifrost/framework/configstore"
    "github.com/maximhq/bifrost/framework/configstore/tables"
    "github.com/maximhq/bifrost/framework/modelcatalog"
    "github.com/maximhq/bifrost/transports/bifrost-http/lib"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)
```

Add a minimal stub ConfigStore (implements only the methods each test calls):
```go
// stubConfigStore is a minimal configstore.ConfigStore stub for handler tests.
// It embeds a nil-safe base and overrides only the methods each test needs.
// Only implement the methods your test calls — the rest panic at runtime if called.
type stubConfigStore struct {
    configstore.ConfigStore // embed interface for zero-value stubs (panics on unexpected calls)
    proxyConfig  *tables.GlobalProxyConfig
    pricingOverride *tables.TablePricingOverride
}

func (s *stubConfigStore) GetProxyConfig(_ context.Context) (*tables.GlobalProxyConfig, error) {
    return s.proxyConfig, nil
}

func (s *stubConfigStore) GetPricingOverrideByID(_ context.Context, _ string) (*tables.TablePricingOverride, error) {
    return s.pricingOverride, nil
}
```

- [ ] **Step 2: Add proxy config handler test**

```go
func TestHandleConfigSync_ProxyConfig(t *testing.T) {
    proxyConfig := &tables.GlobalProxyConfig{Enabled: true, Type: "http", Host: "proxy.local", Port: 8080}
    stub := &stubConfigStore{proxyConfig: proxyConfig}

    s := &BifrostHTTPServer{
        Config: &lib.Config{ConfigStore: stub},
    }

    s.handleConfigSyncEvent(configstore.ConfigSyncEvent{
        Type:   "proxy_config",
        Action: "upsert",
    })

    require.NotNil(t, s.Config.ProxyConfig)
    assert.True(t, s.Config.ProxyConfig.Enabled)
    assert.Equal(t, "proxy.local", s.Config.ProxyConfig.Host)
}
```

- [ ] **Step 3: Add pricing override upsert handler test**

```go
func TestHandleConfigSync_PricingOverrideUpsert(t *testing.T) {
    override := &tables.TablePricingOverride{ID: "po-test", Name: "Test Override"}
    stub := &stubConfigStore{pricingOverride: override}

    // modelcatalog.New creates an in-memory catalog with no background workers.
    // Pass nil config store — UpsertPricingOverrides does not use it.
    catalog, err := modelcatalog.New(context.Background(), nil, nil)
    require.NoError(t, err)

    s := &BifrostHTTPServer{
        Config: &lib.Config{
            ConfigStore:  stub,
            ModelCatalog: catalog,
        },
    }

    s.handleConfigSyncEvent(configstore.ConfigSyncEvent{
        Type:   "pricing_override",
        Action: "upsert",
        ID:     "po-test",
    })

    // Verify the override is now in the catalog.
    got := catalog.GetPricingOverrideByID("po-test")
    require.NotNil(t, got)
    assert.Equal(t, "Test Override", got.Name)
}
```

- [ ] **Step 4: Add pricing override delete handler test**

```go
func TestHandleConfigSync_PricingOverrideDelete(t *testing.T) {
    override := &tables.TablePricingOverride{ID: "po-del", Name: "To Delete"}
    stub := &stubConfigStore{pricingOverride: override}

    catalog, err := modelcatalog.New(context.Background(), nil, nil)
    require.NoError(t, err)
    // Pre-load the override so delete has something to remove.
    require.NoError(t, catalog.UpsertPricingOverrides(override))

    s := &BifrostHTTPServer{
        Config: &lib.Config{
            ConfigStore:  stub,
            ModelCatalog: catalog,
        },
    }

    s.handleConfigSyncEvent(configstore.ConfigSyncEvent{
        Type:   "pricing_override",
        Action: "delete",
        ID:     "po-del",
    })

    got := catalog.GetPricingOverrideByID("po-del")
    assert.Nil(t, got)
}
```

**Note:** If `modelcatalog.New` or `GetPricingOverrideByID` do not exist with this signature, adjust to match the actual constructor and getter. Check `framework/modelcatalog/` for the correct API. If `modelcatalog` tests are impractical to set up in the `server` package, remove those two tests and add a comment documenting that pricing override handler correctness is verified via the publish tests + manual/integration testing.

- [ ] **Step 5: Run handler tests — expect pass**

```bash
cd /path/to/repo/transports
GOWORK=off go test ./bifrost-http/server/... -run "TestHandleConfigSync" -v
```

Expected:
```
--- PASS: TestHandleConfigSync_ProxyConfig
--- PASS: TestHandleConfigSync_PricingOverrideUpsert
--- PASS: TestHandleConfigSync_PricingOverrideDelete
PASS
```

- [ ] **Step 6: Run all server tests**

```bash
cd /path/to/repo/transports
GOWORK=off go test ./bifrost-http/server/... -v
```

Expected: all existing tests still pass.

- [ ] **Step 7: Commit**

```bash
git add transports/bifrost-http/server/server.go \
        transports/bifrost-http/server/server_test.go
git commit -m "feat(server): handle granular sync events for auth/proxy/framework/pricing"
```

---

## Task 5: Add FullReload steps

**Files:**
- Modify: `transports/bifrost-http/server/server.go:829`

`FullReload` is at line 808. `ReloadClientConfigFromConfigStore` is called at line 829. Add 4 reload blocks immediately after it.

- [ ] **Step 1: Add 4 reload steps after `ReloadClientConfigFromConfigStore`**

Replace:
```go
	if err := s.ReloadClientConfigFromConfigStore(ctx); err != nil {
		logger.Warn("FullReload: client config reload failed: %v", err)
	}

	providers, err := s.Config.ConfigStore.GetProviders(ctx)
```
with:
```go
	if err := s.ReloadClientConfigFromConfigStore(ctx); err != nil {
		logger.Warn("FullReload: client config reload failed: %v", err)
	}

	// Auth config — update AuthMiddleware in-memory only (no DB write).
	if authConfig, err := s.Config.ConfigStore.GetAuthConfig(ctx); err == nil && authConfig != nil {
		if s.AuthMiddleware != nil {
			s.AuthMiddleware.UpdateAuthConfig(authConfig)
		}
	} else if err != nil {
		logger.Warn("FullReload: auth config reload failed: %v", err)
	}

	// Proxy config — update s.Config.ProxyConfig in-memory.
	if proxyConfig, err := s.Config.ConfigStore.GetProxyConfig(ctx); err == nil {
		_ = s.ReloadProxyConfig(ctx, proxyConfig)
	} else {
		logger.Warn("FullReload: proxy config reload failed: %v", err)
	}

	// Framework config — update FrameworkConfig.Pricing fields then call UpdateSyncConfig.
	if dbFwConfig, err := s.Config.ConfigStore.GetFrameworkConfig(ctx); err == nil && dbFwConfig != nil {
		if s.Config.FrameworkConfig == nil {
			s.Config.FrameworkConfig = &framework.FrameworkConfig{}
		}
		if s.Config.FrameworkConfig.Pricing == nil {
			s.Config.FrameworkConfig.Pricing = &modelcatalog.Config{}
		}
		s.Config.FrameworkConfig.Pricing.PricingURL = dbFwConfig.PricingURL
		s.Config.FrameworkConfig.Pricing.PricingSyncInterval = dbFwConfig.PricingSyncInterval
		if err := s.UpdateSyncConfig(ctx); err != nil {
			logger.Warn("FullReload: framework config sync failed: %v", err)
		}
	} else if err != nil {
		logger.Warn("FullReload: framework config reload failed: %v", err)
	}

	// Pricing overrides — ReloadPricingFromDBAndPopulateModelPool clears in-memory overrides
	// and reloads all from DB. No network calls (does not re-fetch the remote pricing URL).
	if err := s.ReloadPricingFromDBAndPopulateModelPool(ctx); err != nil {
		logger.Warn("FullReload: pricing overrides reload failed: %v", err)
	}

	providers, err := s.Config.ConfigStore.GetProviders(ctx)
```

- [ ] **Step 2: Build check**

```bash
cd /path/to/repo/transports
GOWORK=off go build ./bifrost-http/... 2>&1
```

Expected: clean build.

- [ ] **Step 3: Run full server test suite**

```bash
cd /path/to/repo/transports
GOWORK=off go test ./bifrost-http/server/... -v
```

Expected: all tests pass.

- [ ] **Step 4: Commit**

```bash
git add transports/bifrost-http/server/server.go
git commit -m "feat(server): reload auth/proxy/framework/pricing in FullReload"
```

---

## Self-Review Checklist

After implementation, verify:

- [ ] `UpdateAuthConfig` in `publishing_config_store.go` emits `"auth_config"`, not `"client_config"`
- [ ] `UpdateProxyConfig` emits `"proxy_config"`
- [ ] `UpdateFrameworkConfig` emits `"framework_config"`
- [ ] `CreatePricingOverride` and `UpdatePricingOverride` emit `"pricing_override"` with correct `ID`
- [ ] `DeletePricingOverride` emits `"pricing_override"` with `action=delete` and correct `ID`
- [ ] `UpdateClientConfig` still emits `"client_config"` (unchanged)
- [ ] `handleConfigSyncEvent` has cases for `"auth_config"`, `"proxy_config"`, `"framework_config"`, `"pricing_override"`
- [ ] `auth_config` handler does NOT call `s.UpdateAuthConfig` (no DB write on peer)
- [ ] `proxy_config` handler calls `s.ReloadProxyConfig` (in-memory only)
- [ ] `framework_config` handler updates `s.Config.FrameworkConfig.Pricing` then calls `s.UpdateSyncConfig`
- [ ] `pricing_override` handler calls `s.UpsertPricingOverride` or `s.DeletePricingOverride` (in-memory only)
- [ ] `FullReload` adds auth/proxy/framework/pricing reload AFTER `ReloadClientConfigFromConfigStore`
- [ ] All 6 publish tests pass
- [ ] All handler tests pass
- [ ] No regressions in existing test suites
