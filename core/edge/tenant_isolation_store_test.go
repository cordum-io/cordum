package edge

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTenantIsolationEdgeSession(t *testing.T) {
	s, _, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()
	ctx := context.Background()
	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		if err := s.CreateSession(ctx, validStoreSession(tenant, tenant+"-session", "principal", time.Now().UTC())); err != nil {
			t.Fatal(err)
		}
	}
	before, ok, err := s.GetSession(ctx, "tenant-b", "tenant-b-session")
	if err != nil || !ok || before == nil {
		t.Fatalf("seed read: %v, %v", ok, err)
	}
	got, ok, err := s.GetSession(ctx, "tenant-a", "tenant-b-session")
	if err != nil || ok || got != nil {
		t.Errorf("foreign session: %v, %v, %v", got, ok, err)
	}
	after, ok, err := s.GetSession(ctx, "tenant-b", "tenant-b-session")
	if err != nil || !ok || !reflect.DeepEqual(before, after) {
		t.Errorf("B session changed: %v", err)
	}
}

func TestTenantIsolationEdgeExecution(t *testing.T) {
	s, _, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()
	ctx := context.Background()
	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		createSessionAndExecution(t, ctx, s, tenant, tenant+"-session", tenant+"-execution", time.Now().UTC())
	}
	before, ok, err := s.GetExecution(ctx, "tenant-b", "tenant-b-execution")
	if err != nil || !ok || before == nil {
		t.Fatalf("seed read: %v, %v", ok, err)
	}
	got, ok, err := s.GetExecution(ctx, "tenant-a", "tenant-b-execution")
	if err != nil || ok || got != nil {
		t.Errorf("foreign execution: %v, %v, %v", got, ok, err)
	}
	page, err := s.ListExecutions(ctx, ListExecutionsQuery{TenantID: "tenant-a", SessionID: "tenant-b-session"})
	if err != nil || len(page.Items) != 0 {
		t.Errorf("foreign executions: %v, %v", page, err)
	}
	after, ok, err := s.GetExecution(ctx, "tenant-b", "tenant-b-execution")
	if err != nil || !ok || !reflect.DeepEqual(before, after) {
		t.Errorf("B execution changed: %v", err)
	}
}

func TestTenantIsolationEdgeEvents(t *testing.T) {
	s, _, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()
	ctx, now := context.Background(), time.Now().UTC()
	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		createSessionAndExecution(t, ctx, s, tenant, tenant+"-session", tenant+"-execution", now)
		_, err := s.AppendEvent(ctx, validStoreEvent(tenant, tenant+"-session", tenant+"-execution", tenant+"-event", 0, now.Add(time.Second), EventKindHookPreToolUse, DecisionAllow))
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []ListEventsQuery{
		{TenantID: "tenant-b", SessionID: "tenant-b-session"},
		{TenantID: "tenant-b", ExecutionID: "tenant-b-execution"},
	} {
		before, err := s.ListEvents(ctx, q)
		if err != nil || len(before.Items) != 1 {
			t.Fatalf("seed read: %v, %v", before, err)
		}
		foreign := q
		foreign.TenantID = "tenant-a"
		got, err := s.ListEvents(ctx, foreign)
		if err != nil || len(got.Items) != 0 {
			t.Errorf("foreign events: %v, %v", got, err)
		}
		after, err := s.ListEvents(ctx, q)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Errorf("B events changed: %v", err)
		}
	}
}

func TestTenantIsolationEdgeApproval(t *testing.T) {
	now := time.Now().UTC()
	s, _, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return now }))
	defer cleanup()
	ctx := context.Background()
	createApprovalParents(t, ctx, s, "tenant-b", "session-b", "execution-b", "event-b", now)
	created, err := s.EnqueueApproval(ctx, validApprovalRequest("tenant-b", "session-b", "execution-b", "event-b", now))
	if err != nil {
		t.Fatal(err)
	}
	before, ok, err := s.GetApproval(ctx, "tenant-b", created.ApprovalRef)
	if err != nil || !ok || before == nil {
		t.Fatalf("seed read: %v, %v", ok, err)
	}
	got, ok, err := s.GetApproval(ctx, "tenant-a", created.ApprovalRef)
	if err != nil || ok || got != nil {
		t.Errorf("foreign approval: %v, %v, %v", got, ok, err)
	}
	page, err := s.ListApprovals(ctx, ListApprovalsQuery{TenantID: "tenant-a", Status: ApprovalStatusPending})
	if err != nil || len(page.Items) != 0 {
		t.Errorf("foreign approvals: %v, %v", page, err)
	}
	after, ok, err := s.GetApproval(ctx, "tenant-b", created.ApprovalRef)
	if err != nil || !ok || !reflect.DeepEqual(before, after) {
		t.Errorf("B approval changed: %v", err)
	}
}

func TestTenantIsolationEdgeMCPUpstreamReadOnly(t *testing.T) {
	ctx, s, client := newMCPUpstreamRegistryForTest(t)
	upstream := validMCPUpstream("tenant-b", "private-b")
	upstream.Endpoint = "https://old.example/mcp"
	if err := s.Create(ctx, &upstream); err != nil {
		t.Fatal(err)
	}
	upstream.Endpoint = "https://new.example/mcp"
	if err := s.Update(ctx, &upstream); err != nil {
		t.Fatal(err)
	}
	before, ok, err := s.Get(ctx, "tenant-b", upstream.Name)
	if err != nil || !ok || before == nil {
		t.Fatalf("seed read: %v, %v", ok, err)
	}
	keys, err := client.Keys(ctx, "edge:mcp:upstream:bak:tenant-b:private-b:*").Result()
	if err != nil || len(keys) != 1 {
		t.Fatalf("seed backup: %v, %v", keys, err)
	}
	backup, err := client.Get(ctx, keys[0]).Result()
	if err != nil || !strings.Contains(backup, "https://old.example/mcp") {
		t.Fatalf("backup read: %v", err)
	}
	got, ok, err := s.Get(ctx, "tenant-a", upstream.Name)
	if err != nil || ok || got != nil {
		t.Errorf("foreign upstream: %v, %v, %v", got, ok, err)
	}
	after, ok, err := s.Get(ctx, "tenant-b", upstream.Name)
	if err != nil || !ok || !reflect.DeepEqual(before, after) {
		t.Errorf("B upstream changed: %v", err)
	}
	afterBackup, err := client.Get(ctx, keys[0]).Result()
	if err != nil || backup != afterBackup {
		t.Errorf("B backup changed: %v", err)
	}
}

func TestTenantIsolationEdgeHeartbeatReadOnly(t *testing.T) {
	s, client, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()
	ctx := context.Background()
	if err := s.CreateSession(ctx, validStoreSession("tenant-b", "session-b", "principal-b", time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchHeartbeat(ctx, "tenant-b", "session-b"); err != nil {
		t.Fatal(err)
	}
	before, err := client.Get(ctx, edgeSessionHeartbeatKey("session-b")).Result()
	if err != nil || before == "" {
		t.Fatalf("seed heartbeat: %q, %v", before, err)
	}
	if alive, err := s.HeartbeatAlive(ctx, "tenant-b", "session-b"); err != nil || !alive {
		t.Fatalf("B heartbeat not alive: %v", err)
	}
	if alive, err := s.HeartbeatAlive(ctx, "tenant-a", "session-b"); err != nil || alive {
		t.Errorf("foreign heartbeat: %v, %v", alive, err)
	}
	after, err := client.Get(ctx, edgeSessionHeartbeatKey("session-b")).Result()
	if err != nil || before != after {
		t.Errorf("B heartbeat changed: %q, %v", after, err)
	}
}
