package runtime

import (
	"testing"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
)

func TestValidateJobRequestWrapper(t *testing.T) {
	// Valid request should pass.
	req := &agentv1.JobRequest{JobId: "job-1", Topic: "job.default"}
	if err := ValidateJobRequest(req); err != nil {
		t.Fatalf("expected nil for valid request, got: %v", err)
	}

	// Empty job_id should fail.
	if err := ValidateJobRequest(&agentv1.JobRequest{Topic: "t"}); err == nil {
		t.Fatal("expected error for empty job_id")
	}

	// Empty topic should fail.
	if err := ValidateJobRequest(&agentv1.JobRequest{JobId: "j"}); err == nil {
		t.Fatal("expected error for empty topic")
	}

	// Nil should fail.
	if err := ValidateJobRequest(nil); err == nil {
		t.Fatal("expected error for nil request")
	}
}

func TestValidateJobResultWrapper(t *testing.T) {
	// Valid result should pass.
	res := &agentv1.JobResult{
		JobId:    "job-1",
		Status:   agentv1.JobStatus_JOB_STATUS_SUCCEEDED,
		WorkerId: "worker-1",
	}
	if err := ValidateJobResult(res); err != nil {
		t.Fatalf("expected nil for valid result, got: %v", err)
	}

	// Empty worker_id should fail.
	if err := ValidateJobResult(&agentv1.JobResult{
		JobId:  "j",
		Status: agentv1.JobStatus_JOB_STATUS_SUCCEEDED,
	}); err == nil {
		t.Fatal("expected error for empty worker_id")
	}

	// Nil should fail.
	if err := ValidateJobResult(nil); err == nil {
		t.Fatal("expected error for nil result")
	}
}
