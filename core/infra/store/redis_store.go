package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

// sanitizeLogValue strips newlines and control characters from a string
// before it is interpolated into a structured log call.  This prevents
// log-injection attacks when the value originates from an environment
// variable or other external source.
func sanitizeLogValue(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return ' '
		}
		if r < 0x20 && r != ' ' {
			return -1 // drop other control characters
		}
		return r
	}, s)
}

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

	ttl := redisStoreDataTTL()
	client, err := redisutil.NewClient(url)
	if err != nil {
		return nil, fmt.Errorf("create redis client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("connect redis: %w", err)
	}

	slog.Debug("redis store connected", "component", "store", "dataTTL", ttl.String())
	return &RedisStore{client: client, dataTTL: ttl}, nil
}

// NewRedisStoreFromClient constructs a Redis-backed store from a shared client.
func NewRedisStoreFromClient(client redis.UniversalClient) *RedisStore {
	return &RedisStore{client: client, dataTTL: redisStoreDataTTL()}
}

func redisStoreDataTTL() time.Duration {
	ttl := defaultDataTTL
	if ttlSeconds := os.Getenv(envRedisDataTTLInSeconds); ttlSeconds != "" {
		secs, err := strconv.Atoi(ttlSeconds)
		if err != nil {
			slog.Warn("invalid "+envRedisDataTTLInSeconds+", using default", "value", sanitizeLogValue(ttlSeconds), "error", sanitizeLogValue(err.Error()), "default", defaultDataTTL) // #nosec -- structured log, sanitized
		} else if secs <= 0 {
			slog.Warn("non-positive "+envRedisDataTTLInSeconds+", using default", "value", secs, "default", defaultDataTTL) // #nosec -- structured log, int value
		} else {
			ttl = time.Duration(secs) * time.Second
		}
	}
	if ttlEnv := os.Getenv(envRedisDataTTLFallback); ttlEnv != "" {
		parsed, err := time.ParseDuration(ttlEnv)
		if err != nil {
			slog.Warn("invalid "+envRedisDataTTLFallback+", using default", "value", sanitizeLogValue(ttlEnv), "error", sanitizeLogValue(err.Error()), "default", defaultDataTTL) // #nosec -- structured log, sanitized
		} else if parsed <= 0 {
			slog.Warn("non-positive "+envRedisDataTTLFallback+", using default", "value", sanitizeLogValue(ttlEnv), "default", defaultDataTTL) // #nosec G115 G706 -- ttlEnv already sanitized above
		} else {
			ttl = parsed
		}
	}
	return ttl
}

func (s *RedisStore) PutContext(ctx context.Context, key string, data []byte) error {
	if s == nil || s.client == nil {
		return errors.New("redis_store: client not initialized")
	}
	if ctx == nil {
		return errors.New("redis_store: PutContext: nil context")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("redis_store: PutContext: %w", err)
	}
	cctx, cancel := context.WithTimeout(ctx, defaultRedisOpTimeout)
	defer cancel()
	if err := s.client.Set(cctx, key, data, s.dataTTL).Err(); err != nil {
		return fmt.Errorf("redis_store: put context %s: %w", key, err)
	}
	return nil
}

func (s *RedisStore) GetContext(ctx context.Context, key string) ([]byte, error) {
	if s == nil || s.client == nil {
		return nil, errors.New("redis_store: client not initialized")
	}
	if ctx == nil {
		return nil, errors.New("redis_store: GetContext: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("redis_store: GetContext: %w", err)
	}
	cctx, cancel := context.WithTimeout(ctx, defaultRedisOpTimeout)
	defer cancel()
	val, err := s.client.Get(cctx, key).Bytes()
	if err != nil {
		return nil, err
	}
	return val, nil
}

func (s *RedisStore) PutResult(ctx context.Context, key string, data []byte) error {
	if s == nil || s.client == nil {
		return errors.New("redis_store: client not initialized")
	}
	if ctx == nil {
		return errors.New("redis_store: PutResult: nil context")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("redis_store: PutResult: %w", err)
	}
	cctx, cancel := context.WithTimeout(ctx, defaultRedisOpTimeout)
	defer cancel()
	if err := s.client.Set(cctx, key, data, s.dataTTL).Err(); err != nil {
		return fmt.Errorf("redis_store: put result %s: %w", key, err)
	}
	return nil
}

func (s *RedisStore) GetResult(ctx context.Context, key string) ([]byte, error) {
	if s == nil || s.client == nil {
		return nil, errors.New("redis_store: client not initialized")
	}
	if ctx == nil {
		return nil, errors.New("redis_store: GetResult: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("redis_store: GetResult: %w", err)
	}
	cctx, cancel := context.WithTimeout(ctx, defaultRedisOpTimeout)
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
