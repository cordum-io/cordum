package schema

import (
	"bytes"
	"context"
	"fmt"
	"io"
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

// NewRegistryFromClient constructs a schema registry from a shared Redis client.
func NewRegistryFromClient(client redis.UniversalClient) *Registry {
	return &Registry{client: client}
}

// Close closes the underlying Redis client.
func (r *Registry) Close() error {
	if r == nil || r.client == nil {
		return nil
	}
	if err := r.client.Close(); err != nil {
		return fmt.Errorf("close schema registry: %w", err)
	}
	return nil
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
	if err != nil {
		return fmt.Errorf("register schema %s: %w", id, err)
	}
	return nil
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
	data, err := r.client.Get(ctx, schemaKey(id)).Bytes()
	if err != nil {
		return nil, fmt.Errorf("get schema %s: %w", id, err)
	}
	return data, nil
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
	if err != nil {
		return fmt.Errorf("delete schema %s: %w", id, err)
	}
	return nil
}

// List returns recent schema ids.
func (r *Registry) List(ctx context.Context, limit int64) ([]string, error) {
	if r == nil || r.client == nil {
		return nil, fmt.Errorf("registry unavailable")
	}
	if limit <= 0 {
		limit = 100
	}
	ids, err := r.client.ZRevRange(ctx, schemaIndexKey(), 0, limit-1).Result()
	if err != nil {
		return nil, fmt.Errorf("list schemas: %w", err)
	}
	return ids, nil
}

// ValidateID validates payload against a stored schema.
// It uses a resolver that looks up $ref URLs via the registry's URL aliases,
// enabling cross-schema references between schemas registered in the same system.
func (r *Registry) ValidateID(ctx context.Context, id string, value any) error {
	schema, err := r.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("load schema %s: %w", id, err)
	}
	resolve := func(url string) (io.ReadCloser, error) {
		data, err := r.GetByURL(ctx, url)
		if err != nil {
			return nil, fmt.Errorf("resolve $ref %s: %w", url, err)
		}
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	return ValidateSchemaWithResolver(schemaID(id), schema, value, resolve)
}

// RegisterURL stores schema bytes keyed by a $id URL (e.g., https://cordum.io/schemas/...).
// This allows $ref resolution to find schemas by their JSON Schema $id URL.
func (r *Registry) RegisterURL(ctx context.Context, url string, schema []byte) error {
	if r == nil || r.client == nil {
		return fmt.Errorf("registry unavailable")
	}
	url = strings.TrimSpace(url)
	if url == "" {
		return fmt.Errorf("schema url required")
	}
	if len(schema) == 0 {
		return fmt.Errorf("schema body required")
	}
	if err := r.client.Set(ctx, schemaURLKey(url), schema, 0).Err(); err != nil {
		return fmt.Errorf("register schema url %s: %w", url, err)
	}
	return nil
}

// GetByURL retrieves schema bytes by their $id URL.
func (r *Registry) GetByURL(ctx context.Context, url string) ([]byte, error) {
	if r == nil || r.client == nil {
		return nil, fmt.Errorf("registry unavailable")
	}
	url = strings.TrimSpace(url)
	if url == "" {
		return nil, fmt.Errorf("schema url required")
	}
	data, err := r.client.Get(ctx, schemaURLKey(url)).Bytes()
	if err != nil {
		return nil, fmt.Errorf("get schema by url %s: %w", url, err)
	}
	return data, nil
}

// DeleteURL removes the URL alias for a schema.
func (r *Registry) DeleteURL(ctx context.Context, url string) error {
	if r == nil || r.client == nil {
		return fmt.Errorf("registry unavailable")
	}
	url = strings.TrimSpace(url)
	if url == "" {
		return fmt.Errorf("schema url required")
	}
	if err := r.client.Del(ctx, schemaURLKey(url)).Err(); err != nil {
		return fmt.Errorf("delete schema url %s: %w", url, err)
	}
	return nil
}

func schemaKey(id string) string {
	return "schema:" + id
}

func schemaURLKey(url string) string {
	return "schema:url:" + url
}

func schemaIndexKey() string {
	return "schema:index"
}
