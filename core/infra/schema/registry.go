package schema

import (
	"bytes"
	"context"
	"errors"
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

// ErrIDNotAvailable refuses a tenant write to an id it cannot own.
var ErrIDNotAvailable = errors.New("schema id not available")

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
	pipe.Del(ctx, ownerKey(id))
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("delete schema %s: %w", id, err)
	}
	return nil
}

// Owner returns the tenant that owns a schema id, or "" when no tenant owns it.
func (r *Registry) Owner(ctx context.Context, id string) (string, error) {
	if r == nil || r.client == nil {
		return "", fmt.Errorf("registry unavailable")
	}
	id = strings.TrimSpace(id)
	owner, err := r.client.Get(ctx, ownerKey(id)).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get schema owner %s: %w", id, err)
	}
	return owner, nil
}

// Owners returns the owner tenant of each id, in order, with "" for an id no
// tenant owns.
func (r *Registry) Owners(ctx context.Context, ids []string) ([]string, error) {
	if r == nil || r.client == nil {
		return nil, fmt.Errorf("registry unavailable")
	}
	owners := make([]string, len(ids))
	if len(ids) == 0 {
		return owners, nil
	}
	pipe := r.client.Pipeline()
	cmds := make([]*redis.StringCmd, len(ids))
	for i, id := range ids {
		cmds[i] = pipe.Get(ctx, ownerKey(id))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("get schema owners: %w", err)
	}
	for i, cmd := range cmds {
		owner, err := cmd.Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return nil, fmt.Errorf("get schema owner %s: %w", ids[i], err)
		}
		owners[i] = owner
	}
	return owners, nil
}

// RegisterOwned stores a document and its owner in one owner-checked step.
func (r *Registry) RegisterOwned(ctx context.Context, id, tenant string, schema []byte) error {
	if r == nil || r.client == nil {
		return fmt.Errorf("registry unavailable")
	}
	id = strings.TrimSpace(id)
	if id == "" || len(schema) == 0 {
		return fmt.Errorf("schema id and body required")
	}
	err := redisutil.Retry(ctx, r.client, func(tx *redis.Tx) error {
		owner, exists, err := ownedState(ctx, tx, id)
		if err != nil {
			return err
		}
		pipe := tx.TxPipeline()
		// Even a refusal validates WATCH without changing stored state.
		pipe.Exists(ctx, schemaKey(id))
		var refusal error
		if tenant == "" || (owner != tenant && (owner != "" || exists)) {
			refusal = ErrIDNotAvailable
		} else {
			pipe.Set(ctx, schemaKey(id), schema, 0)
			pipe.Set(ctx, ownerKey(id), tenant, 0)
			pipe.ZAdd(ctx, schemaIndexKey(), redis.Z{Score: float64(time.Now().Unix()), Member: id})
			pipe.ZRemRangeByRank(ctx, schemaIndexKey(), 0, -schemaIndexMaxLen-1)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return err
		}
		return refusal
	}, redisutil.WithKeys(ownerKey(id), schemaKey(id)))
	if err != nil {
		return fmt.Errorf("register schema %s: %w", id, err)
	}
	return nil
}

// DeleteOwned removes a document, owner and index entry in one checked step.
func (r *Registry) DeleteOwned(ctx context.Context, id, tenant string) error {
	if r == nil || r.client == nil {
		return fmt.Errorf("registry unavailable")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("schema id required")
	}
	err := redisutil.Retry(ctx, r.client, func(tx *redis.Tx) error {
		owner, exists, err := ownedState(ctx, tx, id)
		if err != nil {
			return err
		}
		pipe := tx.TxPipeline()
		pipe.Exists(ctx, schemaKey(id))
		var refusal error
		if tenant != "" && owner == tenant {
			pipe.Del(ctx, schemaKey(id), ownerKey(id))
			pipe.ZRem(ctx, schemaIndexKey(), id)
		} else if owner == "" && exists {
			refusal = ErrIDNotAvailable
		} else {
			refusal = redis.Nil
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return err
		}
		return refusal
	}, redisutil.WithKeys(ownerKey(id), schemaKey(id)))
	if err != nil {
		return fmt.Errorf("delete schema %s: %w", id, err)
	}
	return nil
}

func ownedState(ctx context.Context, tx *redis.Tx, id string) (string, bool, error) {
	owner, err := tx.Get(ctx, ownerKey(id)).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return "", false, err
	}
	exists, err := tx.Exists(ctx, schemaKey(id)).Result()
	return owner, exists != 0, err
}

// GetOwned reads the owner and document together, returning only visible bytes.
func (r *Registry) GetOwned(ctx context.Context, id, tenant string) ([]byte, error) {
	if r == nil || r.client == nil {
		return nil, fmt.Errorf("registry unavailable")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("schema id required")
	}
	pipe := r.client.TxPipeline()
	owner := pipe.Get(ctx, ownerKey(id))
	doc := pipe.Get(ctx, schemaKey(id))
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("get schema %s: %w", id, err)
	}
	if err := owner.Err(); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("get schema owner %s: %w", id, err)
	}
	if owner.Val() != "" && owner.Val() != tenant {
		return nil, fmt.Errorf("get schema %s: %w", id, redis.Nil)
	}
	data, err := doc.Bytes()
	if err != nil {
		return nil, fmt.Errorf("get schema %s: %w", id, err)
	}
	return data, nil
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

func ownerKey(id string) string {
	return "cordum:schema:owner:{" + schemaKey(id) + "}"
}

func schemaURLKey(url string) string {
	return "schema:url:" + url
}

func schemaIndexKey() string {
	return "schema:index"
}
