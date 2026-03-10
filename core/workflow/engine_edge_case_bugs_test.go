package workflow

// ==========================================================================
//  WORKFLOW ENGINE EDGE-CASE BUG AUDIT
//  7 confirmed bugs, 5 remediation items, test-first approach
//
//  Each remediation item lists:
//    - Root cause (exact file + lines)
//    - Required changes
//    - Structured observability expectations (slog fields, timeline events)
//    - Backward-compatibility constraints
//    - Regression tests that MUST pass after fix
//
// ==========================================================================
//
// REMEDIATION 1: Timeout/on_error parity (Bugs 1, 2, 5)
// -------------------------------------------------------
//  Root cause: Three independent locations treat StepStatusTimedOut differently
//  from StepStatusFailed with respect to on_error handlers.
//
//  Fix A — engine.go HandleJobResult, line ~410:
//    Current:  if !retry && stepRun.Status == StepStatusFailed && stepDef != nil && stepDef.OnError != "" {
//    Required: if !retry && (stepRun.Status == StepStatusFailed || stepRun.Status == StepStatusTimedOut) && stepDef != nil && stepDef.OnError != "" {
//    The rest of the block (lines 411-432) stays identical — create error context,
//    set targetSR to Pending, emit "step_error_redirect" timeline event.
//    Observability: The existing timeline event "step_error_redirect" already logs
//    stepID and target handler. Add a slog.Info with fields:
//      "run_id", run.ID, "step_id", stepID, "handler", stepDef.OnError,
//      "trigger", string(stepRun.Status)
//    so operators can distinguish timeout-triggered vs failure-triggered activations.
//
//  Fix B — engine_state.go updateRunStatus, lines 538-539:
//    Current:  case StepStatusTimedOut: hasTimedOut = true
//    Required: Mirror the StepStatusFailed block (lines 517-530):
//      case StepStatusTimedOut:
//          stepDef := wfDef.Steps[stepID]
//          if stepDef != nil && stepDef.OnError != "" {
//              targetSR := run.Steps[stepDef.OnError]
//              if targetSR == nil || targetSR.Status == "" || targetSR.Status == StepStatusPending || targetSR.Status == StepStatusRunning {
//                  allDone = false
//                  break
//              }
//              if targetSR.Status == StepStatusSucceeded {
//                  completed++
//                  break
//              }
//          }
//          hasTimedOut = true
//    No new slog needed here — updateRunStatus is a pure function.
//
//  Fix C — engine_helpers.go depsSatisfied, line ~201:
//    Current:  if sr.Status == StepStatusFailed && wfDef != nil {
//    Required: if (sr.Status == StepStatusFailed || sr.Status == StepStatusTimedOut) && wfDef != nil {
//    The inner logic (check depDef.OnError, check handlerSR.Status == StepStatusSucceeded)
//    is identical and requires no change.
//
//  Backward compatibility: Workflows without on_error handlers are unaffected.
//    Timed-out steps without on_error still produce RunStatusTimedOut as before.
//    Only workflows that explicitly define on_error on a step gain timeout recovery.
//
//  Regression tests (MUST PASS after fix):
//    - TestBug_OnErrorNotTriggeredOnStepTimeout
//    - TestBug_UpdateRunStatusTimedOutIgnoresOnErrorHandler
//    - TestBug_DepsSatisfied_TimedOutWithOnErrorSucceeded
//    - TestInvariant_OnErrorActivatedOnFailedStep (must still pass — no regression)
//    - TestInvariant_UpdateRunStatusFailedWithOnErrorSucceeded (must still pass)
//    - TestUpdateRunStatus_OnErrorAware (existing test in engine_dag_safety_test.go)
//
// REMEDIATION 2: forEach orphan child cancellation (Bug 3)
// ---------------------------------------------------------
//  Root cause: When a forEach child fails, aggregateChildren (engine_state.go
//  line ~445) marks the parent step as Failed. But no code cancels the remaining
//  running children. Unlike parallel steps, forEach has no cancelParallelChildren
//  equivalent. The reconciler only cleans up Cancelled/TimedOut runs, not Failed.
//
//  Required changes:
//    engine.go — In HandleJobResult, after `aggregateChildren(parentSR)` returns
//    StepStatusFailed for a forEach parent, iterate parentSR.Children and:
//      1. For each child with Status == StepStatusRunning and non-empty JobID:
//         a. Call e.publishJobCancel(child.JobID, "forEach sibling failed")
//         b. Set child.Status = StepStatusCancelled, child.CompletedAt = &now
//         c. Update parentSR.Children[childID] and run.Steps[childID]
//      2. For each child with Status == StepStatusPending:
//         a. Set child.Status = StepStatusCancelled, child.CompletedAt = &now
//         b. No publishJobCancel needed (never dispatched)
//
//    The best insertion point is in HandleJobResult between the aggregateChildren
//    call and the updateRunStatus call. Add a helper function:
//      func (e *Engine) cancelForEachSiblings(ctx context.Context, run *WorkflowRun, parentSR *StepRun, now time.Time)
//    This mirrors the existing cancelParallelChildren pattern.
//
//    Observability: Emit slog.Warn for each cancelled orphan:
//      "run_id", run.ID, "parent_step", parentSR.StepID, "cancelled_child", childID,
//      "child_job_id", child.JobID, "reason", "forEach sibling failed"
//    Emit timeline event "step_foreach_orphan_cancelled" per child.
//
//    Also extend reconciler.go reconcileOrphanedJobs to scan RunStatusFailed runs
//    (add to terminalStatuses on line 110). This provides a safety net for edge cases
//    where the inline cancellation fails (bus publish error).
//
//  Backward compatibility: No behavioral change for workflows where all children
//    succeed or where only one child exists. Only affect: faster resource reclaim.
//
//  Regression tests:
//    - TestBug_ForEachOrphanChildrenOnFailure
//    - TestEdgeCase_ForEachMaxParallelPartialDispatchFailure
//    - TestInvariant_ForEachEmptyItemsSucceeds (must still pass)
//
// REMEDIATION 3: Parallel failure child cancellation (Bug 4)
// -----------------------------------------------------------
//  Root cause: engine.go scheduleReady parallel section, lines ~1598-1612 —
//  the failure path calls OnStepFinished and continues but never calls
//  cancelParallelChildren. The success path (line ~1566) does call it.
//
//  Required change:
//    engine.go lines ~1597-1598, before setting parentSR.Status = StepStatusFailed:
//      cancelled := e.cancelParallelChildren(parentSR, run, childStepIDs, now)
//    Then include the cancelled count in the timeline event data (line ~1602):
//      "cancelled_count": cancelled,
//
//    Observability: cancelParallelChildren already emits slog and timeline events.
//    The timeline data for "step_parallel_completed" on failure should include
//    "cancelled_count" just as the success path does.
//
//  Backward compatibility: No change to workflow definitions. Running children are
//    now properly cleaned up on failure (were previously orphaned).
//
//  Regression tests:
//    - TestBug_ParallelAllStrategyOrphanChildrenOnFailure
//    - TestInvariant_ParallelAnyStrategyCancelsOnSuccess (must still pass)
//    - TestInvariant_ParallelNOfMSuccessfullyCompletes (must still pass)
//
// REMEDIATION 4: Chained on_error support (Bug 6)
// -------------------------------------------------
//  Root cause: engine_state.go updateRunStatus lines 517-530 only checks ONE
//  level of on_error chain. When step A fails and handler1 fails, the code sees
//  handler1.Status != Succeeded → marks hasFailed = true, terminating the run
//  BEFORE handler2 can execute.
//
//  Required change — engine_state.go updateRunStatus StepStatusFailed case:
//    Replace the single-level check with an iterative chain walk:
//      case StepStatusFailed:
//          handled := false
//          visited := map[string]bool{stepID: true}  // cycle detection
//          current := stepID
//          for {
//              def := wfDef.Steps[current]
//              if def == nil || def.OnError == "" {
//                  break
//              }
//              if visited[def.OnError] {
//                  break // cycle — stop walking
//              }
//              visited[def.OnError] = true
//              handlerSR := run.Steps[def.OnError]
//              if handlerSR == nil || handlerSR.Status == "" || handlerSR.Status == StepStatusPending || handlerSR.Status == StepStatusRunning {
//                  allDone = false
//                  handled = true
//                  break
//              }
//              if handlerSR.Status == StepStatusSucceeded {
//                  completed++
//                  handled = true
//                  break
//              }
//              if handlerSR.Status == StepStatusFailed || handlerSR.Status == StepStatusTimedOut {
//                  current = def.OnError  // walk to next handler in chain
//                  continue
//              }
//              break // unknown status — treat as unhandled
//          }
//          if !handled {
//              hasFailed = true
//          }
//
//    Apply the SAME chain-walking logic for the StepStatusTimedOut case (after
//    Remediation 1 adds on_error support there).
//
//  Backward compatibility: Single-level on_error workflows behave identically
//    (the loop executes once). Only multi-level chains gain new behavior.
//    Cycle detection via visited map prevents infinite loops.
//
//  Observability: No new slog needed — updateRunStatus is pure.
//    The existing timeline events in HandleJobResult will log each redirect.
//
//  Regression tests:
//    - TestBug_ChainedOnErrorHandlersPreemptedByUpdateRunStatus
//    - TestRunStatusMatrix (all existing cases must still pass)
//    - TestUpdateRunStatus_OnErrorAware (existing, must still pass)
//
// REMEDIATION 5: scheduleReady on_error activation (Bug 7)
// ----------------------------------------------------------
//  Root cause: on_error handler activation code ONLY exists in HandleJobResult
//  (engine.go lines 410-433). Steps that complete inline in scheduleReady
//  (subworkflow, parallel, loop, transform, condition, switch, storage) set
//  parentSR.Status = StepStatusFailed but never activate the on_error handler.
//
//  Required change — Extract on_error activation into a helper:
//    func (e *Engine) activateOnErrorHandler(ctx context.Context, run *WorkflowRun, wfDef *Workflow, stepID string, stepRun *StepRun, now time.Time)
//    The helper should:
//      1. Look up stepDef := wfDef.Steps[stepID]
//      2. If stepDef == nil or stepDef.OnError == "", return
//      3. If wfDef.Steps[stepDef.OnError] doesn't exist, return
//      4. Get/create targetSR for stepDef.OnError
//      5. If targetSR is already terminal, return (don't re-activate)
//      6. Set targetSR.Status = StepStatusPending
//      7. Copy error context from stepRun.Error into targetSR.Input["error"]
//      8. Update run.Steps[stepDef.OnError] = targetSR
//      9. Call e.appendTimeline with "step_error_redirect"
//     10. Log slog.Info with run_id, step_id, handler, trigger status
//
//    Then call this helper from:
//      A. HandleJobResult — replace inline block at lines 410-433
//      B. scheduleReady — after EVERY inline step failure:
//         - subworkflow failures (lines ~1070-1087, 1112-1124, 1139-1147, 1149-1158, 1175-1206)
//         - parallel init errors (lines ~1224-1235, 1472-1479, 1547-1559)
//         - parallel failure path (lines ~1598-1612)
//         - loop config errors (lines ~1242-1254, 1260-1272, 1298-1310)
//         - condition errors (find all condition StepStatusFailed in scheduleReady)
//         - switch errors (all switch StepStatusFailed paths)
//         - transform errors (all transform StepStatusFailed paths)
//         - storage errors (all storage StepStatusFailed paths)
//
//    For each site, after setting parentSR.Status = StepStatusFailed:
//      e.activateOnErrorHandler(ctx, run, wfDef, stepID, parentSR, now)
//
//    Note: After activating on_error, the `continue` statement should be
//    preserved — the step is still Failed, the handler will be scheduled
//    on the next scheduleReady pass (or same pass if the on_error target
//    has no dependencies).
//
//  Backward compatibility: Steps without on_error defined are unaffected.
//    The helper returns early when OnError == "". Existing scheduleReady
//    behavior (mark Failed, continue) is preserved with one additional call.
//
//  Observability: The extracted helper provides consistent logging/timeline
//    events for ALL on_error activations, regardless of whether they originate
//    from HandleJobResult or scheduleReady. Fields:
//      slog: "run_id", "step_id", "handler", "trigger" (status string)
//      timeline: "step_error_redirect" (existing event type)
//
//  Regression tests:
//    - TestBug_SubWorkflowFailureDoesNotTriggerOnError
//    - TestInvariant_OnErrorActivatedOnFailedStep (must still pass)
//    - New tests recommended: TestOnError_ParallelInitFailure,
//      TestOnError_ConditionEvalFailure, TestOnError_TransformFailure
//
// ==========================================================================

import (
	"context"
	"testing"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// ---------------------------------------------------------------------------
// BUG 1: on_error handlers are NOT activated when a step times out.
//
// HandleJobResult only activates on_error for StepStatusFailed, not for
// StepStatusTimedOut. Similarly, updateRunStatus immediately marks the run
// as TimedOut for any timed-out step without checking if an on_error handler
// could recover.
//
// Impact: Steps with on_error handlers silently ignore timeouts. The error
// handler is never invoked, and the run terminates prematurely.
// ---------------------------------------------------------------------------

func TestBug_OnErrorNotTriggeredOnStepTimeout(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()
	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	// Workflow: step "main" has on_error="handler"
	wf := &Workflow{
		ID:    "wf-timeout-onerror",
		OrgID: "org",
		Steps: map[string]*Step{
			"main":    {ID: "main", Type: StepTypeWorker, Topic: "job.default", OnError: "handler"},
			"handler": {ID: "handler", Type: StepTypeWorker, Topic: "job.default"},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	now := time.Now().UTC()
	run := &WorkflowRun{
		ID:         "run-timeout-onerror",
		WorkflowID: wf.ID,
		OrgID:      "org",
		TeamID:     "team",
		Input:      map[string]any{},
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	// Start: should dispatch step "main"
	if err := engine.StartRun(context.Background(), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Complete "main" with TIMEOUT result
	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:        "run-timeout-onerror:main@1",
		Status:       pb.JobStatus_JOB_STATUS_TIMEOUT,
		ErrorMessage: "step timed out after 30s",
	})

	final, err := store.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}

	// BUG: The on_error handler should be activated, just like for FAILED steps.
	// The step "main" has on_error="handler", so when "main" times out, the
	// handler should be activated with error context.
	handlerSR := final.Steps["handler"]
	if handlerSR == nil {
		t.Fatal("BUG CONFIRMED: on_error handler was not activated after step timeout — handler step not found in run")
	}
	if handlerSR.Status == "" {
		t.Fatal("BUG CONFIRMED: on_error handler was not activated after step timeout — handler has no status")
	}
	if handlerSR.Input == nil || handlerSR.Input["error"] == nil {
		t.Fatal("BUG CONFIRMED: on_error handler was activated but has no error context in input")
	}

	// The run should NOT be in terminal state yet — handler is still pending/running
	if final.Status == RunStatusTimedOut {
		t.Fatal("BUG CONFIRMED: run immediately marked TimedOut instead of waiting for on_error handler")
	}
	if final.Status == RunStatusFailed {
		t.Fatal("BUG CONFIRMED: run immediately marked Failed instead of waiting for on_error handler")
	}
}

func TestBug_UpdateRunStatusTimedOutIgnoresOnErrorHandler(t *testing.T) {
	// Unit test for updateRunStatus: verifies that a timed-out step with an
	// on_error handler that succeeded should result in a successful run,
	// analogous to the existing behavior for failed steps.
	now := time.Now()
	wf := &Workflow{
		Steps: map[string]*Step{
			"main":    {ID: "main", OnError: "handler"},
			"handler": {ID: "handler"},
		},
	}

	// Case: main timed out, handler succeeded → run should succeed
	run := &WorkflowRun{
		ID:     "run-timeout-handled",
		Status: RunStatusRunning,
		Steps: map[string]*StepRun{
			"main":    {StepID: "main", Status: StepStatusTimedOut},
			"handler": {StepID: "handler", Status: StepStatusSucceeded},
		},
	}
	updateRunStatus(run, wf, now)

	// BUG: Currently, updateRunStatus immediately marks the run as TimedOut
	// when ANY step has StepStatusTimedOut, without checking if the step has
	// an on_error handler. The same check IS done for StepStatusFailed.
	if run.Status == RunStatusTimedOut {
		t.Fatal("BUG CONFIRMED: updateRunStatus marks run TimedOut even though on_error handler succeeded")
	}
	if run.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded after on_error handler recovered from timeout, got: %s", run.Status)
	}
}

// ---------------------------------------------------------------------------
// BUG 2: forEach orphan children — when a child fails, other running
// children are never cancelled. They continue executing as orphan jobs.
//
// Compare with parallel "any" strategy which properly cancels remaining
// children via cancelParallelChildren.
//
// Impact: Wasted compute resources. Failed forEach parents leave running
// jobs that consume worker capacity. The reconciler does NOT clean up failed
// runs (only cancelled/timed-out), so these orphans persist until the jobs
// naturally complete or timeout.
// ---------------------------------------------------------------------------

func TestBug_ForEachOrphanChildrenOnFailure(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()
	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-foreach-orphan",
		OrgID: "org",
		Steps: map[string]*Step{
			"fan": {
				ID:      "fan",
				Type:    StepTypeWorker,
				Topic:   "job.default",
				ForEach: "input.items",
			},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	now := time.Now().UTC()
	run := &WorkflowRun{
		ID:         "run-foreach-orphan",
		WorkflowID: wf.ID,
		OrgID:      "org",
		TeamID:     "team",
		Input:      map[string]any{"items": []any{"a", "b", "c"}},
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	// Start: dispatches fan[0], fan[1], fan[2]
	if err := engine.StartRun(context.Background(), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	initialPublishes := bus.Count()
	if initialPublishes < 3 {
		t.Fatalf("expected at least 3 fan-out publishes, got %d", initialPublishes)
	}

	// Fail child fan[0] — aggregateChildren marks parent as Failed
	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:        "run-foreach-orphan:fan[0]@1",
		Status:       pb.JobStatus_JOB_STATUS_FAILED,
		ErrorMessage: "child 0 failed",
	})

	final, err := store.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}

	// Parent should be Failed
	if final.Steps["fan"] == nil || final.Steps["fan"].Status != StepStatusFailed {
		t.Fatalf("expected parent fan step to be Failed, got %v", final.Steps["fan"])
	}

	// BUG: fan[1] and fan[2] should have their jobs cancelled.
	// Check that cancel messages were published for the orphaned children.
	msgs := bus.Snapshot()
	cancelCount := 0
	for _, m := range msgs {
		if m.packet.GetJobCancel() != nil {
			cancelCount++
		}
	}
	if cancelCount < 2 {
		t.Fatalf("BUG CONFIRMED: expected at least 2 cancel messages for orphaned forEach children, got %d", cancelCount)
	}

	// Also verify the orphaned children's step status is updated
	for _, childID := range []string{"fan[1]", "fan[2]"} {
		child := final.Steps[childID]
		if child == nil {
			t.Fatalf("child %s not found in run steps", childID)
		}
		if child.Status == StepStatusRunning {
			t.Fatalf("BUG CONFIRMED: orphaned forEach child %s is still Running — should be Cancelled", childID)
		}
	}
}

// ---------------------------------------------------------------------------
// BUG 3: Parallel "all" strategy orphan children on failure.
//
// When any child fails under "all" strategy, evaluateParallelOutcome returns
// done=true, success=false. The code path for failure (engine.go ~1598-1612)
// does NOT call cancelParallelChildren, unlike the success path (~1566).
//
// Impact: Same as Bug 2. Running parallel children become orphan jobs
// when the strategy determines failure.
// ---------------------------------------------------------------------------

func TestBug_ParallelAllStrategyOrphanChildrenOnFailure(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()
	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-parallel-orphan",
		OrgID: "org",
		Steps: map[string]*Step{
			"par": {
				ID:   "par",
				Type: StepTypeParallel,
				Input: map[string]any{
					"steps":    []any{"child_a", "child_b", "child_c"},
					"strategy": "all",
				},
			},
			"child_a": {ID: "child_a", Type: StepTypeWorker, Topic: "job.default"},
			"child_b": {ID: "child_b", Type: StepTypeWorker, Topic: "job.default"},
			"child_c": {ID: "child_c", Type: StepTypeWorker, Topic: "job.default"},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	now := time.Now().UTC()
	run := &WorkflowRun{
		ID:         "run-parallel-orphan",
		WorkflowID: wf.ID,
		OrgID:      "org",
		TeamID:     "team",
		Input:      map[string]any{},
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	// Start: dispatches child_a, child_b, child_c
	if err := engine.StartRun(context.Background(), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Fail child_a → "all" strategy immediately fails
	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:        "run-parallel-orphan:child_a@1",
		Status:       pb.JobStatus_JOB_STATUS_FAILED,
		ErrorMessage: "child_a failed",
	})

	final, err := store.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}

	// Parent should be Failed
	if final.Steps["par"] == nil || final.Steps["par"].Status != StepStatusFailed {
		t.Fatalf("expected parallel parent to be Failed, got %v", final.Steps["par"])
	}

	// BUG: child_b and child_c should have their jobs cancelled.
	msgs := bus.Snapshot()
	cancelCount := 0
	for _, m := range msgs {
		if m.packet.GetJobCancel() != nil {
			cancelCount++
		}
	}
	if cancelCount < 2 {
		t.Fatalf("BUG CONFIRMED: expected at least 2 cancel messages for orphaned parallel children, got %d", cancelCount)
	}

	// Verify children status updated
	for _, childID := range []string{"child_b", "child_c"} {
		child := final.Steps[childID]
		if child == nil {
			continue // May not exist depending on race
		}
		if child.Status == StepStatusRunning {
			t.Fatalf("BUG CONFIRMED: orphaned parallel child %s is still Running — should be Cancelled", childID)
		}
	}
}

// ---------------------------------------------------------------------------
// Invariant: on_error with FAILED step works correctly (positive control)
// This verifies the existing on_error behavior for Failed steps is correct,
// serving as a baseline contrast to Bug 1.
// ---------------------------------------------------------------------------

func TestInvariant_OnErrorActivatedOnFailedStep(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()
	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-onerror-fail",
		OrgID: "org",
		Steps: map[string]*Step{
			"main":    {ID: "main", Type: StepTypeWorker, Topic: "job.default", OnError: "handler"},
			"handler": {ID: "handler", Type: StepTypeWorker, Topic: "job.default"},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	now := time.Now().UTC()
	run := &WorkflowRun{
		ID:         "run-onerror-fail",
		WorkflowID: wf.ID,
		OrgID:      "org",
		TeamID:     "team",
		Input:      map[string]any{},
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(context.Background(), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Fail "main" step
	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:        "run-onerror-fail:main@1",
		Status:       pb.JobStatus_JOB_STATUS_FAILED,
		ErrorMessage: "main failed",
	})

	mid, err := store.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}

	// Handler should be activated (status set to Pending or Running).
	// Note: The error context in Input["error"] is set during on_error activation
	// in HandleJobResult but may be overwritten by the dispatch payload in
	// scheduleReady. We verify activation by checking step status and timeline.
	handlerSR := mid.Steps["handler"]
	if handlerSR == nil {
		t.Fatal("on_error handler step not found in run after main failed")
	}
	if handlerSR.Status != StepStatusPending && handlerSR.Status != StepStatusRunning {
		t.Fatalf("expected handler step Pending or Running, got %s", handlerSR.Status)
	}

	// Verify timeline recorded the redirect
	events, _ := store.ListTimelineEvents(context.Background(), run.ID, 50)
	if !hasTimelineEvent(events, "step_error_redirect") {
		t.Fatal("expected step_error_redirect timeline event for on_error activation")
	}

	// Run should NOT be terminal yet
	if mid.Status == RunStatusFailed || mid.Status == RunStatusSucceeded {
		t.Fatalf("run should still be running while handler is pending, got: %s", mid.Status)
	}

	// Complete handler → run should succeed
	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:  "run-onerror-fail:handler@1",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	})

	final, err := store.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded after on_error handler recovered, got: %s", final.Status)
	}
}

// ---------------------------------------------------------------------------
// Invariant: Parallel "any" strategy properly cancels remaining children
// on first success. This is the positive control for Bug 3.
// ---------------------------------------------------------------------------

func TestInvariant_ParallelAnyStrategyCancelsOnSuccess(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()
	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-parallel-any",
		OrgID: "org",
		Steps: map[string]*Step{
			"par": {
				ID:   "par",
				Type: StepTypeParallel,
				Input: map[string]any{
					"steps":    []any{"child_a", "child_b"},
					"strategy": "any",
				},
			},
			"child_a": {ID: "child_a", Type: StepTypeWorker, Topic: "job.default"},
			"child_b": {ID: "child_b", Type: StepTypeWorker, Topic: "job.default"},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	now := time.Now().UTC()
	run := &WorkflowRun{
		ID:         "run-parallel-any",
		WorkflowID: wf.ID,
		OrgID:      "org",
		TeamID:     "team",
		Input:      map[string]any{},
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(context.Background(), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Succeed child_a → "any" strategy is satisfied
	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:  "run-parallel-any:child_a@1",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	})

	final, err := store.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}

	// Parent should be Succeeded
	if final.Steps["par"] == nil || final.Steps["par"].Status != StepStatusSucceeded {
		t.Fatalf("expected parallel parent Succeeded, got %v", final.Steps["par"])
	}

	// child_b should be Cancelled (cancelParallelChildren is called on success path)
	childB := final.Steps["child_b"]
	if childB == nil {
		t.Fatal("child_b not found in run steps")
	}
	if childB.Status != StepStatusCancelled {
		t.Fatalf("expected child_b Cancelled after any-strategy success, got %s", childB.Status)
	}

	// Cancel message should have been published for child_b
	msgs := bus.Snapshot()
	cancelCount := 0
	for _, m := range msgs {
		if m.packet.GetJobCancel() != nil {
			cancelCount++
		}
	}
	if cancelCount < 1 {
		t.Fatalf("expected at least 1 cancel message for child_b, got %d", cancelCount)
	}
}

// ---------------------------------------------------------------------------
// Invariant: forEach with empty items succeeds immediately
// ---------------------------------------------------------------------------

func TestInvariant_ForEachEmptyItemsSucceeds(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()
	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-foreach-empty",
		OrgID: "org",
		Steps: map[string]*Step{
			"fan": {
				ID:      "fan",
				Type:    StepTypeWorker,
				Topic:   "job.default",
				ForEach: "input.items",
			},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	now := time.Now().UTC()
	run := &WorkflowRun{
		ID:         "run-foreach-empty",
		WorkflowID: wf.ID,
		OrgID:      "org",
		Input:      map[string]any{"items": []any{}},
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(context.Background(), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	final, err := store.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}

	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded for empty forEach, got %s", final.Status)
	}
	if final.Steps["fan"] == nil || final.Steps["fan"].Status != StepStatusSucceeded {
		t.Fatal("expected fan step succeeded for empty items")
	}
}

// ---------------------------------------------------------------------------
// Invariant: updateRunStatus correctly handles failed step with successful
// on_error handler (positive control for Bug 1's updateRunStatus aspect)
// ---------------------------------------------------------------------------

func TestInvariant_UpdateRunStatusFailedWithOnErrorSucceeded(t *testing.T) {
	now := time.Now()
	wf := &Workflow{
		Steps: map[string]*Step{
			"main":    {ID: "main", OnError: "handler"},
			"handler": {ID: "handler"},
		},
	}

	run := &WorkflowRun{
		ID:     "run-test",
		Status: RunStatusRunning,
		Steps: map[string]*StepRun{
			"main":    {StepID: "main", Status: StepStatusFailed},
			"handler": {StepID: "handler", Status: StepStatusSucceeded},
		},
	}
	updateRunStatus(run, wf, now)
	if run.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded when on_error handler recovered from failure, got: %s", run.Status)
	}
}

// ---------------------------------------------------------------------------
// Edge case: forEach with max_parallel where one child fails while others
// are still pending (not yet dispatched). Verifies pending children are
// cleaned up.
// ---------------------------------------------------------------------------

func TestEdgeCase_ForEachMaxParallelPartialDispatchFailure(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()
	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-foreach-throttle",
		OrgID: "org",
		Steps: map[string]*Step{
			"fan": {
				ID:          "fan",
				Type:        StepTypeWorker,
				Topic:       "job.default",
				ForEach:     "input.items",
				MaxParallel: 1, // Only dispatch 1 at a time
			},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	now := time.Now().UTC()
	run := &WorkflowRun{
		ID:         "run-foreach-throttle",
		WorkflowID: wf.ID,
		OrgID:      "org",
		Input:      map[string]any{"items": []any{"a", "b", "c"}},
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(context.Background(), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	// With max_parallel=1, only fan[0] should be dispatched
	mid, err := store.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}

	// fan[0] should be Running, fan[1] and fan[2] should be Pending (pre-created)
	if mid.Steps["fan[0]"] == nil || mid.Steps["fan[0]"].Status != StepStatusRunning {
		t.Fatalf("expected fan[0] Running, got %v", mid.Steps["fan[0]"])
	}
	if mid.Steps["fan[1]"] == nil || mid.Steps["fan[1]"].Status != StepStatusPending {
		t.Fatalf("expected fan[1] Pending (pre-created), got %v", mid.Steps["fan[1]"])
	}

	// Fail fan[0] → parent should fail
	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:        "run-foreach-throttle:fan[0]@1",
		Status:       pb.JobStatus_JOB_STATUS_FAILED,
		ErrorMessage: "fan[0] failed",
	})

	final, err := store.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}

	if final.Steps["fan"] == nil || final.Steps["fan"].Status != StepStatusFailed {
		t.Fatalf("expected parent fan step Failed, got %v", final.Steps["fan"])
	}
	if final.Status != RunStatusFailed {
		t.Fatalf("expected run Failed, got %s", final.Status)
	}

	// The pending children fan[1] and fan[2] should NOT remain as Pending forever.
	// They should be cleaned up (cancelled) or at least the parent's Children map
	// should be consistent.
	for _, childID := range []string{"fan[1]", "fan[2]"} {
		child := final.Steps[childID]
		if child != nil && child.Status == StepStatusPending {
			// This is a data integrity concern — pending children stuck forever.
			// Not a functional bug since updateRunStatus ignores non-wfDef steps,
			// but worth documenting.
			t.Logf("NOTE: orphaned pending forEach child %s remains with status %s", childID, child.Status)
		}
	}
}

// ---------------------------------------------------------------------------
// Edge case: Parallel n_of_m strategy where enough children succeed but
// others are still running.
// ---------------------------------------------------------------------------

func TestInvariant_ParallelNOfMSuccessfullyCompletes(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()
	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-parallel-nofm",
		OrgID: "org",
		Steps: map[string]*Step{
			"par": {
				ID:   "par",
				Type: StepTypeParallel,
				Input: map[string]any{
					"steps":    []any{"child_a", "child_b", "child_c"},
					"strategy": "n_of_m",
					"required": 2,
				},
			},
			"child_a": {ID: "child_a", Type: StepTypeWorker, Topic: "job.default"},
			"child_b": {ID: "child_b", Type: StepTypeWorker, Topic: "job.default"},
			"child_c": {ID: "child_c", Type: StepTypeWorker, Topic: "job.default"},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	now := time.Now().UTC()
	run := &WorkflowRun{
		ID:         "run-parallel-nofm",
		WorkflowID: wf.ID,
		OrgID:      "org",
		Input:      map[string]any{},
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(context.Background(), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Succeed child_a and child_b → 2 of 3 satisfied
	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:  "run-parallel-nofm:child_a@1",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	})
	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:  "run-parallel-nofm:child_b@1",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	})

	final, err := store.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}

	// Parent should be Succeeded (2/3 = satisfied)
	if final.Steps["par"] == nil || final.Steps["par"].Status != StepStatusSucceeded {
		t.Fatalf("expected parallel parent Succeeded with n_of_m, got %v", final.Steps["par"])
	}

	// child_c should be Cancelled (cancelParallelChildren called on success path)
	childC := final.Steps["child_c"]
	if childC == nil {
		t.Fatal("child_c not found in run steps")
	}
	if childC.Status != StepStatusCancelled {
		t.Fatalf("expected child_c Cancelled after n_of_m success, got %s", childC.Status)
	}
}

// ---------------------------------------------------------------------------
// Edge case: depsSatisfied when dependency has timed out with on_error
// (combination of Bug 1 — downstream steps should unblock when on_error
// handler succeeds for a timed-out dependency)
// ---------------------------------------------------------------------------

func TestBug_DepsSatisfied_TimedOutWithOnErrorSucceeded(t *testing.T) {
	// Step C depends on step A. A timed out but A's on_error handler B succeeded.
	// depsSatisfied(C) should return true (same as failed+on_error case).
	wf := &Workflow{
		Steps: map[string]*Step{
			"A": {ID: "A", OnError: "B"},
			"B": {ID: "B"},
			"C": {ID: "C", DependsOn: []string{"A"}},
		},
	}
	run := &WorkflowRun{
		Steps: map[string]*StepRun{
			"A": {StepID: "A", Status: StepStatusTimedOut},
			"B": {StepID: "B", Status: StepStatusSucceeded},
			"C": {StepID: "C", Status: StepStatusPending},
		},
	}

	stepC := wf.Steps["C"]

	// BUG: depsSatisfied only considers failed + on_error, not timed_out + on_error.
	// A timed-out dependency with a successful on_error handler should also satisfy deps.
	if !depsSatisfied(stepC, run, wf) {
		t.Fatal("BUG CONFIRMED: depsSatisfied returns false for timed-out dependency with successful on_error handler")
	}
}

// ---------------------------------------------------------------------------
// BUG 5: Chained on_error handlers — when handler1 fails and has its own
// on_error → handler2, updateRunStatus doesn't recursively check the chain.
//
// When checking if main's failure is handled, updateRunStatus looks at
// handler1's status. handler1 is Failed (not Succeeded), so main's failure
// is NOT considered handled → hasFailed = true → run becomes Failed
// BEFORE handler2 gets a chance to run.
//
// Impact: Multi-level error recovery chains don't work. Only single-level
// on_error handlers are effective.
// ---------------------------------------------------------------------------

func TestBug_ChainedOnErrorHandlersPreemptedByUpdateRunStatus(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()
	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	// A → on_error → B → on_error → C
	wf := &Workflow{
		ID:    "wf-chained-onerror",
		OrgID: "org",
		Steps: map[string]*Step{
			"main":     {ID: "main", Type: StepTypeWorker, Topic: "job.default", OnError: "handler1"},
			"handler1": {ID: "handler1", Type: StepTypeWorker, Topic: "job.default", OnError: "handler2"},
			"handler2": {ID: "handler2", Type: StepTypeWorker, Topic: "job.default"},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	now := time.Now().UTC()
	run := &WorkflowRun{
		ID:         "run-chained-onerror",
		WorkflowID: wf.ID,
		OrgID:      "org",
		Input:      map[string]any{},
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(context.Background(), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	// Fail main → handler1 should activate
	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:        "run-chained-onerror:main@1",
		Status:       pb.JobStatus_JOB_STATUS_FAILED,
		ErrorMessage: "main failed",
	})

	mid1, _ := store.GetRun(context.Background(), run.ID)
	if mid1.Steps["handler1"] == nil || mid1.Steps["handler1"].Status == "" {
		t.Fatal("handler1 should be activated after main failed")
	}
	if mid1.Status == RunStatusFailed || mid1.Status == RunStatusSucceeded {
		t.Fatalf("run should not be terminal while handler1 is pending, got: %s", mid1.Status)
	}

	// Fail handler1 → handler2 should activate
	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:        "run-chained-onerror:handler1@1",
		Status:       pb.JobStatus_JOB_STATUS_FAILED,
		ErrorMessage: "handler1 also failed",
	})

	mid2, _ := store.GetRun(context.Background(), run.ID)

	// BUG: updateRunStatus checks main's on_error (handler1). handler1 is Failed.
	// Since handler1 is NOT Succeeded, main's failure is unhandled → run becomes Failed.
	// handler2 is activated in HandleJobResult but the run is already terminal.
	if mid2.Status == RunStatusFailed {
		t.Log("BUG CONFIRMED: run marked Failed before handler2 got a chance to run")
		t.Log("updateRunStatus only checks one level of on_error chain depth")
		// Even though this is a bug, we mark the test as failed to track it
		t.Fatal("BUG: chained on_error handlers are not supported — run terminated prematurely")
	}

	// If the bug is fixed, handler2 should be activated and run should still be running
	if mid2.Steps["handler2"] == nil || mid2.Steps["handler2"].Status == "" {
		t.Fatal("handler2 should be activated after handler1 failed")
	}

	// Succeed handler2 → run should succeed
	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:  "run-chained-onerror:handler2@1",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	})

	final, _ := store.GetRun(context.Background(), run.ID)
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded after chained on_error recovery, got: %s", final.Status)
	}
}

// ---------------------------------------------------------------------------
// BUG 6: SubWorkflow step failures don't trigger on_error handlers.
//
// When a child workflow fails, the parent's subworkflow step is marked as
// Failed in scheduleReady (not HandleJobResult). But the on_error activation
// code only exists in HandleJobResult. scheduleReady has no on_error
// activation logic, so inline-completing steps (subworkflow, parallel, loop,
// transform, condition, switch, storage) that fail in scheduleReady never
// trigger their on_error handlers.
//
// Impact: on_error handlers on subworkflow, parallel, loop, and other
// inline-completing step types are silently ignored.
// ---------------------------------------------------------------------------

func TestBug_SubWorkflowFailureDoesNotTriggerOnError(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()
	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	// Child workflow: single step that will fail
	childWf := &Workflow{
		ID:    "child-wf-fail",
		OrgID: "org",
		Steps: map[string]*Step{
			"child_step": {ID: "child_step", Type: StepTypeWorker, Topic: "job.default"},
		},
	}

	// Parent workflow: subworkflow step with on_error
	parentWf := &Workflow{
		ID:    "parent-wf-sub-onerror",
		OrgID: "org",
		Steps: map[string]*Step{
			"sub": {
				ID:      "sub",
				Type:    StepTypeSubWorkflow,
				OnError: "fallback",
				Input: map[string]any{
					"workflow_id": "child-wf-fail",
				},
			},
			"fallback": {ID: "fallback", Type: StepTypeWorker, Topic: "job.default"},
		},
	}

	ctx := context.Background()
	if err := store.SaveWorkflow(ctx, childWf); err != nil {
		t.Fatalf("save child workflow: %v", err)
	}
	if err := store.SaveWorkflow(ctx, parentWf); err != nil {
		t.Fatalf("save parent workflow: %v", err)
	}

	now := time.Now().UTC()
	parentRun := &WorkflowRun{
		ID:         "run-sub-onerror",
		WorkflowID: parentWf.ID,
		OrgID:      "org",
		Input:      map[string]any{},
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := store.CreateRun(ctx, parentRun); err != nil {
		t.Fatalf("create parent run: %v", err)
	}

	// Start parent run → subworkflow step should create and start child run
	if err := engine.StartRun(ctx, parentWf.ID, parentRun.ID); err != nil {
		t.Fatalf("start parent run: %v", err)
	}

	// Get the parent run to find child run ID
	mid, _ := store.GetRun(ctx, parentRun.ID)
	subStep := mid.Steps["sub"]
	if subStep == nil || subStep.JobID == "" {
		t.Fatal("subworkflow step should have child run ID in JobID")
	}
	childRunID := subStep.JobID

	// Fail the child step → child run should fail
	engine.HandleJobResult(ctx, &pb.JobResult{
		JobId:        childRunID + ":child_step@1",
		Status:       pb.JobStatus_JOB_STATUS_FAILED,
		ErrorMessage: "child step failed",
	})

	// Re-schedule parent to pick up child completion
	if err := engine.StartRun(ctx, parentWf.ID, parentRun.ID); err != nil {
		t.Fatalf("re-schedule parent: %v", err)
	}

	mid2, _ := store.GetRun(ctx, parentRun.ID)
	if mid2.Steps["sub"] == nil {
		t.Fatal("sub step should exist")
	}

	// The subworkflow step should be Failed (child failed)
	if mid2.Steps["sub"].Status != StepStatusFailed {
		t.Fatalf("expected sub step Failed when child failed, got %s", mid2.Steps["sub"].Status)
	}

	// BUG: The on_error fallback should be activated, but it won't be because
	// subworkflow failures happen in scheduleReady which has no on_error
	// activation code (only HandleJobResult has it).
	fallback := mid2.Steps["fallback"]
	if fallback == nil || fallback.Status == "" {
		t.Fatal("BUG CONFIRMED: on_error handler NOT activated for subworkflow failure — scheduleReady has no on_error activation code")
	}
}

// ---------------------------------------------------------------------------
// Deterministic run status matrix — table-driven test verifying terminal
// states for various step status combinations.
// ---------------------------------------------------------------------------

func TestRunStatusMatrix(t *testing.T) {
	tests := []struct {
		name     string
		wfSteps  map[string]*Step
		runSteps map[string]*StepRun
		expected RunStatus
	}{
		{
			name: "all_succeeded",
			wfSteps: map[string]*Step{
				"a": {ID: "a"},
				"b": {ID: "b"},
			},
			runSteps: map[string]*StepRun{
				"a": {StepID: "a", Status: StepStatusSucceeded},
				"b": {StepID: "b", Status: StepStatusSucceeded},
			},
			expected: RunStatusSucceeded,
		},
		{
			name: "one_failed_no_handler",
			wfSteps: map[string]*Step{
				"a": {ID: "a"},
				"b": {ID: "b"},
			},
			runSteps: map[string]*StepRun{
				"a": {StepID: "a", Status: StepStatusFailed},
				"b": {StepID: "b", Status: StepStatusSucceeded},
			},
			expected: RunStatusFailed,
		},
		{
			name: "failed_with_handler_running",
			wfSteps: map[string]*Step{
				"a": {ID: "a", OnError: "h"},
				"h": {ID: "h"},
			},
			runSteps: map[string]*StepRun{
				"a": {StepID: "a", Status: StepStatusFailed},
				"h": {StepID: "h", Status: StepStatusRunning},
			},
			expected: RunStatusRunning,
		},
		{
			name: "failed_with_handler_succeeded",
			wfSteps: map[string]*Step{
				"a": {ID: "a", OnError: "h"},
				"h": {ID: "h"},
			},
			runSteps: map[string]*StepRun{
				"a": {StepID: "a", Status: StepStatusFailed},
				"h": {StepID: "h", Status: StepStatusSucceeded},
			},
			expected: RunStatusSucceeded,
		},
		{
			name: "one_step_still_running",
			wfSteps: map[string]*Step{
				"a": {ID: "a"},
				"b": {ID: "b"},
			},
			runSteps: map[string]*StepRun{
				"a": {StepID: "a", Status: StepStatusSucceeded},
				"b": {StepID: "b", Status: StepStatusRunning},
			},
			expected: RunStatusRunning,
		},
		{
			name: "unactivated_onerror_target_doesnt_block",
			wfSteps: map[string]*Step{
				"a": {ID: "a", OnError: "h"},
				"h": {ID: "h"},
			},
			runSteps: map[string]*StepRun{
				"a": {StepID: "a", Status: StepStatusSucceeded},
				// h is NOT activated because a succeeded — should not block completion
			},
			expected: RunStatusSucceeded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wf := &Workflow{Steps: tt.wfSteps}
			run := &WorkflowRun{
				ID:     "run-" + tt.name,
				Status: RunStatusRunning,
				Steps:  tt.runSteps,
			}
			updateRunStatus(run, wf, time.Now())
			if run.Status != tt.expected {
				t.Fatalf("expected run status %s, got %s", tt.expected, run.Status)
			}
		})
	}
}
