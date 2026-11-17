package artifacts

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
)

func newContentTestStore(t *testing.T) (*RedisStore, *miniredis.Miniredis) {
	t.Helper()
	srv := miniredis.RunT(t)
	s, err := NewRedisStore("redis://" + srv.Addr())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	return s, srv
}

func TestContentStorePutTwiceKeepsOneObject(t *testing.T) {
	s, srv := newContentTestStore(t)
	ctx := context.Background()
	content := []byte("approved bytes")
	digest, err := s.PutContent(ctx, "tenant-a", content, RetentionAudit)
	require.NoError(t, err)
	require.Equal(t, sha256.Sum256(content), digest)
	key := fmt.Sprintf("cas:tenant-a:%x", digest)
	ttl := srv.TTL(key)
	srv.FastForward(time.Hour)
	again, err := s.PutContent(ctx, "tenant-a", content, RetentionShort)
	require.NoError(t, err)
	require.Equal(t, digest, again)
	require.Equal(t, []string{key}, srv.Keys())
	require.Equal(t, ttl-time.Hour, srv.TTL(key), "duplicate must not shorten TTL")
	got, err := s.GetContent(ctx, "tenant-a", digest)
	require.NoError(t, err)
	require.Equal(t, content, got)
	srv.FastForward(ttl)
	_, err = s.PutContent(ctx, "tenant-a", content, RetentionShort)
	require.NoError(t, err, "put recreates an expired object atomically")
	require.Equal(t, []string{key}, srv.Keys())
	_, err = s.PutContent(ctx, "tenant-a", content, RetentionAudit)
	require.NoError(t, err)
	require.Equal(t, ttl, srv.TTL(key), "duplicate must extend TTL when needed")
}

func TestContentStoreGetRefusesAlteredBytes(t *testing.T) {
	s, srv := newContentTestStore(t)
	ctx := context.Background()
	content := []byte("approved bytes")
	digest, err := s.PutContent(ctx, "tenant-a", content, RetentionShort)
	require.NoError(t, err)
	key := fmt.Sprintf("cas:tenant-a:%x", digest)
	altered := "APPROVED bytes"
	require.Len(t, altered, len(content))
	require.NoError(t, srv.Set(key, altered))
	_, err = s.PutContent(ctx, "tenant-a", content, RetentionAudit)
	require.NoError(t, err)
	stored, err := srv.Get(key)
	require.NoError(t, err)
	require.Equal(t, altered, stored, "duplicate put must not overwrite existing bytes")
	got, err := s.GetContent(ctx, "tenant-a", digest)
	require.ErrorIs(t, err, ErrDigestMismatch)
	require.Nil(t, got, "altered bytes must not be released")
}

func TestContentStoreGetRefusesOtherTenant(t *testing.T) {
	s, srv := newContentTestStore(t)
	ctx := context.Background()
	content := []byte("private input")
	digest, err := s.PutContent(ctx, "tenant-a:sub", content, RetentionShort)
	require.NoError(t, err)
	for _, tenant := range []string{"tenant-b", "tenant-a"} {
		got, err := s.GetContent(ctx, tenant, digest)
		require.ErrorIs(t, err, ErrArtifactNotFound)
		require.Nil(t, got)
	}
	got, err := s.GetContent(ctx, "tenant-a:sub", digest)
	require.NoError(t, err)
	require.Equal(t, content, got)
	before := srv.CommandCount()
	zero, err := s.PutContent(ctx, "", content, RetentionShort)
	require.Error(t, err)
	require.Equal(t, [sha256.Size]byte{}, zero)
	got, err = s.GetContent(ctx, "", digest)
	require.Error(t, err)
	require.Nil(t, got)
	require.Error(t, s.RetainContent(ctx, "", digest, time.Now().Add(time.Hour)))
	require.Equal(t, before, srv.CommandCount(), "empty tenants must never reach Redis")
}

func TestContentStoreRetainCoversGrantLifetime(t *testing.T) {
	t.Setenv(envArtifactTTLShort, "1h")
	s, srv := newContentTestStore(t)
	t0 := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	srv.SetTime(t0)
	ctx := context.Background()
	content := []byte("grant input")
	digest, err := s.PutContent(ctx, "tenant-a", content, RetentionShort)
	require.NoError(t, err)
	// A future grant issuer must retain inputs through its grant's expiry.
	require.NoError(t, s.RetainContent(ctx, "tenant-a", digest, t0.Add(3*time.Hour)))
	key := fmt.Sprintf("cas:tenant-a:%x", digest)
	require.Equal(t, 3*time.Hour, srv.TTL(key))
	srv.FastForward(2 * time.Hour)
	got, err := s.GetContent(ctx, "tenant-a", digest)
	require.NoError(t, err)
	require.Equal(t, content, got)
	require.NoError(t, s.RetainContent(ctx, "tenant-a", digest, t0.Add(30*time.Minute)))
	require.Equal(t, time.Hour, srv.TTL(key), "retain must never shorten TTL")
	srv.FastForward(2 * time.Hour)
	got, err = s.GetContent(ctx, "tenant-a", digest)
	require.ErrorIs(t, err, ErrArtifactNotFound)
	require.Nil(t, got)
	require.ErrorIs(t, s.RetainContent(ctx, "tenant-a", sha256.Sum256(nil), t0.Add(5*time.Hour)), ErrArtifactNotFound)
	require.Empty(t, srv.Keys(), "retaining an unknown digest must not create bytes")
}
