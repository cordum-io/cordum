package edge

import (
	"context"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/internal/testredis"
)

// The sweeper on a Redis Cluster client scans every master and deletes a
// session's keys one DEL each.
func TestSessionSweeperOnClusterClient(t *testing.T) {
	ctx := context.Background()
	store, client, mr, cleanup := newRedisEdgeStore(t)
	defer cleanup()
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	createSessionAndExecution(t, ctx, store, "tenant-a", "sess-aged", "exec-aged", now.Add(-40*24*time.Hour))
	createSessionAndExecution(t, ctx, store, "tenant-a", "sess-fresh", "exec-fresh", now.Add(-10*24*time.Hour))

	cluster := testredis.NewMiniClusterClient(t, mr.Addr())
	sweeper, err := NewSessionSweeper(NewRedisStoreFromClient(cluster), SessionSweeperOptions{
		Now:        func() time.Time { return now },
		LegalHolds: audit.NewLegalHoldStoreFromClient(cluster),
	})
	if err != nil {
		t.Fatalf("NewSessionSweeper: %v", err)
	}
	if swept, err := sweeper.SweepOnce(ctx); err != nil || swept != 1 {
		t.Fatalf("SweepOnce = %d, %v; want 1", swept, err)
	}
	if n, err := client.Exists(ctx, edgeSessionKey("sess-aged"), edgeExecutionKey("exec-aged")).Result(); err != nil || n != 0 {
		t.Fatalf("aged session keys left: %d, %v", n, err)
	}
	if _, ok, err := store.GetSession(ctx, "tenant-a", "sess-fresh"); err != nil || !ok {
		t.Fatalf("fresh session exists=%v err=%v, want preserved", ok, err)
	}
}
