package governance

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
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

// failGetRLStore wraps RDBConfigStore and forces GetRateLimit to fail (recovery merge partial failure).
type failGetRLStore struct {
	*configstore.RDBConfigStore
}

func (f *failGetRLStore) GetRateLimit(ctx context.Context, id string, tx ...*gorm.DB) (*configstoreTables.TableRateLimit, error) {
	_ = id
	return nil, fmt.Errorf("simulated db read failure")
}

func newGovernanceTestSQLiteRDB(t *testing.T) *configstore.RDBConfigStore {
	t.Helper()
	dsn := "file:gov_" + strconv.FormatInt(time.Now().UnixNano(), 10) + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	err = db.AutoMigrate(
		&configstoreTables.TableProvider{},
		&configstoreTables.TableKey{},
		&configstoreTables.TableBudget{},
		&configstoreTables.TableRateLimit{},
		&configstoreTables.TableVirtualKey{},
		&configstoreTables.TableVirtualKeyProviderConfig{},
		&configstoreTables.TableVirtualKeyProviderConfigKey{},
		&configstoreTables.TableCustomer{},
		&configstoreTables.TableTeam{},
		&configstoreTables.TableClientConfig{},
		&configstoreTables.TablePlugin{},
		&configstoreTables.TableMCPClient{},
		&configstoreTables.TableVirtualKeyMCPConfig{},
		&configstoreTables.TableFolder{},
		&configstoreTables.TablePrompt{},
		&configstoreTables.TablePromptVersion{},
		&configstoreTables.TablePromptVersionMessage{},
		&configstoreTables.TablePromptSession{},
		&configstoreTables.TablePromptSessionMessage{},
		&configstoreTables.TableModelConfig{},
		&configstoreTables.TableRoutingRule{},
	)
	require.NoError(t, err)
	require.NoError(t, db.SetupJoinTable(&configstoreTables.TableVirtualKeyProviderConfig{}, "Keys", &configstoreTables.TableVirtualKeyProviderConfigKey{}))
	return configstore.NewRDBConfigStoreForTest(db)
}

func TestRunRecoveryMerge_GetRateLimitErrorReturnsFalse(t *testing.T) {
	ctx := context.Background()
	rdb := newGovernanceTestSQLiteRDB(t)
	wrap := &failGetRLStore{RDBConfigStore: rdb}

	rl := buildRateLimitWithUsage("rl-rec", 10000, 5, 1000, 3)
	require.NoError(t, rdb.CreateRateLimit(ctx, rl))
	vk := buildVirtualKey("vk-rec", "sk-bf-rec", "VK rec", true)
	vk.ProviderConfigs = []configstoreTables.TableVirtualKeyProviderConfig{
		buildProviderConfig("openai", []string{"*"}),
	}
	rid := rl.ID
	vk.RateLimitID = &rid
	require.NoError(t, rdb.CreateVirtualKey(ctx, vk))

	gs, err := NewLocalGovernanceStore(ctx, NewMockLogger(), wrap, nil, nil)
	require.NoError(t, err)

	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(func() { mr.Close() })
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	gs.redisCounters = NewRedisCounterClient(client)

	assert.False(t, gs.RunRecoveryMerge(ctx), "merge must fail when Postgres baseline read fails")
}

func TestDumpRateLimits_UpdatesLastDBUsagesWhenRedisUnavailable(t *testing.T) {
	ctx := context.Background()
	rdb := newGovernanceTestSQLiteRDB(t)

	rl := buildRateLimitWithUsage("rl-dump", 10000, 1, 1000, 2)
	require.NoError(t, rdb.CreateRateLimit(ctx, rl))
	vk := buildVirtualKey("vk-dump", "sk-bf-dump", "VK dump", true)
	vk.ProviderConfigs = []configstoreTables.TableVirtualKeyProviderConfig{
		buildProviderConfig("openai", []string{"*"}),
	}
	rid := rl.ID
	vk.RateLimitID = &rid
	require.NoError(t, rdb.CreateVirtualKey(ctx, vk))

	gs, err := NewLocalGovernanceStore(ctx, NewMockLogger(), rdb, nil, nil)
	require.NoError(t, err)
	gs.SetRedisAvailable(false)

	v, ok := gs.rateLimits.Load("rl-dump")
	require.True(t, ok)
	rlMem := v.(*configstoreTables.TableRateLimit)
	rlMem.TokenCurrentUsage = 42
	rlMem.RequestCurrentUsage = 7

	require.NoError(t, gs.DumpRateLimits(ctx, nil, nil))

	gs.LastDBUsagesRateLimitsTokensMu.RLock()
	gs.LastDBUsagesRateLimitsRequestsMu.RLock()
	tok := gs.LastDBUsagesTokensRateLimits["rl-dump"]
	req := gs.LastDBUsagesRequestsRateLimits["rl-dump"]
	gs.LastDBUsagesRateLimitsRequestsMu.RUnlock()
	gs.LastDBUsagesRateLimitsTokensMu.RUnlock()
	assert.Equal(t, int64(42), tok)
	assert.Equal(t, int64(7), req)
}
