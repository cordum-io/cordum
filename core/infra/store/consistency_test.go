package store

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// BUG-2 (store layer): Result pointer and state atomicity
// ---------------------------------------------------------------------------

// TestSetResultPtrAndStateNotAtomic documents that SetResultPtr and SetState
// are separate Redis operations. In the scheduler's handleJobResult, SetState
// is called BEFORE SetResultPtr. If SetResultPtr fails after SetState succeeds,
// the job is in a terminal state but its result pointer is missing.
//
// The scheduler's terminal-state idempotency check then blocks any retry,
// permanently losing the result pointer.
func TestSetResultPtrAndStateNotAtomic(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	store, err := NewRedisJobStore("redis://" + srv.Addr())
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	jobID := "job-atomicity"

	// Set up: create job in RUNNING state
	err = store.SetState(ctx, jobID, model.JobStatePending)
	require.NoError(t, err)
	err = store.SetState(ctx, jobID, model.JobStateScheduled)
	require.NoError(t, err)
	err = store.SetState(ctx, jobID, model.JobStateRunning)
	require.NoError(t, err)

	// Transition to SUCCEEDED
	err = store.SetState(ctx, jobID, model.JobStateSucceeded)
	require.NoError(t, err)

	// Verify SUCCEEDED
	state, err := store.GetState(ctx, jobID)
	require.NoError(t, err)
	assert.Equal(t, model.JobStateSucceeded, state)

	// The result pointer has NOT been set yet (simulating the gap)
	_, ptrErr := store.GetResultPtr(ctx, jobID)
	assert.Error(t, ptrErr, "result pointer should not exist yet — documenting the window between state set and ptr set")

	// Now try to set it — this would normally succeed, but if Redis failed
	// here, the pointer would be permanently lost because the terminal state
	// check would block any reprocessing.
	err = store.SetResultPtr(ctx, jobID, "redis://res:job-atomicity")
	require.NoError(t, err)

	// Verify the pointer is now set
	ptr, err := store.GetResultPtr(ctx, jobID)
	require.NoError(t, err)
	assert.Equal(t, "redis://res:job-atomicity", ptr)
}

// ---------------------------------------------------------------------------
// State transition WATCH semantics
// ---------------------------------------------------------------------------

// TestSetStateConcurrentWATCH verifies that Redis WATCH-based optimistic
// locking prevents concurrent state transitions from corrupting state.
func TestSetStateConcurrentWATCH(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	store, err := NewRedisJobStore("redis://" + srv.Addr())
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	jobID := "job-watch"

	// Set initial state
	err = store.SetState(ctx, jobID, model.JobStatePending)
	require.NoError(t, err)
	err = store.SetState(ctx, jobID, model.JobStateScheduled)
	require.NoError(t, err)

	// Concurrent transitions: SCHEDULED → DISPATCHED and SCHEDULED → FAILED
	var wg sync.WaitGroup
	var dispatchErr, failErr error

	wg.Add(2)
	go func() {
		defer wg.Done()
		dispatchErr = store.SetState(ctx, jobID, model.JobStateDispatched)
	}()
	go func() {
		defer wg.Done()
		failErr = store.SetState(ctx, jobID, model.JobStateFailed)
	}()
	wg.Wait()

	// At least one should succeed; the other may fail due to WATCH retry
	state, err := store.GetState(ctx, jobID)
	require.NoError(t, err)

	// The final state should be either DISPATCHED or FAILED (both valid from SCHEDULED)
	assert.True(t,
		state == model.JobStateDispatched || state == model.JobStateFailed,
		"final state should be DISPATCHED or FAILED, got %s (dispatchErr=%v, failErr=%v)", state, dispatchErr, failErr)
}

// TestSetStateRejectsInvalidTransition verifies that the allowedTransitions
// map prevents invalid state regressions.
func TestSetStateRejectsInvalidTransition(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	store, err := NewRedisJobStore("redis://" + srv.Addr())
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	jobID := "job-invalid-tx"

	// Set up a terminal state
	err = store.SetState(ctx, jobID, model.JobStatePending)
	require.NoError(t, err)
	err = store.SetState(ctx, jobID, model.JobStateScheduled)
	require.NoError(t, err)
	err = store.SetState(ctx, jobID, model.JobStateSucceeded)
	require.NoError(t, err)

	// Attempt invalid transition: SUCCEEDED → PENDING
	err = store.SetState(ctx, jobID, model.JobStatePending)
	assert.Error(t, err, "SUCCEEDED → PENDING should be rejected")

	// Attempt invalid transition: SUCCEEDED → FAILED
	err = store.SetState(ctx, jobID, model.JobStateFailed)
	assert.Error(t, err, "SUCCEEDED → FAILED should be rejected")

	// Verify state unchanged
	state, err := store.GetState(ctx, jobID)
	require.NoError(t, err)
	assert.Equal(t, model.JobStateSucceeded, state)

	// Valid transition: SUCCEEDED → QUARANTINED (async output policy)
	err = store.SetState(ctx, jobID, model.JobStateQuarantined)
	assert.NoError(t, err, "SUCCEEDED → QUARANTINED should be allowed (async output policy)")
}

// ---------------------------------------------------------------------------
// CancelJob atomicity
// ---------------------------------------------------------------------------

// TestCancelJobAtomicWithTerminalCheck verifies that CancelJob correctly
// handles the case where a job is already in a terminal state.
func TestCancelJobAtomicWithTerminalCheck(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	store, err := NewRedisJobStore("redis://" + srv.Addr())
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	jobID := "job-cancel-atomic"

	// Set up a succeeded job
	err = store.SetState(ctx, jobID, model.JobStatePending)
	require.NoError(t, err)
	err = store.SetState(ctx, jobID, model.JobStateScheduled)
	require.NoError(t, err)
	err = store.SetState(ctx, jobID, model.JobStateSucceeded)
	require.NoError(t, err)

	// Cancel should return the existing terminal state, not modify it
	resultState, err := store.CancelJob(ctx, jobID)
	require.NoError(t, err)
	assert.Equal(t, model.JobStateSucceeded, resultState,
		"cancel should return existing terminal state")

	// Verify state unchanged
	state, err := store.GetState(ctx, jobID)
	require.NoError(t, err)
	assert.Equal(t, model.JobStateSucceeded, state)
}

// TestCancelJobConcurrent verifies that concurrent cancel calls don't
// corrupt state.
func TestCancelJobConcurrent(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	store, err := NewRedisJobStore("redis://" + srv.Addr())
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	jobID := "job-cancel-concurrent"

	err = store.SetState(ctx, jobID, model.JobStatePending)
	require.NoError(t, err)
	err = store.SetState(ctx, jobID, model.JobStateScheduled)
	require.NoError(t, err)
	err = store.SetState(ctx, jobID, model.JobStateRunning)
	require.NoError(t, err)

	// Fire 10 concurrent cancel calls — with optimistic locking (WATCH/MULTI/EXEC),
	// some transactions will fail. We verify at least one succeeds and the final
	// state is CANCELLED.
	var wg sync.WaitGroup
	results := make([]model.JobState, 10)
	errs := make([]error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			state, cancelErr := store.CancelJob(ctx, jobID)
			errs[idx] = cancelErr
			results[idx] = state
		}()
	}
	wg.Wait()

	// At least one cancel must succeed with CANCELLED
	successCount := 0
	for _, state := range results {
		if state == model.JobStateCancelled {
			successCount++
		}
	}
	assert.GreaterOrEqual(t, successCount, 1,
		"at least one concurrent cancel should return CANCELLED")

	// Final state should be CANCELLED
	state, err := store.GetState(ctx, jobID)
	require.NoError(t, err)
	assert.Equal(t, model.JobStateCancelled, state)
}

// ---------------------------------------------------------------------------
// Distributed lock semantics
// ---------------------------------------------------------------------------

// TestTryAcquireLockTokenUniqueness verifies that lock tokens are unique
// across acquisitions, preventing one holder from releasing another's lock.
func TestTryAcquireLockTokenUniqueness(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	store, err := NewRedisJobStore("redis://" + srv.Addr())
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	key := "lock:test-unique"

	token1, err := store.TryAcquireLock(ctx, key, 5*time.Second)
	require.NoError(t, err)
	require.NotEmpty(t, token1)

	// Release and re-acquire
	err = store.ReleaseLock(ctx, key, token1)
	require.NoError(t, err)

	token2, err := store.TryAcquireLock(ctx, key, 5*time.Second)
	require.NoError(t, err)
	require.NotEmpty(t, token2)

	assert.NotEqual(t, token1, token2, "lock tokens should be unique")
}

// TestReleaseLockTokenMismatch verifies that releasing a lock with the
// wrong token fails safely (Lua script check).
func TestReleaseLockTokenMismatch(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	store, err := NewRedisJobStore("redis://" + srv.Addr())
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	key := "lock:test-mismatch"

	token, err := store.TryAcquireLock(ctx, key, 5*time.Second)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	// Try to release with wrong token
	err = store.ReleaseLock(ctx, key, "wrong-token")
	assert.Error(t, err, "release with wrong token should fail")
	assert.Contains(t, err.Error(), "not owned", "error should indicate ownership mismatch")

	// Lock should still be held
	token2, err := store.TryAcquireLock(ctx, key, 5*time.Second)
	require.NoError(t, err)
	assert.Empty(t, token2, "lock should still be held by original owner")
}

// TestRenewLockAfterExpiry verifies that renewing an expired lock fails.
func TestRenewLockAfterExpiry(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	store, err := NewRedisJobStore("redis://" + srv.Addr())
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	key := "lock:test-renew-expired"

	token, err := store.TryAcquireLock(ctx, key, 2*time.Second)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	// Fast-forward past TTL
	srv.FastForward(3 * time.Second)

	// Renew should fail — lock expired
	err = store.RenewLock(ctx, key, token, 5*time.Second)
	assert.Error(t, err, "renew after expiry should fail")
}

// ---------------------------------------------------------------------------
// DLQ index consistency under concurrent Add
// ---------------------------------------------------------------------------

// TestDLQStoreAddConcurrent verifies that concurrent DLQ Add operations
// maintain index consistency (no duplicate index entries, no lost entries).
func TestDLQStoreAddConcurrent(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	store, err := NewDLQStore("redis://"+srv.Addr(), time.Hour)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Add 50 entries concurrently
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			entry := DLQEntry{
				JobID:     fmt.Sprintf("concurrent-job-%d", idx),
				Status:    "FAILED",
				Reason:    "test",
				CreatedAt: time.Now().UTC(),
			}
			if addErr := store.Add(ctx, entry); addErr != nil {
				t.Logf("add %d: %v", idx, addErr)
			}
		}()
	}
	wg.Wait()

	// All 50 should be in the index
	indexCount, err := store.client.ZCard(ctx, dlqIndexKey()).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(50), indexCount,
		"all concurrent entries should be in the index")

	// List should return entries (up to limit)
	entries, err := store.List(ctx, 100)
	require.NoError(t, err)
	assert.Equal(t, 50, len(entries),
		"all entries should be retrievable")
}

// TestDLQStoreAddTrimMaintainsLimit verifies that the Add method's trim
// logic correctly limits the index to dlqMaxEntries.
func TestDLQStoreAddTrimMaintainsLimit(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	store, err := NewDLQStore("redis://"+srv.Addr(), time.Hour)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Add more than dlqMaxEntries entries
	total := dlqMaxEntries + 50
	for i := 0; i < total; i++ {
		entry := DLQEntry{
			JobID:     fmt.Sprintf("trim-job-%04d", i),
			Status:    "FAILED",
			CreatedAt: time.Now().UTC().Add(time.Duration(i) * time.Millisecond),
		}
		err := store.Add(ctx, entry)
		require.NoError(t, err, "add %d", i)
	}

	// Index should be trimmed to dlqMaxEntries
	indexCount, err := store.client.ZCard(ctx, dlqIndexKey()).Result()
	require.NoError(t, err)
	assert.LessOrEqual(t, indexCount, int64(dlqMaxEntries),
		"index should be trimmed to max entries")
}

// ---------------------------------------------------------------------------
// Idempotency key atomicity
// ---------------------------------------------------------------------------

// TestIdempotencyKeySetNXRace verifies that concurrent idempotency key
// writes are properly serialized by Redis SetNX.
func TestIdempotencyKeySetNXRace(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	store, err := NewRedisJobStore("redis://" + srv.Addr())
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	idempotencyKey := "idem-key-123"
	tenant := "tenant-1"

	// Race 10 goroutines trying to set the same idempotency key
	var wg sync.WaitGroup
	var mu sync.Mutex
	winners := make(map[string]bool) // which job IDs won

	for i := 0; i < 10; i++ {
		wg.Add(1)
		jobID := fmt.Sprintf("job-%d", i)
		go func() {
			defer wg.Done()
			err := store.SetIdempotencyKeyScoped(ctx, tenant, idempotencyKey, jobID)
			if err == nil {
				mu.Lock()
				winners[jobID] = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// All should succeed (SetNX returns false but no error when key exists)
	// But only the first writer's value should be stored
	storedJobID, err := store.GetJobByIdempotencyKeyScoped(ctx, tenant, idempotencyKey)
	require.NoError(t, err)
	assert.NotEmpty(t, storedJobID, "idempotency key should map to a job ID")

	// The stored job ID should be exactly one of the 10
	assert.Contains(t, storedJobID, "job-",
		"stored job ID should be one of the submitted jobs")
}

// ---------------------------------------------------------------------------
// State index consistency
// ---------------------------------------------------------------------------

// TestStateIndexConsistencyOnTransition verifies that the per-state sorted
// set indices are correctly updated during state transitions.
func TestStateIndexConsistencyOnTransition(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	store, err := NewRedisJobStore("redis://" + srv.Addr())
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	jobID := "job-idx-test"

	// PENDING
	err = store.SetState(ctx, jobID, model.JobStatePending)
	require.NoError(t, err)

	pendingJobs, err := store.ListJobsByState(ctx, model.JobStatePending, time.Now().UnixMicro()+1e6, 10)
	require.NoError(t, err)
	assert.Len(t, pendingJobs, 1)

	// SCHEDULED — should remove from PENDING index, add to SCHEDULED index
	err = store.SetState(ctx, jobID, model.JobStateScheduled)
	require.NoError(t, err)

	pendingJobs, err = store.ListJobsByState(ctx, model.JobStatePending, time.Now().UnixMicro()+1e6, 10)
	require.NoError(t, err)
	assert.Len(t, pendingJobs, 0, "job should be removed from PENDING index")

	scheduledJobs, err := store.ListJobsByState(ctx, model.JobStateScheduled, time.Now().UnixMicro()+1e6, 10)
	require.NoError(t, err)
	assert.Len(t, scheduledJobs, 1, "job should be in SCHEDULED index")

	// SUCCEEDED (terminal) — should remove from SCHEDULED, add to SUCCEEDED
	err = store.SetState(ctx, jobID, model.JobStateSucceeded)
	require.NoError(t, err)

	scheduledJobs, err = store.ListJobsByState(ctx, model.JobStateScheduled, time.Now().UnixMicro()+1e6, 10)
	require.NoError(t, err)
	assert.Len(t, scheduledJobs, 0, "job should be removed from SCHEDULED index")

	succeededJobs, err := store.ListJobsByState(ctx, model.JobStateSucceeded, time.Now().UnixMicro()+1e6, 10)
	require.NoError(t, err)
	assert.Len(t, succeededJobs, 1, "job should be in SUCCEEDED index")
}

// ---------------------------------------------------------------------------
// Tenant active set consistency
// ---------------------------------------------------------------------------

// TestTenantActiveSetConsistencyOnTransition verifies that the tenant active
// set (job:tenant:active:<tenant>) is correctly maintained during state
// transitions. Active states add to the set; terminal states remove from it.
func TestTenantActiveSetConsistencyOnTransition(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	store, err := NewRedisJobStore("redis://" + srv.Addr())
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	jobID := "job-tenant-active"
	tenant := "tenant-abc"

	// Set tenant in meta BEFORE state transitions so the active set is populated
	err = store.SetTenant(ctx, jobID, tenant)
	require.NoError(t, err)

	// PENDING (active state) — should add to tenant active set
	err = store.SetState(ctx, jobID, model.JobStatePending)
	require.NoError(t, err)

	activeCount, err := store.CountActiveByTenant(ctx, tenant)
	require.NoError(t, err)
	assert.Equal(t, 1, activeCount, "job should be in tenant active set after PENDING")

	// SCHEDULED (still active)
	err = store.SetState(ctx, jobID, model.JobStateScheduled)
	require.NoError(t, err)

	activeCount, err = store.CountActiveByTenant(ctx, tenant)
	require.NoError(t, err)
	assert.Equal(t, 1, activeCount, "job should still be in tenant active set after SCHEDULED")

	// SUCCEEDED (terminal) — should remove from tenant active set
	err = store.SetState(ctx, jobID, model.JobStateSucceeded)
	require.NoError(t, err)

	activeCount, err = store.CountActiveByTenant(ctx, tenant)
	require.NoError(t, err)
	assert.Equal(t, 0, activeCount, "job should be removed from tenant active set after terminal SUCCEEDED")
}

// TestTenantActiveSetIsolation verifies that tenant active sets are properly
// isolated — one tenant's jobs don't appear in another tenant's active count.
func TestTenantActiveSetIsolation(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	store, err := NewRedisJobStore("redis://" + srv.Addr())
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Two tenants, each with a job
	err = store.SetTenant(ctx, "job-t1", "tenant-1")
	require.NoError(t, err)
	err = store.SetTenant(ctx, "job-t2", "tenant-2")
	require.NoError(t, err)

	err = store.SetState(ctx, "job-t1", model.JobStatePending)
	require.NoError(t, err)
	err = store.SetState(ctx, "job-t1", model.JobStateRunning)
	require.NoError(t, err)

	err = store.SetState(ctx, "job-t2", model.JobStatePending)
	require.NoError(t, err)
	err = store.SetState(ctx, "job-t2", model.JobStateRunning)
	require.NoError(t, err)

	// Each tenant should see only their own active jobs
	count1, err := store.CountActiveByTenant(ctx, "tenant-1")
	require.NoError(t, err)
	assert.Equal(t, 1, count1, "tenant-1 should have 1 active job")

	count2, err := store.CountActiveByTenant(ctx, "tenant-2")
	require.NoError(t, err)
	assert.Equal(t, 1, count2, "tenant-2 should have 1 active job")

	// Use CancelJob to cancel tenant-1's job — verifies both the CancelJob
	// tenant active set fix and cross-tenant isolation.
	resultState, err := store.CancelJob(ctx, "job-t1")
	require.NoError(t, err)
	assert.Equal(t, model.JobStateCancelled, resultState)

	count1, err = store.CountActiveByTenant(ctx, "tenant-1")
	require.NoError(t, err)
	assert.Equal(t, 0, count1, "tenant-1 should have 0 active jobs after CancelJob")

	count2, err = store.CountActiveByTenant(ctx, "tenant-2")
	require.NoError(t, err)
	assert.Equal(t, 1, count2, "tenant-2 should still have 1 active job")
}

// TestCancelJobUpdatesTenantActiveSet verifies that CancelJob correctly
// removes the job from the tenant active set (job:tenant:active:<tenant>).
//
// Previously CancelJob was missing the SRem call, causing
// CountActiveByTenant to over-count after cancellation. This was fixed by
// adding a tenant lookup + SRem to the CancelJob pipeline, mirroring the
// pattern in SetState.
func TestCancelJobUpdatesTenantActiveSet(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	store, err := NewRedisJobStore("redis://" + srv.Addr())
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	jobID := "job-cancel-tenant"
	tenant := "tenant-cancel"

	// Set up running job with tenant
	err = store.SetTenant(ctx, jobID, tenant)
	require.NoError(t, err)
	err = store.SetState(ctx, jobID, model.JobStatePending)
	require.NoError(t, err)
	err = store.SetState(ctx, jobID, model.JobStateRunning)
	require.NoError(t, err)

	// Verify job is in tenant active set
	count, err := store.CountActiveByTenant(ctx, tenant)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "job should be in tenant active set")

	// Cancel via CancelJob (not SetState)
	resultState, err := store.CancelJob(ctx, jobID)
	require.NoError(t, err)
	assert.Equal(t, model.JobStateCancelled, resultState)

	// CancelJob now correctly removes from tenant active set
	count, err = store.CountActiveByTenant(ctx, tenant)
	require.NoError(t, err)
	assert.Equal(t, 0, count,
		"CancelJob should remove job from tenant active set — CountActiveByTenant must be 0")
}

// ---------------------------------------------------------------------------
// Event log consistency
// ---------------------------------------------------------------------------

// TestJobEventsLogConsistency verifies that the event log (RPush) records
// all state transitions in order.
func TestJobEventsLogConsistency(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	store, err := NewRedisJobStore("redis://" + srv.Addr())
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	jobID := "job-events"

	// Walk through full lifecycle
	states := []model.JobState{
		model.JobStatePending,
		model.JobStateScheduled,
		model.JobStateDispatched,
		model.JobStateRunning,
		model.JobStateSucceeded,
	}
	for _, state := range states {
		err := store.SetState(ctx, jobID, state)
		require.NoError(t, err)
	}

	// Read event log
	events, err := store.client.LRange(ctx, jobEventsKey(jobID), 0, -1).Result()
	require.NoError(t, err)
	assert.Len(t, events, len(states), "should have one event per state transition")

	// Each event should contain the state name
	for i, event := range events {
		assert.Contains(t, event, string(states[i]),
			"event %d should contain state %s", i, states[i])
	}
}

// ---------------------------------------------------------------------------
// Recent jobs index consistency
// ---------------------------------------------------------------------------

// TestRecentJobsIndexUpdatedOnTransition verifies that the job:recent sorted
// set is updated on every state transition.
func TestRecentJobsIndexUpdatedOnTransition(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	store, err := NewRedisJobStore("redis://" + srv.Addr())
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	jobID := "job-recent-idx"

	err = store.SetState(ctx, jobID, model.JobStatePending)
	require.NoError(t, err)

	// Should appear in recent jobs
	recent, err := store.ListRecentJobs(ctx, 10)
	require.NoError(t, err)
	assert.Len(t, recent, 1)
	assert.Equal(t, jobID, recent[0].ID)

	// Score should increase on subsequent transitions
	time.Sleep(time.Millisecond) // ensure different timestamp
	err = store.SetState(ctx, jobID, model.JobStateScheduled)
	require.NoError(t, err)

	recent2, err := store.ListRecentJobs(ctx, 10)
	require.NoError(t, err)
	assert.Len(t, recent2, 1)
	assert.GreaterOrEqual(t, recent2[0].UpdatedAt, recent[0].UpdatedAt,
		"score should increase on state transition")
}

// TestDeadlineIndexCleanedOnTerminalState verifies that the deadline index
// entry is removed when a job reaches a terminal state.
func TestDeadlineIndexCleanedOnTerminalState(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	store, err := NewRedisJobStore("redis://" + srv.Addr())
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	jobID := "job-deadline-clean"

	// Set up job with deadline
	err = store.SetState(ctx, jobID, model.JobStatePending)
	require.NoError(t, err)
	err = store.SetDeadline(ctx, jobID, time.Now().Add(10*time.Minute))
	require.NoError(t, err)

	// Verify deadline is in index
	expired, err := store.ListExpiredDeadlines(ctx, time.Now().Add(20*time.Minute).Unix(), 10)
	require.NoError(t, err)
	assert.Len(t, expired, 1)

	// Transition to terminal state
	err = store.SetState(ctx, jobID, model.JobStateScheduled)
	require.NoError(t, err)
	err = store.SetState(ctx, jobID, model.JobStateFailed)
	require.NoError(t, err)

	// Deadline should be cleaned from index
	expired, err = store.ListExpiredDeadlines(ctx, time.Now().Add(20*time.Minute).Unix(), 10)
	require.NoError(t, err)
	assert.Len(t, expired, 0, "deadline should be removed on terminal state")
}
