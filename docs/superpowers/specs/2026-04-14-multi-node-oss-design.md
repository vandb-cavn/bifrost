# Multi-Node OSS Support — Design Spec

**Date:** 2026-04-14 (revised 2026-04-15)
**Status:** Draft
**Scope:** Bifrost OSS — real-time multi-node sync via Redis

---

## Problem Statement

Bifrost OSS docs state that running multiple nodes with a Postgres backend is unsupported because all critical state (provider configs, API keys, budgets, usage, traffic distribution) is kept in memory and never re-read from the database after startup.

Two root problems:

**Problem 1 — Config propagation:** When Node A updates a provider/virtual key/routing rule via the API/UI, it writes to Postgres and updates its own in-memory state. Node B is never notified and continues using stale in-memory config.

**Problem 2 — Usage counter split:** Each node maintains independent in-memory counters (`sync.Map`) for budgets and rate limits. Node A counts 500 requests, Node B counts 500 — each believes it is under the 1000-request limit while the cluster has already reached it. Every 10 seconds each node dumps its absolute in-memory value to Postgres (last-write-wins), erasing the other node's counts.

---

## Goals

- Real-time multi-node sync: config propagation + usage accuracy
- Rate limits: eventually consistent (tight window, acceptable small error)
- Budgets: sub-second eventual consistency — much tighter than current 10s dump cycle, but not a strict per-request guarantee
- Zero breaking change for single-node deployments (cluster block absent → single-node mode)
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
    XADD   │  XREAD        XADD  │  XREAD
           │      │               │      │
        ┌──▼──────▼───────────────▼──────▼───┐
        │          Redis (Streams + counters) │
        │  • Stream: bifrost:config:events    │
        │  • rate limit counters (INCRBY)     │
        │  • budget counters (INCRBYFLOAT)    │
        └──────────────┬──────────────────────┘
                       │  periodic dump / read
        ┌──────────────▼──────────────────────┐
        │             Postgres                │
        │  • source of truth for all config   │
        │  • persisted usage counters         │
        └─────────────────────────────────────┘
```

- **Postgres** is the source of truth for all config and persisted state.
- **Redis** is the real-time sync layer — Streams for config invalidation, atomic counters for usage.
- Each node bootstraps from Postgres on startup; Redis keeps live state synchronized across nodes.
- When Redis is unavailable: each node falls back to single-node behavior (existing code path). No crash.

---

## Component 1: Config Sync (Redis Streams)

### Why Streams, not Pub/Sub

Redis Pub/Sub is at-most-once: any invalidation missed during a subscriber disconnect is gone permanently. Redis Streams are persistent — each consumer tracks a `last_seen_id` and can replay missed events after reconnect via `XREAD FROM last_id`. This gives **at-least-once delivery** for config invalidation.

Consequence: **all reload handlers must be idempotent**. Receiving the same event twice must produce the same result as receiving it once. The existing `Reload*` and `Remove*` server functions are already idempotent (reload fetches from DB; re-running is safe).

### Stream key

```
bifrost:config:events    (single stream, all event types)
```

Stream trimmed to last 24h via `MAXLEN ~ 50000` (approximate, configurable). Events older than 24h are considered unrecoverable — full resync from Postgres is used instead.

### Event schema

```go
type ConfigSyncEvent struct {
    Type      string    // "provider", "virtual_key", "team", "customer",
                        // "model_config", "routing_rule", "mcp_client",
                        // "plugin", "client_config"
    Action    string    // "upsert" or "delete"
    ID        string    // entity ID (provider name, UUID, etc.)
    UpdatedAt time.Time // monotonic field from the DB row; used for ordering and dedup
    NodeID    string    // publishing node's ID; receivers skip events from themselves
}
```

`UpdatedAt` allows a subscriber to detect stale events (if it already loaded a newer version of the entity, it can skip an older event). This is not required for correctness (idempotency handles it), but useful for reducing redundant DB reads.

### Publish layer: `PublishingConfigStore` decorator

**Problem (Finding 2):** Governance mutations bypass `lib.Config` and go directly to `ConfigStore` from handlers. Wrapping only `lib.Config` misses a large share of the mutation surface.

**Solution:** Wrap `ConfigStore` itself with a decorator that publishes after each successful write. This catches every code path — handlers, `lib.Config`, and any future callers.

```go
// PublishingConfigStore wraps ConfigStore and emits a stream event after each
// successful write. It only publishes at the "outermost successful write boundary":
// - For plain writes (no tx): publish after the underlying method returns nil.
// - For methods that accept tx ...*gorm.DB: do NOT publish inside the method.
//   The caller (handler or lib.Config) is responsible for publishing after
//   the transaction commits. Helper methods called within a transaction are
//   not wrapped individually.
type PublishingConfigStore struct {
    ConfigStore
    syncer ClusterSyncer
    nodeID string
}
```

**Transaction boundary rule:** Methods with `tx ...*gorm.DB` parameters are called inside transactions by their callers. Publishing inside them would fire before the transaction commits. Instead:
- These methods return normally (no publish inside)
- The wrapping handler/lib.Config publishes explicitly after `tx.Commit()` succeeds
- Only the outermost successful write boundary publishes — not every inner helper

This means the publish call lives in two places:
1. `PublishingConfigStore` wrapping all non-transactional write methods
2. Explicit `syncer.Publish()` calls at transaction commit sites in handlers and `lib.Config` where tx is used

### Reconnect and catch-up flow

Each node stores `lastSeenStreamID` (persisted to Redis key `bifrost:node:{nodeID}:last_seen`):

```
On Redis reconnect:
  1. Read lastSeenStreamID from Redis (or local memory fallback)
  2. XREAD COUNT 1000 BLOCK 0 STREAMS bifrost:config:events {lastSeenStreamID}
  3. Process all returned entries in order; update lastSeenStreamID after each
  4. If stream entries not found (stream trimmed / consumer state lost):
     → Full reload: call Postgres for all entities, reload entire in-memory state
  5. Resume normal XREAD blocking loop
```

Full reload is triggered when:
- `lastSeenStreamID` is older than the oldest entry in the stream
- Node starts fresh (no lastSeenStreamID)
- Redis cluster rebalance / failover wipes stream

### Reload dispatch table (server-layer entry points)

Subscriber receives events and calls the corresponding `BifrostHTTPServer` method. **Never call `LocalGovernanceStore` or `bifrost.Bifrost` directly** — server-layer methods handle all cascade effects (model catalog sync, MCP state, rule recompilation, governance store update).

| Event type | Action | Server method |
|------------|--------|---------------|
| `provider` | `upsert` | `server.ReloadProvider(ctx, provider)` |
| `provider` | `delete` | `server.RemoveProvider(ctx, provider)` |
| `virtual_key` | `upsert` | `server.ReloadVirtualKey(ctx, id)` |
| `virtual_key` | `delete` | `server.RemoveVirtualKey(ctx, id)` |
| `team` | `upsert` | `server.ReloadTeam(ctx, id)` |
| `team` | `delete` | `server.RemoveTeam(ctx, id)` |
| `customer` | `upsert` | `server.ReloadCustomer(ctx, id)` |
| `customer` | `delete` | `server.RemoveCustomer(ctx, id)` |
| `model_config` | `upsert` | `server.ReloadModelConfig(ctx, id)` |
| `model_config` | `delete` | `server.RemoveModelConfig(ctx, id)` |
| `routing_rule` | `upsert` | `server.ReloadRoutingRule(ctx, id)` |
| `routing_rule` | `delete` | `server.RemoveRoutingRule(ctx, id)` |
| `mcp_client` | `upsert` | `server.ReconnectMCPClient(ctx, id)` |
| `mcp_client` | `delete` | `server.RemoveMCPClient(ctx, id)` |
| `plugin` | `upsert` | `server.ReloadPlugin(ctx, name, path, pluginConfig, placement, order)` — args from DB |
| `plugin` | `delete` | `server.RemovePlugin(ctx, name)` |
| `client_config` | `upsert` | `server.ReloadClientConfigFromConfigStore(ctx)` |

If a server method does not yet exist for a needed action, add it at the server layer before wiring the subscriber.

### Graceful degradation

- Redis down at publish time → log warning, continue (Postgres write succeeded; cross-node sync is lost until Redis recovers)
- Redis down at subscribe time → reconnect goroutine with exponential backoff; node serves requests with local state (potentially stale for the outage window)

---

## Component 2: Rate Limit Sync (Redis INCRBY — Eventually Consistent)

### Redis key pattern

```
bifrost:rl:{rateLimitID}:tokens    → cumulative token usage (int64)
bifrost:rl:{rateLimitID}:requests  → cumulative request count (int64)
```

### Write path (per-request)

In `UpdateProviderAndModelRateLimitUsageInMemory`, `UpdateVirtualKeyRateLimitUsageInMemory`, and `UpdateUserRateLimitUsageInMemory`:

```
INCRBY bifrost:rl:{rateLimitID}:tokens    <tokenDelta>
INCRBY bifrost:rl:{rateLimitID}:requests  <requestDelta>
```

Existing `sync.Map` update is kept in parallel for graceful degradation.

### Check path (before routing)

In `CheckRateLimit`, `CheckProviderRateLimit`, `CheckModelRateLimit`: read from Redis instead of `sync.Map`:

```
GET bifrost:rl:{rateLimitID}:tokens    → compare to max_tokens
GET bifrost:rl:{rateLimitID}:requests  → compare to max_requests
```

### Reset

In `ResetExpiredRateLimitsInMemory`, for each expired limit:
```
SET bifrost:rl:{rateLimitID}:tokens    0  EX {resetDurationSeconds}
SET bifrost:rl:{rateLimitID}:requests  0  EX {resetDurationSeconds}
```

TTL = reset duration. No separate cron job needed.

### Bootstrap (node start)

```
for each rateLimitID from Postgres:
    SET bifrost:rl:{id}:tokens    {currentTokenUsage}    NX
    SET bifrost:rl:{id}:requests  {currentRequestUsage}  NX
```

`NX` ensures bootstrap does not overwrite a running cluster's live counters.

### Persist to Postgres

`DumpRateLimits` (existing 10s interval): read from Redis → write to Postgres. Replaces the existing absolute-overwrite logic.

### Graceful degradation

Redis unavailable → fall back to `sync.Map` local counters (existing code path). Rate limit accuracy is lost cluster-wide but requests continue serving.

---

## Component 3: Budget Sync (Redis INCRBYFLOAT — Sub-Second Eventual Consistency)

### Consistency model (important)

Budgets are **sub-second eventually consistent**, not strongly consistent per-request. This is a deliberate, honest trade-off:

- The current system has ~10s eventual consistency window (one dump cycle per node)
- This design reduces it to sub-second (Redis propagation latency ~1ms)
- What it does NOT guarantee: a request will never be the one that tips the budget over. Multiple nodes can pass the check simultaneously before any of them has posted actual cost back to Redis
- Overshoot upper bound: approximately the sum of costs of all currently in-flight requests across the cluster at the moment the budget is reached — not bounded to a single request

This is substantially better than the current 10s × N-nodes window, and honest about what OSS can deliver without pre-request cost estimation infrastructure.

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
bifrost:budget:{budgetID}:spent → current cumulative spend (float64)
```

Every budget has a unique `budgetID` regardless of level. Unified key pattern across all levels.

### Check path (pre-request)

Read from Redis, compare to `max_budget`:

```
GET bifrost:budget:{budgetID}:spent → if >= max → reject
```

Run per `budgetID` from the hierarchy. If any exceeds limit → reject with `BifrostError`, `AllowFallbacks = &false`.

### Write path (post-request, actual cost known)

```go
// Called in PostLLMHook after cost is computed from token usage
for each budgetID in hierarchy:
    INCRBYFLOAT bifrost:budget:{budgetID}:spent {actualCost}
```

This is the only deduction point. There is no pre-deduct/refund pattern — cost is only known after response.

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

`DumpBudgets` (existing 10s interval): read from Redis → write to Postgres.

### Graceful degradation

Redis unavailable → fall back to `sync.Map` local counters (existing behavior). Budget accuracy reverts to the pre-feature 10s eventual consistency window.

If `cluster.strict_budgets: true` is set in config, Redis unavailability instead triggers a DB-level atomic check:
```sql
UPDATE budgets SET current = current + $cost WHERE current + $cost <= max RETURNING id
```
This is strongly consistent but adds ~1-2ms latency per request. Default is `false`.

---

## Component 4: Redis Connection Config

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
- `cluster_mode: false` → `redis.NewClient` (standalone); `cluster_mode: true` → `redis.NewClusterClient`; `db` is ignored in cluster mode (cluster does not support DB selection)
- Entire `cluster` block absent → single-node mode; all Redis code paths skipped

### Implementation pattern

Reuse the existing `redis.UniversalClient` pattern from `framework/vectorstore/redis.go`:

```go
var client redis.UniversalClient
if cfg.ClusterMode {
    client = redis.NewClusterClient(&redis.ClusterOptions{
        Addrs:    resolveAddrs(cfg), // addrs if non-empty, else []string{cfg.Addr}
        Password: cfg.Password,
        PoolSize: cfg.PoolSize,
        // TLS, timeouts (same fields as vectorstore)
    })
} else {
    client = redis.NewClient(&redis.Options{
        Addr:     cfg.Addr,
        Password: cfg.Password,
        DB:       cfg.DB,
        PoolSize: cfg.PoolSize,
    })
}
```

### Redis Streams in Cluster mode

`XADD` and `XREAD` are single-key operations — all nodes read/write the same stream key (`bifrost:config:events`). In cluster mode the key lands on a single slot; all nodes fan out via `XREAD`. No hash tags needed.

`INCRBY`/`INCRBYFLOAT` are single-key operations per rateLimitID / budgetID — no cross-slot issue.

---

## Startup Sequence

```
1. Assign node ID: uuid.New()
2. Connect Postgres → load all config into memory (existing)
3. If cluster.redis configured:
   a. Connect Redis
   b. Bootstrap rate limit counters: SET NX from Postgres values
   c. Bootstrap budget counters: SET NX from Postgres values
   d. Determine lastSeenStreamID:
      - Read from Redis key bifrost:node:{nodeID}:last_seen
      - If not found: set to "$" (read only new events going forward) unless full resync needed
   e. If first start (no persisted state): perform full governance state reload from Postgres
   f. Start XREAD consumer goroutine from lastSeenStreamID
4. Start serving requests
```

---

## Failure Mode Matrix

| Scenario | Behavior |
|----------|----------|
| Redis down at startup | Log warning, start in single-node mode |
| Redis down mid-run | `atomic.Bool redisAvailable = false` → switch all reads/writes to local sync.Map |
| Redis reconnects | Re-bootstrap counters from Postgres (SET NX); XREAD catch-up from lastSeenStreamID; if stream gap → full Postgres resync |
| Postgres down | Redis serves rate limit + budget checks; config mutations fail (existing behavior) |
| Network partition between nodes | Each node uses local Redis data; converges when partition heals |

Mode switching uses `atomic.Bool redisAvailable` — no restart needed.

---

## Testing Strategy

### Unit tests (mock Redis via `miniredis`)

- `TestStreamPublish_AfterCommit` — publish fires only after successful write, not inside uncommitted tx
- `TestStreamConsumer_CatchUp` — consumer resumes from lastSeenStreamID, processes missed events
- `TestStreamConsumer_FullResync` — stream gap triggers full Postgres reload
- `TestReloadHandler_Idempotent` — same event delivered twice produces identical in-memory state
- `TestRateLimitINCRBY_Concurrent` — 100 goroutines increment concurrently, verify atomic correctness
- `TestBudgetCheck_SubSecondWindow` — budget reached; subsequent requests see updated counter
- `TestBudgetBootstrap_NX` — bootstrap does not overwrite existing Redis values

### Integration tests (real Redis + Postgres, added to `make test-plugins`)

- `TestMultiNode_ConfigSync_Upsert` — Node A updates provider → Node B XREAD event → server.ReloadProvider called → in-memory updated
- `TestMultiNode_ConfigSync_Delete` — Node A deletes VK → Node B calls server.RemoveVirtualKey
- `TestMultiNode_ConfigSync_ReconnectCatchup` — Node B subscriber paused mid-stream → resumes → catches up all missed events
- `TestMultiNode_RateLimitClusterWide` — 1000-request limit, 500 requests per node → both hit limit
- `TestMultiNode_BudgetEventualConsistency` — budget spent across 2 nodes; combined total reflected in Redis within sub-second
- `TestMultiNode_RedisFailover` — Redis goes down → nodes fall back to local mode; Redis comes back → resume sync and catch up

Test file: `plugins/governance/multinode_test.go`
Uses `miniredis` for unit tests; real Redis (via Docker Compose) for integration tests.

---

## Files to Create / Modify

| File | Change |
|------|--------|
| `transports/config.schema.json` | Add `cluster` block (source of truth) |
| `framework/configstore/cluster_syncer.go` | New: `ClusterSyncer` interface + Redis Streams implementation |
| `framework/configstore/publishing_config_store.go` | New: `PublishingConfigStore` decorator |
| `framework/configstore/publishing_config_store_test.go` | New: unit tests with miniredis |
| `plugins/governance/store.go` | Update Check/Update functions to read/write Redis counters |
| `plugins/governance/tracker.go` | Update `DumpRateLimits` / `DumpBudgets` to read from Redis |
| `plugins/governance/multinode_test.go` | New: multi-node integration tests |
| `transports/bifrost-http/server/server.go` | Add XREAD consumer goroutine; wire `PublishingConfigStore` at startup; add `ClusterSyncer` to server |
| `transports/bifrost-http/lib/config.go` | Inject `ClusterSyncer`; add explicit `Publish()` calls at tx commit sites |
| `examples/configs/withmultinode/config.json` | New example config |
| `docs/features/multi-node.mdx` | New: user-facing documentation |
