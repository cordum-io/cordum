package scheduler_test

import (
	"sync"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/scheduler"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
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

func TestMemoryRegistryDoubleCloseNoPanic(t *testing.T) {
	r := scheduler.NewMemoryRegistry()
	r.Close()
	r.Close() // must not panic
}

func TestMemoryRegistry_StatsEmpty(t *testing.T) {
	r := scheduler.NewMemoryRegistry()
	defer r.Close()

	total, byPool := r.Stats()
	if total != 0 {
		t.Fatalf("expected 0 total workers, got %d", total)
	}
	if len(byPool) != 0 {
		t.Fatalf("expected empty pool map, got %v", byPool)
	}
}

func TestMemoryRegistry_StatsMultiplePools(t *testing.T) {
	r := scheduler.NewMemoryRegistry()
	defer r.Close()

	r.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w1", Pool: "gpu"})
	r.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w2", Pool: "gpu"})
	r.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w3", Pool: "cpu"})
	r.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w4", Pool: "cpu"})
	r.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w5", Pool: "cpu"})

	total, byPool := r.Stats()
	if total != 5 {
		t.Fatalf("expected 5 total workers, got %d", total)
	}
	if byPool["gpu"] != 2 {
		t.Fatalf("expected 2 gpu workers, got %d", byPool["gpu"])
	}
	if byPool["cpu"] != 3 {
		t.Fatalf("expected 3 cpu workers, got %d", byPool["cpu"])
	}
}

func TestMemoryRegistry_StatsExcludesExpired(t *testing.T) {
	r := scheduler.NewMemoryRegistryWithTTL(10 * time.Millisecond)
	defer r.Close()

	r.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w-stale", Pool: "A"})

	// Wait for TTL to expire
	time.Sleep(30 * time.Millisecond)

	total, byPool := r.Stats()
	if total != 0 {
		t.Fatalf("expected 0 total workers after expiry, got %d", total)
	}
	if len(byPool) != 0 {
		t.Fatalf("expected empty pool map after expiry, got %v", byPool)
	}
}

func TestMemoryRegistry_StatsEmptyPoolName(t *testing.T) {
	r := scheduler.NewMemoryRegistry()
	defer r.Close()

	r.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w1", Pool: ""})
	r.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w2", Pool: "gpu"})

	total, byPool := r.Stats()
	if total != 2 {
		t.Fatalf("expected 2 total workers, got %d", total)
	}
	if byPool["(none)"] != 1 {
		t.Fatalf("expected 1 worker in (none) pool, got %d", byPool["(none)"])
	}
	if byPool["gpu"] != 1 {
		t.Fatalf("expected 1 worker in gpu pool, got %d", byPool["gpu"])
	}
}

func TestMemoryRegistry_ExpiresStaleWorkers(t *testing.T) {
	r := scheduler.NewMemoryRegistryWithTTL(10 * time.Millisecond)

	r.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w-expire", Pool: "A"})

	// Poll until the expire loop removes the stale worker.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(r.Snapshot()) == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	snapshot := r.Snapshot()
	if len(snapshot) != 0 {
		t.Fatalf("expected worker to expire, found %d", len(snapshot))
	}
}
