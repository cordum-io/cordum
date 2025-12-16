package scheduler

import (
	"testing"

	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
)

func TestLeastLoadedStrategyPicksPoolMatch(t *testing.T) {
	strategy := NewLeastLoadedStrategy(map[string]string{
		"job.echo": "echo",
	})
	workers := map[string]*pb.Heartbeat{
		"w1": {WorkerId: "w1", Pool: "echo", ActiveJobs: 2, CpuLoad: 50},
		"w2": {WorkerId: "w2", Pool: "echo", ActiveJobs: 1, CpuLoad: 10},
		"w3": {WorkerId: "w3", Pool: "other", ActiveJobs: 0, CpuLoad: 0},
	}

	subject, err := strategy.PickSubject(&pb.JobRequest{Topic: "job.echo"}, workers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if subject != "worker.w2.jobs" {
		t.Fatalf("expected subject worker.w2.jobs, got %s", subject)
	}
}

func TestLeastLoadedStrategyNoWorkers(t *testing.T) {
	strategy := NewLeastLoadedStrategy(map[string]string{
		"job.echo": "echo",
	})
	_, err := strategy.PickSubject(&pb.JobRequest{Topic: "job.echo"}, map[string]*pb.Heartbeat{})
	if err == nil {
		t.Fatalf("expected error when no workers")
	}
}

func TestLeastLoadedStrategyNoPoolConfigured(t *testing.T) {
	strategy := NewLeastLoadedStrategy(map[string]string{
		"job.echo": "echo",
	})
	_, err := strategy.PickSubject(&pb.JobRequest{Topic: "job.unknown"}, map[string]*pb.Heartbeat{})
	if err == nil {
		t.Fatalf("expected error for unknown topic pool")
	}
}

func TestLeastLoadedStrategyUsesLoadScore(t *testing.T) {
	strategy := NewLeastLoadedStrategy(map[string]string{
		"job.echo": "echo",
	})
	workers := map[string]*pb.Heartbeat{
		"w1": {WorkerId: "w1", Pool: "echo", ActiveJobs: 1, CpuLoad: 90, GpuUtilization: 0},
		"w2": {WorkerId: "w2", Pool: "echo", ActiveJobs: 1, CpuLoad: 10, GpuUtilization: 0},
	}

	subject, err := strategy.PickSubject(&pb.JobRequest{Topic: "job.echo"}, workers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if subject != "worker.w2.jobs" {
		t.Fatalf("expected subject worker.w2.jobs, got %s", subject)
	}
	// w2 should be selected due to lower score; ensure best isn't nil
}

func TestLeastLoadedStrategyHonorsPreferredWorker(t *testing.T) {
	strategy := NewLeastLoadedStrategy(map[string]string{
		"job.echo": "echo",
	})
	workers := map[string]*pb.Heartbeat{
		"w1": {WorkerId: "w1", Pool: "echo", ActiveJobs: 5, CpuLoad: 90},
		"w2": {WorkerId: "w2", Pool: "echo", ActiveJobs: 2, CpuLoad: 50},
		"w3": {WorkerId: "w3", Pool: "echo", ActiveJobs: 1, CpuLoad: 10},
	}

	req := &pb.JobRequest{
		Topic: "job.echo",
		Labels: map[string]string{
			"preferred_worker_id": "w2",
		},
	}

	subject, err := strategy.PickSubject(req, workers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if subject != "worker.w2.jobs" {
		t.Fatalf("expected subject worker.w2.jobs via preferred hint, got %s", subject)
	}
}

func TestLeastLoadedStrategyIgnoresWorkflowLabelsForPlacement(t *testing.T) {
	strategy := NewLeastLoadedStrategy(map[string]string{
		"job.echo": "echo",
	})
	workers := map[string]*pb.Heartbeat{
		"w1": {WorkerId: "w1", Pool: "echo", ActiveJobs: 0, CpuLoad: 10},
	}

	req := &pb.JobRequest{
		Topic: "job.echo",
		Labels: map[string]string{
			"workflow_id": "wf-1",
			"run_id":      "run-1",
			"step_id":     "step-1",
		},
	}

	subject, err := strategy.PickSubject(req, workers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if subject != "worker.w1.jobs" {
		t.Fatalf("expected subject worker.w1.jobs, got %s", subject)
	}
}
