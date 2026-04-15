# Multi-Node OSS Support — Design Spec

**Date:** 2026-04-14 (revised 2026-04-15)
**Status:** Approved
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

Stream trimmed to last 24h via `MAXLEN ~ 50000` (approximate, configurable). Events older than the stream window are unrecoverable — full resync from Postgres is triggered instead.

### Consumer identity and cursor durability

Node ID (for self-publish filtering) and consumer identity (for cursor durability) are **separate concerns**:

- **Node ID** — `uuid.New()` on every startup. Used only to filter self-published events in the stream consumer. Ephemeral by design.
- **Consumer ID** — stable across restarts. Configured via `cluster.node_id` (e.g., set via env var `BIFROST_NODE_ID`). Falls back to `os.Hostname()` if not set. In Kubernetes, pod name is the hostname and is stable across restarts within the same pod identity.

Stream cursor stored under a stable key:
```
bifrost:consumer:{consumerID}:last_seen    → last processed stream entry ID
```

On startup, the node reads `bifrost:consumer:{consumerID}:last_seen` to resume from the correct position. A brand-new consumer with no cursor performs a **watermark-first full reload** (see below) to avoid a race window.

### Event schema

```go
type ConfigSyncEvent struct {
    Type      string // "provider", "virtual_key", "team", "customer",
                     // "model_config", "routing_rule", "mcp_client",
                     // "plugin", "client_config"
    Action    string // "upsert" or "delete"
    ID        string // entity ID (provider name, UUID, etc.)
    UpdatedAt time.Time // from the DB row; included for observability/debugging only —
                        // NOT used for dedup or ordering (stream ID is the ordering primitive)
    NodeID    string // publishing node's ephemeral ID; receivers skip events where NodeID == self
}
```

**Ordering and dedup:** Redis stream IDs are monotonic and append-only — this is the authoritative ordering primitive. `UpdatedAt` is metadata only. Since handlers are idempotent, duplicate delivery is safe without additional dedup logic.

**Delete events:** Delete events carry only `Type`, `Action: "delete"`, and `ID`. `UpdatedAt` is omitted (no surviving row). Handlers must not attempt to read the entity from DB on a delete event.

### Publish layer: `PublishingConfigStore` as single choke point

**Problem:** Governance mutations bypass `lib.Config` and go directly to `ConfigStore` from handlers. Manual publish calls scattered across handlers and `lib.Config` create a surface where future write paths can silently miss publishing or double-publish.

**Solution:** A single invariant — **all ConfigStore writes emit events at the correct commit boundary, automatically.**

`PublishingConfigStore` wraps `ConfigStore` and intercepts `ExecuteTransaction` as the sole publish choke point:

```go
type PublishingConfigStore struct {
    ConfigStore                     // embedded — all non-write methods delegate directly
    syncer ClusterSyncer
    nodeID string
}

// ExecuteTransaction is the single publish choke point.
// Events scheduled by write methods inside fn are published only after commit succeeds.
func (pcs *PublishingConfigStore) ExecuteTransaction(
    ctx context.Context,
    fn func(*gorm.DB) error,
) error {
    acc := &eventAccumulator{}
    ctx = withEventAccumulator(ctx, acc)

    err := pcs.ConfigStore.ExecuteTransaction(ctx, fn)
    if err != nil {
        return err // transaction rolled back — do not publish
    }

    // Publish all accumulated events in order, after commit
    for _, ev := range acc.events {
        if pubErr := pcs.syncer.Publish(ctx, ev); pubErr != nil {
            // log warning — Postgres write succeeded, cross-node sync lost for this event
            logger.Warn("failed to publish config event: %v", pubErr)
        }
    }
    return nil
}
```

Each ConfigStore write method (whether called inside a transaction or as a standalone operation) calls `scheduleEvent(ctx, event)`:
- If `ctx` carries an `eventAccumulator` (inside a transaction) → event is queued, published after commit
- If not (standalone write not wrapped in `ExecuteTransaction`) → the write method wraps itself in a micro-transaction, which triggers the publish path above

**Invariant:** "Any write that goes through any ConfigStore method emits an event, at the correct commit boundary." No manual publish calls needed in handlers or `lib.Config`.

### Reconnect and catch-up flow

```
On Redis reconnect (or first start):
  1. Read bifrost:consumer:{consumerID}:last_seen from Redis
     (or local memory fallback if Redis just restarted)
  2. If last_seen_id exists:
     a. XREAD COUNT 1000 STREAMS bifrost:config:events {last_seen_id}
     b. If entries returned: process in order, update last_seen_id after each batch, repeat
     c. If XREAD returns empty (caught up): switch to blocking XREAD loop — done
     d. If last_seen_id is older than oldest entry in stream (gap/trim):
        → trigger watermark-first full reload (see below), then resume blocking XREAD
  3. If no last_seen_id (first start of this consumer identity):
     → trigger watermark-first full reload (see below), then resume blocking XREAD
```

### Watermark-first full reload

Used on first start or when stream gap makes catch-up impossible. The ordering prevents a race window where events committed after the Postgres snapshot would be missed by both the snapshot and the stream cursor.

```
1. XREVRANGE bifrost:config:events + - COUNT 1 → capture watermark W
2. Call server.FullReload(ctx) — reload all runtime state from Postgres (see below)
3. Persist W as last_seen_id: SET bifrost:consumer:{consumerID}:last_seen W
4. Start blocking XREAD from W
```

Any event committed to Postgres before step 2 is captured by the full reload.
Any event committed after W (captured in step 1) appears in the stream with ID > W and is caught by XREAD in step 4.
No event falls through the gap between the snapshot and the stream cursor.

### `server.FullReload(ctx)`

A new server-layer method that reloads all runtime state from Postgres in a fixed, deterministic order. Must be **idempotent** — calling it multiple times produces the same in-memory state as calling it once.

Reload order (dependencies first):

```
1. ReloadClientConfigFromConfigStore(ctx)     — global settings, TLS, auth
2. For each provider: ReloadProvider(ctx, id) — providers + model catalog sync
3. For each model config: ReloadModelConfig(ctx, id)
4. For each virtual key: ReloadVirtualKey(ctx, id)  ┐
   For each team: ReloadTeam(ctx, id)                │ governance state
   For each customer: ReloadCustomer(ctx, id)        │
   For each routing rule: ReloadRoutingRule(ctx, id) ┘
5. For each MCP client: ReconnectMCPClient(ctx, id)
6. Plugin reconciliation (DB-authoritative):
   a. db_plugins  = SELECT * FROM plugins WHERE enabled = true
   b. mem_plugins = server.GetPluginStatus(ctx) → map[name]pluginState
   c. in db_plugins, not in mem_plugins          → ReloadPlugin(ctx, name, path, cfg, placement, order)
   d. in mem_plugins, not in db_plugins          → RemovePlugin(ctx, name)
   e. in both, config/path/placement/order differs → ReloadPlugin(ctx, name, ...)
   f. in both, disabled in DB                    → RemovePlugin(ctx, name)
   Plugins carry side effects; the DB list is the single source of truth.
```

Full reload is also triggered when:
- No `last_seen_id` exists (first start of this consumer identity)
- Stream gap detected (last_seen_id older than oldest stream entry)
- Redis cluster rebalance wipes the stream

### Reload dispatch table (server-layer entry points)

Subscriber receives events and calls the corresponding `BifrostHTTPServer` method. **Never call `LocalGovernanceStore` or `bifrost.Bifrost` directly** — server-layer methods handle all cascade effects (model catalog sync, MCP state, rule recompilation, governance store update). If a server method does not yet exist for a needed action, add it at the server layer before wiring the subscriber.

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

### Graceful degradation

- Redis down at publish time → `ExecuteTransaction` logs warning and continues; Postgres write succeeded; cross-node sync is lost for events emitted during the outage
- Redis down at subscribe time → reconnect goroutine with exponential backoff; node serves with local state (potentially stale for the outage window)

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

Existing `sync.Map` update is kept in parallel for graceful degradation (see below).

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

### Bootstrap (node start / Redis recovery)

See "Redis Recovery — Merging Outage-Period Deltas" below.

### Persist to Postgres

`DumpRateLimits` (existing 10s interval): read from Redis → write to Postgres. Replaces the existing absolute-overwrite logic.

---

## Component 3: Budget Sync (Redis INCRBYFLOAT — Sub-Second Eventual Consistency)

### Consistency model (important)

Budgets are **sub-second eventually consistent**, not strongly consistent per-request. This is a deliberate, honest trade-off:

- The current system has ~10s eventual consistency window (one dump cycle per node)
- This design reduces it to sub-second (Redis propagation latency ~1ms)
- What it does NOT guarantee: that no request will exceed the budget. Multiple nodes can pass the check simultaneously before any of them has posted actual cost back to Redis
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
bifrost:budget:{budgetID}:spent → cumulative spend (float64)
```

Every budget has a unique `budgetID` regardless of level. Unified key pattern across all levels.

### Check path (pre-request)

```
GET bifrost:budget:{budgetID}:spent → if >= max_budget → reject
```

Run per `budgetID` from the hierarchy. If any exceeds limit → reject with `BifrostError`, `AllowFallbacks = &false`.

### Write path (post-request, actual cost known)

```go
// Called in PostLLMHook after cost is computed from token usage
for each budgetID in hierarchy:
    INCRBYFLOAT bifrost:budget:{budgetID}:spent {actualCost}
```

Cost is only known after response — there is no pre-deduct/refund pattern. This is the only deduction point.

Covered update functions:
- `UpdateVirtualKeyBudgetUsageInMemory(vk, provider, cost)` → all IDs from `collectBudgetsFromHierarchy`
- `UpdateProviderAndModelBudgetUsageInMemory(model, provider, cost)` → provider budget ID + model budget ID
- `UpdateUserBudgetUsageInMemory(userID, cost)` → user budget ID

### Reset

In `ResetExpiredBudgetsInMemory`, for each budget reaching its period end:

```
SET bifrost:budget:{id}:spent 0 EX {resetDurationSeconds}
```

### Bootstrap (node start / Redis recovery)

See "Redis Recovery — Merging Outage-Period Deltas" below.

### Persist to Postgres

`DumpBudgets` (existing 10s interval): read from Redis → write to Postgres.

### Graceful degradation

Redis unavailable → fall back to `sync.Map` local counters (existing behavior). Budget accuracy reverts to the pre-feature 10s eventual consistency window.

If `cluster.strict_budgets: true` is set in config, Redis unavailability instead triggers a DB-level atomic check:
```sql
UPDATE budgets SET current = current + $cost WHERE current + $cost <= max RETURNING id
```
Strongly consistent but adds ~1-2ms latency per request. Default is `false`.

---

## Redis Recovery — Merging Outage-Period Deltas

This section defines how local usage accumulated during a Redis outage is merged back without data loss when Redis becomes available again.

### The problem

During a Redis outage, nodes fall back to local `sync.Map` counters. When Redis recovers (or Redis data was lost and keys are gone), a naive `SET NX` from Postgres would produce wrong results:

1. Postgres is behind by up to the 10s dump interval
2. Each node has a local delta not yet in Postgres
3. Multiple nodes race to initialize the same Redis keys

The last-writer overwrites the others' contributions, silently undercounting.

### `LastDBUsages*` initialization

`LastDBUsages*` maps must be **initialized from Postgres-loaded values at startup**, not from zero. When the server loads rate limit and budget state from Postgres during startup, it sets:

```
LastDBUsagesRequestsRateLimits[id] = postgres_value
LastDBUsagesTokensRateLimits[id]   = postgres_value
LastDBUsagesBudgets[id]            = postgres_value
```

This is what makes `localDelta = inMemory - LastDBUsages[id]` correct in all cases:

| Scenario | LastDBUsages[id] | inMemory | localDelta |
|----------|-----------------|----------|------------|
| Fresh start, no outage | postgres_baseline | postgres_baseline | 0 ✓ |
| Crash + restart | postgres_baseline (re-init'd) | postgres_baseline | 0 ✓ (no overcounting) |
| Running through outage | last_dump_value | last_dump_value + outage_delta | outage_delta ✓ |

**Guarantee scope:** No usage data loss when a node **remains running** through a Redis outage. If a node crashes while Redis is down, its outage-period usage is lost — bounded to at most 10s of usage (one dump interval) for the crashed instance. This is the explicit OSS guarantee; a durable local WAL is out of scope.

### Recovery procedure (per node, on Redis reconnect)

**Do not force-dump to Postgres before the Lua merge.** If this node dumps first, the Postgres baseline would already include this node's outage delta; adding `localDelta` again via `INCRBY` would double-count it.

```
For each rateLimitID and budgetID tracked locally:

  1. Compute localDelta (before any dump):
       localDelta = inMemoryValue - LastDBUsages[id]
     LastDBUsages* are initialized from Postgres at startup and updated on every
     successful dump. This is exactly this node's outage-period contribution.
     On a fresh start or crash-restart, LastDBUsages[id] = postgres_baseline
     and localDelta = 0 — the node contributes nothing (correct: no outage delta).

  2. Read postgres_baseline directly from Postgres (stale — this is correct):
       postgres_baseline = current value in Postgres for this counter
     This value reflects the last successful dump before or during the outage.
     Do NOT refresh it with a forced dump first.

  3. Run atomic Lua merge script per counter key:
```

```lua
-- KEYS[1] = counter key (e.g. bifrost:rl:{id}:tokens)
-- ARGV[1] = postgres_baseline (stale Postgres value, read in step 2)
-- ARGV[2] = local_delta (this node's outage-period contribution, computed in step 1)
if redis.call('EXISTS', KEYS[1]) == 0 then
    redis.call('SET', KEYS[1], ARGV[1])   -- initialize from Postgres baseline
end
if tonumber(ARGV[2]) > 0 then
    redis.call('INCRBY', KEYS[1], ARGV[2])  -- add this node's outage-period delta
end
return redis.call('GET', KEYS[1])
```

```
  4. Only after ALL counter merges succeed:
     set redisAvailable = true → switch reads/writes to Redis path.
     If any merge fails: stay in degraded local mode (redisAvailable = false),
     retry after backoff. Do not switch to Redis-read path with a partially
     merged state — that would produce wrong check results.

  5. After switching to Redis path, the next periodic DumpRateLimits/DumpBudgets
     will sync the merged Redis values back to Postgres. No manual dump needed.
```

### Why this works with concurrent nodes

Multiple nodes calling the Lua script independently produce a correct result:
- First node: Redis key does not exist → `SET postgres_baseline` → `INCRBY localDelta_A`
- Second node: Redis key exists → skip SET → `INCRBY localDelta_B`
- Final value: `postgres_baseline + localDelta_A + localDelta_B` = cluster-wide usage ✓

`postgres_baseline` is the cluster's last-known-good value before the outage. Each `localDelta` is exactly one node's contribution during the outage. No contribution is double-counted because `LastDBUsages[id]` is initialized from Postgres at startup and updated on every successful dump — it always represents "what Postgres already knows about this node's usage."

### TTL on recovery

Rate limit keys set during recovery use the remaining TTL of the reset window. If no TTL can be determined, set a conservative TTL matching the shortest configured reset duration for that entity.

---

## Component 4: Redis Connection Config

### config.json schema (new optional block)

```json
{
  "cluster": {
    "node_id": "",
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

- `node_id`: stable consumer identity across restarts. Falls back to `os.Hostname()` if empty. Set via `BIFROST_NODE_ID` env var for Kubernetes deployments.
- `strict_budgets`: if `true`, budget checks fall back to DB-atomic on Redis unavailability
- `addr`: single address for standalone mode
- `addrs`: seed node list for cluster mode (overrides `addr` when non-empty)
- `cluster_mode: false` → `redis.NewClient`; `cluster_mode: true` → `redis.NewClusterClient`; `db` ignored in cluster mode
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
        // TLS, timeouts mirroring vectorstore pattern
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

### Redis Streams and INCRBY in Cluster mode

`XADD`/`XREAD` and `INCRBY`/`INCRBYFLOAT` are all single-key operations. Each lands on a deterministic slot; no cross-slot issue. No hash tags needed.

---

## Startup Sequence

```
1. Resolve consumer identity:
   - consumerID = cluster.node_id ?? os.Hostname()
   - nodeID = uuid.New()  (ephemeral, for self-publish filtering only)

2. Connect Postgres → load all config into memory (existing)

3. If cluster.redis configured:
   a. Connect Redis
   b. Run counter recovery merge:
      - For each counter: compute localDelta = inMemory - LastDBUsages[id]
      - Read postgres_baseline from Postgres (stale, no forced dump)
      - Run Lua merge script per counter
      - Only on full success: set redisAvailable = true
      - On any failure: stay in degraded local mode, skip step (c)
   c. If redisAvailable:
      - Read bifrost:consumer:{consumerID}:last_seen from Redis
      - If cursor found: begin catch-up XREAD from that cursor
      - If no cursor (first start) or stream gap detected:
        run watermark-first full reload → server.FullReload(ctx) → XREAD from watermark
      - Start blocking XREAD consumer goroutine

4. Start serving requests
```

---

## Failure Mode Matrix

| Scenario | Behavior |
|----------|----------|
| Redis down at startup | Log warning, start in single-node mode |
| Redis down mid-run | `atomic.Bool redisAvailable = false` → switch all reads/writes to local sync.Map |
| Redis recovers | Run Lua merge (stale Postgres baseline + localDelta, no forced dump); only set `redisAvailable = true` after all merges succeed; if any merge fails → stay in degraded mode; if stream gap → watermark-first full reload via `server.FullReload` |
| Postgres down | Redis serves rate limit + budget checks; config mutations fail (existing behavior) |
| Network partition between nodes | Each node uses local Redis data; converges when partition heals |

Mode switching uses `atomic.Bool redisAvailable` — no restart needed.

---

## Testing Strategy

### Unit tests (mock Redis via `miniredis`)

- `TestStreamPublish_AfterCommit` — event emitted only after `ExecuteTransaction` commits, not on rollback
- `TestStreamPublish_Standalone` — non-transactional write emits event immediately
- `TestStreamConsumer_CatchUp` — consumer resumes from stored cursor, processes missed events in order
- `TestStreamConsumer_FullResync` — stream gap (cursor older than oldest entry) triggers full Postgres reload
- `TestReloadHandler_Idempotent` — same event delivered twice produces identical in-memory state
- `TestDeleteEvent_NoDBRead` — delete event handler does not attempt to fetch entity from DB
- `TestConsumerCursor_Durable` — cursor persisted to `bifrost:consumer:{consumerID}:last_seen`, survives reconnect
- `TestRateLimitINCRBY_Concurrent` — 100 goroutines increment concurrently, verify atomic correctness
- `TestBudgetCheck_SubSecondWindow` — budget reached; subsequent requests see updated Redis counter
- `TestRecoveryMerge_Lua` — verify Lua script: first node initializes, second node adds delta; final = baseline + delta_A + delta_B
- `TestRecoveryMerge_NoDoublCount` — forced dump before merge is NOT done; localDelta = inMemory - LastDBUsages (not from fresh Postgres)
- `TestRecoveryMerge_PartialFailure` — if any counter merge fails, redisAvailable stays false; reads remain on local sync.Map
- `TestFullReload_Idempotent` — calling server.FullReload(ctx) twice produces identical in-memory state
- `TestFullReload_Order` — verify reload order: client config → providers → governance → MCP → plugins (reconciliation)
- `TestFullReload_PluginReconcile_Delete` — plugin in memory but not DB → RemovePlugin called
- `TestFullReload_PluginReconcile_Disabled` — plugin disabled in DB → RemovePlugin called
- `TestLastDBUsages_InitFromPostgres` — after Postgres load, LastDBUsages[id] = postgres_value; localDelta on first recovery merge = 0
- `TestRecoveryMerge_CrashRestart` — node crashes and restarts; LastDBUsages re-init'd from Postgres; localDelta = 0; no overcounting in Redis
- `TestWatermarkFirstReload_NoGap` — events committed after watermark W appear in stream and are caught by XREAD from W

### Integration tests (real Redis + Postgres, added to `make test-plugins`)

- `TestMultiNode_ConfigSync_Upsert` — Node A updates provider → Node B XREAD event → `server.ReloadProvider` called → in-memory updated
- `TestMultiNode_ConfigSync_Delete` — Node A deletes VK → Node B calls `server.RemoveVirtualKey`
- `TestMultiNode_ConfigSync_ReconnectCatchup` — Node B subscriber paused mid-stream → resumes from stored cursor → catches up missed events
- `TestMultiNode_RateLimitClusterWide` — 1000-request limit, 500 requests per node → both hit limit together
- `TestMultiNode_BudgetEventualConsistency` — budget spent across 2 nodes; combined total in Redis reflects sub-second
- `TestMultiNode_RedisRecovery_NoDataLoss` — Redis down during traffic; local deltas accumulated; Redis recovers; Lua merge preserves all counts from all nodes
- `TestMultiNode_StableConsumerID` — simulated restart with same `consumerID`; cursor restored; no missed events

Test file: `plugins/governance/multinode_test.go`
Uses `miniredis` for unit tests; real Redis (via Docker Compose) for integration tests.

---

## Files to Create / Modify

| File | Change |
|------|--------|
| `transports/config.schema.json` | Add `cluster` block including `node_id`, `strict_budgets`, `redis.*` |
| `framework/configstore/cluster_syncer.go` | New: `ClusterSyncer` interface + Redis Streams implementation |
| `framework/configstore/publishing_config_store.go` | New: `PublishingConfigStore` decorator; `ExecuteTransaction` as single publish choke point; `eventAccumulator` context type |
| `framework/configstore/publishing_config_store_test.go` | New: unit tests with miniredis |
| `plugins/governance/store.go` | Update Check functions to read Redis; Update functions to write INCRBY/INCRBYFLOAT to Redis; initialize `LastDBUsages*` from Postgres-loaded values at startup; add Lua recovery merge |
| `plugins/governance/tracker.go` | Update `DumpRateLimits`/`DumpBudgets` to read from Redis; add forced-dump path for recovery |
| `plugins/governance/multinode_test.go` | New: multi-node integration tests |
| `transports/bifrost-http/server/server.go` | Add `FullReload(ctx)` (idempotent, ordered); add XREAD consumer goroutine with watermark-first reload; wire `PublishingConfigStore` at startup; add `ClusterSyncer` and `consumerID` to server; add recovery bootstrap sequence |
| `transports/bifrost-http/lib/config.go` | Accept `PublishingConfigStore` instead of raw `ConfigStore`; remove any remaining manual publish calls |
| `examples/configs/withmultinode/config.json` | New example config |
| `docs/features/multi-node.mdx` | New: user-facing documentation |
