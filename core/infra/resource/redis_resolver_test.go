package resource

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func newRedisRegistry(t *testing.T, now *time.Time, allowed []string, max uint64) (*Registry, *miniredis.Miniredis) {
	t.Helper()
	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run: %v", err)
	}
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close(); server.Close() })
	resolver, err := NewRedisResolver(RedisResolverConfig{
		ID: "cache", Client: client, Scheme: "redis", Authority: "resources",
		KeyPrefix: "blob:", AllowedMediaTypes: allowed, MaxBytes: max,
	})
	if err != nil {
		t.Fatalf("NewRedisResolver: %v", err)
	}
	registry, err := NewRegistry(func() time.Time { return *now }, resolver)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return registry, server
}

func redisRef(now time.Time, uri, media string, body []byte) *agentv1.ResourceRef {
	digest := sha256.Sum256(body)
	return &agentv1.ResourceRef{ResolverId: "cache", Uri: uri, Sha256: digest[:], MediaType: media,
		SizeBytes: uint64(len(body)), ExpiresAt: timestamppb.New(now.Add(time.Minute)), Purpose: "job.input"}
}

func TestRedisResolverReturnsOnlyIntegrityCheckedContent(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	registry, server := newRedisRegistry(t, &now, []string{"application/json"}, 1024)
	body := []byte(`{"safe":true}`)
	server.Set("blob:tenant-a:job-a:item", string(body))
	ref := redisRef(now, "redis://resources/blob:tenant-a:job-a:item", "application/json", body)
	resolved, err := registry.Resolve(context.Background(), ref,
		TrustedContext{TenantID: "tenant-a", JobID: "job-a"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if string(resolved.Content) != string(body) || resolved.MediaType != "application/json" {
		t.Fatalf("resolved = %#v", resolved)
	}
}

func TestRedisResolverRejectsURIAndScopeAttacks(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	registry, server := newRedisRegistry(t, &now, []string{"application/json"}, 1024)
	body := []byte("safe")
	server.Set("blob:tenant-a:job-a:item", string(body))
	tests := map[string]string{
		"wrong scheme":      "https://resources/blob:tenant-a:job-a:item",
		"wrong authority":   "redis://other/blob:tenant-a:job-a:item",
		"userinfo":          "redis://user:secret@resources/blob:tenant-a:job-a:item",
		"query credential":  "redis://resources/blob:tenant-a:job-a:item?token=secret",
		"fragment":          "redis://resources/blob:tenant-a:job-a:item#secret",
		"encoded traversal": "redis://resources/blob:tenant-a:job-a:%2e%2e",
		"encoded nul":       "redis://resources/blob:tenant-a:job-a:%00",
		"path traversal":    "redis://resources/blob:tenant-a:job-a:..",
		"nested path":       "redis://resources/blob:tenant-a:job-a/final",
		"backslash":         `redis://resources/blob:tenant-a:job-a:\final`,
		"other tenant":      "redis://resources/blob:tenant-b:job-a:item",
		"other job":         "redis://resources/blob:tenant-a:job-b:item",
		"empty key":         "redis://resources/blob:tenant-a:job-a:",
	}
	for name, uri := range tests {
		t.Run(name, func(t *testing.T) {
			ref := redisRef(now, uri, "application/json", body)
			_, err := registry.Resolve(context.Background(), ref,
				TrustedContext{TenantID: "tenant-a", JobID: "job-a"})
			if !errors.Is(err, ErrPolicyViolation) {
				t.Fatalf("Resolve error = %v, want ErrPolicyViolation", err)
			}
			if strings.Contains(strings.ToLower(err.Error()), "secret") {
				t.Fatalf("error leaked credential: %v", err)
			}
		})
	}
}

func TestRedisResolverRejectsPolicyAndContentConflicts(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	registry, server := newRedisRegistry(t, &now, []string{"application/json"}, 8)
	server.Set("blob:t:j:item", "123456789")
	tests := map[string]struct {
		media string
		size  uint64
		want  error
	}{
		"media":           {media: "text/plain", size: 8, want: ErrMediaTypeMismatch},
		"declared max":    {media: "application/json", size: 9, want: ErrSizeMismatch},
		"stored oversize": {media: "application/json", size: 8, want: ErrSizeMismatch},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			body := []byte("12345678")
			ref := redisRef(now, "redis://resources/blob:t:j:item", tc.media, body)
			ref.SizeBytes = tc.size
			_, err := registry.Resolve(context.Background(), ref, TrustedContext{TenantID: "t", JobID: "j"})
			if !errors.Is(err, tc.want) {
				t.Fatalf("Resolve error = %v, want %v", err, tc.want)
			}
		})
	}
	server.Set("blob:t:j:item", "short")
	ref := redisRef(now, "redis://resources/blob:t:j:item", "application/json", []byte("12345678"))
	if _, err := registry.Resolve(context.Background(), ref, TrustedContext{TenantID: "t", JobID: "j"}); !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("short content error = %v, want ErrSizeMismatch", err)
	}
}

func TestRedisResolverRejectsUnavailableAndCancelledFetch(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	registry, server := newRedisRegistry(t, &now, []string{"application/json"}, 64)
	ref := redisRef(now, "redis://resources/blob:t:j:missing", "application/json", []byte("x"))
	_, err := registry.Resolve(context.Background(), ref, TrustedContext{TenantID: "t", JobID: "j"})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing error = %v, want ErrUnavailable", err)
	}
	server.Close()
	_, err = registry.Resolve(context.Background(), ref, TrustedContext{TenantID: "t", JobID: "j"})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("closed resolver error = %v, want ErrUnavailable", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = registry.Resolve(ctx, ref, TrustedContext{TenantID: "t", JobID: "j"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v, want context.Canceled", err)
	}
}

func TestRedisResolverRejectsUnsafeOperatorPolicy(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { _ = client.Close() })
	valid := RedisResolverConfig{ID: "cache", Client: client, Scheme: "redis", Authority: "resources",
		KeyPrefix: "blob:", AllowedMediaTypes: []string{"application/json"}, MaxBytes: 1024}
	tests := map[string]func(*RedisResolverConfig){
		"generic scheme":     func(cfg *RedisResolverConfig) { cfg.Scheme = "https" },
		"userinfo authority": func(cfg *RedisResolverConfig) { cfg.Authority = "user@resources" },
		"unsafe prefix":      func(cfg *RedisResolverConfig) { cfg.KeyPrefix = "../blob:" },
		"missing max":        func(cfg *RedisResolverConfig) { cfg.MaxBytes = 0 },
		"unbounded max":      func(cfg *RedisResolverConfig) { cfg.MaxBytes = absoluteRedisMaxBytes + 1 },
		"duplicate media": func(cfg *RedisResolverConfig) {
			cfg.AllowedMediaTypes = []string{"application/json", "application/json"}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := valid
			mutate(&cfg)
			if _, err := NewRedisResolver(cfg); !errors.Is(err, ErrInvalidResolverConfig) {
				t.Fatalf("NewRedisResolver error = %v, want ErrInvalidResolverConfig", err)
			}
		})
	}
}

func TestRedisResolverSnapshotsOperatorPolicy(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	media := []string{"application/json"}
	registry, server := newRedisRegistry(t, &now, media, 64)
	media[0] = "text/plain"
	body := []byte("trusted")
	server.Set("blob:t:j:item", string(body))
	ref := redisRef(now, "redis://resources/blob:t:j:item", "application/json", body)
	if _, err := registry.Resolve(context.Background(), ref, TrustedContext{TenantID: "t", JobID: "j"}); err != nil {
		t.Fatalf("Resolve after config mutation: %v", err)
	}
	ref.MediaType = "text/plain"
	if _, err := registry.Resolve(context.Background(), ref, TrustedContext{TenantID: "t", JobID: "j"}); !errors.Is(err, ErrMediaTypeMismatch) {
		t.Fatalf("mutated policy was observed: %v", err)
	}
}
