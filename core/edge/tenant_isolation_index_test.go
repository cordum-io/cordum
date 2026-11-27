package edge

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

var edgeIsolationPairs = [][2]string{{"tenant-a", "tenant-b"}, {"tenant-b", "tenant-a"}}

// seedEdgeIndexTenant gives a tenant one session, execution, approval event
// and pending approval. The principal, trace, run, job, event id and action
// hash are the same in both tenants, so a shared index without a tenant filter
// would hand back the other tenant's member.
func seedEdgeIndexTenant(t *testing.T, ctx context.Context, s *RedisStore, tenant string, now time.Time) string {
	t.Helper()
	session := validStoreSession(tenant, "session-"+tenant, "principal-shared", now)
	session.TraceID, session.WorkflowRunID, session.JobID = "trace-shared", "run-shared", "job-shared"
	if err := s.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	execution := validStoreExecution(tenant, session.SessionID, "execution-"+tenant, now.Add(time.Second), func(e *AgentExecution) {
		e.JobID, e.TraceID, e.WorkflowRunID = "job-shared", "trace-shared", "run-shared"
	})
	if err := s.CreateExecution(ctx, execution); err != nil {
		t.Fatal(err)
	}
	event := validStoreEvent(tenant, session.SessionID, execution.ExecutionID, "event-shared", 0, now.Add(2*time.Second), EventKindApprovalRequested, DecisionRequireApproval)
	event.PrincipalID, event.Status = "principal-shared", ActionStatusBlocked
	if _, err := s.AppendEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	req := validApprovalRequest(tenant, session.SessionID, execution.ExecutionID, event.EventID, now)
	req.PrincipalID, req.ActionHash = "principal-shared", "actionhash-shared"
	approval, err := s.EnqueueApproval(ctx, req)
	if err != nil || approval.TenantID != tenant {
		t.Fatalf("seed approval %s: %v, %v", tenant, approval, err)
	}
	return approval.ApprovalRef
}

// edgeKeyspace renders the value of every key matching the patterns. TTLs are
// left out: time passing is not a write by the caller.
func edgeKeyspace(t *testing.T, ctx context.Context, client redis.UniversalClient, patterns ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, pattern := range patterns {
		keys, err := client.Keys(ctx, pattern).Result()
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range keys {
			var value any
			switch kind := client.Type(ctx, key).Val(); kind {
			case "string":
				value, err = client.Get(ctx, key).Result()
			case "hash":
				value, err = client.HGetAll(ctx, key).Result()
			case "list":
				value, err = client.LRange(ctx, key, 0, -1).Result()
			case "set":
				var members []string
				members, err = client.SMembers(ctx, key).Result()
				sort.Strings(members)
				value = members
			case "zset":
				value, err = client.ZRangeWithScores(ctx, key, 0, -1).Result()
			default:
				t.Fatalf("key %s has type %q", key, kind)
			}
			if err != nil {
				t.Fatalf("read %s: %v", key, err)
			}
			out[key] = fmt.Sprint(value)
		}
	}
	return out
}

func assertEdgeSessionIndexReads(t *testing.T, ctx context.Context, s *RedisStore, tenant, other string) {
	t.Helper()
	own, foreign, mine := "session-"+tenant, "session-"+other, []string{"execution-" + tenant}
	for q, want := range map[ListSessionsQuery][]string{{}: {own}, {PrincipalID: "principal-shared"}: {own}} {
		q.TenantID = tenant
		page, err := s.ListSessions(ctx, q)
		var got []string
		for _, item := range page.Items {
			got = append(got, item.SessionID)
		}
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("sessions %+v = %v, %v; want %v", q, got, err, want)
		}
	}
	for q, want := range map[ListExecutionsQuery][]string{
		{SessionID: own}: mine, {SessionID: foreign}: nil,
		{JobID: "job-shared"}: mine, {TraceID: "trace-shared"}: mine, {WorkflowRunID: "run-shared"}: mine,
	} {
		q.TenantID = tenant
		page, err := s.ListExecutions(ctx, q)
		var got []string
		for _, item := range page.Items {
			got = append(got, item.ExecutionID)
		}
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("executions %+v = %v, %v; want %v", q, got, err, want)
		}
	}
	// Both tenants' events share one event id, so the execution id tells them apart.
	for q, want := range map[ListEventsQuery][]string{
		{SessionID: own}: mine, {SessionID: foreign}: nil,
		{ExecutionID: "execution-" + tenant}: mine, {ExecutionID: "execution-" + other}: nil,
	} {
		q.TenantID = tenant
		page, err := s.ListEvents(ctx, q)
		var got []string
		for _, item := range page.Items {
			got = append(got, item.ExecutionID)
		}
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("events %+v = %v, %v; want %v", q, got, err, want)
		}
	}
}

func TestTenantIsolationEdgeSessionIndexes(t *testing.T) {
	now := time.Now().UTC()
	s, client, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return now }))
	defer cleanup()
	ctx := context.Background()
	for _, pair := range edgeIsolationPairs {
		seedEdgeIndexTenant(t, ctx, s, pair[0], now)
	}
	shared := map[string]string{
		edgeSessionTraceIndexKey("trace-shared"):   "session-tenant-b",
		edgeSessionRunIndexKey("run-shared"):       "session-tenant-b",
		edgeJobIndexKey("job-shared"):              "execution-tenant-b",
		edgeExecutionTraceIndexKey("trace-shared"): "execution-tenant-b",
		edgeExecutionRunIndexKey("run-shared"):     "execution-tenant-b",
	}
	scores := map[string]float64{}
	for key, member := range shared {
		if n := client.ZCard(ctx, key).Val(); n != 2 {
			t.Fatalf("seed %s holds %d members, want both tenants", key, n)
		}
		scores[key] = client.ZScore(ctx, key, member).Val()
	}
	before := edgeKeyspace(t, ctx, client, "*")
	for _, pair := range edgeIsolationPairs {
		assertEdgeSessionIndexReads(t, ctx, s, pair[0], pair[1])
	}
	// event_id: B's event id under B's execution is refused before A reaches it.
	event := validStoreEvent("tenant-a", "session-tenant-b", "execution-tenant-b", "event-shared", 0, now.Add(3*time.Second), EventKindHookPreToolUse, DecisionAllow)
	if _, err := s.AppendEvent(ctx, event); !errors.Is(err, ErrNotFound) {
		t.Errorf("A append into B execution: %v", err)
	}
	req := validApprovalRequest("tenant-a", "session-tenant-b", "execution-tenant-b", "event-shared", now)
	if got, err := s.EnqueueApproval(ctx, req); !errors.Is(err, ErrNotFound) {
		t.Errorf("A approval on B event: %+v, %v", got, err)
	}
	if err := s.DeleteSession(ctx, "tenant-a", "session-tenant-b"); err != nil {
		t.Fatal(err)
	}
	if after := edgeKeyspace(t, ctx, client, "*"); !reflect.DeepEqual(before, after) {
		t.Errorf("keyspace changed by A's reads and refused writes")
	}
	// session_trace and session_run have no reader; session cleanup is their only
	// consumer. A's delete must take only A's members out of the shared keys.
	beforeB := edgeKeyspace(t, ctx, client, "*tenant-b*")
	if err := s.DeleteSession(ctx, "tenant-a", "session-tenant-a"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.GetSession(ctx, "tenant-a", "session-tenant-a"); err != nil || ok {
		t.Fatalf("A delete did not run: %v, %v", ok, err)
	}
	for key, member := range shared {
		got, err := client.ZRangeWithScores(ctx, key, 0, -1).Result()
		if want := []redis.Z{{Score: scores[key], Member: member}}; err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s after A delete = %v, %v; want %v", key, got, err, want)
		}
	}
	if afterB := edgeKeyspace(t, ctx, client, "*tenant-b*"); !reflect.DeepEqual(beforeB, afterB) {
		t.Errorf("B keys changed by A's delete")
	}
	assertEdgeSessionIndexReads(t, ctx, s, "tenant-b", "tenant-a")
}

func assertEdgeApprovalRefs(t *testing.T, ctx context.Context, s *RedisStore, q ListApprovalsQuery, want ...string) {
	t.Helper()
	page, err := s.ListApprovals(ctx, q)
	got := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		got = append(got, item.ApprovalRef)
	}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("approvals %+v = %v, %v; want %v", q, got, err, want)
	}
}

func TestTenantIsolationEdgeApprovalIndexes(t *testing.T) {
	now := time.Now().UTC()
	s, client, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return now }))
	defer cleanup()
	ctx := context.Background()
	refs := map[string]string{}
	for _, pair := range edgeIsolationPairs {
		refs[pair[0]] = seedEdgeIndexTenant(t, ctx, s, pair[0], now)
	}
	before := edgeKeyspace(t, ctx, client, "*")
	for _, pair := range edgeIsolationPairs {
		tenant, other := pair[0], pair[1]
		for _, q := range []ListApprovalsQuery{
			{}, {Status: ApprovalStatusPending}, {PrincipalID: "principal-shared"},
			{PrincipalID: "principal-shared", Status: ApprovalStatusPending},
			{SessionID: "session-" + tenant, ExecutionID: "execution-" + tenant, ActionHash: "actionhash-shared"},
		} {
			q.TenantID = tenant
			assertEdgeApprovalRefs(t, ctx, s, q, refs[tenant])
		}
		assertEdgeApprovalRefs(t, ctx, s, ListApprovalsQuery{TenantID: tenant, SessionID: "session-" + other, ExecutionID: "execution-" + other, ActionHash: "actionhash-shared"})
		got, err := s.GetApprovalsByActionHash(ctx, tenant, "actionhash-shared")
		if err != nil || len(got) != 1 || got[0].ApprovalRef != refs[tenant] {
			t.Errorf("%s by hash = %+v, %v", tenant, got, err)
		}
	}
	if after := edgeKeyspace(t, ctx, client, "*"); !reflect.DeepEqual(before, after) {
		t.Errorf("keyspace changed by reads")
	}
	beforeB := edgeKeyspace(t, ctx, client, "*tenant-b*", edgeApprovalKey(refs["tenant-b"]))
	if n, err := s.ExpireApprovals(ctx, "tenant-a", now.Add(10*time.Minute)); err != nil || n != 1 {
		t.Fatalf("A expire = %d, %v; want 1", n, err)
	}
	if got, ok, err := s.GetApproval(ctx, "tenant-a", refs["tenant-a"]); err != nil || !ok || got.Status != ApprovalStatusExpired {
		t.Fatalf("A approval not expired: %+v, %v", got, err)
	}
	if afterB := edgeKeyspace(t, ctx, client, "*tenant-b*", edgeApprovalKey(refs["tenant-b"])); !reflect.DeepEqual(beforeB, afterB) {
		t.Errorf("B approval keys changed by A's expiry")
	}
	if _, err := s.ApproveApproval(ctx, ApprovalResolution{TenantID: "tenant-b", ApprovalRef: refs["tenant-b"], ResolverID: "reviewer-b", ResolvedBy: "reviewer-b", Reason: "approve B", ResolvedAt: now}); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := s.LookupByActionHash(ctx, "tenant-a", "actionhash-shared"); err != nil || ok || got != nil {
		t.Errorf("A reached B's approved hash: %+v, %v, %v", got, ok, err)
	}
	if got, ok, err := s.LookupByActionHash(ctx, "tenant-b", "actionhash-shared"); err != nil || !ok || got.ApprovalRef != refs["tenant-b"] {
		t.Errorf("B lookup = %+v, %v, %v", got, ok, err)
	}
}

func TestTenantIsolationEdgeMCPUpstreamIndex(t *testing.T) {
	ctx, s, client := newMCPUpstreamRegistryForTest(t)
	for _, u := range []UpstreamServer{validMCPUpstream("tenant-a", "shared"), validMCPUpstream("tenant-b", "shared"), validMCPUpstream("tenant-b", "private-b")} {
		u.Endpoint = "https://mcp.example.com/" + u.TenantID + "/" + u.Name
		if err := s.Create(ctx, &u); err != nil {
			t.Fatal(err)
		}
	}
	before := edgeKeyspace(t, ctx, client, "*")
	for tenant, want := range map[string][]string{"tenant-a": {"shared"}, "tenant-b": {"private-b", "shared"}} {
		got, err := s.List(ctx, tenant)
		if err != nil || !slices.Equal(mcpUpstreamNames(got), want) {
			t.Errorf("%s list = %v, %v; want %v", tenant, mcpUpstreamNames(got), err, want)
		}
		for _, u := range got {
			if u.TenantID != tenant || u.Endpoint != "https://mcp.example.com/"+tenant+"/"+u.Name {
				t.Errorf("%s list holds %+v", tenant, u)
			}
		}
	}
	if got, ok, err := s.Get(ctx, "tenant-a", "private-b"); err != nil || ok || got != nil {
		t.Errorf("A reached B's upstream: %+v, %v, %v", got, ok, err)
	}
	if after := edgeKeyspace(t, ctx, client, "*"); !reflect.DeepEqual(before, after) {
		t.Errorf("keyspace changed by reads")
	}
}
