package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
)

type fakeReconcileStore struct {
	states  map[string]JobState
	updated map[string]int64
	tenants map[string]string
	teams   map[string]string
	safety  map[string]SafetyDecisionRecord
	dead    map[string]int64
	attempts map[string]int
	locks   map[string]time.Time
	fail    bool
}

func newFakeReconcileStore() *fakeReconcileStore {
	return &fakeReconcileStore{
		states:  make(map[string]JobState),
		updated: make(map[string]int64),
		tenants: make(map[string]string),
		teams:   make(map[string]string),
		safety:  make(map[string]SafetyDecisionRecord),
		dead:    make(map[string]int64),
		attempts: make(map[string]int),
		locks:   make(map[string]time.Time),
	}
}

func toUnixMicros(t time.Time) int64 {
	return t.UnixNano() / int64(time.Microsecond)
}

func (s *fakeReconcileStore) SetState(_ context.Context, jobID string, state JobState) error {
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
	s.dead[jobID] = deadline.Unix()
	return nil
}

func (s *fakeReconcileStore) ListExpiredDeadlines(_ context.Context, nowUnix int64, limit int64) ([]JobRecord, error) {
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
	s.tenants[jobID] = tenant
	return nil
}

func (s *fakeReconcileStore) GetTenant(_ context.Context, jobID string) (string, error) {
	return s.tenants[jobID], nil
}

func (s *fakeReconcileStore) SetTeam(_ context.Context, jobID, team string) error {
	s.teams[jobID] = team
	return nil
}

func (s *fakeReconcileStore) GetTeam(_ context.Context, jobID string) (string, error) {
	return s.teams[jobID], nil
}

func (s *fakeReconcileStore) SetSafetyDecision(_ context.Context, jobID string, record SafetyDecisionRecord) error {
	s.safety[jobID] = record
	return nil
}

func (s *fakeReconcileStore) GetSafetyDecision(_ context.Context, jobID string) (SafetyDecisionRecord, error) {
	return s.safety[jobID], nil
}

func (s *fakeReconcileStore) GetAttempts(_ context.Context, jobID string) (int, error) {
	return s.attempts[jobID], nil
}

func (s *fakeReconcileStore) CountActiveByTenant(_ context.Context, _ string) (int, error) {
	return 0, nil
}

func (s *fakeReconcileStore) TryAcquireLock(_ context.Context, key string, ttl time.Duration) (bool, error) {
	if s.locks == nil {
		s.locks = make(map[string]time.Time)
	}
	if until, ok := s.locks[key]; ok && until.After(time.Now()) {
		return false, nil
	}
	s.locks[key] = time.Now().Add(ttl)
	return true, nil
}

func (s *fakeReconcileStore) ReleaseLock(_ context.Context, key string) error {
	delete(s.locks, key)
	return nil
}

func (s *fakeReconcileStore) CancelJob(_ context.Context, jobID string) (JobState, error) {
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

	time.Sleep(50 * time.Millisecond)
	cancel()
	time.Sleep(10 * time.Millisecond)

	if store.states["dispatched-old"] != JobStateTimeout {
		t.Fatalf("expected dispatched-old to be TIMEOUT, got %s", store.states["dispatched-old"])
	}
	if store.states["running-old"] != JobStateTimeout {
		t.Fatalf("expected running-old to be TIMEOUT, got %s", store.states["running-old"])
	}
	if store.states["dispatched-fresh"] != JobStateDispatched {
		t.Fatalf("expected dispatched-fresh to remain DISPATCHED, got %s", store.states["dispatched-fresh"])
	}
	if store.states["succeeded-old"] != JobStateSucceeded {
		t.Fatalf("terminal state should be unchanged, got %s", store.states["succeeded-old"])
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
