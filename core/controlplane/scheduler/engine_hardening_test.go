package scheduler

import (
	"context"
	"errors"
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

func (s *slowSafety) Check(_ *pb.JobRequest) (SafetyDecisionRecord, error) {
	time.Sleep(s.delay)
	return SafetyDecisionRecord{Decision: SafetyAllow}, nil
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
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), NewMemoryRegistry(), NewNaiveStrategy(), store, nil)

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
	engine := NewEngine(bus, NewSafetyBasic(), NewMemoryRegistry(), NewNaiveStrategy(), store, nil)

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
	engine := NewEngine(bus, NewSafetyBasic(), NewMemoryRegistry(), NewNaiveStrategy(), store, nil)
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
	engine := NewEngine(bus, NewSafetyBasic(), NewMemoryRegistry(), NewNaiveStrategy(), store, nil).WithDLQSink(sink)

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
	engine := NewEngine(&fakeBus{}, slow, NewMemoryRegistry(), NewNaiveStrategy(), store, nil)

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
