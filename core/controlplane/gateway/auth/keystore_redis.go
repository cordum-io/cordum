package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/cordum/cordum/core/infra/redisutil"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

// unmarshalManagedKey unmarshals a ManagedKey from JSON, fixing data corrupted
// by Lua cjson.encode which turns empty Go slices ("scopes":[]) into objects
// ("scopes":{}). This is a self-healing read: it tolerates the corrupt form.
func unmarshalManagedKey(raw []byte) (ManagedKey, error) {
	var mk ManagedKey
	if err := json.Unmarshal(raw, &mk); err != nil {
		// Attempt recovery: replace "scopes":{} with "scopes":[] and retry.
		fixed := bytes.Replace(raw, []byte(`"scopes":{}`), []byte(`"scopes":[]`), 1)
		if err2 := json.Unmarshal(fixed, &mk); err2 != nil {
			return mk, fmt.Errorf("unmarshal key: %w", err)
		}
		slog.Warn("self-healed corrupted scopes field in managed key", "key_id", mk.ID)
	}
	return mk, nil
}

// RedisKeyStore implements KeyStore using Redis for persistence.
type RedisKeyStore struct {
	client *redis.Client
}

// NewRedisKeyStore creates a new Redis-backed API key store.
func NewRedisKeyStore(redisURL string) (*RedisKeyStore, error) {
	opts, err := redisutil.ParseOptions(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	client := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return &RedisKeyStore{client: client}, nil
}

// NewRedisKeyStoreFromClient creates a RedisKeyStore using an existing Redis client.
func NewRedisKeyStoreFromClient(client *redis.Client) *RedisKeyStore {
	return &RedisKeyStore{client: client}
}

// Close closes the Redis connection.
func (s *RedisKeyStore) Close() error {
	return s.client.Close()
}

func keyRecordKey(id string) string { return apiKeyPrefix + id }
func keyTenantKey(tenant string) string {
	if tenant == "" {
		tenant = "default"
	}
	return apiKeyTenantPrefix + tenant
}
func keyPrefixIndexKey(prefix string) string { return apiKeyPrefixIndexPrefix + prefix }

const apiKeyPrefixLen = 11 // "ck_" + 8 hex chars

// ErrKeyNotFound is returned when a managed API key cannot be found.
var ErrKeyNotFound = errors.New("key not found")

// GenerateRawKey creates a cryptographically random API key with the ck_ prefix.
func GenerateRawKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate key: %w", err)
	}
	return "ck_" + hex.EncodeToString(b), nil
}
func rawKeyPrefix(rawKey string) (string, error) {
	if len(rawKey) < apiKeyPrefixLen {
		return "", fmt.Errorf("invalid key length")
	}
	return rawKey[:apiKeyPrefixLen], nil
}

// List returns all non-revoked managed keys for a tenant.
func (s *RedisKeyStore) List(ctx context.Context, tenant string) ([]*ManagedKey, error) {
	ids, err := s.client.SMembers(ctx, keyTenantKey(tenant)).Result()
	if err != nil {
		return nil, fmt.Errorf("list keys: %w", err)
	}
	if len(ids) == 0 {
		return []*ManagedKey{}, nil
	}

	pipe := s.client.Pipeline()
	cmds := make([]*redis.StringCmd, len(ids))
	for i, id := range ids {
		cmds[i] = pipe.Get(ctx, keyRecordKey(id))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("list keys pipeline: %w", err)
	}

	keys := make([]*ManagedKey, 0, len(ids))
	var errs []error
	for i, cmd := range cmds {
		raw, err := cmd.Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			slog.WarnContext(ctx, "list keys: failed to read key", "key_id", ids[i], "error", err)
			errs = append(errs, err)
			continue
		}
		mk, err := unmarshalManagedKey([]byte(raw))
		if err != nil {
			slog.WarnContext(ctx, "list keys: unmarshal failed", "key_id", ids[i], "error", err)
			continue
		}
		if mk.Revoked {
			continue
		}
		keys = append(keys, &mk)
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("list keys: %d commands failed: %w", len(errs), errors.Join(errs...))
	}
	return keys, nil
}

// Create stores a new managed API key in Redis.
// The rawSecret is hashed with bcrypt for storage; a prefix index enables lookup.
func (s *RedisKeyStore) Create(ctx context.Context, key *ManagedKey, rawSecret string) error {
	if key.ID == "" {
		key.ID = uuid.NewString()
	}
	if key.Prefix == "" {
		prefix, err := rawKeyPrefix(rawSecret)
		if err != nil {
			return fmt.Errorf("derive key prefix: %w", err)
		}
		key.Prefix = prefix
	} else if len(key.Prefix) < apiKeyPrefixLen {
		return fmt.Errorf("key prefix too short")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(rawSecret), bcryptCostFromEnv())
	if err != nil {
		return fmt.Errorf("hash key: %w", err)
	}
	key.KeyHash = string(hash)

	data, err := json.Marshal(key)
	if err != nil {
		return fmt.Errorf("marshal key: %w", err)
	}

	pipe := s.client.TxPipeline()
	pipe.Set(ctx, keyRecordKey(key.ID), data, 0)
	pipe.SAdd(ctx, keyPrefixIndexKey(key.Prefix), key.ID)
	pipe.SAdd(ctx, keyTenantKey(key.Tenant), key.ID)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("create key: %w", err)
	}
	return nil
}

// Revoke marks a key as revoked and removes its lookup index so it stops working immediately.
// The tenant parameter ensures callers can only revoke keys belonging to their own tenant.
func (s *RedisKeyStore) Revoke(ctx context.Context, id string, tenant string) error {
	txFunc := func(tx *redis.Tx) error {
		raw, err := tx.Get(ctx, keyRecordKey(id)).Result()
		if err == redis.Nil {
			return ErrKeyNotFound
		}
		if err != nil {
			return fmt.Errorf("get key: %w", err)
		}

		mk, err := unmarshalManagedKey([]byte(raw))
		if err != nil {
			return err
		}
		if mk.Tenant != tenant {
			return ErrKeyNotFound
		}
		mk.Revoked = true

		data, err := json.Marshal(&mk)
		if err != nil {
			return fmt.Errorf("marshal key: %w", err)
		}

		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, keyRecordKey(id), data, 0)
			if mk.Prefix != "" {
				pipe.SRem(ctx, keyPrefixIndexKey(mk.Prefix), id)
			}
			return nil
		})
		return err
	}

	// task-c7e419d8: Redis CAS retry loop extracted into redisutil.Retry.
	// Default 3-attempt budget preserved. ErrKeyNotFound is not TxFailedErr,
	// so Retry returns it immediately — matches the old early-exit semantic.
	if err := redisutil.Retry(ctx, s.client, txFunc, redisutil.WithKeys(keyRecordKey(id))); err != nil {
		if errors.Is(err, ErrKeyNotFound) {
			return err
		}
		if errors.Is(err, redisutil.ErrMaxAttemptsExceeded) {
			return fmt.Errorf("revoke key: too many retries")
		}
		return fmt.Errorf("revoke key: %w", err)
	}
	return nil
}

// ValidateKey checks a raw API key against the store.
// Returns the ManagedKey if valid, or an error if not found, revoked, or expired.
func (s *RedisKeyStore) ValidateKey(ctx context.Context, rawKey string) (*ManagedKey, error) {
	prefix, err := rawKeyPrefix(rawKey)
	if err != nil {
		return nil, fmt.Errorf("key not found")
	}

	ids, err := s.client.SMembers(ctx, keyPrefixIndexKey(prefix)).Result()
	if err != nil {
		return nil, fmt.Errorf("lookup key: %w", err)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("key not found")
	}

	pipe := s.client.Pipeline()
	cmds := make([]*redis.StringCmd, len(ids))
	for i, id := range ids {
		cmds[i] = pipe.Get(ctx, keyRecordKey(id))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("lookup key records: %w", err)
	}

	for i, cmd := range cmds {
		raw, err := cmd.Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("lookup key record: %w", err)
		}
		mk, err := unmarshalManagedKey([]byte(raw))
		if err != nil {
			slog.WarnContext(ctx, "validate key: unmarshal failed", "key_id", ids[i], "error", err)
			continue
		}

		// Verify with bcrypt for full security (prefix index is only a hint).
		if err := bcrypt.CompareHashAndPassword([]byte(mk.KeyHash), []byte(rawKey)); err != nil {
			continue
		}
		if mk.Revoked {
			return nil, fmt.Errorf("key revoked")
		}
		if !mk.ExpiresAt.IsZero() && time.Now().After(mk.ExpiresAt) {
			return nil, fmt.Errorf("key expired")
		}
		return &mk, nil
	}

	return nil, fmt.Errorf("key not found")
}

// recordUsageLua atomically increments usage_count and updates last_used
// inside the JSON blob, preventing lost updates under concurrent access.
// NOTE: Lua cjson.encode turns empty tables into "{}" (object) instead of
// "[]" (array). We must preserve the scopes field as an array so Go can
// unmarshal it back into []string. If scopes is nil or an empty table we
// explicitly encode it as a JSON array literal.
var recordUsageLua = redis.NewScript(`
local raw = redis.call('GET', KEYS[1])
if not raw then return redis.error_reply('key not found') end
local obj = cjson.decode(raw)
obj.usage_count = (obj.usage_count or 0) + 1
obj.last_used = ARGV[1]
local encoded = cjson.encode(obj)
-- Fix: cjson.encode turns empty arrays into "{}". Replace "scopes":{} with "scopes":[].
encoded = string.gsub(encoded, '"scopes":{}', '"scopes":[]')
redis.call('SET', KEYS[1], encoded)
return obj.usage_count
`)

// RecordUsage atomically increments the usage counter and updates the
// last-used timestamp using a Lua script to prevent lost updates.
func (s *RedisKeyStore) RecordUsage(ctx context.Context, id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	err := recordUsageLua.Run(ctx, s.client, []string{keyRecordKey(id)}, now).Err()
	if err != nil {
		slog.Warn("failed to record key usage", "key_id", id, "error", err)
		return fmt.Errorf("record usage: %w", err)
	}
	return nil
}
