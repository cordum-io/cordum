package artifacts

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
)

func TestRedisStorePutGet(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("create redis store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	content := []byte("hello")
	ptr, err := store.Put(ctx, content, Metadata{ContentType: "text/plain", Retention: RetentionShort})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if ptr == "" {
		t.Fatalf("expected pointer")
	}

	got, meta, err := store.Get(ctx, ptr)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("unexpected content: %s", got)
	}
	if meta.ContentType != "text/plain" {
		t.Fatalf("unexpected content type: %s", meta.ContentType)
	}
	if meta.SizeBytes != int64(len(content)) {
		t.Fatalf("unexpected size: %d", meta.SizeBytes)
	}
}

func TestParseDurationEnv(t *testing.T) {
	if got := parseDurationEnv("NOT_SET", 5*time.Second); got != 5*time.Second {
		t.Fatalf("unexpected fallback duration")
	}
	t.Setenv(envArtifactTTLShort, "2s")
	if got := parseDurationEnv(envArtifactTTLShort, 5*time.Second); got != 2*time.Second {
		t.Fatalf("unexpected parsed duration")
	}
	t.Setenv(envArtifactTTLShort, "bad")
	if got := parseDurationEnv(envArtifactTTLShort, 5*time.Second); got != 5*time.Second {
		t.Fatalf("expected fallback for invalid duration")
	}
}
