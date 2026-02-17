package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
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

	// CAP v2.5.2 types
	Handshake     = agentv1.Handshake
	ComponentRole = agentv1.ComponentRole
	ErrorCode     = agentv1.ErrorCode
	AlertSeverity = agentv1.AlertSeverity
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

// NewRedisBlobStoreWithTLS creates a Redis-backed blob store that applies
// REDIS_TLS_* environment variables (CA cert, client cert/key, server name).
// This is required when Redis uses TLS with a custom CA (e.g. self-signed
// certs from cordumctl generate-certs). Without this, the rediss:// scheme
// provides only a basic TLS config that trusts system CAs, which fails with
// custom CAs.
func NewRedisBlobStoreWithTLS(redisURL string) (BlobStore, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	tlsCfg, tlsErr := RedisTLSConfigFromEnv()
	if tlsErr != nil {
		return nil, fmt.Errorf("redis tls config: %w", tlsErr)
	}
	if tlsCfg != nil {
		opts.TLSConfig = tlsCfg
	}
	client := redis.NewClient(opts)
	return &redisBlobStore{client: client}, nil
}

// NewRedisBlobStoreWithPing creates a Redis-backed blob store with TLS support
// and immediately verifies the connection with a PING. This surfaces auth
// and TLS failures (NOAUTH, certificate errors) at startup.
func NewRedisBlobStoreWithPing(redisURL string) (BlobStore, error) {
	store, err := NewRedisBlobStoreWithTLS(redisURL)
	if err != nil {
		return nil, err
	}
	if err := PingRedis(redisURL); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}
	return store, nil
}

// redisBlobStore is a Redis-backed blob store that supports custom TLS config.
type redisBlobStore struct {
	client *redis.Client
}

func (r *redisBlobStore) Get(ctx context.Context, key string) ([]byte, error) {
	return r.client.Get(ctx, key).Bytes()
}

func (r *redisBlobStore) Set(ctx context.Context, key string, data []byte) error {
	return r.client.Set(ctx, key, data, 0).Err()
}

func (r *redisBlobStore) Close() error {
	return r.client.Close()
}

// PingRedis verifies connectivity and auth to a Redis instance.
// It applies REDIS_TLS_* env vars when the URL uses the rediss:// scheme.
func PingRedis(redisURL string) error {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return fmt.Errorf("parse redis url: %w", err)
	}
	tlsCfg, tlsErr := RedisTLSConfigFromEnv()
	if tlsErr != nil {
		return fmt.Errorf("redis tls config: %w", tlsErr)
	}
	if tlsCfg != nil {
		opts.TLSConfig = tlsCfg
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
