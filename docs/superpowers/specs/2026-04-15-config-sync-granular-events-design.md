# Config Sync — Granular Event Types Design

**Date:** 2026-04-15  
**Branch:** feat/multi-node-oss  
**Status:** Approved

---

## Problem

The `PublishingConfigStore` currently maps 6 write methods to a single `"client_config"` event type. The `handleConfigSyncEvent` handler only calls `ReloadClientConfigFromConfigStore` for this event, which reloads connection pool / whitelisted routes — it does **not** reload auth config, proxy config, framework config, or pricing overrides.

**Result:** When node A updates auth, proxy, framework config, or pricing overrides, peer nodes never apply the change to their in-memory state.

### Affected write methods and current (broken) mapping

| Write method | Current event | Should be |
|---|---|---|
| `UpdateClientConfig` | `"client_config"` | `"client_config"` ✓ |
| `UpdateAuthConfig` | `"client_config"` | `"auth_config"` |
| `UpdateProxyConfig` | `"client_config"` | `"proxy_config"` |
| `UpdateFrameworkConfig` | `"client_config"` | `"framework_config"` |
| `CreatePricingOverride` | `"client_config"` | `"pricing_override"` (upsert + ID) |
| `UpdatePricingOverride` | `"client_config"` | `"pricing_override"` (upsert + ID) |
| `DeletePricingOverride` | `"client_config"` | `"pricing_override"` (delete + ID) |

### Out of scope

- `UpdateVectorStoreConfig` / `UpdateLogsStoreConfig` — called only at server startup from `lib/config.go`, no runtime admin API. Not a sync gap.
- Budget/RL counters in FullReload — RecoveryMerge already handles Redis reconnect; DumpRateLimits/DumpBudgets keeps Postgres up-to-date every 10s. Adding RecoveryMerge to FullReload would cause over-merging on healthy nodes. Option A (no change) is correct.

---

## Design

### Transport

No change. Redis Streams (`XADD`/`XREAD`) remain the sync transport. `ConfigSyncEvent` struct already has `Type`, `Action`, `ID`, `NodeID`, `UpdatedAt` fields — no new fields needed.

---

### Section 1: Event Publishing (`publishing_config_store.go`)

Change the event type emitted by 4 write methods:

```go
// UpdateAuthConfig
scheduleEvent(ctx, ConfigSyncEvent{Type: "auth_config", Action: "upsert", UpdatedAt: time.Now()}, ...)

// UpdateProxyConfig
scheduleEvent(ctx, ConfigSyncEvent{Type: "proxy_config", Action: "upsert", UpdatedAt: time.Now()}, ...)

// UpdateFrameworkConfig
scheduleEvent(ctx, ConfigSyncEvent{Type: "framework_config", Action: "upsert", UpdatedAt: time.Now()}, ...)

// CreatePricingOverride / UpdatePricingOverride
scheduleEvent(ctx, ConfigSyncEvent{Type: "pricing_override", Action: "upsert", ID: override.ID, UpdatedAt: time.Now()}, ...)

// DeletePricingOverride
scheduleEvent(ctx, ConfigSyncEvent{Type: "pricing_override", Action: "delete", ID: id, UpdatedAt: time.Now()}, ...)
```

`UpdateClientConfig` keeps `"client_config"` unchanged.

---

### Section 2: Event Handler (`server.go` — `handleConfigSyncEvent`)

Add 4 cases to the switch. All errors log warn and continue — consistent with existing cases.

**Key constraint:** The peer handler must **not** call the server's `UpdateAuthConfig` method (it writes to DB → double-write). It reads from DB and updates in-memory directly.

```go
case "auth_config":
    // Read from DB, update AuthMiddleware in-memory only (no DB write on peer).
    if config, err := s.Config.ConfigStore.GetAuthConfig(ctx); err == nil && config != nil {
        if s.AuthMiddleware != nil {
            s.AuthMiddleware.UpdateAuthConfig(config)
        }
    } else if err != nil {
        logger.Warn("cluster: auth config reload failed: %v", err)
    }

case "proxy_config":
    // ReloadProxyConfig is in-memory only (updates s.Config.ProxyConfig).
    if config, err := s.Config.ConfigStore.GetProxyConfig(ctx); err == nil {
        _ = s.ReloadProxyConfig(ctx, config)
    } else {
        logger.Warn("cluster: proxy config reload failed: %v", err)
    }

case "framework_config":
    // Read from DB, convert TableFrameworkConfig → FrameworkConfig,
    // update s.Config.FrameworkConfig, then call UpdateSyncConfig.
    if config, err := s.Config.ConfigStore.GetFrameworkConfig(ctx); err == nil && config != nil {
        // conversion logic (reuse pattern from initFrameworkConfig)
        _ = s.UpdateSyncConfig(ctx)
    } else if err != nil {
        logger.Warn("cluster: framework config reload failed: %v", err)
    }

case "pricing_override":
    // UpsertPricingOverride / DeletePricingOverride are in-memory only (no DB write on peer).
    if event.Action == "delete" {
        _ = s.DeletePricingOverride(ctx, event.ID)
    } else {
        if override, err := s.Config.ConfigStore.GetPricingOverrideByID(ctx, event.ID); err == nil && override != nil {
            _ = s.UpsertPricingOverride(ctx, override)
        } else if err != nil {
            logger.Warn("cluster: pricing override %s reload failed: %v", event.ID, err)
        }
    }
```

---

### Section 3: FullReload additions (`server.go` — `FullReload`)

Add 4 reload steps immediately after `ReloadClientConfigFromConfigStore`. All errors log warn and continue.

```go
// Auth config — update AuthMiddleware in-memory only
if config, err := s.Config.ConfigStore.GetAuthConfig(ctx); err == nil && config != nil {
    if s.AuthMiddleware != nil {
        s.AuthMiddleware.UpdateAuthConfig(config)
    }
} else if err != nil {
    logger.Warn("FullReload: auth config reload failed: %v", err)
}

// Proxy config — update s.Config.ProxyConfig
if config, err := s.Config.ConfigStore.GetProxyConfig(ctx); err == nil {
    _ = s.ReloadProxyConfig(ctx, config)
} else {
    logger.Warn("FullReload: proxy config reload failed: %v", err)
}

// Framework config — update FrameworkConfig + pricing sync interval
if config, err := s.Config.ConfigStore.GetFrameworkConfig(ctx); err == nil && config != nil {
    // convert + update s.Config.FrameworkConfig
    _ = s.UpdateSyncConfig(ctx)
} else if err != nil {
    logger.Warn("FullReload: framework config reload failed: %v", err)
}

// Pricing overrides — ReloadPricingFromDBAndPopulateModelPool handles
// clear + full reload from DB (no network calls, no URL fetch).
if err := s.ReloadPricingFromDBAndPopulateModelPool(ctx); err != nil {
    logger.Warn("FullReload: pricing overrides reload failed: %v", err)
}
```

---

### Section 4: Testing

**`framework/configstore/publishing_config_store_test.go`**

One test per write method asserting the correct event type, action, and ID in the Redis Stream:

| Test | Assert |
|---|---|
| `TestPublish_UpdateAuthConfig` | type=`auth_config`, action=`upsert` |
| `TestPublish_UpdateProxyConfig` | type=`proxy_config`, action=`upsert` |
| `TestPublish_UpdateFrameworkConfig` | type=`framework_config`, action=`upsert` |
| `TestPublish_CreatePricingOverride` | type=`pricing_override`, action=`upsert`, ID matches |
| `TestPublish_UpdatePricingOverride` | type=`pricing_override`, action=`upsert`, ID matches |
| `TestPublish_DeletePricingOverride` | type=`pricing_override`, action=`delete`, ID matches |

Use `miniredis` (already in the test file). Use `readLastStreamEvent` helper already defined.

**`transports/bifrost-http/server/server_test.go`**

Test `handleConfigSyncEvent` with mock ConfigStore, asserting in-memory state is updated:

| Test | Assert |
|---|---|
| `TestHandleConfigSync_AuthConfig` | `AuthMiddleware.UpdateAuthConfig` called with correct config |
| `TestHandleConfigSync_ProxyConfig` | `s.Config.ProxyConfig` updated |
| `TestHandleConfigSync_FrameworkConfig` | `UpdateSyncConfig` called |
| `TestHandleConfigSync_PricingOverrideUpsert` | `ModelCatalog.UpsertPricingOverrides` called |
| `TestHandleConfigSync_PricingOverrideDelete` | `ModelCatalog.DeletePricingOverride` called |

---

## Multi-node / FullReload: issues and resolutions

Granular sync and `FullReload` increase how often peers run `UpdateSyncConfig` (framework URL / pricing sync) and the final `reconcilePlugins` pass. Two classes of production issues showed up; both are addressed in code.

### 1. `reconcilePlugins` removed built-in plugins when `config_plugins` had no row

**Symptom:** `failed to reload virtual key: plugin governance not found` (and similar paths that call `getGovernancePlugin()`), even though nothing logged an explicit “plugin unloaded” — **`RemovePlugin` does not log on success**.

**Cause:** `reconcilePlugins` compared in-memory plugin status to `dbEnabled`, where `dbEnabled` only contains rows from `config_plugins` with **`enabled=true`**. Built-in plugins (telemetry, governance, compat, …) are loaded from code at startup and **may have no row** in `config_plugins`. They were therefore absent from `dbEnabled` and incorrectly treated as “orphans” and removed from `BasePlugins`.

**Resolution:** Build `dbRowsByName` for **all** plugin rows (any `enabled`). For built-ins (`lib.IsBuiltinPlugin`), if **there is no row at all**, **skip** `RemovePlugin`. If a row exists with `enabled=false`, `dbEnabled` still omits the plugin — **remove** from memory as before.

**Files:** `transports/bifrost-http/server/server.go` — `reconcilePlugins`.

---

### 2. PostgreSQL deadlock (`SQLSTATE 40P01`) during model catalog DB sync

**Symptom:** e.g. `failed to sync model parameters to database: failed to upsert model parameters for model … ERROR: deadlock detected`.

**Cause:** `syncModelParameters` and `syncPricing` ran many `ON CONFLICT` upserts **inside one transaction**, but iterated **`range` over a Go `map`**, whose order is **non-deterministic**. Two nodes running `FullReload` / sync concurrently could lock rows in different orders.

**Resolution:**

- **`syncModelParameters`:** collect model names, **`slices.Sort`**, then upsert in sorted order inside the transaction.
- **`syncPricing`:** dedupe pricing rows with stable key order, **`slices.SortFunc` by** `makeKey(model, provider, mode)`, then upsert in that order.

**Note:** Per-row `ON CONFLICT` upserts (see comments on `UpsertModelParameters` / `UpsertModelPrices`) reduce contention for a **single** row; they do **not** remove cross-row ordering deadlocks inside one multi-statement transaction.

**Files:** `framework/modelcatalog/sync.go` — `syncModelParameters`, `syncPricing`.

---

## File Changelist

| File | Change |
|---|---|
| `framework/configstore/publishing_config_store.go` | Fix event types for 6 write methods |
| `transports/bifrost-http/server/server.go` | Add 4 cases to `handleConfigSyncEvent`; add 4 reload steps to `FullReload`; fix `reconcilePlugins` for built-ins vs `config_plugins` |
| `framework/configstore/publishing_config_store_test.go` | 6 new publish tests |
| `transports/bifrost-http/server/server_test.go` | 5 new handler tests |
| `framework/modelcatalog/sync.go` | Deterministic upsert order for model parameters + pricing (deadlock avoidance) |
