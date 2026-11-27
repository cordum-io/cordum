package scheduler

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordum/cordum/core/licensing"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// requeueBoundSafety answers every check with one scripted result and counts
// the checks.
type requeueBoundSafety struct {
	record SafetyDecisionRecord
	err    error
	calls  atomic.Int64
}

func (s *requeueBoundSafety) Check(context.Context, *pb.JobRequest) (SafetyDecisionRecord, error) {
	s.calls.Add(1)
	return s.record, s.err
}

type requeueBoundCase struct {
	name   string
	record SafetyDecisionRecord
	err    error
	active int // other jobs holding the tenant's slots
	plan   licensing.Plan
}

func requeueBoundCases() []requeueBoundCase {
	tenantCap := &pb.PolicyConstraints{Budgets: &pb.BudgetConstraints{MaxConcurrentJobs: 1}}
	return []requeueBoundCase{
		{name: "tenant_limit", record: SafetyDecisionRecord{Decision: SafetyAllowWithConstraints, Constraints: tenantCap}, active: 1},
		{name: "tier_limit", record: SafetyDecisionRecord{Decision: SafetyAllow}, active: 3, plan: licensing.PlanCommunity},
		{name: "safety_unavailable", record: SafetyDecisionRecord{Decision: SafetyUnavailable, Reason: "kernel down"}},
		{name: "safety_not_recorded", err: errors.New("kernel unreachable")},
		{name: "safety_throttle", record: SafetyDecisionRecord{Decision: SafetyThrottle, Reason: "rate"}},
	}
}

// requeueBoundSetup seeds one PENDING job, with a live worker that would take
// it if nothing held it, and returns a replayer that re-handles it.
func requeueBoundSetup(t *testing.T, tc requeueBoundCase) (*PendingReplayer, *activeCountJobStore, *fakeBus, *requeueBoundSafety, string) {
	t.Helper()
	bus := &fakeBus{}
	registry := newTestRegistry(t)
	registry.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "worker-1", Pool: "default"})
	store := &activeCountJobStore{fakeJobStore: newFakeJobStore(), active: tc.active}
	safety := &requeueBoundSafety{record: tc.record, err: tc.err}
	engine := NewEngine(bus, safety, registry, NewNaiveStrategy(), store, nil)
	if tc.plan != "" {
		engine = engine.WithEntitlements(newSchedulerEntitlementResolver(t, tc.plan, nil))
	}
	req := &pb.JobRequest{JobId: "job-" + tc.name, Topic: "job.default", TenantId: "default"}
	ctx := context.Background()
	if err := store.SetJobRequest(ctx, req); err != nil {
		t.Fatalf("set job request: %v", err)
	}
	if err := store.SetState(ctx, req.JobId, JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	return NewPendingReplayer(engine, store, time.Millisecond, time.Millisecond), store, bus, safety, req.JobId
}

func requeueBoundReplay(r *PendingReplayer, n int) {
	for range n {
		r.replayPending(context.Background(), time.Now())
	}
}

// requeueBoundDispatched counts job requests published to a worker subject.
func requeueBoundDispatched(bus *fakeBus) (dispatched int, dlqCodes []string) {
	for _, msg := range bus.snapshotPublished() {
		if msg.packet.GetJobRequest() != nil {
			dispatched++
		}
		if msg.subject == capsdk.SubjectDLQ {
			dlqCodes = append(dlqCodes, msg.packet.GetJobResult().GetErrorCode())
		}
	}
	return dispatched, dlqCodes
}

// TestPendingRequeuesCountAttempts drives a held PENDING job only through the
// pending replayer. Each limit or safety requeue counts one attempt, so the job
// fails to the DLQ at maxSchedulingRetries, and nothing is dispatched.
func TestPendingRequeuesCountAttempts(t *testing.T) {
	for _, tc := range requeueBoundCases() {
		t.Run(tc.name, func(t *testing.T) {
			replayer, store, bus, safety, jobID := requeueBoundSetup(t, tc)
			requeueBoundReplay(replayer, 2*maxSchedulingRetries)

			ctx := context.Background()
			attempts, _ := store.GetAttempts(ctx, jobID)
			state, _ := store.GetState(ctx, jobID)
			dispatched, dlqCodes := requeueBoundDispatched(bus)
			if attempts != maxSchedulingRetries || state != JobStateFailed {
				t.Fatalf("after %d replays: attempts=%d state=%s, want attempts=%d state=%s",
					2*maxSchedulingRetries, attempts, state, maxSchedulingRetries, JobStateFailed)
			}
			if got := safety.calls.Load(); got > maxSchedulingRetries+1 {
				t.Fatalf("safety checks = %d, want at most %d", got, maxSchedulingRetries+1)
			}
			if dispatched != 0 {
				t.Fatalf("held job dispatched %d times", dispatched)
			}
			if len(dlqCodes) != 1 || dlqCodes[0] != "max_scheduling_retries" {
				t.Fatalf("dlq codes = %v, want [max_scheduling_retries]", dlqCodes)
			}
		})
	}

	// A job at its tenant's limit stays queued below the cap, and runs once a
	// slot frees.
	t.Run("tenant_limit_queues", func(t *testing.T) {
		replayer, store, bus, _, jobID := requeueBoundSetup(t, requeueBoundCases()[0])
		requeueBoundReplay(replayer, 10)
		ctx := context.Background()
		state, _ := store.GetState(ctx, jobID)
		if dispatched, _ := requeueBoundDispatched(bus); state != JobStatePending || dispatched != 0 {
			t.Fatalf("after 10 replays: state=%s dispatched=%d, want %s 0", state, dispatched, JobStatePending)
		}
		if attempts, _ := store.GetAttempts(ctx, jobID); attempts != 10 {
			t.Errorf("after 10 replays: attempts=%d, want 10", attempts)
		}
		store.active = 0
		requeueBoundReplay(replayer, 1)
		if dispatched, _ := requeueBoundDispatched(bus); dispatched != 1 {
			t.Fatalf("job not dispatched after its slot freed: dispatched=%d", dispatched)
		}
	})
}
