package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

type fakeReconcileStore struct {
	mu             sync.RWMutex
	states         map[string]JobState
	updated        map[string]int64
	tenants        map[string]string
	teams          map[string]string
	safety         map[string]SafetyDecisionRecord
	output         map[string]OutputSafetyRecord
	dead           map[string]int64
	attempts       map[string]int
	locks          map[string]time.Time
	failureReasons map[string]string
	fail           bool
}

func newFakeReconcileStore() *fakeReconcileStore {
	return &fakeReconcileStore{
		states:         make(map[string]JobState),
		updated:        make(map[string]int64),
		tenants:        make(map[string]string),
		teams:          make(map[string]string),
		safety:         make(map[string]SafetyDecisionRecord),
		output:         make(map[string]OutputSafetyRecord),
		dead:           make(map[string]int64),
		attempts:       make(map[string]int),
		locks:          make(map[string]time.Time),
		failureReasons: make(map[string]string),
	}
}

func toUnixMicros(t time.Time) int64 {
	return t.UnixNano() / int64(time.Microsecond)
}

func (s *fakeReconcileStore) SetState(_ context.Context, jobID string, state JobState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return errors.New("forced failure")
	}
	s.states[jobID] = state
	s.updated[jobID] = toUnixMicros(time.Now())
	if state == JobStateScheduled {
		s.attempts[jobID]++
	}
	return nil
}

func (s *fakeReconcileStore) GetState(_ context.Context, jobID string) (JobState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.states[jobID], nil
}

func (s *fakeReconcileStore) SetResultPtr(_ context.Context, jobID, resultPtr string) error {
	return nil
}

func (s *fakeReconcileStore) GetResultPtr(_ context.Context, jobID string) (string, error) {
	return "", nil
}

func (s *fakeReconcileStore) SetJobMeta(_ context.Context, _ *pb.JobRequest) error {
	return nil
}

func (s *fakeReconcileStore) SetDeadline(_ context.Context, jobID string, deadline time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dead[jobID] = deadline.Unix()
	return nil
}

func (s *fakeReconcileStore) ListExpiredDeadlines(_ context.Context, nowUnix int64, limit int64) ([]JobRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []JobRecord
	for id, ts := range s.dead {
		if ts <= nowUnix {
			out = append(out, JobRecord{ID: id, DeadlineUnix: ts})
			if limit > 0 && int64(len(out)) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (s *fakeReconcileStore) ListJobsByState(_ context.Context, state JobState, updatedBeforeUnix int64, _ int64) ([]JobRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []JobRecord
	for id, st := range s.states {
		if st != state {
			continue
		}
		if ts := s.updated[id]; ts <= updatedBeforeUnix {
			out = append(out, JobRecord{ID: id, UpdatedAt: ts})
		}
	}
	return out, nil
}

func (s *fakeReconcileStore) AddJobToTrace(_ context.Context, traceID, jobID string) error {
	return nil
}

func (s *fakeReconcileStore) GetTraceJobs(_ context.Context, traceID string) ([]JobRecord, error) {
	return nil, nil
}

func (s *fakeReconcileStore) SetTopic(_ context.Context, jobID, topic string) error {
	return nil
}

func (s *fakeReconcileStore) GetTopic(_ context.Context, jobID string) (string, error) {
	return "", nil
}

func (s *fakeReconcileStore) SetTenant(_ context.Context, jobID, tenant string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tenants[jobID] = tenant
	return nil
}

func (s *fakeReconcileStore) GetTenant(_ context.Context, jobID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tenants[jobID], nil
}

func (s *fakeReconcileStore) SetTeam(_ context.Context, jobID, team string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.teams[jobID] = team
	return nil
}

func (s *fakeReconcileStore) GetTeam(_ context.Context, jobID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.teams[jobID], nil
}

func (s *fakeReconcileStore) SetSafetyDecision(_ context.Context, jobID string, record SafetyDecisionRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.safety[jobID] = record
	return nil
}

func (s *fakeReconcileStore) GetSafetyDecision(_ context.Context, jobID string) (SafetyDecisionRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.safety[jobID], nil
}

func (s *fakeReconcileStore) GetAttempts(_ context.Context, jobID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.attempts[jobID], nil
}

func (s *fakeReconcileStore) CountActiveByTenant(_ context.Context, _ string) (int, error) {
	return 0, nil
}

func (s *fakeReconcileStore) TryAcquireLock(_ context.Context, key string, ttl time.Duration) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locks == nil {
		s.locks = make(map[string]time.Time)
	}
	if until, ok := s.locks[key]; ok && until.After(time.Now()) {
		return "", nil
	}
	s.locks[key] = time.Now().Add(ttl)
	return fmt.Sprintf("token-%s", key), nil
}

func (s *fakeReconcileStore) ReleaseLock(_ context.Context, key string, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.locks, key)
	return nil
}

func (s *fakeReconcileStore) SetFailureReason(_ context.Context, jobID, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failureReasons[jobID] = reason
	return nil
}

func (s *fakeReconcileStore) GetFailureReason(_ context.Context, jobID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.failureReasons[jobID], nil
}

func (s *fakeReconcileStore) SetOutputDecision(_ context.Context, jobID string, record OutputSafetyRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.output[jobID] = record
	return nil
}

func (s *fakeReconcileStore) GetOutputDecision(_ context.Context, jobID string) (OutputSafetyRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.output[jobID], nil
}

func (s *fakeReconcileStore) CancelJob(_ context.Context, jobID string) (JobState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.states[jobID]
	if terminalStates[state] {
		return state, nil
	}
	s.states[jobID] = JobStateCancelled
	return JobStateCancelled, nil
}

func TestReconcilerTimeouts(t *testing.T) {
	store := newFakeReconcileStore()

	// Seed jobs with old timestamps to trigger timeout.
	store.states["dispatched-old"] = JobStateDispatched
	store.updated["dispatched-old"] = toUnixMicros(time.Now().Add(-5 * time.Minute))
	store.states["running-old"] = JobStateRunning
	store.updated["running-old"] = toUnixMicros(time.Now().Add(-10 * time.Minute))

	// Fresh jobs should not be touched.
	store.states["dispatched-fresh"] = JobStateDispatched
	store.updated["dispatched-fresh"] = toUnixMicros(time.Now())
	store.states["succeeded-old"] = JobStateSucceeded
	store.updated["succeeded-old"] = toUnixMicros(time.Now().Add(-15 * time.Minute))

	reconciler := NewReconciler(store, 1*time.Minute, 1*time.Minute, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go reconciler.Start(ctx)

	// Poll until reconciler has processed the timeout transitions.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s1, _ := store.GetState(context.Background(), "dispatched-old")
		s2, _ := store.GetState(context.Background(), "running-old")
		if s1 == JobStateTimeout && s2 == JobStateTimeout {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	if state, _ := store.GetState(context.Background(), "dispatched-old"); state != JobStateTimeout {
		t.Fatalf("expected dispatched-old to be TIMEOUT, got %s", state)
	}
	if state, _ := store.GetState(context.Background(), "running-old"); state != JobStateTimeout {
		t.Fatalf("expected running-old to be TIMEOUT, got %s", state)
	}
	if state, _ := store.GetState(context.Background(), "dispatched-fresh"); state != JobStateDispatched {
		t.Fatalf("expected dispatched-fresh to remain DISPATCHED, got %s", state)
	}
	if state, _ := store.GetState(context.Background(), "succeeded-old"); state != JobStateSucceeded {
		t.Fatalf("terminal state should be unchanged, got %s", state)
	}
}

func TestReconcilerSetsFailureReasonOnTimeout(t *testing.T) {
	store := newFakeReconcileStore()

	store.states["dispatched-timeout"] = JobStateDispatched
	store.updated["dispatched-timeout"] = toUnixMicros(time.Now().Add(-5 * time.Minute))
	store.states["running-timeout"] = JobStateRunning
	store.updated["running-timeout"] = toUnixMicros(time.Now().Add(-10 * time.Minute))

	reconciler := NewReconciler(store, 1*time.Minute, 1*time.Minute, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go reconciler.Start(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s1, _ := store.GetState(context.Background(), "dispatched-timeout")
		s2, _ := store.GetState(context.Background(), "running-timeout")
		if s1 == JobStateTimeout && s2 == JobStateTimeout {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	if state, _ := store.GetState(context.Background(), "dispatched-timeout"); state != JobStateTimeout {
		t.Fatalf("expected dispatched-timeout to be TIMEOUT, got %s", state)
	}
	if state, _ := store.GetState(context.Background(), "running-timeout"); state != JobStateTimeout {
		t.Fatalf("expected running-timeout to be TIMEOUT, got %s", state)
	}

	reason1, _ := store.GetFailureReason(context.Background(), "dispatched-timeout")
	if reason1 == "" {
		t.Fatal("expected failure reason for dispatched-timeout, got empty")
	}
	if !strings.Contains(reason1, "DISPATCHED") {
		t.Fatalf("expected reason to mention DISPATCHED, got %q", reason1)
	}

	reason2, _ := store.GetFailureReason(context.Background(), "running-timeout")
	if reason2 == "" {
		t.Fatal("expected failure reason for running-timeout, got empty")
	}
	if !strings.Contains(reason2, "RUNNING") {
		t.Fatalf("expected reason to mention RUNNING, got %q", reason2)
	}
}

func TestReconcilerSetsFailureReasonOnDeadlineExpiry(t *testing.T) {
	store := newFakeReconcileStore()

	store.states["deadline-job"] = JobStateRunning
	store.updated["deadline-job"] = toUnixMicros(time.Now())
	store.dead["deadline-job"] = time.Now().Add(-1 * time.Minute).Unix()

	reconciler := NewReconciler(store, 1*time.Hour, 1*time.Hour, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go reconciler.Start(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s, _ := store.GetState(context.Background(), "deadline-job")
		if s == JobStateTimeout {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	if state, _ := store.GetState(context.Background(), "deadline-job"); state != JobStateTimeout {
		t.Fatalf("expected deadline-job to be TIMEOUT, got %s", state)
	}

	reason, _ := store.GetFailureReason(context.Background(), "deadline-job")
	if reason != "timeout: deadline expired" {
		t.Fatalf("expected reason 'timeout: deadline expired', got %q", reason)
	}
}

// ---------------------------------------------------------------------------
// Benchmarks
// ---------------------------------------------------------------------------

// BenchmarkReconcilerTick benchmarks a single reconciler tick with 500 DISPATCHED
// jobs that have old timestamps, triggering the timeout path.
func BenchmarkReconcilerTick(b *testing.B) {
	silenceLogs(b)
	const jobCount = 500
	oldTs := toUnixMicros(time.Now().Add(-10 * time.Minute))

	store := newFakeReconcileStore()
	rec := NewReconciler(store, time.Minute, time.Minute, time.Second)
	ctx := context.Background()

	// Seed jobs once before reset.
	seedJobs := func() {
		store.mu.Lock()
		for k := range store.states {
			delete(store.states, k)
			delete(store.updated, k)
		}
		for i := 0; i < jobCount; i++ {
			id := fmt.Sprintf("bench-job-%d", i)
			store.states[id] = JobStateDispatched
			store.updated[id] = oldTs
		}
		store.mu.Unlock()
	}

	seedJobs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		seedJobs()
		b.StartTimer()
		rec.tick(ctx)
	}
}

func TestReconcilerStopsWhenNoProgress(t *testing.T) {
	store := newFakeReconcileStore()
	store.fail = true
	store.states["stuck"] = JobStateRunning
	store.updated["stuck"] = toUnixMicros(time.Now().Add(-10 * time.Minute))

	rec := NewReconciler(store, time.Minute, time.Minute, 10*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		rec.handleTimeouts(ctx, JobStateRunning, time.Now().Add(-time.Minute))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reconciler did not exit when no progress was made before timeout")
	}
}
