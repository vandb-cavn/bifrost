package configstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
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
	Type      string    `json:"type"`
	Action    string    `json:"action"`
	ID        string    `json:"id,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	NodeID    string    `json:"node_id"`
}

// ClusterSyncer publishes and subscribes to config change events.
type ClusterSyncer interface {
	Publish(ctx context.Context, event ConfigSyncEvent) error
	Subscribe(
		ctx context.Context,
		consumerID string,
		selfNodeID string,
		fullReloadFn func(ctx context.Context) error,
		handler func(ConfigSyncEvent),
	)
	// Close releases syncer-owned resources. Does not close the redis client when shared.
	Close() error
}

// RedisClusterSyncer implements ClusterSyncer via Redis Streams.
type RedisClusterSyncer struct {
	client redis.UniversalClient
	logger schemas.Logger // optional; when set, emits Debug lines for publish/subscribe tracing
}

// NewRedisClusterSyncer creates a syncer. client must already be connected; caller owns lifecycle.
// logger may be nil; when non-nil, cluster sync publishes and stream deliveries are logged at Debug.
func NewRedisClusterSyncer(client redis.UniversalClient, logger schemas.Logger) *RedisClusterSyncer {
	return &RedisClusterSyncer{client: client, logger: logger}
}

// Publish sends a ConfigSyncEvent to the Redis Stream using XADD MAXLEN ~ N.
func (s *RedisClusterSyncer) Publish(ctx context.Context, event ConfigSyncEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	err = s.client.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		MaxLen: streamMaxLen,
		Approx: true,
		Values: map[string]interface{}{"data": string(payload)},
	}).Err()
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("cluster sync: redis XADD failed stream=%s type=%s action=%s id=%s: %v",
				streamKey, event.Type, event.Action, event.ID, err)
		} else {
			log.Printf("cluster sync: redis XADD failed stream=%s type=%s action=%s id=%s: %v",
				streamKey, event.Type, event.Action, event.ID, err)
		}
		return err
	}
	if s.logger != nil {
		s.logger.Debug("cluster sync: published to redis stream=%s type=%s action=%s id=%s node_id=%s",
			streamKey, event.Type, event.Action, event.ID, event.NodeID)
	}
	return nil
}

// Subscribe starts the blocking XREAD consumer loop.
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
		needsFullReload := err != nil || lastID == ""
		if !needsFullReload {
			needsFullReload = s.hasStreamGap(ctx, lastID)
		}

		if needsFullReload {
			var werr error
			lastID, werr = s.watermarkFirstFullReload(ctx, cursorKey, fullReloadFn)
			if werr != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(delay):
					delay = minDur(delay*2, reconnectMax)
				}
				continue
			}
			delay = reconnectBase
		}

		err = s.readLoop(ctx, cursorKey, lastID, selfNodeID, handler)
		if err == nil || errors.Is(err, context.Canceled) {
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
			delay = minDur(delay*2, reconnectMax)
		}
	}
}

func (s *RedisClusterSyncer) watermarkFirstFullReload(
	ctx context.Context,
	cursorKey string,
	fullReloadFn func(ctx context.Context) error,
) (string, error) {
	watermark := "0-0"
	msgs, err := s.client.XRevRangeN(ctx, streamKey, "+", "-", 1).Result()
	if err == nil && len(msgs) > 0 {
		watermark = msgs[0].ID
	}

	if err := fullReloadFn(ctx); err != nil {
		return watermark, fmt.Errorf("full reload failed, cursor not advanced: %w", err)
	}

	if err := s.client.Set(ctx, cursorKey, watermark, 0).Err(); err != nil {
		return watermark, err
	}

	return watermark, nil
}

func (s *RedisClusterSyncer) hasStreamGap(ctx context.Context, lastID string) bool {
	info, err := s.client.XInfoStream(ctx, streamKey).Result()
	if err != nil {
		log.Printf("cluster sync: XInfoStream failed (treating as no gap): %v", err)
		return false
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
		if errors.Is(err, redis.Nil) {
			continue
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
				if err := json.Unmarshal([]byte(data), &event); err != nil {
					if s.logger != nil {
						s.logger.Debug("cluster sync: skip stream id=%s (unmarshal event: %v)", msg.ID, err)
					}
					id = msg.ID
					_ = s.client.Set(ctx, cursorKey, id, 0).Err()
					continue
				}
				skipSelf := event.NodeID != "" && event.NodeID == selfNodeID
				if s.logger != nil {
					if skipSelf {
						s.logger.Debug("cluster sync: received stream id=%s type=%s action=%s id=%s source_node=%s (skip, same node)",
							msg.ID, event.Type, event.Action, event.ID, event.NodeID)
					} else {
						s.logger.Debug("cluster sync: received stream id=%s type=%s action=%s id=%s source_node=%s (dispatch handler)",
							msg.ID, event.Type, event.Action, event.ID, event.NodeID)
					}
				}
				if event.NodeID == "" || event.NodeID != selfNodeID {
					handler(event)
				}
				id = msg.ID
				_ = s.client.Set(ctx, cursorKey, id, 0).Err()
			}
		}
	}
}

func (s *RedisClusterSyncer) loadCursor(ctx context.Context, key string) (string, error) {
	id, err := s.client.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", nil
		}
		return "", err
	}
	if id != "" {
		if err := validateRedisStreamCursor(id); err != nil {
			return "", err
		}
	}
	return id, nil
}

// validateRedisStreamCursor rejects malformed persisted cursors so gap detection does not
// treat arbitrary strings as the numeric id "0-0".
func validateRedisStreamCursor(s string) error {
	for i, c := range s {
		if c == '-' && i > 0 && i < len(s)-1 {
			_, err1 := strconv.ParseUint(s[:i], 10, 64)
			_, err2 := strconv.ParseUint(s[i+1:], 10, 64)
			if err1 != nil || err2 != nil {
				return fmt.Errorf("invalid cluster stream cursor %q", s)
			}
			return nil
		}
	}
	return fmt.Errorf("invalid cluster stream cursor %q", s)
}

// Close implements ClusterSyncer. The redis client is owned by the caller and is not closed.
func (s *RedisClusterSyncer) Close() error {
	return nil
}

func compareStreamIDs(a, b string) int {
	if a == "" {
		a = "0-0"
	}
	if b == "" {
		b = "0-0"
	}
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

func minDur(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
