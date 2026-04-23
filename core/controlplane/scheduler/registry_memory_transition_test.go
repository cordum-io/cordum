package scheduler

// Internal-package tests for MemoryRegistry.UpdateHeartbeatWithTransition
// — the new method introduced by task-7a2514ae so the scheduler can
// detect offline→online transitions without waiting for a poll tick.
// Internal package so we can manipulate workerEntry.lastSeen directly,
// avoiding wall-clock sleeps (explicit task rail).

import (
	"testing"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestMemoryRegistry_UpdateHeartbeatWithTransition_ReportsNewWorker(t *testing.T) {
	t.Parallel()
	reg := NewMemoryRegistryWithTTL(50 * time.Millisecond)
	t.Cleanup(reg.Close)

	transition := reg.UpdateHeartbeatWithTransition(&pb.Heartbeat{WorkerId: "w-new", Pool: "p1"})
	if !transition {
		t.Fatal("first heartbeat must report transition=true (no prior entry)")
	}
}

func TestMemoryRegistry_UpdateHeartbeatWithTransition_RefreshIsNotTransition(t *testing.T) {
	t.Parallel()
	reg := NewMemoryRegistryWithTTL(50 * time.Millisecond)
	t.Cleanup(reg.Close)

	_ = reg.UpdateHeartbeatWithTransition(&pb.Heartbeat{WorkerId: "w-refresh", Pool: "p1"})
	transition := reg.UpdateHeartbeatWithTransition(&pb.Heartbeat{WorkerId: "w-refresh", Pool: "p1"})
	if transition {
		t.Fatal("consecutive heartbeat within TTL must report transition=false")
	}
}

func TestMemoryRegistry_UpdateHeartbeatWithTransition_PostExpiryIsTransition(t *testing.T) {
	t.Parallel()
	reg := NewMemoryRegistryWithTTL(50 * time.Millisecond)
	t.Cleanup(reg.Close)

	_ = reg.UpdateHeartbeatWithTransition(&pb.Heartbeat{WorkerId: "w-expire", Pool: "p1"})

	// Rewind the entry's lastSeen past the TTL horizon. Direct state
	// manipulation — no wall-clock sleep.
	reg.mu.Lock()
	if entry, ok := reg.workers["w-expire"]; ok {
		entry.lastSeen = time.Now().Add(-2 * reg.ttl)
	}
	reg.mu.Unlock()

	transition := reg.UpdateHeartbeatWithTransition(&pb.Heartbeat{WorkerId: "w-expire", Pool: "p1"})
	if !transition {
		t.Fatal("heartbeat after TTL expiry must report transition=true")
	}
}

func TestMemoryRegistry_UpdateHeartbeatWithTransition_NilHeartbeatIgnored(t *testing.T) {
	t.Parallel()
	reg := NewMemoryRegistryWithTTL(50 * time.Millisecond)
	t.Cleanup(reg.Close)

	if reg.UpdateHeartbeatWithTransition(nil) {
		t.Fatal("nil heartbeat must return transition=false (no-op)")
	}
}
