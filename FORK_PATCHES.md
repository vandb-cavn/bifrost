# Fork Patches

Every modification to upstream-owned code in this fork, so an upstream merge is mechanical re-application, not re-derivation. Baseline: `4bbcd5846` (merge-base with upstream).

All *new* files are fork-owned and not listed here:
- `transports/bifrost-http/server/server_cluster.go`
- `transports/bifrost-http/lib/cluster_config.go`
- `plugins/governance/redis_counters.go`
- `plugins/governance/quota_window.go`
- `plugins/governance/quota_window_test.go`
- `plugins/governance/quota_window_redis_test.go`
- `plugins/governance/multinode_test.go`
- `framework/configstore/cluster_syncer.go`
- `framework/configstore/cluster_syncer_test.go`
- `framework/configstore/publishing_config_store.go`
- `framework/configstore/publishing_config_store_test.go`
- `framework/configstore/rdb_fork.go`

---

## Feature: Multi-node cluster sync + calendar-aligned quota windows

### transports/bifrost-http/server/server.go
- **struct `BifrostHTTPServer` (~L148-155)**: Add +6 fields (`clusterSyncer`, `clusterRedisClient`, `clusterCtx`, `clusterCancel`, `clusterEventNodeID`, `fullReloadMu`) to hold cluster runtime handles.
- **bootstrap (~L1344, before LoadPlugins)**: Add call to `s.initClusterPublishing(ctx)` to wrap ConfigStore so governance + handlers use the publishing store.
- **bootstrap (~L1600, after routes)**: Add call to `s.initClusterSubscriberAndRedis(ctx)` because FullReload needs Client + routes.
- **shutdown (~L1655)**: Add cleanup block: `s.clusterCancel()` + `s.clusterRedisClient.Close()`.
  *(All implementation logic lives in same-package file: `server_cluster.go`)*

### transports/bifrost-http/lib/config.go
- **`ConfigData` L162 / `UnmarshalJSON` L241, L260**: Add `Cluster *ClusterConfig` field and JSON unmarshal plumbing.
- **`Config` L334 / `LoadConfig` L677**: Add `Cluster *ClusterConfig` runtime config field and assignments.
- **`GetAvailableProviders()` (~L4282)**: Add helper method to filter enabled non-empty keys (legitimate non-cluster fork helper).
  *(Type defined in: `lib/cluster_config.go`)*

### transports/config.schema.json
- **`properties` block**: Add definition for `oss_cluster_config` to validate the new `cluster` configuration block.

### plugins/governance/store.go
- **`LocalGovernanceStore` (~L58-61)**: Add fields `redisCounters`, `redisAvailable` to gate and run cluster Redis interactions.
- **`BumpBudgetUsage` (~L398)**: Guarded `redisCounters.IncrBudget` call.
- **`BumpRateLimitUsage` (~L449)**: Guarded `redisCounters.IncrTokens/IncrRequests` calls.
- **`ResetExpiredBudgetsInMemory` (~L1402, L1424)**: Call `EvaluateQuotaWindow` and guarded `redisCounters.ResetBudget`.
- **`ResetExpiredRateLimitsInMemory` (~L1453, L1478)**: Call `evaluateQuotaWindowPtr` and `resetRateLimitRedis`.
- **`DumpRateLimits` (~L1583, L1645)**: Guarded Redis counters fetch / update.
- **`DumpBudgets` (~L1689)**: Guarded Redis counters fetch / update.
  *(All implementation logic lives in same-package file: `redis_counters.go` and `quota_window.go`)*

### plugins/governance/tracker.go
- **`PerformStartupResets` (~L196-224)**: Rewritten (102 -> 19 lines) to perform calendar-aligned rate limit and budget resets delegating to store methods.
  *(Implementation helper in: `quota_window.go`)*

### framework/modelcatalog/sync.go
- **`syncPricing` (~L50-75)**: Sort pricing keys and rows deterministically before upserting.
- **`syncModelParameters` (~L462-475)**: Sort model catalog parameters keys before upserting.
  *Why: concurrent multi-node upserts deadlock on PostgreSQL (40P01) without a stable row lock order.*
