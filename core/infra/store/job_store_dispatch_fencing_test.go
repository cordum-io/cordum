package store

// Behavioral evidence for task-a13f83fa step-10: atomic dispatch/attempt
// fencing must reject late/wrong-attempt/wrong-worker events BEFORE any
// caller mutates job state, and must never apply the same event twice.

import (
	"context"
	"sync"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/model"
)

func newDispatchFencingTestStore(t *testing.T) *RedisJobStore {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	t.Cleanup(srv.Close)
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestBeginDispatch_RequiresExistingJob(t *testing.T) {
	store := newDispatchFencingTestStore(t)
	_, _, err := store.BeginDispatch(context.Background(), "job-does-not-exist", "worker-1", "tenant-a")
	if err == nil {
		t.Fatalf("BeginDispatch succeeded for a job that was never submitted")
	}
}

func TestBeginDispatch_AssignsFreshIdentityAndMonotonicAttempt(t *testing.T) {
	store := newDispatchFencingTestStore(t)
	ctx := context.Background()
	jobID := "job-1"
	if err := store.SetState(ctx, jobID, model.JobStatePending); err != nil {
		t.Fatalf("SetState: %v", err)
	}

	dispatch1, attempt1, err := store.BeginDispatch(ctx, jobID, "worker-1", "tenant-a")
	if err != nil {
		t.Fatalf("BeginDispatch (1st): %v", err)
	}
	if dispatch1 == "" || attempt1 != 1 {
		t.Fatalf("first dispatch = (%q, %d), want (non-empty, 1)", dispatch1, attempt1)
	}

	dispatch2, attempt2, err := store.BeginDispatch(ctx, jobID, "worker-2", "tenant-a")
	if err != nil {
		t.Fatalf("BeginDispatch (2nd): %v", err)
	}
	if dispatch2 == dispatch1 {
		t.Fatalf("second dispatch reused the same dispatch_id: %q", dispatch2)
	}
	if attempt2 != 2 {
		t.Fatalf("second dispatch attempt = %d, want 2 (monotonic)", attempt2)
	}
}

func TestAcceptJobEvent_RejectsStaleAttemptBeforeSideEffects(t *testing.T) {
	store := newDispatchFencingTestStore(t)
	ctx := context.Background()
	jobID := "job-1"
	if err := store.SetState(ctx, jobID, model.JobStatePending); err != nil {
		t.Fatalf("SetState: %v", err)
	}

	staleDispatch, staleAttempt, err := store.BeginDispatch(ctx, jobID, "worker-1", "tenant-a")
	if err != nil {
		t.Fatalf("BeginDispatch (stale): %v", err)
	}
	currentDispatch, currentAttempt, err := store.BeginDispatch(ctx, jobID, "worker-1", "tenant-a")
	if err != nil {
		t.Fatalf("BeginDispatch (current): %v", err)
	}

	// A late event from the STALE attempt must be rejected.
	accepted, err := store.AcceptJobEvent(ctx, jobID, staleDispatch, staleAttempt, "worker-1", "tenant-a", "msg-1")
	if err != nil {
		t.Fatalf("AcceptJobEvent (stale): %v", err)
	}
	if accepted {
		t.Fatalf("AcceptJobEvent accepted a stale-attempt event")
	}

	// The CURRENT attempt's event must be accepted.
	accepted, err = store.AcceptJobEvent(ctx, jobID, currentDispatch, currentAttempt, "worker-1", "tenant-a", "msg-2")
	if err != nil {
		t.Fatalf("AcceptJobEvent (current): %v", err)
	}
	if !accepted {
		t.Fatalf("AcceptJobEvent rejected the current attempt's event")
	}
}

func TestAcceptJobEvent_RejectsWrongWorkerAndWrongTenant(t *testing.T) {
	store := newDispatchFencingTestStore(t)
	ctx := context.Background()
	jobID := "job-1"
	if err := store.SetState(ctx, jobID, model.JobStatePending); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	dispatchID, attempt, err := store.BeginDispatch(ctx, jobID, "worker-1", "tenant-a")
	if err != nil {
		t.Fatalf("BeginDispatch: %v", err)
	}

	if accepted, err := store.AcceptJobEvent(ctx, jobID, dispatchID, attempt, "worker-EVIL", "tenant-a", "msg-1"); err != nil || accepted {
		t.Fatalf("AcceptJobEvent(wrong worker) = (%v, %v), want (false, nil)", accepted, err)
	}
	if accepted, err := store.AcceptJobEvent(ctx, jobID, dispatchID, attempt, "worker-1", "tenant-EVIL", "msg-1"); err != nil || accepted {
		t.Fatalf("AcceptJobEvent(wrong tenant) = (%v, %v), want (false, nil)", accepted, err)
	}
	if accepted, err := store.AcceptJobEvent(ctx, jobID, "wrong-dispatch-id", attempt, "worker-1", "tenant-a", "msg-1"); err != nil || accepted {
		t.Fatalf("AcceptJobEvent(wrong dispatch id) = (%v, %v), want (false, nil)", accepted, err)
	}
}

func TestAcceptJobEvent_IdenticalRedeliveryIsIdempotentNotDoubleApplied(t *testing.T) {
	store := newDispatchFencingTestStore(t)
	ctx := context.Background()
	jobID := "job-1"
	if err := store.SetState(ctx, jobID, model.JobStatePending); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	dispatchID, attempt, err := store.BeginDispatch(ctx, jobID, "worker-1", "tenant-a")
	if err != nil {
		t.Fatalf("BeginDispatch: %v", err)
	}

	first, err := store.AcceptJobEvent(ctx, jobID, dispatchID, attempt, "worker-1", "tenant-a", "msg-1")
	if err != nil || !first {
		t.Fatalf("first AcceptJobEvent = (%v, %v), want (true, nil)", first, err)
	}
	// Exact same message-id redelivered: must NOT be accepted a second time
	// (the caller must treat this as already-applied, never mutate twice).
	second, err := store.AcceptJobEvent(ctx, jobID, dispatchID, attempt, "worker-1", "tenant-a", "msg-1")
	if err != nil {
		t.Fatalf("second AcceptJobEvent: %v", err)
	}
	if second {
		t.Fatalf("AcceptJobEvent accepted the identical message id twice")
	}
	// A DIFFERENT message id in the SAME attempt must still be accepted.
	third, err := store.AcceptJobEvent(ctx, jobID, dispatchID, attempt, "worker-1", "tenant-a", "msg-2")
	if err != nil || !third {
		t.Fatalf("third AcceptJobEvent (new message id) = (%v, %v), want (true, nil)", third, err)
	}
}

func TestAcceptJobEvent_RequiresAllFields(t *testing.T) {
	store := newDispatchFencingTestStore(t)
	if _, err := store.AcceptJobEvent(context.Background(), "", "d", 1, "w", "t", "m"); err == nil {
		t.Fatalf("AcceptJobEvent accepted an empty jobID")
	}
}

// TestAcceptJobEvent_ConcurrentIdenticalMessageIDHasExactlyOneWinner proves
// the dedupe is truly atomic under real concurrency: N goroutines racing to
// accept the SAME message-id must produce exactly one winner, never zero
// (lost update) and never more than one (double-applied side effect).
func TestAcceptJobEvent_ConcurrentIdenticalMessageIDHasExactlyOneWinner(t *testing.T) {
	store := newDispatchFencingTestStore(t)
	ctx := context.Background()
	jobID := "job-race"
	if err := store.SetState(ctx, jobID, model.JobStatePending); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	dispatchID, attempt, err := store.BeginDispatch(ctx, jobID, "worker-1", "tenant-a")
	if err != nil {
		t.Fatalf("BeginDispatch: %v", err)
	}

	const n = 20
	wins := make(chan bool, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			accepted, err := store.AcceptJobEvent(ctx, jobID, dispatchID, attempt, "worker-1", "tenant-a", "race-msg")
			if err != nil {
				t.Errorf("AcceptJobEvent: %v", err)
				return
			}
			wins <- accepted
		}()
	}
	wg.Wait()
	close(wins)

	var winCount int
	for accepted := range wins {
		if accepted {
			winCount++
		}
	}
	if winCount != 1 {
		t.Fatalf("expected exactly 1 winner for the identical message id under concurrency, got %d", winCount)
	}
}

// TestBeginDispatch_ConcurrentAttemptsAreAllUnique proves N concurrent
// BeginDispatch calls for the same job each get a unique dispatch_id and the
// attempt counter reaches exactly N (no lost increments under a race).
func TestBeginDispatch_ConcurrentAttemptsAreAllUnique(t *testing.T) {
	store := newDispatchFencingTestStore(t)
	ctx := context.Background()
	jobID := "job-race-dispatch"
	if err := store.SetState(ctx, jobID, model.JobStatePending); err != nil {
		t.Fatalf("SetState: %v", err)
	}

	const n = 20
	dispatchIDs := make(chan string, n)
	attempts := make(chan int, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			dispatchID, attempt, err := store.BeginDispatch(ctx, jobID, "worker-1", "tenant-a")
			if err != nil {
				t.Errorf("BeginDispatch: %v", err)
				return
			}
			dispatchIDs <- dispatchID
			attempts <- attempt
		}()
	}
	wg.Wait()
	close(dispatchIDs)
	close(attempts)

	seenIDs := map[string]bool{}
	for id := range dispatchIDs {
		if seenIDs[id] {
			t.Fatalf("duplicate dispatch_id assigned under concurrency: %q", id)
		}
		seenIDs[id] = true
	}
	if len(seenIDs) != n {
		t.Fatalf("expected %d unique dispatch ids, got %d", n, len(seenIDs))
	}

	seenAttempts := map[int]bool{}
	for a := range attempts {
		if seenAttempts[a] {
			t.Fatalf("duplicate attempt number assigned under concurrency: %d (lost increment)", a)
		}
		seenAttempts[a] = true
	}
	if len(seenAttempts) != n {
		t.Fatalf("expected %d unique monotonic attempts, got %d (lost increment under concurrency)", n, len(seenAttempts))
	}
}
