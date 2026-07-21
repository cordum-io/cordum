package scheduler

import (
	"sync"
	"testing"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// TestMemoryRegistry_HeartbeatObserversAndForget locks the heartbeat-age
// telemetry fix: the age observer must fire for live workers on the expiry
// sweep (so the gauge reflects growing staleness rather than ~0 at receive),
// and the forget observer must fire when a worker expires (so its gauge
// series is cleared instead of frozen at its last value forever).
func TestMemoryRegistry_HeartbeatObserversAndForget(t *testing.T) {
	r := NewMemoryRegistryWithTTL(30 * time.Second)
	r.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "live"})
	r.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "dead"})
	// Stop the background expiry loop so the manual sweep below is the only
	// one and the assertions are deterministic.
	r.Close()

	// Backdate the "dead" worker beyond the TTL so the sweep expires it.
	r.mu.Lock()
	if entry := r.workers["dead"]; entry != nil {
		entry.lastSeen = time.Now().Add(-time.Hour)
	}
	r.mu.Unlock()

	var mu sync.Mutex
	aged := map[string]bool{}
	forgot := map[string]bool{}
	r.SetHeartbeatObservers(
		func(id string, _ time.Time, _ time.Time) { mu.Lock(); aged[id] = true; mu.Unlock() },
		func(id string) { mu.Lock(); forgot[id] = true; mu.Unlock() },
	)

	r.expire()

	mu.Lock()
	defer mu.Unlock()
	if !aged["live"] {
		t.Errorf("ageObserver was not invoked for the live worker")
	}
	if aged["dead"] {
		t.Errorf("ageObserver fired for an expired worker (should be skipped)")
	}
	if !forgot["dead"] {
		t.Errorf("forgetObserver was not invoked for the expired worker — its gauge series would freeze")
	}
	if forgot["live"] {
		t.Errorf("forgetObserver fired for a live worker")
	}
}
