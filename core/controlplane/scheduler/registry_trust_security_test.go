package scheduler

import (
	"testing"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestMemoryRegistryUntrustedHandshakeCannotGrantOrOverwriteReadiness(t *testing.T) {
	t.Parallel()
	registry := NewMemoryRegistryWithTTL(time.Minute)
	t.Cleanup(registry.Close)
	registry.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "worker-1", Pool: "default"})
	registry.UpdateHandshakeTrust(capabilityHandshake("worker-1", "node/1", "jobs.allowed"), true)
	registry.UpdateHandshakeTrust(capabilityHandshake("worker-1", "node/1", "jobs.evil"), false)

	state := registry.ReadinessSnapshot()["worker-1"]
	if !state.Trusted || !state.Ready {
		t.Fatalf("trusted readiness lost: %+v", state)
	}
	if len(state.ReadyTopics) != 1 || state.ReadyTopics[0] != "jobs.allowed" {
		t.Fatalf("untrusted handshake overwrote readiness: %+v", state.ReadyTopics)
	}
}

func TestMemoryRegistryUntrustedHandshakeIsTelemetryOnly(t *testing.T) {
	t.Parallel()
	registry := NewMemoryRegistryWithTTL(time.Minute)
	t.Cleanup(registry.Close)
	registry.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "worker-1", Pool: "default"})
	registry.UpdateHandshakeTrust(capabilityHandshake("worker-1", "node/1", "jobs.allowed"), false)

	state := registry.ReadinessSnapshot()["worker-1"]
	if state.Trusted || state.Ready || len(state.ReadyTopics) != 0 {
		t.Fatalf("untrusted handshake granted readiness: %+v", state)
	}
	registry.mu.RLock()
	handshake := registry.workers["worker-1"].handshake
	registry.mu.RUnlock()
	if handshake == nil {
		t.Fatal("untrusted capability advertisement was not retained as telemetry")
	}
}

func TestMemoryRegistryHandshakeCannotRefreshHeartbeatLiveness(t *testing.T) {
	t.Parallel()
	for _, trusted := range []bool{false, true} {
		trusted := trusted
		t.Run(map[bool]string{false: "untrusted", true: "trusted"}[trusted], func(t *testing.T) {
			registry := NewMemoryRegistryWithTTL(time.Minute)
			t.Cleanup(registry.Close)
			registry.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "worker-1", Pool: "default"})
			registry.mu.Lock()
			registry.workers["worker-1"].lastSeen = time.Now().Add(-2 * time.Minute)
			registry.mu.Unlock()

			registry.UpdateHandshakeTrust(capabilityHandshake("worker-1", "node/1", "jobs.allowed"), trusted)

			if registry.IsAlive("worker-1") {
				t.Fatal("handshake refreshed stale heartbeat liveness")
			}
			if _, ok := registry.Snapshot()["worker-1"]; ok {
				t.Fatal("handshake restored stale worker to live snapshot")
			}
			if _, ok := registry.ReadinessSnapshot()["worker-1"]; ok {
				t.Fatal("handshake restored stale worker readiness")
			}
		})
	}
}

func capabilityHandshake(workerID, sdkVersion string, topics ...string) *pb.Handshake {
	return &pb.Handshake{
		ComponentId: workerID, Role: pb.ComponentRole_COMPONENT_ROLE_WORKER,
		SdkVersion: sdkVersion, SupportedVersions: []int32{1}, ReadyTopics: topics,
	}
}
