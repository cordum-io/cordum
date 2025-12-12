package scheduler

import (
	"context"
	"testing"
	"time"
)

type fakeReconcileStore struct {
	states  map[string]JobState
	updated map[string]int64
	tenants map[string]string
	safety  map[string]struct{ decision, reason string }
}

func newFakeReconcileStore() *fakeReconcileStore {
	return &fakeReconcileStore{
		states:  make(map[string]JobState),
		updated: make(map[string]int64),
		tenants: make(map[string]string),
		safety:  make(map[string]struct{ decision, reason string }),
	}
}

func (s *fakeReconcileStore) SetState(_ context.Context, jobID string, state JobState) error {
	s.states[jobID] = state
	s.updated[jobID] = time.Now().Unix()
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

func (s *fakeReconcileStore) SetSafetyDecision(_ context.Context, jobID, decision, reason string) error {
	s.safety[jobID] = struct{ decision, reason string }{decision: decision, reason: reason}
	return nil
}

func (s *fakeReconcileStore) GetSafetyDecision(_ context.Context, jobID string) (string, string, error) {
	entry := s.safety[jobID]
	return entry.decision, entry.reason, nil
}

func TestReconcilerTimeouts(t *testing.T) {
	store := newFakeReconcileStore()

	// Seed jobs with old timestamps to trigger timeout.
	store.states["dispatched-old"] = JobStateDispatched
	store.updated["dispatched-old"] = time.Now().Add(-5 * time.Minute).Unix()
	store.states["running-old"] = JobStateRunning
	store.updated["running-old"] = time.Now().Add(-10 * time.Minute).Unix()

	// Fresh jobs should not be touched.
	store.states["dispatched-fresh"] = JobStateDispatched
	store.updated["dispatched-fresh"] = time.Now().Unix()
	store.states["succeeded-old"] = JobStateSucceeded
	store.updated["succeeded-old"] = time.Now().Add(-15 * time.Minute).Unix()

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
