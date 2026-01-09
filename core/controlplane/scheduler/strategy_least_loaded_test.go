package scheduler

import (
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func routingForTopic(topic, pool string) PoolRouting {
	return PoolRouting{
		Topics: map[string][]string{
			topic: {pool},
		},
		Pools: map[string]PoolProfile{
			pool: {},
		},
	}
}

func TestLeastLoadedStrategyPicksPoolMatch(t *testing.T) {
	strategy := NewLeastLoadedStrategy(routingForTopic("job.default", "default"))
	workers := map[string]*pb.Heartbeat{
		"w1": {WorkerId: "w1", Pool: "default", ActiveJobs: 2, CpuLoad: 50},
		"w2": {WorkerId: "w2", Pool: "default", ActiveJobs: 1, CpuLoad: 10},
		"w3": {WorkerId: "w3", Pool: "other", ActiveJobs: 0, CpuLoad: 0},
	}

	subject, err := strategy.PickSubject(&pb.JobRequest{Topic: "job.default"}, workers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if subject != "worker.w2.jobs" {
		t.Fatalf("expected subject worker.w2.jobs, got %s", subject)
	}
}

func TestLeastLoadedStrategyNoWorkers(t *testing.T) {
	strategy := NewLeastLoadedStrategy(routingForTopic("job.default", "default"))
	_, err := strategy.PickSubject(&pb.JobRequest{Topic: "job.default"}, map[string]*pb.Heartbeat{})
	if err == nil {
		t.Fatalf("expected error when no workers")
	}
}

func TestLeastLoadedStrategyNoPoolConfigured(t *testing.T) {
	strategy := NewLeastLoadedStrategy(routingForTopic("job.default", "default"))
	_, err := strategy.PickSubject(&pb.JobRequest{Topic: "job.unknown"}, map[string]*pb.Heartbeat{})
	if err == nil {
		t.Fatalf("expected error for unknown topic pool")
	}
}

func TestLeastLoadedStrategyUsesLoadScore(t *testing.T) {
	strategy := NewLeastLoadedStrategy(routingForTopic("job.default", "default"))
	workers := map[string]*pb.Heartbeat{
		"w1": {WorkerId: "w1", Pool: "default", ActiveJobs: 1, CpuLoad: 90, GpuUtilization: 0},
		"w2": {WorkerId: "w2", Pool: "default", ActiveJobs: 1, CpuLoad: 10, GpuUtilization: 0},
	}

	subject, err := strategy.PickSubject(&pb.JobRequest{Topic: "job.default"}, workers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if subject != "worker.w2.jobs" {
		t.Fatalf("expected subject worker.w2.jobs, got %s", subject)
	}
	// w2 should be selected due to lower score; ensure best isn't nil
}

func TestLeastLoadedStrategyHonorsPreferredWorker(t *testing.T) {
	strategy := NewLeastLoadedStrategy(routingForTopic("job.default", "default"))
	workers := map[string]*pb.Heartbeat{
		"w1": {WorkerId: "w1", Pool: "default", ActiveJobs: 5, CpuLoad: 90},
		"w2": {WorkerId: "w2", Pool: "default", ActiveJobs: 2, CpuLoad: 50},
		"w3": {WorkerId: "w3", Pool: "default", ActiveJobs: 1, CpuLoad: 10},
	}

	req := &pb.JobRequest{
		Topic: "job.default",
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
	strategy := NewLeastLoadedStrategy(routingForTopic("job.default", "default"))
	workers := map[string]*pb.Heartbeat{
		"w1": {WorkerId: "w1", Pool: "default", ActiveJobs: 0, CpuLoad: 10},
	}

	req := &pb.JobRequest{
		Topic: "job.default",
		Labels: map[string]string{
			"workflow_id": "wf-1",
			"run_id":      "run-1",
			"step_id":     "step-1",
			"node_id":     "n-1",
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

func TestLeastLoadedStrategyDoesNotMarkIdleWorkerOverloaded(t *testing.T) {
	strategy := NewLeastLoadedStrategy(routingForTopic("job.default", "default"))
	workers := map[string]*pb.Heartbeat{
		"w1": {WorkerId: "w1", Pool: "default", ActiveJobs: 0, CpuLoad: 1, MaxParallelJobs: 1},
	}

	subject, err := strategy.PickSubject(&pb.JobRequest{Topic: "job.default"}, workers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if subject != "worker.w1.jobs" {
		t.Fatalf("expected subject worker.w1.jobs, got %s", subject)
	}
}

func TestLeastLoadedStrategyMarksWorkerOverloadedWhenAtCapacity(t *testing.T) {
	strategy := NewLeastLoadedStrategy(routingForTopic("job.default", "default"))
	workers := map[string]*pb.Heartbeat{
		"w1": {WorkerId: "w1", Pool: "default", ActiveJobs: 1, CpuLoad: 1, MaxParallelJobs: 1},
	}

	_, err := strategy.PickSubject(&pb.JobRequest{Topic: "job.default"}, workers)
	if err == nil {
		t.Fatalf("expected error when all workers overloaded")
	}
}

func TestFilterPlacementLabels(t *testing.T) {
	labels := map[string]string{
		"preferred_worker_id": "w1",
		"preferred_pool":      "pool",
		"approval_granted":    "true",
		"secrets_present":     "true",
		"cordum.trace":        "trace",
		"workflow_id":         "wf",
		"run_id":              "run",
		"step_id":             "step",
		"node_id":             "node",
		"worker_id":           "worker",
		"region":              "us-east",
		"gpu":                 "true",
	}
	out := filterPlacementLabels(labels)
	if len(out) != 2 {
		t.Fatalf("expected 2 placement labels, got %d", len(out))
	}
	if out["region"] != "us-east" || out["gpu"] != "true" {
		t.Fatalf("unexpected placement labels: %#v", out)
	}
}

func TestFilterEligiblePools(t *testing.T) {
	pools := []string{"p1", "p2", "p3"}
	configs := map[string]PoolProfile{
		"p1": {Requires: []string{"linux", "gpu"}},
		"p2": {Requires: []string{"linux"}},
		"p3": {},
	}
	eligible := filterEligiblePools(pools, []string{"linux"}, configs)
	if len(eligible) != 2 || eligible[0] != "p1" || eligible[1] != "p2" {
		t.Fatalf("unexpected eligible pools: %#v", eligible)
	}
	eligible = filterEligiblePools(pools, nil, configs)
	if len(eligible) != 3 {
		t.Fatalf("expected all pools when no requires")
	}
}

func TestPoolSatisfies(t *testing.T) {
	if !poolSatisfies([]string{"GPU", " linux "}, []string{"gpu", "linux"}) {
		t.Fatalf("expected pool to satisfy requirements")
	}
	if poolSatisfies([]string{"gpu"}, []string{"gpu", "linux"}) {
		t.Fatalf("expected pool to miss requirements")
	}
	if poolSatisfies(nil, []string{"gpu"}) {
		t.Fatalf("expected empty pool requires to fail")
	}
}

func TestContainsPool(t *testing.T) {
	if !containsPool([]string{"a", "b"}, "b") {
		t.Fatalf("expected to find pool")
	}
	if containsPool([]string{"a", "b"}, "c") {
		t.Fatalf("expected missing pool")
	}
}

func TestIsOverloadedThresholds(t *testing.T) {
	if !isOverloaded(&pb.Heartbeat{CpuLoad: 95}) {
		t.Fatalf("expected cpu overload")
	}
	if !isOverloaded(&pb.Heartbeat{GpuUtilization: 95}) {
		t.Fatalf("expected gpu overload")
	}
	if isOverloaded(&pb.Heartbeat{CpuLoad: 10, GpuUtilization: 10}) {
		t.Fatalf("expected not overloaded")
	}
}
