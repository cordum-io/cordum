package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/workercredentials"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestActiveDispatchRefreshesRevokedTopicWithoutNotification(t *testing.T) {
	stale := refreshTestCredential([]string{"default"}, []string{"job.default"})
	current := refreshTestCredential([]string{"default"}, []string{"job.other"})
	engine, calls := refreshTestEngine(stale, current)
	workers := map[string]*pb.Heartbeat{"worker-1": {WorkerId: "worker-1", Pool: "default"}}
	readiness := map[string]WorkerReadiness{
		"worker-1": {Ready: true, Trusted: true, ReadyTopics: []string{"job.default"}},
	}

	if authorized := engine.filterBoundWorkers(workers, readiness, "job.default"); len(authorized) != 0 {
		t.Fatal("dispatch used a stale allowed topic after canonical revocation")
	}
	if calls.Load() != 1 {
		t.Fatalf("canonical refresh calls=%d want=1 per dispatch filter", calls.Load())
	}
}

func TestActiveCapabilityRefreshesRevokedTopicWithoutNotification(t *testing.T) {
	stale := refreshTestCredential([]string{"default"}, []string{"job.default"})
	current := refreshTestCredential([]string{"default"}, []string{"job.other"})
	engine, calls := refreshTestEngine(stale, current)
	handshake := capabilityHandshake("worker-1", "node/1", "job.default")

	trusted, ok := engine.authorizedCapability(handshake, refreshTestClaims())
	if !ok || trusted == nil {
		t.Fatal("canonical identity binding was unexpectedly rejected")
	}
	if len(trusted.GetReadyTopics()) != 0 {
		t.Fatalf("capability retained revoked ready topics: %v", trusted.GetReadyTopics())
	}
	if calls.Load() != 1 {
		t.Fatalf("canonical refresh calls=%d want=1 for capability authorization", calls.Load())
	}
}

func TestActiveHeartbeatRefreshesRevokedPoolWithoutNotification(t *testing.T) {
	stale := refreshTestCredential([]string{"default"}, []string{"job.default"})
	current := refreshTestCredential([]string{"restricted"}, []string{"job.default"})
	engine, calls := refreshTestEngine(stale, current)
	heartbeat := &pb.Heartbeat{WorkerId: "worker-1", Pool: "default"}

	if engine.allowActiveHeartbeatPool(heartbeat, refreshTestClaims()) {
		t.Fatal("heartbeat used a stale allowed pool after canonical revocation")
	}
	if calls.Load() != 1 {
		t.Fatalf("canonical refresh calls=%d want=1 for heartbeat authorization", calls.Load())
	}
}

func TestActiveAuthorizationFailsClosedDuringOverlappingRefresh(t *testing.T) {
	stale := refreshTestCredential([]string{"default"}, []string{"job.default"})
	current := refreshTestCredential([]string{"restricted"}, []string{"job.other"})
	engine, calls := refreshTestEngine(stale, current)
	started, release := make(chan struct{}), make(chan struct{})
	engine.workerCredentialCache.list = func(context.Context) ([]workercredentials.Credential, error) {
		calls.Add(1)
		close(started)
		<-release
		return []workercredentials.Credential{current}, nil
	}
	defer closeIfOpen(release)

	filtered := make(chan map[string]*pb.Heartbeat, 1)
	go func() {
		workers := map[string]*pb.Heartbeat{"worker-1": {WorkerId: "worker-1", Pool: "default"}}
		ready := map[string]WorkerReadiness{"worker-1": {Ready: true, Trusted: true, ReadyTopics: []string{"job.default"}}}
		filtered <- engine.filterBoundWorkers(workers, ready, "job.default")
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("dispatch did not synchronously refresh canonical authority")
	}
	if _, ok := engine.authorizedCapability(capabilityHandshake("worker-1", "node/1", "job.default"), refreshTestClaims()); ok {
		t.Fatal("capability admitted authority during overlapping refresh")
	}
	if engine.allowActiveHeartbeatPool(&pb.Heartbeat{WorkerId: "worker-1", Pool: "default"}, refreshTestClaims()) {
		t.Fatal("heartbeat admitted authority during overlapping refresh")
	}
	close(release)
	if authorized := <-filtered; len(authorized) != 0 {
		t.Fatal("dispatch admitted canonically revoked authority")
	}
	if calls.Load() != 1 {
		t.Fatalf("overlapping canonical list calls=%d want=1", calls.Load())
	}
}

func refreshTestEngine(stale, current workercredentials.Credential) (*Engine, *atomic.Int32) {
	cache := NewWorkerCredentialCache(nil)
	cache.authority = map[string]workercredentials.Credential{stale.WorkerID: stale}
	cache.authorityReady = true
	calls := &atomic.Int32{}
	cache.list = func(context.Context) ([]workercredentials.Credential, error) {
		calls.Add(1)
		return []workercredentials.Credential{current}, nil
	}
	engine := &Engine{
		workerCredentialCache: cache,
		sessionMiddleware:     NewSessionTokenMiddleware(nil, HandshakeModeEnforce, NewHandshakeMissingTracker()),
	}
	return engine, calls
}

func refreshTestCredential(pools, topics []string) workercredentials.Credential {
	return workercredentials.Credential{
		WorkerID: "worker-1", TenantID: "tenant-1", AgentID: "agent-1", ProofKeyID: "proof-1",
		AllowedPools: append([]string(nil), pools...), AllowedTopics: append([]string(nil), topics...),
	}
}

func refreshTestClaims() *SessionTokenClaims {
	return &SessionTokenClaims{
		Subject: "worker-1", Tenant: "tenant-1", AgentID: "agent-1", ProofKeyID: "proof-1",
		Audience: WorkerHandshakeAudience, SDKVersion: "node/1",
	}
}

func closeIfOpen(channel chan struct{}) {
	select {
	case <-channel:
	default:
		close(channel)
	}
}
