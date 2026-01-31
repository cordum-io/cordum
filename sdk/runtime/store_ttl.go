package runtime

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// TTLRedisBlobStore is a Redis-backed blob store that applies a TTL on Set.
type TTLRedisBlobStore struct {
	client *redis.Client
	ttl    time.Duration
}

// NewRedisBlobStoreWithTTL creates a Redis-backed blob store with a TTL for stored values.
func NewRedisBlobStoreWithTTL(redisURL string, ttl time.Duration) (*TTLRedisBlobStore, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}
	client := redis.NewClient(opts)
	return &TTLRedisBlobStore{client: client, ttl: ttl}, nil
}

// NewRedisBlobStoreWithTTLFromEnv creates a TTL store using REDIS_URL if redisURL is empty.
func NewRedisBlobStoreWithTTLFromEnv(redisURL string, ttl time.Duration) (*TTLRedisBlobStore, error) {
	url := strings.TrimSpace(redisURL)
	if url == "" {
		url = strings.TrimSpace(os.Getenv("REDIS_URL"))
	}
	if url == "" {
		url = "redis://127.0.0.1:6379/0"
	}
	return NewRedisBlobStoreWithTTL(url, ttl)
}

// Get fetches a payload from Redis.
func (r *TTLRedisBlobStore) Get(ctx context.Context, key string) ([]byte, error) {
	return r.client.Get(ctx, key).Bytes()
}

// Set stores a payload in Redis with TTL.
func (r *TTLRedisBlobStore) Set(ctx context.Context, key string, data []byte) error {
	return r.client.Set(ctx, key, data, r.ttl).Err()
}

// Close closes the Redis client.
func (r *TTLRedisBlobStore) Close() error {
	return r.client.Close()
}
