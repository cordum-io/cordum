package edge

// Redis unavailable simulations in this file use miniredis.Close() for
// connection-loss paths and miniredis.SetError() for command/pipeline
// failures. miniredis cannot model every production Redis failure mode (for
// example timeout-after-WATCH but before EXEC, kernel-level half-open TCP, or
// cluster failover mid-pipeline), so those remain out of scope unless a fake
// store is introduced explicitly in a targeted test.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRedisStoreCreateSessionReturnsErrorWhenRedisClosed(t *testing.T) {
	ctx := context.Background()
	store, _, mr, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	mr.Close()
	err := store.CreateSession(ctx, validStoreSession("tenant-a", "sess-redis-closed", "principal-a", time.Now().UTC()))
	assertRedisUnavailableError(t, err)
	assertStoreErrorOmitsSyntheticSecrets(t, err)
}

func TestRedisStoreAppendEventsSetErrorLeavesNoPartialEventKeys(t *testing.T) {
	ctx := context.Background()
	store, client, mr, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	base := time.Date(2026, 5, 1, 16, 0, 0, 0, time.UTC)
	createSessionAndExecution(t, ctx, store, "tenant-a", "sess-redis-seterror", "exec-redis-seterror", base)

	mr.SetError("edge redis unavailable")
	_, err := store.AppendEvent(ctx, validStoreEvent("tenant-a", "sess-redis-seterror", "exec-redis-seterror", "evt-seterror-single", 0, base.Add(time.Second), EventKindHookPreToolUse, DecisionAllow))
	assertRedisUnavailableError(t, err)
	assertStoreErrorOmitsSyntheticSecrets(t, err)

	_, err = store.AppendEvents(ctx, []AgentActionEvent{
		validStoreEvent("tenant-a", "sess-redis-seterror", "exec-redis-seterror", "evt-seterror-batch-1", 0, base.Add(2*time.Second), EventKindHookPreToolUse, DecisionAllow),
		validStoreEvent("tenant-a", "sess-redis-seterror", "exec-redis-seterror", "evt-seterror-batch-2", 0, base.Add(3*time.Second), EventKindHookPolicyDecision, DecisionDeny),
	})
	assertRedisUnavailableError(t, err)
	assertStoreErrorOmitsSyntheticSecrets(t, err)

	mr.SetError("")
	page, err := store.ListEvents(ctx, ListEventsQuery{TenantID: "tenant-a", ExecutionID: "exec-redis-seterror", Limit: 10})
	if err != nil {
		t.Fatalf("ListEvents after clearing SetError: %v", err)
	}
	assertEventIDs(t, page.Items, []string{})
	for _, key := range []string{edgeEventsKey("exec-redis-seterror"), edgeEventSeqKey("exec-redis-seterror"), edgeSessionEventsIndexKey("sess-redis-seterror")} {
		exists, err := client.Exists(ctx, key).Result()
		if err != nil {
			t.Fatalf("Exists(%s): %v", key, err)
		}
		if exists != 0 {
			t.Fatalf("key %s exists after failed AppendEvents, want no partial event/index writes", key)
		}
	}
}

func TestSessionExportAssemblerReturnsErrorWhenRedisClosed(t *testing.T) {
	ctx := context.Background()
	env := setupExportTestEnv(t)
	env.mr.Close()

	_, err := (&SessionExportAssembler{Store: env.store}).Assemble(ctx, ExportSessionQuery{TenantID: env.tenantID, SessionID: env.sessionID}, ExportOptions{MaxEvents: 10})
	assertRedisUnavailableError(t, err)
	assertStoreErrorOmitsSyntheticSecrets(t, err)
}

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

// EDGE-054 — CreateExecution must refuse to attach a child execution to a
// parent session that has already transitioned to a terminal status. The
// pre-fix code only checked parent existence at L584-590; the inside-TX
// re-validation introduced by this task closes the TOCTOU window where
// EndSession lands between the GetSession read and the WATCH commit.
//
// This test pre-flips the parent to SessionStatusEnded BEFORE calling
// CreateExecution, which is logically equivalent to "EndSession won the
// race." The unfixed code creates an orphan; the fixed code returns
// ErrParentSessionTerminal and leaves no execution rows behind.
func TestRedisStoreCreateExecutionRefusesOrphanWhenParentAlreadyTerminal(t *testing.T) {
	ctx := context.Background()
	store, client, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	base := time.Date(2026, 5, 1, 11, 30, 0, 0, time.UTC)
	if err := store.CreateSession(ctx, validStoreSession("tenant-a", "sess-orphan-end", "principal-a", base)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := store.EndSession(ctx, "tenant-a", "sess-orphan-end", base.Add(time.Minute), SessionStatusEnded); err != nil {
		t.Fatalf("EndSession: %v", err)
	}

	execution := validStoreExecution("tenant-a", "sess-orphan-end", "exec-orphan-end", base.Add(2*time.Minute), nil)
	err := store.CreateExecution(ctx, execution)
	if err == nil {
		t.Fatalf("CreateExecution succeeded on terminal parent — orphan was created (the EDGE-054 bug)")
	}
	if !errors.Is(err, ErrParentSessionTerminal) {
		t.Fatalf("CreateExecution error = %v, want ErrParentSessionTerminal", err)
	}

	// No execution key, no per-session execution-index entry, no by-job/trace/run
	// index entries. A leaked write would mean the WATCH-set widening missed a key.
	for _, key := range []string{
		edgeExecutionKey("exec-orphan-end"),
		edgeSessionExecutionsIndexKey("sess-orphan-end"),
		edgeJobIndexKey(execution.JobID),
		edgeExecutionTraceIndexKey(execution.TraceID),
		edgeExecutionRunIndexKey(execution.WorkflowRunID),
	} {
		exists, err := client.Exists(ctx, key).Result()
		if err != nil {
			t.Fatalf("Exists(%s): %v", key, err)
		}
		if exists != 0 {
			t.Fatalf("key %s exists after CreateExecution rejected — partial write of an orphan execution", key)
		}
	}

	if got, ok, err := store.GetExecution(ctx, "tenant-a", "exec-orphan-end"); err != nil || ok || got != nil {
		t.Fatalf("GetExecution after orphan rejection = (%#v,%v,%v), want clean miss", got, ok, err)
	}
}

// EDGE-054 — CreateExecution must refuse when the parent session has been
// deleted (DeleteSession). The pre-fix code's L584 GetSession would catch
// this, but the inside-TX re-validation also re-checks under WATCH so a
// concurrent DeleteSession after L584 is still rejected.
func TestRedisStoreCreateExecutionRefusesOrphanWhenParentDeleted(t *testing.T) {
	ctx := context.Background()
	store, client, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	base := time.Date(2026, 5, 1, 11, 45, 0, 0, time.UTC)
	if err := store.CreateSession(ctx, validStoreSession("tenant-a", "sess-orphan-del", "principal-a", base)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := store.DeleteSession(ctx, "tenant-a", "sess-orphan-del"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	execution := validStoreExecution("tenant-a", "sess-orphan-del", "exec-orphan-del", base.Add(2*time.Minute), nil)
	err := store.CreateExecution(ctx, execution)
	if err == nil {
		t.Fatalf("CreateExecution succeeded on deleted parent — orphan was created")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("CreateExecution error = %v, want ErrNotFound (parent deleted)", err)
	}

	for _, key := range []string{
		edgeExecutionKey("exec-orphan-del"),
		edgeSessionExecutionsIndexKey("sess-orphan-del"),
	} {
		exists, err := client.Exists(ctx, key).Result()
		if err != nil {
			t.Fatalf("Exists(%s): %v", key, err)
		}
		if exists != 0 {
			t.Fatalf("key %s exists after CreateExecution rejected — partial write", key)
		}
	}
}

// EDGE-054 — Positive control. Parent active → CreateExecution succeeds.
// Confirms the inside-TX re-validation does not introduce a regression on
// the happy path that all existing tests already exercise indirectly.
func TestRedisStoreCreateExecutionSucceedsWhenParentActive(t *testing.T) {
	ctx := context.Background()
	store, _, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	if err := store.CreateSession(ctx, validStoreSession("tenant-a", "sess-active", "principal-a", base)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	execution := validStoreExecution("tenant-a", "sess-active", "exec-active", base.Add(time.Minute), nil)
	if err := store.CreateExecution(ctx, execution); err != nil {
		t.Fatalf("CreateExecution on active parent: %v", err)
	}
	got, ok, err := store.GetExecution(ctx, "tenant-a", "exec-active")
	if err != nil || !ok || got == nil || got.ExecutionID != "exec-active" {
		t.Fatalf("GetExecution after happy-path create = (%#v,%v,%v), want exec-active", got, ok, err)
	}
}

// EDGE-054 — Concurrent invariant: under simultaneous CreateExecution +
// EndSession bursts on the same parent, every CreateExecution outcome
// must either (a) succeed and persist exactly one execution, or (b) refuse
// with ErrParentSessionTerminal/ErrNotFound and leave no execution row.
// A successful create followed by a successful session end is normal
// session history, not an orphan; the regression this guards is a failed
// create path that still leaks a child execution.
func TestRedisStoreCreateExecutionRefusesOrphanWhenParentEndedConcurrently(t *testing.T) {
	ctx := context.Background()
	store, _, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	base := time.Date(2026, 5, 1, 13, 0, 0, 0, time.UTC)
	const iterations = 16
	for i := 0; i < iterations; i++ {
		sessionID := fmt.Sprintf("sess-race-%d", i)
		executionID := fmt.Sprintf("exec-race-%d", i)
		if err := store.CreateSession(ctx, validStoreSession("tenant-a", sessionID, "principal-a", base.Add(time.Duration(i)*time.Second))); err != nil {
			t.Fatalf("iter %d CreateSession: %v", i, err)
		}

		var wg sync.WaitGroup
		start := make(chan struct{})
		var createErr error
		execution := validStoreExecution("tenant-a", sessionID, executionID, base.Add(time.Duration(i)*time.Second).Add(time.Second), nil)

		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			createErr = store.CreateExecution(ctx, execution)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, _ = store.EndSession(ctx, "tenant-a", sessionID, base.Add(time.Duration(i)*time.Second).Add(2*time.Second), SessionStatusEnded)
		}()
		close(start)
		wg.Wait()

		// Final-state invariant: parent terminal AND child exists is impossible
		// under the fix (both happy-path success-then-end and rejection are OK).
		parent, ok, err := store.GetSession(ctx, "tenant-a", sessionID)
		if err != nil || !ok || parent == nil {
			t.Fatalf("iter %d GetSession: (%#v,%v,%v)", i, parent, ok, err)
		}
		_, childExists, err := store.GetExecution(ctx, "tenant-a", executionID)
		if err != nil {
			t.Fatalf("iter %d GetExecution: %v", i, err)
		}
		if createErr != nil && !errors.Is(createErr, ErrParentSessionTerminal) && !errors.Is(createErr, ErrNotFound) {
			// Any other error suggests an unrelated bug — surface it loudly.
			t.Fatalf("iter %d CreateExecution returned unexpected error: %v", i, createErr)
		}
		if createErr == nil && !childExists {
			t.Fatalf("iter %d CreateExecution succeeded but child execution %s is missing", i, executionID)
		}
		if createErr != nil && childExists {
			t.Fatalf("iter %d CreateExecution failed with %v but child execution %s exists (parent status=%s)",
				i, createErr, executionID, parent.Status)
		}
	}
}

// EDGE-054 — verify the create_execution_aborted_total metric fires with the
// correct bounded reason on each abort path. Uses a stub Recorder embedded in
// NoopRecorder so unrelated method calls (RecordExecutionStarted etc.) are no-ops.
// EDGE-055 — extends the same stub with appendEventsReasons to capture
// AppendEvents abort emissions.
type abortRecorder struct {
	NoopRecorder
	mu                       sync.Mutex
	reasons                  []string
	appendEventsReasons      []string
	eventsPersisted          []eventPersistedCall
	eventCapRejected         int
	cleanupDurations         []time.Duration
	cleanupKeysDeleted       int
	cleanupDeadlines         int
	sessionsSwept            int
	idempotencyTTLExtended   []string
	idempotencyWindowExpired []string
}

type eventPersistedCall struct {
	layer, kind, decision string
}

func (r *abortRecorder) RecordCreateExecutionAborted(reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reasons = append(r.reasons, reason)
}

func (r *abortRecorder) RecordAppendEventsAborted(reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.appendEventsReasons = append(r.appendEventsReasons, reason)
}

func (r *abortRecorder) RecordEventPersisted(_ string, layer string, kind string, decision string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.eventsPersisted = append(r.eventsPersisted, eventPersistedCall{layer: layer, kind: kind, decision: decision})
}

func (r *abortRecorder) RecordSessionEventCapRejected() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.eventCapRejected++
}

func (r *abortRecorder) ObserveSessionCleanupDuration(duration time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanupDurations = append(r.cleanupDurations, duration)
}

func (r *abortRecorder) AddSessionCleanupKeysDeleted(count int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanupKeysDeleted += count
}

func (r *abortRecorder) RecordSessionCleanupDeadline() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanupDeadlines++
}

func (r *abortRecorder) RecordSessionSwept() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessionsSwept++
}

func (r *abortRecorder) Snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.reasons...)
}

func (r *abortRecorder) SnapshotAppendEvents() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.appendEventsReasons...)
}

func (r *abortRecorder) SnapshotEventsPersisted() []eventPersistedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]eventPersistedCall(nil), r.eventsPersisted...)
}

func (r *abortRecorder) SnapshotEventCapRejected() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.eventCapRejected
}

func (r *abortRecorder) SnapshotCleanupMetrics() (int, int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.cleanupDurations), r.cleanupKeysDeleted, r.cleanupDeadlines
}

func (r *abortRecorder) SnapshotSessionsSwept() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessionsSwept
}

// EDGE-061 — extend abortRecorder with idempotency-metric capture so the
// reopen-fix regression test can pin the bounded label contract for both
// new metrics.
func (r *abortRecorder) RecordIdempotencyTTLExtended(state string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.idempotencyTTLExtended = append(r.idempotencyTTLExtended, state)
}

func (r *abortRecorder) RecordIdempotencyWindowExpired(phase string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.idempotencyWindowExpired = append(r.idempotencyWindowExpired, phase)
}

func (r *abortRecorder) SnapshotIdempotencyTTLExtended() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.idempotencyTTLExtended...)
}

func (r *abortRecorder) SnapshotIdempotencyWindowExpired() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.idempotencyWindowExpired...)
}

func TestRedisStoreCreateExecutionAbortedMetricFiresWithBoundedReason(t *testing.T) {
	t.Run("parent_terminal", func(t *testing.T) {
		ctx := context.Background()
		mr := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 1})
		t.Cleanup(func() { _ = client.Close(); mr.Close() })
		rec := &abortRecorder{}
		store := NewRedisStoreFromClient(client, WithRecorder(rec))

		base := time.Date(2026, 5, 1, 12, 30, 0, 0, time.UTC)
		if err := store.CreateSession(ctx, validStoreSession("tenant-a", "sess-metric-term", "principal-a", base)); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		if _, err := store.EndSession(ctx, "tenant-a", "sess-metric-term", base.Add(time.Minute), SessionStatusEnded); err != nil {
			t.Fatalf("EndSession: %v", err)
		}
		execution := validStoreExecution("tenant-a", "sess-metric-term", "exec-metric-term", base.Add(2*time.Minute), nil)
		err := store.CreateExecution(ctx, execution)
		if !errors.Is(err, ErrParentSessionTerminal) {
			t.Fatalf("CreateExecution = %v, want ErrParentSessionTerminal", err)
		}
		got := rec.Snapshot()
		if len(got) != 1 || got[0] != "parent_terminal" {
			t.Fatalf("recorder reasons = %#v, want [parent_terminal]", got)
		}
	})

	t.Run("happy_path_emits_no_abort", func(t *testing.T) {
		ctx := context.Background()
		mr := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 1})
		t.Cleanup(func() { _ = client.Close(); mr.Close() })
		rec := &abortRecorder{}
		store := NewRedisStoreFromClient(client, WithRecorder(rec))

		base := time.Date(2026, 5, 1, 12, 45, 0, 0, time.UTC)
		if err := store.CreateSession(ctx, validStoreSession("tenant-a", "sess-metric-ok", "principal-a", base)); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		execution := validStoreExecution("tenant-a", "sess-metric-ok", "exec-metric-ok", base.Add(time.Minute), nil)
		if err := store.CreateExecution(ctx, execution); err != nil {
			t.Fatalf("CreateExecution: %v", err)
		}
		if got := rec.Snapshot(); len(got) != 0 {
			t.Fatalf("recorder reasons = %#v, want empty (happy path)", got)
		}
	})
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
	assertScoreIDStoreCursor(t, page1.NextCursor, "events", float64(2), "event-2")
	if _, err := store.ListEvents(ctx, ListEventsQuery{TenantID: "tenant-a", ExecutionID: "exec-events", Cursor: "999999999", Limit: 1}); err == nil {
		t.Fatalf("ListEvents accepted fabricated integer cursor, want invalid cursor error")
	}

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

func TestRedisStoreListSessionEventsPagesBeyondOldScanCap(t *testing.T) {
	ctx := context.Background()
	store, _, _, cleanup := newRedisEdgeStore(t, WithMaxEventsPerExecution(maxSessionEventScan+10))
	defer cleanup()

	base := time.Date(2026, 5, 1, 13, 45, 0, 0, time.UTC)
	createSessionAndExecution(t, ctx, store, "tenant-a", "sess-long-events", "exec-long-events", base)

	const batchSize = 500
	total := maxSessionEventScan + 2
	for start := 0; start < total; start += batchSize {
		end := start + batchSize
		if end > total {
			end = total
		}
		events := make([]AgentActionEvent, 0, end-start)
		for i := start; i < end; i++ {
			events = append(events, validStoreEvent(
				"tenant-a",
				"sess-long-events",
				"exec-long-events",
				storePagingID("event-long", i),
				0,
				base.Add(time.Duration(i)*time.Millisecond),
				EventKindHookPostToolUse,
				DecisionAllow,
			))
		}
		if _, err := store.AppendEvents(ctx, events); err != nil {
			t.Fatalf("AppendEvents batch %d..%d: %v", start, end, err)
		}
	}

	var got []string
	cursor := ""
	for {
		page, err := store.ListEvents(ctx, ListEventsQuery{
			TenantID:  "tenant-a",
			SessionID: "sess-long-events",
			Cursor:    cursor,
			Limit:     maxStorePageLimit,
		})
		if err != nil {
			t.Fatalf("ListEvents cursor %q: %v", cursor, err)
		}
		for _, event := range page.Items {
			got = append(got, event.EventID)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
		if len(got) > total {
			t.Fatalf("ListEvents returned more than seeded events: got %d want %d", len(got), total)
		}
	}
	if len(got) != total {
		t.Fatalf("ListEvents by session returned %d events, want complete history %d; first=%q last=%q", len(got), total, got[0], got[len(got)-1])
	}
	if got[0] != storePagingID("event-long", 0) || got[len(got)-1] != storePagingID("event-long", total-1) {
		t.Fatalf("ListEvents order endpoints = %q/%q, want %q/%q", got[0], got[len(got)-1], storePagingID("event-long", 0), storePagingID("event-long", total-1))
	}
}

func TestRedisStoreListSessionEventsUsesBoundedSessionIndexCursor(t *testing.T) {
	ctx := context.Background()
	store, _, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	base := time.Date(2026, 5, 1, 13, 47, 0, 0, time.UTC)
	if err := store.CreateSession(ctx, validStoreSession("tenant-a", "sess-multi-events", "principal-a", base)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	for _, execution := range []AgentExecution{
		validStoreExecution("tenant-a", "sess-multi-events", "exec-older", base.Add(time.Second), nil),
		validStoreExecution("tenant-a", "sess-multi-events", "exec-newer", base.Add(2*time.Second), nil),
	} {
		if err := store.CreateExecution(ctx, execution); err != nil {
			t.Fatalf("CreateExecution(%s): %v", execution.ExecutionID, err)
		}
	}
	for _, event := range []AgentActionEvent{
		validStoreEvent("tenant-a", "sess-multi-events", "exec-older", "event-older-1", 1, base.Add(3*time.Second), EventKindHookPreToolUse, DecisionAllow),
		validStoreEvent("tenant-a", "sess-multi-events", "exec-newer", "event-newer-1", 1, base.Add(4*time.Second), EventKindHookPreToolUse, DecisionAllow),
		validStoreEvent("tenant-a", "sess-multi-events", "exec-newer", "event-newer-2", 2, base.Add(5*time.Second), EventKindHookPolicyDecision, DecisionDeny),
	} {
		if _, err := store.AppendEvent(ctx, event); err != nil {
			t.Fatalf("AppendEvent(%s): %v", event.EventID, err)
		}
	}

	page1, err := store.ListEvents(ctx, ListEventsQuery{TenantID: "tenant-a", SessionID: "sess-multi-events", Limit: 1})
	if err != nil {
		t.Fatalf("ListEvents session page1: %v", err)
	}
	assertEventIDs(t, page1.Items, []string{"event-older-1"})
	assertSessionIndexEventCursor(t, page1.NextCursor, base.Add(3*time.Second), sessionEventIndexMember(page1.Items[0]))

	page2, err := store.ListEvents(ctx, ListEventsQuery{TenantID: "tenant-a", SessionID: "sess-multi-events", Cursor: page1.NextCursor, Limit: 1})
	if err != nil {
		t.Fatalf("ListEvents session page2: %v", err)
	}
	assertEventIDs(t, page2.Items, []string{"event-newer-1"})
	assertSessionIndexEventCursor(t, page2.NextCursor, base.Add(4*time.Second), sessionEventIndexMember(page2.Items[0]))

	page3, err := store.ListEvents(ctx, ListEventsQuery{TenantID: "tenant-a", SessionID: "sess-multi-events", Cursor: page2.NextCursor, Limit: 1})
	if err != nil {
		t.Fatalf("ListEvents session page3: %v", err)
	}
	assertEventIDs(t, page3.Items, []string{"event-newer-2"})
}

func TestRedisStoreAppendEventsRejectsTerminalExecution(t *testing.T) {
	ctx := context.Background()
	store, _, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	base := time.Date(2026, 5, 1, 13, 50, 0, 0, time.UTC)
	createSessionAndExecution(t, ctx, store, "tenant-a", "sess-terminal", "exec-terminal", base)
	if _, err := store.EndExecution(ctx, "tenant-a", "exec-terminal", base.Add(time.Minute), ExecutionStatusSucceeded); err != nil {
		t.Fatalf("EndExecution: %v", err)
	}
	_, err := store.AppendEvents(ctx, []AgentActionEvent{
		validStoreEvent("tenant-a", "sess-terminal", "exec-terminal", "event-after-terminal", 0, base.Add(2*time.Minute), EventKindHookPostToolUse, DecisionAllow),
	})
	if err == nil || !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("AppendEvents terminal execution error = %v, want terminal rejection", err)
	}
	page, err := store.ListEvents(ctx, ListEventsQuery{TenantID: "tenant-a", ExecutionID: "exec-terminal", Limit: 10})
	if err != nil {
		t.Fatalf("ListEvents after terminal append rejection: %v", err)
	}
	assertEventIDs(t, page.Items, []string{})
}

// EDGE-055 — AppendEvents must refuse when the parent session has gone
// terminal, even if the AgentExecution itself is still RUNNING (the
// EDGE-054 sibling pattern applied to AppendEvents). Pre-fix, AppendEvents
// only watched the execution key + executionID re-checked terminal status;
// session-level termination between the outside-TX read and the WATCH
// commit could let events land on a terminal-parent session's still-running
// execution. This test pre-ends the session AFTER createSessionAndExecution
// has built both records (which itself uses store.CreateSession +
// store.CreateExecution while parent is non-terminal — pre-fix path), then
// attempts AppendEvents while the execution stays RUNNING. Without the
// EDGE-055 widening, the append would silently land on a terminal-parent
// execution. With the fix, the inside-TX session re-check fires
// ErrParentSessionTerminal.
func TestRedisStoreAppendEventsRefusesWhenParentSessionAlreadyTerminal(t *testing.T) {
	ctx := context.Background()
	store, client, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	base := time.Date(2026, 5, 1, 13, 55, 0, 0, time.UTC)
	createSessionAndExecution(t, ctx, store, "tenant-a", "sess-edge055-parent", "exec-edge055-parent", base)
	// Pre-end the parent session so the execution is still RUNNING (we did
	// NOT call EndExecution — the execution-level terminal check from
	// TestRedisStoreAppendEventsRejectsTerminalExecution would NOT fire here).
	// EDGE-055 must reject solely on parent-session-terminal grounds.
	if _, err := store.EndSession(ctx, "tenant-a", "sess-edge055-parent", base.Add(time.Minute), SessionStatusEnded); err != nil {
		t.Fatalf("EndSession: %v", err)
	}

	_, err := store.AppendEvents(ctx, []AgentActionEvent{
		validStoreEvent("tenant-a", "sess-edge055-parent", "exec-edge055-parent", "event-after-parent-terminal", 0, base.Add(2*time.Minute), EventKindHookPostToolUse, DecisionAllow),
	})
	if err == nil {
		t.Fatalf("AppendEvents succeeded with terminal parent session — EDGE-055 widening missing; events would land on a terminal-parent execution")
	}
	if !errors.Is(err, ErrParentSessionTerminal) {
		t.Fatalf("AppendEvents err = %v, want ErrParentSessionTerminal (EDGE-055 contract)", err)
	}

	// Negative: NO event should have been persisted. The events list, seq,
	// and id-index keys must all stay empty.
	for _, key := range []string{
		edgeEventsKey("exec-edge055-parent"),
		edgeEventSeqKey("exec-edge055-parent"),
		edgeEventIDIndexKey("exec-edge055-parent"),
	} {
		exists, err := client.Exists(ctx, key).Result()
		if err != nil {
			t.Fatalf("Exists(%s): %v", key, err)
		}
		if exists != 0 {
			t.Fatalf("key %s exists after AppendEvents rejected on terminal-parent — partial write breaks the EDGE-055 invariant", key)
		}
	}

	page, err := store.ListEvents(ctx, ListEventsQuery{TenantID: "tenant-a", ExecutionID: "exec-edge055-parent", Limit: 10})
	if err != nil {
		t.Fatalf("ListEvents after rejection: %v", err)
	}
	assertEventIDs(t, page.Items, []string{})
}

func TestRedisStoreAppendEventsAfterDeleteSessionLeavesNoOrphan(t *testing.T) {
	ctx := context.Background()
	store, client, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	base := time.Date(2026, 5, 1, 14, 10, 0, 0, time.UTC)
	createSessionAndExecution(t, ctx, store, "tenant-a", "sess-delete-append", "exec-delete-append", base)
	if err := store.DeleteSession(ctx, "tenant-a", "sess-delete-append"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	_, err := store.AppendEvents(ctx, []AgentActionEvent{
		validStoreEvent("tenant-a", "sess-delete-append", "exec-delete-append", "event-after-delete", 0, base.Add(time.Minute), EventKindHookPostToolUse, DecisionAllow),
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("AppendEvents after DeleteSession error = %v, want ErrNotFound", err)
	}
	assertRedisKeysGone(t, ctx, client,
		edgeEventsKey("exec-delete-append"),
		edgeEventSeqKey("exec-delete-append"),
		edgeEventIDIndexKey("exec-delete-append"),
	)
}

// EDGE-055 — same contract for the idempotent path. AppendEventsWithIdempotency
// MUST also fire ErrParentSessionTerminal when the parent session is terminal,
// not silently fall through and persist an idempotency-cached event on a
// terminal-parent execution.
func TestRedisStoreAppendEventsWithIdempotencyRefusesWhenParentSessionTerminal(t *testing.T) {
	ctx := context.Background()
	store, _, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	base := time.Date(2026, 5, 1, 14, 5, 0, 0, time.UTC)
	createSessionAndExecution(t, ctx, store, "tenant-a", "sess-edge055-idem-parent", "exec-edge055-idem-parent", base)
	if _, err := store.EndSession(ctx, "tenant-a", "sess-edge055-idem-parent", base.Add(time.Minute), SessionStatusEnded); err != nil {
		t.Fatalf("EndSession: %v", err)
	}

	idemReq := EdgeIdempotencyRequest{
		TenantID:    "tenant-a",
		Endpoint:    "POST /api/v1/edge/events",
		Key:         "edge055-idem-key",
		RequestHash: strings.Repeat("a", 64),
	}
	event := validStoreEvent("tenant-a", "sess-edge055-idem-parent", "exec-edge055-idem-parent", "event-edge055-idem", 0, base.Add(2*time.Minute), EventKindHookPostToolUse, DecisionAllow)
	_, err := store.AppendEventsWithIdempotency(ctx, idemReq, []AgentActionEvent{event}, storeSingleEventReplayResponse)
	if err == nil {
		t.Fatalf("AppendEventsWithIdempotency succeeded with terminal parent — EDGE-055 widening missing on idempotent path")
	}
	if !errors.Is(err, ErrParentSessionTerminal) {
		t.Fatalf("AppendEventsWithIdempotency err = %v, want ErrParentSessionTerminal (EDGE-055 contract on idempotent path)", err)
	}
}

// EDGE-055 — verify the append_events_aborted_total metric fires with the
// correct bounded reason on each abort path. Mirrors
// TestRedisStoreCreateExecutionAbortedMetricFiresWithBoundedReason.
func TestRedisStoreAppendEventsAbortedMetricFiresWithBoundedReason(t *testing.T) {
	t.Run("parent_session_terminal", func(t *testing.T) {
		ctx := context.Background()
		mr := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 1})
		t.Cleanup(func() { _ = client.Close(); mr.Close() })
		rec := &abortRecorder{}
		store := NewRedisStoreFromClient(client, WithRecorder(rec))

		base := time.Date(2026, 5, 1, 15, 0, 0, 0, time.UTC)
		createSessionAndExecution(t, ctx, store, "tenant-a", "sess-edge055-metric-parent", "exec-edge055-metric-parent", base)
		if _, err := store.EndSession(ctx, "tenant-a", "sess-edge055-metric-parent", base.Add(time.Minute), SessionStatusEnded); err != nil {
			t.Fatalf("EndSession: %v", err)
		}
		_, err := store.AppendEvents(ctx, []AgentActionEvent{
			validStoreEvent("tenant-a", "sess-edge055-metric-parent", "exec-edge055-metric-parent", "event-edge055-parent-metric", 0, base.Add(2*time.Minute), EventKindHookPostToolUse, DecisionAllow),
		})
		if !errors.Is(err, ErrParentSessionTerminal) {
			t.Fatalf("AppendEvents = %v, want ErrParentSessionTerminal", err)
		}
		got := rec.SnapshotAppendEvents()
		if len(got) != 1 || got[0] != "parent_session_terminal" {
			t.Fatalf("recorder appendEvents reasons = %#v, want [parent_session_terminal]", got)
		}
	})

	t.Run("execution_terminal", func(t *testing.T) {
		ctx := context.Background()
		mr := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 1})
		t.Cleanup(func() { _ = client.Close(); mr.Close() })
		rec := &abortRecorder{}
		store := NewRedisStoreFromClient(client, WithRecorder(rec))

		base := time.Date(2026, 5, 1, 15, 15, 0, 0, time.UTC)
		createSessionAndExecution(t, ctx, store, "tenant-a", "sess-edge055-metric-exec", "exec-edge055-metric-exec", base)
		if _, err := store.EndExecution(ctx, "tenant-a", "exec-edge055-metric-exec", base.Add(time.Minute), ExecutionStatusFailed); err != nil {
			t.Fatalf("EndExecution: %v", err)
		}
		_, err := store.AppendEvents(ctx, []AgentActionEvent{
			validStoreEvent("tenant-a", "sess-edge055-metric-exec", "exec-edge055-metric-exec", "event-edge055-exec-metric", 0, base.Add(2*time.Minute), EventKindHookPostToolUse, DecisionAllow),
		})
		if err == nil {
			t.Fatal("AppendEvents succeeded with terminal execution")
		}
		got := rec.SnapshotAppendEvents()
		if len(got) != 1 || got[0] != "execution_terminal" {
			t.Fatalf("recorder appendEvents reasons = %#v, want [execution_terminal]", got)
		}
	})

	t.Run("happy_path_emits_no_abort", func(t *testing.T) {
		ctx := context.Background()
		mr := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 1})
		t.Cleanup(func() { _ = client.Close(); mr.Close() })
		rec := &abortRecorder{}
		store := NewRedisStoreFromClient(client, WithRecorder(rec))

		base := time.Date(2026, 5, 1, 15, 30, 0, 0, time.UTC)
		createSessionAndExecution(t, ctx, store, "tenant-a", "sess-edge055-metric-ok", "exec-edge055-metric-ok", base)
		if _, err := store.AppendEvents(ctx, []AgentActionEvent{
			validStoreEvent("tenant-a", "sess-edge055-metric-ok", "exec-edge055-metric-ok", "event-edge055-ok", 0, base.Add(time.Minute), EventKindHookPostToolUse, DecisionAllow),
		}); err != nil {
			t.Fatalf("AppendEvents happy path: %v", err)
		}
		if got := rec.SnapshotAppendEvents(); len(got) != 0 {
			t.Fatalf("recorder appendEvents reasons = %#v, want empty (happy path)", got)
		}
	})
}

func TestEDGE072RedisStoreRecordsEventPersistedOnlyAfterSuccessfulAppend(t *testing.T) {
	ctx := context.Background()
	rec := &abortRecorder{}
	store, _, mr, cleanup := newRedisEdgeStore(t, WithRecorder(rec))
	defer cleanup()

	base := time.Date(2026, 5, 5, 10, 0, 0, 0, time.UTC)
	createSessionAndExecution(t, ctx, store, "tenant-a", "sess-edge072-persisted", "exec-edge072-persisted", base)

	mr.SetError("edge redis unavailable")
	_, err := store.AppendEvents(ctx, []AgentActionEvent{
		validStoreEvent("tenant-a", "sess-edge072-persisted", "exec-edge072-persisted", "event-edge072-failed", 0, base.Add(time.Second), EventKindHookPreToolUse, DecisionAllow),
	})
	assertRedisUnavailableError(t, err)
	if got := rec.SnapshotEventsPersisted(); len(got) != 0 {
		t.Fatalf("RecordEventPersisted fired on failed append: %#v", got)
	}

	mr.SetError("")
	_, err = store.AppendEvents(ctx, []AgentActionEvent{
		validStoreEvent("tenant-a", "sess-edge072-persisted", "exec-edge072-persisted", "event-edge072-ok-1", 0, base.Add(2*time.Second), EventKindHookPreToolUse, DecisionAllow),
		validStoreEvent("tenant-a", "sess-edge072-persisted", "exec-edge072-persisted", "event-edge072-ok-2", 0, base.Add(3*time.Second), EventKindHookPolicyDecision, DecisionDeny),
	})
	if err != nil {
		t.Fatalf("AppendEvents successful batch: %v", err)
	}
	got := rec.SnapshotEventsPersisted()
	want := []eventPersistedCall{
		{layer: "hook", kind: "hook.pre_tool_use", decision: "ALLOW"},
		{layer: "hook", kind: "hook.policy_decision", decision: "DENY"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RecordEventPersisted calls = %#v, want %#v", got, want)
	}
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

func TestRedisStoreTraceRunIndexesStayEntityScopedWhenSessionIDEqualsExecutionID(t *testing.T) {
	ctx := context.Background()
	store, _, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	base := time.Date(2026, 5, 1, 15, 30, 0, 0, time.UTC)
	collisionID := "collision-id"
	traceID := "trace-collision"
	runID := "run-collision"
	sessionToDelete := validStoreSession("tenant-a", collisionID, "principal-a", base)
	sessionToDelete.TraceID = traceID
	sessionToDelete.WorkflowRunID = runID
	if err := store.CreateSession(ctx, sessionToDelete); err != nil {
		t.Fatalf("CreateSession collision session: %v", err)
	}
	ownerSession := validStoreSession("tenant-a", "owner-session", "principal-a", base.Add(time.Minute))
	ownerSession.TraceID = traceID
	ownerSession.WorkflowRunID = runID
	if err := store.CreateSession(ctx, ownerSession); err != nil {
		t.Fatalf("CreateSession owner session: %v", err)
	}
	execution := validStoreExecution("tenant-a", ownerSession.SessionID, collisionID, base.Add(2*time.Minute), func(e *AgentExecution) {
		e.TraceID = traceID
		e.WorkflowRunID = runID
	})
	if err := store.CreateExecution(ctx, execution); err != nil {
		t.Fatalf("CreateExecution collision execution: %v", err)
	}

	beforeTrace, err := store.ListExecutions(ctx, ListExecutionsQuery{TenantID: "tenant-a", TraceID: traceID, Limit: 10})
	if err != nil {
		t.Fatalf("ListExecutions by trace before delete: %v", err)
	}
	assertExecutionIDs(t, beforeTrace.Items, []string{collisionID})
	beforeRun, err := store.ListExecutions(ctx, ListExecutionsQuery{TenantID: "tenant-a", WorkflowRunID: runID, Limit: 10})
	if err != nil {
		t.Fatalf("ListExecutions by run before delete: %v", err)
	}
	assertExecutionIDs(t, beforeRun.Items, []string{collisionID})

	if err := store.DeleteSession(ctx, "tenant-a", collisionID); err != nil {
		t.Fatalf("DeleteSession collision session: %v", err)
	}
	if got, ok, err := store.GetExecution(ctx, "tenant-a", collisionID); err != nil || !ok || got == nil {
		t.Fatalf("GetExecution after deleting same-id session = (%#v,%v,%v), want execution still present", got, ok, err)
	}
	afterTrace, err := store.ListExecutions(ctx, ListExecutionsQuery{TenantID: "tenant-a", TraceID: traceID, Limit: 10})
	if err != nil {
		t.Fatalf("ListExecutions by trace after delete: %v", err)
	}
	assertExecutionIDs(t, afterTrace.Items, []string{collisionID})
	afterRun, err := store.ListExecutions(ctx, ListExecutionsQuery{TenantID: "tenant-a", WorkflowRunID: runID, Limit: 10})
	if err != nil {
		t.Fatalf("ListExecutions by run after delete: %v", err)
	}
	assertExecutionIDs(t, afterRun.Items, []string{collisionID})
}

func TestRedisStoreListSessionsUsesOpaqueCursorPastMaxPageLimit(t *testing.T) {
	ctx := context.Background()
	store, _, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	base := time.Date(2026, 5, 1, 16, 0, 0, 0, time.UTC)
	for i := 0; i < maxStorePageLimit+2; i++ {
		session := validStoreSession("tenant-a", storePagingID("sess", i), "principal-a", base.Add(time.Duration(i)*time.Second))
		if err := store.CreateSession(ctx, session); err != nil {
			t.Fatalf("CreateSession(%s): %v", session.SessionID, err)
		}
	}

	page1, err := store.ListSessions(ctx, ListSessionsQuery{TenantID: "tenant-a", Limit: maxStorePageLimit})
	if err != nil {
		t.Fatalf("ListSessions page1: %v", err)
	}
	assertSessionIDs(t, page1.Items[:3], []string{storePagingID("sess", maxStorePageLimit+1), storePagingID("sess", maxStorePageLimit), storePagingID("sess", maxStorePageLimit-1)})
	if len(page1.Items) != maxStorePageLimit {
		t.Fatalf("ListSessions page1 len=%d, want %d", len(page1.Items), maxStorePageLimit)
	}
	assertOpaqueStoreCursor(t, page1.NextCursor)

	page2, err := store.ListSessions(ctx, ListSessionsQuery{TenantID: "tenant-a", Cursor: page1.NextCursor, Limit: maxStorePageLimit})
	if err != nil {
		t.Fatalf("ListSessions page2: %v", err)
	}
	assertSessionIDs(t, page2.Items, []string{storePagingID("sess", 1), storePagingID("sess", 0)})
	if page2.NextCursor != "" {
		t.Fatalf("ListSessions page2 NextCursor=%q, want empty", page2.NextCursor)
	}
	if _, err := store.ListSessions(ctx, ListSessionsQuery{TenantID: "tenant-a", Cursor: "999999999", Limit: 1}); err == nil {
		t.Fatalf("ListSessions accepted fabricated integer cursor, want invalid cursor error")
	}
}

func TestRedisStoreListSessionsCursorDoesNotSkipSameTimestampRows(t *testing.T) {
	ctx := context.Background()
	store, _, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	startedAt := time.Date(2026, 5, 1, 16, 15, 0, 0, time.UTC)
	for _, id := range []string{"sess-same-a", "sess-same-b", "sess-same-c", "sess-same-d"} {
		if err := store.CreateSession(ctx, validStoreSession("tenant-a", id, "principal-a", startedAt)); err != nil {
			t.Fatalf("CreateSession(%s): %v", id, err)
		}
	}

	page1, err := store.ListSessions(ctx, ListSessionsQuery{TenantID: "tenant-a", Limit: 2})
	if err != nil {
		t.Fatalf("ListSessions same timestamp page1: %v", err)
	}
	if len(page1.Items) != 2 || page1.NextCursor == "" {
		t.Fatalf("ListSessions same timestamp page1 len/cursor = %d/%q, want 2 and cursor", len(page1.Items), page1.NextCursor)
	}
	page2, err := store.ListSessions(ctx, ListSessionsQuery{TenantID: "tenant-a", Cursor: page1.NextCursor, Limit: 2})
	if err != nil {
		t.Fatalf("ListSessions same timestamp page2: %v", err)
	}
	got := append(append([]EdgeSession{}, page1.Items...), page2.Items...)
	if len(got) != 4 {
		t.Fatalf("same-timestamp pagination returned %d sessions, want 4", len(got))
	}
	seen := map[string]bool{}
	for _, session := range got {
		if seen[session.SessionID] {
			t.Fatalf("same-timestamp pagination duplicated session %q", session.SessionID)
		}
		seen[session.SessionID] = true
	}
	for _, id := range []string{"sess-same-a", "sess-same-b", "sess-same-c", "sess-same-d"} {
		if !seen[id] {
			t.Fatalf("same-timestamp pagination skipped session %q; got %#v", id, seen)
		}
	}
}

func TestRedisStoreListSessionsScoreIDCursorSurvivesInsertDeleteBetweenPages(t *testing.T) {
	ctx := context.Background()
	store, _, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	base := time.Date(2026, 5, 1, 16, 20, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		session := validStoreSession("tenant-a", storePagingID("sess-mutate", i), "principal-a", base.Add(time.Duration(i)*time.Second))
		if err := store.CreateSession(ctx, session); err != nil {
			t.Fatalf("CreateSession(%s): %v", session.SessionID, err)
		}
	}
	page1, err := store.ListSessions(ctx, ListSessionsQuery{TenantID: "tenant-a", Limit: 2})
	if err != nil {
		t.Fatalf("ListSessions mutate page1: %v", err)
	}
	assertSessionIDs(t, page1.Items, []string{storePagingID("sess-mutate", 4), storePagingID("sess-mutate", 3)})
	assertScoreIDStoreCursor(t, page1.NextCursor, "sessions", float64(base.Add(3*time.Second).UTC().UnixMicro()), storePagingID("sess-mutate", 3))

	if err := store.DeleteSession(ctx, "tenant-a", storePagingID("sess-mutate", 3)); err != nil {
		t.Fatalf("DeleteSession cursor member: %v", err)
	}
	newer := validStoreSession("tenant-a", "sess-mutate-newer", "principal-a", base.Add(10*time.Second))
	if err := store.CreateSession(ctx, newer); err != nil {
		t.Fatalf("CreateSession newer: %v", err)
	}
	page2, err := store.ListSessions(ctx, ListSessionsQuery{TenantID: "tenant-a", Cursor: page1.NextCursor, Limit: 2})
	if err != nil {
		t.Fatalf("ListSessions mutate page2: %v", err)
	}
	assertSessionIDs(t, page2.Items, []string{storePagingID("sess-mutate", 2), storePagingID("sess-mutate", 1)})
}

func TestRedisStoreListSessionsDeletedSameScoreCursorBeyondFirstWindow(t *testing.T) {
	ctx := context.Background()
	store, _, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	startedAt := time.Date(2026, 5, 1, 16, 25, 0, 0, time.UTC)
	total := maxStorePageLimit*4 + 5
	for i := 0; i < total; i++ {
		session := validStoreSession("tenant-a", fmt.Sprintf("sess-same-delete-%04d", i), "principal-a", startedAt)
		if err := store.CreateSession(ctx, session); err != nil {
			t.Fatalf("CreateSession(%s): %v", session.SessionID, err)
		}
	}

	page1, err := store.ListSessions(ctx, ListSessionsQuery{TenantID: "tenant-a", Limit: maxStorePageLimit})
	if err != nil {
		t.Fatalf("ListSessions same-score page1: %v", err)
	}
	if len(page1.Items) != maxStorePageLimit || page1.NextCursor == "" {
		t.Fatalf("ListSessions same-score page1 len/cursor = %d/%q, want full page and cursor", len(page1.Items), page1.NextCursor)
	}
	page2, err := store.ListSessions(ctx, ListSessionsQuery{TenantID: "tenant-a", Cursor: page1.NextCursor, Limit: maxStorePageLimit})
	if err != nil {
		t.Fatalf("ListSessions same-score page2: %v", err)
	}
	if len(page2.Items) != maxStorePageLimit || page2.NextCursor == "" {
		t.Fatalf("ListSessions same-score page2 len/cursor = %d/%q, want full page and cursor", len(page2.Items), page2.NextCursor)
	}
	page2Cursor := decodeStoreCursorForTest(t, page2.NextCursor)
	if err := store.DeleteSession(ctx, "tenant-a", page2Cursor.ID); err != nil {
		t.Fatalf("DeleteSession cursor member %q: %v", page2Cursor.ID, err)
	}

	page3, err := store.ListSessions(ctx, ListSessionsQuery{TenantID: "tenant-a", Cursor: page2.NextCursor, Limit: maxStorePageLimit})
	if err != nil {
		t.Fatalf("ListSessions same-score page3 after deleted cursor: %v", err)
	}
	if len(page3.Items) != maxStorePageLimit || page3.NextCursor == "" {
		t.Fatalf("ListSessions same-score page3 len/cursor = %d/%q, want full page and cursor after deleted cursor member", len(page3.Items), page3.NextCursor)
	}
	for _, session := range page3.Items {
		if session.SessionID == page2Cursor.ID {
			t.Fatalf("ListSessions page3 returned deleted cursor member %q", page2Cursor.ID)
		}
	}
}

func TestRedisStoreListExecutionsUsesOpaqueCursorPastMaxPageLimit(t *testing.T) {
	ctx := context.Background()
	store, _, _, cleanup := newRedisEdgeStore(t, WithMaxExecutionsPerSession(maxStorePageLimit+3))
	defer cleanup()

	base := time.Date(2026, 5, 1, 16, 30, 0, 0, time.UTC)
	if err := store.CreateSession(ctx, validStoreSession("tenant-a", "sess-exec-page", "principal-a", base)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	for i := 0; i < maxStorePageLimit+2; i++ {
		execution := validStoreExecution("tenant-a", "sess-exec-page", storePagingID("exec", i), base.Add(time.Duration(i)*time.Second), nil)
		if err := store.CreateExecution(ctx, execution); err != nil {
			t.Fatalf("CreateExecution(%s): %v", execution.ExecutionID, err)
		}
	}

	page1, err := store.ListExecutions(ctx, ListExecutionsQuery{TenantID: "tenant-a", SessionID: "sess-exec-page", Limit: maxStorePageLimit})
	if err != nil {
		t.Fatalf("ListExecutions page1: %v", err)
	}
	assertExecutionIDs(t, page1.Items[:3], []string{storePagingID("exec", maxStorePageLimit+1), storePagingID("exec", maxStorePageLimit), storePagingID("exec", maxStorePageLimit-1)})
	if len(page1.Items) != maxStorePageLimit {
		t.Fatalf("ListExecutions page1 len=%d, want %d", len(page1.Items), maxStorePageLimit)
	}
	assertOpaqueStoreCursor(t, page1.NextCursor)
	assertScoreIDStoreCursor(t, page1.NextCursor, "executions", float64(base.Add(2*time.Second).UTC().UnixMicro()), storePagingID("exec", 2))

	page2, err := store.ListExecutions(ctx, ListExecutionsQuery{TenantID: "tenant-a", SessionID: "sess-exec-page", Cursor: page1.NextCursor, Limit: maxStorePageLimit})
	if err != nil {
		t.Fatalf("ListExecutions page2: %v", err)
	}
	assertExecutionIDs(t, page2.Items, []string{storePagingID("exec", 1), storePagingID("exec", 0)})
	if page2.NextCursor != "" {
		t.Fatalf("ListExecutions page2 NextCursor=%q, want empty", page2.NextCursor)
	}
	if _, err := store.ListExecutions(ctx, ListExecutionsQuery{TenantID: "tenant-a", SessionID: "sess-exec-page", Cursor: "999999999", Limit: 1}); err == nil {
		t.Fatalf("ListExecutions accepted fabricated integer cursor, want invalid cursor error")
	}
}

func TestRedisStoreListExecutionsDeletedSameScoreCursorBeyondFirstWindow(t *testing.T) {
	ctx := context.Background()
	startedAt := time.Date(2026, 5, 1, 16, 35, 0, 0, time.UTC)
	total := maxStorePageLimit*4 + 5
	store, client, _, cleanup := newRedisEdgeStore(t, WithMaxExecutionsPerSession(total+1))
	defer cleanup()

	if err := store.CreateSession(ctx, validStoreSession("tenant-a", "sess-exec-same-delete", "principal-a", startedAt)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	for i := 0; i < total; i++ {
		execution := validStoreExecution("tenant-a", "sess-exec-same-delete", fmt.Sprintf("exec-same-delete-%04d", i), startedAt.Add(time.Second), nil)
		if err := store.CreateExecution(ctx, execution); err != nil {
			t.Fatalf("CreateExecution(%s): %v", execution.ExecutionID, err)
		}
	}

	page1, err := store.ListExecutions(ctx, ListExecutionsQuery{TenantID: "tenant-a", SessionID: "sess-exec-same-delete", Limit: maxStorePageLimit})
	if err != nil {
		t.Fatalf("ListExecutions same-score page1: %v", err)
	}
	if len(page1.Items) != maxStorePageLimit || page1.NextCursor == "" {
		t.Fatalf("ListExecutions same-score page1 len/cursor = %d/%q, want full page and cursor", len(page1.Items), page1.NextCursor)
	}
	page2, err := store.ListExecutions(ctx, ListExecutionsQuery{TenantID: "tenant-a", SessionID: "sess-exec-same-delete", Cursor: page1.NextCursor, Limit: maxStorePageLimit})
	if err != nil {
		t.Fatalf("ListExecutions same-score page2: %v", err)
	}
	if len(page2.Items) != maxStorePageLimit || page2.NextCursor == "" {
		t.Fatalf("ListExecutions same-score page2 len/cursor = %d/%q, want full page and cursor", len(page2.Items), page2.NextCursor)
	}
	page2Cursor := decodeStoreCursorForTest(t, page2.NextCursor)
	if removed, err := client.ZRem(ctx, edgeSessionExecutionsIndexKey("sess-exec-same-delete"), page2Cursor.ID).Result(); err != nil || removed != 1 {
		t.Fatalf("ZRem cursor execution member %q = %d, %v; want 1,nil", page2Cursor.ID, removed, err)
	}

	page3, err := store.ListExecutions(ctx, ListExecutionsQuery{TenantID: "tenant-a", SessionID: "sess-exec-same-delete", Cursor: page2.NextCursor, Limit: maxStorePageLimit})
	if err != nil {
		t.Fatalf("ListExecutions same-score page3 after deleted cursor: %v", err)
	}
	if len(page3.Items) != maxStorePageLimit || page3.NextCursor == "" {
		t.Fatalf("ListExecutions same-score page3 len/cursor = %d/%q, want full page and cursor after deleted cursor member", len(page3.Items), page3.NextCursor)
	}
	for _, execution := range page3.Items {
		if execution.ExecutionID == page2Cursor.ID {
			t.Fatalf("ListExecutions page3 returned deleted cursor member %q", page2Cursor.ID)
		}
	}
}

func TestRedisStoreEdgeIdempotencyReserveCompleteReplayTTLConflictAndNoRawKey(t *testing.T) {
	ctx := context.Background()
	store, client, mr, cleanup := newRedisEdgeStore(t, WithIdempotencyTTL(time.Second))
	defer cleanup()

	req := EdgeIdempotencyRequest{
		TenantID:    "tenant-a",
		Endpoint:    "POST /api/v1/edge/events",
		Key:         "raw-client-key-must-not-be-stored",
		RequestHash: "sha256:request-a",
	}
	reserved, err := store.ReserveIdempotency(ctx, req)
	if err != nil {
		t.Fatalf("ReserveIdempotency first: %v", err)
	}
	if reserved.State != EdgeIdempotencyReserved {
		t.Fatalf("first reserve state = %q, want reserved", reserved.State)
	}
	pending, err := store.ReserveIdempotency(ctx, req)
	if err != nil {
		t.Fatalf("ReserveIdempotency pending: %v", err)
	}
	if pending.State != EdgeIdempotencyPending {
		t.Fatalf("second reserve before complete state = %q, want pending", pending.State)
	}

	response := EdgeIdempotencyResponse{
		StatusCode:  201,
		ContentType: "application/json",
		Body:        []byte(`{"event_id":"evt-1"}`),
	}
	completed, err := store.CompleteIdempotency(ctx, req, response)
	if err != nil {
		t.Fatalf("CompleteIdempotency: %v", err)
	}
	if completed.Status != EdgeIdempotencyCompleted || string(completed.Response.Body) != string(response.Body) {
		t.Fatalf("completed record = %#v, want completed replay body", completed)
	}
	replay, err := store.ReserveIdempotency(ctx, req)
	if err != nil {
		t.Fatalf("ReserveIdempotency replay: %v", err)
	}
	if replay.State != EdgeIdempotencyReplay || replay.Record == nil || string(replay.Record.Response.Body) != string(response.Body) {
		t.Fatalf("replay = %#v, want cached response body", replay)
	}

	keys, err := client.Keys(ctx, "edge:idempotency:*").Result()
	if err != nil {
		t.Fatalf("list idempotency keys: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("idempotency key count = %d, want 1: %#v", len(keys), keys)
	}
	if strings.Contains(keys[0], req.Key) {
		t.Fatalf("redis idempotency key leaked raw client key: %q", keys[0])
	}
	raw, err := client.Get(ctx, keys[0]).Result()
	if err != nil {
		t.Fatalf("read idempotency record: %v", err)
	}
	if strings.Contains(raw, req.Key) {
		t.Fatalf("idempotency record leaked raw client key: %s", raw)
	}

	conflicting := req
	conflicting.RequestHash = "sha256:request-b"
	if _, err := store.ReserveIdempotency(ctx, conflicting); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("ReserveIdempotency conflict error = %v, want ErrIdempotencyConflict", err)
	}

	mr.FastForward(time.Second + time.Millisecond)
	afterTTL, err := store.ReserveIdempotency(ctx, conflicting)
	if err != nil {
		t.Fatalf("ReserveIdempotency after TTL: %v", err)
	}
	if afterTTL.State != EdgeIdempotencyReserved {
		t.Fatalf("reserve after TTL state = %q, want reserved", afterTTL.State)
	}
}

// EDGE-061 — pending-branch retry on ReserveIdempotency must refresh the
// redis key TTL so a long-running flow that retries past the initial TTL
// keeps once-semantics. Pre-fix, the pending-branch returned without
// refreshing TTL, allowing the key to expire mid-flight and a later retry
// to be processed as fresh.
//
// Strategy A+B: TTL extension on retry. The 7-day cap (Strategy B) is
// pinned by TestRedisStoreIdempotencyReserveRejects7DayCappedRecord and
// TestRedisStoreIdempotencyCompleteRejects7DayCappedRecord below.
func TestRedisStoreIdempotencyPendingRetryRefreshesTTL(t *testing.T) {
	ctx := context.Background()
	store, client, mr, cleanup := newRedisEdgeStore(t, WithIdempotencyTTL(2*time.Second))
	defer cleanup()

	req := EdgeIdempotencyRequest{
		TenantID:    "tenant-a",
		Endpoint:    "POST /api/v1/edge/events",
		Key:         "edge061-long-running",
		RequestHash: "sha256:edge061-long",
	}

	first, err := store.ReserveIdempotency(ctx, req)
	if err != nil {
		t.Fatalf("first ReserveIdempotency: %v", err)
	}
	if first.State != EdgeIdempotencyReserved {
		t.Fatalf("first reserve state = %q, want reserved", first.State)
	}

	keys, err := client.Keys(ctx, "edge:idempotency:*").Result()
	if err != nil || len(keys) != 1 {
		t.Fatalf("idempotency key count = %d (err=%v), want 1: %#v", len(keys), err, keys)
	}
	idemKey := keys[0]

	// Advance most of the way through the TTL but stop short.
	mr.FastForward(1500 * time.Millisecond)

	// Pending-retry: the second Reserve sees the in-flight record. EDGE-061
	// requires that the redis key TTL is refreshed back to the full
	// idempotencyTTL — otherwise the next FastForward will expire the key.
	second, err := store.ReserveIdempotency(ctx, req)
	if err != nil {
		t.Fatalf("pending ReserveIdempotency: %v", err)
	}
	if second.State != EdgeIdempotencyPending {
		t.Fatalf("second reserve state = %q, want pending", second.State)
	}

	// Confirm the redis-side TTL is now back to ~full TTL (within tolerance
	// for miniredis float-rounding). Pre-fix, this remaining TTL is < 1s
	// (1.5s elapsed of 2s); post-fix, it is ~2s.
	ttl, err := client.TTL(ctx, idemKey).Result()
	if err != nil {
		t.Fatalf("TTL(%s): %v", idemKey, err)
	}
	if ttl < 1500*time.Millisecond {
		t.Fatalf("TTL after pending retry = %v, want ~2s (refresh missing — EDGE-061 fix not applied at ReserveIdempotency pending branch)", ttl)
	}

	// Sanity: advance another 1.5s. With refresh, total real time passed is
	// 3s but only 1.5s since the last Reserve, so the key is still alive.
	// Without the refresh, the key would have expired at the 2s mark and
	// the third Reserve would create a new record (state=Reserved).
	mr.FastForward(1500 * time.Millisecond)
	third, err := store.ReserveIdempotency(ctx, req)
	if err != nil {
		t.Fatalf("third ReserveIdempotency: %v", err)
	}
	if third.State != EdgeIdempotencyPending {
		t.Fatalf("third reserve state = %q, want pending (key should still be alive after refresh) — EDGE-061 once-semantics broken", third.State)
	}
}

// EDGE-061 — ReserveIdempotency must reject records older than the
// 7-day max-in-flight window with ErrIdempotencyRecordExpired so a
// stuck long-running request cannot hold the key forever (Strategy B
// cap). Pre-fix, the entry handler reads the existing record and
// returns it as Pending regardless of age.
func TestRedisStoreIdempotencyReserveRejects7DayCappedRecord(t *testing.T) {
	ctx := context.Background()
	// Use a fake clock we can advance past the 7-day cap without redis-side
	// TTL expiry interfering. Set TTL large enough that the redis key stays
	// alive across the simulated 8-day jump.
	clock := &mutableClock{now: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)}
	store, _, _, cleanup := newRedisEdgeStore(t,
		WithIdempotencyTTL(30*24*time.Hour),
		WithClock(clock.Now),
	)
	defer cleanup()

	req := EdgeIdempotencyRequest{
		TenantID:    "tenant-a",
		Endpoint:    "POST /api/v1/edge/events",
		Key:         "edge061-cap-reserve",
		RequestHash: "sha256:edge061-cap",
	}

	first, err := store.ReserveIdempotency(ctx, req)
	if err != nil {
		t.Fatalf("first ReserveIdempotency: %v", err)
	}
	if first.State != EdgeIdempotencyReserved {
		t.Fatalf("first reserve state = %q, want reserved", first.State)
	}

	// Jump 8 days forward — past the 7-day cap. The redis key is still
	// present (TTL > 8d), but the record's CreatedAt is now > 7d in the past.
	clock.Advance(8 * 24 * time.Hour)

	if _, err := store.ReserveIdempotency(ctx, req); !errors.Is(err, ErrIdempotencyRecordExpired) {
		t.Fatalf("ReserveIdempotency past 7-day cap err = %v, want ErrIdempotencyRecordExpired", err)
	}
}

// EDGE-061 — CompleteIdempotency must also enforce the 7-day cap. A
// stuck request whose record sits unresolved past the cap cannot
// complete; the caller must observe a typed error rather than silently
// transition to a stale record.
func TestRedisStoreIdempotencyCompleteRejects7DayCappedRecord(t *testing.T) {
	ctx := context.Background()
	clock := &mutableClock{now: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)}
	store, _, _, cleanup := newRedisEdgeStore(t,
		WithIdempotencyTTL(30*24*time.Hour),
		WithClock(clock.Now),
	)
	defer cleanup()

	req := EdgeIdempotencyRequest{
		TenantID:    "tenant-a",
		Endpoint:    "POST /api/v1/edge/events",
		Key:         "edge061-cap-complete",
		RequestHash: "sha256:edge061-cap-c",
	}
	if _, err := store.ReserveIdempotency(ctx, req); err != nil {
		t.Fatalf("ReserveIdempotency: %v", err)
	}

	clock.Advance(8 * 24 * time.Hour)

	resp := EdgeIdempotencyResponse{StatusCode: 201, ContentType: "application/json", Body: []byte(`{"ok":true}`)}
	if _, err := store.CompleteIdempotency(ctx, req, resp); !errors.Is(err, ErrIdempotencyRecordExpired) {
		t.Fatalf("CompleteIdempotency past 7-day cap err = %v, want ErrIdempotencyRecordExpired", err)
	}
}

// mutableClock is a tiny mockable now-fn used only by EDGE-061 cap-check
// tests. The miniredis FastForward used by sibling tests advances redis
// time but not s.now; for cap-check tests we want the inverse — advance
// s.now without expiring redis keys.
type mutableClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *mutableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mutableClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// EDGE-061 reopen #1 — verify both new metrics fire with bounded labels.
// Mirrors TestRedisStoreCreateExecutionAbortedMetricFiresWithBoundedReason.
func TestRedisStoreIdempotencyMetricsFireWithBoundedLabels(t *testing.T) {
	t.Run("ttl_extended_pending", func(t *testing.T) {
		ctx := context.Background()
		mr := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 1})
		t.Cleanup(func() { _ = client.Close(); mr.Close() })
		rec := &abortRecorder{}
		store := NewRedisStoreFromClient(client, WithRecorder(rec), WithIdempotencyTTL(2*time.Second))

		req := EdgeIdempotencyRequest{
			TenantID:    "tenant-a",
			Endpoint:    "POST /api/v1/edge/events",
			Key:         "edge061-metric-pending",
			RequestHash: "sha256:edge061-metric-pending",
		}
		if _, err := store.ReserveIdempotency(ctx, req); err != nil {
			t.Fatalf("first ReserveIdempotency: %v", err)
		}
		// First Reserve takes the Reserved branch (no TTL extension).
		if got := rec.SnapshotIdempotencyTTLExtended(); len(got) != 0 {
			t.Fatalf("first Reserve TTL-extended reasons = %#v, want empty", got)
		}
		// Second Reserve hits the pending branch and refreshes TTL.
		if _, err := store.ReserveIdempotency(ctx, req); err != nil {
			t.Fatalf("pending ReserveIdempotency: %v", err)
		}
		got := rec.SnapshotIdempotencyTTLExtended()
		if len(got) != 1 || got[0] != "pending" {
			t.Fatalf("TTL-extended reasons = %#v, want [pending]", got)
		}
	})

	t.Run("ttl_extended_replay", func(t *testing.T) {
		ctx := context.Background()
		mr := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 1})
		t.Cleanup(func() { _ = client.Close(); mr.Close() })
		rec := &abortRecorder{}
		store := NewRedisStoreFromClient(client, WithRecorder(rec))

		req := EdgeIdempotencyRequest{
			TenantID:    "tenant-a",
			Endpoint:    "POST /api/v1/edge/events",
			Key:         "edge061-metric-replay",
			RequestHash: "sha256:edge061-metric-replay",
		}
		if _, err := store.ReserveIdempotency(ctx, req); err != nil {
			t.Fatalf("ReserveIdempotency: %v", err)
		}
		if _, err := store.CompleteIdempotency(ctx, req, EdgeIdempotencyResponse{
			StatusCode:  201,
			ContentType: "application/json",
			Body:        []byte(`{"event_id":"evt-replay"}`),
		}); err != nil {
			t.Fatalf("CompleteIdempotency: %v", err)
		}
		// The Reserve immediately after Complete takes the replay branch and refreshes TTL.
		replay, err := store.ReserveIdempotency(ctx, req)
		if err != nil {
			t.Fatalf("Reserve after Complete: %v", err)
		}
		if replay.State != EdgeIdempotencyReplay {
			t.Fatalf("Reserve after Complete state = %q, want replay", replay.State)
		}
		got := rec.SnapshotIdempotencyTTLExtended()
		if len(got) != 1 || got[0] != "replay" {
			t.Fatalf("TTL-extended reasons = %#v, want [replay]", got)
		}
	})

	t.Run("window_expired_reserve", func(t *testing.T) {
		ctx := context.Background()
		clock := &mutableClock{now: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)}
		mr := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 1})
		t.Cleanup(func() { _ = client.Close(); mr.Close() })
		rec := &abortRecorder{}
		store := NewRedisStoreFromClient(client,
			WithRecorder(rec),
			WithClock(clock.Now),
			WithIdempotencyTTL(30*24*time.Hour),
		)
		req := EdgeIdempotencyRequest{
			TenantID:    "tenant-a",
			Endpoint:    "POST /api/v1/edge/events",
			Key:         "edge061-metric-cap-reserve",
			RequestHash: "sha256:edge061-metric-cap-reserve",
		}
		if _, err := store.ReserveIdempotency(ctx, req); err != nil {
			t.Fatalf("ReserveIdempotency: %v", err)
		}
		clock.Advance(8 * 24 * time.Hour)
		if _, err := store.ReserveIdempotency(ctx, req); !errors.Is(err, ErrIdempotencyRecordExpired) {
			t.Fatalf("ReserveIdempotency past cap = %v, want ErrIdempotencyRecordExpired", err)
		}
		got := rec.SnapshotIdempotencyWindowExpired()
		if len(got) != 1 || got[0] != "reserve" {
			t.Fatalf("window-expired phases = %#v, want [reserve]", got)
		}
	})

	t.Run("window_expired_complete", func(t *testing.T) {
		ctx := context.Background()
		clock := &mutableClock{now: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)}
		mr := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 1})
		t.Cleanup(func() { _ = client.Close(); mr.Close() })
		rec := &abortRecorder{}
		store := NewRedisStoreFromClient(client,
			WithRecorder(rec),
			WithClock(clock.Now),
			WithIdempotencyTTL(30*24*time.Hour),
		)
		req := EdgeIdempotencyRequest{
			TenantID:    "tenant-a",
			Endpoint:    "POST /api/v1/edge/events",
			Key:         "edge061-metric-cap-complete",
			RequestHash: "sha256:edge061-metric-cap-complete",
		}
		if _, err := store.ReserveIdempotency(ctx, req); err != nil {
			t.Fatalf("ReserveIdempotency: %v", err)
		}
		clock.Advance(8 * 24 * time.Hour)
		resp := EdgeIdempotencyResponse{StatusCode: 201, ContentType: "application/json", Body: []byte(`{"ok":true}`)}
		if _, err := store.CompleteIdempotency(ctx, req, resp); !errors.Is(err, ErrIdempotencyRecordExpired) {
			t.Fatalf("CompleteIdempotency past cap = %v, want ErrIdempotencyRecordExpired", err)
		}
		got := rec.SnapshotIdempotencyWindowExpired()
		if len(got) != 1 || got[0] != "complete" {
			t.Fatalf("window-expired phases = %#v, want [complete]", got)
		}
	})

	t.Run("happy_path_emits_no_metric", func(t *testing.T) {
		ctx := context.Background()
		mr := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 1})
		t.Cleanup(func() { _ = client.Close(); mr.Close() })
		rec := &abortRecorder{}
		store := NewRedisStoreFromClient(client, WithRecorder(rec))

		req := EdgeIdempotencyRequest{
			TenantID:    "tenant-a",
			Endpoint:    "POST /api/v1/edge/events",
			Key:         "edge061-metric-ok",
			RequestHash: "sha256:edge061-metric-ok",
		}
		if _, err := store.ReserveIdempotency(ctx, req); err != nil {
			t.Fatalf("Reserve: %v", err)
		}
		if _, err := store.CompleteIdempotency(ctx, req, EdgeIdempotencyResponse{StatusCode: 201, ContentType: "application/json", Body: []byte(`{"ok":true}`)}); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if got := rec.SnapshotIdempotencyTTLExtended(); len(got) != 0 {
			t.Fatalf("TTL-extended on first Reserve+Complete = %#v, want empty", got)
		}
		if got := rec.SnapshotIdempotencyWindowExpired(); len(got) != 0 {
			t.Fatalf("window-expired on first Reserve+Complete = %#v, want empty", got)
		}
	})
}

func TestRedisStoreEdgeIdempotencyConcurrentReserveSingleWriter(t *testing.T) {
	ctx := context.Background()
	store, _, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	req := EdgeIdempotencyRequest{
		TenantID:    "tenant-a",
		Endpoint:    "POST /api/v1/edge/events/batch",
		Key:         "concurrent-edge-idempotency",
		RequestHash: "sha256:batch",
	}
	const workers = 8
	var wg sync.WaitGroup
	states := make(chan EdgeIdempotencyState, workers)
	errs := make(chan error, workers)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			reservation, err := store.ReserveIdempotency(ctx, req)
			if err != nil {
				errs <- err
				return
			}
			states <- reservation.State
		}()
	}
	close(start)
	wg.Wait()
	close(states)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent ReserveIdempotency error: %v", err)
	}
	counts := map[EdgeIdempotencyState]int{}
	for state := range states {
		counts[state]++
	}
	if counts[EdgeIdempotencyReserved] != 1 {
		t.Fatalf("reserved count = %d, want exactly 1 (all states=%#v)", counts[EdgeIdempotencyReserved], counts)
	}
	if counts[EdgeIdempotencyPending] != workers-1 {
		t.Fatalf("pending count = %d, want %d (all states=%#v)", counts[EdgeIdempotencyPending], workers-1, counts)
	}

	if _, err := store.CompleteIdempotency(ctx, req, EdgeIdempotencyResponse{StatusCode: 201, Body: []byte(`{"items":[]}`)}); err != nil {
		t.Fatalf("CompleteIdempotency after concurrent reserve: %v", err)
	}
	replay, err := store.ReserveIdempotency(ctx, req)
	if err != nil {
		t.Fatalf("ReserveIdempotency replay after concurrent reserve: %v", err)
	}
	if replay.State != EdgeIdempotencyReplay {
		t.Fatalf("state after complete = %q, want replay", replay.State)
	}
}

func TestRedisStoreAppendEventsWithIdempotencyAtomicallyAppendsAndReplays(t *testing.T) {
	ctx := context.Background()
	store, _, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()
	base := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	createSessionAndExecution(t, ctx, store, "tenant-a", "sess-idem-atomic", "exec-idem-atomic", base)
	req := EdgeIdempotencyRequest{
		TenantID:    "tenant-a",
		Endpoint:    "POST /api/v1/edge/events",
		Key:         "atomic-key",
		RequestHash: "sha256:atomic-body",
	}
	event := validStoreEvent("tenant-a", "sess-idem-atomic", "exec-idem-atomic", "event-idem-atomic", 0, base.Add(time.Minute), EventKindHookPreToolUse, DecisionAllow)

	first, err := store.AppendEventsWithIdempotency(ctx, req, []AgentActionEvent{event}, storeSingleEventReplayResponse)
	if err != nil {
		t.Fatalf("AppendEventsWithIdempotency first: %v", err)
	}
	if first.State != EdgeIdempotencyCompleted || len(first.Events) != 1 || first.Events[0].Seq != 1 {
		t.Fatalf("first result = %#v, want completed event with seq 1", first)
	}
	if first.Record == nil || first.Record.Status != EdgeIdempotencyCompleted || len(first.Record.Response.Body) == 0 {
		t.Fatalf("first replay record = %#v, want completed response", first.Record)
	}
	replay, err := store.AppendEventsWithIdempotency(ctx, req, []AgentActionEvent{event}, storeSingleEventReplayResponse)
	if err != nil {
		t.Fatalf("AppendEventsWithIdempotency replay: %v", err)
	}
	if replay.State != EdgeIdempotencyReplay || replay.Record == nil || string(replay.Record.Response.Body) != string(first.Record.Response.Body) {
		t.Fatalf("replay result = %#v, want first response body", replay)
	}
	page, err := store.ListEvents(ctx, ListEventsQuery{TenantID: "tenant-a", ExecutionID: "exec-idem-atomic"})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	assertEventIDs(t, page.Items, []string{"event-idem-atomic"})
}

func TestRedisStoreLoadCompletedAppendReplayAfterDuplicateEvent(t *testing.T) {
	ctx := context.Background()
	store, _, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()
	base := time.Date(2026, 5, 2, 12, 15, 0, 0, time.UTC)
	createSessionAndExecution(t, ctx, store, "tenant-a", "sess-idem-race", "exec-idem-race", base)
	req := EdgeIdempotencyRequest{
		TenantID:    "tenant-a",
		Endpoint:    "POST /api/v1/edge/events/batch",
		Key:         "race-replay-key",
		RequestHash: "sha256:race-replay-body",
	}
	event := validStoreEvent("tenant-a", "sess-idem-race", "exec-idem-race", "event-idem-race", 0, base.Add(time.Minute), EventKindHookPreToolUse, DecisionAllow)
	first, err := store.AppendEventsWithIdempotency(ctx, req, []AgentActionEvent{event}, storeSingleEventReplayResponse)
	if err != nil {
		t.Fatalf("AppendEventsWithIdempotency first: %v", err)
	}
	key := edgeIdempotencyKey(req.TenantID, req.Endpoint, req.Key)
	replay, ok, err := store.loadCompletedAppendReplay(ctx, key, req)
	if err != nil {
		t.Fatalf("loadCompletedAppendReplay: %v", err)
	}
	if !ok || replay.State != EdgeIdempotencyReplay || replay.Record == nil {
		t.Fatalf("replay result = %#v ok=%v, want replay record", replay, ok)
	}
	if string(replay.Record.Response.Body) != string(first.Record.Response.Body) {
		t.Fatalf("replay body = %s, want %s", replay.Record.Response.Body, first.Record.Response.Body)
	}
}

func TestRedisStoreAppendEventsWithIdempotencyRejectsDuplicateEventAfterReplayTTL(t *testing.T) {
	ctx := context.Background()
	store, _, mr, cleanup := newRedisEdgeStore(t, WithIdempotencyTTL(time.Second))
	defer cleanup()
	base := time.Date(2026, 5, 2, 12, 30, 0, 0, time.UTC)
	createSessionAndExecution(t, ctx, store, "tenant-a", "sess-idem-ttl", "exec-idem-ttl", base)
	req := EdgeIdempotencyRequest{
		TenantID:    "tenant-a",
		Endpoint:    "POST /api/v1/edge/events",
		Key:         "ttl-key",
		RequestHash: "sha256:ttl-body",
	}
	event := validStoreEvent("tenant-a", "sess-idem-ttl", "exec-idem-ttl", "event-idem-ttl", 0, base.Add(time.Minute), EventKindHookPreToolUse, DecisionAllow)

	if _, err := store.AppendEventsWithIdempotency(ctx, req, []AgentActionEvent{event}, storeSingleEventReplayResponse); err != nil {
		t.Fatalf("AppendEventsWithIdempotency first: %v", err)
	}
	mr.FastForward(time.Second + time.Millisecond)
	if _, err := store.AppendEventsWithIdempotency(ctx, req, []AgentActionEvent{event}, storeSingleEventReplayResponse); !errors.Is(err, ErrIdempotencyWindowExpired) {
		t.Fatalf("AppendEventsWithIdempotency after TTL error = %v, want ErrIdempotencyWindowExpired", err)
	}
	page, err := store.ListEvents(ctx, ListEventsQuery{TenantID: "tenant-a", ExecutionID: "exec-idem-ttl"})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	assertEventIDs(t, page.Items, []string{"event-idem-ttl"})
}

func TestRedisStoreAppendEventsWithIdempotencyDoesNotAppendWhenReplayBuildFails(t *testing.T) {
	ctx := context.Background()
	store, client, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()
	base := time.Date(2026, 5, 2, 13, 0, 0, 0, time.UTC)
	createSessionAndExecution(t, ctx, store, "tenant-a", "sess-idem-build-fail", "exec-idem-build-fail", base)
	req := EdgeIdempotencyRequest{
		TenantID:    "tenant-a",
		Endpoint:    "POST /api/v1/edge/events",
		Key:         "build-failure-key",
		RequestHash: "sha256:build-failure-body",
	}
	event := validStoreEvent("tenant-a", "sess-idem-build-fail", "exec-idem-build-fail", "event-idem-build-fail", 0, base.Add(time.Minute), EventKindHookPreToolUse, DecisionAllow)

	_, err := store.AppendEventsWithIdempotency(ctx, req, []AgentActionEvent{event}, func([]AgentActionEvent) (EdgeIdempotencyResponse, error) {
		return EdgeIdempotencyResponse{}, errors.New("injected replay build failure")
	})
	if err == nil {
		t.Fatal("AppendEventsWithIdempotency returned nil error, want replay build failure")
	}
	page, err := store.ListEvents(ctx, ListEventsQuery{TenantID: "tenant-a", ExecutionID: "exec-idem-build-fail"})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	assertEventIDs(t, page.Items, []string{})
	keys, err := client.Keys(ctx, "edge:idempotency:*").Result()
	if err != nil {
		t.Fatalf("list idempotency keys: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("idempotency key count after failed atomic append = %d, want 0 keys=%v", len(keys), keys)
	}
}

func storeSingleEventReplayResponse(events []AgentActionEvent) (EdgeIdempotencyResponse, error) {
	if len(events) != 1 {
		return EdgeIdempotencyResponse{}, fmt.Errorf("expected one event, got %d", len(events))
	}
	body, err := json.Marshal(events[0])
	if err != nil {
		return EdgeIdempotencyResponse{}, err
	}
	return EdgeIdempotencyResponse{StatusCode: 201, ContentType: "application/json", Body: body}, nil
}

func assertRedisUnavailableError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("operation returned nil error after Redis was made unavailable")
	}
}

func assertStoreErrorOmitsSyntheticSecrets(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	msg := err.Error()
	for _, forbidden := range []string{"Bearer ", "ghp_", "sk-test", "redis://"} {
		if strings.Contains(msg, forbidden) {
			t.Fatalf("store error leaked forbidden marker %q: %v", forbidden, err)
		}
	}
}

func newRedisEdgeStore(t *testing.T, opts ...StoreOption) (*RedisStore, *redis.Client, *miniredis.Miniredis, func()) {
	t.Helper()
	mr := miniredis.RunT(t)
	// Keep the miniredis-backed unit-test client single-connection so -race
	// exercises our Redis CAS logic rather than go-redis lazy per-connection
	// option initialization under concurrent first use.
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 1})
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

func appendStoreEventsInChunks(t *testing.T, ctx context.Context, store *RedisStore, tenantID, sessionID, executionID string, count int, started time.Time) {
	t.Helper()
	const chunkSize = 250
	for offset := 0; offset < count; offset += chunkSize {
		end := offset + chunkSize
		if end > count {
			end = count
		}
		events := make([]AgentActionEvent, 0, end-offset)
		for i := offset; i < end; i++ {
			eventID := fmt.Sprintf("%s-event-%05d", executionID, i)
			at := started.Add(time.Duration(i+1) * time.Millisecond)
			events = append(events, validStoreEvent(tenantID, sessionID, executionID, eventID, 0, at, EventKindHookPostToolUse, DecisionAllow))
		}
		if _, err := store.AppendEvents(ctx, events); err != nil {
			t.Fatalf("AppendEvents chunk %d..%d: %v", offset, end, err)
		}
	}
}

func assertRedisKeysGone(t *testing.T, ctx context.Context, client redis.UniversalClient, keys ...string) {
	t.Helper()
	for _, key := range keys {
		exists, err := client.Exists(ctx, key).Result()
		if err != nil {
			t.Fatalf("Exists(%s): %v", key, err)
		}
		if exists != 0 {
			t.Fatalf("key %s exists after cleanup; want 0", key)
		}
	}
}

func assertSortedSetMemberAbsent(t *testing.T, ctx context.Context, client redis.UniversalClient, key, member string) {
	t.Helper()
	_, err := client.ZScore(ctx, key, member).Result()
	if errors.Is(err, redis.Nil) {
		return
	}
	if err != nil {
		t.Fatalf("ZScore(%s,%s): %v", key, member, err)
	}
	t.Fatalf("member %s still present in sorted set %s", member, key)
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

func storePagingID(prefix string, i int) string {
	return prefix + "-" + strconv.Itoa(i)
}

func assertOpaqueStoreCursor(t *testing.T, cursor string) {
	t.Helper()
	if strings.TrimSpace(cursor) == "" {
		t.Fatalf("NextCursor empty, want opaque continuation")
	}
	if _, err := strconv.Atoi(cursor); err == nil {
		t.Fatalf("NextCursor %q is a bare integer offset, want opaque cursor", cursor)
	}
}

func assertScoreIDStoreCursor(t *testing.T, raw, kind string, score float64, id string) {
	t.Helper()
	assertOpaqueStoreCursor(t, raw)
	cursor := decodeStoreCursorForTest(t, raw)
	if cursor.Version != storeCursorVersion || cursor.Kind != kind || cursor.Score != score || cursor.ID != id {
		t.Fatalf("cursor = %#v, want version=%d kind=%q score=%v id=%q", cursor, storeCursorVersion, kind, score, id)
	}
}

func assertSessionIndexEventCursor(t *testing.T, raw string, at time.Time, member string) {
	t.Helper()
	cursor := decodeStoreCursorForTest(t, raw)
	score := float64(at.UTC().UnixMicro())
	if cursor.Version != storeCursorVersion || cursor.Kind != "events" || cursor.Scope != "session_index" || cursor.Score != score || cursor.ID != member {
		t.Fatalf("session index event cursor = %#v, want score=%v id=%q", cursor, score, member)
	}
}

func decodeStoreCursorForTest(t *testing.T, raw string) storeCursor {
	t.Helper()
	assertOpaqueStoreCursor(t, raw)
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		t.Fatalf("decode cursor %q: %v", raw, err)
	}
	var cursor storeCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		t.Fatalf("unmarshal cursor %q: %v", raw, err)
	}
	return cursor
}

// EDGE-037 tests: per-session execution cap + bounded DeleteSession cleanup.

func TestRedisStoreCountSessionExecutionsEmptySession(t *testing.T) {
	ctx := context.Background()
	store, _, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	if err := store.CreateSession(ctx, validStoreSession("tenant-a", "sess-empty", "principal-a", time.Now().UTC())); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	count, err := store.CountSessionExecutions(ctx, "tenant-a", "sess-empty")
	if err != nil {
		t.Fatalf("CountSessionExecutions empty: %v", err)
	}
	if count != 0 {
		t.Fatalf("count empty session = %d, want 0", count)
	}
}

func TestRedisStoreCountSessionExecutionsRequiresInputs(t *testing.T) {
	ctx := context.Background()
	store, _, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	for _, tc := range []struct{ tenant, session string }{
		{"", "sess-x"},
		{"tenant-a", ""},
		{"   ", "sess-x"},
		{"tenant-a", "   "},
	} {
		_, err := store.CountSessionExecutions(ctx, tc.tenant, tc.session)
		if err == nil {
			t.Fatalf("CountSessionExecutions(%q,%q) succeeded; want validation error", tc.tenant, tc.session)
		}
		if !errors.Is(err, ErrValidation) {
			t.Fatalf("CountSessionExecutions(%q,%q) error = %v, want errors.Is(ErrValidation)", tc.tenant, tc.session, err)
		}
	}
}

func TestRedisStoreCountSessionExecutionsAfterMultipleCreates(t *testing.T) {
	ctx := context.Background()
	store, _, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	base := time.Now().UTC()
	if err := store.CreateSession(ctx, validStoreSession("tenant-a", "sess-multi", "principal-a", base)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	for i := 0; i < 7; i++ {
		execution := validStoreExecution("tenant-a", "sess-multi", fmt.Sprintf("exec-multi-%d", i), base.Add(time.Duration(i+1)*time.Second), nil)
		if err := store.CreateExecution(ctx, execution); err != nil {
			t.Fatalf("CreateExecution %d: %v", i, err)
		}
	}
	count, err := store.CountSessionExecutions(ctx, "tenant-a", "sess-multi")
	if err != nil {
		t.Fatalf("CountSessionExecutions: %v", err)
	}
	if count != 7 {
		t.Fatalf("count after 7 creates = %d, want 7", count)
	}
}

func TestRedisStoreCountSessionExecutionsTenantSessionIsolation(t *testing.T) {
	ctx := context.Background()
	store, _, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	base := time.Now().UTC()
	// Two different sessions for the same tenant
	if err := store.CreateSession(ctx, validStoreSession("tenant-a", "sess-iso-1", "principal-a", base)); err != nil {
		t.Fatalf("CreateSession iso-1: %v", err)
	}
	if err := store.CreateSession(ctx, validStoreSession("tenant-a", "sess-iso-2", "principal-a", base)); err != nil {
		t.Fatalf("CreateSession iso-2: %v", err)
	}
	// Different tenant, different session
	if err := store.CreateSession(ctx, validStoreSession("tenant-b", "sess-iso-3", "principal-b", base)); err != nil {
		t.Fatalf("CreateSession iso-3: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := store.CreateExecution(ctx, validStoreExecution("tenant-a", "sess-iso-1", fmt.Sprintf("exec-iso-1-%d", i), base.Add(time.Second), nil)); err != nil {
			t.Fatalf("CreateExecution iso-1-%d: %v", i, err)
		}
	}
	for i := 0; i < 5; i++ {
		if err := store.CreateExecution(ctx, validStoreExecution("tenant-a", "sess-iso-2", fmt.Sprintf("exec-iso-2-%d", i), base.Add(time.Second), nil)); err != nil {
			t.Fatalf("CreateExecution iso-2-%d: %v", i, err)
		}
	}
	for i := 0; i < 11; i++ {
		if err := store.CreateExecution(ctx, validStoreExecution("tenant-b", "sess-iso-3", fmt.Sprintf("exec-iso-3-%d", i), base.Add(time.Second), nil)); err != nil {
			t.Fatalf("CreateExecution iso-3-%d: %v", i, err)
		}
	}
	cases := []struct {
		tenant, session string
		want            int64
	}{
		{"tenant-a", "sess-iso-1", 3},
		{"tenant-a", "sess-iso-2", 5},
		{"tenant-b", "sess-iso-3", 11},
	}
	for _, tc := range cases {
		got, err := store.CountSessionExecutions(ctx, tc.tenant, tc.session)
		if err != nil {
			t.Fatalf("CountSessionExecutions(%s/%s): %v", tc.tenant, tc.session, err)
		}
		if got != tc.want {
			t.Fatalf("CountSessionExecutions(%s/%s) = %d, want %d", tc.tenant, tc.session, got, tc.want)
		}
	}
}

func TestRedisStoreCreateExecutionEnforcesSessionCap(t *testing.T) {
	ctx := context.Background()
	store, _, _, cleanup := newRedisEdgeStore(t, WithMaxExecutionsPerSession(2))
	defer cleanup()

	base := time.Now().UTC()
	if err := store.CreateSession(ctx, validStoreSession("tenant-a", "sess-exec-cap", "principal-a", base)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	for i := 0; i < 2; i++ {
		execution := validStoreExecution("tenant-a", "sess-exec-cap", fmt.Sprintf("exec-cap-%d", i), base.Add(time.Duration(i+1)*time.Second), nil)
		if err := store.CreateExecution(ctx, execution); err != nil {
			t.Fatalf("CreateExecution under cap %d: %v", i, err)
		}
	}
	err := store.CreateExecution(ctx, validStoreExecution("tenant-a", "sess-exec-cap", "exec-cap-over", base.Add(3*time.Second), nil))
	if !errors.Is(err, ErrSessionExecutionFanoutExceeded) {
		t.Fatalf("CreateExecution over cap error = %v, want ErrSessionExecutionFanoutExceeded", err)
	}
	count, err := store.CountSessionExecutions(ctx, "tenant-a", "sess-exec-cap")
	if err != nil {
		t.Fatalf("CountSessionExecutions: %v", err)
	}
	if count != 2 {
		t.Fatalf("execution count after rejected create = %d, want 2", count)
	}
}

func TestRedisStoreAppendEventsRejectsAfterDefaultExecutionEventCap(t *testing.T) {
	ctx := context.Background()
	rec := &abortRecorder{}
	store, client, _, cleanup := newRedisEdgeStore(t, WithRecorder(rec))
	defer cleanup()

	base := time.Now().UTC()
	createSessionAndExecution(t, ctx, store, "tenant-a", "sess-event-cap", "exec-event-cap", base)
	appendStoreEventsInChunks(t, ctx, store, "tenant-a", "sess-event-cap", "exec-event-cap", DefaultMaxEventsPerExecution, base)

	_, err := store.AppendEvents(ctx, []AgentActionEvent{
		validStoreEvent("tenant-a", "sess-event-cap", "exec-event-cap", "event-cap-over", 0, base.Add(time.Hour), EventKindHookPostToolUse, DecisionAllow),
	})
	if !errors.Is(err, ErrExecutionEventCapExceeded) {
		t.Fatalf("AppendEvents over cap error = %v, want ErrExecutionEventCapExceeded", err)
	}
	if got := rec.SnapshotEventCapRejected(); got != 1 {
		t.Fatalf("event cap metric count = %d, want 1", got)
	}
	length, err := client.LLen(ctx, edgeEventsKey("exec-event-cap")).Result()
	if err != nil {
		t.Fatalf("LLen events: %v", err)
	}
	if length != int64(DefaultMaxEventsPerExecution) {
		t.Fatalf("event list length after rejected append = %d, want %d", length, DefaultMaxEventsPerExecution)
	}
}

func TestRedisStoreAppendEventsWithIdempotencyRejectsEventCap(t *testing.T) {
	ctx := context.Background()
	rec := &abortRecorder{}
	store, _, _, cleanup := newRedisEdgeStore(t, WithMaxEventsPerExecution(1), WithRecorder(rec))
	defer cleanup()

	base := time.Now().UTC()
	createSessionAndExecution(t, ctx, store, "tenant-a", "sess-idem-event-cap", "exec-idem-event-cap", base)
	if _, err := store.AppendEvents(ctx, []AgentActionEvent{
		validStoreEvent("tenant-a", "sess-idem-event-cap", "exec-idem-event-cap", "event-idem-cap-1", 0, base.Add(time.Second), EventKindHookPostToolUse, DecisionAllow),
	}); err != nil {
		t.Fatalf("AppendEvents seed: %v", err)
	}
	req := EdgeIdempotencyRequest{
		TenantID:    "tenant-a",
		Endpoint:    "POST /api/v1/edge/events/batch",
		Key:         "idem-event-cap",
		RequestHash: "sha256:idem-event-cap",
	}
	_, err := store.AppendEventsWithIdempotency(ctx, req, []AgentActionEvent{
		validStoreEvent("tenant-a", "sess-idem-event-cap", "exec-idem-event-cap", "event-idem-cap-2", 0, base.Add(2*time.Second), EventKindHookPostToolUse, DecisionAllow),
	}, storeSingleEventReplayResponse)
	if !errors.Is(err, ErrExecutionEventCapExceeded) {
		t.Fatalf("AppendEventsWithIdempotency over cap error = %v, want ErrExecutionEventCapExceeded", err)
	}
	if got := rec.SnapshotEventCapRejected(); got != 1 {
		t.Fatalf("event cap metric count = %d, want 1", got)
	}
}

func TestRedisStoreDeleteSessionCleansHundredExecutionsWithHundredEventsEach(t *testing.T) {
	ctx := context.Background()
	rec := &abortRecorder{}
	store, client, _, cleanup := newRedisEdgeStore(t, WithRecorder(rec))
	defer cleanup()

	const tenantID = "tenant-cleanup-100"
	const sessionID = "sess-cleanup-100"
	const executionCount = DefaultMaxExecutionsPerSession
	const eventsPerExecution = 100
	base := time.Now().UTC()
	if err := store.CreateSession(ctx, validStoreSession(tenantID, sessionID, "principal-cleanup", base)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	executionIDs := make([]string, 0, executionCount)
	for i := 0; i < executionCount; i++ {
		executionID := fmt.Sprintf("exec-cleanup-%03d", i)
		execution := validStoreExecution(tenantID, sessionID, executionID, base.Add(time.Duration(i+1)*time.Millisecond), func(e *AgentExecution) {
			e.JobID = "job-" + executionID
			e.TraceID = "trace-" + executionID
			e.WorkflowRunID = "run-" + executionID
		})
		if err := store.CreateExecution(ctx, execution); err != nil {
			t.Fatalf("CreateExecution %d: %v", i, err)
		}
		appendStoreEventsInChunks(t, ctx, store, tenantID, sessionID, executionID, eventsPerExecution, base.Add(time.Duration(i)*time.Second))
		executionIDs = append(executionIDs, executionID)
	}

	started := time.Now()
	if err := store.DeleteSession(ctx, tenantID, sessionID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= sessionCleanupDeadline {
		t.Fatalf("DeleteSession elapsed %s, want under %s", elapsed, sessionCleanupDeadline)
	}
	durationCount, keysDeleted, deadlines := rec.SnapshotCleanupMetrics()
	if durationCount != 1 || keysDeleted == 0 || deadlines != 0 {
		t.Fatalf("cleanup metrics durations=%d keys_deleted=%d deadlines=%d, want one duration, >0 keys, zero deadlines", durationCount, keysDeleted, deadlines)
	}
	assertRedisKeysGone(t, ctx, client, edgeSessionKey(sessionID), edgeSessionHeartbeatKey(sessionID), edgeSessionExecutionsIndexKey(sessionID), edgeSessionEventsIndexKey(sessionID))
	assertSortedSetMemberAbsent(t, ctx, client, edgeTenantIndexKey(tenantID), sessionID)
	assertSortedSetMemberAbsent(t, ctx, client, edgePrincipalIndexKey(tenantID, "principal-cleanup"), sessionID)
	for _, executionID := range executionIDs {
		assertRedisKeysGone(t, ctx, client,
			edgeExecutionKey(executionID),
			edgeEventsKey(executionID),
			edgeEventSeqKey(executionID),
			edgeEventIDIndexKey(executionID),
		)
		assertSortedSetMemberAbsent(t, ctx, client, edgeJobIndexKey("job-"+executionID), executionID)
		assertSortedSetMemberAbsent(t, ctx, client, edgeExecutionTraceIndexKey("trace-"+executionID), executionID)
		assertSortedSetMemberAbsent(t, ctx, client, edgeExecutionRunIndexKey("run-"+executionID), executionID)
	}
}

func TestRedisStoreDeleteSessionPagedCleanupOverMaxStorePageLimit(t *testing.T) {
	ctx := context.Background()
	const tenantID = "tenant-paged"
	const sessionID = "sess-paged-cleanup"
	// One session, 1.25 pages worth of executions, to force >2 ZRange calls.
	executionCount := maxStorePageLimit + maxStorePageLimit/4
	store, client, _, cleanup := newRedisEdgeStore(t, WithMaxExecutionsPerSession(executionCount+1))
	defer cleanup()

	base := time.Now().UTC()
	if err := store.CreateSession(ctx, validStoreSession(tenantID, sessionID, "principal-paged", base)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	executionIDs := make([]string, 0, executionCount)
	for i := 0; i < executionCount; i++ {
		executionID := fmt.Sprintf("exec-paged-%04d", i)
		execution := validStoreExecution(tenantID, sessionID, executionID, base.Add(time.Duration(i+1)*time.Millisecond), func(e *AgentExecution) {
			e.JobID = "job-" + executionID
			e.TraceID = "trace-paged-" + executionID
			e.WorkflowRunID = "run-paged-" + executionID
		})
		if err := store.CreateExecution(ctx, execution); err != nil {
			t.Fatalf("CreateExecution %d: %v", i, err)
		}
		executionIDs = append(executionIDs, executionID)
	}

	// Pre-condition: count matches
	if got, err := store.CountSessionExecutions(ctx, tenantID, sessionID); err != nil || got != int64(executionCount) {
		t.Fatalf("pre-delete count = %d (err=%v), want %d", got, err, executionCount)
	}

	if err := store.DeleteSession(ctx, tenantID, sessionID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	// Post-condition: all session-level keys gone
	for _, key := range []string{
		edgeSessionKey(sessionID),
		edgeSessionHeartbeatKey(sessionID),
		edgeSessionExecutionsIndexKey(sessionID),
		edgeSessionEventsIndexKey(sessionID),
	} {
		exists, err := client.Exists(ctx, key).Result()
		if err != nil {
			t.Fatalf("Exists(%s): %v", key, err)
		}
		if exists != 0 {
			t.Fatalf("session-level key %s exists after DeleteSession; want 0", key)
		}
	}

	// Post-condition: all execution-level keys + secondary indexes gone
	for _, executionID := range executionIDs {
		for _, key := range []string{
			edgeExecutionKey(executionID),
			edgeEventsKey(executionID),
			edgeEventSeqKey(executionID),
			edgeEventIDIndexKey(executionID),
			edgeJobIndexKey("job-" + executionID),
			edgeExecutionTraceIndexKey("trace-paged-" + executionID),
			edgeExecutionRunIndexKey("run-paged-" + executionID),
		} {
			exists, err := client.Exists(ctx, key).Result()
			if err != nil {
				t.Fatalf("Exists(%s): %v", key, err)
			}
			if exists != 0 {
				t.Fatalf("execution-scoped key %s exists after DeleteSession; want 0", key)
			}
		}
	}
}

func TestRedisStoreDeleteSessionPageBoundaryEqualsLimit(t *testing.T) {
	ctx := context.Background()

	const tenantID = "tenant-boundary"
	const sessionID = "sess-boundary"
	// Exactly maxStorePageLimit executions — exercises the
	// "len(executionIDs) < maxStorePageLimit" termination on the SECOND
	// iteration after the first page returns exactly the limit.
	store, client, _, cleanup := newRedisEdgeStore(t, WithMaxExecutionsPerSession(maxStorePageLimit+1))
	defer cleanup()

	base := time.Now().UTC()
	if err := store.CreateSession(ctx, validStoreSession(tenantID, sessionID, "principal-boundary", base)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	for i := 0; i < maxStorePageLimit; i++ {
		executionID := fmt.Sprintf("exec-bndy-%04d", i)
		execution := validStoreExecution(tenantID, sessionID, executionID, base.Add(time.Duration(i+1)*time.Millisecond), nil)
		if err := store.CreateExecution(ctx, execution); err != nil {
			t.Fatalf("CreateExecution %d: %v", i, err)
		}
	}

	if err := store.DeleteSession(ctx, tenantID, sessionID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	exists, err := client.Exists(ctx, edgeSessionExecutionsIndexKey(sessionID)).Result()
	if err != nil {
		t.Fatalf("Exists session executions index: %v", err)
	}
	if exists != 0 {
		t.Fatalf("session executions index exists after DeleteSession; want 0")
	}
}

func TestRedisStoreDeleteSessionIdempotentOnMissingSession(t *testing.T) {
	ctx := context.Background()
	store, _, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	// Calling DeleteSession on a never-created session is a no-op (no error).
	if err := store.DeleteSession(ctx, "tenant-z", "sess-never-existed"); err != nil {
		t.Fatalf("DeleteSession on missing session = %v, want nil (idempotent)", err)
	}
}
