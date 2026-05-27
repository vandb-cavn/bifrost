package governance

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	rateLimitTokensKeyFmt   = "bifrost:rl:%s:tokens"
	rateLimitRequestsKeyFmt = "bifrost:rl:%s:requests"
	budgetSpentKeyFmt       = "bifrost:budget:%s:spent"
)

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
	key := fmt.Sprintf(rateLimitTokensKeyFmt, rateLimitID)
	return r.client.IncrBy(ctx, key, delta).Result()
}

// IncrRequests increments the request counter for a rate limit.
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

// ResetRateLimit sets both counters to 0 with optional TTL.
func (r *RedisCounterClient) ResetRateLimit(ctx context.Context, rateLimitID string, ttlSeconds int64) error {
	tokenKey := fmt.Sprintf(rateLimitTokensKeyFmt, rateLimitID)
	requestKey := fmt.Sprintf(rateLimitRequestsKeyFmt, rateLimitID)
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

// IncrBudget increments the budget spent counter.
func (r *RedisCounterClient) IncrBudget(ctx context.Context, budgetID string, delta float64) (float64, error) {
	key := fmt.Sprintf(budgetSpentKeyFmt, budgetID)
	return r.client.IncrByFloat(ctx, key, delta).Result()
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

// ResetBudget sets the budget counter to 0 with optional TTL.
func (r *RedisCounterClient) ResetBudget(ctx context.Context, budgetID string, ttlSeconds int64) error {
	key := fmt.Sprintf(budgetSpentKeyFmt, budgetID)
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
