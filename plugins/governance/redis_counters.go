package governance

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
)

const (
	rateLimitTokensKeyFmt   = "bifrost:rl:%s:tokens"
	rateLimitRequestsKeyFmt = "bifrost:rl:%s:requests"
	budgetSpentKeyFmt       = "bifrost:budget:%s:spent"
)

func rlTokenKey(id string) string   { return fmt.Sprintf(rateLimitTokensKeyFmt, id) }
func rlRequestKey(id string) string { return fmt.Sprintf(rateLimitRequestsKeyFmt, id) }
func budgetKey(id string) string    { return fmt.Sprintf(budgetSpentKeyFmt, id) }


// luaMerge initializes a key from postgres_baseline if absent, then adds local_delta (if > 0).
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

// RedisCounterClient wraps a redis.UniversalClient for rate limit and budget counters.
type RedisCounterClient struct {
	client redis.UniversalClient
}

// NewRedisCounterClient creates a RedisCounterClient.
func NewRedisCounterClient(client redis.UniversalClient) *RedisCounterClient {
	return &RedisCounterClient{client: client}
}

// IncrTokens increments the token counter for a rate limit.
func (r *RedisCounterClient) IncrTokens(ctx context.Context, rateLimitID string, delta int64) (int64, error) {
	key := rlTokenKey(rateLimitID)
	return r.client.IncrBy(ctx, key, delta).Result()
}

// IncrRequests increments the request counter for a rate limit.
func (r *RedisCounterClient) IncrRequests(ctx context.Context, rateLimitID string, delta int64) (int64, error) {
	key := rlRequestKey(rateLimitID)
	return r.client.IncrBy(ctx, key, delta).Result()
}

// GetTokens reads the token counter for a rate limit.
func (r *RedisCounterClient) GetTokens(ctx context.Context, rateLimitID string) (int64, error) {
	key := rlTokenKey(rateLimitID)
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
	key := rlRequestKey(rateLimitID)
	val, err := r.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(val, 10, 64)
}

// ResetRateLimit sets both counters to 0 with optional TTL.
func (r *RedisCounterClient) ResetRateLimit(ctx context.Context, rateLimitID string, ttlSeconds int64) error {
	tokenKey := rlTokenKey(rateLimitID)
	requestKey := rlRequestKey(rateLimitID)
	var ttl time.Duration
	if ttlSeconds > 0 {
		ttl = time.Duration(ttlSeconds) * time.Second
	}
	pipe := r.client.Pipeline()
	pipe.Set(ctx, tokenKey, 0, ttl)
	pipe.Set(ctx, requestKey, 0, ttl)
	_, err := pipe.Exec(ctx)
	return err
}

// ResetRateLimitTokens sets only the token counter to 0 with optional TTL.
func (r *RedisCounterClient) ResetRateLimitTokens(ctx context.Context, rateLimitID string, ttlSeconds int64) error {
	key := rlTokenKey(rateLimitID)
	var ttl time.Duration
	if ttlSeconds > 0 {
		ttl = time.Duration(ttlSeconds) * time.Second
	}
	return r.client.Set(ctx, key, 0, ttl).Err()
}

// ResetRateLimitRequests sets only the request counter to 0 with optional TTL.
func (r *RedisCounterClient) ResetRateLimitRequests(ctx context.Context, rateLimitID string, ttlSeconds int64) error {
	key := rlRequestKey(rateLimitID)
	var ttl time.Duration
	if ttlSeconds > 0 {
		ttl = time.Duration(ttlSeconds) * time.Second
	}
	return r.client.Set(ctx, key, 0, ttl).Err()
}

// IncrBudget increments the budget spent counter.
func (r *RedisCounterClient) IncrBudget(ctx context.Context, budgetID string, delta float64) (float64, error) {
	key := budgetKey(budgetID)
	return r.client.IncrByFloat(ctx, key, delta).Result()
}

// GetBudget reads the budget spent counter.
func (r *RedisCounterClient) GetBudget(ctx context.Context, budgetID string) (float64, error) {
	key := budgetKey(budgetID)
	val, err := r.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(val, 64)
}

// ResetBudget sets the budget counter to 0 with optional TTL.
func (r *RedisCounterClient) ResetBudget(ctx context.Context, budgetID string, ttlSeconds int64) error {
	key := budgetKey(budgetID)
	var ttl time.Duration
	if ttlSeconds > 0 {
		ttl = time.Duration(ttlSeconds) * time.Second
	}
	return r.client.Set(ctx, key, 0, ttl).Err()
}

// MergeDelta runs the atomic Lua merge for integer (rate limit) counters.
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

func (gs *LocalGovernanceStore) clusterRateUsage(ctx context.Context, rl *configstoreTables.TableRateLimit, tokenBaseline, reqBaseline int64) (int64, int64) {
	tokens := rl.TokenCurrentUsage + tokenBaseline
	reqs := rl.RequestCurrentUsage + reqBaseline
	if gs.redisAvailable.Load() && gs.redisCounters != nil {
		if t, err := gs.redisCounters.GetTokens(ctx, rl.ID); err == nil {
			tokens = t
		}
		if r, err := gs.redisCounters.GetRequests(ctx, rl.ID); err == nil {
			reqs = r
		}
	}
	return tokens, reqs
}

func (gs *LocalGovernanceStore) clusterBudgetSpent(ctx context.Context, b *configstoreTables.TableBudget, baseline float64) float64 {
	spent := b.CurrentUsage + baseline
	if gs.redisAvailable.Load() && gs.redisCounters != nil {
		if s, err := gs.redisCounters.GetBudget(ctx, b.ID); err == nil {
			return s
		}
	}
	return spent
}

// InitRedis attaches a Redis client and runs recovery merge. Returns true if merge succeeded.
func (gs *LocalGovernanceStore) InitRedis(ctx context.Context, client redis.UniversalClient) bool {
	if client == nil {
		return false
	}
	gs.redisCounters = NewRedisCounterClient(client)
	if ok := gs.RunRecoveryMerge(ctx); ok {
		gs.redisAvailable.Store(true)
		return true
	}
	return false
}

// SetRedisAvailable marks whether Redis is authoritative for counters.
func (gs *LocalGovernanceStore) SetRedisAvailable(v bool) {
	gs.redisAvailable.Store(v)
}

// GetRedisCounters returns the Redis counter client, or nil.
func (gs *LocalGovernanceStore) GetRedisCounters() *RedisCounterClient {
	return gs.redisCounters
}

// IsRedisAvailable reports whether Redis read paths are active.
func (gs *LocalGovernanceStore) IsRedisAvailable() bool {
	return gs.redisAvailable.Load()
}

// RunRecoveryMerge merges local outage deltas into Redis using Postgres baselines.
func (gs *LocalGovernanceStore) RunRecoveryMerge(ctx context.Context) bool {
	if gs.redisCounters == nil {
		return false
	}

	gs.LastDBUsagesRateLimitsTokensMu.RLock()
	gs.LastDBUsagesRateLimitsRequestsMu.RLock()
	type rlSnapshot struct {
		id             string
		inMemTokens    int64
		lastDBTokens   int64
		inMemRequests  int64
		lastDBRequests int64
	}
	var rlSnaps []rlSnapshot
	gs.rateLimits.Range(func(key, value interface{}) bool {
		rl, ok := value.(*configstoreTables.TableRateLimit)
		if !ok || rl == nil {
			return true
		}
		snap := rlSnapshot{
			id:            rl.ID,
			inMemTokens:   rl.TokenCurrentUsage,
			inMemRequests: rl.RequestCurrentUsage,
		}
		snap.lastDBTokens = gs.LastDBUsagesTokensRateLimits[rl.ID]
		snap.lastDBRequests = gs.LastDBUsagesRequestsRateLimits[rl.ID]
		rlSnaps = append(rlSnaps, snap)
		return true
	})
	gs.LastDBUsagesRateLimitsRequestsMu.RUnlock()
	gs.LastDBUsagesRateLimitsTokensMu.RUnlock()

	for _, snap := range rlSnaps {
		tokenDelta := snap.inMemTokens - snap.lastDBTokens
		reqDelta := snap.inMemRequests - snap.lastDBRequests

		rl, err := gs.configStore.GetRateLimit(ctx, snap.id)
		if err != nil {
			gs.logger.Error("recovery merge: get rate limit %s from db: %v", snap.id, err)
			return false
		}
		if rl == nil {
			gs.logger.Error("recovery merge: get rate limit %s from db: nil row", snap.id)
			return false
		}

		tokenKey := rlTokenKey(snap.id)
		requestKey := rlRequestKey(snap.id)

		if err := gs.redisCounters.MergeDelta(ctx, tokenKey, rl.TokenCurrentUsage, tokenDelta); err != nil {
			gs.logger.Error("recovery merge failed for %s tokens: %v", snap.id, err)
			return false
		}
		if err := gs.redisCounters.MergeDelta(ctx, requestKey, rl.RequestCurrentUsage, reqDelta); err != nil {
			gs.logger.Error("recovery merge failed for %s requests: %v", snap.id, err)
			return false
		}
	}

	type budgetSnapshot struct {
		id     string
		inMem  float64
		lastDB float64
	}
	var bSnaps []budgetSnapshot
	gs.LastDBUsagesBudgetsMu.RLock()
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
		if err != nil {
			gs.logger.Error("recovery merge: get budget %s from db: %v", snap.id, err)
			return false
		}
		if budget == nil {
			gs.logger.Error("recovery merge: get budget %s from db: nil row", snap.id)
			return false
		}

		key := budgetKey(snap.id)
		if err := gs.redisCounters.MergeBudgetDelta(ctx, key, budget.CurrentUsage, delta); err != nil {
			gs.logger.Error("recovery merge failed for budget %s: %v", snap.id, err)
			return false
		}
	}

	return true
}

// resetRateLimitRedis zeros only the Redis counter keys whose in-memory windows rolled over.
func (gs *LocalGovernanceStore) resetRateLimitRedis(ctx context.Context, rl *configstoreTables.TableRateLimit, resetTokens, resetRequests bool) {
	if !gs.redisAvailable.Load() || gs.redisCounters == nil {
		return
	}
	if !resetTokens && !resetRequests {
		return
	}
	var ttl int64
	if resetTokens && rl.TokenResetDuration != nil {
		if dur, err := configstoreTables.ParseDuration(*rl.TokenResetDuration); err == nil {
			ttl = int64(dur.Seconds())
		}
	}
	if ttl == 0 && resetRequests && rl.RequestResetDuration != nil {
		if dur, err := configstoreTables.ParseDuration(*rl.RequestResetDuration); err == nil {
			ttl = int64(dur.Seconds())
		}
	}
	var err error
	switch {
	case resetTokens && resetRequests:
		err = gs.redisCounters.ResetRateLimit(ctx, rl.ID, ttl)
	case resetTokens:
		err = gs.redisCounters.ResetRateLimitTokens(ctx, rl.ID, ttl)
	case resetRequests:
		err = gs.redisCounters.ResetRateLimitRequests(ctx, rl.ID, ttl)
	}
	if err != nil {
		gs.logger.Warn("redis rate limit reset failed for %s: %v", rl.ID, err)
	}
}
