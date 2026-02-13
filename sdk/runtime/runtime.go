package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	capruntime "github.com/cordum-io/cap/v2/sdk/go/runtime"
	"github.com/redis/go-redis/v9"
)

type (
	Agent                      = capruntime.Agent
	BlobStore                  = capruntime.BlobStore
	Context                    = capruntime.Context
	Handler[TIn any, TOut any] = capruntime.Handler[TIn, TOut]
	InMemoryBlobStore          = capruntime.InMemoryBlobStore
	JobOption                  = capruntime.JobOption
	NATSConn                   = capruntime.NATSConn
	RedisBlobStore             = capruntime.RedisBlobStore
)

// Register wires a typed handler to a topic using the CAP runtime.
func Register[TIn any, TOut any](agent *Agent, topic string, handler Handler[TIn, TOut], opts ...JobOption) {
	capruntime.Register(agent, topic, handler, opts...)
}

// WithRetries overrides the default retry count for a handler.
func WithRetries(retries int) JobOption {
	return capruntime.WithRetries(retries)
}

// NewRedisBlobStore creates a Redis-backed blob store.
func NewRedisBlobStore(redisURL string) (*RedisBlobStore, error) {
	return capruntime.NewRedisBlobStore(redisURL)
}

// NewRedisBlobStoreWithPing creates a Redis-backed blob store and immediately
// verifies the connection with a PING. This surfaces auth failures (NOAUTH)
// at startup rather than on the first job.
func NewRedisBlobStoreWithPing(redisURL string) (*RedisBlobStore, error) {
	store, err := capruntime.NewRedisBlobStore(redisURL)
	if err != nil {
		return nil, err
	}
	if err := PingRedis(redisURL); err != nil {
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}
	return store, nil
}

// PingRedis verifies connectivity and auth to a Redis instance.
func PingRedis(redisURL string) error {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return fmt.Errorf("parse redis url: %w", err)
	}
	client := redis.NewClient(opts)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return client.Ping(ctx).Err()
}

// ValidateRedisURL logs a warning if the URL appears to be missing auth credentials.
func ValidateRedisURL(redisURL string) bool {
	return strings.Contains(redisURL, "@")
}

// NewInMemoryBlobStore returns a new in-memory blob store.
func NewInMemoryBlobStore() *InMemoryBlobStore {
	return capruntime.NewInMemoryBlobStore()
}
