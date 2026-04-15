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
)

// ---- helpers ----

// failNBus fails the first N Publish calls, then succeeds.
type failNBus struct {
	mu        sync.Mutex
	callCount int
	failCount int
	published []publishedMsg
}

func (b *failNBus) Publish(subject string, packet *pb.BusPacket) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.callCount++
	if b.callCount <= b.failCount {
		return errors.New("bus unavailable")
	}
	b.published = append(b.published, publishedMsg{subject: subject, packet: packet})
	return nil
}

func (b *failNBus) Subscribe(string, string, func(*pb.BusPacket) error) error { return nil }

type dlqSinkSpy struct {
	mu        sync.Mutex
	entries   []DLQEntry
	callCount int
	failCount int
}

func (s *dlqSinkSpy) Add(_ context.Context, entry DLQEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callCount++
	if s.callCount <= s.failCount {
		return errors.New("dlq sink unavailable")
	}
	s.entries = append(s.entries, entry)
	return nil
}

// slowSafety blocks for the given duration before returning.
type slowSafety struct {
	delay time.Duration
}

func (s *slowSafety) Check(ctx context.Context, _ *pb.JobRequest) (SafetyDecisionRecord, error) {
	select {
	case <-time.After(s.delay):
		return SafetyDecisionRecord{Decision: SafetyAllow}, nil
	case <-ctx.Done():
		return SafetyDecisionRecord{}, ctx.Err()
	}
}

// dlqSpy tracks IncDLQEmitFailure calls.
type dlqSpy struct {
	spyMetrics
	dlqFails atomic.Int64
}

func newDLQSpy() *dlqSpy {
	return &dlqSpy{spyMetrics: spyMetrics{orphanReplayed: map[string]int{}}}
}

func (m *dlqSpy) IncDLQEmitFailure(string) {
	m.dlqFails.Add(1)
}

// ---- tests ----

func TestAtomicOutputSafetyEnabled(t *testing.T) {
	store := newFakeJobStore()
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)

	// Concurrent writes should not race (verified by -count=3).
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			engine.outputSafetyEnabled.Store(true)
		}()
		go func() {
			defer wg.Done()
			_ = engine.outputSafetyEnabled.Load()
		}()
	}
	wg.Wait()

	engine.outputSafetyEnabled.Store(true)
	if !engine.outputSafetyEnabled.Load() {
		t.Fatal("expected outputSafetyEnabled to be true")
	}
	engine.outputSafetyEnabled.Store(false)
	if engine.outputSafetyEnabled.Load() {
		t.Fatal("expected outputSafetyEnabled to be false")
	}
}

func TestReconcilerScheduledTimeout(t *testing.T) {
	store := newFakeReconcileStore()

	// Seed a SCHEDULED job with an old timestamp.
	store.states["sched-old"] = JobStateScheduled
	store.updated["sched-old"] = toUnixMicros(time.Now().Add(-5 * time.Minute))

	// Fresh SCHEDULED job should not be touched.
	store.states["sched-fresh"] = JobStateScheduled
	store.updated["sched-fresh"] = toUnixMicros(time.Now())

	reconciler := NewReconciler(store, 1*time.Minute, 1*time.Minute, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go reconciler.Start(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s, _ := store.GetState(context.Background(), "sched-old")
		if s == JobStateTimeout {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	if state, _ := store.GetState(context.Background(), "sched-old"); state != JobStateTimeout {
		t.Fatalf("expected sched-old to be TIMEOUT, got %s", state)
	}
	if state, _ := store.GetState(context.Background(), "sched-fresh"); state != JobStateScheduled {
		t.Fatalf("expected sched-fresh to remain SCHEDULED, got %s", state)
	}
}

func TestDLQEmitRetrySucceedsOnSecondAttempt(t *testing.T) {
	bus := &failNBus{failCount: 1} // first Publish fails, second succeeds
	store := newFakeJobStore()
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)

	err := engine.emitDLQWithRetry("job-retry", "job.test", pb.JobStatus_JOB_STATUS_FAILED, "test", "test_code")
	if err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	bus.mu.Lock()
	if len(bus.published) != 1 {
		t.Fatalf("expected 1 successful publish after retry, got %d", len(bus.published))
	}
	bus.mu.Unlock()
}

func TestDLQEmitRetryPermanentFailureMetric(t *testing.T) {
	bus := &failNBus{failCount: 999} // always fail
	store := newFakeJobStore()
	metrics := newDLQSpy()
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)
	engine.metrics = metrics

	err := engine.emitDLQWithRetry("job-perm", "job.test", pb.JobStatus_JOB_STATUS_FAILED, "test", "test_code")
	if err == nil {
		t.Fatal("expected permanent failure")
	}
	if got := metrics.dlqFails.Load(); got != 1 {
		t.Fatalf("expected IncDLQEmitFailure called once, got %d", got)
	}
}

func TestDLQEmitPersistsToSinkWhenBusUnavailable(t *testing.T) {
	bus := &failNBus{failCount: 999} // always fail
	store := newFakeJobStore()
	sink := &dlqSinkSpy{}
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil).WithDLQSink(sink)

	err := engine.emitDLQWithRetry("job-denied", "job.test", pb.JobStatus_JOB_STATUS_DENIED, "blocked", "safety_denied")
	if err == nil {
		t.Fatal("expected publish failure with unavailable bus")
	}

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.entries) == 0 {
		t.Fatal("expected DLQ sink to persist entry even when bus publish fails")
	}
	entry := sink.entries[0]
	if entry.JobID != "job-denied" {
		t.Fatalf("expected job id job-denied, got %s", entry.JobID)
	}
	if entry.Status != pb.JobStatus_JOB_STATUS_DENIED.String() {
		t.Fatalf("expected status %s, got %s", pb.JobStatus_JOB_STATUS_DENIED.String(), entry.Status)
	}
	if entry.ReasonCode != "safety_denied" {
		t.Fatalf("expected reason code safety_denied, got %s", entry.ReasonCode)
	}
}

func TestSafetyCheckDefenseTimeout(t *testing.T) {
	store := newFakeJobStore()
	slow := &slowSafety{delay: 5 * time.Second}
	engine := NewEngine(&fakeBus{}, slow, newTestRegistry(t), NewNaiveStrategy(), store, nil)

	start := time.Now()
	record, err := engine.checkSafetyDecision(&pb.JobRequest{
		JobId:    "job-timeout",
		Topic:    "job.test",
		TenantId: "default",
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	if record.Decision != SafetyUnavailable {
		t.Fatalf("expected SafetyUnavailable, got %s", record.Decision)
	}
	// Should complete in ~3s (safetyCheckTimeout), not 5s.
	if elapsed > 4*time.Second {
		t.Fatalf("timeout took too long: %s (expected ~3s)", elapsed)
	}
}

// ctxTrackingSafety records whether the context was cancelled during Check.
type ctxTrackingSafety struct {
	ctxCancelled atomic.Bool
	delay        time.Duration
}

func (s *ctxTrackingSafety) Check(ctx context.Context, _ *pb.JobRequest) (SafetyDecisionRecord, error) {
	select {
	case <-time.After(s.delay):
		return SafetyDecisionRecord{Decision: SafetyAllow}, nil
	case <-ctx.Done():
		s.ctxCancelled.Store(true)
		return SafetyDecisionRecord{}, ctx.Err()
	}
}

func TestSafetyCheck_TimeoutCancelsContext(t *testing.T) {
	store := newFakeJobStore()
	tracker := &ctxTrackingSafety{delay: 10 * time.Second}
	engine := NewEngine(&fakeBus{}, tracker, newTestRegistry(t), NewNaiveStrategy(), store, nil)

	start := time.Now()
	record, _ := engine.checkSafetyDecision(&pb.JobRequest{
		JobId:    "job-ctx-cancel",
		Topic:    "job.test",
		TenantId: "default",
	})
	elapsed := time.Since(start)

	if record.Decision != SafetyUnavailable {
		t.Fatalf("expected SafetyUnavailable, got %s", record.Decision)
	}
	// The timeout should have fired (~3s), not waited for the full 10s delay.
	if elapsed > 5*time.Second {
		t.Fatalf("timeout took too long: %s", elapsed)
	}
	// The context passed to Check should have been cancelled, ending the goroutine.
	// Give a small window for the goroutine to observe cancellation.
	time.Sleep(50 * time.Millisecond)
	if !tracker.ctxCancelled.Load() {
		t.Fatal("expected context cancellation to propagate to safety checker (goroutine leak)")
	}
}

// failReleaseLockStore wraps fakeJobStore but fails ReleaseLock the first N times.
type failReleaseLockStore struct {
	*fakeJobStore
	mu           sync.Mutex
	releaseCount int
	failCount    int
}

func (s *failReleaseLockStore) ReleaseLock(ctx context.Context, key string, token string) error {
	s.mu.Lock()
	s.releaseCount++
	n := s.releaseCount
	s.mu.Unlock()
	if n <= s.failCount {
		return errors.New("redis connection refused")
	}
	return s.fakeJobStore.ReleaseLock(ctx, key, token)
}

func TestWithJobLockReleaseRetry(t *testing.T) {
	store := &failReleaseLockStore{
		fakeJobStore: newFakeJobStore(),
		failCount:    1, // first ReleaseLock fails, second succeeds
	}
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)

	called := false
	err := engine.withJobLock("job-retry-release", 30*time.Second, func(context.Context) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("withJobLock should not fail: %v", err)
	}
	if !called {
		t.Fatal("expected fn to be called")
	}

	// Wait a moment for the deferred release goroutine to complete.
	time.Sleep(100 * time.Millisecond)

	// Lock should have been released on the retry (second call).
	store.mu.Lock()
	count := store.releaseCount
	store.mu.Unlock()
	if count < 2 {
		t.Fatalf("expected at least 2 ReleaseLock calls (first fails, second retries), got %d", count)
	}

	// The lock should no longer be held.
	store.fakeJobStore.mu.RLock()
	_, locked := store.locks[jobLockKey("job-retry-release")]
	store.fakeJobStore.mu.RUnlock()
	if locked {
		t.Fatal("expected lock to be released after retry")
	}
}

func TestWithJobLockReleaseUsesBackgroundContext(t *testing.T) {
	store := newFakeJobStore()
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)

	// Cancel the engine context before the deferred release runs.
	engine.cancel()

	called := false
	// withJobLock acquires using e.ctx, which is cancelled, so TryAcquireLock
	// should fail. But if we pre-seed the lock, we can test the release path.
	// Instead, just verify that a normal flow still works when engine is stopped
	// mid-operation by checking that the fn still executes.

	// Create a fresh engine, run fn, then cancel — the deferred release
	// should still succeed because it uses context.Background().
	engine2 := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)
	err := engine2.withJobLock("job-bg-ctx", 30*time.Second, func(context.Context) error {
		// Cancel the engine context while fn is running.
		engine2.cancel()
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("withJobLock should succeed: %v", err)
	}
	if !called {
		t.Fatal("expected fn to be called")
	}

	// Wait for deferred release.
	time.Sleep(100 * time.Millisecond)

	// Lock should be released despite engine context cancellation.
	store.mu.RLock()
	_, locked := store.locks[jobLockKey("job-bg-ctx")]
	store.mu.RUnlock()
	if locked {
		t.Fatal("expected lock to be released even after engine context cancelled")
	}
}

func TestWithJobLockRenewalKeepsLockAlive(t *testing.T) {
	store := newFakeJobStore()
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)

	// Use a short TTL so the renewal goroutine must fire to keep the lock alive.
	ttl := 150 * time.Millisecond

	var fnDone atomic.Bool
	err := engine.withJobLock("job-renewal", ttl, func(context.Context) error {
		// Sleep longer than the TTL. Without renewal, the lock would expire.
		time.Sleep(400 * time.Millisecond)
		fnDone.Store(true)
		return nil
	})
	if err != nil {
		t.Fatalf("withJobLock should succeed: %v", err)
	}
	if !fnDone.Load() {
		t.Fatal("expected fn to complete")
	}

	// Verify lock was released after fn completed.
	store.mu.RLock()
	_, locked := store.locks[jobLockKey("job-renewal")]
	store.mu.RUnlock()
	if locked {
		t.Fatal("expected lock to be released after fn")
	}
}

func TestWithJobLockRenewalStopsOnCompletion(t *testing.T) {
	// Track renewal calls via a counting store.
	store := &renewCountStore{fakeJobStore: newFakeJobStore()}
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)

	ttl := 90 * time.Millisecond // renewal at ttl/3 = 30ms
	err := engine.withJobLock("job-renew-stop", ttl, func(context.Context) error {
		// Return immediately.
		return nil
	})
	if err != nil {
		t.Fatalf("withJobLock should succeed: %v", err)
	}

	// Record how many renewals have occurred so far.
	countAfterFn := store.renewCount.Load()

	// Wait well past the renewal interval. No new renewals should happen.
	time.Sleep(200 * time.Millisecond)
	countLater := store.renewCount.Load()
	if countLater > countAfterFn {
		t.Fatalf("expected no renewals after fn completed, but got %d more", countLater-countAfterFn)
	}
}

// renewCountStore wraps fakeJobStore and counts RenewLock calls.
type renewCountStore struct {
	*fakeJobStore
	renewCount atomic.Int32
}

func (s *renewCountStore) RenewLock(ctx context.Context, key, token string, ttl time.Duration) error {
	s.renewCount.Add(1)
	return s.fakeJobStore.RenewLock(ctx, key, token, ttl)
}

// alwaysFailRenewStore wraps fakeJobStore and always fails RenewLock.
type alwaysFailRenewStore struct {
	*fakeJobStore
	renewCount atomic.Int32
}

func (s *alwaysFailRenewStore) RenewLock(_ context.Context, _, _ string, _ time.Duration) error {
	s.renewCount.Add(1)
	return errors.New("redis connection refused")
}

func TestWithJobLock_RenewalAbandonAfterConsecutiveFailures(t *testing.T) {
	store := &alwaysFailRenewStore{fakeJobStore: newFakeJobStore()}
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)

	ttl := 150 * time.Millisecond // renewal every 50ms

	var ctxCancelled atomic.Bool
	err := engine.withJobLock("job-abandon", ttl, func(lockCtx context.Context) error {
		// Wait for the fenced context to be cancelled (lock abandonment).
		<-lockCtx.Done()
		ctxCancelled.Store(true)
		// Return nil — withJobLock itself must still return errLockAbandoned.
		return nil
	})

	// withJobLock must return errLockAbandoned when lock renewal fails,
	// even if fn returns nil.
	if !errors.Is(err, errLockAbandoned) {
		t.Fatalf("expected errLockAbandoned, got: %v", err)
	}
	if !ctxCancelled.Load() {
		t.Fatal("expected fenced context to be cancelled on abandonment")
	}

	// After 3 consecutive failures the goroutine should have stopped.
	countAtReturn := store.renewCount.Load()
	time.Sleep(200 * time.Millisecond)
	countLater := store.renewCount.Load()

	if countAtReturn < 3 {
		t.Fatalf("expected at least 3 renewal attempts, got %d", countAtReturn)
	}
	if countLater > countAtReturn {
		t.Fatalf("expected no renewals after abandon, but got %d more", countLater-countAtReturn)
	}
	if countAtReturn != 3 {
		t.Fatalf("expected exactly 3 renewal attempts (abandon on 3rd), got %d", countAtReturn)
	}

	// Lock should NOT be released after abandonment — we no longer own it.
	store.mu.RLock()
	_, locked := store.locks[jobLockKey("job-abandon")]
	store.mu.RUnlock()
	if !locked {
		t.Fatal("expected lock to remain (skip release after abandonment)")
	}
}

// intermittentRenewStore fails on odd-numbered calls, succeeds on even.
type intermittentRenewStore struct {
	*fakeJobStore
	renewCount atomic.Int32
}

func (s *intermittentRenewStore) RenewLock(ctx context.Context, key, token string, ttl time.Duration) error {
	n := s.renewCount.Add(1)
	if n%2 == 1 { // odd calls fail
		return errors.New("redis timeout")
	}
	return s.fakeJobStore.RenewLock(ctx, key, token, ttl)
}

func TestWithJobLock_RenewalIntermittentFailureNoAbandon(t *testing.T) {
	store := &intermittentRenewStore{fakeJobStore: newFakeJobStore()}
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)

	ttl := 90 * time.Millisecond // renewal every 30ms

	var fnDone atomic.Bool
	err := engine.withJobLock("job-intermittent", ttl, func(context.Context) error {
		// Sleep long enough for 6+ renewal ticks.
		time.Sleep(300 * time.Millisecond)
		fnDone.Store(true)
		return nil
	})
	if err != nil {
		t.Fatalf("withJobLock should succeed: %v", err)
	}
	if !fnDone.Load() {
		t.Fatal("expected fn to complete")
	}

	// With alternating fail/succeed, consecutive failures never reach 3.
	// The goroutine should still be running throughout fn execution,
	// meaning we should have more than 3 total renewal attempts.
	count := store.renewCount.Load()
	if count < 6 {
		t.Fatalf("expected at least 6 renewal attempts (intermittent), got %d", count)
	}

	// Lock should be released.
	store.mu.RLock()
	_, locked := store.locks[jobLockKey("job-intermittent")]
	store.mu.RUnlock()
	if locked {
		t.Fatal("expected lock to be released")
	}
}

// TestWithJobLock_ConcurrentAbandonedFlagSafe verifies that the abandoned
// flag (now atomic.Bool) does not race when multiple goroutines concurrently
// call withJobLock with failing renewals. This test would trigger a data race
// under the race detector if the flag were a bare bool.
func TestWithJobLock_ConcurrentAbandonedFlagSafe(t *testing.T) {
	const concurrency = 50
	var wg sync.WaitGroup
	var abandonCount atomic.Int32

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			store := &alwaysFailRenewStore{fakeJobStore: newFakeJobStore()}
			engine := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)

			ttl := 30 * time.Millisecond // renewal every 10ms, fast failure
			err := engine.withJobLock(fmt.Sprintf("job-race-%d", id), ttl, func(lockCtx context.Context) error {
				<-lockCtx.Done()
				return nil
			})

			if errors.Is(err, errLockAbandoned) {
				abandonCount.Add(1)
			}
		}(i)
	}
	wg.Wait()

	// All jobs should have been abandoned due to always-failing renewals
	if got := abandonCount.Load(); got != concurrency {
		t.Fatalf("expected %d abandoned jobs, got %d", concurrency, got)
	}
}

func TestLockRenewalGoroutine_StopsOnEngineShutdown(t *testing.T) {
	store := &renewCountStore{fakeJobStore: newFakeJobStore()}
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)

	ttl := 90 * time.Millisecond // renewal at ttl/3 = 30ms

	// Start a lock that takes a while (simulating work in progress).
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- engine.withJobLock("job-shutdown-test", ttl, func(ctx context.Context) error {
			close(started)
			// Wait for context cancellation (engine shutdown) or timeout.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
				return errors.New("timeout — engine shutdown didn't cancel context")
			}
		})
	}()

	// Wait for the lock to be acquired and renewal goroutine to start.
	<-started
	time.Sleep(50 * time.Millisecond) // let at least one renewal fire
	if engine.ActiveRenewals() < 1 {
		t.Fatal("expected at least 1 active renewal goroutine")
	}

	// Shutdown the engine — should cancel e.ctx and stop renewal goroutines.
	engine.Stop()

	// Verify renewal goroutine exited.
	time.Sleep(100 * time.Millisecond)
	if got := engine.ActiveRenewals(); got != 0 {
		t.Fatalf("expected 0 active renewals after shutdown, got %d", got)
	}

	// Collect the lock result (should be context cancellation or lock-abandoned).
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, errLockAbandoned) {
		t.Logf("lock function returned (expected on shutdown): %v", err)
	}
}

func TestActiveRenewals_ReturnsToZeroAfterLock(t *testing.T) {
	store := newFakeJobStore()
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)

	if got := engine.ActiveRenewals(); got != 0 {
		t.Fatalf("expected 0 active renewals initially, got %d", got)
	}

	// withJobLock starts a renewal goroutine, which should exit after fn returns.
	err := engine.withJobLock("job-counter-test", 90*time.Millisecond, func(context.Context) error {
		// Give the goroutine scheduler time to start the renewal goroutine.
		time.Sleep(10 * time.Millisecond)
		if engine.ActiveRenewals() < 1 {
			t.Fatal("expected active renewal during lock hold")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("withJobLock: %v", err)
	}

	// After withJobLock returns, the renewal goroutine should have exited.
	if got := engine.ActiveRenewals(); got != 0 {
		t.Fatalf("expected 0 active renewals after lock release, got %d", got)
	}
}
