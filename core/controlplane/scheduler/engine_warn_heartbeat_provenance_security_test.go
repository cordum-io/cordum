package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/workercredentials"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestWarnTokenlessHeartbeatCannotEnterDispatchSnapshot(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	registry := NewMemoryRegistryWithTTL(time.Minute)
	t.Cleanup(registry.Close)
	tracker := NewHandshakeMissingTracker()
	middleware := NewSessionTokenMiddleware(issuer, HandshakeModeWarn, tracker)
	engine := boundaryEngine(
		registry, middleware,
		warnHeartbeatAuthorityCache(),
	)

	if err := engine.HandlePacket(trustedCapabilityPacket(t, issuer, "job.default")); err != nil {
		t.Fatalf("establish trusted capability: %v", err)
	}
	packet := boundaryPacket("worker-1")
	packet.Payload = &pb.BusPacket_Heartbeat{Heartbeat: &pb.Heartbeat{
		WorkerId: "worker-1", Pool: "default",
	}}
	if err := engine.HandlePacket(packet); err != nil {
		t.Fatalf("handle tokenless WARN heartbeat: %v", err)
	}

	if _, present := registry.Snapshot()["worker-1"]; present {
		t.Fatal("tokenless WARN heartbeat entered the dispatch snapshot")
	}
	if tracker.ShouldLog("worker-1") {
		t.Fatal("tokenless WARN heartbeat was not recorded by missing-token observability")
	}
}

func TestWarnBoundHeartbeatStillEntersDispatchSnapshot(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	registry := NewMemoryRegistryWithTTL(time.Minute)
	t.Cleanup(registry.Close)
	engine := boundaryEngine(
		registry,
		NewSessionTokenMiddleware(issuer, HandshakeModeWarn, NewHandshakeMissingTracker()),
		warnHeartbeatAuthorityCache(),
	)
	token, _, err := issuer.IssueBound(context.Background(), boundaryBinding())
	if err != nil {
		t.Fatalf("issue bound session: %v", err)
	}
	packet := boundaryPacket("worker-1")
	packet.AuthToken = token
	packet.Payload = &pb.BusPacket_Heartbeat{Heartbeat: &pb.Heartbeat{
		WorkerId: "worker-1", Pool: "default",
	}}
	if err := engine.HandlePacket(packet); err != nil {
		t.Fatalf("handle bound WARN heartbeat: %v", err)
	}
	if _, present := registry.Snapshot()["worker-1"]; !present {
		t.Fatal("bound WARN heartbeat did not enter the dispatch snapshot")
	}
}

func TestWarnTokenlessHeartbeatCannotReplaceBoundHeartbeat(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	registry := NewMemoryRegistryWithTTL(time.Minute)
	t.Cleanup(registry.Close)
	engine := boundaryEngine(
		registry,
		NewSessionTokenMiddleware(issuer, HandshakeModeWarn, NewHandshakeMissingTracker()),
		warnHeartbeatAuthorityCache(),
	)
	token, _, err := issuer.IssueBound(context.Background(), boundaryBinding())
	if err != nil {
		t.Fatalf("issue bound session: %v", err)
	}
	bound := boundaryPacket("worker-1")
	bound.AuthToken = token
	bound.Payload = &pb.BusPacket_Heartbeat{Heartbeat: &pb.Heartbeat{
		WorkerId: "worker-1", Pool: "default", ActiveJobs: 1,
	}}
	if err := engine.HandlePacket(bound); err != nil {
		t.Fatalf("handle bound WARN heartbeat: %v", err)
	}
	tokenless := boundaryPacket("worker-1")
	tokenless.Payload = &pb.BusPacket_Heartbeat{Heartbeat: &pb.Heartbeat{
		WorkerId: "worker-1", Pool: "attacker", ActiveJobs: 999,
	}}
	if err := engine.HandlePacket(tokenless); err != nil {
		t.Fatalf("handle tokenless WARN heartbeat: %v", err)
	}
	got := registry.Snapshot()["worker-1"]
	if got == nil || got.GetPool() != "default" || got.GetActiveJobs() != 1 {
		t.Fatalf("tokenless heartbeat replaced bound telemetry: %+v", got)
	}
}

func warnHeartbeatAuthorityCache() *WorkerCredentialCache {
	record := workercredentials.Credential{
		WorkerID: "worker-1", TenantID: "tenant-1", AgentID: "agent-1",
		ProofKeyID: "proof-1", AllowedPools: []string{"default"},
		AllowedTopics: []string{"job.default"},
	}
	cache := NewWorkerCredentialCache(nil)
	cache.list = func(context.Context) ([]workercredentials.Credential, error) {
		return []workercredentials.Credential{record}, nil
	}
	return cache
}
