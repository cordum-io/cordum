package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/redisutil"
	"github.com/redis/go-redis/v9"
)

const (
	defaultRedisURL = "redis://localhost:6379"
	pointerPrefix   = "redis://"
	// data TTL guards against unbounded Redis growth; configurable via env.
	defaultDataTTL           = 24 * time.Hour
	defaultRedisOpTimeout    = 2 * time.Second
	envRedisDataTTLInSeconds = "REDIS_DATA_TTL_SECONDS"
	envRedisDataTTLFallback  = "REDIS_DATA_TTL" // accepts ParseDuration values (e.g. 24h)
)

// Store defines access to the memory fabric for contexts and results.
type Store interface {
	PutContext(ctx context.Context, key string, data []byte) error
	GetContext(ctx context.Context, key string) ([]byte, error)
	PutResult(ctx context.Context, key string, data []byte) error
	GetResult(ctx context.Context, key string) ([]byte, error)
	Close() error
}

// RedisStore implements Store using Redis.
type RedisStore struct {
	client  redis.UniversalClient
	dataTTL time.Duration
}

// NewRedisStore constructs a Redis-backed store from a redis:// URL.
func NewRedisStore(url string) (*RedisStore, error) {
	if url == "" {
		url = defaultRedisURL
	}

	ttl := defaultDataTTL
	if ttlSeconds := os.Getenv(envRedisDataTTLInSeconds); ttlSeconds != "" {
		if secs, err := strconv.Atoi(ttlSeconds); err == nil && secs > 0 {
			ttl = time.Duration(secs) * time.Second
		}
	}
	if ttlEnv := os.Getenv(envRedisDataTTLFallback); ttlEnv != "" {
		if parsed, err := time.ParseDuration(ttlEnv); err == nil && parsed > 0 {
			ttl = parsed
		}
	}

	client, err := redisutil.NewClient(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("connect redis: %w", err)
	}

	return &RedisStore{client: client, dataTTL: ttl}, nil
}

func (s *RedisStore) PutContext(ctx context.Context, key string, data []byte) error {
	if ctx == nil {
		ctx = context.Background()
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultRedisOpTimeout)
	defer cancel()
	return s.client.Set(cctx, key, data, s.dataTTL).Err()
}

func (s *RedisStore) GetContext(ctx context.Context, key string) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultRedisOpTimeout)
	defer cancel()
	val, err := s.client.Get(cctx, key).Bytes()
	if err != nil {
		return nil, err
	}
	return val, nil
}

func (s *RedisStore) PutResult(ctx context.Context, key string, data []byte) error {
	if ctx == nil {
		ctx = context.Background()
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultRedisOpTimeout)
	defer cancel()
	return s.client.Set(cctx, key, data, s.dataTTL).Err()
}

func (s *RedisStore) GetResult(ctx context.Context, key string) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultRedisOpTimeout)
	defer cancel()
	val, err := s.client.Get(cctx, key).Bytes()
	if err != nil {
		return nil, err
	}
	return val, nil
}

// Close closes the underlying Redis client.
func (s *RedisStore) Close() error {
	return s.client.Close()
}

// Client exposes the underlying Redis client for advanced operations (lists/sets/etc).
// Prefer using Store methods where possible.
func (s *RedisStore) Client() redis.UniversalClient {
	return s.client
}

// MakeContextKey constructs the context key for a given job ID.
func MakeContextKey(jobID string) string {
	return "ctx:" + jobID
}

// MakeResultKey constructs the result key for a given job ID.
func MakeResultKey(jobID string) string {
	return "res:" + jobID
}

// PointerForKey formats a Redis key as a redis:// pointer.
func PointerForKey(key string) string {
	return pointerPrefix + key
}

// KeyFromPointer parses a redis:// pointer and returns the key component.
func KeyFromPointer(ptr string) (string, error) {
	if ptr == "" {
		return "", errors.New("empty pointer")
	}
	if !strings.HasPrefix(ptr, pointerPrefix) {
		return "", fmt.Errorf("invalid pointer prefix: %s", ptr)
	}
	key := strings.TrimPrefix(ptr, pointerPrefix)
	if key == "" {
		return "", errors.New("missing key in pointer")
	}
	return key, nil
}
