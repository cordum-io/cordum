package scheduler

import (
	"context"
	"errors"
	"testing"

	"github.com/cordum/cordum/core/controlplane/workercredentials"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

type dispatchTenantMode struct {
	name        string
	handshake   HandshakeMode
	attestation WorkerAttestationMode
}

type dispatchTenantCase struct {
	name         string
	jobTenant    string
	workerTenant string
	twoWorkers   bool
	unavailable  bool
	wantWorker   string
}

func TestDispatchTenantBound(t *testing.T) {
	t.Parallel()
	modes := []dispatchTenantMode{
		{"attestation-enforce", HandshakeModeOff, WorkerAttestationEnforce},
		{"handshake-warn", HandshakeModeWarn, WorkerAttestationOff},
		{"handshake-enforce", HandshakeModeEnforce, WorkerAttestationOff},
		{"off", HandshakeModeOff, WorkerAttestationOff},
		{"attestation-warn", HandshakeModeOff, WorkerAttestationWarn},
	}
	cases := []dispatchTenantCase{
		{name: "own-tenant", jobTenant: "tenant-a", workerTenant: "tenant-a", wantWorker: "worker-a"},
		{name: "two-tenants", jobTenant: "tenant-b", workerTenant: "tenant-a", twoWorkers: true, wantWorker: "worker-b"},
		{name: "other-tenant", jobTenant: "tenant-b", workerTenant: "tenant-a"},
		{name: "empty-job-tenant", workerTenant: "tenant-a"},
		{name: "empty-tenants"},
		{name: "authority-unavailable", jobTenant: "tenant-a", workerTenant: "tenant-a", unavailable: true},
	}
	for _, mode := range modes {
		t.Run(mode.name, func(t *testing.T) {
			modeCases := cases
			if mode.handshake == HandshakeModeOff && !mode.attestation.Enforced() {
				modeCases = []dispatchTenantCase{{name: "any-tenant", jobTenant: "tenant-b", wantWorker: "worker-a"}}
			}
			for _, test := range modeCases {
				t.Run(test.name, func(t *testing.T) { runDispatchTenantCase(t, mode, test) })
			}
		})
	}
}

func runDispatchTenantCase(t *testing.T, mode dispatchTenantMode, test dispatchTenantCase) {
	t.Helper()
	engine, bus := dispatchTenantEngine(t, mode, test)
	req := &pb.JobRequest{JobId: "job-1", Topic: "job.shared", TenantId: test.jobTenant}
	if test.twoWorkers {
		req.Labels = map[string]string{"preferred_worker_id": "worker-a"}
	}
	err := engine.processJob(testCtx(t), req, "trace-1")
	published := bus.snapshotPublished()
	if test.wantWorker == "" {
		if !errors.Is(err, ErrNoWorkers) {
			t.Errorf("dispatch error=%v, want ErrNoWorkers", err)
		}
		if len(published) != 0 {
			t.Errorf("publishes=%d, want 0", len(published))
		}
		return
	}
	if err != nil {
		t.Errorf("dispatch error=%v, want nil", err)
	}
	if len(published) != 1 {
		t.Fatalf("publishes=%d, want 1", len(published))
	}
	if want := "worker." + test.wantWorker + ".jobs"; published[0].subject != want {
		t.Errorf("subject=%q, want %q", published[0].subject, want)
	}
	if got := published[0].packet.GetJobRequest().GetJobId(); got != req.JobId {
		t.Errorf("published job=%q, want %q", got, req.JobId)
	}
}

func dispatchTenantEngine(t *testing.T, mode dispatchTenantMode, test dispatchTenantCase) (*Engine, *fakeBus) {
	t.Helper()
	bus := &fakeBus{}
	registry := newTestRegistry(t)
	workers := map[string]string{"worker-a": test.workerTenant}
	if test.twoWorkers {
		workers["worker-b"] = "tenant-b"
	}
	cache := NewWorkerCredentialCache(nil)
	var records []workercredentials.Credential
	for id, tenant := range workers {
		registry.UpdateHeartbeat(&pb.Heartbeat{WorkerId: id, Pool: "shared"})
		if mode.handshake != HandshakeModeOff {
			registry.UpdateHandshakeTrust(capabilityHandshake(id, "node/1", "job.shared"), true)
		}
		if mode.handshake != HandshakeModeOff || mode.attestation.Enforced() {
			record := workercredentials.Credential{
				WorkerID: id, TenantID: tenant, AgentID: "agent-" + id, ProofKeyID: "proof-" + id,
				AllowedPools: []string{"shared"}, AllowedTopics: []string{"job.shared"},
			}
			records = append(records, record)
			cache.authority[id] = cloneCredentialRecord(record)
		}
	}
	cache.authorityReady = true
	cache.list = func(context.Context) ([]workercredentials.Credential, error) {
		if test.unavailable {
			return nil, errors.New("credential list unavailable")
		}
		return records, nil
	}
	engine := NewEngine(bus, NewSafetyBasic(), registry,
		NewLeastLoadedStrategy(routingForTopic("job.shared", "shared")), newFakeJobStore(), nil,
	).WithWorkerAttestationMode(mode.attestation)
	if mode.handshake != HandshakeModeOff || mode.attestation != WorkerAttestationOff {
		engine.WithWorkerCredentialCache(cache)
	}
	if mode.handshake != HandshakeModeOff {
		engine.WithSessionMiddleware(NewSessionTokenMiddleware(nil, mode.handshake, NewHandshakeMissingTracker()))
	}
	return engine, bus
}
