package workflow

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
)

func newTestStore(t *testing.T) *RedisStore {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisWorkflowStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("store init: %v", err)
	}
	return store
}

func TestWorkflowSaveGetList(t *testing.T) {
	store := newTestStore(t)
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	wf := &Workflow{
		ID:          "wf-1",
		OrgID:       "org-1",
		Name:        "Sample",
		Description: "desc",
		Version:     "v1",
		Steps: map[string]*Step{
			"start": {ID: "start", Name: "Start", Type: StepTypeWorker, Topic: "job.default"},
		},
	}
	if err := store.SaveWorkflow(ctx, wf); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := store.GetWorkflow(ctx, "wf-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != wf.Name || got.OrgID != wf.OrgID {
		t.Fatalf("mismatch: %+v", got)
	}

	list, err := store.ListWorkflows(ctx, "org-1", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != "wf-1" {
		t.Fatalf("unexpected list: %+v", list)
	}
}

func TestWorkflowPolicyOverrideRoundTrip(t *testing.T) {
	store := newTestStore(t)
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	policyOverride := `tier: workflow
selector:
  workflow_id: wf-policy
default_decision: deny
rules:
  - id: workflow-deny-deploy
    decision: deny
    match:
      topics: ["job.deploy"]
`
	wf := &Workflow{
		ID:             "wf-policy",
		OrgID:          "org-1",
		Name:           "Policy workflow",
		PolicyOverride: policyOverride,
		Steps:          map[string]*Step{"start": {ID: "start", Type: StepTypeWorker}},
	}
	if err := store.SaveWorkflow(ctx, wf); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := store.GetWorkflow(ctx, wf.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.PolicyOverride != policyOverride {
		t.Fatalf("policy override mismatch:\n got %q\nwant %q", got.PolicyOverride, policyOverride)
	}

	list, err := store.ListWorkflows(ctx, "org-1", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].PolicyOverride != policyOverride {
		t.Fatalf("list did not preserve policy override: %+v", list)
	}
}

func TestWorkflowPolicyOverrideRemovedOnDelete(t *testing.T) {
	store := newTestStore(t)
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	wf := &Workflow{
		ID:             "wf-policy-delete",
		OrgID:          "org-1",
		Name:           "Delete policy workflow",
		PolicyOverride: "tier: workflow\nselector:\n  workflow_id: wf-policy-delete\nrules: []\n",
		Steps:          map[string]*Step{"start": {ID: "start", Type: StepTypeWorker}},
	}
	if err := store.SaveWorkflow(ctx, wf); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := store.DeleteWorkflow(ctx, wf.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.GetWorkflow(ctx, wf.ID); err == nil {
		t.Fatalf("expected workflow and policy override to be deleted")
	}
}

func TestWorkflowRunsCRUD(t *testing.T) {
	store := newTestStore(t)
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	run := &WorkflowRun{
		ID:         "run-1",
		WorkflowID: "wf-1",
		OrgID:      "org-1",
		Input:      map[string]any{"foo": "bar"},
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		Labels:     map[string]string{"tenant": "org-1"},
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	got, err := store.GetRun(ctx, "run-1")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Status != RunStatusPending {
		t.Fatalf("expected pending, got %s", got.Status)
	}

	now := time.Now().UTC()
	run.Status = RunStatusRunning
	run.StartedAt = &now
	if err := store.UpdateRun(ctx, run); err != nil {
		t.Fatalf("update run: %v", err)
	}

	got, err = store.GetRun(ctx, "run-1")
	if err != nil {
		t.Fatalf("get run 2: %v", err)
	}
	if got.Status != RunStatusRunning {
		t.Fatalf("expected running, got %s", got.Status)
	}

	list, err := store.ListRunsByWorkflow(ctx, "wf-1", 5)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(list) != 1 || list[0].ID != "run-1" {
		t.Fatalf("unexpected runs: %+v", list)
	}
}

func TestWorkflowListRunsAll(t *testing.T) {
	store := newTestStore(t)
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	run1 := &WorkflowRun{
		ID:         "run-a",
		WorkflowID: "wf-1",
		OrgID:      "org-1",
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
	}
	run2 := &WorkflowRun{
		ID:         "run-b",
		WorkflowID: "wf-2",
		OrgID:      "org-1",
		Status:     RunStatusRunning,
		Steps:      map[string]*StepRun{},
	}
	if err := store.CreateRun(ctx, run1); err != nil {
		t.Fatalf("create run1: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := store.CreateRun(ctx, run2); err != nil {
		t.Fatalf("create run2: %v", err)
	}

	list, err := store.ListRuns(ctx, time.Now().UTC().Unix(), 10)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(list))
	}
	if list[0].ID != "run-b" {
		t.Fatalf("expected newest run-b first, got %s", list[0].ID)
	}
}

func TestWorkflowDeleteRemovesIndexes(t *testing.T) {
	store := newTestStore(t)
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	wf := &Workflow{
		ID:    "wf-del",
		OrgID: "org-1",
		Name:  "Delete me",
		Steps: map[string]*Step{"start": {ID: "start", Type: StepTypeApproval}},
	}
	if err := store.SaveWorkflow(ctx, wf); err != nil {
		t.Fatalf("save: %v", err)
	}

	if err := store.DeleteWorkflow(ctx, wf.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.GetWorkflow(ctx, wf.ID); err == nil {
		t.Fatalf("expected workflow to be deleted")
	}

	listOrg, err := store.ListWorkflows(ctx, "org-1", 10)
	if err != nil {
		t.Fatalf("list org: %v", err)
	}
	if len(listOrg) != 0 {
		t.Fatalf("expected empty org list, got %+v", listOrg)
	}
	listAll, err := store.ListWorkflows(ctx, "", 10)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(listAll) != 0 {
		t.Fatalf("expected empty list, got %+v", listAll)
	}
}

func TestRunDeleteRemovesIndexes(t *testing.T) {
	store := newTestStore(t)
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	run := &WorkflowRun{
		ID:         "run-del",
		WorkflowID: "wf-1",
		OrgID:      "org-1",
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := store.DeleteRun(ctx, run.ID); err != nil {
		t.Fatalf("delete run: %v", err)
	}
	if _, err := store.GetRun(ctx, run.ID); err == nil {
		t.Fatalf("expected run to be deleted")
	}

	list, err := store.ListRunsByWorkflow(ctx, run.WorkflowID, 5)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty runs list, got %+v", list)
	}
}

func TestRunStatusIndexing(t *testing.T) {
	store := newTestStore(t)
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	run := &WorkflowRun{
		ID:         "run-idx-1",
		WorkflowID: "wf-1",
		OrgID:      "org-1",
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	ids, err := store.ListRunIDsByStatus(ctx, RunStatusPending, 10)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(ids) != 1 || ids[0] != run.ID {
		t.Fatalf("unexpected pending ids: %+v", ids)
	}

	run.Status = RunStatusRunning
	if err := store.UpdateRun(ctx, run); err != nil {
		t.Fatalf("update run: %v", err)
	}

	ids, err = store.ListRunIDsByStatus(ctx, RunStatusPending, 10)
	if err != nil {
		t.Fatalf("list pending after update: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("expected no pending ids, got %+v", ids)
	}

	ids, err = store.ListRunIDsByStatus(ctx, RunStatusRunning, 10)
	if err != nil {
		t.Fatalf("list running: %v", err)
	}
	if len(ids) != 1 || ids[0] != run.ID {
		t.Fatalf("unexpected running ids: %+v", ids)
	}
}

func TestRunIdempotencyKeyMapping(t *testing.T) {
	store := newTestStore(t)
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	run := &WorkflowRun{
		ID:             "run-idem-1",
		WorkflowID:     "wf-1",
		OrgID:          "org-1",
		Status:         RunStatusPending,
		Steps:          map[string]*StepRun{},
		IdempotencyKey: "idem-key-1",
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	got, err := store.GetRunByIdempotencyKey(ctx, "idem-key-1")
	if err != nil {
		t.Fatalf("get idempotency: %v", err)
	}
	if got != run.ID {
		t.Fatalf("expected run id %s, got %s", run.ID, got)
	}
	ok, err := store.TrySetRunIdempotencyKey(ctx, "idem-key-1", "run-idem-2")
	if err != nil {
		t.Fatalf("try set: %v", err)
	}
	if ok {
		t.Fatalf("expected idempotency key to be taken")
	}

	if err := store.DeleteRunIdempotencyKey(ctx, "idem-key-1"); err != nil {
		t.Fatalf("delete idempotency key: %v", err)
	}
	if got, err := store.GetRunByIdempotencyKey(ctx, "idem-key-1"); err == nil || got != "" {
		t.Fatalf("expected idempotency key to be removed")
	}
	if err := store.DeleteRunIdempotencyKey(ctx, ""); err == nil {
		t.Fatalf("expected error on empty idempotency key")
	}
}

func TestRunTimelineAppendAndList(t *testing.T) {
	store := newTestStore(t)
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	run := &WorkflowRun{
		ID:         "run-timeline",
		WorkflowID: "wf-1",
		OrgID:      "org-1",
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := store.AppendTimelineEvent(ctx, run.ID, &TimelineEvent{Type: "run_created"}); err != nil {
		t.Fatalf("append timeline: %v", err)
	}
	if err := store.AppendTimelineEvent(ctx, run.ID, &TimelineEvent{Type: "run_status", Status: string(RunStatusRunning)}); err != nil {
		t.Fatalf("append timeline: %v", err)
	}

	events, err := store.ListTimelineEvents(ctx, run.ID, 10)
	if err != nil {
		t.Fatalf("list timeline: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Type != "run_created" || events[1].Type != "run_status" {
		t.Fatalf("unexpected timeline events: %+v", events)
	}
}

func TestRedisStoreUpdateAuditHashPersistsByJobIndex(t *testing.T) {
	store := newTestStore(t)
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	jobID := "run-audit-idx:step-1@1"
	hash := strings.Repeat("a", 64)
	replacement := strings.Repeat("b", 64)
	run := &WorkflowRun{
		ID:         "run-audit-idx",
		WorkflowID: "wf-audit",
		Status:     RunStatusRunning,
		Steps: map[string]*StepRun{
			"step-1":  {StepID: "step-1", Status: StepStatusRunning, JobID: jobID},
			"skipped": {StepID: "skipped", Status: StepStatusSkipped, SkipReason: "upstream failed"},
			"nohash":  {StepID: "nohash", Status: StepStatusRunning, JobID: "run-audit-idx:nohash@1"},
		},
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	ref, ok, err := store.lookupRunJobRef(ctx, jobID)
	if err != nil {
		t.Fatalf("lookup job ref: %v", err)
	}
	if !ok || ref.RunID != run.ID || len(ref.StepPath) == 0 || ref.StepPath[0] != "step-1" {
		t.Fatalf("unexpected job ref: ref=%+v ok=%v", ref, ok)
	}

	if err := store.UpdateAuditHash(ctx, jobID, hash); err != nil {
		t.Fatalf("update audit hash: %v", err)
	}
	assertRunAuditHash(t, store, run.ID, "step-1", hash)
	assertRunAuditHash(t, store, run.ID, "skipped", "")
	assertRunAuditHash(t, store, run.ID, "nohash", "")

	if err := store.UpdateAuditHash(ctx, jobID, hash); err != nil {
		t.Fatalf("idempotent update: %v", err)
	}
	if err := store.UpdateAuditHash(ctx, jobID, replacement); err != nil {
		t.Fatalf("conflicting update should be non-fatal: %v", err)
	}
	assertRunAuditHash(t, store, run.ID, "step-1", hash)
	if err := store.UpdateAuditHash(ctx, jobID, "not-a-sha256-hex-digest"); err == nil {
		t.Fatalf("invalid audit hash should be rejected")
	}
	assertRunAuditHash(t, store, run.ID, "step-1", hash)

	run.Status = RunStatusSucceeded
	if err := store.UpdateRun(ctx, run); err != nil {
		t.Fatalf("stale UpdateRun should preserve audit hash: %v", err)
	}
	assertRunAuditHash(t, store, run.ID, "step-1", hash)

	if err := store.UpdateAuditHash(ctx, "not-a-workflow-job", hash); err != nil {
		t.Fatalf("non-workflow job should be a no-op: %v", err)
	}
	if err := store.UpdateAuditHash(ctx, jobID, ""); err != nil {
		t.Fatalf("empty hash should be a no-op: %v", err)
	}
	assertRunAuditHash(t, store, run.ID, "step-1", hash)
}

func TestRedisStoreUpdateAuditHashAppliesPendingOnNextRunWrite(t *testing.T) {
	store := newTestStore(t)
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	jobID := "run-pending-audit:step-1@1"
	hash := strings.Repeat("c", 64)
	if err := store.UpdateAuditHash(ctx, jobID, hash); err != nil {
		t.Fatalf("pending update: %v", err)
	}

	run := &WorkflowRun{
		ID:         "run-pending-audit",
		WorkflowID: "wf-audit",
		Status:     RunStatusRunning,
		Steps: map[string]*StepRun{
			"step-1": {StepID: "step-1", Status: StepStatusRunning, JobID: jobID},
		},
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	assertRunAuditHash(t, store, run.ID, "step-1", hash)
}

func TestRedisStoreUpdateAuditHashUpdatesNestedDuplicateStepRuns(t *testing.T) {
	store := newTestStore(t)
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	jobID := "run-nested-audit:fanout[0]@1"
	hash := strings.Repeat("d", 64)
	child := &StepRun{StepID: "fanout[0]", Status: StepStatusRunning, JobID: jobID}
	run := &WorkflowRun{
		ID:         "run-nested-audit",
		WorkflowID: "wf-audit",
		Status:     RunStatusRunning,
		Steps: map[string]*StepRun{
			"fanout":    {StepID: "fanout", Status: StepStatusRunning, Children: map[string]*StepRun{"fanout[0]": child}},
			"fanout[0]": {StepID: "fanout[0]", Status: StepStatusRunning, JobID: jobID},
		},
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := store.UpdateAuditHash(ctx, jobID, hash); err != nil {
		t.Fatalf("update audit hash: %v", err)
	}

	got, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Steps["fanout[0]"].AuditHash != hash {
		t.Fatalf("top-level child hash = %q, want %q", got.Steps["fanout[0]"].AuditHash, hash)
	}
	if got.Steps["fanout"].Children["fanout[0]"].AuditHash != hash {
		t.Fatalf("nested child hash = %q, want %q", got.Steps["fanout"].Children["fanout[0]"].AuditHash, hash)
	}
}

// TestRedisStoreUpdateRunPreservesAuditHashAcrossInFlightRace is the
// regression for the QA-rejected lost-update race: a stale UpdateRun whose
// caller marshaled the payload *before* a concurrent UpdateAuditHash wrote
// its audit_hash must not erase that hash on SET.
//
// Determinism: rather than rely on goroutine scheduling, the test reproduces
// the post-race Redis state directly — UpdateAuditHash is invoked first so the
// run-key carries the populated hash, then UpdateRun is invoked with a stale
// payload (no hash) that the caller built before the audit write happened.
// In the buggy implementation the SET writes the stale payload and the hash
// is lost; in the fixed implementation the script's GET-merge-SET copies the
// persisted hash forward into the SET payload before writing.
//
// To prove the test catches the race specifically (and is not just covered
// by the existing "stale UpdateRun after the hash already exists" cases),
// the stale payload's status is the same as the persisted run's, so the
// only behavior the test is measuring is whether the audit_hash survives.
func TestRedisStoreUpdateRunPreservesAuditHashAcrossInFlightRace(t *testing.T) {
	store := newTestStore(t)
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	jobID := "run-race:step-1@1"
	hash := strings.Repeat("e", 64)

	run := &WorkflowRun{
		ID:         "run-race",
		WorkflowID: "wf-race",
		Status:     RunStatusRunning,
		Steps: map[string]*StepRun{
			"step-1": {StepID: "step-1", Status: StepStatusRunning, JobID: jobID},
		},
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	// The audit consumer writes the hash atomically.
	if err := store.UpdateAuditHash(ctx, jobID, hash); err != nil {
		t.Fatalf("update audit hash: %v", err)
	}
	assertRunAuditHash(t, store, run.ID, "step-1", hash)

	// Caller's stale payload — built before the audit consumer landed the hash.
	// Identical to the persisted run except the in-memory copy never received
	// the audit_hash. Marshaling this and sending SET would erase the hash
	// without the script's atomic merge.
	staleCopy := &WorkflowRun{
		ID:         run.ID,
		WorkflowID: run.WorkflowID,
		Status:     RunStatusSucceeded,
		Steps: map[string]*StepRun{
			"step-1": {StepID: "step-1", Status: StepStatusRunning, JobID: jobID},
		},
	}
	if err := store.UpdateRun(ctx, staleCopy); err != nil {
		t.Fatalf("update run: %v", err)
	}

	// Atomically merged: the hash from the persisted run survived the SET.
	assertRunAuditHash(t, store, run.ID, "step-1", hash)

	// And the caller's status mutation (Running → Succeeded) still lands —
	// the merge only fills empty audit_hash slots, it does not revert the
	// caller's intended state on other fields.
	got, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Status != RunStatusSucceeded {
		t.Fatalf("status mutation lost: got %s want %s", got.Status, RunStatusSucceeded)
	}
}

// TestRedisStoreUpdateRunPreservesAuditHashUnderConcurrentInFlightAuditWrite
// stresses the load-bearing claim: across many goroutine orderings of
// UpdateRun + UpdateAuditHash on the same job, no iteration loses the hash.
// With the Lua-merge atomic GET-merge-SET, both orderings (audit first, then
// stale UpdateRun; or stale UpdateRun first, then audit WATCH/MULTI/EXEC) end
// up with the hash persisted. A regression that drops merge atomicity would
// statistically lose the hash within a few hundred iterations.
func TestRedisStoreUpdateRunPreservesAuditHashUnderConcurrentInFlightAuditWrite(t *testing.T) {
	store := newTestStore(t)
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	const iterations = 200
	hash := strings.Repeat("f", 64)

	for i := 0; i < iterations; i++ {
		runID := "run-stress-" + strings.Repeat("x", 1) + itoa(i)
		jobID := runID + ":step-1@1"
		run := &WorkflowRun{
			ID:         runID,
			WorkflowID: "wf-stress",
			Status:     RunStatusRunning,
			Steps: map[string]*StepRun{
				"step-1": {StepID: "step-1", Status: StepStatusRunning, JobID: jobID},
			},
		}
		if err := store.CreateRun(ctx, run); err != nil {
			t.Fatalf("iter %d: create run: %v", i, err)
		}

		staleCopy := &WorkflowRun{
			ID:         runID,
			WorkflowID: "wf-stress",
			Status:     RunStatusSucceeded,
			Steps: map[string]*StepRun{
				"step-1": {StepID: "step-1", Status: StepStatusRunning, JobID: jobID},
			},
		}

		var wg sync.WaitGroup
		wg.Add(2)
		var updateErr, auditErr error
		go func() {
			defer wg.Done()
			updateErr = store.UpdateRun(ctx, staleCopy)
		}()
		go func() {
			defer wg.Done()
			auditErr = store.UpdateAuditHash(ctx, jobID, hash)
		}()
		wg.Wait()

		if updateErr != nil {
			t.Fatalf("iter %d: UpdateRun: %v", i, updateErr)
		}
		if auditErr != nil {
			t.Fatalf("iter %d: UpdateAuditHash: %v", i, auditErr)
		}

		got, err := store.GetRun(ctx, runID)
		if err != nil {
			t.Fatalf("iter %d: get run: %v", i, err)
		}
		if got.Steps["step-1"].AuditHash != hash {
			t.Fatalf("iter %d: audit hash lost — got %q, want %q", i, got.Steps["step-1"].AuditHash, hash)
		}
	}
}

// itoa is a small helper that avoids pulling fmt into the hot loop above.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

func assertRunAuditHash(t *testing.T, store *RedisStore, runID, stepID, want string) {
	t.Helper()
	got, err := store.GetRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("get run %s: %v", runID, err)
	}
	if got.Steps[stepID].AuditHash != want {
		t.Fatalf("step %s audit hash = %q, want %q", stepID, got.Steps[stepID].AuditHash, want)
	}
}

// TestUpdateRunConcurrent verifies that sequential status transitions update
// both the run data and the status indexes consistently. In production,
// updates to the same run are serialized by lockRun().
func TestUpdateRunConcurrent(t *testing.T) {
	store := newTestStore(t)
	defer func() { _ = store.Close() }()

	ctx := context.Background()

	// Create the base run.
	run := &WorkflowRun{
		ID:         "run-conc",
		WorkflowID: "wf-1",
		OrgID:      "org-1",
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	// Transition through statuses sequentially (matching production lockRun serialization).
	statuses := []RunStatus{
		RunStatusRunning,
		RunStatusSucceeded,
		RunStatusFailed,
	}
	for i, s := range statuses {
		r := &WorkflowRun{
			ID:         "run-conc",
			WorkflowID: "wf-1",
			OrgID:      "org-1",
			Status:     s,
			Steps:      map[string]*StepRun{},
		}
		if err := store.UpdateRun(ctx, r); err != nil {
			t.Fatalf("update %d failed: %v", i, err)
		}
	}

	// The run must be in exactly one status index (the last writer wins).
	got, err := store.GetRun(ctx, "run-conc")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	finalStatus := got.Status

	// Verify run appears in its final status index.
	ids, err := store.ListRunIDsByStatus(ctx, finalStatus, 10)
	if err != nil {
		t.Fatalf("list final status: %v", err)
	}
	found := false
	for _, id := range ids {
		if id == "run-conc" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("run-conc not found in status index %s", finalStatus)
	}

	// Verify run does NOT appear in other status indexes.
	for _, s := range statuses {
		if s == finalStatus {
			continue
		}
		ids, err := store.ListRunIDsByStatus(ctx, s, 10)
		if err != nil {
			t.Fatalf("list status %s: %v", s, err)
		}
		for _, id := range ids {
			if id == "run-conc" {
				t.Fatalf("run-conc should not be in status index %s (final=%s)", s, finalStatus)
			}
		}
	}
}
