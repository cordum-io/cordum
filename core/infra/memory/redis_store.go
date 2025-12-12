package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	defaultRedisURL = "redis://localhost:6379"
	pointerPrefix   = "redis://"
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
	client *redis.Client
}

// NewRedisStore constructs a Redis-backed store from a redis:// URL.
func NewRedisStore(url string) (*RedisStore, error) {
	if url == "" {
		url = defaultRedisURL
	}

	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}

	client := redis.NewClient(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("connect redis: %w", err)
	}

	return &RedisStore{client: client}, nil
}

func (s *RedisStore) PutContext(ctx context.Context, key string, data []byte) error {
	return s.client.Set(ctx, key, data, 0).Err()
}

func (s *RedisStore) GetContext(ctx context.Context, key string) ([]byte, error) {
	val, err := s.client.Get(ctx, key).Bytes()
	if err != nil {
		return nil, err
	}
	return val, nil
}

func (s *RedisStore) PutResult(ctx context.Context, key string, data []byte) error {
	return s.client.Set(ctx, key, data, 0).Err()
}

func (s *RedisStore) GetResult(ctx context.Context, key string) ([]byte, error) {
	val, err := s.client.Get(ctx, key).Bytes()
	if err != nil {
		return nil, err
	}
	return val, nil
}

// Close closes the underlying Redis client.
func (s *RedisStore) Close() error {
	return s.client.Close()
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
