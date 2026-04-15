package configstore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type testLogger struct{}

func (testLogger) Debug(string, ...any)                   {}
func (testLogger) Info(string, ...any)                    {}
func (testLogger) Warn(string, ...any)                    {}
func (testLogger) Error(string, ...any)                   {}
func (testLogger) Fatal(string, ...any)                   {}
func (testLogger) SetLevel(schemas.LogLevel)              {}
func (testLogger) SetOutputType(schemas.LoggerOutputType) {}
func (testLogger) LogHTTPRequest(schemas.LogLevel, string) schemas.LogEventBuilder {
	return schemas.NoopLogEvent
}

func newMiniRedis(t *testing.T) (redis.UniversalClient, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close(); mr.Close() })
	return client, mr
}

func readLastStreamEvent(t *testing.T, client redis.UniversalClient) *ConfigSyncEvent {
	t.Helper()
	msgs, err := client.XRange(context.Background(), streamKey, "-", "+").Result()
	require.NoError(t, err)
	if len(msgs) == 0 {
		return nil
	}
	last := msgs[len(msgs)-1]
	data, ok := last.Values["data"].(string)
	require.True(t, ok)
	var ev ConfigSyncEvent
	require.NoError(t, json.Unmarshal([]byte(data), &ev))
	return &ev
}

func TestPublishingConfigStore_PublishAfterCommit(t *testing.T) {
	client, _ := newMiniRedis(t)
	syncer := NewRedisClusterSyncer(client)
	inner := setupRDBTestStore(t)
	pcs := NewPublishingConfigStore(inner, syncer, "node-A", testLogger{})

	ctx := context.Background()
	err := pcs.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		return pcs.AddProvider(ctx, "openai", ProviderConfig{
			Keys: []schemas.Key{
				{ID: "k1", Name: "n", Value: *schemas.NewEnvVar("sk-test"), Weight: 1},
			},
		}, tx)
	})
	require.NoError(t, err)

	ev := readLastStreamEvent(t, client)
	require.NotNil(t, ev)
	assert.Equal(t, "provider", ev.Type)
	assert.Equal(t, "upsert", ev.Action)
	assert.Equal(t, "openai", ev.ID)
	assert.Equal(t, "node-A", ev.NodeID)
}

func TestPublishingConfigStore_NoPublishOnRollback(t *testing.T) {
	client, _ := newMiniRedis(t)
	syncer := NewRedisClusterSyncer(client)
	inner := setupRDBTestStore(t)
	pcs := NewPublishingConfigStore(inner, syncer, "node-A", testLogger{})

	ctx := context.Background()
	err := pcs.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		return errors.New("db error")
	})
	assert.Error(t, err)

	ev := readLastStreamEvent(t, client)
	assert.Nil(t, ev)
}

func TestPublishingConfigStore_NoPublishOnRollbackAfterWrite(t *testing.T) {
	client, _ := newMiniRedis(t)
	syncer := NewRedisClusterSyncer(client)
	inner := setupRDBTestStore(t)
	pcs := NewPublishingConfigStore(inner, syncer, "node-A", testLogger{})

	ctx := context.Background()
	err := pcs.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		if err := pcs.AddProvider(ctx, "anthropic", ProviderConfig{
			Keys: []schemas.Key{
				{ID: "k-rollback", Name: "n", Value: *schemas.NewEnvVar("sk-x"), Weight: 1},
			},
		}, tx); err != nil {
			return err
		}
		return errors.New("forced rollback after write")
	})
	require.Error(t, err)

	ev := readLastStreamEvent(t, client)
	assert.Nil(t, ev, "stream must have no event when transaction rolls back after scheduling")
}

func TestPublishingConfigStore_NilSyncer_Passthrough(t *testing.T) {
	inner := setupRDBTestStore(t)
	pcs := NewPublishingConfigStore(inner, nil, "node-A", testLogger{})

	ctx := context.Background()
	err := pcs.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		return pcs.AddProvider(ctx, "groq", ProviderConfig{
			Keys: []schemas.Key{
				{ID: "k2", Name: "n", Value: *schemas.NewEnvVar("sk-test2"), Weight: 1},
			},
		}, tx)
	})
	require.NoError(t, err)
}
