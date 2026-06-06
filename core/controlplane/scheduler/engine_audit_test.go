package scheduler

import (
	"context"
	"testing"

	"github.com/cordum/cordum/core/audit"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// TestScheduler_FailedRetryableDoesNotSilentlyDrop asserts that a
// JOB_STATUS_FAILED_RETRYABLE result does NOT transition the job to the
// terminal JobStateFailed state — which would cause a subsequent retry packet
// for the same jobID to short-circuit at the terminal-state check in
// handleJobRequest, silently dropping the retry.
//
// The fix maps FAILED_RETRYABLE to the new non-terminal JobStateRetrying so the
// retry packet can re-dispatch. DLQ suppression for FAILED_RETRYABLE is
// preserved (it was correct given the new non-terminal state).
//
// Regression for audit finding #3 (scheduler/engine.go:2254-2272).
func TestScheduler_FailedRetryableDoesNotSilentlyDrop(t *testing.T) {
	bus := &fakeBus{}
	jobStore := newFakeJobStore()
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), jobStore, nil)

	// Seed prior dispatch state (the path the job took on its first attempt).
	jobID := "job-retryable-not-terminal"
	if err := engine.setJobState(context.Background(), jobID, JobStateRunning); err != nil {
		t.Fatalf("seed Running: %v", err)
	}

	res := &pb.JobResult{
		JobId:  jobID,
		Status: pb.JobStatus_JOB_STATUS_FAILED_RETRYABLE,
	}
	if err := engine.handleJobResult(res); err != nil {
		t.Fatalf("handle job result: %v", err)
	}

	got := jobStore.states[jobID]
	if got == JobStateFailed {
		t.Fatalf("FAILED_RETRYABLE must not transition to terminal JobStateFailed (would prevent retry re-dispatch). got %s", got)
	}
	if got != JobStateRetrying {
		t.Fatalf("expected JobStateRetrying (non-terminal, allows retry re-dispatch), got %s", got)
	}

	// And DLQ is still suppressed for FAILED_RETRYABLE — workflow retries should
	// not produce DLQ noise on every attempt.
	if len(bus.published) != 0 {
		t.Fatalf("expected no DLQ publish for retryable failure, got %d", len(bus.published))
	}
}

// TestFailOpenEmitsAuditEvent asserts that when safety kernel admit a job via
// the fail-open path, a dedicated audit event is emitted carrying the tenant,
// job ID, and bypass reason. Currently fail-open is logged at WARN only,
// which is insufficient for forensic correlation of bypassed jobs.
//
// Regression for audit finding #11 (scheduler/engine.go:1514-1532).
func TestFailOpenEmitsAuditEvent(t *testing.T) {
	bus := &fakeBus{}
	jobStore := newFakeJobStore()
	sink := &recordingSink{}
	// SafetyBasic always returns ALLOW. Substitute a fake that returns
	// SafetyUnavailable so the fail-open branch runs.
	safety := &unavailableSafety{}
	engine := NewEngine(bus, safety, newTestRegistry(t), NewNaiveStrategy(), jobStore, nil).
		WithDispatchAuditSink(sink).
		WithInputFailMode("open")

	req := &pb.JobRequest{
		JobId:    "job-fail-open",
		Topic:    "job.default",
		TenantId: "tenant-x",
	}
	if err := engine.processJob(testCtx(t), req, "trace-fail-open"); err != nil {
		t.Fatalf("processJob: %v", err)
	}

	// Expect at least one safety-bypass audit event.
	found := false
	for _, ev := range sink.events {
		if ev.EventType == audit.EventSafetyBypassAdmit {
			if ev.TenantID != "tenant-x" {
				t.Errorf("audit event TenantID = %q, want tenant-x", ev.TenantID)
			}
			if ev.JobID != "job-fail-open" {
				t.Errorf("audit event JobID = %q, want job-fail-open", ev.JobID)
			}
			if ev.Severity != audit.SeverityHigh {
				t.Errorf("audit event Severity = %q, want SeverityHigh (admit-on-unavailable is a security-relevant event)", ev.Severity)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected EventSafetyBypassAdmit emitted on fail-open admission; got %d events (%+v)", sink.count(), sink.events)
	}
}

// unavailableSafety always returns SafetyUnavailable so the fail-open branch
// of processJob runs.
type unavailableSafety struct{}

func (unavailableSafety) Check(_ context.Context, _ *pb.JobRequest) (SafetyDecisionRecord, error) {
	return SafetyDecisionRecord{Decision: SafetyUnavailable, Reason: "kernel offline"}, nil
}
