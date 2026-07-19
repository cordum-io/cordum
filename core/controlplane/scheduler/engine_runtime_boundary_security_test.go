package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/workercredentials"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestEngineRejectsInvalidEnvelopeBeforeRegistryMutation(t *testing.T) {
	t.Parallel()
	tests := map[string]func(*pb.BusPacket){
		"missing trace":       func(packet *pb.BusPacket) { packet.TraceId = "" },
		"unsupported version": func(packet *pb.BusPacket) { packet.ProtocolVersion = 2 },
		"sender mismatch":     func(packet *pb.BusPacket) { packet.SenderId = "other-worker" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			registry := NewMemoryRegistryWithTTL(time.Minute)
			t.Cleanup(registry.Close)
			engine := boundaryEngine(registry, nil, nil)
			packet := boundaryPacket("worker-1")
			packet.Payload = &pb.BusPacket_Heartbeat{Heartbeat: &pb.Heartbeat{
				WorkerId: "worker-1", Pool: "default",
			}}
			mutate(packet)

			if err := engine.HandlePacket(packet); err != nil {
				t.Fatalf("HandlePacket returned retryable error: %v", err)
			}
			if _, ok := registry.Snapshot()["worker-1"]; ok {
				t.Fatal("invalid envelope mutated worker registry")
			}
		})
	}
}

func TestHandlePacketWithContextRejectsBeforeTraceContextMutation(t *testing.T) {
	t.Parallel()
	registry := NewMemoryRegistryWithTTL(time.Minute)
	t.Cleanup(registry.Close)
	engine := boundaryEngine(registry, nil, nil)
	packet := boundaryPacket("worker-1")
	packet.TraceId = ""
	packet.Payload = &pb.BusPacket_Heartbeat{Heartbeat: &pb.Heartbeat{WorkerId: "worker-1"}}
	incoming := context.WithValue(context.Background(), boundaryContextKey{}, "untrusted")

	if err := engine.HandlePacketWithContext(incoming, packet); err != nil {
		t.Fatalf("HandlePacketWithContext: %v", err)
	}
	engine.traceCtxMu.Lock()
	stored := engine.lastTraceCtx
	engine.traceCtxMu.Unlock()
	if stored != nil {
		t.Fatal("invalid packet mutated trace context before validation")
	}
}

func TestEngineWarnTokenlessCapabilityIsTelemetryOnly(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	registry := NewMemoryRegistryWithTTL(time.Minute)
	t.Cleanup(registry.Close)
	registry.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "worker-1", Pool: "default"})
	middleware := NewSessionTokenMiddleware(issuer, HandshakeModeWarn, NewHandshakeMissingTracker())
	engine := boundaryEngine(registry, middleware, credentialCacheForBoundary())
	packet := boundaryPacket("worker-1")
	packet.Payload = &pb.BusPacket_Handshake{
		Handshake: capabilityHandshake("worker-1", "node/1", "jobs.allowed"),
	}
	engine.HandlePacket(packet)

	state := registry.ReadinessSnapshot()["worker-1"]
	if state.Ready || state.Trusted || len(state.ReadyTopics) != 0 {
		t.Fatalf("warn tokenless capability granted authority: %+v", state)
	}
	registry.mu.RLock()
	handshake := registry.workers["worker-1"].handshake
	registry.mu.RUnlock()
	if handshake == nil {
		t.Fatal("warn capability was not retained as telemetry")
	}
}

func TestEngineTrustedCapabilityIntersectsCredentialTopics(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	registry := NewMemoryRegistryWithTTL(time.Minute)
	t.Cleanup(registry.Close)
	registry.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "worker-1", Pool: "default"})
	middleware := NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker())
	engine := boundaryEngine(registry, middleware, credentialCacheForBoundary())
	packet := trustedCapabilityPacket(t, issuer, "jobs.allowed", "jobs.evil")

	if err := engine.HandlePacket(packet); err != nil {
		t.Fatalf("HandlePacket: %v", err)
	}
	state := registry.ReadinessSnapshot()["worker-1"]
	if !state.Trusted || !state.Ready {
		t.Fatalf("valid bound capability was not trusted: %+v", state)
	}
	if len(state.ReadyTopics) != 1 || state.ReadyTopics[0] != "jobs.allowed" {
		t.Fatalf("ready topics = %v, want credential intersection", state.ReadyTopics)
	}
}

func TestEngineEnforceRejectsCapabilityWithMismatchedBoundClaims(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	registry := NewMemoryRegistryWithTTL(time.Minute)
	t.Cleanup(registry.Close)
	middleware := NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker())
	engine := boundaryEngine(registry, middleware, credentialCacheForBoundary())
	binding := boundaryBinding()
	binding.AgentID = "other-agent"
	token, _, err := issuer.IssueBound(context.Background(), binding)
	if err != nil {
		t.Fatalf("issue mismatched token: %v", err)
	}
	packet := boundaryPacket("worker-1")
	packet.Payload = &pb.BusPacket_Handshake{
		Handshake: capabilityHandshake("worker-1", "node/1", "jobs.allowed"),
	}
	packet.AuthToken = token
	engine.HandlePacket(packet)

	registry.mu.RLock()
	_, recorded := registry.workers["worker-1"]
	registry.mu.RUnlock()
	if recorded {
		t.Fatal("enforce mode recorded capability with mismatched authority")
	}
}

func TestConfigChangeRejectsInvalidEnvelopeBeforeRefresh(t *testing.T) {
	t.Parallel()
	called := make(chan struct{}, 1)
	cache := NewWorkerCredentialCache(nil)
	cache.list = func(context.Context) ([]workercredentials.Credential, error) {
		called <- struct{}{}
		return nil, nil
	}
	engine := boundaryEngine(NewMemoryRegistryWithTTL(time.Minute), nil, cache)
	t.Cleanup(func() { engine.registry.(*MemoryRegistry).Close() })
	packet := boundaryPacket("config-service")
	packet.Payload = &pb.BusPacket_Alert{Alert: &pb.SystemAlert{
		Message: "config changed", Details: map[string]string{"scope": "system", "scope_id": "workers"},
	}}
	packet.ProtocolVersion = 2
	if err := engine.handleConfigChangedPacket(packet); err != nil {
		t.Fatalf("handleConfigChangedPacket: %v", err)
	}
	select {
	case <-called:
		t.Fatal("invalid config-change envelope refreshed security authority")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestNeedsWorkerCredentialAuthorityIncludesActiveHandshakeMode(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	active := &Engine{sessionMiddleware: NewSessionTokenMiddleware(
		issuer, HandshakeModeWarn, NewHandshakeMissingTracker(),
	)}
	if !active.needsWorkerCredentialAuthority() {
		t.Fatal("active handshake mode must initialize credential authority")
	}
	if (&Engine{}).needsWorkerCredentialAuthority() {
		t.Fatal("fully disabled trust modes must not require credential authority")
	}
	legacyAttestation := &Engine{workerAttestation: WorkerAttestationEnforce}
	if !legacyAttestation.needsWorkerCredentialAuthority() {
		t.Fatal("legacy worker attestation still requires credential authority")
	}
}

func TestEngineStartRefreshesCredentialsForActiveHandshakeMode(t *testing.T) {
	t.Parallel()
	calls := 0
	cache := NewWorkerCredentialCache(nil)
	cache.list = func(context.Context) ([]workercredentials.Credential, error) {
		calls++
		return nil, nil
	}
	bus := &recordingBus{}
	engine := NewEngine(
		bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), newFakeJobStore(), nil,
	).WithWorkerCredentialCache(cache).WithSessionMiddleware(
		NewSessionTokenMiddleware(nil, HandshakeModeWarn, NewHandshakeMissingTracker()),
	)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(engine.Stop)
	if calls != 1 {
		t.Fatalf("credential refresh calls = %d, want 1", calls)
	}
	foundConfigSubscription := false
	for _, subscription := range bus.subs {
		foundConfigSubscription = foundConfigSubscription || subscription.subject == "sys.config.changed"
	}
	if !foundConfigSubscription {
		t.Fatal("active handshake mode did not subscribe to credential changes")
	}
}

func boundaryEngine(registry WorkerRegistry, middleware *SessionTokenMiddleware, cache *WorkerCredentialCache) *Engine {
	return &Engine{ctx: context.Background(), registry: registry, sessionMiddleware: middleware, workerCredentialCache: cache}
}

type boundaryContextKey struct{}

func boundaryPacket(sender string) *pb.BusPacket {
	return &pb.BusPacket{
		TraceId: "trace-boundary", SenderId: sender, ProtocolVersion: 1,
		CreatedAt: timestamppb.Now(),
	}
}

func credentialCacheForBoundary() *WorkerCredentialCache {
	cache := NewWorkerCredentialCache(nil)
	record := workercredentials.Credential{
		WorkerID: "worker-1", TenantID: "tenant-1", AgentID: "agent-1",
		ProofKeyID: "proof-1", AllowedTopics: []string{"jobs.allowed"},
	}
	cache.records["worker-1"] = record
	cache.authority["worker-1"] = record
	cache.authorityReady = true
	cache.list = func(context.Context) ([]workercredentials.Credential, error) {
		return []workercredentials.Credential{cloneCredentialRecord(record)}, nil
	}
	return cache
}

func boundaryBinding() SessionBinding {
	return SessionBinding{
		WorkerID: "worker-1", AgentID: "agent-1", Tenant: "tenant-1",
		Audience: WorkerHandshakeAudience, ProofKeyID: "proof-1", SDKVersion: "node/1",
	}
}

func trustedCapabilityPacket(t *testing.T, issuer *SessionTokenIssuer, topics ...string) *pb.BusPacket {
	t.Helper()
	token, _, err := issuer.IssueBound(context.Background(), boundaryBinding())
	if err != nil {
		t.Fatalf("issue bound token: %v", err)
	}
	packet := boundaryPacket("worker-1")
	packet.Payload = &pb.BusPacket_Handshake{
		Handshake: capabilityHandshake("worker-1", "node/1", topics...),
	}
	packet.AuthToken = token
	return packet
}
