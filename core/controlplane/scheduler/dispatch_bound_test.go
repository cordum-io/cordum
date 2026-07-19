package scheduler

import (
	"context"
	"sync"
	"testing"

	"github.com/cordum/cordum/core/controlplane/workercredentials"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
)

type dispatchCredentialResolver struct {
	mu      sync.RWMutex
	records map[string]workercredentials.Credential
}

func (r *dispatchCredentialResolver) GetByWorkerID(_ context.Context, workerID string) (*workercredentials.Credential, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	record, ok := r.records[workerID]
	if !ok {
		return nil, nil
	}
	copy := record
	return &copy, nil
}

func (r *dispatchCredentialResolver) add(workerID, tenant string) SessionBinding {
	binding := SessionBinding{
		WorkerID: workerID, AgentID: "agent-" + workerID, Tenant: tenant,
		Audience: WorkerHandshakeAudience, ProofKeyID: "proof-" + workerID, SDKVersion: "v1",
	}
	r.mu.Lock()
	r.records[workerID] = workercredentials.Credential{
		WorkerID: workerID, TenantID: tenant, AgentID: binding.AgentID, ProofKeyID: binding.ProofKeyID,
	}
	r.mu.Unlock()
	return binding
}

func newBoundDispatchResolverForTest(t *testing.T, rdb redis.UniversalClient) (*TrustResolver, *dispatchCredentialResolver) {
	t.Helper()
	credentials := &dispatchCredentialResolver{records: make(map[string]workercredentials.Credential)}
	resolver, err := NewBoundTrustResolver(rdb, credentials)
	if err != nil {
		t.Fatal(err)
	}
	return resolver, credentials
}

func issueDispatchSession(t *testing.T, ctx context.Context, issuer *SessionTokenIssuer, credentials *dispatchCredentialResolver, workerID, tenant string) SessionTokenClaims {
	t.Helper()
	binding := credentials.add(workerID, tenant)
	_, claims, err := issuer.IssueBound(ctx, binding)
	if err != nil {
		t.Fatalf("issue bound session for %s: %v", workerID, err)
	}
	return claims
}

func TestDispatchGateSessionModesFailClosedWithoutBoundAuthority(t *testing.T) {
	reg := NewMemoryRegistry()
	defer reg.Close()
	reg.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "worker-a", Pool: "pool-a"})
	_, _, rdb, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()

	for name, resolver := range map[string]*TrustResolver{
		"nil":     nil,
		"unbound": NewTrustResolver(rdb),
	} {
		for _, mode := range []HeartbeatMode{HeartbeatModeWarn, HeartbeatModeTelemetry} {
			t.Run(name+"/"+mode.String(), func(t *testing.T) {
				gate := NewDispatchGate(resolver, mode)
				if !gate.EnforcesSession() {
					t.Fatal("active session mode reported enforcement disabled")
				}
				workers, _ := gate.EligibleWorkers(context.Background(), reg)
				if len(workers) != 0 {
					t.Fatalf("missing bound authority passed workers through: %+v", workers)
				}
				if eligible, _ := gate.IsWorkerEligible(context.Background(), "worker-a", reg.IsAlive); eligible {
					t.Fatal("missing bound authority admitted worker")
				}
			})
		}
	}
}
