package scheduler

import (
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestDispatchConstraintRequiresWorkerEnforcement(t *testing.T) {
	for _, constrained := range []bool{false, true} {
		name := "plain_allow"
		if constrained {
			name = "sandbox_requires_enforcement"
		}
		t.Run(name, func(t *testing.T) { dispatchConstraintCheck(t, constrained) })
	}
}

func dispatchConstraintCheck(t *testing.T, constrained bool) {
	t.Helper()
	bus := &fakeBus{}
	store := newFakeJobStore()
	registry := newTestRegistry(t)
	registry.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w1", Pool: "default"})
	record := SafetyDecisionRecord{Decision: SafetyAllow}
	if constrained {
		record.Decision = SafetyAllowWithConstraints
		record.Constraints = &pb.PolicyConstraints{Sandbox: &pb.SandboxProfile{Isolated: true}}
	}
	engine := NewEngine(bus, &fixedSafetyRecordChecker{record: record}, registry,
		NewLeastLoadedStrategy(defaultRouting()), store, nil)
	req := &pb.JobRequest{JobId: "constraint-job", Topic: "job.default"}
	if err := engine.handleJobRequest(req, "constraint-trace"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	dispatches := 0
	for _, msg := range bus.snapshotPublished() {
		if msg.packet.GetJobRequest().GetJobId() == req.JobId {
			dispatches++
		}
	}
	if constrained {
		if dispatches != 0 {
			t.Errorf("sandbox constraint requires enforcement: published %d job requests, want none", dispatches)
		}
		if state := store.states[req.JobId]; state != JobStateDenied {
			t.Errorf("sandbox constraint requires denial: state=%s", state)
		}
	} else if dispatches != 1 || store.states[req.JobId] != JobStateRunning {
		t.Errorf("plain allow: dispatches=%d state=%s, want one dispatch and RUNNING", dispatches, store.states[req.JobId])
	}
}
