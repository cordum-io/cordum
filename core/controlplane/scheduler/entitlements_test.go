package scheduler

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/cordum/cordum/core/licensing"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

type activeCountJobStore struct {
	*fakeJobStore
	active   int
	countErr error
}

func (s *activeCountJobStore) CountActiveByTenant(_ context.Context, _ string) (int, error) {
	if s.countErr != nil {
		return 0, s.countErr
	}
	return s.active, nil
}

func newSchedulerEntitlementResolver(t *testing.T, plan licensing.Plan, mutate func(*licensing.Entitlements)) *licensing.EntitlementResolver {
	t.Helper()

	entitlements := licensing.DefaultEntitlements(plan)
	if mutate != nil {
		mutate(&entitlements)
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}

	payloadBytes, err := json.Marshal(licensing.Claims{
		Plan:         string(plan),
		Entitlements: &entitlements,
	})
	if err != nil {
		t.Fatalf("marshal license payload: %v", err)
	}

	licenseBytes, err := json.Marshal(map[string]any{
		"payload":   json.RawMessage(payloadBytes),
		"signature": base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payloadBytes)),
	})
	if err != nil {
		t.Fatalf("marshal license: %v", err)
	}

	t.Setenv("CORDUM_LICENSE_FILE", "")
	t.Setenv("CORDUM_LICENSE_TOKEN", string(licenseBytes))
	t.Setenv("CORDUM_LICENSE_PUBLIC_KEY_PATH", "")
	t.Setenv("CORDUM_LICENSE_PUBLIC_KEY", base64.StdEncoding.EncodeToString(publicKey))

	resolver := licensing.NewEntitlementResolver()
	resolver.Init()
	return resolver
}

func TestHandlePacketRejectsHeartbeatWhenMaxWorkersExceeded(t *testing.T) {
	registry := newTestRegistry(t)
	for _, workerID := range []string{"worker-1", "worker-2", "worker-3"} {
		registry.UpdateHeartbeat(&pb.Heartbeat{WorkerId: workerID, Pool: "default"})
	}

	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), registry, NewNaiveStrategy(), newFakeJobStore(), nil).
		WithEntitlements(newSchedulerEntitlementResolver(t, licensing.PlanCommunity, nil))

	if err := engine.HandlePacket(newHeartbeatPacket("worker-4", "worker-4", "default", "")); err != nil {
		t.Fatalf("HandlePacket returned error: %v", err)
	}

	snapshot := registry.Snapshot()
	if len(snapshot) != 3 {
		t.Fatalf("expected 3 workers after rejected heartbeat, got %d", len(snapshot))
	}
	if _, ok := snapshot["worker-4"]; ok {
		t.Fatalf("expected worker-4 heartbeat to be rejected at max_workers limit")
	}
}

func TestProcessJobRequeuesWhenTierConcurrencyExceeded(t *testing.T) {
	bus := &fakeBus{}
	registry := newTestRegistry(t)
	registry.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "worker-1", Pool: "default"})

	store := &activeCountJobStore{
		fakeJobStore: newFakeJobStore(),
		active:       3,
	}
	engine := NewEngine(bus, NewSafetyBasic(), registry, NewNaiveStrategy(), store, nil).
		WithEntitlements(newSchedulerEntitlementResolver(t, licensing.PlanCommunity, nil))

	req := &pb.JobRequest{
		JobId:    "job-tier-limit",
		Topic:    "job.default",
		TenantId: "default",
	}
	err := engine.processJob(context.Background(), req, "trace-tier-limit")
	if err == nil {
		t.Fatal("expected retryable tier-limit error")
	}

	var retryErr *retryableError
	if !errors.As(err, &retryErr) {
		t.Fatalf("expected retryable error, got %T: %v", err, err)
	}
	if retryErr.RetryDelay() != retryDelayNoWorkers {
		t.Fatalf("RetryDelay = %s, want %s", retryErr.RetryDelay(), retryDelayNoWorkers)
	}

	var limitErr *licensing.TierLimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("expected tier limit error, got %T: %v", err, err)
	}
	if limitErr.Limit != "max_concurrent_jobs" {
		t.Fatalf("limit = %q, want max_concurrent_jobs", limitErr.Limit)
	}
	if got := reasonCodeForSchedulingError(err); got != "tier_limit_exceeded" {
		t.Fatalf("reasonCodeForSchedulingError() = %q, want tier_limit_exceeded", got)
	}
	if published := bus.snapshotPublished(); len(published) != 0 {
		t.Fatalf("expected no dispatch publish on tier requeue, got %d", len(published))
	}
	if state, _ := store.GetState(context.Background(), req.JobId); state != "" {
		t.Fatalf("expected job state to remain unchanged on requeue, got %q", state)
	}
}
