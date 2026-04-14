# Multi-Node OSS Support — Design Spec

**Date:** 2026-04-14  
**Status:** Draft  
**Scope:** Bifrost OSS — real-time multi-node sync via Redis

---

## Problem Statement

Bifrost OSS docs state that running multiple nodes with a Postgres backend is unsupported because all critical state (provider configs, API keys, budgets, usage, traffic distribution) is kept in memory and never re-read from the database after startup.

Two root problems:

**Problem 1 — Config propagation:** When Node A updates a provider/virtual key/routing rule via the API/UI, it writes to Postgres and updates its own in-memory state. Node B is never notified and continues using stale in-memory config.

**Problem 2 — Usage counter split:** Each node maintains independent in-memory counters (`sync.Map`) for budgets and rate limits. Node A counts 500 requests, Node B counts 500 — each believes it is under the 1000-request limit while the cluster has already reached it. Every 10 seconds each node dumps its absolute in-memory value to Postgres (last-write-wins), so one node's counts erase the other's.

---

## Goals

- Full real-time multi-node sync (config propagation + usage accuracy)
- Rate limits: eventually consistent (small error window acceptable)
- Budgets: strongly consistent (never allow overspend cluster-wide)
- Zero breaking change for single-node deployments
- Support Redis standalone and Redis Cluster

---

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                     Load Balancer                       │
└──────────────┬──────────────────────┬───────────────────┘
               │                      │
        ┌──────▼──────┐        ┌──────▼──────┐
        │   Node A    │        │   Node B    │
        │             │        │             │
        │  in-memory  │        │  in-memory  │
        │  providers  │        │  providers  │
        │  gov state  │        │  gov state  │
        └──┬──────┬───┘        └──┬──────┬───┘
           │      │               │      │
    PUBLISH│  SUBSCRIBE     PUBLISH│  SUBSCRIBE
           │      │               │      │
        ┌──▼──────▼───────────────▼──────▼───┐
        │              Redis                  │
        │  • pub/sub: bifrost:config:*        │
        │  • rate limit counters (INCRBY)     │
        │  • budget counters (Lua CAS)        │
        └──────────────┬──────────────────────┘
                       │  periodic dump / read
        ┌──────────────▼──────────────────────┐
        │             Postgres                │
        │  • source of truth for all config   │
        │  • persisted usage counters         │
        └─────────────────────────────────────┘
```

- **Postgres** is the source of truth for all config and persisted state.
- **Redis** is the real-time sync layer — pub/sub for config invalidation, atomic counters for usage.
- Each node bootstraps from Postgres on startup; Redis keeps live state synchronized across nodes.
- When Redis is unavailable: each node falls back to single-node behavior (existing code path). No crash.

---

## Component 1: Config Sync (Redis Pub/Sub)

### Interface

Add `ClusterSyncer` to `framework/configstore/`:

```go
type ClusterSyncer interface {
    Publish(ctx context.Context, event ConfigInvalidationEvent) error
    Subscribe(ctx context.Context, handler func(ConfigInvalidationEvent)) error
    Close() error
}

type ConfigInvalidationEvent struct {
    Type   string // "provider", "virtual_key", "routing_rule", "mcp_client", "plugin", "framework_config"
    ID     string // entity ID (empty = reload all of that type)
    NodeID string // sender node ID (receivers skip events from themselves)
}
```

### Redis key

Single channel: `bifrost:config:invalidate`

### Publish points

Wrap at the `lib.Config` layer — all mutations pass through here. No handler changes needed.

| Mutation | Event type |
|----------|-----------|
| `AddProvider`, `UpdateProviderConfig`, `AddProviderKey`, `UpdateProviderKey`, `DeleteProviderKey` | `provider` |
| `CreateVirtualKey`, `UpdateVirtualKey`, `DeleteVirtualKey` | `virtual_key` |
| `CreateTeam`, `UpdateTeam`, `DeleteTeam` | `team` |
| `CreateCustomer`, `UpdateCustomer`, `DeleteCustomer` | `customer` |
| `CreateRoutingRule`, `UpdateRoutingRule`, `DeleteRoutingRule` | `routing_rule` |
| `AddMCPClient`, `UpdateMCPClient`, `DeleteMCPClient` | `mcp_client` |
| `ReloadPlugin` | `plugin` |
| `UpdateClientConfig`, `UpdateFrameworkConfig` | `framework_config` |

### Receive side

Each node maintains one SUBSCRIBE goroutine. On event:
1. If `event.NodeID == self.NodeID` → skip (self-published)
2. Call the appropriate existing reload function for the entity type:
   - `provider` → `bifrost.UpdateProvider(id)`
   - `virtual_key` → `governanceStore.ReloadVirtualKey(ctx, id)`
   - `team` → `governanceStore.ReloadTeam(ctx, id)`
   - `customer` → `governanceStore.ReloadCustomer(ctx, id)`
   - `routing_rule` → `governanceStore.ReloadRoutingRules(ctx)`
   - `mcp_client` → `mcpManager.ReloadClient(ctx, id)`
   - `plugin` → `bifrost.ReloadPlugin(...)` (existing)
   - `framework_config` → `configManager.ReloadClientConfigFromConfigStore(ctx)` (existing)
3. If `id` is empty → full reload of that entity type from Postgres

### Graceful degradation

- Redis down at publish time → log warning, continue (write to Postgres succeeded; only cross-node sync is lost).
- Redis down at subscribe time → reconnect with exponential backoff; requests are served normally using local state.

---

## Component 2: Rate Limit Sync (Redis INCRBY — Eventually Consistent)

### Redis key pattern

```
bifrost:rl:{rateLimitID}:tokens    → current token usage (int64)
bifrost:rl:{rateLimitID}:requests  → current request count (int64)
```

### Write path (per-request)

In `UpdateProviderAndModelRateLimitUsageInMemory`, `UpdateVirtualKeyRateLimitUsageInMemory`, and `UpdateUserRateLimitUsageInMemory`:

```
INCRBY bifrost:rl:{rateLimitID}:tokens    <tokenDelta>
INCRBY bifrost:rl:{rateLimitID}:requests  <requestDelta>
```

Keep the existing `sync.Map` update in parallel for backward compat with single-node mode.

### Check path (before routing)

In `CheckRateLimit`, `CheckProviderRateLimit`, `CheckModelRateLimit`:

Read from Redis instead of `sync.Map`:
```
GET bifrost:rl:{rateLimitID}:tokens    → compare to max_tokens
GET bifrost:rl:{rateLimitID}:requests  → compare to max_requests
```

### Reset

In `ResetExpiredRateLimitsInMemory`, when a limit expires:
```
SET bifrost:rl:{rateLimitID}:tokens    0  EX {resetDurationSeconds}
SET bifrost:rl:{rateLimitID}:requests  0  EX {resetDurationSeconds}
```

TTL of the Redis key equals the reset duration — no separate cron job needed.

### Bootstrap (node start)

```
for each rateLimitID from Postgres:
    SET bifrost:rl:{id}:tokens    {currentTokenUsage}    NX   # NX = don't overwrite running cluster
    SET bifrost:rl:{id}:requests  {currentRequestUsage}  NX
```

### Persist to Postgres

`DumpRateLimits` (existing 10s interval): read Redis values → write to Postgres.  
Replaces the existing absolute-overwrite logic.

### Graceful degradation

Redis unavailable → fall back to `sync.Map` local counters (existing code path). Rate limit accuracy is lost cluster-wide but node continues serving.

---

## Component 3: Budget Enforcement (Redis Lua — Strongly Consistent)

### Budget hierarchy (all levels covered)

```
Request
  ├── CheckProviderBudget     → providers.BudgetID (1 budget per provider)
  ├── CheckModelBudget        → modelConfigs.BudgetID (1 budget per model config)
  ├── CheckUserBudget         → users.BudgetID (enterprise)
  └── CheckBudget (VK hierarchy) via collectBudgetsFromHierarchy:
        ├── vk.ProviderConfigs[].Budgets[]   ← multi-budgets per provider per VK
        ├── vk.Budgets[]                     ← multi-budgets at VK level
        ├── vk.Team.BudgetID                 ← single budget at Team
        └── vk.Team.Customer.BudgetID        ← single budget at Customer
            (or vk.Customer.BudgetID if VK directly attached to Customer)
```

### Redis key pattern

```
bifrost:budget:{budgetID}:spent → current spend (float64)
```

Every budget has a unique `budgetID` regardless of level. One Redis key per budget ID, unified pattern across all levels.

### Check + deduct path (pre-request, Lua atomic)

```lua
-- KEYS[1] = "bifrost:budget:{budgetID}:spent"
-- ARGV[1] = cost (float), ARGV[2] = max_budget (float)
local current = tonumber(redis.call('GET', KEYS[1]) or '0')
local cost    = tonumber(ARGV[1])
local max     = tonumber(ARGV[2])
if current + cost > max then
    return 0  -- rejected, no deduction
end
redis.call('INCRBYFLOAT', KEYS[1], cost)
return 1  -- approved and deducted atomically
```

Run per `budgetID` collected from the hierarchy before routing. The check and deduction happen in one atomic operation — no two nodes can simultaneously pass the same budget check and together exceed the limit.

If any script returns 0 → reject the entire request with `BifrostError`, `AllowFallbacks = &false`. Any budgets that were already deducted in this request's loop are refunded immediately via `INCRBYFLOAT key -cost` before returning the error.

### Refund path (post-request, on provider failure)

```
for each budgetID that was deducted:
    INCRBYFLOAT bifrost:budget:{budgetID}:spent -{cost}
```

Called in the PostLLMHook when the provider returns an error. On success: nothing to do (already deducted pre-request).

Covered update functions:
- `UpdateVirtualKeyBudgetUsageInMemory(vk, provider, cost)` → all IDs from `collectBudgetsFromHierarchy`
- `UpdateProviderAndModelBudgetUsageInMemory(model, provider, cost)` → provider budget ID + model budget ID
- `UpdateUserBudgetUsageInMemory(userID, cost)` → user budget ID

### Reset

In `ResetExpiredBudgetsInMemory`, for each budget reaching its period end:
```
SET bifrost:budget:{id}:spent 0 EX {resetDurationSeconds}
```

### Bootstrap

```
for each budget from Postgres:
    SET bifrost:budget:{id}:spent {currentUsage} NX
```

### Persist to Postgres

`DumpBudgets` (existing 10s interval): read Redis values → write to Postgres.

### Graceful degradation

Redis unavailable → fall back to `sync.Map` local counters (existing behavior, eventually consistent). Budget accuracy is lost cluster-wide but nodes continue serving without interruption.

If the operator requires strict budget enforcement even when Redis is down, they can set `cluster.strict_budgets: true` in config. With this flag, Redis unavailability causes budget checks to fall back to a DB-level atomic operation: `UPDATE budgets SET current = current + $cost WHERE current + $cost <= max RETURNING id` (~1-2ms latency per request). Default is `false`.

---

## Component 4: Governance State Reload

When the config invalidation subscriber receives a `virtual_key`, `team`, `customer`, or `routing_rule` event, the node must reload that entity from Postgres into its `LocalGovernanceStore` sync.Maps.

Add reload methods to `LocalGovernanceStore`:
- `ReloadVirtualKey(ctx, id)` — re-fetch from Postgres, update `virtualKeys` sync.Map and cascade to `budgets`/`rateLimits`
- `ReloadTeam(ctx, id)` — re-fetch, update `teams` sync.Map
- `ReloadCustomer(ctx, id)` — re-fetch, update `customers` sync.Map
- `ReloadRoutingRules(ctx)` — full reload of `routingRules` sync.Map (rules are cheap, full reload is simpler)

These are thin wrappers around the existing `LoadGovernanceState` logic, scoped to a single entity.

---

## Component 5: Redis Connection Config

### config.json schema (new optional block)

```json
{
  "cluster": {
    "strict_budgets": false,
    "redis": {
      "addr": "localhost:6379",
      "addrs": [],
      "cluster_mode": false,
      "password": "",
      "db": 0,
      "pool_size": 20,
      "tls": {
        "enabled": false,
        "cert_file": "",
        "key_file": "",
        "ca_file": ""
      }
    }
  }
}
```

- `addr`: single address for standalone mode
- `addrs`: seed node list for cluster mode (overrides `addr` when non-empty)
- `cluster_mode: false` (default) → `redis.NewClient` (standalone)
- `cluster_mode: true` → `redis.NewClusterClient` (cluster); `db` is ignored (cluster does not support DB selection)
- If the entire `cluster` block is absent → single-node mode, all Redis code paths are skipped

### Implementation

Use `redis.UniversalClient` (already the pattern in `framework/vectorstore/redis.go`):

```go
var client redis.UniversalClient
if cfg.ClusterMode {
    client = redis.NewClusterClient(&redis.ClusterOptions{
        Addrs:    resolveAddrs(cfg), // addrs if set, else []string{cfg.Addr}
        Password: cfg.Password,
        PoolSize: cfg.PoolSize,
        // ... TLS, timeouts
    })
} else {
    client = redis.NewClient(&redis.Options{
        Addr:     cfg.Addr,
        Password: cfg.Password,
        DB:       cfg.DB,
        PoolSize: cfg.PoolSize,
        // ... TLS, timeouts
    })
}
```

### Lua scripts in Cluster mode

Each script call accesses exactly one key (`KEYS[1]`). Single-key scripts always hash to the same slot — no cross-slot issue. No hash tags needed.

### Pub/Sub in Cluster mode

`go-redis ClusterClient` supports `Subscribe`/`Publish` natively and broadcasts to all cluster nodes transparently.

---

## Startup Sequence

```
1. Assign node ID: uuid.New()
2. Connect Postgres → load all config into memory (existing)
3. If cluster.redis configured:
   a. Connect Redis
   b. Bootstrap rate limit counters: SET NX from Postgres values
   c. Bootstrap budget counters: SET NX from Postgres values
   d. Start SUBSCRIBE goroutine for "bifrost:config:invalidate"
4. Start serving requests
```

`SET NX` ensures bootstrap does not overwrite counters from a running cluster.

---

## Failure Mode Matrix

| Scenario | Behavior |
|----------|----------|
| Redis down at startup | Log warning, start in single-node mode |
| Redis down mid-run | `atomic.Bool redisAvailable = false` → switch to local sync.Map path |
| Redis reconnects | Re-bootstrap counters from Postgres + local delta; set `redisAvailable = true` |
| Postgres down | Redis serves rate limit + budget checks; config mutations fail (existing behavior) |
| Network partition between nodes | Each node uses its local Redis data; eventually consistent when partition heals |

Mode switching uses `atomic.Bool redisAvailable` — no restart needed.

---

## Testing Strategy

### Unit tests (mock Redis via `miniredis`)

- `TestBudgetLuaScript_AllLevels` — Lua script with all 6 budget levels (provider, model, user, VK provider-config, VK, team/customer)
- `TestRateLimitINCRBY_Concurrent` — 100 goroutines increment concurrently, verify atomic correctness
- `TestConfigInvalidation_SelfFilter` — node filters its own NodeID, does not reload
- `TestConfigInvalidation_ReloadTriggered` — node receives foreign event, calls correct reload function
- `TestBudgetBootstrap_NX` — bootstrap does not overwrite existing Redis values

### Integration tests (real Redis + Postgres, added to `make test-plugins`)

- `TestMultiNode_BudgetNotExceeded` — 2 simulated nodes, combined cost = 0.99 × max → both pass
- `TestMultiNode_BudgetExceeded` — combined cost exceeds max → second node is rejected
- `TestMultiNode_RateLimitClusterWide` — 1000-request limit, 500 per node → both hit limit together
- `TestMultiNode_ConfigSync` — Node A updates provider → Node B receives event → in-memory state updated
- `TestMultiNode_RedisFailover` — Redis goes down → nodes fall back to local mode; Redis comes back → resume sync

Test file: `plugins/governance/multinode_test.go`  
Uses `miniredis` for unit tests; real Redis (via Docker Compose) for integration tests.

---

## Files to Create / Modify

| File | Change |
|------|--------|
| `transports/config.schema.json` | Add `cluster.redis` block (source of truth) |
| `framework/configstore/cluster_syncer.go` | New: `ClusterSyncer` interface + Redis implementation |
| `framework/configstore/cluster_syncer_test.go` | New: unit tests with miniredis |
| `plugins/governance/store.go` | Update Check/Update functions to read/write Redis counters |
| `plugins/governance/store.go` | Add `ReloadVirtualKey`, `ReloadTeam`, `ReloadCustomer`, `ReloadRoutingRules` |
| `plugins/governance/tracker.go` | Update `DumpRateLimits` / `DumpBudgets` to read from Redis |
| `plugins/governance/multinode_test.go` | New: multi-node integration tests |
| `transports/bifrost-http/lib/config.go` | Wrap mutation functions with `clusterSyncer.Publish()` |
| `transports/bifrost-http/server/` | Initialize `ClusterSyncer` from config, wire into `lib.Config` and governance store |
| `examples/configs/withmultinode/config.json` | New example config |
| `docs/features/multi-node.mdx` | New: user-facing documentation |
