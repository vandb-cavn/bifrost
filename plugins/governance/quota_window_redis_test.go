package governance

import (
	"context"
	"fmt"
	"testing"

	"github.com/alicebob/miniredis/v2"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResetRateLimitRedis_RequestOnlyPreservesTokenKey(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(func() { mr.Close() })
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	rlID := "rl-partial"
	tokenKey := fmt.Sprintf("bifrost:rl:%s:tokens", rlID)
	requestKey := fmt.Sprintf("bifrost:rl:%s:requests", rlID)
	require.NoError(t, client.Set(ctx, tokenKey, 500, 0).Err())
	require.NoError(t, client.Set(ctx, requestKey, 42, 0).Err())

	gs := &LocalGovernanceStore{
		logger:         NewMockLogger(),
		redisCounters:  NewRedisCounterClient(client),
	}
	gs.redisAvailable.Store(true)

	reqDur := "1h"
	rl := &configstoreTables.TableRateLimit{
		ID:                     rlID,
		RequestResetDuration:   &reqDur,
	}

	gs.resetRateLimitRedis(ctx, rl, false, true)

	tokens, err := client.Get(ctx, tokenKey).Int64()
	require.NoError(t, err)
	assert.Equal(t, int64(500), tokens)

	requests, err := client.Get(ctx, requestKey).Int64()
	require.NoError(t, err)
	assert.Equal(t, int64(0), requests)
}

func TestResetRateLimitRedis_TokenOnlyPreservesRequestKey(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(func() { mr.Close() })
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	rlID := "rl-partial-tok"
	tokenKey := fmt.Sprintf("bifrost:rl:%s:tokens", rlID)
	requestKey := fmt.Sprintf("bifrost:rl:%s:requests", rlID)
	require.NoError(t, client.Set(ctx, tokenKey, 100, 0).Err())
	require.NoError(t, client.Set(ctx, requestKey, 7, 0).Err())

	gs := &LocalGovernanceStore{
		logger:        NewMockLogger(),
		redisCounters: NewRedisCounterClient(client),
	}
	gs.redisAvailable.Store(true)

	tokDur := "1h"
	rl := &configstoreTables.TableRateLimit{
		ID:                   rlID,
		TokenResetDuration:   &tokDur,
	}

	gs.resetRateLimitRedis(ctx, rl, true, false)

	tokens, err := client.Get(ctx, tokenKey).Int64()
	require.NoError(t, err)
	assert.Equal(t, int64(0), tokens)

	requests, err := client.Get(ctx, requestKey).Int64()
	require.NoError(t, err)
	assert.Equal(t, int64(7), requests)
}
