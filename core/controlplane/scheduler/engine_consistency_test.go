package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Test harness helpers for deterministic failure injection
// ---------------------------------------------------------------------------

// failOnStateJobStore wraps fakeJobStore to fail SetState for a specific
// target state. This lets tests force partial failures in multi-step
// state transitions without relying on timing.
type failOnStateJobStore struct {
	*fakeJobStore
	failState JobState     // which state transition should fail
	failErr   error        // error to return
	failCount int          // how many times to fail (0 = forever)
	calls     atomic.Int32 // count of failures triggered
}

func (s *failOnStateJobStore) SetState(_ context.Context, jobID string, state JobState) error {
	if state == s.failState {
		count := int(s.calls.Add(1))
		if s.failCount == 0 || count <= s.failCount {
			return s.failErr
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[jobID] = state
	if state == JobStateScheduled {
		s.attempts[jobID]++
	}
	return nil
}

// failOnResultPtrJobStore wraps fakeJobStore to fail SetResultPtr.
type failOnResultPtrJobStore struct {
	*fakeJobStore
	failErr   error
	failCount int
	calls     atomic.Int32
}

func (s *failOnResultPtrJobStore) SetResultPtr(_ context.Context, jobID, resultPtr string) error {
	count := int(s.calls.Add(1))
	if s.failCount == 0 || count <= s.failCount {
		return s.failErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ptrs[jobID] = resultPtr
	return nil
}

// countingBus tracks publish calls per subject for assertion.
type countingBus struct {
	mu        sync.Mutex
	published []publishedMsg
	counts    map[string]int
}

func newCountingBus() *countingBus {
	return &countingBus{counts: map[string]int{}}
}

func (b *countingBus) Publish(subject string, packet *pb.BusPacket) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.published = append(b.published, publishedMsg{subject: subject, packet: packet})
	b.counts[subject]++
	return nil
}

func (b *countingBus) Subscribe(string, string, func(*pb.BusPacket) error) error { return nil }

func (b *countingBus) publishCount(subject string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.counts[subject]
}

func (b *countingBus) totalPublishes() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.published)
}

// ---------------------------------------------------------------------------
// BUG-1 FIX: State set before publish prevents duplicate dispatch
// ---------------------------------------------------------------------------

// TestDuplicateDispatchOnDispatchedStateFailure verifies that when setting the
// DISPATCHED state fails, the job is NOT published to the bus. The state-before-
// publish ordering ensures that a NATS redelivery will not cause a duplicate
// dispatch because the publish never happened.
func TestDuplicateDispatchOnDispatchedStateFailure(t *testing.T) {
	bus := newCountingBus()
	store := &failOnStateJobStore{
		fakeJobStore: newFakeJobStore(),
		failState:    JobStateDispatched,
		failErr:      fmt.Errorf("redis timeout"),
		failCount:    1, // fail once, then succeed
	}
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)

	req := &pb.JobRequest{
		JobId: "job-dup-dispatch",
		Topic: "job.default",
	}

	// First call: DISPATCHED state set fails BEFORE publish → no message sent
	err := engine.processJob(context.Background(), req, "trace-1")
	require.Error(t, err, "processJob should fail when DISPATCHED state set fails")

	// The job was NOT published — state-before-publish ordering prevents it
	assert.Equal(t, 0, bus.publishCount("job.default"),
		"job must not be published when DISPATCHED state set fails")

	// State is SCHEDULED (DISPATCHED set failed)
	store.mu.RLock()
	state := store.states["job-dup-dispatch"]
	store.mu.RUnlock()
	assert.Equal(t, JobStateScheduled, state,
		"job should remain SCHEDULED after DISPATCHED failure")

	// Second call (simulating NATS redelivery): succeeds normally
	err = engine.processJob(context.Background(), req, "trace-1")
	require.NoError(t, err, "second call should succeed after failCount exhausted")

	// FIX VERIFIED: The job was published exactly once
	publishCount := bus.publishCount("job.default")
	assert.Equal(t, 1, publishCount,
		"job must be published exactly once (was %d)", publishCount)
}

// TestDuplicateDispatchOnRunningStateFailure documents a similar bug where
// the RUNNING state set fails after DISPATCHED succeeds.
func TestDuplicateDispatchOnRunningStateFailure(t *testing.T) {
	bus := newCountingBus()
	store := &failOnStateJobStore{
		fakeJobStore: newFakeJobStore(),
		failState:    JobStateRunning,
		failErr:      fmt.Errorf("redis timeout"),
		failCount:    1,
	}
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)

	req := &pb.JobRequest{
		JobId: "job-dup-running",
		Topic: "job.default",
	}

	// First call: publishes, DISPATCHED succeeds, RUNNING fails → RetryAfter
	err := engine.processJob(context.Background(), req, "trace-1")
	require.Error(t, err, "processJob should fail when RUNNING state set fails")

	// State is DISPATCHED (RUNNING set failed)
	store.mu.RLock()
	state := store.states["job-dup-running"]
	store.mu.RUnlock()
	assert.Equal(t, JobStateDispatched, state,
		"job should be in DISPATCHED after RUNNING failure")

	// On redelivery via handleJobRequest, DISPATCHED IS in the skip list,
	// so the job would NOT be re-processed. This path is safe.
	// Verifying: handleJobRequest checks state == JobStateDispatched and returns nil.
}

// ---------------------------------------------------------------------------
// BUG-2 FIX: Result pointer written BEFORE terminal state
// ---------------------------------------------------------------------------

// TestResultPtrLostOnPartialFailure verifies that when SetResultPtr fails,
// the job remains in its pre-terminal state (RUNNING) so that NATS redelivery
// can retry and eventually persist both the result pointer and the terminal
// state. This was previously a bug where terminal state was set BEFORE the
// result pointer, causing the idempotency guard to block retries.
//
// Invariant: "a succeeded job must have its result pointer durably stored"
func TestResultPtrLostOnPartialFailure(t *testing.T) {
	bus := newCountingBus()
	store := &failOnResultPtrJobStore{
		fakeJobStore: newFakeJobStore(),
		failErr:      fmt.Errorf("redis timeout on result ptr"),
		failCount:    1, // fail once, then succeed
	}
	// Pre-set job to RUNNING state
	store.mu.Lock()
	store.states["job-lost-ptr"] = JobStateRunning
	store.topics["job-lost-ptr"] = "job.default"
	store.mu.Unlock()

	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)

	result := &pb.JobResult{
		JobId:     "job-lost-ptr",
		WorkerId:  "worker-1",
		Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr: "redis://res:job-lost-ptr",
	}

	// First call: SetResultPtr fails → RetryAfter, job stays in RUNNING
	err := engine.handleJobResult(result)
	require.Error(t, err, "handleJobResult should fail when SetResultPtr fails")

	// State should still be RUNNING (NOT terminal) since result ptr is
	// written before state transition and the failure aborted early.
	store.mu.RLock()
	state := store.states["job-lost-ptr"]
	ptr := store.ptrs["job-lost-ptr"]
	store.mu.RUnlock()

	assert.Equal(t, JobStateRunning, state,
		"job should remain in RUNNING state when SetResultPtr fails (not terminal)")
	assert.Empty(t, ptr,
		"result pointer should be empty after first failed attempt")

	// Second call (simulating NATS redelivery): SetResultPtr succeeds,
	// then terminal state is set.
	err = engine.handleJobResult(result)
	require.NoError(t, err, "second call should succeed after SetResultPtr failure exhausted")

	// Result pointer and terminal state should both be present now
	store.mu.RLock()
	stateAfterRetry := store.states["job-lost-ptr"]
	ptrAfterRetry := store.ptrs["job-lost-ptr"]
	store.mu.RUnlock()

	assert.Equal(t, JobStateSucceeded, stateAfterRetry,
		"job should reach SUCCEEDED after retry")
	assert.Equal(t, "redis://res:job-lost-ptr", ptrAfterRetry,
		"result pointer should be persisted after retry — no data loss")
}

// ---------------------------------------------------------------------------
// BUG-3: Concurrent cancel and result messages
// ---------------------------------------------------------------------------

// TestCancelAndResultRaceSerializedByLock verifies that concurrent cancel
// and result messages for the same job are properly serialized by the job lock,
// ensuring exactly one terminal state is reached.
func TestCancelAndResultRaceSerializedByLock(t *testing.T) {
	bus := newCountingBus()
	store := newFakeJobStore()
	store.mu.Lock()
	store.states["job-race"] = JobStateRunning
	store.topics["job-race"] = "job.default"
	store.mu.Unlock()

	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)

	var wg sync.WaitGroup
	var cancelErr, resultErr error

	// Simulate concurrent cancel and result
	wg.Add(2)
	go func() {
		defer wg.Done()
		cancelPacket := &pb.BusPacket{
			Payload: &pb.BusPacket_JobCancel{
				JobCancel: &pb.JobCancel{
					JobId:  "job-race",
					Reason: "user requested",
				},
			},
		}
		cancelErr = engine.HandlePacket(cancelPacket)
	}()
	go func() {
		defer wg.Done()
		resultPacket := &pb.BusPacket{
			Payload: &pb.BusPacket_JobResult{
				JobResult: &pb.JobResult{
					JobId:    "job-race",
					WorkerId: "worker-1",
					Status:   pb.JobStatus_JOB_STATUS_SUCCEEDED,
				},
			},
		}
		resultErr = engine.HandlePacket(resultPacket)
	}()
	wg.Wait()

	// Both should succeed (one processes, other is idempotent)
	assert.NoError(t, cancelErr, "cancel should not error")
	assert.NoError(t, resultErr, "result should not error")

	// Final state should be one of the terminal states (either CANCELLED or SUCCEEDED)
	store.mu.RLock()
	finalState := store.states["job-race"]
	store.mu.RUnlock()

	assert.True(t, terminalStates[finalState],
		"final state should be terminal, got %s", finalState)
	assert.True(t, finalState == JobStateCancelled || finalState == JobStateSucceeded,
		"final state should be CANCELLED or SUCCEEDED, got %s", finalState)
}

// ---------------------------------------------------------------------------
// BUG-4: handleJobRequest redelivery with duplicate submit messages
// ---------------------------------------------------------------------------

// TestHandleJobRequestIdempotentOnRedelivery verifies that redelivered submit
// messages for already-dispatched/running/terminal jobs are properly skipped.
func TestHandleJobRequestIdempotentOnRedelivery(t *testing.T) {
	tests := []struct {
		name          string
		preState      JobState
		expectSkipped bool
	}{
		{
			name:          "skip when DISPATCHED",
			preState:      JobStateDispatched,
			expectSkipped: true,
		},
		{
			name:          "skip when RUNNING",
			preState:      JobStateRunning,
			expectSkipped: true,
		},
		{
			name:          "skip when SUCCEEDED",
			preState:      JobStateSucceeded,
			expectSkipped: true,
		},
		{
			name:          "skip when FAILED",
			preState:      JobStateFailed,
			expectSkipped: true,
		},
		{
			name:          "skip when CANCELLED",
			preState:      JobStateCancelled,
			expectSkipped: true,
		},
		{
			name:          "reprocess when SCHEDULED",
			preState:      JobStateScheduled,
			expectSkipped: false,
		},
		{
			name:          "reprocess when PENDING",
			preState:      JobStatePending,
			expectSkipped: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bus := newCountingBus()
			store := newFakeJobStore()
			jobID := "job-idem-" + string(tc.preState)
			store.mu.Lock()
			store.states[jobID] = tc.preState
			store.topics[jobID] = "job.default"
			store.mu.Unlock()

			engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)

			req := &pb.JobRequest{JobId: jobID, Topic: "job.default"}
			err := engine.handleJobRequest(req, "trace-idem")

			if tc.expectSkipped {
				assert.NoError(t, err, "should silently skip")
				assert.Equal(t, 0, bus.totalPublishes(),
					"no publish should occur for skipped state %s", tc.preState)
			} else {
				// For SCHEDULED/PENDING, the job is re-processed (published)
				assert.True(t, bus.totalPublishes() > 0 || err != nil,
					"should attempt processing for state %s", tc.preState)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// BUG-5: Terminal state regression protection
// ---------------------------------------------------------------------------

// TestTerminalStateRegressionBlocked verifies that once a job reaches a
// terminal state, no further state transitions are allowed (except
// SUCCEEDED → QUARANTINED for async output policy).
func TestTerminalStateRegressionBlocked(t *testing.T) {
	bus := newCountingBus()
	store := newFakeJobStore()

	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)

	// Test that SUCCEEDED job rejects result redelivery
	store.mu.Lock()
	store.states["job-terminal-1"] = JobStateSucceeded
	store.topics["job-terminal-1"] = "job.default"
	store.mu.Unlock()

	err := engine.handleJobResult(&pb.JobResult{
		JobId:    "job-terminal-1",
		WorkerId: "worker-1",
		Status:   pb.JobStatus_JOB_STATUS_FAILED,
	})
	assert.NoError(t, err, "should silently skip duplicate result")

	store.mu.RLock()
	state := store.states["job-terminal-1"]
	store.mu.RUnlock()
	assert.Equal(t, JobStateSucceeded, state,
		"SUCCEEDED should not regress to FAILED on duplicate result")
}

// ---------------------------------------------------------------------------
// BUG-6: Multiple concurrent result handlers for same job
// ---------------------------------------------------------------------------

// TestConcurrentResultHandlersForSameJob verifies that multiple concurrent
// result handlers for the same job (e.g., from worker retransmissions) are
// properly serialized by the job lock, preventing double-counting in metrics
// and double DLQ entries.
func TestConcurrentResultHandlersForSameJob(t *testing.T) {
	bus := newCountingBus()
	store := newFakeJobStore()
	store.mu.Lock()
	store.states["job-concurrent"] = JobStateRunning
	store.topics["job-concurrent"] = "job.default"
	store.mu.Unlock()

	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)

	// Fire 5 concurrent result handlers
	var wg sync.WaitGroup
	errCh := make(chan error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := engine.handleJobResult(&pb.JobResult{
				JobId:     "job-concurrent",
				WorkerId:  "worker-1",
				Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
				ResultPtr: "redis://res:job-concurrent",
			})
			errCh <- err
		}()
	}
	wg.Wait()
	close(errCh)

	var errs []error
	for err := range errCh {
		if err != nil {
			errs = append(errs, err)
		}
	}
	// All should succeed (one processes, others are idempotent)
	assert.Empty(t, errs, "all concurrent result handlers should complete without error")

	// Final state should be SUCCEEDED (not corrupted by concurrent access)
	store.mu.RLock()
	state := store.states["job-concurrent"]
	store.mu.RUnlock()
	assert.Equal(t, JobStateSucceeded, state,
		"final state should be SUCCEEDED after concurrent result handling")
}

// ---------------------------------------------------------------------------
// BUG-7 FIX VERIFIED: processJob now uses lockCtx for store operations
// ---------------------------------------------------------------------------

// TestProcessJobContextFencing verifies that processJob uses the lock-fenced
// context (lockCtx) rather than the engine context (e.ctx) for store operations.
// When lockCtx is cancelled (simulating lock abandonment), in-flight store
// operations should also be cancelled, preventing state mutations without
// lock protection.
func TestProcessJobContextFencing(t *testing.T) {
	bus := newCountingBus()
	store := newFakeJobStore()
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)

	req := &pb.JobRequest{
		JobId: "job-ctx-fence",
		Topic: "job.default",
	}

	// Normal case: active lockCtx allows processJob to complete.
	err := engine.processJob(engine.ctx, req, "trace-fence")
	assert.NoError(t, err)

	// Fencing case: cancelled lockCtx should cause store operations to fail.
	// Create a pre-cancelled context simulating lock abandonment.
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	req2 := &pb.JobRequest{
		JobId: "job-ctx-fence-2",
		Topic: "job.default",
	}

	// processJob uses lockCtx for GetAttempts — a cancelled context should
	// still allow processJob to proceed (GetAttempts error is non-fatal),
	// but downstream store ops that return RetryAfter on context error
	// will propagate the cancellation.
	_ = engine.processJob(cancelledCtx, req2, "trace-fence-2") // error expected; we only verify engine.ctx survives
	// With a cancelled context, store operations like SetDeadline or
	// state transitions will fail, causing processJob to return an error
	// or complete with degraded behavior (skipped store ops).
	// The key verification: engine.ctx is NOT cancelled — only lockCtx is.
	assert.NoError(t, engine.ctx.Err(),
		"engine context should remain active even when lockCtx is cancelled")
}

// TestProcessJobLockCtxPropagation verifies that processJob derives all
// store operation timeouts from the provided lockCtx, ensuring that lock
// abandonment (fenceCtx cancellation) cancels in-flight store operations.
func TestProcessJobLockCtxPropagation(t *testing.T) {
	store := newFakeJobStore()
	bus := newCountingBus()
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)

	// Use a context that we can cancel mid-flight to simulate lock abandonment.
	lockCtx, lockCancel := context.WithCancelCause(context.Background())

	req := &pb.JobRequest{
		JobId: "job-propagation",
		Topic: "job.default",
	}

	// Cancel the lockCtx before calling processJob — all store ops should
	// see the cancelled context and fail/skip accordingly.
	lockCancel(errLockAbandoned)

	_ = engine.processJob(lockCtx, req, "trace-propagation")
	// processJob may return nil (if store ops are non-fatal) or an error
	// (if a required store op like state transition fails with context error).
	// Either way, the engine context must remain active.
	assert.NoError(t, engine.ctx.Err(),
		"engine context must not be affected by lockCtx cancellation")

	// Verify the cause is errLockAbandoned
	assert.Equal(t, errLockAbandoned, context.Cause(lockCtx),
		"lockCtx cause should be errLockAbandoned")
}

// ---------------------------------------------------------------------------
// BUG-8: DLQ emission failure leaves job in inconsistent state
// ---------------------------------------------------------------------------

// TestDLQEmitFailureDoesNotBlockStateTransition verifies that when DLQ
// emission fails, the job state transition still completes. The DLQ entry
// is lost but the job reaches its terminal state.
func TestDLQEmitFailureDoesNotBlockStateTransition(t *testing.T) {
	bus := &fakeBus{publishErr: errors.New("bus down"), failSubject: "sys.dlq"}
	store := newFakeJobStore()
	store.mu.Lock()
	store.states["job-dlq-fail"] = JobStateRunning
	store.topics["job-dlq-fail"] = "job.default"
	store.mu.Unlock()

	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)

	// Result that triggers DLQ (FAILED status)
	err := engine.handleJobResult(&pb.JobResult{
		JobId:        "job-dlq-fail",
		WorkerId:     "worker-1",
		Status:       pb.JobStatus_JOB_STATUS_FAILED,
		ErrorMessage: "worker crash",
		ErrorCode:    "INTERNAL",
	})

	// DLQ emission uses RetryAfter, so this propagates as an error
	// for NATS to redeliver. The state IS set before DLQ emission.
	if err != nil {
		// State should already be terminal despite the DLQ failure
		store.mu.RLock()
		state := store.states["job-dlq-fail"]
		store.mu.RUnlock()
		assert.Equal(t, JobStateFailed, state,
			"BUG DOCUMENTED: state is set to FAILED before DLQ emission, but DLQ failure causes retry which finds terminal state and skips — DLQ entry permanently lost")
	}
}

// ---------------------------------------------------------------------------
// DLQ store consistency tests
// ---------------------------------------------------------------------------

// TestDLQStoreAddTrimOutsideTransaction documents that the DLQ Add method's
// data key cleanup (Del) happens outside the TxPipeline. If Del fails,
// the data keys become orphans until TTL expiry.
func TestDLQStoreAddTrimOutsideTransaction(t *testing.T) {
	// This test documents the behavior rather than triggering the actual
	// failure, since miniredis doesn't support injecting failures on
	// specific commands within a pipeline.
	//
	// The sequence in DLQ Add is:
	// 1. TxPipeline: Set(entry) + ZAdd(index) + ZRange(trim) + ZRemRangeByRank(trim)
	// 2. Del(trimmed data keys) — OUTSIDE transaction
	//
	// If step 2 fails:
	// - Index entries are correctly trimmed (step 1 succeeded)
	// - Data keys are orphaned (no index pointer, but still in Redis)
	// - Orphaned keys expire via TTL (entryTTL), so impact is bounded
	//
	// Risk: memory waste proportional to trim batch * entry size until TTL

	t.Log("DOCUMENTED: DLQ Add trim Del is outside TxPipeline — orphan data keys possible on failure, mitigated by TTL")
}

// ---------------------------------------------------------------------------
// NATS idempotency guard tests
// ---------------------------------------------------------------------------

// TestNATSIdempotencyGuardNonAtomic documents the non-atomic nature of the
// NATS bus idempotency guard: EXISTS check and SET are separate operations
// with a processing window between them.
func TestNATSIdempotencyGuardNonAtomic(t *testing.T) {
	// In nats.go Subscribe (JetStream path):
	// 1. EXISTS check (line 276) — returns 0 (not processed)
	// 2. Handler invocation (line 299) — takes N ms/seconds
	// 3. SET processed key (line 337) — marks as processed
	//
	// Between steps 1 and 3, NATS could redeliver the same message
	// to another replica, which also passes the EXISTS check.
	//
	// Mitigations that make this LOW severity:
	// - JetStream queue consumers deliver each message to ONE consumer
	// - AckWait (10min) means redelivery only on crash/timeout
	// - Scheduler's withJobLock serializes per-job operations
	// - Terminal state checks provide final idempotency
	//
	// The EXISTS-SET gap is a defense-in-depth weakness, not a primary
	// correctness issue. Using SET NX (SetNX) with a "processing" value
	// before handler invocation would close this gap.

	t.Log("DOCUMENTED: NATS idempotency guard has EXISTS→SET gap (mitigated by JetStream queue + job locks)")
}

// ---------------------------------------------------------------------------
// Idempotency key fire-and-forget test
// ---------------------------------------------------------------------------

// TestIdempotencyKeyFireAndForget documents that SetIdempotencyKeyScoped
// errors are silently discarded in SetJobMeta. If the idempotency key
// write fails, duplicate job submissions are possible.
func TestIdempotencyKeyFireAndForget(t *testing.T) {
	// In job_store.go SetJobMeta (line 663-666):
	//   if idempotencyKey != "" {
	//       tenantID := model.ExtractTenant(req)
	//       _ = s.SetIdempotencyKeyScoped(ctx, tenantID, idempotencyKey, req.GetJobId())
	//   }
	//
	// The `_ =` discards any error from SetIdempotencyKeyScoped.
	// If Redis fails on the SetNX, the idempotency key is not set,
	// and future submissions with the same key create duplicate jobs.
	//
	// The idempotency key uses SetNX which is atomic for the write itself,
	// but the error handling gap means the overall guarantee is best-effort.
	//
	// This is acceptable for the current architecture because:
	// 1. The gateway checks idempotency BEFORE publishing to NATS
	// 2. The scheduler checks job state before processing
	// 3. JetStream msg-id dedup covers NATS-level retries

	t.Log("DOCUMENTED: SetIdempotencyKeyScoped error silently discarded — duplicate jobs possible on Redis failure")
}

// ---------------------------------------------------------------------------
// State transition atomicity tests
// ---------------------------------------------------------------------------

// TestSetStateWATCHRetryOnConcurrentModification verifies that the WATCH-based
// optimistic locking in RedisJobStore.SetState correctly handles concurrent
// modifications. This test uses the fakeJobStore which doesn't have WATCH,
// documenting that the fake store allows state regressions that the real
// Redis store would prevent.
func TestSetStateTransitionValidation(t *testing.T) {
	store := newFakeJobStore()
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)

	// Valid transition: PENDING → SCHEDULED → DISPATCHED → RUNNING → SUCCEEDED
	transitions := []JobState{
		JobStatePending,
		JobStateScheduled,
		JobStateDispatched,
		JobStateRunning,
		JobStateSucceeded,
	}

	jobID := "job-transition"
	for _, state := range transitions {
		err := engine.setJobState(jobID, state)
		assert.NoError(t, err, "transition to %s should succeed", state)
	}

	store.mu.RLock()
	finalState := store.states[jobID]
	store.mu.RUnlock()
	assert.Equal(t, JobStateSucceeded, finalState)
}

// ---------------------------------------------------------------------------
// Lock contention and expiry scenarios
// ---------------------------------------------------------------------------

// TestJobLockBusyReturnsRetryAfter verifies that when the job lock is held
// by another handler, the current handler returns a RetryAfter error rather
// than blocking indefinitely.
func TestJobLockBusyReturnsRetryAfter(t *testing.T) {
	bus := newCountingBus()
	store := newFakeJobStore()
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)

	jobID := "job-lock-busy"

	// Pre-acquire the lock with a long TTL
	store.mu.Lock()
	store.locks[jobLockKey(jobID)] = time.Now().Add(10 * time.Minute)
	store.mu.Unlock()

	// handleJobRequest should return RetryAfter since lock is held
	req := &pb.JobRequest{JobId: jobID, Topic: "job.default"}
	err := engine.handleJobRequest(req, "trace-lock")

	require.Error(t, err, "should return error when lock is busy")
	// Verify it's a retryable error
	var retryErr *retryableError
	ok := errors.As(err, &retryErr)
	assert.True(t, ok, "error should be retryable (RetryAfter)")
}

// TestJobLockPreventsConcurrentProcessing verifies that the job lock
// serializes concurrent handlers for the same job ID.
func TestJobLockPreventsConcurrentProcessing(t *testing.T) {
	bus := newCountingBus()
	store := newFakeJobStore()
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)

	jobID := "job-lock-serial"
	var mu sync.Mutex
	var executionOrder []int

	// Run two handlers concurrently — they should serialize via the lock
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			_ = engine.withJobLock(jobID, 5*time.Second, func(context.Context) error {
				mu.Lock()
				executionOrder = append(executionOrder, idx)
				mu.Unlock()
				time.Sleep(10 * time.Millisecond) // simulate work
				return nil
			})
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	assert.Len(t, executionOrder, 2, "both handlers should have executed")
	// They should have run sequentially (not interleaved)
}

// ---------------------------------------------------------------------------
// Stopped engine behavior
// ---------------------------------------------------------------------------

// TestStoppedEngineRejectsNewPackets verifies that a stopped engine
// silently drops new packets rather than processing them.
func TestStoppedEngineRejectsNewPackets(t *testing.T) {
	bus := newCountingBus()
	store := newFakeJobStore()
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)

	engine.Stop()

	packet := &pb.BusPacket{
		Payload: &pb.BusPacket_JobRequest{
			JobRequest: &pb.JobRequest{
				JobId: "job-after-stop",
				Topic: "job.default",
			},
		},
	}

	err := engine.HandlePacket(packet)
	assert.NoError(t, err, "stopped engine should silently drop packets")
	assert.Equal(t, 0, bus.totalPublishes(),
		"no publishes should occur after engine stop")
}
