package scheduler

import (
	"errors"
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestIsRetryableSchedulingError(t *testing.T) {
	if !isRetryableSchedulingError(ErrNoWorkers) {
		t.Fatalf("expected no workers to be retryable")
	}
	if !isRetryableSchedulingError(ErrPoolOverloaded) {
		t.Fatalf("expected pool overloaded to be retryable")
	}
	if !isRetryableSchedulingError(ErrTenantLimit) {
		t.Fatalf("expected tenant limit to be retryable")
	}
	if !isRetryableSchedulingError(errors.New("no workers available for pool")) {
		t.Fatalf("expected substring match to be retryable")
	}
	if isRetryableSchedulingError(errors.New("random")) {
		t.Fatalf("unexpected retryable error")
	}
	if isRetryableSchedulingError(nil) {
		t.Fatalf("nil error should not be retryable")
	}
}

func TestReasonCodeForSchedulingError(t *testing.T) {
	cases := map[error]string{
		ErrNoPoolMapping: "no_pool_mapping",
		ErrNoWorkers:     "no_workers",
		ErrPoolOverloaded: "pool_overloaded",
		ErrTenantLimit:   "tenant_limit",
		errors.New("x"):  "dispatch_failed",
	}
	for err, expect := range cases {
		if got := reasonCodeForSchedulingError(err); got != expect {
			t.Fatalf("error %v expected %s got %s", err, expect, got)
		}
	}
	if reasonCodeForSchedulingError(nil) != "" {
		t.Fatalf("expected empty reason for nil error")
	}
}

func TestApplyConstraints(t *testing.T) {
	req := &pb.JobRequest{
		Env:    map[string]string{},
		Budget: &pb.Budget{DeadlineMs: 5000},
	}
	constraints := &pb.PolicyConstraints{
		RedactionLevel: "high",
		Budgets: &pb.BudgetConstraints{
			MaxRuntimeMs:      2000,
			MaxArtifactBytes:  1024,
			MaxConcurrentJobs: 3,
			MaxRetries:        2,
		},
	}

	applyConstraints(req, constraints)

	if req.Env["CORDUM_POLICY_CONSTRAINTS"] == "" {
		t.Fatalf("expected policy constraints env")
	}
	if req.Env["CORDUM_REDACTION_LEVEL"] != "high" {
		t.Fatalf("expected redaction level")
	}
	if req.Env["CORDUM_MAX_ARTIFACT_BYTES"] != "1024" {
		t.Fatalf("expected max artifact bytes")
	}
	if req.Env["CORDUM_MAX_CONCURRENT_JOBS"] != "3" {
		t.Fatalf("expected max concurrent jobs")
	}
	if req.Env["CORDUM_MAX_RETRIES"] != "2" {
		t.Fatalf("expected max retries")
	}
	if req.Budget.DeadlineMs != 2000 {
		t.Fatalf("expected deadline to clamp to max runtime")
	}
}

func TestConstraintHelpers(t *testing.T) {
	if maxRetriesFromConstraints(nil) != 0 {
		t.Fatalf("expected zero retries for nil constraints")
	}
	if maxConcurrentFromConstraints(nil) != 0 {
		t.Fatalf("expected zero concurrent for nil constraints")
	}
	constraints := &pb.PolicyConstraints{Budgets: &pb.BudgetConstraints{MaxRetries: 4, MaxConcurrentJobs: 7}}
	if maxRetriesFromConstraints(constraints) != 4 {
		t.Fatalf("expected retries from constraints")
	}
	if maxConcurrentFromConstraints(constraints) != 7 {
		t.Fatalf("expected concurrent from constraints")
	}
}

func TestSafeJobIDAndTopic(t *testing.T) {
	if safeJobID(nil) != "" {
		t.Fatalf("expected empty job id")
	}
	if safeTopic(nil) != "" {
		t.Fatalf("expected empty topic")
	}
	if safeJobID(&pb.JobRequest{JobId: "job-1"}) != "job-1" {
		t.Fatalf("expected job id")
	}
	if safeTopic(&pb.JobRequest{Topic: "job.test"}) != "job.test" {
		t.Fatalf("expected topic")
	}
}
