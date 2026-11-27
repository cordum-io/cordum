package artifacts

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrDigestMismatch means stored bytes no longer match the requested digest.
var ErrDigestMismatch = errors.New("content digest mismatch")

// ContentStore stores exact bytes under tenant-scoped SHA-256 digests.
// Callers must retain every input through the lifetime of any referencing grant.
type ContentStore interface {
	PutContent(ctx context.Context, tenant string, content []byte, retention RetentionClass) ([sha256.Size]byte, error)
	GetContent(ctx context.Context, tenant string, digest [sha256.Size]byte) ([]byte, error)
	RetainContent(ctx context.Context, tenant string, digest [sha256.Size]byte, until time.Time) error
}

var _ ContentStore = (*RedisStore)(nil)

// Both operations are atomic with respect to expiry and concurrent writers.
var putContentScript = redis.NewScript(`
if not redis.call("SET", KEYS[1], ARGV[1], "NX", "PX", ARGV[2]) then
  redis.call("PEXPIRE", KEYS[1], ARGV[2], "GT")
end
return 1
`)

var retainContentScript = redis.NewScript(`
if redis.call("EXISTS", KEYS[1]) == 0 then
  return 0
end
redis.call("PEXPIREAT", KEYS[1], ARGV[1], "GT")
return 1
`)

// PutContent writes an object once; a duplicate may only lengthen its retention.
func (s *RedisStore) PutContent(ctx context.Context, tenant string, content []byte, retention RetentionClass) ([sha256.Size]byte, error) {
	if tenant == "" {
		return [sha256.Size]byte{}, fmt.Errorf("content tenant required")
	}
	if s == nil || s.client == nil {
		return [sha256.Size]byte{}, fmt.Errorf("artifact store unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	digest := sha256.Sum256(content)
	if err := putContentScript.Run(ctx, s.client, []string{contentKey(tenant, digest)}, content, s.ttlFor(retention).Milliseconds()).Err(); err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("put content: %w", err)
	}
	return digest, nil
}

// GetContent releases bytes only after verifying the requested digest.
func (s *RedisStore) GetContent(ctx context.Context, tenant string, digest [sha256.Size]byte) ([]byte, error) {
	if tenant == "" {
		return nil, fmt.Errorf("content tenant required")
	}
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("artifact store unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	content, err := s.client.Get(ctx, contentKey(tenant, digest)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrArtifactNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get content: %w", err)
	}
	if sha256.Sum256(content) != digest {
		return nil, ErrDigestMismatch
	}
	return content, nil
}

// RetainContent lengthens an existing object's expiry, never shortens it.
func (s *RedisStore) RetainContent(ctx context.Context, tenant string, digest [sha256.Size]byte, until time.Time) error {
	if tenant == "" {
		return fmt.Errorf("content tenant required")
	}
	if s == nil || s.client == nil {
		return fmt.Errorf("artifact store unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	found, err := retainContentScript.Run(ctx, s.client, []string{contentKey(tenant, digest)}, until.UnixMilli()).Int()
	if err != nil {
		return fmt.Errorf("retain content: %w", err)
	}
	if found == 0 {
		return ErrArtifactNotFound
	}
	return nil
}

func contentKey(tenant string, digest [sha256.Size]byte) string {
	return fmt.Sprintf("cas:%s:%x", tenant, digest)
}
