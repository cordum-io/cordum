package edge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/redis/go-redis/v9"
)

type scanEdgeReads struct {
	mu   sync.Mutex
	keys map[string]int
}

func (r *scanEdgeReads) DialHook(next redis.DialHook) redis.DialHook { return next }
func (r *scanEdgeReads) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (r *scanEdgeReads) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "get" || cmd.Name() == "zrange" {
			r.mu.Lock()
			r.keys[fmt.Sprint(cmd.Args()[1])]++
			r.mu.Unlock()
		}
		return next(ctx, cmd)
	}
}

func scanEdgeCluster(t *testing.T) (*redis.ClusterClient, *scanEdgeReads, []*miniredis.Miniredis) {
	t.Helper()
	servers := []*miniredis.Miniredis{miniredis.RunT(t), miniredis.RunT(t)}
	client := testredis.NewTwoMasterClusterClient(t, servers[0].Addr(), servers[1].Addr())
	reads := &scanEdgeReads{keys: make(map[string]int)}
	client.OnNewNode(func(node *redis.Client) { node.AddHook(reads) })
	return client, reads, servers
}

func scanEdgeAssertReads(t *testing.T, reads *scanEdgeReads, servers []*miniredis.Miniredis, keys []string) {
	t.Helper()
	for _, server := range servers {
		if len(server.Keys()) == 0 {
			t.Fatalf("server %s holds no seeded key", server.Addr())
		}
	}
	reads.mu.Lock()
	defer reads.mu.Unlock()
	for _, key := range keys {
		if got := reads.keys[key]; got != 1 {
			t.Errorf("key %s read %d times, want 1", key, got)
		}
	}
}

func TestSessionSweepVisitsEveryMaster(t *testing.T) {
	client, reads, servers := scanEdgeCluster(t)
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	var keys []string
	for i := range 12 {
		session := validStoreSession("tenant-a", fmt.Sprintf("scan-session-%02d", i), "principal-a", now)
		data, err := json.Marshal(session)
		if err != nil {
			t.Fatal(err)
		}
		key := edgeSessionKey(session.SessionID)
		if err := client.Set(t.Context(), key, data, 0).Err(); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key)
	}
	heartbeat := edgeSessionHeartbeatKey("scan-session-00")
	if err := client.Set(t.Context(), heartbeat, now.Format(time.RFC3339Nano), 0).Err(); err != nil {
		t.Fatal(err)
	}
	sweeper, err := NewSessionSweeper(NewRedisStoreFromClient(client), SessionSweeperOptions{
		Now: func() time.Time { return now }, LegalHolds: audit.NewLegalHoldStoreFromClient(client),
	})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := sweeper.SweepOnce(t.Context()); err != nil || n != 0 {
		t.Fatalf("SweepOnce = %d, %v, want 0, nil", n, err)
	}
	scanEdgeAssertReads(t, reads, servers, keys)
	reads.mu.Lock()
	defer reads.mu.Unlock()
	if got := reads.keys[heartbeat]; got != 0 {
		t.Fatalf("heartbeat read %d times, want 0", got)
	}
}

func TestApprovalSweepVisitsEveryMaster(t *testing.T) {
	client, reads, servers := scanEdgeCluster(t)
	keys := scanEdgeSeedPending(t, client)
	sweeper, err := NewApprovalSweeper(NewRedisStoreFromClient(client), ApprovalSweeperOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := sweeper.SweepOnce(t.Context()); err != nil || n != 0 {
		t.Fatalf("SweepOnce = %d, %v, want 0, nil", n, err)
	}
	scanEdgeAssertReads(t, reads, servers, keys)
}

func scanEdgeSeedPending(t *testing.T, client redis.UniversalClient) []string {
	t.Helper()
	var keys []string
	for i := range 12 {
		key := edgeApprovalStatusIndexKey(fmt.Sprintf("scan-tenant-%02d", i), ApprovalStatusPending)
		if err := client.ZAdd(t.Context(), key, redis.Z{Score: 1, Member: fmt.Sprintf("missing-%02d", i)}).Err(); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key)
	}
	return keys
}

func TestApprovalSweepTenantLimitAcrossMasters(t *testing.T) {
	client, reads, _ := scanEdgeCluster(t)
	keys := scanEdgeSeedPending(t, client)
	sweeper, err := NewApprovalSweeper(NewRedisStoreFromClient(client), ApprovalSweeperOptions{MaxTenantsPerSweep: 3})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := sweeper.SweepOnce(t.Context()); err != nil || n != 0 {
		t.Fatalf("SweepOnce = %d, %v, want 0, nil", n, err)
	}
	reads.mu.Lock()
	defer reads.mu.Unlock()
	var visited int
	for _, key := range keys {
		visited += reads.keys[key]
	}
	if visited != 3 {
		t.Fatalf("visited %d tenants, want 3", visited)
	}
}

func TestSweepsKeepErrorContext(t *testing.T) {
	for _, approval := range []bool{false, true} {
		for _, failure := range []string{"scan", "callback", "cancel"} {
			if !approval && failure == "cancel" {
				continue // Covered by TestSessionSweeperSweepOnceReturnsContextCancellation.
			}
			t.Run(fmt.Sprintf("approval=%t/%s", approval, failure), func(t *testing.T) {
				scanEdgeCheckError(t, approval, failure)
			})
		}
	}
}

func scanEdgeCheckError(t *testing.T, approval bool, failure string) {
	t.Helper()
	server := miniredis.RunT(t)
	client := testredis.NewClient(t, server.Addr())
	store := NewRedisStoreFromClient(client)
	session, err := NewSessionSweeper(store, SessionSweeperOptions{LegalHolds: audit.NewLegalHoldStoreFromClient(client)})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := NewApprovalSweeper(store, ApprovalSweeperOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sweep, prefix, key := session.SweepOnce, "scan edge sessions for retention: ", edgeSessionKey("broken")
	if approval {
		sweep, prefix, key = pending.SweepOnce, "scan edge approval pending indexes: ", edgeApprovalStatusIndexKey("broken", ApprovalStatusPending)
	}
	if err := client.Set(t.Context(), key, "not JSON or a sorted set", 0).Err(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	switch failure {
	case "scan":
		server.SetError("ERR scan unavailable")
	case "cancel":
		cancel()
	}
	_, err = sweep(ctx)
	if err == nil || strings.HasPrefix(err.Error(), prefix) != (failure == "scan") {
		t.Fatalf("%s error = %v, scan prefix wanted: %t", failure, err, failure == "scan")
	}
	if failure == "cancel" && !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v, want context.Canceled", err)
	}
}
