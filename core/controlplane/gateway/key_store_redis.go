package gateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

// RedisKeyStore implements KeyStore using Redis for persistence.
type RedisKeyStore struct {
	client *redis.Client
}

// NewRedisKeyStore creates a new Redis-backed API key store.
func NewRedisKeyStore(redisURL string) (*RedisKeyStore, error) {
	opts, err := redis.ParseURL(redisURL)
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

// Close closes the Redis connection.
func (s *RedisKeyStore) Close() error {
	return s.client.Close()
}

func keyRecordKey(id string) string   { return apiKeyPrefix + id }
func keyLookupKey(hash string) string { return apiKeyLookupPrefix + hash }
func keyTenantKey(tenant string) string {
	if tenant == "" {
		tenant = "default"
	}
	return apiKeyTenantPrefix + tenant
}

// GenerateRawKey creates a cryptographically random API key with the ck_ prefix.
func GenerateRawKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate key: %w", err)
	}
	return "ck_" + hex.EncodeToString(b), nil
}

// sha256Hex returns the hex-encoded SHA-256 hash of a string.
func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
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
	_, _ = pipe.Exec(ctx)

	keys := make([]*ManagedKey, 0, len(ids))
	for _, cmd := range cmds {
		raw, err := cmd.Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			continue
		}
		var mk ManagedKey
		if err := json.Unmarshal([]byte(raw), &mk); err != nil {
			continue
		}
		if mk.Revoked {
			continue
		}
		keys = append(keys, &mk)
	}
	return keys, nil
}

// Create stores a new managed API key in Redis.
// The rawSecret is hashed with bcrypt for storage and SHA-256 for the lookup index.
func (s *RedisKeyStore) Create(ctx context.Context, key *ManagedKey, rawSecret string) error {
	if key.ID == "" {
		key.ID = uuid.NewString()
	}
	if key.Prefix == "" && len(rawSecret) >= 11 {
		key.Prefix = rawSecret[:11] // "ck_" + 8 hex chars
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(rawSecret), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash key: %w", err)
	}
	key.KeyHash = string(hash)

	lookupHash := sha256Hex(rawSecret)
	key.LookupHash = lookupHash

	data, err := json.Marshal(key)
	if err != nil {
		return fmt.Errorf("marshal key: %w", err)
	}

	pipe := s.client.TxPipeline()
	pipe.Set(ctx, keyRecordKey(key.ID), data, 0)
	pipe.Set(ctx, keyLookupKey(lookupHash), key.ID, 0)
	pipe.SAdd(ctx, keyTenantKey(key.Tenant), key.ID)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("create key: %w", err)
	}
	return nil
}

// Revoke marks a key as revoked and removes its lookup index so it stops working immediately.
// The tenant parameter ensures callers can only revoke keys belonging to their own tenant.
func (s *RedisKeyStore) Revoke(ctx context.Context, id string, tenant string) error {
	raw, err := s.client.Get(ctx, keyRecordKey(id)).Result()
	if err == redis.Nil {
		return fmt.Errorf("key not found")
	}
	if err != nil {
		return fmt.Errorf("get key: %w", err)
	}

	var mk ManagedKey
	if err := json.Unmarshal([]byte(raw), &mk); err != nil {
		return fmt.Errorf("unmarshal key: %w", err)
	}
	if mk.Tenant != tenant {
		return fmt.Errorf("key not found")
	}
	mk.Revoked = true

	data, err := json.Marshal(&mk)
	if err != nil {
		return fmt.Errorf("marshal key: %w", err)
	}

	pipe := s.client.TxPipeline()
	pipe.Set(ctx, keyRecordKey(id), data, 0)
	pipe.Del(ctx, keyLookupKey(mk.LookupHash))
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("revoke key: %w", err)
	}
	return nil
}

// ValidateKey checks a raw API key against the store.
// Returns the ManagedKey if valid, or an error if not found, revoked, or expired.
func (s *RedisKeyStore) ValidateKey(ctx context.Context, rawKey string) (*ManagedKey, error) {
	lookupHash := sha256Hex(rawKey)
	id, err := s.client.Get(ctx, keyLookupKey(lookupHash)).Result()
	if err == redis.Nil {
		return nil, fmt.Errorf("key not found")
	}
	if err != nil {
		return nil, fmt.Errorf("lookup key: %w", err)
	}

	raw, err := s.client.Get(ctx, keyRecordKey(id)).Result()
	if err != nil {
		return nil, fmt.Errorf("get key record: %w", err)
	}

	var mk ManagedKey
	if err := json.Unmarshal([]byte(raw), &mk); err != nil {
		return nil, fmt.Errorf("unmarshal key: %w", err)
	}

	if mk.Revoked {
		return nil, fmt.Errorf("key revoked")
	}
	if !mk.ExpiresAt.IsZero() && time.Now().After(mk.ExpiresAt) {
		return nil, fmt.Errorf("key expired")
	}

	// Verify with bcrypt for full security (SHA-256 is just for fast lookup)
	if err := bcrypt.CompareHashAndPassword([]byte(mk.KeyHash), []byte(rawKey)); err != nil {
		return nil, fmt.Errorf("key mismatch")
	}

	return &mk, nil
}

// RecordUsage increments the usage counter and updates the last-used timestamp.
func (s *RedisKeyStore) RecordUsage(ctx context.Context, id string) error {
	raw, err := s.client.Get(ctx, keyRecordKey(id)).Result()
	if err != nil {
		return fmt.Errorf("get key: %w", err)
	}

	var mk ManagedKey
	if err := json.Unmarshal([]byte(raw), &mk); err != nil {
		return fmt.Errorf("unmarshal key: %w", err)
	}

	mk.UsageCount++
	mk.LastUsed = time.Now().UTC()

	data, err := json.Marshal(&mk)
	if err != nil {
		return fmt.Errorf("marshal key: %w", err)
	}

	if err := s.client.Set(ctx, keyRecordKey(id), data, 0).Err(); err != nil {
		slog.Warn("failed to record key usage", "key_id", id, "error", err)
		return err
	}
	return nil
}
