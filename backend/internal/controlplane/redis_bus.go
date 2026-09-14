package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const realtimeRedisChannel = "scrcpycat:realtime"

type redisEnvelope struct {
	Source       string         `json:"source"`
	TargetDevice string         `json:"target_device,omitempty"`
	Broadcast    bool           `json:"broadcast,omitempty"`
	Message      map[string]any `json:"message"`
}

type redisBus struct {
	client *redis.Client
	hub    *realtimeHub
	id     string
	cancel context.CancelFunc
	mu     sync.RWMutex
	err    error
}

func newRedisBus(ctx context.Context, rawURL string, hub *realtimeHub) (*redisBus, error) {
	if rawURL == "" {
		return nil, errors.New("SCRCPYCAT_REDIS_URL is required")
	}
	options, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis URL: %w", err)
	}
	client := redis.NewClient(options)
	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("connect redis: %w", err)
	}
	loopCtx, cancel := context.WithCancel(context.Background())
	bus := &redisBus{client: client, hub: hub, id: randomID(), cancel: cancel}
	go bus.consume(loopCtx)
	return bus, nil
}

func (b *redisBus) consume(ctx context.Context) {
	pubsub := b.client.Subscribe(ctx, realtimeRedisChannel)
	defer pubsub.Close()
	channel := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case message, ok := <-channel:
			if !ok {
				return
			}
			var envelope redisEnvelope
			if err := json.Unmarshal([]byte(message.Payload), &envelope); err != nil {
				continue
			}
			if envelope.Source == b.id {
				continue
			}
			if envelope.Broadcast {
				b.hub.broadcast(envelope.Message)
			}
			if envelope.TargetDevice != "" {
				b.hub.sendAgent(envelope.TargetDevice, envelope.Message)
			}
		}
	}
}

func (b *redisBus) publish(envelope redisEnvelope) {
	if b == nil {
		return
	}
	envelope.Source = b.id
	raw, err := json.Marshal(envelope)
	if err == nil {
		err = b.client.Publish(context.Background(), realtimeRedisChannel, raw).Err()
	}
	b.mu.Lock()
	b.err = err
	b.mu.Unlock()
}

func (b *redisBus) PublishAgent(deviceID string, message map[string]any) {
	b.publish(redisEnvelope{TargetDevice: deviceID, Message: message})
}
func (b *redisBus) PublishBroadcast(message map[string]any) {
	b.publish(redisEnvelope{Broadcast: true, Message: message})
}
func (b *redisBus) Healthy() error {
	if b == nil {
		return nil
	}
	if err := b.client.Ping(context.Background()).Err(); err != nil {
		return err
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.err
}
func (b *redisBus) Close() {
	if b != nil {
		b.cancel()
		_ = b.client.Close()
	}
}

var _ = time.Second
