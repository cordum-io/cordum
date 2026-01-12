package schema

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/redisutil"
	"github.com/redis/go-redis/v9"
)

const (
	defaultRedisURL   = "redis://localhost:6379"
	schemaIndexMaxLen = 500
)

// Registry stores JSON Schemas in Redis and validates payloads.
type Registry struct {
	client redis.UniversalClient
}

// NewRegistry constructs a Redis-backed schema registry.
func NewRegistry(url string) (*Registry, error) {
	if url == "" {
		url = defaultRedisURL
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
	return &Registry{client: client}, nil
}

// Close closes the underlying Redis client.
func (r *Registry) Close() error {
	if r == nil || r.client == nil {
		return nil
	}
	return r.client.Close()
}

// Register stores a schema by id.
func (r *Registry) Register(ctx context.Context, id string, schema []byte) error {
	if r == nil || r.client == nil {
		return fmt.Errorf("registry unavailable")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("schema id required")
	}
	if len(schema) == 0 {
		return fmt.Errorf("schema body required")
	}
	now := time.Now().UTC()
	pipe := r.client.TxPipeline()
	pipe.Set(ctx, schemaKey(id), schema, 0)
	pipe.ZAdd(ctx, schemaIndexKey(), redis.Z{Score: float64(now.Unix()), Member: id})
	pipe.ZRemRangeByRank(ctx, schemaIndexKey(), 0, -schemaIndexMaxLen-1)
	_, err := pipe.Exec(ctx)
	return err
}

// Get returns the raw schema bytes.
func (r *Registry) Get(ctx context.Context, id string) ([]byte, error) {
	if r == nil || r.client == nil {
		return nil, fmt.Errorf("registry unavailable")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("schema id required")
	}
	return r.client.Get(ctx, schemaKey(id)).Bytes()
}

// Delete removes a schema from the registry.
func (r *Registry) Delete(ctx context.Context, id string) error {
	if r == nil || r.client == nil {
		return fmt.Errorf("registry unavailable")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("schema id required")
	}
	pipe := r.client.TxPipeline()
	pipe.Del(ctx, schemaKey(id))
	pipe.ZRem(ctx, schemaIndexKey(), id)
	_, err := pipe.Exec(ctx)
	return err
}

// List returns recent schema ids.
func (r *Registry) List(ctx context.Context, limit int64) ([]string, error) {
	if r == nil || r.client == nil {
		return nil, fmt.Errorf("registry unavailable")
	}
	if limit <= 0 {
		limit = 100
	}
	return r.client.ZRevRange(ctx, schemaIndexKey(), 0, limit-1).Result()
}

// ValidateID validates payload against a stored schema.
func (r *Registry) ValidateID(ctx context.Context, id string, value any) error {
	schema, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	return ValidateSchema(schemaID(id), schema, value)
}

func schemaKey(id string) string {
	return "schema:" + id
}

func schemaIndexKey() string {
	return "schema:index"
}
