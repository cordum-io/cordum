package registry

import (
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestBuildSnapshot(t *testing.T) {
	workers := map[string]*pb.Heartbeat{
		"w1": {WorkerId: "w1", Pool: "default", ActiveJobs: 2, MaxParallelJobs: 4},
		"w2": {WorkerId: "w2", Pool: "default", ActiveJobs: 1, MaxParallelJobs: 2},
		"w3": {WorkerId: "w3", Pool: "batch", ActiveJobs: 0, MaxParallelJobs: 1},
	}
	topicToPool := map[string]string{
		"job.default": "default",
		"job.batch":   "batch",
		"job.unused":  "unused",
	}

	snapshot := BuildSnapshot(workers, topicToPool)

	if snapshot.Pools["default"].Workers != 2 {
		t.Fatalf("expected 2 default workers, got %d", snapshot.Pools["default"].Workers)
	}
	if snapshot.Pools["default"].Capacity != 6 {
		t.Fatalf("expected default capacity 6, got %d", snapshot.Pools["default"].Capacity)
	}
	if !snapshot.Topics["job.default"].Available {
		t.Fatalf("expected job.default available")
	}
	if snapshot.Topics["job.unused"].Available {
		t.Fatalf("expected job.unused unavailable")
	}
}
