package audit

import (
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/internal/testredis"
)

func TestNewLegalHoldStoreClosesClientWhenPingFails(t *testing.T) {
	t.Setenv("REDIS_CLUSTER_ADDRESSES", "")
	srv := miniredis.RunT(t)
	srv.SetError("ERR injected ping failure")
	store, err := NewLegalHoldStore(testredis.URL(srv.Addr()))
	if store != nil {
		t.Fatal("expected nil store after failed ping")
	}
	if err == nil || err.Error() != "redis ping: ERR injected ping failure" {
		t.Fatalf("unexpected ping error: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for srv.CurrentConnectionCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if count := srv.CurrentConnectionCount(); count != 0 {
		t.Fatalf("connections left after failed ping: %d", count)
	}
}
