package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestActiveHandshakeDispatchRequiresTrustedCredentialAuthority(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		trusted       bool
		allowedPools  []string
		allowedTopics []string
		wantPublish   bool
	}{
		"untrusted readiness": {false, []string{"default"}, []string{"job.default"}, false},
		"pool denied":         {true, []string{"restricted"}, []string{"job.default"}, false},
		"topic denied":        {true, []string{"default"}, []string{"job.other"}, false},
		"fully authorized":    {true, []string{"default"}, []string{"job.default"}, true},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			bus := &fakeBus{}
			registry := newTestRegistry(t)
			registry.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "worker-1", Pool: "default"})
			registry.UpdateHandshakeTrust(capabilityHandshake("worker-1", "node/1", "job.default"), test.trusted)
			cache := activeAuthorityCache(test.allowedPools, test.allowedTopics)
			engine := NewEngine(bus, NewSafetyBasic(), registry,
				NewLeastLoadedStrategy(routingForTopic("job.default", "default")), newFakeJobStore(), nil,
			).WithWorkerCredentialCache(cache).WithSessionMiddleware(
				NewSessionTokenMiddleware(nil, HandshakeModeEnforce, NewHandshakeMissingTracker()),
			)

			err := engine.processJob(testCtx(t), &pb.JobRequest{JobId: "job-1", Topic: "job.default"}, "trace-1")
			published := len(bus.snapshotPublished())
			if test.wantPublish && (err != nil || published != 1) {
				t.Fatalf("authorized dispatch err=%v publishes=%d", err, published)
			}
			if !test.wantPublish && (err == nil || published != 0) {
				t.Fatalf("unauthorized dispatch err=%v publishes=%d", err, published)
			}
		})
	}
}

func TestActiveHandshakeConfigRefreshRequiresReservedServiceToken(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	tests := map[string]struct {
		token       func() string
		wantRefresh bool
	}{
		"tokenless": {func() string { return "" }, false},
		"worker session": {func() string {
			token, _, err := issuer.IssueBound(context.Background(), boundaryBinding())
			if err != nil {
				t.Fatalf("issue worker token: %v", err)
			}
			return token
		}, false},
		"reserved service": {func() string {
			token, err := issuer.MintServiceToken(servicetoken.IdentityGateway)
			if err != nil {
				t.Fatalf("mint service token: %v", err)
			}
			return token
		}, true},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			called := make(chan struct{}, 1)
			cache := NewWorkerCredentialCache(nil)
			cache.list = func(context.Context) ([]workercredentials.Credential, error) {
				called <- struct{}{}
				return nil, nil
			}
			middleware := NewSessionTokenMiddleware(issuer, HandshakeModeWarn, NewHandshakeMissingTracker())
			engine := boundaryEngine(NewMemoryRegistryWithTTL(time.Minute), middleware, cache)
			t.Cleanup(func() { engine.registry.(*MemoryRegistry).Close() })
			packet := configChangeSecurityPacket(test.token())
			if err := engine.handleConfigChangedPacket(packet); err != nil {
				t.Fatalf("handle config change: %v", err)
			}
			assertRefreshCall(t, called, test.wantRefresh)
		})
	}
}

func TestActiveHandshakeHeartbeatRequiresBoundCredentialPool(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	tests := map[string]struct {
		allowed []string
		want    bool
	}{
		"pool denied":  {[]string{"restricted"}, false},
		"pool allowed": {[]string{"default"}, true},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			registry := NewMemoryRegistryWithTTL(time.Minute)
			t.Cleanup(registry.Close)
			cache := activeAuthorityCache(test.allowed, []string{"job.default"})
			middleware := NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker())
			engine := boundaryEngine(registry, middleware, cache)
			token, _, err := issuer.IssueBound(context.Background(), boundaryBinding())
			if err != nil {
				t.Fatalf("issue bound token: %v", err)
			}
			packet := boundaryPacket("worker-1")
			packet.AuthToken = token
			packet.Payload = &pb.BusPacket_Heartbeat{Heartbeat: &pb.Heartbeat{WorkerId: "worker-1", Pool: "default"}}
			if err := engine.HandlePacket(packet); err != nil {
				t.Fatalf("HandlePacket: %v", err)
			}
			_, present := registry.Snapshot()["worker-1"]
			if present != test.want {
				t.Fatalf("heartbeat present=%v, want %v", present, test.want)
			}
		})
	}
}

func activeAuthorityCache(pools, topics []string) *WorkerCredentialCache {
	cache := NewWorkerCredentialCache(nil)
	record := workercredentials.Credential{
		WorkerID: "worker-1", TenantID: "tenant-1", AgentID: "agent-1", ProofKeyID: "proof-1",
		AllowedPools: append([]string(nil), pools...), AllowedTopics: append([]string(nil), topics...),
	}
	cache.authority = map[string]workercredentials.Credential{"worker-1": record}
	cache.authorityReady = true
	cache.list = func(context.Context) ([]workercredentials.Credential, error) {
		return []workercredentials.Credential{cloneCredentialRecord(record)}, nil
	}
	return cache
}

func configChangeSecurityPacket(token string) *pb.BusPacket {
	packet := boundaryPacket(servicetoken.IdentityGateway)
	packet.AuthToken = token
	packet.Payload = &pb.BusPacket_Alert{Alert: &pb.SystemAlert{
		Message: "config changed", Details: map[string]string{"scope": "system", "scope_id": "workers"},
	}}
	return packet
}

func assertRefreshCall(t *testing.T, called <-chan struct{}, want bool) {
	t.Helper()
	select {
	case <-called:
		if !want {
			t.Fatal("unauthorized config change refreshed authority")
		}
	case <-time.After(200 * time.Millisecond):
		if want {
			t.Fatal("reserved service config change did not refresh authority")
		}
	}
}
