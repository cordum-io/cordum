package artifacts

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/redis/go-redis/v9"
)

func TestRedisStorePutOnClusterClient(t *testing.T) {
	mr := miniredis.RunT(t)
	store := NewRedisStoreFromClient(testredis.NewMiniClusterClient(t, mr.Addr()))
	ctx := context.Background()
	content := []byte("cluster artifact")

	ptr, err := store.Put(ctx, content, Metadata{ContentType: "text/plain", Labels: map[string]string{"tenant_id": "tenant-cluster"}})
	if err != nil {
		t.Fatalf("put on cluster client: %v", err)
	}
	got, meta, err := store.Get(ctx, ptr)
	if err != nil || string(got) != string(content) {
		t.Fatalf("get = %q, %v; want %q", got, err, content)
	}
	if meta.ContentType != "text/plain" || meta.SizeBytes != int64(len(content)) {
		t.Fatalf("get meta = %+v", meta)
	}
	stat, err := store.Stat(ctx, ptr)
	if err != nil || stat.Labels["tenant_id"] != "tenant-cluster" {
		t.Fatalf("stat = %+v, %v", stat, err)
	}
}

func TestRedisStorePutWritesBytesBeforeMeta(t *testing.T) {
	mr := miniredis.RunT(t)
	client := testredis.NewMiniClusterClient(t, mr.Addr())
	failure := errors.New("artifact bytes write failed")
	client.AddHook(artifactBytesFailureHook{err: failure})

	// Put picks the id, so the hook fails every bytes key.
	if _, err := NewRedisStoreFromClient(client).Put(context.Background(), []byte("x"), Metadata{}); !errors.Is(err, failure) {
		t.Fatalf("put error = %v; want %v", err, failure)
	}
	for _, key := range mr.Keys() {
		if strings.HasPrefix(key, "art:meta:") {
			t.Fatalf("meta %s was written before the bytes", key)
		}
	}
}

// artifactBytesFailureHook fails any command, or any pipeline holding one,
// on an artifact bytes key (art:<id>, not art:meta:<id>).
type artifactBytesFailureHook struct {
	err error
}

func (h artifactBytesFailureHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h artifactBytesFailureHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if artifactBytesCmd(cmd) {
			return h.err
		}
		return next(ctx, cmd)
	}
}

func (h artifactBytesFailureHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		if slices.ContainsFunc(cmds, artifactBytesCmd) {
			return h.err
		}
		return next(ctx, cmds)
	}
}

func artifactBytesCmd(cmd redis.Cmder) bool {
	args := cmd.Args()
	if len(args) < 2 {
		return false
	}
	key, _ := args[1].(string)
	return strings.HasPrefix(key, "art:") && !strings.HasPrefix(key, "art:meta:")
}
