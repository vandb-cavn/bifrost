package governance

import (
	"context"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedisMergeDelta_BaselineAndDelta(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(func() { mr.Close() })
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	rc := NewRedisCounterClient(client)
	ctx := context.Background()
	key := "bifrost:rl:rl-merge:tokens"
	require.NoError(t, rc.MergeDelta(ctx, key, 1000, 42))
	v, err := client.Get(ctx, key).Int64()
	require.NoError(t, err)
	assert.Equal(t, int64(1042), v)
}

func TestRedisMergeBudgetDelta_BaselineAndDelta(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(func() { mr.Close() })
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	rc := NewRedisCounterClient(client)
	ctx := context.Background()
	key := "bifrost:budget:b1:spent"
	require.NoError(t, rc.MergeBudgetDelta(ctx, key, 10.5, 0.25))
	v, err := client.Get(ctx, key).Float64()
	require.NoError(t, err)
	assert.InDelta(t, 10.75, v, 0.0001)
}

func TestRedisMergeDelta_ConcurrentIncrements(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(func() { mr.Close() })
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	rc := NewRedisCounterClient(client)
	ctx := context.Background()
	key := "bifrost:rl:rl-conc:requests"
	const workers = 32
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			assert.NoError(t, rc.MergeDelta(ctx, key, 0, 1))
		}()
	}
	wg.Wait()
	v, err := client.Get(ctx, key).Int64()
	require.NoError(t, err)
	assert.Equal(t, int64(workers), v)
}
