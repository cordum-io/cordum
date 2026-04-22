package delegation

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/redisutil"
	"github.com/redis/go-redis/v9"
)

const (
	delegationRevocationPrefix = "delegation:revoked:"
	delegationRevocationMarker = "1"
)

type RevocationStore interface {
	Revoke(ctx context.Context, jti string, expiresAt time.Time) error
	IsRevoked(ctx context.Context, jti string) (bool, error)
}

type RedisRevocationStore struct {
	client redis.UniversalClient
}

func NewRedisRevocationStore(url string) (*RedisRevocationStore, error) {
	client, err := redisutil.NewClient(url)
	if err != nil {
		return nil, fmt.Errorf("delegation revocation store: parse redis url: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("delegation revocation store: connect redis: %w", err)
	}
	return &RedisRevocationStore{client: client}, nil
}

func NewRedisRevocationStoreFromClient(client redis.UniversalClient) *RedisRevocationStore {
	if client == nil {
		return nil
	}
	return &RedisRevocationStore{client: client}
}

func (s *RedisRevocationStore) Revoke(ctx context.Context, jti string, expiresAt time.Time) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.client == nil {
		return fmt.Errorf("delegation revocation store unavailable")
	}
	jti = strings.TrimSpace(jti)
	if jti == "" {
		return fmt.Errorf("delegation jti required")
	}
	ttl := time.Until(expiresAt.UTC())
	if ttl <= 0 {
		ttl = time.Second
	}
	return s.client.Set(ctx, delegationRevocationKey(jti), delegationRevocationMarker, ttl).Err()
}

func (s *RedisRevocationStore) IsRevoked(ctx context.Context, jti string) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.client == nil {
		return false, nil
	}
	jti = strings.TrimSpace(jti)
	if jti == "" {
		return false, nil
	}
	exists, err := s.client.Exists(ctx, delegationRevocationKey(jti)).Result()
	if err != nil {
		return false, err
	}
	return exists > 0, nil
}

func delegationRevocationKey(jti string) string {
	return delegationRevocationPrefix + strings.TrimSpace(jti)
}
