package scheduler_test

import (
	"encoding/json"
	"testing"

	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/registry"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func makeSnapshot(workers []registry.WorkerSummary) []byte {
	snap := registry.Snapshot{
		CapturedAt: "2026-02-20T00:00:00Z",
		Workers:    workers,
	}
	data, _ := json.Marshal(snap)
	return data
}

func TestHydrateFromSnapshot_ValidData(t *testing.T) {
	r := scheduler.NewMemoryRegistry()
	t.Cleanup(r.Close)

	data := makeSnapshot([]registry.WorkerSummary{
		{WorkerID: "w1", Pool: "default", ActiveJobs: 2, MaxParallelJobs: 10},
		{WorkerID: "w2", Pool: "default", ActiveJobs: 0, MaxParallelJobs: 5},
		{WorkerID: "w3", Pool: "gpu", ActiveJobs: 1, MaxParallelJobs: 4, Capabilities: []string{"cuda"}},
	})

	if err := r.HydrateFromSnapshot(data); err != nil {
		t.Fatalf("HydrateFromSnapshot: %v", err)
	}

	// Verify all 3 workers in snapshot.
	snap := r.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("expected 3 workers, got %d", len(snap))
	}

	// Verify per-pool filtering.
	defaultWorkers := r.WorkersForPool("default")
	if len(defaultWorkers) != 2 {
		t.Fatalf("expected 2 default workers, got %d", len(defaultWorkers))
	}
	gpuWorkers := r.WorkersForPool("gpu")
	if len(gpuWorkers) != 1 {
		t.Fatalf("expected 1 gpu worker, got %d", len(gpuWorkers))
	}

	// Verify worker fields preserved.
	w3 := snap["w3"]
	if w3 == nil {
		t.Fatal("w3 not found in snapshot")
	}
	if w3.Pool != "gpu" {
		t.Errorf("expected pool 'gpu', got %q", w3.Pool)
	}
	if w3.MaxParallelJobs != 4 {
		t.Errorf("expected max_parallel_jobs 4, got %d", w3.MaxParallelJobs)
	}
	if len(w3.Capabilities) != 1 || w3.Capabilities[0] != "cuda" {
		t.Errorf("expected capabilities [cuda], got %v", w3.Capabilities)
	}
}

func TestHydrateFromSnapshot_EmptyData(t *testing.T) {
	r := scheduler.NewMemoryRegistry()
	t.Cleanup(r.Close)

	// nil data → no-op.
	if err := r.HydrateFromSnapshot(nil); err != nil {
		t.Fatalf("nil: %v", err)
	}
	if len(r.Snapshot()) != 0 {
		t.Fatalf("expected empty registry after nil hydration")
	}

	// Empty slice → no-op.
	if err := r.HydrateFromSnapshot([]byte{}); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if len(r.Snapshot()) != 0 {
		t.Fatalf("expected empty registry after empty hydration")
	}
}

func TestHydrateFromSnapshot_CorruptData(t *testing.T) {
	r := scheduler.NewMemoryRegistry()
	t.Cleanup(r.Close)

	err := r.HydrateFromSnapshot([]byte("not json"))
	if err == nil {
		t.Fatal("expected error for corrupt data")
	}
	if len(r.Snapshot()) != 0 {
		t.Fatal("expected empty registry after corrupt data")
	}
}

func TestHydrateFromSnapshot_EmptyWorkersList(t *testing.T) {
	r := scheduler.NewMemoryRegistry()
	t.Cleanup(r.Close)

	data := makeSnapshot(nil) // Workers: nil → empty in JSON
	if err := r.HydrateFromSnapshot(data); err != nil {
		t.Fatalf("HydrateFromSnapshot: %v", err)
	}
	if len(r.Snapshot()) != 0 {
		t.Fatal("expected empty registry for empty workers list")
	}
}

func TestHydrateFromSnapshot_MergeWithLiveHeartbeats(t *testing.T) {
	r := scheduler.NewMemoryRegistry()
	t.Cleanup(r.Close)

	// Hydrate with 2 workers.
	data := makeSnapshot([]registry.WorkerSummary{
		{WorkerID: "w1", Pool: "default", MaxParallelJobs: 5},
		{WorkerID: "w2", Pool: "default", MaxParallelJobs: 3},
	})
	if err := r.HydrateFromSnapshot(data); err != nil {
		t.Fatalf("HydrateFromSnapshot: %v", err)
	}

	// Simulate live heartbeat for a 3rd worker.
	r.UpdateHeartbeat(&pb.Heartbeat{
		WorkerId:        "w3",
		Pool:            "gpu",
		MaxParallelJobs: 8,
	})

	snap := r.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("expected 3 workers (2 hydrated + 1 live), got %d", len(snap))
	}

	// Simulate live heartbeat for existing worker — should update, not duplicate.
	r.UpdateHeartbeat(&pb.Heartbeat{
		WorkerId:        "w1",
		Pool:            "default",
		MaxParallelJobs: 20, // updated
	})

	snap = r.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("expected 3 workers after update, got %d", len(snap))
	}
	if snap["w1"].MaxParallelJobs != 20 {
		t.Errorf("expected updated max_parallel_jobs 20, got %d", snap["w1"].MaxParallelJobs)
	}
}

func TestHydrateFromSnapshot_SkipsEmptyWorkerIDs(t *testing.T) {
	r := scheduler.NewMemoryRegistry()
	t.Cleanup(r.Close)

	data := makeSnapshot([]registry.WorkerSummary{
		{WorkerID: "", Pool: "default"},
		{WorkerID: "w1", Pool: "default"},
	})
	if err := r.HydrateFromSnapshot(data); err != nil {
		t.Fatalf("HydrateFromSnapshot: %v", err)
	}
	if len(r.Snapshot()) != 1 {
		t.Fatalf("expected 1 worker (empty ID skipped), got %d", len(r.Snapshot()))
	}
}
