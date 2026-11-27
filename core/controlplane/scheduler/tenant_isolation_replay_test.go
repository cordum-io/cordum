package scheduler

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/cordum/cordum/core/infra/redisutil"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

func newIsolationRedis(t *testing.T) (*miniredis.Miniredis, redis.UniversalClient) {
	t.Helper()
	srv := miniredis.RunT(t)
	rdb, err := redisutil.NewClient("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("redis client: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	return srv, rdb
}

func snapshotKeys(t *testing.T, srv *miniredis.Miniredis, keys []string) []string {
	t.Helper()
	values := make([]string, len(keys))
	for i, key := range keys {
		value, err := srv.Get(key)
		if err != nil {
			t.Fatalf("read %s: %v", key, err)
		}
		values[i] = value
	}
	return values
}

// Worker ids are unique across tenants (the credential store refuses another
// tenant's worker id), so tenant B's colliding handshake comes from its own
// worker. It reuses tenant A's request id and client nonce.
func TestTenantIsolationSchedulerHandshakeReplayKeys(t *testing.T) {
	srv, rdb := newIsolationRedis(t)
	store := NewRedisHandshakeChallengeStore(rdb)
	ctx := context.Background()
	challengeFor := func(tenant, worker, challengeID string) *agentv1.WorkerHandshakeChallenge {
		return &agentv1.WorkerHandshakeChallenge{
			ChallengeId: challengeID, WorkerId: worker, TenantId: tenant,
			RequestId: "req-shared", ClientNonce: []byte("client-nonce-shared"),
		}
	}
	a := challengeFor("tenant-a", "worker-a", "challenge-a")
	if created, err := store.Create(ctx, a, 30*time.Second); err != nil || !created {
		t.Fatalf("tenant-a challenge = %v, %v; want created", created, err)
	}
	keysA := srv.Keys()
	if len(keysA) != 3 {
		t.Fatalf("tenant-a handshake keys = %v; want challenge, request and nonce", keysA)
	}
	valuesA := snapshotKeys(t, srv, keysA)

	b := challengeFor("tenant-b", "worker-b", "challenge-b")
	if created, err := store.Create(ctx, b, 30*time.Second); err != nil || !created {
		t.Fatalf("tenant-b challenge reusing tenant-a's request id and nonce = %v, %v; want created", created, err)
	}
	if !slices.Equal(snapshotKeys(t, srv, keysA), valuesA) {
		t.Fatal("tenant-a handshake records changed")
	}

	// Tenant A's replay protection still holds, and each challenge is consumed by its owner.
	if created, err := store.Create(ctx, challengeFor("tenant-a", "worker-a", "challenge-a2"), 30*time.Second); err != nil || created {
		t.Fatalf("tenant-a request replay = %v, %v; want refused", created, err)
	}
	for _, challenge := range []*agentv1.WorkerHandshakeChallenge{a, b} {
		if status, err := store.Consume(ctx, challenge); err != nil || status != HandshakeConsumeMatched {
			t.Fatalf("%s consume = %v, %v; want matched", challenge.GetTenantId(), status, err)
		}
	}
}

// Workflow ids are owned by one tenant (workflow create refuses another
// tenant's id), so tenant B's step runs in its own workflow and reuses tenant
// A's job id.
func TestTenantIsolationSchedulerSagaRecorded(t *testing.T) {
	srv, rdb := newIsolationRedis(t)
	saga := NewSagaManager(&fakeBus{}, rdb)
	ctx := context.Background()
	record := func(tenant, workflowID string) {
		t.Helper()
		req := &pb.JobRequest{
			JobId: "job-shared", Topic: "job.primary", WorkflowId: workflowID, TenantId: tenant,
			PrincipalId: "principal-" + tenant, Compensation: &pb.Compensation{Topic: "job.undo"},
		}
		if err := saga.RecordCompensation(ctx, req); err != nil {
			t.Fatalf("%s record: %v", tenant, err)
		}
	}
	stack := func(workflowID string) []string {
		t.Helper()
		entries, err := srv.List(sagaStackKey(workflowID))
		if err != nil {
			t.Fatalf("read %s stack: %v", workflowID, err)
		}
		return entries
	}
	tenantOf := func(raw string) string {
		var req pb.JobRequest
		if err := proto.Unmarshal([]byte(raw), &req); err != nil {
			t.Fatalf("decode saga entry: %v", err)
		}
		return req.GetTenantId()
	}

	record("tenant-a", "wf-iso-a")
	stackA := stack("wf-iso-a")
	if len(stackA) != 1 {
		t.Fatalf("tenant-a stack = %d entries; want 1", len(stackA))
	}

	record("tenant-b", "wf-iso-b")
	if stackB := stack("wf-iso-b"); len(stackB) != 1 || tenantOf(stackB[0]) != "tenant-b" {
		t.Fatalf("tenant-b stack = %d entries; want its own step recorded", len(stackB))
	}
	for _, workflowID := range []string{"wf-iso-a", "wf-iso-b"} {
		if ok, err := srv.IsMember(sagaDedupeKey(workflowID), "job-shared"); err != nil || !ok {
			t.Fatalf("%s recorded set lacks job-shared: %v, %v", workflowID, ok, err)
		}
	}
	if after := stack("wf-iso-a"); !slices.Equal(after, stackA) || tenantOf(after[0]) != "tenant-a" {
		t.Fatal("tenant-a stack changed")
	}

	// Dedupe still holds inside tenant A's workflow.
	record("tenant-a", "wf-iso-a")
	if after := stack("wf-iso-a"); len(after) != 1 {
		t.Fatalf("tenant-a re-record stack = %d entries; want 1", len(after))
	}
}
