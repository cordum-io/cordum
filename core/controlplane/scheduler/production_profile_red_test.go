package scheduler

// RED tests for task-a13f83fa. The production-profile admission and fencing
// APIs intentionally do not exist yet; these tests freeze the security
// contract before implementation.

import (
	"errors"
	"testing"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
)

func TestProductionAdmissionRejectsPayloadSessionTenantMismatch(t *testing.T) {
	packet := &agentv1.BusPacket{SenderId: "worker-a"}
	request := &agentv1.JobRequest{TenantId: "payload-tenant"}
	packet.Payload = &agentv1.BusPacket_JobRequest{JobRequest: request}
	session := ProductionSession{TenantID: "session-tenant", Subject: "worker-a"}

	err := ValidateProductionIdentity(packet, session)
	if !errors.Is(err, ErrProductionIdentityMismatch) {
		t.Fatalf("ValidateProductionIdentity error = %v, want identity mismatch", err)
	}
}

func TestProductionAdmissionRejectsStaleAttemptBeforeSideEffects(t *testing.T) {
	current := DispatchFence{DispatchID: "current", Attempt: 2, WorkerID: "worker-a"}
	event := DispatchFence{DispatchID: "old", Attempt: 1, WorkerID: "worker-a"}

	err := ValidateDispatchEvent(current, event)
	if !errors.Is(err, ErrStaleDispatchEvent) {
		t.Fatalf("ValidateDispatchEvent error = %v, want stale event rejection", err)
	}
}

func TestProductionCompensationFailsClosedWhenSafetyUnavailable(t *testing.T) {
	err := ValidateCompensationSafety(nil, errors.New("safety unavailable"), true)
	if !errors.Is(err, ErrSafetyUnavailable) {
		t.Fatalf("ValidateCompensationSafety error = %v, want fail-closed rejection", err)
	}
}
