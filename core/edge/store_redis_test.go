package edge

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRedisStoreSessionLifecycleIndexesPaginationAndTenantIsolation(t *testing.T) {
	ctx := context.Background()
	store, _, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	base := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	sessions := []EdgeSession{
		validStoreSession("tenant-a", "sess-1", "principal-a", base),
		validStoreSession("tenant-a", "sess-2", "principal-b", base.Add(time.Minute)),
		validStoreSession("tenant-a", "sess-3", "principal-a", base.Add(2*time.Minute)),
		validStoreSession("tenant-b", "sess-4", "principal-a", base.Add(3*time.Minute)),
	}
	for _, session := range sessions {
		if err := store.CreateSession(ctx, session); err != nil {
			t.Fatalf("CreateSession(%s): %v", session.SessionID, err)
		}
	}
	if err := store.CreateSession(ctx, sessions[0]); err == nil {
		t.Fatalf("CreateSession duplicate session_id returned nil error")
	}

	got, ok, err := store.GetSession(ctx, "tenant-a", "sess-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if !ok || got.SessionID != "sess-1" || got.TenantID != "tenant-a" {
		t.Fatalf("GetSession returned (%#v,%v), want tenant-a sess-1 hit", got, ok)
	}
	if crossTenant, ok, err := store.GetSession(ctx, "tenant-b", "sess-1"); err != nil || ok || crossTenant != nil {
		t.Fatalf("cross-tenant GetSession = (%#v,%v,%v), want miss nil,nil", crossTenant, ok, err)
	}

	firstPage, err := store.ListSessions(ctx, ListSessionsQuery{TenantID: "tenant-a", Limit: 2})
	if err != nil {
		t.Fatalf("ListSessions tenant page 1: %v", err)
	}
	assertSessionIDs(t, firstPage.Items, []string{"sess-3", "sess-2"})
	if firstPage.NextCursor == "" {
		t.Fatalf("ListSessions page 1 NextCursor empty, want opaque continuation")
	}
	secondPage, err := store.ListSessions(ctx, ListSessionsQuery{TenantID: "tenant-a", Cursor: firstPage.NextCursor, Limit: 2})
	if err != nil {
		t.Fatalf("ListSessions tenant page 2: %v", err)
	}
	assertSessionIDs(t, secondPage.Items, []string{"sess-1"})
	if secondPage.NextCursor != "" {
		t.Fatalf("ListSessions page 2 NextCursor=%q, want empty", secondPage.NextCursor)
	}

	principalPage, err := store.ListSessions(ctx, ListSessionsQuery{TenantID: "tenant-a", PrincipalID: "principal-a", Limit: 10})
	if err != nil {
		t.Fatalf("ListSessions principal: %v", err)
	}
	assertSessionIDs(t, principalPage.Items, []string{"sess-3", "sess-1"})

	endedAt := base.Add(5 * time.Minute)
	ended, err := store.EndSession(ctx, "tenant-a", "sess-1", endedAt, SessionStatusEnded)
	if err != nil {
		t.Fatalf("EndSession: %v", err)
	}
	if ended.Status != SessionStatusEnded || ended.EndedAt == nil || !ended.EndedAt.Equal(endedAt) {
		t.Fatalf("EndSession returned status/ended_at %#v/%v, want ended/%s", ended.Status, ended.EndedAt, endedAt)
	}
}

func TestRedisStoreExecutionLifecycleAndSecondaryIndexes(t *testing.T) {
	ctx := context.Background()
	store, _, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	base := time.Date(2026, 5, 1, 11, 0, 0, 0, time.UTC)
	if err := store.CreateSession(ctx, validStoreSession("tenant-a", "sess-idx", "principal-a", base)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	executions := []AgentExecution{
		validStoreExecution("tenant-a", "sess-idx", "exec-1", base.Add(time.Minute), func(e *AgentExecution) {
			e.JobID = "job-1"
			e.TraceID = "trace-1"
			e.WorkflowRunID = "run-1"
			e.StepID = "step-a"
		}),
		validStoreExecution("tenant-a", "sess-idx", "exec-2", base.Add(2*time.Minute), func(e *AgentExecution) {
			e.JobID = "job-2"
			e.TraceID = "trace-1"
			e.WorkflowRunID = "run-1"
			e.StepID = "step-b"
		}),
	}
	for _, execution := range executions {
		if err := store.CreateExecution(ctx, execution); err != nil {
			t.Fatalf("CreateExecution(%s): %v", execution.ExecutionID, err)
		}
	}

	got, ok, err := store.GetExecution(ctx, "tenant-a", "exec-1")
	if err != nil {
		t.Fatalf("GetExecution: %v", err)
	}
	if !ok || got.ExecutionID != "exec-1" || got.JobID != "job-1" {
		t.Fatalf("GetExecution returned (%#v,%v), want exec-1 job-1 hit", got, ok)
	}
	if crossTenant, ok, err := store.GetExecution(ctx, "tenant-b", "exec-1"); err != nil || ok || crossTenant != nil {
		t.Fatalf("cross-tenant GetExecution = (%#v,%v,%v), want miss nil,nil", crossTenant, ok, err)
	}

	sessionPage, err := store.ListExecutions(ctx, ListExecutionsQuery{TenantID: "tenant-a", SessionID: "sess-idx", Limit: 10})
	if err != nil {
		t.Fatalf("ListExecutions by session: %v", err)
	}
	assertExecutionIDs(t, sessionPage.Items, []string{"exec-2", "exec-1"})

	jobPage, err := store.ListExecutions(ctx, ListExecutionsQuery{TenantID: "tenant-a", JobID: "job-1", Limit: 10})
	if err != nil {
		t.Fatalf("ListExecutions by job: %v", err)
	}
	assertExecutionIDs(t, jobPage.Items, []string{"exec-1"})

	tracePage, err := store.ListExecutions(ctx, ListExecutionsQuery{TenantID: "tenant-a", TraceID: "trace-1", Limit: 10})
	if err != nil {
		t.Fatalf("ListExecutions by trace: %v", err)
	}
	assertExecutionIDs(t, tracePage.Items, []string{"exec-2", "exec-1"})

	runPage, err := store.ListExecutions(ctx, ListExecutionsQuery{TenantID: "tenant-a", WorkflowRunID: "run-1", Limit: 1})
	if err != nil {
		t.Fatalf("ListExecutions by run: %v", err)
	}
	assertExecutionIDs(t, runPage.Items, []string{"exec-2"})
	if runPage.NextCursor == "" {
		t.Fatalf("ListExecutions by run NextCursor empty, want opaque continuation")
	}

	endedAt := base.Add(10 * time.Minute)
	ended, err := store.EndExecution(ctx, "tenant-a", "exec-1", endedAt, ExecutionStatusSucceeded)
	if err != nil {
		t.Fatalf("EndExecution: %v", err)
	}
	if ended.Status != ExecutionStatusSucceeded || ended.EndedAt == nil || !ended.EndedAt.Equal(endedAt) {
		t.Fatalf("EndExecution returned status/ended_at %#v/%v, want succeeded/%s", ended.Status, ended.EndedAt, endedAt)
	}
}

func TestRedisStoreEventAppendListOrderingPaginationAndFilters(t *testing.T) {
	ctx := context.Background()
	store, _, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	createSessionAndExecution(t, ctx, store, "tenant-a", "sess-events", "exec-events", base)

	first, err := store.AppendEvent(ctx, validStoreEvent("tenant-a", "sess-events", "exec-events", "event-1", 0, base.Add(time.Second), EventKindHookPreToolUse, DecisionAllow))
	if err != nil {
		t.Fatalf("AppendEvent event-1: %v", err)
	}
	if first.Seq != 1 {
		t.Fatalf("AppendEvent auto seq = %d, want 1", first.Seq)
	}
	second, err := store.AppendEvent(ctx, validStoreEvent("tenant-a", "sess-events", "exec-events", "event-2", 2, base.Add(2*time.Second), EventKindHookPolicyDecision, DecisionDeny))
	if err != nil {
		t.Fatalf("AppendEvent event-2: %v", err)
	}
	if second.Seq != 2 {
		t.Fatalf("AppendEvent explicit seq = %d, want 2", second.Seq)
	}
	third, err := store.AppendEvent(ctx, validStoreEvent("tenant-a", "sess-events", "exec-events", "event-3", 3, base.Add(3*time.Second), EventKindApprovalRequested, DecisionRequireApproval))
	if err != nil {
		t.Fatalf("AppendEvent event-3: %v", err)
	}
	if third.Seq != 3 {
		t.Fatalf("AppendEvent explicit seq = %d, want 3", third.Seq)
	}

	all, err := store.ListEvents(ctx, ListEventsQuery{TenantID: "tenant-a", ExecutionID: "exec-events", Limit: 10})
	if err != nil {
		t.Fatalf("ListEvents all: %v", err)
	}
	assertEventIDs(t, all.Items, []string{"event-1", "event-2", "event-3"})

	page1, err := store.ListEvents(ctx, ListEventsQuery{TenantID: "tenant-a", ExecutionID: "exec-events", Limit: 2})
	if err != nil {
		t.Fatalf("ListEvents page1: %v", err)
	}
	assertEventIDs(t, page1.Items, []string{"event-1", "event-2"})
	if page1.NextCursor == "" {
		t.Fatalf("ListEvents page1 NextCursor empty, want continuation")
	}
	page2, err := store.ListEvents(ctx, ListEventsQuery{TenantID: "tenant-a", ExecutionID: "exec-events", Cursor: page1.NextCursor, Limit: 2})
	if err != nil {
		t.Fatalf("ListEvents page2: %v", err)
	}
	assertEventIDs(t, page2.Items, []string{"event-3"})

	kindFiltered, err := store.ListEvents(ctx, ListEventsQuery{TenantID: "tenant-a", ExecutionID: "exec-events", Kind: EventKindHookPolicyDecision, Limit: 10})
	if err != nil {
		t.Fatalf("ListEvents kind filter: %v", err)
	}
	assertEventIDs(t, kindFiltered.Items, []string{"event-2"})

	decisionFiltered, err := store.ListEvents(ctx, ListEventsQuery{TenantID: "tenant-a", ExecutionID: "exec-events", Decision: DecisionRequireApproval, Limit: 10})
	if err != nil {
		t.Fatalf("ListEvents decision filter: %v", err)
	}
	assertEventIDs(t, decisionFiltered.Items, []string{"event-3"})

	if _, err := store.AppendEvent(ctx, validStoreEvent("tenant-a", "sess-events", "exec-events", "event-dup", 2, base.Add(4*time.Second), EventKindHookPostToolUse, DecisionAllow)); err == nil {
		t.Fatalf("AppendEvent duplicate seq returned nil error")
	}
	if _, err := store.AppendEvent(ctx, validStoreEvent("tenant-a", "sess-events", "exec-events", "event-skip", 5, base.Add(5*time.Second), EventKindHookPostToolUse, DecisionAllow)); err == nil {
		t.Fatalf("AppendEvent skipped seq returned nil error")
	}
	if _, err := store.AppendEvent(ctx, validStoreEvent("tenant-b", "sess-events", "exec-events", "event-cross", 4, base.Add(6*time.Second), EventKindHookPostToolUse, DecisionAllow)); err == nil {
		t.Fatalf("AppendEvent cross-tenant execution returned nil error")
	}
}

func TestRedisStoreRejectsOversizeEventBeforeWriting(t *testing.T) {
	ctx := context.Background()
	store, client, _, cleanup := newRedisEdgeStore(t, WithMaxEventBytes(512))
	defer cleanup()

	base := time.Date(2026, 5, 1, 13, 0, 0, 0, time.UTC)
	createSessionAndExecution(t, ctx, store, "tenant-a", "sess-big", "exec-big", base)

	event := validStoreEvent("tenant-a", "sess-big", "exec-big", "event-big", 1, base.Add(time.Second), EventKindHookPreToolUse, DecisionDeny)
	event.InputRedacted = map[string]any{"summary": strings.Repeat("x", 2048)}
	if _, err := store.AppendEvent(ctx, event); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("AppendEvent oversize error = %v, want size rejection", err)
	}
	if n, err := client.LLen(ctx, "edge:events:exec-big").Result(); err != nil || n != 0 {
		t.Fatalf("edge:events:exec-big length = %d, %v; want 0,nil", n, err)
	}
	if seq, err := client.Get(ctx, "edge:events:seq:exec-big").Result(); err == nil {
		t.Fatalf("seq key was written before oversize rejection: %q", seq)
	} else if !errors.Is(err, redis.Nil) {
		t.Fatalf("read seq key after oversize rejection: %v", err)
	}
}

func TestRedisStoreAppendEventsAtomicPrevalidation(t *testing.T) {
	ctx := context.Background()
	store, client, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	base := time.Date(2026, 5, 1, 13, 30, 0, 0, time.UTC)
	createSessionAndExecution(t, ctx, store, "tenant-a", "sess-batch", "exec-batch", base)

	appended, err := store.AppendEvents(ctx, []AgentActionEvent{
		validStoreEvent("tenant-a", "sess-batch", "exec-batch", "event-batch-1", 0, base.Add(time.Second), EventKindHookPreToolUse, DecisionAllow),
		validStoreEvent("tenant-a", "sess-batch", "exec-batch", "event-batch-2", 0, base.Add(2*time.Second), EventKindHookPolicyDecision, DecisionDeny),
	})
	if err != nil {
		t.Fatalf("AppendEvents valid batch: %v", err)
	}
	if len(appended) != 2 || appended[0].Seq != 1 || appended[1].Seq != 2 {
		t.Fatalf("AppendEvents valid batch = %#v, want two events seq 1/2", appended)
	}

	if _, err := store.AppendEvents(ctx, []AgentActionEvent{
		validStoreEvent("tenant-a", "sess-batch", "exec-batch", "event-batch-should-not-append", 0, base.Add(3*time.Second), EventKindHookPostToolUse, DecisionAllow),
		validStoreEvent("tenant-a", "sess-batch", "exec-batch", "event-batch-invalid", 0, time.Time{}, EventKindHookPostToolUse, DecisionAllow),
	}); err == nil {
		t.Fatalf("AppendEvents invalid later event returned nil error")
	}
	if n, err := client.LLen(ctx, "edge:events:exec-batch").Result(); err != nil || n != 2 {
		t.Fatalf("edge:events:exec-batch length after invalid batch = %d, %v; want 2,nil", n, err)
	}
	page, err := store.ListEvents(ctx, ListEventsQuery{TenantID: "tenant-a", ExecutionID: "exec-batch", Limit: 10})
	if err != nil {
		t.Fatalf("ListEvents after invalid batch: %v", err)
	}
	assertEventIDs(t, page.Items, []string{"event-batch-1", "event-batch-2"})
}

func TestRedisStoreHeartbeatTTL(t *testing.T) {
	ctx := context.Background()
	ttl := 3 * time.Second
	store, client, mr, cleanup := newRedisEdgeStore(t, WithHeartbeatTTL(ttl))
	defer cleanup()

	base := time.Date(2026, 5, 1, 14, 0, 0, 0, time.UTC)
	if err := store.CreateSession(ctx, validStoreSession("tenant-a", "sess-heartbeat", "principal-a", base)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := store.TouchHeartbeat(ctx, "tenant-a", "sess-heartbeat"); err != nil {
		t.Fatalf("TouchHeartbeat: %v", err)
	}
	alive, err := store.HeartbeatAlive(ctx, "tenant-a", "sess-heartbeat")
	if err != nil {
		t.Fatalf("HeartbeatAlive: %v", err)
	}
	if !alive {
		t.Fatalf("HeartbeatAlive returned false immediately after TouchHeartbeat")
	}
	if got, err := client.Get(ctx, "edge:session:heartbeat:sess-heartbeat").Result(); err != nil || strings.TrimSpace(got) == "" {
		t.Fatalf("heartbeat key value=%q err=%v, want timestamp value", got, err)
	}
	mr.FastForward(ttl + time.Second)
	alive, err = store.HeartbeatAlive(ctx, "tenant-a", "sess-heartbeat")
	if err != nil {
		t.Fatalf("HeartbeatAlive after TTL: %v", err)
	}
	if alive {
		t.Fatalf("HeartbeatAlive returned true after TTL expiration")
	}
	if err := store.TouchHeartbeat(ctx, "tenant-b", "sess-heartbeat"); err == nil {
		t.Fatalf("TouchHeartbeat cross-tenant session returned nil error")
	}
}

func TestRedisStoreSkipsStaleIndexesAndReportsCorruptRecords(t *testing.T) {
	ctx := context.Background()
	store, client, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	base := time.Date(2026, 5, 1, 15, 0, 0, 0, time.UTC)
	if err := store.CreateSession(ctx, validStoreSession("tenant-a", "sess-good", "principal-a", base)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := client.ZAdd(ctx, "edge:index:tenant:tenant-a", redis.Z{Score: float64(base.Add(time.Minute).UnixMicro()), Member: "sess-missing"}).Err(); err != nil {
		t.Fatalf("seed stale tenant index: %v", err)
	}
	page, err := store.ListSessions(ctx, ListSessionsQuery{TenantID: "tenant-a", Limit: 10})
	if err != nil {
		t.Fatalf("ListSessions with stale index: %v", err)
	}
	assertSessionIDs(t, page.Items, []string{"sess-good"})

	if err := client.Set(ctx, "edge:session:sess-corrupt", "{not-json", 0).Err(); err != nil {
		t.Fatalf("seed corrupt session: %v", err)
	}
	if err := client.ZAdd(ctx, "edge:index:tenant:tenant-a", redis.Z{Score: float64(base.Add(2 * time.Minute).UnixMicro()), Member: "sess-corrupt"}).Err(); err != nil {
		t.Fatalf("seed corrupt tenant index: %v", err)
	}
	if _, err := store.ListSessions(ctx, ListSessionsQuery{TenantID: "tenant-a", Limit: 10}); err == nil || !strings.Contains(err.Error(), "unmarshal") {
		t.Fatalf("ListSessions corrupt record error = %v, want unmarshal error", err)
	}
}

func newRedisEdgeStore(t *testing.T, opts ...StoreOption) (*RedisStore, *redis.Client, *miniredis.Miniredis, func()) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	cleanup := func() {
		_ = client.Close()
		mr.Close()
	}
	return NewRedisStoreFromClient(client, opts...), client, mr, cleanup
}

func createSessionAndExecution(t *testing.T, ctx context.Context, store *RedisStore, tenantID, sessionID, executionID string, started time.Time) {
	t.Helper()
	if err := store.CreateSession(ctx, validStoreSession(tenantID, sessionID, "principal-a", started)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	execution := validStoreExecution(tenantID, sessionID, executionID, started.Add(time.Second), func(e *AgentExecution) {
		e.JobID = "job-" + executionID
		e.TraceID = "trace-" + executionID
		e.WorkflowRunID = "run-" + executionID
	})
	if err := store.CreateExecution(ctx, execution); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
}

func validStoreSession(tenantID, sessionID, principalID string, started time.Time) EdgeSession {
	return EdgeSession{
		SessionID:         sessionID,
		TenantID:          tenantID,
		PrincipalID:       principalID,
		PrincipalType:     PrincipalTypeHuman,
		AgentProduct:      "Claude Code",
		AgentVersion:      "2.1.123",
		Mode:              SessionModeLocalDev,
		Repo:              "cordum",
		GitRemote:         "https://example.invalid/cordum.git",
		GitBranch:         "feature/edge",
		GitSHA:            "abc123",
		CWD:               "/workspace/cordum",
		HostID:            "host-1",
		DeviceID:          "device-1",
		TraceID:           "trace-" + sessionID,
		WorkflowRunID:     "run-" + sessionID,
		JobID:             "job-" + sessionID,
		PolicySnapshot:    "policy-v1",
		EnforcementLayers: EnforcementLayers{"hook": true},
		PolicyMode:        PolicyModeEnforce,
		Status:            SessionStatusRunning,
		RiskSummary:       RiskSummary{DeniedCount: 1, ApprovalCount: 2, ArtifactCount: 3, MaxRisk: RiskLevelHigh},
		StartedAt:         started.UTC(),
		Labels:            Labels{"env": "test"},
	}
}

func validStoreExecution(tenantID, sessionID, executionID string, started time.Time, mutate func(*AgentExecution)) AgentExecution {
	execution := AgentExecution{
		ExecutionID:    executionID,
		SessionID:      sessionID,
		TenantID:       tenantID,
		Adapter:        AdapterClaudeCodeHook,
		Mode:           ExecutionModeLocalDev,
		WorkflowRunID:  "run-" + executionID,
		StepID:         "step-1",
		JobID:          "job-" + executionID,
		Attempt:        1,
		TraceID:        "trace-" + executionID,
		WorkerID:       "worker-1",
		PolicySnapshot: "policy-v1",
		Status:         ExecutionStatusRunning,
		StartedAt:      started.UTC(),
		Metrics:        ExecutionMetrics{Events: 1, Allow: 1, Deny: 0, RequireApproval: 0, Artifacts: 0, LLMCostUSD: 0},
		Labels:         Labels{"env": "test"},
	}
	if mutate != nil {
		mutate(&execution)
	}
	return execution
}

func validStoreEvent(tenantID, sessionID, executionID, eventID string, seq int, at time.Time, kind EventKind, decision EdgeDecision) AgentActionEvent {
	return AgentActionEvent{
		EventID:        eventID,
		SessionID:      sessionID,
		ExecutionID:    executionID,
		TenantID:       tenantID,
		PrincipalID:    "principal-a",
		Seq:            seq,
		Timestamp:      at.UTC(),
		Layer:          LayerHook,
		Kind:           kind,
		AgentProduct:   "Claude Code",
		ToolName:       "Bash",
		ToolUseID:      "tool-" + eventID,
		ActionName:     "bash",
		Capability:     "filesystem.delete",
		RiskTags:       []string{"filesystem"},
		InputRedacted:  map[string]any{"summary": "redacted command"},
		InputHash:      "sha256:" + eventID,
		Decision:       decision,
		DecisionReason: "test decision",
		RuleID:         "rule-1",
		PolicySnapshot: "policy-v1",
		ApprovalRef:    "approval-" + eventID,
		DurationMS:     42,
		Status:         ActionStatusOK,
		Labels:         Labels{"env": "test"},
	}
}

func assertSessionIDs(t *testing.T, got []EdgeSession, want []string) {
	t.Helper()
	ids := make([]string, 0, len(got))
	for _, item := range got {
		ids = append(ids, item.SessionID)
	}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("session ids = %#v, want %#v", ids, want)
	}
}

func assertExecutionIDs(t *testing.T, got []AgentExecution, want []string) {
	t.Helper()
	ids := make([]string, 0, len(got))
	for _, item := range got {
		ids = append(ids, item.ExecutionID)
	}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("execution ids = %#v, want %#v", ids, want)
	}
}

func assertEventIDs(t *testing.T, got []AgentActionEvent, want []string) {
	t.Helper()
	ids := make([]string, 0, len(got))
	for _, item := range got {
		ids = append(ids, item.EventID)
	}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("event ids = %#v, want %#v", ids, want)
	}
}
