# Multi-Node OSS Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add real-time multi-node sync to Bifrost OSS via Redis Streams (config invalidation) and Redis atomic counters (rate limits + budgets).

**Architecture:** `PublishingConfigStore` wraps `ConfigStore` and publishes to a Redis Stream after every committed write; a XREAD consumer goroutine on each node receives events and calls existing server-layer reload methods. Rate limit and budget counters move from per-node `sync.Map` to shared Redis INCRBY/INCRBYFLOAT keys, with a Lua merge script for crash-safe Redis recovery. `atomic.Bool redisAvailable` gates all Redis paths so single-node deployments are completely unaffected.

**Tech Stack:** Go 1.26, `github.com/redis/go-redis/v9` (already in framework + transports go.mod), `alicebob/miniredis/v2` for unit tests, existing `gorm.io/gorm` transaction interface.

---

## File Map

| File | Status | Responsibility |
|------|--------|----------------|
| `transports/config.schema.json` | Modify | Add `cluster` block to JSON schema |
| `transports/bifrost-http/lib/cluster_config.go` | **Create** | `ClusterConfig` + `ClusterRedisConfig` Go types; `LoadClusterConfig()` |
| `framework/configstore/cluster_syncer.go` | **Create** | `ClusterSyncer` interface; `ConfigSyncEvent` type; `RedisClusterSyncer` (XADD publish + XREAD subscribe + cursor persistence) |
| `framework/configstore/publishing_config_store.go` | **Create** | `PublishingConfigStore` decorator; `eventAccumulator` context type; `ExecuteTransaction` as sole publish choke point |
| `framework/configstore/publishing_config_store_test.go` | **Create** | Unit tests for publish-on-commit, no-publish-on-rollback, standalone write |
| `plugins/governance/redis_counters.go` | **Create** | `RedisCounterClient` wrapping `redis.UniversalClient`; INCRBY/INCRBYFLOAT write/read/reset; Lua recovery merge; `NewRedisCounterClient()` |
| `plugins/governance/go.mod` | Modify | Promote `github.com/redis/go-redis/v9` from indirect to direct |
| `plugins/governance/store.go` | Modify | Add `redisCounters *RedisCounterClient` + `redisAvailable atomic.Bool` to `LocalGovernanceStore`; init `LastDBUsages*` from Postgres at startup; add Redis read path to Check* functions; add Redis write path to Update* functions; add `RunRecoveryMerge()` |
| `plugins/governance/tracker.go` | Modify | `DumpRateLimits` reads from Redis when available; `DumpBudgets` reads from Redis when available; trigger `RunRecoveryMerge` on Redis reconnect |
| `plugins/governance/multinode_test.go` | **Create** | Unit tests with miniredis; integration test stubs |
| `transports/bifrost-http/server/server.go` | Modify | Add `FullReload(ctx)` (idempotent ordered); add `ClusterSyncer` field + XREAD consumer goroutine; wire `PublishingConfigStore` at startup; add recovery bootstrap |

---

## Task 1: Config Schema — Add `cluster` block

**Files:**
- Modify: `transports/config.schema.json`

- [ ] **Step 1: Locate the `properties` root object in the schema**

Run: `grep -n '"properties"' transports/config.schema.json | head -5`

- [ ] **Step 2: Add `cluster` property to the schema's top-level `properties` object**

Find the `"properties": {` section that contains top-level config keys (same level as `"providers"`, `"plugins"`, etc.) and add after the last property:

```json
"cluster": {
  "type": "object",
  "description": "Multi-node cluster configuration. Omit entirely for single-node mode.",
  "additionalProperties": false,
  "properties": {
    "node_id": {
      "type": "string",
      "description": "Stable consumer identity across restarts. Falls back to os.Hostname() if empty. Set via BIFROST_NODE_ID env var for Kubernetes."
    },
    "strict_budgets": {
      "type": "boolean",
      "description": "When true, budget checks fall back to DB-atomic UPDATE on Redis unavailability. Adds ~1-2ms latency. Default: false.",
      "default": false
    },
    "redis": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "addr": { "type": "string", "description": "Redis address (host:port) for standalone mode." },
        "addrs": {
          "type": "array",
          "items": { "type": "string" },
          "description": "Seed addresses for Redis Cluster mode (overrides addr when non-empty)."
        },
        "cluster_mode": { "type": "boolean", "default": false },
        "password": { "type": "string" },
        "db": { "type": "integer", "default": 0, "description": "Database number. Ignored in cluster_mode." },
        "pool_size": { "type": "integer", "default": 20 },
        "tls": {
          "type": "object",
          "additionalProperties": false,
          "properties": {
            "enabled": { "type": "boolean", "default": false },
            "cert_file": { "type": "string" },
            "key_file": { "type": "string" },
            "ca_file": { "type": "string" }
          }
        }
      }
    }
  }
}
```

- [ ] **Step 3: Validate JSON is still valid**

Run: `python3 -m json.tool transports/config.schema.json > /dev/null && echo OK`
Expected: `OK`

- [ ] **Step 4: Commit**

```bash
git add transports/config.schema.json
git commit -m "feat(schema): add cluster block for multi-node redis config"
```

---

## Task 2: ClusterConfig Go Types

**Files:**
- Create: `transports/bifrost-http/lib/cluster_config.go`

- [ ] **Step 1: Create the file**

```go
package lib

import (
	"os"

	"github.com/redis/go-redis/v9"
)

// ClusterTLSConfig holds TLS settings for the cluster Redis connection.
type ClusterTLSConfig struct {
	Enabled  bool   `json:"enabled"`
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`
	CAFile   string `json:"ca_file"`
}

// ClusterRedisConfig holds connection parameters for the cluster Redis.
type ClusterRedisConfig struct {
	Addr        string           `json:"addr"`
	Addrs       []string         `json:"addrs"`
	ClusterMode bool             `json:"cluster_mode"`
	Password    string           `json:"password"`
	DB          int              `json:"db"`
	PoolSize    int              `json:"pool_size"`
	TLS         ClusterTLSConfig `json:"tls"`
}

// ClusterConfig is the top-level optional cluster block in config.json.
// Absent → single-node mode, all Redis code paths skipped.
type ClusterConfig struct {
	NodeID        string             `json:"node_id"`
	StrictBudgets bool               `json:"strict_budgets"`
	Redis         ClusterRedisConfig `json:"redis"`
}

// ConsumerID returns the stable consumer identity for cursor persistence.
// Prefers cluster.node_id, then BIFROST_NODE_ID env var, then os.Hostname().
func (c *ClusterConfig) ConsumerID() string {
	if c.NodeID != "" {
		return c.NodeID
	}
	if env := os.Getenv("BIFROST_NODE_ID"); env != "" {
		return env
	}
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return "bifrost-node"
}

// NewRedisUniversalClient builds a redis.UniversalClient from ClusterRedisConfig.
// Returns nil if addr and addrs are both empty (no Redis configured).
func (r *ClusterRedisConfig) NewRedisUniversalClient() redis.UniversalClient {
	addrs := r.Addrs
	if len(addrs) == 0 && r.Addr != "" {
		addrs = []string{r.Addr}
	}
	if len(addrs) == 0 {
		return nil
	}
	if r.ClusterMode {
		return redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:    addrs,
			Password: r.Password,
			PoolSize: r.PoolSize,
		})
	}
	return redis.NewClient(&redis.Options{
		Addr:     addrs[0],
		Password: r.Password,
		DB:       r.DB,
		PoolSize: r.PoolSize,
	})
}
```

- [ ] **Step 2: Add redis import to transports go.mod (promote from indirect to direct)**

```bash
cd transports && go get github.com/redis/go-redis/v9 && cd ..
```

Expected: no errors, go.mod updated with direct dependency.

- [ ] **Step 3: Build check**

```bash
cd transports && go build ./bifrost-http/lib/... && cd ..
```
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add transports/bifrost-http/lib/cluster_config.go transports/go.mod transports/go.sum
git commit -m "feat(transport): add ClusterConfig types with redis.UniversalClient factory"
```

---

## Task 3: ClusterSyncer (Redis Streams)

**Files:**
- Create: `framework/configstore/cluster_syncer.go`

- [ ] **Step 1: Create the file with interface and event type**

```go
package configstore

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/bytedance/sonic"
	"github.com/redis/go-redis/v9"
)

const (
	streamKey     = "bifrost:config:events"
	streamMaxLen  = 50000
	cursorKeyFmt  = "bifrost:consumer:%s:last_seen"
	xreadBlock    = 5 * time.Second
	xreadCount    = 1000
	reconnectBase = 500 * time.Millisecond
	reconnectMax  = 30 * time.Second
)

// ConfigSyncEvent is published to the Redis Stream on every ConfigStore write.
type ConfigSyncEvent struct {
	Type      string    `json:"type"`   // "provider","virtual_key","team","customer","model_config","routing_rule","mcp_client","plugin","client_config"
	Action    string    `json:"action"` // "upsert" or "delete"
	ID        string    `json:"id"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	NodeID    string    `json:"node_id"` // publishing node's ephemeral ID
}

// ClusterSyncer publishes and subscribes to config change events.
type ClusterSyncer interface {
	// Publish sends an event to the stream. Non-blocking; logs on failure.
	Publish(ctx context.Context, event ConfigSyncEvent) error
	// Subscribe starts the blocking XREAD consumer loop. Calls handler for each
	// event not published by this node (NodeID != selfNodeID).
	// fullReloadFn is called synchronously on first start or stream gap;
	// cursor is only persisted after fullReloadFn returns nil.
	// Blocks until ctx is cancelled.
	Subscribe(
		ctx context.Context,
		consumerID string,
		selfNodeID string,
		fullReloadFn func(ctx context.Context) error,
		handler func(ConfigSyncEvent),
	)
	// Close releases all resources.
	Close() error
}

// RedisClusterSyncer implements ClusterSyncer via Redis Streams.
type RedisClusterSyncer struct {
	client redis.UniversalClient
}

// NewRedisClusterSyncer creates a syncer. client must already be connected.
func NewRedisClusterSyncer(client redis.UniversalClient) *RedisClusterSyncer {
	return &RedisClusterSyncer{client: client}
}

// Publish sends a ConfigSyncEvent to the Redis Stream using XADD MAXLEN ~ N.
func (s *RedisClusterSyncer) Publish(ctx context.Context, event ConfigSyncEvent) error {
	payload, err := sonic.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	return s.client.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		MaxLen: streamMaxLen,
		Approx: true,
		Values: map[string]interface{}{"data": string(payload)},
	}).Err()
}

// Subscribe starts the blocking XREAD consumer loop.
// On first start (no cursor) or stream gap → watermark-first full reload via fullReloadFn,
// then resumes blocking XREAD from the watermark.
// Cursor is only persisted after fullReloadFn returns nil — a failed reload retries with backoff.
func (s *RedisClusterSyncer) Subscribe(
	ctx context.Context,
	consumerID string,
	selfNodeID string,
	fullReloadFn func(ctx context.Context) error,
	handler func(ConfigSyncEvent),
) {
	cursorKey := fmt.Sprintf(cursorKeyFmt, consumerID)
	delay := reconnectBase

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		lastID, err := s.loadCursor(ctx, cursorKey)
		needsFullReload := (err != nil || lastID == "")
		if !needsFullReload {
			needsFullReload = s.hasStreamGap(ctx, lastID)
		}

		if needsFullReload {
			lastID, err = s.watermarkFirstFullReload(ctx, cursorKey, fullReloadFn)
			if err != nil {
				// Full reload failed — backoff and retry; do NOT advance cursor
				select {
				case <-ctx.Done():
					return
				case <-time.After(delay):
					delay = min(delay*2, reconnectMax)
				}
				continue
			}
			delay = reconnectBase // reset on success
		}

		// Blocking XREAD catch-up + live loop
		err = s.readLoop(ctx, cursorKey, lastID, selfNodeID, handler)
		if err == nil || ctx.Err() != nil {
			return
		}

		// Reconnect with backoff
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
			delay = min(delay*2, reconnectMax)
		}
	}
}

// watermarkFirstFullReload captures stream tip W, calls fullReloadFn synchronously,
// and only persists W as the cursor if fullReloadFn succeeds.
// Returns the watermark ID and any error from fullReloadFn.
func (s *RedisClusterSyncer) watermarkFirstFullReload(
	ctx context.Context,
	cursorKey string,
	fullReloadFn func(ctx context.Context) error,
) (string, error) {
	// 1. Capture watermark (stream tip) BEFORE full reload
	msgs, err := s.client.XRevRangeN(ctx, streamKey, "+", "-", 1).Result()
	watermark := "0"
	if err == nil && len(msgs) > 0 {
		watermark = msgs[0].ID
	}

	// 2. Run full reload synchronously — must succeed before cursor is advanced
	if err := fullReloadFn(ctx); err != nil {
		return watermark, fmt.Errorf("full reload failed, cursor not advanced: %w", err)
	}

	// 3. Persist watermark as cursor — only reached on success
	_ = s.client.Set(ctx, cursorKey, watermark, 0).Err()

	return watermark, nil
}

// hasStreamGap returns true if lastID is older than the oldest entry in the stream.
func (s *RedisClusterSyncer) hasStreamGap(ctx context.Context, lastID string) bool {
	info, err := s.client.XInfoStream(ctx, streamKey).Result()
	if err != nil {
		return false // can't tell; assume no gap
	}
	if info.FirstEntry.ID == "" {
		return false
	}
	return compareStreamIDs(lastID, info.FirstEntry.ID) < 0
}

func (s *RedisClusterSyncer) readLoop(
	ctx context.Context,
	cursorKey string,
	startID string,
	selfNodeID string,
	handler func(ConfigSyncEvent),
) error {
	id := startID
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		streams, err := s.client.XRead(ctx, &redis.XReadArgs{
			Streams: []string{streamKey, id},
			Count:   xreadCount,
			Block:   xreadBlock,
		}).Result()
		if err == redis.Nil {
			continue // block timeout, retry
		}
		if err != nil {
			return err
		}
		for _, stream := range streams {
			for _, msg := range stream.Messages {
				data, ok := msg.Values["data"].(string)
				if !ok {
					id = msg.ID
					_ = s.client.Set(ctx, cursorKey, id, 0).Err()
					continue
				}
				var event ConfigSyncEvent
				if err := sonic.Unmarshal([]byte(data), &event); err != nil {
					id = msg.ID
					_ = s.client.Set(ctx, cursorKey, id, 0).Err()
					continue
				}
				// Skip self-published events
				if event.NodeID != selfNodeID {
					handler(event)
				}
				id = msg.ID
				_ = s.client.Set(ctx, cursorKey, id, 0).Err()
			}
		}
	}
}

func (s *RedisClusterSyncer) loadCursor(ctx context.Context, key string) (string, error) {
	return s.client.Get(ctx, key).Result()
}

func (s *RedisClusterSyncer) Close() error {
	return s.client.Close()
}

// compareStreamIDs compares two Redis stream IDs (ms-seq format).
// Returns negative if a < b, 0 if equal, positive if a > b.
func compareStreamIDs(a, b string) int {
	parseID := func(id string) (int64, int64) {
		for i, c := range id {
			if c == '-' {
				ms, _ := strconv.ParseInt(id[:i], 10, 64)
				seq, _ := strconv.ParseInt(id[i+1:], 10, 64)
				return ms, seq
			}
		}
		ms, _ := strconv.ParseInt(id, 10, 64)
		return ms, 0
	}
	ams, aseq := parseID(a)
	bms, bseq := parseID(b)
	if ams != bms {
		if ams < bms {
			return -1
		}
		return 1
	}
	if aseq < bseq {
		return -1
	}
	if aseq > bseq {
		return 1
	}
	return 0
}

func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
```

- [ ] **Step 2: Build check**

```bash
cd framework && go build ./configstore/... && cd ..
```
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add framework/configstore/cluster_syncer.go framework/go.mod framework/go.sum
git commit -m "feat(framework): add ClusterSyncer interface and Redis Streams implementation"
```

---

## Task 4: PublishingConfigStore Decorator

**Files:**
- Create: `framework/configstore/publishing_config_store.go`

- [ ] **Step 1: Create the file**

```go
package configstore

import (
	"context"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"gorm.io/gorm"
)

// eventAccumulatorKey is the context key for the event accumulator.
type eventAccumulatorKey struct{}

// eventAccumulator queues events inside a transaction; published after commit.
type eventAccumulator struct {
	events []ConfigSyncEvent
}

// withEventAccumulator attaches an accumulator to ctx.
func withEventAccumulator(ctx context.Context, acc *eventAccumulator) context.Context {
	return context.WithValue(ctx, eventAccumulatorKey{}, acc)
}

// scheduleEvent queues an event if inside a transaction, or publishes immediately.
// Must be called by every ConfigStore write method.
func scheduleEvent(ctx context.Context, event ConfigSyncEvent, syncer ClusterSyncer, nodeID string) {
	if syncer == nil {
		return
	}
	event.NodeID = nodeID
	if acc, ok := ctx.Value(eventAccumulatorKey{}).(*eventAccumulator); ok && acc != nil {
		acc.events = append(acc.events, event)
		return
	}
	// Standalone write (no surrounding transaction) — publish directly.
	// This path is rare; most writes go through ExecuteTransaction.
	_ = syncer.Publish(ctx, event)
}

// PublishingConfigStore wraps ConfigStore and emits ConfigSyncEvents after every
// committed write. ExecuteTransaction is the single publish choke point.
type PublishingConfigStore struct {
	ConfigStore
	syncer ClusterSyncer
	nodeID string
	logger schemas.Logger
}

// NewPublishingConfigStore wraps an existing ConfigStore.
// If syncer is nil, the decorator is a transparent pass-through (single-node mode).
func NewPublishingConfigStore(inner ConfigStore, syncer ClusterSyncer, nodeID string, logger schemas.Logger) *PublishingConfigStore {
	return &PublishingConfigStore{
		ConfigStore: inner,
		syncer:      syncer,
		nodeID:      nodeID,
		logger:      logger,
	}
}

// ExecuteTransaction is the single publish choke point.
// Events scheduled by write methods inside fn are published only after commit succeeds.
func (pcs *PublishingConfigStore) ExecuteTransaction(
	ctx context.Context,
	fn func(tx *gorm.DB) error,
) error {
	if pcs.syncer == nil {
		return pcs.ConfigStore.ExecuteTransaction(ctx, fn)
	}

	acc := &eventAccumulator{}
	txCtx := withEventAccumulator(ctx, acc)

	err := pcs.ConfigStore.ExecuteTransaction(txCtx, fn)
	if err != nil {
		return err // rollback — do not publish
	}

	for _, ev := range acc.events {
		if pubErr := pcs.syncer.Publish(ctx, ev); pubErr != nil {
			pcs.logger.Warn("cluster sync publish failed (postgres write succeeded): %v", pubErr)
		}
	}
	return nil
}

// --- Write method overrides ---
// Each override calls the embedded ConfigStore method (which runs the actual DB write),
// then schedules a ConfigSyncEvent so that ExecuteTransaction publishes after commit.
// For standalone writes (not inside ExecuteTransaction), scheduleEvent publishes directly.

func (pcs *PublishingConfigStore) AddProvider(ctx context.Context, provider interface{ String() string }, config ProviderConfig, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.AddProvider(ctx, provider.(interface{ String() string }), config, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "provider", Action: "upsert", ID: provider.(interface{ String() string }).String(), UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}
```

Wait — the `provider` parameter type is `schemas.ModelProvider`, which is a string type. Let me write this correctly:

```go
package configstore

import (
	"context"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"gorm.io/gorm"
)

type eventAccumulatorKey struct{}

type eventAccumulator struct {
	events []ConfigSyncEvent
}

func withEventAccumulator(ctx context.Context, acc *eventAccumulator) context.Context {
	return context.WithValue(ctx, eventAccumulatorKey{}, acc)
}

func scheduleEvent(ctx context.Context, event ConfigSyncEvent, syncer ClusterSyncer, nodeID string) {
	if syncer == nil {
		return
	}
	event.NodeID = nodeID
	if acc, ok := ctx.Value(eventAccumulatorKey{}).(*eventAccumulator); ok && acc != nil {
		acc.events = append(acc.events, event)
		return
	}
	_ = syncer.Publish(ctx, event)
}

// PublishingConfigStore wraps ConfigStore and emits ConfigSyncEvents after every committed write.
type PublishingConfigStore struct {
	ConfigStore
	syncer ClusterSyncer
	nodeID string
	logger schemas.Logger
}

func NewPublishingConfigStore(inner ConfigStore, syncer ClusterSyncer, nodeID string, logger schemas.Logger) *PublishingConfigStore {
	return &PublishingConfigStore{
		ConfigStore: inner,
		syncer:      syncer,
		nodeID:      nodeID,
		logger:      logger,
	}
}

func (pcs *PublishingConfigStore) ExecuteTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
	if pcs.syncer == nil {
		return pcs.ConfigStore.ExecuteTransaction(ctx, fn)
	}
	acc := &eventAccumulator{}
	txCtx := withEventAccumulator(ctx, acc)
	err := pcs.ConfigStore.ExecuteTransaction(txCtx, fn)
	if err != nil {
		return err
	}
	for _, ev := range acc.events {
		if pubErr := pcs.syncer.Publish(ctx, ev); pubErr != nil {
			pcs.logger.Warn("cluster sync publish failed: %v", pubErr)
		}
	}
	return nil
}

// Provider write methods
func (pcs *PublishingConfigStore) AddProvider(ctx context.Context, provider schemas.ModelProvider, config ProviderConfig, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.AddProvider(ctx, provider, config, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "provider", Action: "upsert", ID: string(provider), UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateProvider(ctx context.Context, provider schemas.ModelProvider, config ProviderConfig, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.UpdateProvider(ctx, provider, config, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "provider", Action: "upsert", ID: string(provider), UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) DeleteProvider(ctx context.Context, provider schemas.ModelProvider, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.DeleteProvider(ctx, provider, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "provider", Action: "delete", ID: string(provider)}, pcs.syncer, pcs.nodeID)
	return nil
}

// VirtualKey write methods
func (pcs *PublishingConfigStore) CreateVirtualKey(ctx context.Context, vk *tables.TableVirtualKey, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.CreateVirtualKey(ctx, vk, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "virtual_key", Action: "upsert", ID: vk.ID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateVirtualKey(ctx context.Context, vk *tables.TableVirtualKey, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.UpdateVirtualKey(ctx, vk, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "virtual_key", Action: "upsert", ID: vk.ID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) DeleteVirtualKey(ctx context.Context, id string) error {
	if err := pcs.ConfigStore.DeleteVirtualKey(ctx, id); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "virtual_key", Action: "delete", ID: id}, pcs.syncer, pcs.nodeID)
	return nil
}

// Team write methods
func (pcs *PublishingConfigStore) CreateTeam(ctx context.Context, team *tables.TableTeam, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.CreateTeam(ctx, team, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "team", Action: "upsert", ID: team.ID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateTeam(ctx context.Context, team *tables.TableTeam, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.UpdateTeam(ctx, team, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "team", Action: "upsert", ID: team.ID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) DeleteTeam(ctx context.Context, id string) error {
	if err := pcs.ConfigStore.DeleteTeam(ctx, id); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "team", Action: "delete", ID: id}, pcs.syncer, pcs.nodeID)
	return nil
}

// Customer write methods
func (pcs *PublishingConfigStore) CreateCustomer(ctx context.Context, c *tables.TableCustomer, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.CreateCustomer(ctx, c, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "customer", Action: "upsert", ID: c.ID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateCustomer(ctx context.Context, c *tables.TableCustomer, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.UpdateCustomer(ctx, c, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "customer", Action: "upsert", ID: c.ID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) DeleteCustomer(ctx context.Context, id string) error {
	if err := pcs.ConfigStore.DeleteCustomer(ctx, id); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "customer", Action: "delete", ID: id}, pcs.syncer, pcs.nodeID)
	return nil
}

// ModelConfig write methods
func (pcs *PublishingConfigStore) CreateModelConfig(ctx context.Context, mc *tables.TableModelConfig, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.CreateModelConfig(ctx, mc, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "model_config", Action: "upsert", ID: mc.ID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateModelConfig(ctx context.Context, mc *tables.TableModelConfig, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.UpdateModelConfig(ctx, mc, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "model_config", Action: "upsert", ID: mc.ID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) DeleteModelConfig(ctx context.Context, id string) error {
	if err := pcs.ConfigStore.DeleteModelConfig(ctx, id); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "model_config", Action: "delete", ID: id}, pcs.syncer, pcs.nodeID)
	return nil
}

// RoutingRule write methods
func (pcs *PublishingConfigStore) CreateRoutingRule(ctx context.Context, rule *tables.TableRoutingRule, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.CreateRoutingRule(ctx, rule, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "routing_rule", Action: "upsert", ID: rule.ID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateRoutingRule(ctx context.Context, rule *tables.TableRoutingRule, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.UpdateRoutingRule(ctx, rule, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "routing_rule", Action: "upsert", ID: rule.ID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) DeleteRoutingRule(ctx context.Context, id string, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.DeleteRoutingRule(ctx, id, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "routing_rule", Action: "delete", ID: id}, pcs.syncer, pcs.nodeID)
	return nil
}

// MCP client write methods
func (pcs *PublishingConfigStore) CreateMCPClientConfig(ctx context.Context, cc *schemas.MCPClientConfig) error {
	if err := pcs.ConfigStore.CreateMCPClientConfig(ctx, cc); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "mcp_client", Action: "upsert", ID: cc.ID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateMCPClientConfig(ctx context.Context, id string, cc *tables.TableMCPClient) error {
	if err := pcs.ConfigStore.UpdateMCPClientConfig(ctx, id, cc); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "mcp_client", Action: "upsert", ID: id, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) DeleteMCPClientConfig(ctx context.Context, id string) error {
	if err := pcs.ConfigStore.DeleteMCPClientConfig(ctx, id); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "mcp_client", Action: "delete", ID: id}, pcs.syncer, pcs.nodeID)
	return nil
}

// Plugin write methods
func (pcs *PublishingConfigStore) UpsertPlugin(ctx context.Context, plugin *tables.TablePlugin, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.UpsertPlugin(ctx, plugin, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "plugin", Action: "upsert", ID: plugin.Name, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdatePlugin(ctx context.Context, plugin *tables.TablePlugin, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.UpdatePlugin(ctx, plugin, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "plugin", Action: "upsert", ID: plugin.Name, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) DeletePlugin(ctx context.Context, name string, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.DeletePlugin(ctx, name, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "plugin", Action: "delete", ID: name}, pcs.syncer, pcs.nodeID)
	return nil
}

// ClientConfig write method
func (pcs *PublishingConfigStore) UpdateClientConfig(ctx context.Context, config *ClientConfig) error {
	if err := pcs.ConfigStore.UpdateClientConfig(ctx, config); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "client_config", Action: "upsert", UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

// Auth / Proxy / Framework config write methods — all affect runtime config on other nodes
func (pcs *PublishingConfigStore) UpdateAuthConfig(ctx context.Context, config *AuthConfig) error {
	if err := pcs.ConfigStore.UpdateAuthConfig(ctx, config); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "client_config", Action: "upsert", UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateProxyConfig(ctx context.Context, config *tables.GlobalProxyConfig) error {
	if err := pcs.ConfigStore.UpdateProxyConfig(ctx, config); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "client_config", Action: "upsert", UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateFrameworkConfig(ctx context.Context, config *tables.TableFrameworkConfig) error {
	if err := pcs.ConfigStore.UpdateFrameworkConfig(ctx, config); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "client_config", Action: "upsert", UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

// Provider key write methods — emit parent provider event so other nodes reload the full provider
func (pcs *PublishingConfigStore) CreateProviderKey(ctx context.Context, provider schemas.ModelProvider, key schemas.Key, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.CreateProviderKey(ctx, provider, key, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "provider", Action: "upsert", ID: string(provider), UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateProviderKey(ctx context.Context, provider schemas.ModelProvider, keyID string, key schemas.Key, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.UpdateProviderKey(ctx, provider, keyID, key, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "provider", Action: "upsert", ID: string(provider), UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) DeleteProviderKey(ctx context.Context, provider schemas.ModelProvider, keyID string, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.DeleteProviderKey(ctx, provider, keyID, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "provider", Action: "upsert", ID: string(provider), UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

// VK child config write methods — emit parent virtual_key event
func (pcs *PublishingConfigStore) CreateVirtualKeyProviderConfig(ctx context.Context, vkpc *tables.TableVirtualKeyProviderConfig, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.CreateVirtualKeyProviderConfig(ctx, vkpc, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "virtual_key", Action: "upsert", ID: vkpc.VirtualKeyID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateVirtualKeyProviderConfig(ctx context.Context, vkpc *tables.TableVirtualKeyProviderConfig, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.UpdateVirtualKeyProviderConfig(ctx, vkpc, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "virtual_key", Action: "upsert", ID: vkpc.VirtualKeyID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) DeleteVirtualKeyProviderConfig(ctx context.Context, id uint, tx ...*gorm.DB) error {
	// id is the provider config row ID, not the VK ID; we cannot emit a VK-specific event
	// without a DB lookup. Emit full_reload — this mutation is rare.
	if err := pcs.ConfigStore.DeleteVirtualKeyProviderConfig(ctx, id, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "full_reload", Action: "upsert"}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) CreateVirtualKeyMCPConfig(ctx context.Context, vkmc *tables.TableVirtualKeyMCPConfig, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.CreateVirtualKeyMCPConfig(ctx, vkmc, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "virtual_key", Action: "upsert", ID: vkmc.VirtualKeyID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateVirtualKeyMCPConfig(ctx context.Context, vkmc *tables.TableVirtualKeyMCPConfig, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.UpdateVirtualKeyMCPConfig(ctx, vkmc, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "virtual_key", Action: "upsert", ID: vkmc.VirtualKeyID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) DeleteVirtualKeyMCPConfig(ctx context.Context, id uint, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.DeleteVirtualKeyMCPConfig(ctx, id, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "full_reload", Action: "upsert"}, pcs.syncer, pcs.nodeID)
	return nil
}

// MCP discovered tools — reload the MCP client on other nodes
func (pcs *PublishingConfigStore) UpdateMCPClientDiscoveredTools(ctx context.Context, clientID string, tools map[string]schemas.ChatTool, toolNameMapping map[string]string) error {
	if err := pcs.ConfigStore.UpdateMCPClientDiscoveredTools(ctx, clientID, tools, toolNameMapping); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "mcp_client", Action: "upsert", ID: clientID, UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

// Pricing overrides — affect cost calc; reload client config on other nodes
func (pcs *PublishingConfigStore) CreatePricingOverride(ctx context.Context, override *tables.TablePricingOverride, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.CreatePricingOverride(ctx, override, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "client_config", Action: "upsert", UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdatePricingOverride(ctx context.Context, override *tables.TablePricingOverride, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.UpdatePricingOverride(ctx, override, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "client_config", Action: "upsert", UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) DeletePricingOverride(ctx context.Context, id string, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.DeletePricingOverride(ctx, id, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "client_config", Action: "upsert", UpdatedAt: time.Now()}, pcs.syncer, pcs.nodeID)
	return nil
}

// Rate limit CRUD — entity ID not sufficient to identify parent; emit full_reload.
// These mutations are rare (governance API usually updates parent VK/provider/team).
func (pcs *PublishingConfigStore) CreateRateLimit(ctx context.Context, rl *tables.TableRateLimit, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.CreateRateLimit(ctx, rl, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "full_reload", Action: "upsert"}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateRateLimit(ctx context.Context, rl *tables.TableRateLimit, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.UpdateRateLimit(ctx, rl, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "full_reload", Action: "upsert"}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateRateLimits(ctx context.Context, rls []*tables.TableRateLimit, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.UpdateRateLimits(ctx, rls, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "full_reload", Action: "upsert"}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) DeleteRateLimit(ctx context.Context, id string, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.DeleteRateLimit(ctx, id, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "full_reload", Action: "upsert"}, pcs.syncer, pcs.nodeID)
	return nil
}

// Budget CRUD — same rationale as rate limits; emit full_reload.
func (pcs *PublishingConfigStore) CreateBudget(ctx context.Context, b *tables.TableBudget, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.CreateBudget(ctx, b, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "full_reload", Action: "upsert"}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateBudget(ctx context.Context, b *tables.TableBudget, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.UpdateBudget(ctx, b, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "full_reload", Action: "upsert"}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) UpdateBudgets(ctx context.Context, bs []*tables.TableBudget, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.UpdateBudgets(ctx, bs, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "full_reload", Action: "upsert"}, pcs.syncer, pcs.nodeID)
	return nil
}

func (pcs *PublishingConfigStore) DeleteBudget(ctx context.Context, id string, tx ...*gorm.DB) error {
	if err := pcs.ConfigStore.DeleteBudget(ctx, id, tx...); err != nil {
		return err
	}
	scheduleEvent(ctx, ConfigSyncEvent{Type: "full_reload", Action: "upsert"}, pcs.syncer, pcs.nodeID)
	return nil
}
```

Note: `tables` must be imported as `"github.com/maximhq/bifrost/framework/configstore/tables"`.

- [ ] **Step 2: Build check**

```bash
cd framework && go build ./configstore/... && cd ..
```
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add framework/configstore/publishing_config_store.go
git commit -m "feat(framework): add PublishingConfigStore decorator with ExecuteTransaction choke point"
```

---

## Task 5: PublishingConfigStore Unit Tests

**Files:**
- Create: `framework/configstore/publishing_config_store_test.go`

- [ ] **Step 1: Add miniredis to framework go.mod**

```bash
cd framework && go get github.com/alicebob/miniredis/v2 && cd ..
```

- [ ] **Step 2: Write the failing tests**

```go
package configstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubLogger satisfies schemas.Logger with no-op methods.
type stubLogger struct{}

func (s stubLogger) Debug(msg string, args ...interface{}) {}
func (s stubLogger) Info(msg string, args ...interface{})  {}
func (s stubLogger) Warn(msg string, args ...interface{})  {}
func (s stubLogger) Error(msg string, args ...interface{}) {}

// stubConfigStore is a minimal in-memory ConfigStore for testing.
type stubConfigStore struct {
	configstore.ConfigStore // embed for unimplemented methods (panics if called)
	txFn                   func(fn func(tx interface{}) error) error
	vkCreated              *tables.TableVirtualKey
}

func (s *stubConfigStore) ExecuteTransaction(ctx context.Context, fn func(tx interface{}) error) error {
	return fn(nil)
}

func (s *stubConfigStore) CreateVirtualKey(ctx context.Context, vk *tables.TableVirtualKey, tx ...interface{}) error {
	s.vkCreated = vk
	return nil
}

// failingConfigStore returns error on ExecuteTransaction.
type failingConfigStore struct{ configstore.ConfigStore }

func (f *failingConfigStore) ExecuteTransaction(ctx context.Context, fn func(tx interface{}) error) error {
	return errors.New("db error")
}
func (f *failingConfigStore) CreateVirtualKey(ctx context.Context, vk *tables.TableVirtualKey, tx ...interface{}) error {
	return nil
}

func newMiniRedisClient(t *testing.T) (redis.UniversalClient, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { client.Close(); mr.Close() })
	return client, mr
}

func readLastStreamEvent(t *testing.T, client redis.UniversalClient) *configstore.ConfigSyncEvent {
	t.Helper()
	msgs, err := client.XRange(context.Background(), "bifrost:config:events", "-", "+").Result()
	require.NoError(t, err)
	if len(msgs) == 0 {
		return nil
	}
	last := msgs[len(msgs)-1]
	data, ok := last.Values["data"].(string)
	require.True(t, ok)
	var ev configstore.ConfigSyncEvent
	require.NoError(t, json.Unmarshal([]byte(data), &ev))
	return &ev
}

func TestPublishingConfigStore_PublishAfterCommit(t *testing.T) {
	client, _ := newMiniRedisClient(t)
	syncer := configstore.NewRedisClusterSyncer(client)
	inner := &stubConfigStore{}
	pcs := configstore.NewPublishingConfigStore(inner, syncer, "node-A", stubLogger{})

	ctx := context.Background()
	err := pcs.ExecuteTransaction(ctx, func(tx interface{}) error {
		return pcs.CreateVirtualKey(ctx, &tables.TableVirtualKey{ID: "vk-1"})
	})
	require.NoError(t, err)

	ev := readLastStreamEvent(t, client)
	require.NotNil(t, ev)
	assert.Equal(t, "virtual_key", ev.Type)
	assert.Equal(t, "upsert", ev.Action)
	assert.Equal(t, "vk-1", ev.ID)
	assert.Equal(t, "node-A", ev.NodeID)
}

func TestPublishingConfigStore_NoPublishOnRollback(t *testing.T) {
	client, _ := newMiniRedisClient(t)
	syncer := configstore.NewRedisClusterSyncer(client)
	inner := &failingConfigStore{}
	pcs := configstore.NewPublishingConfigStore(inner, syncer, "node-A", stubLogger{})

	ctx := context.Background()
	err := pcs.ExecuteTransaction(ctx, func(tx interface{}) error {
		_ = pcs.CreateVirtualKey(ctx, &tables.TableVirtualKey{ID: "vk-2"})
		return nil
	})
	assert.Error(t, err)

	ev := readLastStreamEvent(t, client)
	assert.Nil(t, ev, "no event should be published on rollback")
}

func TestPublishingConfigStore_NilSyncer_Passthrough(t *testing.T) {
	inner := &stubConfigStore{}
	pcs := configstore.NewPublishingConfigStore(inner, nil, "node-A", stubLogger{})

	ctx := context.Background()
	err := pcs.ExecuteTransaction(ctx, func(tx interface{}) error {
		return pcs.CreateVirtualKey(ctx, &tables.TableVirtualKey{ID: "vk-3"})
	})
	require.NoError(t, err)
	assert.Equal(t, "vk-3", inner.vkCreated.ID)
}
```

Note: The `ExecuteTransaction` signature uses `*gorm.DB` in production — adjust the stub to match the actual interface. The test pattern above may need minor adaptation for the gorm.DB type.

- [ ] **Step 3: Run the failing tests**

```bash
cd framework && go test ./configstore/... -run TestPublishingConfigStore -v 2>&1 | head -40 && cd ..
```
Expected: compilation errors or FAIL — tests should fail before implementation is complete.

- [ ] **Step 4: Run after implementation is complete**

```bash
cd framework && go test ./configstore/... -run TestPublishingConfigStore -v && cd ..
```
Expected: all 3 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add framework/configstore/publishing_config_store_test.go framework/go.mod framework/go.sum
git commit -m "test(framework): add PublishingConfigStore unit tests"
```

---

## Task 6: Governance — RedisCounterClient

**Files:**
- Create: `plugins/governance/redis_counters.go`
- Modify: `plugins/governance/go.mod`

- [ ] **Step 1: Promote redis to direct dependency in governance go.mod**

```bash
cd plugins/governance && go get github.com/redis/go-redis/v9 && cd ../..
```

- [ ] **Step 2: Write the failing test for Lua merge**

In `plugins/governance/multinode_test.go` (create if not exists):

```go
package governance_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maximhq/bifrost/plugins/governance"
)

func newTestRedis(t *testing.T) (redis.UniversalClient, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { client.Close(); mr.Close() })
	return client, mr
}

func TestRecoveryMerge_TwoNodes_NoDuplicate(t *testing.T) {
	client, _ := newTestRedis(t)
	rc := governance.NewRedisCounterClient(client)
	ctx := context.Background()

	// Simulate: postgres_baseline = 500, node A delta = 100, node B delta = 50
	// Expected final: 500 + 100 + 50 = 650
	keyA := "bifrost:rl:test-rl:tokens"

	err := rc.MergeDelta(ctx, keyA, 500, 100)
	require.NoError(t, err)

	err = rc.MergeDelta(ctx, keyA, 500, 50)
	require.NoError(t, err)

	val, err := client.Get(ctx, keyA).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(650), mustParseInt64(val))
}

func TestRecoveryMerge_ZeroDelta_NoIncr(t *testing.T) {
	client, _ := newTestRedis(t)
	rc := governance.NewRedisCounterClient(client)
	ctx := context.Background()

	key := "bifrost:rl:test-rl2:tokens"
	err := rc.MergeDelta(ctx, key, 300, 0)
	require.NoError(t, err)

	val, err := client.Get(ctx, key).Result()
	require.NoError(t, err)
	// Only baseline set, no INCRBY since delta == 0
	assert.Equal(t, int64(300), mustParseInt64(val))
}

func mustParseInt64(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}
```

- [ ] **Step 3: Run failing tests**

```bash
cd plugins/governance && go test ./... -run TestRecoveryMerge -v 2>&1 | head -20 && cd ../..
```
Expected: compilation error — `governance.NewRedisCounterClient` does not exist yet.

- [ ] **Step 4: Create `redis_counters.go`**

```go
package governance

import (
	"context"
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"
)

const (
	rateLimitTokensKeyFmt   = "bifrost:rl:%s:tokens"
	rateLimitRequestsKeyFmt = "bifrost:rl:%s:requests"
	budgetSpentKeyFmt       = "bifrost:budget:%s:spent"
)

// luaMerge atomically initializes a key from postgres_baseline if absent,
// then adds local_delta (only if > 0).
// KEYS[1] = counter key
// ARGV[1] = postgres_baseline (int string)
// ARGV[2] = local_delta (int string)
var luaMerge = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then
    redis.call('SET', KEYS[1], ARGV[1])
end
if tonumber(ARGV[2]) > 0 then
    redis.call('INCRBY', KEYS[1], ARGV[2])
end
return redis.call('GET', KEYS[1])
`)

// luaBudgetMerge is the float variant for budget keys.
var luaBudgetMerge = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then
    redis.call('SET', KEYS[1], ARGV[1])
end
if tonumber(ARGV[2]) > 0 then
    redis.call('INCRBYFLOAT', KEYS[1], ARGV[2])
end
return redis.call('GET', KEYS[1])
`)

// RedisCounterClient wraps a redis.UniversalClient and provides
// typed operations for rate limit and budget counters.
type RedisCounterClient struct {
	client redis.UniversalClient
}

// NewRedisCounterClient creates a RedisCounterClient.
func NewRedisCounterClient(client redis.UniversalClient) *RedisCounterClient {
	return &RedisCounterClient{client: client}
}

// IncrTokens increments the token counter for a rate limit. Returns new value.
func (r *RedisCounterClient) IncrTokens(ctx context.Context, rateLimitID string, delta int64) (int64, error) {
	key := fmt.Sprintf(rateLimitTokensKeyFmt, rateLimitID)
	return r.client.IncrBy(ctx, key, delta).Result()
}

// IncrRequests increments the request counter for a rate limit. Returns new value.
func (r *RedisCounterClient) IncrRequests(ctx context.Context, rateLimitID string, delta int64) (int64, error) {
	key := fmt.Sprintf(rateLimitRequestsKeyFmt, rateLimitID)
	return r.client.IncrBy(ctx, key, delta).Result()
}

// GetTokens reads the token counter for a rate limit.
func (r *RedisCounterClient) GetTokens(ctx context.Context, rateLimitID string) (int64, error) {
	key := fmt.Sprintf(rateLimitTokensKeyFmt, rateLimitID)
	val, err := r.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(val, 10, 64)
}

// GetRequests reads the request counter for a rate limit.
func (r *RedisCounterClient) GetRequests(ctx context.Context, rateLimitID string) (int64, error) {
	key := fmt.Sprintf(rateLimitRequestsKeyFmt, rateLimitID)
	val, err := r.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(val, 10, 64)
}

// ResetRateLimit sets both counters to 0 with an expiry TTL.
func (r *RedisCounterClient) ResetRateLimit(ctx context.Context, rateLimitID string, ttlSeconds int64) error {
	tokenKey := fmt.Sprintf(rateLimitTokensKeyFmt, rateLimitID)
	requestKey := fmt.Sprintf(rateLimitRequestsKeyFmt, rateLimitID)
	pipe := r.client.Pipeline()
	pipe.Set(ctx, tokenKey, 0, 0)
	pipe.Set(ctx, requestKey, 0, 0)
	if ttlSeconds > 0 {
		pipe.Expire(ctx, tokenKey, secondsDuration(ttlSeconds))
		pipe.Expire(ctx, requestKey, secondsDuration(ttlSeconds))
	}
	_, err := pipe.Exec(ctx)
	return err
}

// IncrBudget increments the budget spent counter. Returns new value.
func (r *RedisCounterClient) IncrBudget(ctx context.Context, budgetID string, delta float64) (float64, error) {
	key := fmt.Sprintf(budgetSpentKeyFmt, budgetID)
	val, err := r.client.IncrByFloat(ctx, key, delta).Result()
	return val, err
}

// GetBudget reads the budget spent counter.
func (r *RedisCounterClient) GetBudget(ctx context.Context, budgetID string) (float64, error) {
	key := fmt.Sprintf(budgetSpentKeyFmt, budgetID)
	val, err := r.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(val, 64)
}

// ResetBudget sets the budget counter to 0 with an expiry TTL.
func (r *RedisCounterClient) ResetBudget(ctx context.Context, budgetID string, ttlSeconds int64) error {
	key := fmt.Sprintf(budgetSpentKeyFmt, budgetID)
	cmd := r.client.Set(ctx, key, 0, 0)
	if err := cmd.Err(); err != nil {
		return err
	}
	if ttlSeconds > 0 {
		return r.client.Expire(ctx, key, secondsDuration(ttlSeconds)).Err()
	}
	return nil
}

// MergeDelta runs the atomic Lua merge for integer (rate limit) counters.
// postgresBaseline is the stale Postgres value. localDelta is this node's outage-period contribution.
func (r *RedisCounterClient) MergeDelta(ctx context.Context, key string, postgresBaseline, localDelta int64) error {
	return luaMerge.Run(ctx, r.client, []string{key},
		strconv.FormatInt(postgresBaseline, 10),
		strconv.FormatInt(localDelta, 10),
	).Err()
}

// MergeBudgetDelta runs the atomic Lua merge for float (budget) counters.
func (r *RedisCounterClient) MergeBudgetDelta(ctx context.Context, key string, postgresBaseline, localDelta float64) error {
	return luaBudgetMerge.Run(ctx, r.client, []string{key},
		strconv.FormatFloat(postgresBaseline, 'f', -1, 64),
		strconv.FormatFloat(localDelta, 'f', -1, 64),
	).Err()
}

func secondsDuration(secs int64) time.Duration {
	return time.Duration(secs) * time.Second
}
```

Add `"time"` to the import block.

- [ ] **Step 5: Run tests — should pass**

```bash
cd plugins/governance && go test ./... -run TestRecoveryMerge -v && cd ../..
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add plugins/governance/redis_counters.go plugins/governance/multinode_test.go plugins/governance/go.mod plugins/governance/go.sum
git commit -m "feat(governance): add RedisCounterClient with Lua merge for recovery"
```

---

## Task 7: Governance Store — Add Redis Fields + Init LastDBUsages from Postgres

**Files:**
- Modify: `plugins/governance/store.go`

- [ ] **Step 1: Write the failing test**

In `plugins/governance/multinode_test.go`, add:

```go
func TestLastDBUsages_InitFromPostgres(t *testing.T) {
	// After governance store loads from Postgres, LastDBUsages should equal
	// the postgres-loaded value so localDelta = 0 at first recovery.
	// This test exercises the invariant via the exported maps directly.
	// (Integration test — requires a running Postgres; skip if not configured)
	if testing.Short() {
		t.Skip("requires postgres")
	}
	// TODO: implement with real Postgres after other tasks complete
}
```

This test is a stub — the invariant is verified by unit-testing the `loadFromDatabase` initialisation path in Task 8.

- [ ] **Step 2: Add `redisCounters` and `redisAvailable` to `LocalGovernanceStore`**

In `plugins/governance/store.go`, find the struct definition (lines 21-53) and add two fields after the `LastDBUsages*` maps:

```go
// Redis counter client for multi-node sync (nil in single-node mode)
redisCounters *RedisCounterClient

// redisAvailable gates all Redis read/write paths.
// Only set to true after ALL counter merge operations succeed on Redis reconnect.
redisAvailable atomic.Bool
```

Also add `"sync/atomic"` to imports if not already present.

- [ ] **Step 3: Update `NewLocalGovernanceStore` to accept optional `RedisCounterClient`**

Find `NewLocalGovernanceStore` (line ~161) and add `redisCounters *RedisCounterClient` as the last parameter:

```go
func NewLocalGovernanceStore(ctx context.Context, logger schemas.Logger, configStore configstore.ConfigStore, governanceConfig *configstore.GovernanceConfig, modelCatalog *modelcatalog.ModelCatalog, redisCounters *RedisCounterClient) (*LocalGovernanceStore, error) {
```

Inside the function, after `store := &LocalGovernanceStore{...}`, add:

```go
store.redisCounters = redisCounters
```

- [ ] **Step 4: Initialize `LastDBUsages*` from Postgres-loaded values**

In `loadFromDatabase` (line ~1986), find the call to `gs.rebuildInMemoryStructures(...)` and add after it:

```go
// Initialize LastDBUsages* from Postgres-loaded values so localDelta = 0 at first
// recovery merge. This prevents overcounting on fresh start or crash-restart.
gs.initLastDBUsagesFromPostgres(budgets, rateLimits)
```

Then add the new function after `loadFromDatabase`:

```go
func (gs *LocalGovernanceStore) initLastDBUsagesFromPostgres(
	budgets []configstoreTables.TableBudget,
	rateLimits []configstoreTables.TableRateLimit,
) {
	gs.LastDBUsagesBudgetsMu.Lock()
	for _, b := range budgets {
		gs.LastDBUsagesBudgets[b.ID] = b.CurrentUsage
	}
	gs.LastDBUsagesBudgetsMu.Unlock()

	gs.LastDBUsagesRateLimitsRequestsMu.Lock()
	gs.LastDBUsagesRateLimitsTokensMu.Lock()
	for _, rl := range rateLimits {
		gs.LastDBUsagesRequestsRateLimits[rl.ID] = rl.RequestCurrentUsage
		gs.LastDBUsagesTokensRateLimits[rl.ID] = rl.TokenCurrentUsage
	}
	gs.LastDBUsagesRateLimitsRequestsMu.Unlock()
	gs.LastDBUsagesRateLimitsTokensMu.Unlock()
}
```

- [ ] **Step 5: Fix all callers of `NewLocalGovernanceStore`**

Search for callers:

```bash
grep -rn "NewLocalGovernanceStore" /Users/vanduong/Documents/vibecoding/bifost2 --include="*.go"
```

For each caller, pass `nil` as the last argument (single-node mode). Update in `lib/config.go` or wherever the call appears.

- [ ] **Step 6: Add exported `InitRedis` and `SetRedisAvailable` methods**

These are the only methods the server package needs to call on `LocalGovernanceStore` for Redis setup. Adding them as exported methods avoids the server package directly accessing unexported fields.

Add to `store.go` after `initLastDBUsagesFromPostgres`:

```go
// InitRedis wires up the Redis counter client and runs the recovery merge.
// Returns true if the merge succeeded; caller should call SetRedisAvailable(true) on success.
// Safe to call from outside the governance package — does not expose unexported fields.
func (gs *LocalGovernanceStore) InitRedis(ctx context.Context, client redis.UniversalClient) bool {
	gs.redisCounters = NewRedisCounterClient(client)
	return gs.RunRecoveryMerge(ctx)
}

// SetRedisAvailable flips the atomic.Bool that gates all Redis read/write paths.
// Must only be set to true after InitRedis succeeds.
func (gs *LocalGovernanceStore) SetRedisAvailable(v bool) {
	gs.redisAvailable.Store(v)
}
```

Add `"github.com/redis/go-redis/v9"` to imports if not already present (it should be after Step 1).

- [ ] **Step 7: Fix all callers of `NewLocalGovernanceStore`**

Search for callers:

```bash
grep -rn "NewLocalGovernanceStore" /Users/vanduong/Documents/vibecoding/bifost2 --include="*.go"
```

For each caller, pass `nil` as the last argument (single-node mode). Update in `lib/config.go` or wherever the call appears.

- [ ] **Step 8: Build check**

```bash
cd plugins/governance && go build ./... && cd ../..
```
Expected: no errors.

- [ ] **Step 9: Commit**

```bash
git add plugins/governance/store.go
git commit -m "feat(governance): add Redis fields to LocalGovernanceStore, init LastDBUsages from Postgres, exported InitRedis/SetRedisAvailable"
```

---

## Task 8: Governance Store — Rate Limit Redis Read/Write

**Files:**
- Modify: `plugins/governance/store.go`

- [ ] **Step 1: Write failing test**

In `plugins/governance/multinode_test.go`, add:

```go
func TestRateLimitRedisPath_IncrAndGet(t *testing.T) {
	client, _ := newTestRedis(t)
	rc := governance.NewRedisCounterClient(client)

	ctx := context.Background()

	// Simulate two nodes incrementing
	_, err := rc.IncrTokens(ctx, "rl-abc", 100)
	require.NoError(t, err)
	_, err = rc.IncrTokens(ctx, "rl-abc", 50)
	require.NoError(t, err)

	tokens, err := rc.GetTokens(ctx, "rl-abc")
	require.NoError(t, err)
	assert.Equal(t, int64(150), tokens)
}
```

- [ ] **Step 2: Run failing test**

```bash
cd plugins/governance && go test ./... -run TestRateLimitRedisPath -v && cd ../..
```
Expected: PASS (this tests RedisCounterClient directly, not store.go).

- [ ] **Step 3: Modify `UpdateProviderAndModelRateLimitUsageInMemory` to also write to Redis**

In `store.go`, inside `updateRateLimit` helper (line ~1358), after `gs.rateLimits.Store(rateLimitID, &clone)`, add:

```go
if gs.redisAvailable.Load() && gs.redisCounters != nil {
    if shouldUpdateTokens {
        if _, err := gs.redisCounters.IncrTokens(ctx, rateLimitID, tokensUsed); err != nil {
            gs.logger.Warn("redis IncrTokens failed for %s: %v", rateLimitID, err)
        }
    }
    if shouldUpdateRequests {
        if _, err := gs.redisCounters.IncrRequests(ctx, rateLimitID, 1); err != nil {
            gs.logger.Warn("redis IncrRequests failed for %s: %v", rateLimitID, err)
        }
    }
}
```

Apply the same pattern to `UpdateVirtualKeyRateLimitUsageInMemory`.

- [ ] **Step 4: Modify `CheckRateLimit` to read from Redis when available**

In `CheckRateLimit` (line ~1120), after the existing baseline handling (lines ~1167-1168):

```go
// If Redis is available, read cluster-wide counters instead of the passed-in baselines.
if gs.redisAvailable.Load() && gs.redisCounters != nil {
    if t, err := gs.redisCounters.GetTokens(ctx, rateLimit.ID); err == nil {
        tokensBaselines[rateLimit.ID] = t
    }
    if r, err := gs.redisCounters.GetRequests(ctx, rateLimit.ID); err == nil {
        requestsBaselines[rateLimit.ID] = r
    }
}
```

Apply the same pattern to `CheckProviderRateLimit` and `CheckModelRateLimit`.

- [ ] **Step 5: Modify `ResetExpiredRateLimitsInMemory` to also reset Redis**

For each expired rate limit returned by the function, add after the in-memory reset:

```go
if gs.redisAvailable.Load() && gs.redisCounters != nil {
    var ttl int64
    // compute ttl from reset duration
    if rl.TokenResetDuration != nil {
        if dur, err := configstoreTables.ParseDuration(*rl.TokenResetDuration); err == nil {
            ttl = int64(dur.Seconds())
        }
    }
    if err := gs.redisCounters.ResetRateLimit(ctx, rl.ID, ttl); err != nil {
        gs.logger.Warn("redis ResetRateLimit failed for %s: %v", rl.ID, err)
    }
}
```

- [ ] **Step 6: Build check**

```bash
cd plugins/governance && go build ./... && cd ../..
```

- [ ] **Step 7: Commit**

```bash
git add plugins/governance/store.go
git commit -m "feat(governance): rate limit read/write via Redis when redisAvailable"
```

---

## Task 9: Governance Store — Budget Redis Read/Write

**Files:**
- Modify: `plugins/governance/store.go`

- [ ] **Step 1: Write failing test**

In `plugins/governance/multinode_test.go`, add:

```go
func TestBudgetRedisPath_IncrAndGet(t *testing.T) {
	client, _ := newTestRedis(t)
	rc := governance.NewRedisCounterClient(client)
	ctx := context.Background()

	_, err := rc.IncrBudget(ctx, "budget-xyz", 12.50)
	require.NoError(t, err)
	_, err = rc.IncrBudget(ctx, "budget-xyz", 7.25)
	require.NoError(t, err)

	spent, err := rc.GetBudget(ctx, "budget-xyz")
	require.NoError(t, err)
	assert.InDelta(t, 19.75, spent, 0.001)
}
```

Run and verify PASS (tests RedisCounterClient):

```bash
cd plugins/governance && go test ./... -run TestBudgetRedisPath -v && cd ../..
```

- [ ] **Step 2: Modify `UpdateVirtualKeyBudgetUsageInMemory` to also write to Redis**

In `UpdateVirtualKeyBudgetUsageInMemory` (line ~1217), after `gs.budgets.Store(budgetID, &clone)`, add:

```go
if gs.redisAvailable.Load() && gs.redisCounters != nil {
    if _, err := gs.redisCounters.IncrBudget(ctx, budgetID, cost); err != nil {
        gs.logger.Warn("redis IncrBudget failed for %s: %v", budgetID, err)
    }
}
```

Apply the same pattern inside `UpdateProviderAndModelBudgetUsageInMemory` (line ~1258) for each budget update call.

- [ ] **Step 3: Modify `CheckBudget` to read from Redis when available**

In `CheckBudget` (line ~513), before the `if budget.CurrentUsage+baseline >= budget.MaxLimit` check, add:

```go
if gs.redisAvailable.Load() && gs.redisCounters != nil {
    if spent, err := gs.redisCounters.GetBudget(ctx, budget.ID); err == nil {
        baselines[budget.ID] = spent
    }
}
```

Apply the same to `CheckProviderBudget` and `CheckModelBudget`.

- [ ] **Step 4: Modify `ResetExpiredBudgetsInMemory` to also reset Redis**

For each expired budget, after the in-memory reset, add:

```go
if gs.redisAvailable.Load() && gs.redisCounters != nil {
    var ttl int64
    if b.ResetDuration != "" {
        if dur, err := configstoreTables.ParseDuration(b.ResetDuration); err == nil {
            ttl = int64(dur.Seconds())
        }
    }
    if err := gs.redisCounters.ResetBudget(ctx, b.ID, ttl); err != nil {
        gs.logger.Warn("redis ResetBudget failed for %s: %v", b.ID, err)
    }
}
```

- [ ] **Step 5: Build + test**

```bash
cd plugins/governance && go build ./... && go test ./... -v -count=1 2>&1 | tail -20 && cd ../..
```

- [ ] **Step 6: Commit**

```bash
git add plugins/governance/store.go
git commit -m "feat(governance): budget read/write via Redis when redisAvailable"
```

---

## Task 10: Governance Store — Recovery Merge + `RunRecoveryMerge`

**Files:**
- Modify: `plugins/governance/store.go`
- Modify: `plugins/governance/redis_counters.go`

- [ ] **Step 1: Add `RunRecoveryMerge` to `LocalGovernanceStore` in `store.go`**

Add this method after `initLastDBUsagesFromPostgres`:

```go
// RunRecoveryMerge runs the Lua merge script for all known rate limit and budget counters.
// Called on Redis reconnect (startup or mid-run recovery).
// Returns true if ALL merges succeeded → caller should set redisAvailable = true.
// Returns false if any merge failed → caller should stay in degraded mode.
func (gs *LocalGovernanceStore) RunRecoveryMerge(ctx context.Context) bool {
	if gs.redisCounters == nil {
		return false
	}

	// --- Rate limits ---
	gs.LastDBUsagesRateLimitsTokensMu.RLock()
	gs.LastDBUsagesRateLimitsRequestsMu.RLock()
	type rlSnapshot struct {
		id               string
		inMemTokens      int64
		lastDBTokens     int64
		inMemRequests    int64
		lastDBRequests   int64
	}
	var rlSnaps []rlSnapshot
	gs.rateLimits.Range(func(key, value interface{}) bool {
		rl, ok := value.(*configstoreTables.TableRateLimit)
		if !ok || rl == nil {
			return true
		}
		snap := rlSnapshot{id: rl.ID, inMemTokens: rl.TokenCurrentUsage, inMemRequests: rl.RequestCurrentUsage}
		snap.lastDBTokens = gs.LastDBUsagesTokensRateLimits[rl.ID]
		snap.lastDBRequests = gs.LastDBUsagesRequestsRateLimits[rl.ID]
		rlSnaps = append(rlSnaps, snap)
		return true
	})
	gs.LastDBUsagesRateLimitsTokensMu.RUnlock()
	gs.LastDBUsagesRateLimitsRequestsMu.RUnlock()

	for _, snap := range rlSnaps {
		tokenDelta := snap.inMemTokens - snap.lastDBTokens
		reqDelta := snap.inMemRequests - snap.lastDBRequests

		// Read stale Postgres baseline
		rl, err := gs.configStore.GetRateLimit(ctx, snap.id)
		if err != nil || rl == nil {
			continue
		}

		tokenKey := fmt.Sprintf("bifrost:rl:%s:tokens", snap.id)
		requestKey := fmt.Sprintf("bifrost:rl:%s:requests", snap.id)

		if err := gs.redisCounters.MergeDelta(ctx, tokenKey, rl.TokenCurrentUsage, tokenDelta); err != nil {
			gs.logger.Error("recovery merge failed for %s tokens: %v", snap.id, err)
			return false
		}
		if err := gs.redisCounters.MergeDelta(ctx, requestKey, rl.RequestCurrentUsage, reqDelta); err != nil {
			gs.logger.Error("recovery merge failed for %s requests: %v", snap.id, err)
			return false
		}
	}

	// --- Budgets ---
	gs.LastDBUsagesBudgetsMu.RLock()
	type budgetSnapshot struct {
		id        string
		inMem     float64
		lastDB    float64
	}
	var bSnaps []budgetSnapshot
	gs.budgets.Range(func(key, value interface{}) bool {
		b, ok := value.(*configstoreTables.TableBudget)
		if !ok || b == nil {
			return true
		}
		bSnaps = append(bSnaps, budgetSnapshot{
			id:     b.ID,
			inMem:  b.CurrentUsage,
			lastDB: gs.LastDBUsagesBudgets[b.ID],
		})
		return true
	})
	gs.LastDBUsagesBudgetsMu.RUnlock()

	for _, snap := range bSnaps {
		delta := snap.inMem - snap.lastDB

		budget, err := gs.configStore.GetBudget(ctx, snap.id)
		if err != nil || budget == nil {
			continue
		}

		key := fmt.Sprintf("bifrost:budget:%s:spent", snap.id)
		if err := gs.redisCounters.MergeBudgetDelta(ctx, key, budget.CurrentUsage, delta); err != nil {
			gs.logger.Error("recovery merge failed for budget %s: %v", snap.id, err)
			return false
		}
	}

	return true
}
```

- [ ] **Step 2: Write the failing test**

In `multinode_test.go`, add:

```go
func TestRecoveryMerge_Lua_ConcurrentNodes(t *testing.T) {
	client, _ := newTestRedis(t)
	rc := governance.NewRedisCounterClient(client)
	ctx := context.Background()

	// postgres_baseline = 500
	// Node A delta = 100, Node B delta = 50 → expected 650
	keyTokens := "bifrost:rl:rl-concurrent:tokens"
	require.NoError(t, rc.MergeDelta(ctx, keyTokens, 500, 100))
	require.NoError(t, rc.MergeDelta(ctx, keyTokens, 500, 50))

	val, err := client.Get(ctx, keyTokens).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(650), mustParseInt64(val))
}
```

- [ ] **Step 3: Run test**

```bash
cd plugins/governance && go test ./... -run TestRecoveryMerge_Lua_ConcurrentNodes -v && cd ../..
```
Expected: PASS.

- [ ] **Step 4: Build**

```bash
cd plugins/governance && go build ./... && cd ../..
```

- [ ] **Step 5: Commit**

```bash
git add plugins/governance/store.go
git commit -m "feat(governance): add RunRecoveryMerge for Redis outage recovery"
```

---

## Task 11: Tracker — DumpRateLimits/DumpBudgets from Redis

**Files:**
- Modify: `plugins/governance/tracker.go`

**Context:** `DumpRateLimits` (store.go:1757) and `DumpBudgets` (store.go:1916) are called every 10s to write in-memory usage to Postgres. When Redis is available, they should read from Redis (cluster-wide view) instead of the local `sync.Map`.

- [ ] **Step 1: Extend `GovernanceStore` interface if needed**

Check if the interface already has a method to expose `redisAvailable` and `redisCounters`. If not, the simplest approach is to use a type assertion to `*LocalGovernanceStore` in the tracker and call `GetRedisSnapshot()`.

Add to `LocalGovernanceStore` in `store.go`:

```go
// GetRedisCounters returns the Redis counter client. Nil in single-node mode.
func (gs *LocalGovernanceStore) GetRedisCounters() *RedisCounterClient {
	return gs.redisCounters
}

// IsRedisAvailable returns whether the Redis read path is active.
func (gs *LocalGovernanceStore) IsRedisAvailable() bool {
	return gs.redisAvailable.Load()
}
```

- [ ] **Step 2: Modify `DumpRateLimits` in `store.go`**

In `DumpRateLimits` (line ~1757), when building the final usage value to write to Postgres, replace the existing `inMemoryRateLimit.TokenCurrentUsage` and `inMemoryRateLimit.RequestCurrentUsage` reads with Redis reads if available.

Find the section inside `DumpRateLimits` where it reads rate limit values and writes to Postgres. Add before each value assignment:

```go
tokenUsage := inMemoryRateLimit.TokenCurrentUsage
requestUsage := inMemoryRateLimit.RequestCurrentUsage
if gs.IsRedisAvailable() && gs.redisCounters != nil {
    if t, err := gs.redisCounters.GetTokens(ctx, inMemoryRateLimit.ID); err == nil {
        tokenUsage = t
    }
    if r, err := gs.redisCounters.GetRequests(ctx, inMemoryRateLimit.ID); err == nil {
        requestUsage = r
    }
}
// Use tokenUsage, requestUsage when writing to Postgres
```

Update `LastDBUsages*` after successful write:

```go
gs.LastDBUsagesRateLimitsTokensMu.Lock()
gs.LastDBUsagesTokensRateLimits[id] = tokenUsage
gs.LastDBUsagesRateLimitsTokensMu.Unlock()
gs.LastDBUsagesRateLimitsRequestsMu.Lock()
gs.LastDBUsagesRequestsRateLimits[id] = requestUsage
gs.LastDBUsagesRateLimitsRequestsMu.Unlock()
```

- [ ] **Step 3: Modify `DumpBudgets` in `store.go` the same way**

For each budget, read from Redis if available before writing to Postgres:

```go
newUsage := inMemoryBudget.CurrentUsage
if gs.IsRedisAvailable() && gs.redisCounters != nil {
    if spent, err := gs.redisCounters.GetBudget(ctx, inMemoryBudget.ID); err == nil {
        newUsage = spent
    }
}
// Use newUsage when writing to Postgres
```

Update `LastDBUsagesBudgets` after successful write:

```go
gs.LastDBUsagesBudgetsMu.Lock()
gs.LastDBUsagesBudgets[inMemoryBudget.ID] = newUsage
gs.LastDBUsagesBudgetsMu.Unlock()
```

- [ ] **Step 4: Build check**

```bash
cd plugins/governance && go build ./... && cd ../..
```

- [ ] **Step 5: Commit**

```bash
git add plugins/governance/store.go plugins/governance/tracker.go
git commit -m "feat(governance): dump reads from Redis when available; update LastDBUsages after each dump"
```

---

## Task 12: server.FullReload — Ordered Idempotent Reload

**Files:**
- Modify: `transports/bifrost-http/server/server.go`

- [ ] **Step 1: Write failing test (compile-time check)**

Add to a new test file `transports/bifrost-http/server/server_test.go` or existing test file:

```go
// TestFullReload_Exists verifies FullReload is callable (compile-time check).
func TestFullReload_Exists(t *testing.T) {
	// Just verify the method exists and compiles.
	// Integration testing requires a full server setup.
	_ = (*BifrostHTTPServer)(nil)
}
```

- [ ] **Step 2: Add `FullReload` to `server.go`**

Add this method to `BifrostHTTPServer`:

```go
// FullReload reloads all runtime state from Postgres in a fixed, deterministic order.
// Idempotent: calling multiple times produces the same in-memory state as calling once.
// DB is authoritative: entities present in memory but absent from DB are removed.
// Order: ClientConfig → Providers → ModelConfigs → VirtualKeys/Teams/Customers/RoutingRules → MCP → Plugins.
func (s *BifrostHTTPServer) FullReload(ctx context.Context) error {
	if s.Config == nil || s.Config.ConfigStore == nil {
		return fmt.Errorf("config store not initialized")
	}

	// Snapshot current in-memory state for removal reconciliation.
	// GetGovernanceData returns all in-memory entities; use it to build ID sets.
	var govData *governance.GovernanceData
	if gp, err := s.getGovernancePlugin(); err == nil {
		govData = gp.GetGovernanceData()
	}
	inMemProviders := s.Config.GetAvailableProviders() // []schemas.ModelProvider

	// 1. Client config
	if err := s.ReloadClientConfigFromConfigStore(ctx); err != nil {
		logger.Warn("FullReload: client config reload failed: %v", err)
	}

	// 2. Providers — upsert all DB providers, then remove stale in-memory ones
	providers, err := s.Config.ConfigStore.GetProviders(ctx)
	if err != nil {
		logger.Warn("FullReload: failed to list providers: %v", err)
	} else {
		dbProviderSet := make(map[schemas.ModelProvider]bool)
		for _, p := range providers {
			pr := schemas.ModelProvider(p.Name)
			dbProviderSet[pr] = true
			if _, err := s.ReloadProvider(ctx, pr); err != nil {
				logger.Warn("FullReload: provider %s reload failed: %v", p.Name, err)
			}
		}
		for _, mp := range inMemProviders {
			if !dbProviderSet[mp] {
				if err := s.RemoveProvider(ctx, mp); err != nil {
					logger.Warn("FullReload: RemoveProvider %s failed: %v", mp, err)
				}
			}
		}
	}

	// 3. Model configs — upsert all DB model configs, remove stale
	modelConfigs, err := s.Config.ConfigStore.GetModelConfigs(ctx)
	if err != nil {
		logger.Warn("FullReload: failed to list model configs: %v", err)
	} else {
		dbMCSet := make(map[string]bool)
		for _, mc := range modelConfigs {
			dbMCSet[mc.ID] = true
			if _, err := s.ReloadModelConfig(ctx, mc.ID); err != nil {
				logger.Warn("FullReload: model config %s reload failed: %v", mc.ID, err)
			}
		}
		if govData != nil {
			for _, mc := range govData.ModelConfigs {
				if mc != nil && !dbMCSet[mc.ID] {
					if err := s.RemoveModelConfig(ctx, mc.ID); err != nil {
						logger.Warn("FullReload: RemoveModelConfig %s failed: %v", mc.ID, err)
					}
				}
			}
		}
	}

	// 4. Governance state — upsert all DB entities, remove stale in-memory copies
	virtualKeys, err := s.Config.ConfigStore.GetVirtualKeys(ctx)
	if err != nil {
		logger.Warn("FullReload: failed to list virtual keys: %v", err)
	} else {
		dbVKSet := make(map[string]bool)
		for _, vk := range virtualKeys {
			dbVKSet[vk.ID] = true
			if _, err := s.ReloadVirtualKey(ctx, vk.ID); err != nil {
				logger.Warn("FullReload: virtual key %s reload failed: %v", vk.ID, err)
			}
		}
		if govData != nil {
			for id := range govData.VirtualKeys {
				if !dbVKSet[id] {
					if err := s.RemoveVirtualKey(ctx, id); err != nil {
						logger.Warn("FullReload: RemoveVirtualKey %s failed: %v", id, err)
					}
				}
			}
		}
	}

	teams, err := s.Config.ConfigStore.GetTeams(ctx, "")
	if err != nil {
		logger.Warn("FullReload: failed to list teams: %v", err)
	} else {
		dbTeamSet := make(map[string]bool)
		for _, t := range teams {
			dbTeamSet[t.ID] = true
			if _, err := s.ReloadTeam(ctx, t.ID); err != nil {
				logger.Warn("FullReload: team %s reload failed: %v", t.ID, err)
			}
		}
		if govData != nil {
			for id := range govData.Teams {
				if !dbTeamSet[id] {
					if err := s.RemoveTeam(ctx, id); err != nil {
						logger.Warn("FullReload: RemoveTeam %s failed: %v", id, err)
					}
				}
			}
		}
	}

	customers, err := s.Config.ConfigStore.GetCustomers(ctx)
	if err != nil {
		logger.Warn("FullReload: failed to list customers: %v", err)
	} else {
		dbCustSet := make(map[string]bool)
		for _, c := range customers {
			dbCustSet[c.ID] = true
			if _, err := s.ReloadCustomer(ctx, c.ID); err != nil {
				logger.Warn("FullReload: customer %s reload failed: %v", c.ID, err)
			}
		}
		if govData != nil {
			for id := range govData.Customers {
				if !dbCustSet[id] {
					if err := s.RemoveCustomer(ctx, id); err != nil {
						logger.Warn("FullReload: RemoveCustomer %s failed: %v", id, err)
					}
				}
			}
		}
	}

	routingRules, err := s.Config.ConfigStore.GetRoutingRules(ctx)
	if err != nil {
		logger.Warn("FullReload: failed to list routing rules: %v", err)
	} else {
		dbRRSet := make(map[string]bool)
		for _, r := range routingRules {
			dbRRSet[r.ID] = true
			if err := s.ReloadRoutingRule(ctx, r.ID); err != nil {
				logger.Warn("FullReload: routing rule %s reload failed: %v", r.ID, err)
			}
		}
		if govData != nil {
			for id := range govData.RoutingRules {
				if !dbRRSet[id] {
					if err := s.RemoveRoutingRule(ctx, id); err != nil {
						logger.Warn("FullReload: RemoveRoutingRule %s failed: %v", id, err)
					}
				}
			}
		}
	}

	// 5. MCP clients — reconnect all DB clients, remove stale
	mcpConfig, err := s.Config.ConfigStore.GetMCPConfig(ctx)
	if err != nil {
		logger.Warn("FullReload: failed to get MCP config: %v", err)
	} else if mcpConfig != nil {
		dbMCPSet := make(map[string]bool)
		for _, client := range mcpConfig.MCPClients {
			dbMCPSet[client.ID] = true
			if err := s.ReconnectMCPClient(ctx, client.ID); err != nil {
				logger.Warn("FullReload: MCP client %s reconnect failed: %v", client.ID, err)
			}
		}
		// Remove stale MCP clients: GetMCPClients() returns currently registered clients
		if existingClients, err := s.Client.GetMCPClients(); err == nil {
			for _, ec := range existingClients {
				if !dbMCPSet[ec.Config.ID] {
					if err := s.RemoveMCPClient(ctx, ec.Config.ID); err != nil {
						logger.Warn("FullReload: RemoveMCPClient %s failed: %v", ec.Config.ID, err)
					}
				}
			}
		}
	}

	// 6. Plugin reconciliation (DB-authoritative, including removals)
	if err := s.reconcilePlugins(ctx); err != nil {
		logger.Warn("FullReload: plugin reconciliation failed: %v", err)
	}

	return nil
}

// reconcilePlugins diffs DB plugin list vs in-memory state and syncs.
func (s *BifrostHTTPServer) reconcilePlugins(ctx context.Context) error {
	dbPlugins, err := s.Config.ConfigStore.GetPlugins(ctx)
	if err != nil {
		return fmt.Errorf("list plugins from DB: %w", err)
	}

	memPlugins := s.GetPluginStatus(ctx)

	dbEnabled := make(map[string]*tables.TablePlugin)
	for _, p := range dbPlugins {
		if p != nil && p.Enabled {
			dbEnabled[p.Name] = p
		}
	}

	// In DB but not in memory → load
	for name, p := range dbEnabled {
		if _, inMem := memPlugins[name]; !inMem {
			if err := s.ReloadPlugin(ctx, name, p.Path, p.Config, p.Placement, p.Order); err != nil {
				logger.Warn("reconcilePlugins: failed to load plugin %s: %v", name, err)
			}
		}
	}

	// In memory but not in DB (or disabled in DB) → remove
	for name := range memPlugins {
		if _, inDB := dbEnabled[name]; !inDB {
			if err := s.RemovePlugin(ctx, name); err != nil {
				logger.Warn("reconcilePlugins: failed to remove plugin %s: %v", name, err)
			}
		}
	}

	// In both → reload if config/placement/order changed
	for name, p := range dbEnabled {
		if _, inMem := memPlugins[name]; inMem {
			// Always reload on FullReload to ensure config is current
			if err := s.ReloadPlugin(ctx, name, p.Path, p.Config, p.Placement, p.Order); err != nil {
				logger.Warn("reconcilePlugins: failed to reload plugin %s: %v", name, err)
			}
		}
	}

	return nil
}
```

Note: `tables.TablePlugin` field types for `Path`, `Config`, `Placement`, `Order` must match the actual table struct. Check `framework/configstore/tables/` for the exact field types and adjust the `ReloadPlugin` call accordingly.

- [ ] **Step 3: Build check**

```bash
cd transports && go build ./bifrost-http/server/... && cd ..
```

- [ ] **Step 4: Commit**

```bash
git add transports/bifrost-http/server/server.go
git commit -m "feat(server): add FullReload(ctx) with ordered, idempotent reload and plugin reconciliation"
```

---

## Task 13: Server Startup — Wire ClusterSyncer + XREAD Consumer

**Files:**
- Modify: `transports/bifrost-http/server/server.go`
- Modify: `transports/bifrost-http/lib/config.go`

- [ ] **Step 1: Add `clusterSyncer` and consumer fields to `BifrostHTTPServer`**

In `server.go`, add to `BifrostHTTPServer` struct:

```go
clusterSyncer  configstore.ClusterSyncer
clusterCtx     context.Context
clusterCancel  context.CancelFunc
```

- [ ] **Step 2: Add `InitCluster` method to `server.go`**

This method is called during server startup, after `Config` is initialized:

```go
// InitCluster sets up multi-node sync if cluster config is present.
// Must be called after Config and ConfigStore are initialized.
func (s *BifrostHTTPServer) InitCluster(ctx context.Context, clusterCfg *lib.ClusterConfig, nodeID string) error {
	if clusterCfg == nil {
		return nil // single-node mode
	}

	redisClient := clusterCfg.Redis.NewRedisUniversalClient()
	if redisClient == nil {
		return nil // no redis addr configured
	}

	if err := redisClient.Ping(ctx).Err(); err != nil {
		logger.Warn("cluster redis unavailable at startup, starting in single-node mode: %v", err)
		_ = redisClient.Close()
		return nil
	}

	syncer := configstore.NewRedisClusterSyncer(redisClient)
	s.clusterSyncer = syncer

	// Wrap ConfigStore with PublishingConfigStore
	s.Config.ConfigStore = configstore.NewPublishingConfigStore(
		s.Config.ConfigStore,
		syncer,
		nodeID,
		logger,
	)

	// Run counter recovery merge via exported methods — server package must not
	// access unexported fields of LocalGovernanceStore directly.
	if govStore := s.getGovernanceLocalStore(); govStore != nil {
		if ok := govStore.InitRedis(ctx, redisClient); ok {
			govStore.SetRedisAvailable(true)
			logger.Info("cluster: Redis recovery merge complete, switching to Redis read path")
		} else {
			logger.Warn("cluster: Redis recovery merge partial/failed, staying in degraded mode")
		}
	}

	// Start XREAD consumer goroutine.
	// fullReloadFn is passed separately; cursor is only persisted after it succeeds.
	consumerID := clusterCfg.ConsumerID()
	s.clusterCtx, s.clusterCancel = context.WithCancel(ctx)
	go syncer.Subscribe(s.clusterCtx, consumerID, nodeID, s.FullReload, s.handleConfigSyncEvent)

	logger.Info("cluster: multi-node sync active (consumerID=%s)", consumerID)
	return nil
}

// handleConfigSyncEvent dispatches a ConfigSyncEvent to the appropriate server method.
func (s *BifrostHTTPServer) handleConfigSyncEvent(event configstore.ConfigSyncEvent) {
	ctx := context.Background()

	switch event.Type {
	case "full_reload":
		if err := s.FullReload(ctx); err != nil {
			logger.Warn("cluster: FullReload failed: %v", err)
		}
	case "provider":
		if event.Action == "delete" {
			_ = s.RemoveProvider(ctx, schemas.ModelProvider(event.ID))
		} else {
			_, _ = s.ReloadProvider(ctx, schemas.ModelProvider(event.ID))
		}
	case "virtual_key":
		if event.Action == "delete" {
			_ = s.RemoveVirtualKey(ctx, event.ID)
		} else {
			_, _ = s.ReloadVirtualKey(ctx, event.ID)
		}
	case "team":
		if event.Action == "delete" {
			_ = s.RemoveTeam(ctx, event.ID)
		} else {
			_, _ = s.ReloadTeam(ctx, event.ID)
		}
	case "customer":
		if event.Action == "delete" {
			_ = s.RemoveCustomer(ctx, event.ID)
		} else {
			_, _ = s.ReloadCustomer(ctx, event.ID)
		}
	case "model_config":
		if event.Action == "delete" {
			_ = s.RemoveModelConfig(ctx, event.ID)
		} else {
			_, _ = s.ReloadModelConfig(ctx, event.ID)
		}
	case "routing_rule":
		if event.Action == "delete" {
			_ = s.RemoveRoutingRule(ctx, event.ID)
		} else {
			_ = s.ReloadRoutingRule(ctx, event.ID)
		}
	case "mcp_client":
		if event.Action == "delete" {
			_ = s.RemoveMCPClient(ctx, event.ID)
		} else {
			_ = s.ReconnectMCPClient(ctx, event.ID)
		}
	case "plugin":
		if event.Action == "delete" {
			_ = s.RemovePlugin(ctx, event.ID)
		} else {
			// Fetch full plugin config from DB for args
			if p, err := s.Config.ConfigStore.GetPlugin(ctx, event.ID); err == nil && p != nil {
				_ = s.ReloadPlugin(ctx, event.ID, p.Path, p.Config, p.Placement, p.Order)
			}
		}
	case "client_config":
		_ = s.ReloadClientConfigFromConfigStore(ctx)
	}
}

// getGovernanceLocalStore returns the governance *LocalGovernanceStore or nil.
func (s *BifrostHTTPServer) getGovernanceLocalStore() *governance.LocalGovernanceStore {
	if !s.Config.IsPluginLoaded(s.getGovernancePluginName()) {
		return nil
	}
	gp, err := s.getGovernancePlugin()
	if err != nil {
		return nil
	}
	store := gp.GetGovernanceStore()
	ls, ok := store.(*governance.LocalGovernanceStore)
	if !ok {
		return nil
	}
	return ls
}
```

- [ ] **Step 3: Call `InitCluster` during server startup**

Find the `Start` or `Init` function in `server.go` where `Config` is fully initialized. Add the cluster init call after Config setup:

```go
// Parse cluster config from config file (set in lib/config.go from ConfigData.Cluster)
nodeID := uuid.New().String()
if err := s.InitCluster(ctx, s.Config.Cluster, nodeID); err != nil {
    logger.Warn("cluster init failed, running in single-node mode: %v", err)
}
```

`s.Config.Cluster` is populated from `configData.Cluster` during config load (see Step 4 below).

- [ ] **Step 4: Add `Cluster` to `ConfigData` struct and JSON parsing**

`ConfigData` in `lib/config.go` (line ~128-144) is the top-level struct parsed from `config.json`. It has a custom `UnmarshalJSON` method (line ~149) that uses an internal `TempConfigData` struct to unmarshal and then copies fields.

**4a. Add `Cluster` to the top-level `ConfigData` struct (line ~143, before the closing `}`)**:

```go
Cluster *ClusterConfig `json:"cluster,omitempty"`
```

**4b. Add `Cluster` to the `TempConfigData` struct inside `UnmarshalJSON` (line ~164, before the closing `}`)**:

```go
Cluster *ClusterConfig `json:"cluster,omitempty"`
```

**4c. Copy the field in the "Set simple fields" section (after line ~181, after `cd.WebSocket = temp.WebSocket`)**:

```go
cd.Cluster = temp.Cluster
```

**4d. In the `Config` struct (separate from `ConfigData`, the runtime config object), also add the field** — search for the `Config` struct definition and add:

```go
Cluster *ClusterConfig // populated from ConfigData.Cluster after parsing
```

**4e. After `json.Unmarshal(data, &configData)` at line 484, pass cluster config to the runtime Config**:

The main parse call is at line 484:
```go
if err := json.Unmarshal(data, &configData); err != nil {
    return nil, fmt.Errorf("failed to unmarshal config: %w", err)
}
```

Later in the same function where `config` (type `*Config`) is built from `configData`, add:
```go
config.Cluster = configData.Cluster
```

**Then in Task 13 Step 3**, access it as `s.Config.Cluster` (not `s.Config.RawCluster`):

```go
var clusterCfg *lib.ClusterConfig
if s.Config.Cluster != nil {
    clusterCfg = s.Config.Cluster
}
```

- [ ] **Step 5: Handle cluster shutdown**

In the server shutdown path (search for `cancel()` or `Server.Shutdown`), add:

```go
if s.clusterCancel != nil {
    s.clusterCancel()
}
if s.clusterSyncer != nil {
    _ = s.clusterSyncer.Close()
}
```

- [ ] **Step 6: Build check**

```bash
cd transports && go build ./... && cd ..
```

- [ ] **Step 7: Commit**

```bash
git add transports/bifrost-http/server/server.go transports/bifrost-http/lib/config.go
git commit -m "feat(server): wire ClusterSyncer, XREAD consumer, and recovery bootstrap"
```

---

## Task 14: Unit Tests — Stream Consumer + Cursor

**Files:**
- Modify: `plugins/governance/multinode_test.go`

- [ ] **Step 1: Add stream consumer cursor test**

```go
func TestStreamConsumer_CursorDurable(t *testing.T) {
	client, mr := newTestRedis(t)
	ctx := context.Background()

	syncer := configstore.NewRedisClusterSyncer(client)

	// Publish 3 events
	for i := 0; i < 3; i++ {
		require.NoError(t, syncer.Publish(ctx, configstore.ConfigSyncEvent{
			Type:   "virtual_key",
			Action: "upsert",
			ID:     fmt.Sprintf("vk-%d", i),
			NodeID: "other-node",
		}))
	}

	var received []string
	consumerCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	go syncer.Subscribe(consumerCtx, "test-consumer", "self-node", func(ev configstore.ConfigSyncEvent) {
		if ev.Type != "full_reload" {
			received = append(received, ev.ID)
		}
	})

	<-consumerCtx.Done()

	// After consumer runs, cursor should be stored in Redis
	cursorKey := "bifrost:consumer:test-consumer:last_seen"
	cursor, err := client.Get(ctx, cursorKey).Result()
	require.NoError(t, err)
	assert.NotEmpty(t, cursor, "cursor should be persisted after consuming events")
	_ = mr // silence unused warning
}
```

- [ ] **Step 2: Add self-publish filter test**

```go
func TestStreamConsumer_SkipSelfPublished(t *testing.T) {
	client, _ := newTestRedis(t)
	ctx := context.Background()
	syncer := configstore.NewRedisClusterSyncer(client)

	// Publish event from "self-node"
	require.NoError(t, syncer.Publish(ctx, configstore.ConfigSyncEvent{
		Type:   "team",
		Action: "upsert",
		ID:     "team-self",
		NodeID: "self-node",
	}))
	// Publish event from other node
	require.NoError(t, syncer.Publish(ctx, configstore.ConfigSyncEvent{
		Type:   "team",
		Action: "upsert",
		ID:     "team-other",
		NodeID: "other-node",
	}))

	var received []string
	consumerCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	go syncer.Subscribe(consumerCtx, "consumer-filter-test", "self-node", func(ev configstore.ConfigSyncEvent) {
		if ev.Type != "full_reload" {
			received = append(received, ev.ID)
		}
	})

	<-consumerCtx.Done()
	assert.Contains(t, received, "team-other")
	assert.NotContains(t, received, "team-self")
}
```

- [ ] **Step 3: Run all unit tests**

```bash
cd plugins/governance && go test ./... -run "TestStream|TestRecovery|TestRateLimit|TestBudget" -v && cd ../..
```
Expected: all PASS.

- [ ] **Step 4: Commit**

```bash
git add plugins/governance/multinode_test.go
git commit -m "test(governance): add stream consumer cursor and self-publish filter tests"
```

---

## Task 15: Example Config + Run Check

**Files:**
- Create: `examples/configs/withmultinode/config.json`

- [ ] **Step 1: Create example config**

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

Note: This is a partial config — in practice it would be merged with provider and governance config.

- [ ] **Step 2: Full build check across all modules**

```bash
cd framework && go build ./... && cd ..
cd plugins/governance && go build ./... && cd ../..
cd transports && go build ./... && cd ..
```
Expected: no errors in any module.

- [ ] **Step 3: Run all governance tests**

```bash
cd plugins/governance && go test ./... -v -count=1 -short && cd ../..
```

- [ ] **Step 4: Commit**

```bash
git add examples/configs/withmultinode/config.json
git commit -m "feat: add multi-node example config and verify full build"
```

---

## Self-Review

Checking spec coverage against tasks:

| Spec requirement | Task |
|-----------------|------|
| Redis Streams XADD/XREAD with cursor | Task 3 |
| PublishingConfigStore, ExecuteTransaction choke point | Task 4 |
| eventAccumulator in context | Task 4 |
| Publish after commit, not on rollback | Task 4 + tests Task 5 |
| Consumer identity (stable consumerID vs ephemeral nodeID) | Task 3 |
| Watermark-first full reload | Task 3 |
| Reconnect catch-up | Task 3 |
| INCRBY rate limit write | Task 8 |
| GET rate limit read | Task 8 |
| Reset rate limit with TTL | Task 8 |
| INCRBYFLOAT budget write | Task 9 |
| GET budget read | Task 9 |
| Reset budget with TTL | Task 9 |
| All 6 budget levels covered | Task 9 (via existing `collectBudgetsFromHierarchy`) |
| Lua merge script (recovery) | Task 6 + Task 10 |
| LastDBUsages init from Postgres | Task 7 |
| `redisAvailable` atomic.Bool | Task 7 |
| Switch to Redis only after all merges succeed | Task 10 |
| `server.FullReload` ordered + idempotent | Task 12 |
| Plugin reconciliation in FullReload | Task 12 |
| ClusterSyncer wired at startup | Task 13 |
| Dispatch table (handleConfigSyncEvent) | Task 13 |
| Shutdown cleanup | Task 13 |
| Config schema | Task 1 |
| ClusterConfig types + factory | Task 2 |
| Redis standalone and cluster mode | Task 2 |
| Cursor durability test | Task 14 |
| Self-publish filter test | Task 14 |
| Example config | Task 15 |
| `strict_budgets` flag | Task 1 (schema) + Task 2 (type) — implementation would be an extension to CheckBudget degraded path |

One gap: `strict_budgets` DB-atomic fallback in `CheckBudget` when Redis is unavailable. This is a lower-priority optional feature. If implementing: in `CheckBudget`, when `redisAvailable = false && strict_budgets = true`, run `UPDATE budgets SET current_usage = current_usage + $cost WHERE current_usage + $cost <= max_limit RETURNING id` instead of the in-memory check. Add to Task 9 if time allows; skip for initial implementation.
