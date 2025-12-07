package scheduler_test

import (
	"sync"
	"testing"

	"github.com/yaront1111/cortex-os/core/internal/scheduler"
	pb "github.com/yaront1111/cortex-os/core/pkg/pb/v1"
)

func TestMemoryRegistry_UpdateHeartbeat(t *testing.T) {
	r := scheduler.NewMemoryRegistry()

	hb := &pb.Heartbeat{
		WorkerId: "worker-1",
		Pool:     "gpu-pool",
		CpuLoad:  50.0,
	}

	r.UpdateHeartbeat(hb)

	snapshot := r.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 worker, got %d", len(snapshot))
	}

	saved, ok := snapshot["worker-1"]
	if !ok {
		t.Fatal("worker-1 not found in snapshot")
	}
	if saved.Pool != "gpu-pool" {
		t.Errorf("expected pool 'gpu-pool', got '%s'", saved.Pool)
	}
}

func TestMemoryRegistry_WorkersForPool(t *testing.T) {
	r := scheduler.NewMemoryRegistry()

	r.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w1", Pool: "A"})
	r.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w2", Pool: "A"})
	r.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w3", Pool: "B"})

	poolA := r.WorkersForPool("A")
	if len(poolA) != 2 {
		t.Errorf("expected 2 workers in pool A, got %d", len(poolA))
	}

	poolB := r.WorkersForPool("B")
	if len(poolB) != 1 {
		t.Errorf("expected 1 worker in pool B, got %d", len(poolB))
	}

	poolC := r.WorkersForPool("C")
	if len(poolC) != 0 {
		t.Errorf("expected 0 workers in pool C, got %d", len(poolC))
	}
}

func TestMemoryRegistry_Concurrency(t *testing.T) {
	r := scheduler.NewMemoryRegistry()
	var wg sync.WaitGroup

	// Concurrently update heartbeats
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			r.UpdateHeartbeat(&pb.Heartbeat{
				WorkerId: "worker", // Same ID to test race on map write
				CpuLoad:  float32(id),
			})
		}(i)
	}

	// Concurrently read snapshots
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.Snapshot()
		}()
	}

	wg.Wait()
	
	// Ensure map is still valid
	if len(r.Snapshot()) != 1 {
		t.Errorf("expected 1 worker after concurrent updates")
	}
}
