package artifacts

import (
	"context"
	"errors"
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
	defer func() { _ = store.Close() }()

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

// TestRedisStoreStatReturnsMetadataWithoutLoadingContent exercises the
// EDGE-013 evidence-export entry point — Stat must read the metadata key
// without paging in the content key, and must surface labels (including
// tenant_id and source-of-truth fields) that the bundler relies on for
// cross-tenant matching.
func TestRedisStoreStatReturnsMetadataWithoutLoadingContent(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("create redis store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	content := []byte("transcript-bytes-not-leaked-to-stat")
	want := Metadata{
		ContentType: "application/json",
		Retention:   RetentionAudit,
		Labels: map[string]string{
			"tenant_id":     "tenant-a",
			"session_id":    "edge_sess_01",
			"execution_id":  "exec_01",
			"event_id":      "evt_01",
			"artifact_type": "edge.transcript",
		},
	}
	ptr, err := store.Put(ctx, content, want)
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	got, err := store.Stat(ctx, ptr)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got.ContentType != want.ContentType {
		t.Errorf("ContentType = %q, want %q", got.ContentType, want.ContentType)
	}
	if got.Retention != want.Retention {
		t.Errorf("Retention = %q, want %q", got.Retention, want.Retention)
	}
	if got.SizeBytes != int64(len(content)) {
		t.Errorf("SizeBytes = %d, want %d", got.SizeBytes, len(content))
	}
	for key, wantValue := range want.Labels {
		if got.Labels[key] != wantValue {
			t.Errorf("Labels[%q] = %q, want %q", key, got.Labels[key], wantValue)
		}
	}
	// Stat returns Metadata only — there is no API for it to leak content.
	// Other tests (TestRedisStorePutGet) exercise the content path; this
	// test asserts the metadata-only contract by type alone.
}

// TestRedisStoreStatReturnsErrArtifactNotFoundForMissingArtifact ensures
// the export bundler can distinguish "artifact was never stored / TTL
// expired" from "Redis is unhappy" and route the former to the missing
// artifacts manifest section instead of failing the whole export.
func TestRedisStoreStatReturnsErrArtifactNotFoundForMissingArtifact(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("create redis store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	// Synthesize a pointer for an id that was never written. We use the same
	// pointer encoding the production code uses so this exercises the real
	// path from pointer -> metadata key -> ErrArtifactNotFound.
	missingPtr, err := store.Put(ctx, []byte("written"), Metadata{Retention: RetentionShort})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	srv.FlushAll()

	if _, err := store.Stat(ctx, missingPtr); !errors.Is(err, ErrArtifactNotFound) {
		t.Fatalf("Stat on missing artifact: err = %v, want ErrArtifactNotFound", err)
	}
}

// TestRedisStoreGetReturnsErrArtifactNotFoundForMissingArtifact pairs with
// the Stat test above. Get's previous behavior on a missing artifact was an
// opaque redis.Nil error; callers couldn't tell missing from broken without
// reaching into the redis driver. EDGE-013 wraps that in ErrArtifactNotFound
// so consumers (export bundler, but also dashboard) get a stable signal.
func TestRedisStoreGetReturnsErrArtifactNotFoundForMissingArtifact(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("create redis store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	missingPtr, err := store.Put(ctx, []byte("written"), Metadata{Retention: RetentionShort})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	srv.FlushAll()

	if _, _, err := store.Get(ctx, missingPtr); !errors.Is(err, ErrArtifactNotFound) {
		t.Fatalf("Get on missing artifact: err = %v, want ErrArtifactNotFound", err)
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
