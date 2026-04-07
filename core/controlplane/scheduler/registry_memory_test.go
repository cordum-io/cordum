package scheduler_test

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/scheduler"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/encoding/protowire"
)

func handshakeWithReadyTopics(componentID string, topics ...string) *pb.Handshake {
	hs := &pb.Handshake{
		ComponentId: componentID,
		Role:        pb.ComponentRole_COMPONENT_ROLE_WORKER,
	}
	if len(topics) == 0 {
		return hs
	}
	raw := append([]byte{}, hs.ProtoReflect().GetUnknown()...)
	for _, topic := range topics {
		raw = protowire.AppendTag(raw, 6, protowire.BytesType)
		raw = protowire.AppendString(raw, topic)
	}
	hs.ProtoReflect().SetUnknown(raw)
	return hs
}

func TestMemoryRegistry_UpdateHeartbeat(t *testing.T) {
	r := scheduler.NewMemoryRegistry()
	t.Cleanup(r.Close)

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
	t.Cleanup(r.Close)

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
	t.Cleanup(r.Close)
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

	// Poll until TTL expires and worker is removed.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		total, _ := r.Stats()
		if total == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
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
	t.Cleanup(r.Close)

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

func TestMemoryRegistry_ReadinessSetOnHandshake(t *testing.T) {
	r := scheduler.NewMemoryRegistry()
	t.Cleanup(r.Close)

	r.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "worker-ready", Pool: "default"})
	r.UpdateHandshake(handshakeWithReadyTopics("worker-ready", "job.default", "job.other"))

	readiness := r.ReadinessSnapshot()
	state, ok := readiness["worker-ready"]
	if !ok {
		t.Fatal("expected worker-ready in readiness snapshot")
	}
	if !state.Ready {
		t.Fatal("expected worker to be ready after handshake")
	}
	if !reflect.DeepEqual(state.ReadyTopics, []string{"job.default", "job.other"}) {
		t.Fatalf("unexpected ready topics: %#v", state.ReadyTopics)
	}

	r.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "worker-ready", Pool: "default", ActiveJobs: 1})
	state = r.ReadinessSnapshot()["worker-ready"]
	if !state.Ready {
		t.Fatal("expected readiness to survive heartbeat updates")
	}
}

func TestMemoryRegistry_ReadinessTTLExpiry(t *testing.T) {
	t.Setenv("WORKER_READINESS_TTL", "20ms")
	r := scheduler.NewMemoryRegistryWithTTL(500 * time.Millisecond)
	t.Cleanup(r.Close)

	r.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "worker-ttl", Pool: "default"})
	r.UpdateHandshake(handshakeWithReadyTopics("worker-ttl", "job.default"))

	if !r.ReadinessSnapshot()["worker-ttl"].Ready {
		t.Fatal("expected worker to start ready")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !r.ReadinessSnapshot()["worker-ttl"].Ready {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if !r.IsAlive("worker-ttl") {
		t.Fatal("expected worker heartbeat to remain alive after readiness expiry")
	}
	if state := r.ReadinessSnapshot()["worker-ttl"]; state.Ready {
		t.Fatalf("expected readiness to expire, got %#v", state)
	}
	if len(r.Snapshot()) != 1 {
		t.Fatalf("expected worker heartbeat to remain in snapshot, got %d workers", len(r.Snapshot()))
	}
}

func TestMemoryRegistry_ReadinessClearedOnEmptyTopics(t *testing.T) {
	r := scheduler.NewMemoryRegistry()
	t.Cleanup(r.Close)

	r.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "worker-empty", Pool: "default"})
	r.UpdateHandshake(handshakeWithReadyTopics("worker-empty", "job.default"))
	if !r.ReadinessSnapshot()["worker-empty"].Ready {
		t.Fatal("expected worker to be ready before clearing topics")
	}

	r.UpdateHandshake(handshakeWithReadyTopics("worker-empty"))
	state := r.ReadinessSnapshot()["worker-empty"]
	if state.Ready {
		t.Fatalf("expected readiness to clear on empty topics, got %#v", state)
	}
	if len(state.ReadyTopics) != 0 {
		t.Fatalf("expected ready topics to be cleared, got %#v", state.ReadyTopics)
	}
}
