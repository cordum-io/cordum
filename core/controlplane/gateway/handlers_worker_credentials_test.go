package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/controlplane/topicregistry"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/licensing"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
)

func seedWorkerCredentialAccessConfig(t *testing.T, s *server) {
	t.Helper()

	if err := s.configSvc.Set(context.Background(), &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data: map[string]any{
			"pools": map[string]any{
				"pools": map[string]any{
					"default": map[string]any{"requires": []any{}},
				},
			},
		},
	}); err != nil {
		t.Fatalf("seed pool config: %v", err)
	}
	if err := s.topicRegistry.Set(context.Background(), topicregistry.Registration{
		Name:   "job.external",
		Pool:   "default",
		Status: topicregistry.StatusActive,
	}); err != nil {
		t.Fatalf("seed topic registry: %v", err)
	}
}

func TestRegisterExternalWorker(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	seedWorkerCredentialAccessConfig(t, s)

	body := bytes.NewBufferString(`{"worker_id":"external-worker","allowed_pools":["default"],"allowed_topics":["job.external"]}`)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/workers/credentials", body), &auth.AuthContext{
		Tenant:      "default",
		Role:        "admin",
		PrincipalID: "admin-user",
	})
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.handleCreateWorkerCredential(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp issueWorkerCredentialResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if resp.WorkerID != "external-worker" {
		t.Fatalf("expected worker id external-worker, got %q", resp.WorkerID)
	}
	if resp.Token == "" {
		t.Fatal("expected plaintext token in create response")
	}
	if got, want := resp.AllowedPools, []string{"default"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("allowed pools mismatch: got %v want %v", got, want)
	}
	if got, want := resp.AllowedTopics, []string{"job.external"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("allowed topics mismatch: got %v want %v", got, want)
	}

	record, ok, err := s.workerCredentialStore.Verify(context.Background(), resp.WorkerID, resp.Token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok || record == nil {
		t.Fatalf("expected stored credential to verify, got ok=%v record=%v", ok, record)
	}
	if record.CreatedBy != "admin-user" {
		t.Fatalf("expected created_by admin-user, got %q", record.CreatedBy)
	}

	listReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/workers/credentials", nil), &auth.AuthContext{
		Tenant: "default",
		Role:   "admin",
	})
	listRR := httptest.NewRecorder()
	s.handleListWorkerCredentials(listRR, listReq)
	if listRR.Code != http.StatusOK {
		t.Fatalf("expected 200 from list, got %d: %s", listRR.Code, listRR.Body.String())
	}

	var listPayload struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.NewDecoder(listRR.Body).Decode(&listPayload); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listPayload.Items) != 1 {
		t.Fatalf("expected 1 listed credential, got %d", len(listPayload.Items))
	}
	if listPayload.Items[0]["worker_id"] != "external-worker" {
		t.Fatalf("unexpected listed credential: %+v", listPayload.Items[0])
	}
	if _, ok := listPayload.Items[0]["token"]; ok {
		t.Fatalf("list response must not include plaintext token: %+v", listPayload.Items[0])
	}
	if _, ok := listPayload.Items[0]["credential_hash"]; ok {
		t.Fatalf("list response must not include credential hash: %+v", listPayload.Items[0])
	}

	if len(bus.published) == 0 {
		t.Fatal("expected config changed publish")
	}
	last := bus.published[len(bus.published)-1]
	if last.subject != capsdk.SubjectConfigChanged {
		t.Fatalf("expected config changed publish, got %q", last.subject)
	}
	if alert := last.packet.GetAlert(); alert == nil || alert.GetDetails()["scope_id"] != "workers" {
		t.Fatalf("expected workers config change alert, got %+v", last.packet.GetAlert())
	}
}

func TestRevokeWorker(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	issuer := wireSessionIssuer(t, s)
	ctx := context.Background()

	issued, err := s.workerCredentialStore.Create(ctx, workercredentials.IssueInput{
		TenantID:  "default",
		WorkerID:  "external-worker",
		CreatedBy: "tester",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, _, err := issuer.Issue(ctx, "external-worker", "default", "v1"); err != nil {
		t.Fatalf("issue session: %v", err)
	}

	req := withAuth(httptest.NewRequest(http.MethodDelete, "/api/v1/workers/credentials/external-worker", nil), &auth.AuthContext{
		Tenant: "default",
		Role:   "admin",
	})
	req.SetPathValue("worker_id", "external-worker")
	rr := httptest.NewRecorder()

	s.handleDeleteWorkerCredential(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}

	record, err := s.workerCredentialStore.Get(ctx, "default", "external-worker")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if record == nil || !record.Revoked() {
		t.Fatalf("expected revoked record, got %+v", record)
	}

	record, ok, err := s.workerCredentialStore.Verify(ctx, "external-worker", issued.Token)
	if err != nil {
		t.Fatalf("Verify revoked: %v", err)
	}
	if ok {
		t.Fatal("expected revoked credential verification to fail")
	}
	if record == nil || !record.Revoked() {
		t.Fatalf("expected revoked record from Verify, got %+v", record)
	}
	trust, err := scheduler.NewTrustResolver(s.jobStore.Client()).ResolveTrust(ctx, "external-worker")
	if err != nil {
		t.Fatalf("resolve trust after credential revoke: %v", err)
	}
	if trust.SessionValid || trust.RevokedAt == nil {
		t.Fatalf("expected deleting credential to revoke active worker session, got %+v", trust)
	}

	if len(bus.published) == 0 {
		t.Fatal("expected config changed publish")
	}
	last := bus.published[len(bus.published)-1]
	if last.subject != capsdk.SubjectConfigChanged {
		t.Fatalf("expected config changed publish, got %q", last.subject)
	}
	if alert := last.packet.GetAlert(); alert == nil || alert.GetDetails()["scope_id"] != "workers" {
		t.Fatalf("expected workers config change alert, got %+v", last.packet.GetAlert())
	}
}

func createWorkerCredentialForTenant(t *testing.T, s *server, tenant, workerID string) issueWorkerCredentialResponse {
	t.Helper()
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/workers/credentials",
		bytes.NewBufferString(fmt.Sprintf(`{"worker_id":%q}`, workerID))), &auth.AuthContext{
		Tenant:      tenant,
		Role:        "admin",
		PrincipalID: tenant + "-admin",
	})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleCreateWorkerCredential(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %s/%s: expected 201, got %d: %s", tenant, workerID, rec.Code, rec.Body.String())
	}
	var resp issueWorkerCredentialResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	return resp
}

func listWorkerCredentialIDsForTenant(t *testing.T, s *server, tenant string) []string {
	t.Helper()
	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/workers/credentials", nil), &auth.AuthContext{
		Tenant: tenant,
		Role:   "admin",
	})
	rec := httptest.NewRecorder()
	s.handleListWorkerCredentials(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list %s: expected 200, got %d: %s", tenant, rec.Code, rec.Body.String())
	}
	var payload struct {
		Items []workerCredentialResponse `json:"items"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	ids := make([]string, 0, len(payload.Items))
	for _, item := range payload.Items {
		ids = append(ids, item.WorkerID)
	}
	return ids
}

func TestWorkerCredentials_ScopedToTenant(t *testing.T) {
	s, _, _ := newTestGateway(t)
	tenantA := createWorkerCredentialForTenant(t, s, "tenant-a", "tenant-a-worker")
	tenantB := createWorkerCredentialForTenant(t, s, "tenant-b", "tenant-b-worker")

	if got := listWorkerCredentialIDsForTenant(t, s, "tenant-a"); !equalStringSlices(got, []string{"tenant-a-worker"}) {
		t.Fatalf("tenant-a list = %v, want only tenant-a-worker", got)
	}
	if got := listWorkerCredentialIDsForTenant(t, s, "tenant-b"); !equalStringSlices(got, []string{"tenant-b-worker"}) {
		t.Fatalf("tenant-b list = %v, want only tenant-b-worker", got)
	}

	req := withAuth(httptest.NewRequest(http.MethodDelete, "/api/v1/workers/credentials/tenant-b-worker", nil), &auth.AuthContext{
		Tenant: "tenant-a",
		Role:   "admin",
	})
	req.SetPathValue("worker_id", "tenant-b-worker")
	rec := httptest.NewRecorder()
	s.handleDeleteWorkerCredential(rec, req)
	requireStableErrorCode(t, rec, http.StatusNotFound, "WORKER_CRED_NOT_FOUND")

	if _, ok, err := s.workerCredentialStore.Verify(context.Background(), tenantB.WorkerID, tenantB.Token); err != nil || !ok {
		t.Fatalf("tenant-b credential must remain valid after tenant-a revoke attempt: ok=%v err=%v", ok, err)
	}
	if _, ok, err := s.workerCredentialStore.Verify(context.Background(), tenantA.WorkerID, tenantA.Token); err != nil || !ok {
		t.Fatalf("tenant-a credential should still verify: ok=%v err=%v", ok, err)
	}
}

func TestWorkerCredentials_LicenseCASNoOvershoot(t *testing.T) {
	s, _, _ := newTestGateway(t)
	// The invariant is exact license-cap enforcement under concurrent CAS, not
	// a specific cap size. Keep 3x contention but bound the race-detector memory
	// footprint so WSL runners can repeat the test with -race -count=3.
	const maxWorkers = 4
	setTestEntitlements(t, s, licensing.PlanTeam, func(e *licensing.Entitlements) {
		e.MaxWorkers = maxWorkers
	})

	const attempts = maxWorkers * 3
	start := make(chan struct{})
	var wg sync.WaitGroup
	codes := make(chan int, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/workers/credentials",
				bytes.NewBufferString(fmt.Sprintf(`{"worker_id":"cas-worker-%03d"}`, i))), &auth.AuthContext{
				Tenant:      "default",
				Role:        "admin",
				PrincipalID: "admin",
			})
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			s.handleCreateWorkerCredential(rec, req)
			codes <- rec.Code
		}(i)
	}
	close(start)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %d worker credential create attempts", attempts)
	}
	close(codes)

	successes := 0
	for code := range codes {
		if code == http.StatusCreated {
			successes++
		}
	}
	if successes != maxWorkers {
		t.Fatalf("worker license CAS allowed %d creates, want exactly %d", successes, maxWorkers)
	}
	records, err := s.workerCredentialStore.List(context.Background(), "default")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	active := 0
	for _, record := range records {
		if !record.Revoked() {
			active++
		}
	}
	if active != maxWorkers {
		t.Fatalf("stored active worker credentials = %d, want %d", active, maxWorkers)
	}
}

func TestCreateCredentialEmptyWorkerID(t *testing.T) {
	s, _, _ := newTestGateway(t)

	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/workers/credentials", bytes.NewBufferString(`{"worker_id":"   "}`)), &auth.AuthContext{
		Tenant: "default",
		Role:   "admin",
	})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleCreateWorkerCredential(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	requireStableErrorCode(t, rec, http.StatusBadRequest, "WORKER_CRED_BINDING_INVALID")
}

func TestCreateCredentialArrayTooLong(t *testing.T) {
	s, _, _ := newTestGateway(t)

	allowedPools := make([]string, maxCredentialArrayItems+1)
	for i := range allowedPools {
		allowedPools[i] = "default"
	}
	body, err := json.Marshal(map[string]any{
		"worker_id":     "external-worker",
		"allowed_pools": allowedPools,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/workers/credentials", bytes.NewReader(body)), &auth.AuthContext{
		Tenant: "default",
		Role:   "admin",
	})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleCreateWorkerCredential(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateCredentialPoolNotFound(t *testing.T) {
	s, _, _ := newTestGateway(t)

	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/workers/credentials", bytes.NewBufferString(`{"worker_id":"external-worker","allowed_pools":["missing-pool"]}`)), &auth.AuthContext{
		Tenant: "default",
		Role:   "admin",
	})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleCreateWorkerCredential(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	requireStableErrorCode(t, rec, http.StatusNotFound, "POOL_NOT_FOUND")
}

func TestRevokeNonexistentCredential(t *testing.T) {
	s, _, _ := newTestGateway(t)

	req := withAuth(httptest.NewRequest(http.MethodDelete, "/api/v1/workers/credentials/missing-worker", nil), &auth.AuthContext{
		Tenant: "default",
		Role:   "admin",
	})
	req.SetPathValue("worker_id", "missing-worker")
	rec := httptest.NewRecorder()

	s.handleDeleteWorkerCredential(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	requireStableErrorCode(t, rec, http.StatusNotFound, "WORKER_CRED_NOT_FOUND")
}

func TestRotateCredentialClearsAgentLink(t *testing.T) {
	// Regression test: rotating a credential from agent_id=X to agent_id=""
	// must clear the reverse-lookup so GetByWorkerID no longer returns the agent.
	s, _, _ := newTestGateway(t)
	seedWorkerCredentialAccessConfig(t, s)
	ctx := context.Background()

	// Create an agent identity.
	agent, err := s.agentIdentityStore.Create(ctx, store.AgentIdentity{
		Name: "test-agent", Owner: "admin", RiskTier: "low", Status: "active",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	// Create credential linked to agent.
	body := bytes.NewBufferString(`{"worker_id":"link-worker","allowed_pools":["default"],"allowed_topics":["job.external"],"agent_id":"` + agent.ID + `"}`)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/workers/credentials", body), &auth.AuthContext{
		Tenant: "default", Role: "admin", PrincipalID: "admin",
	})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleCreateWorkerCredential(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify agent is linked.
	linked, err := s.agentIdentityStore.GetByWorkerID(ctx, "link-worker")
	if err != nil || linked == nil {
		t.Fatalf("expected agent linked after create, got err=%v agent=%v", err, linked)
	}

	// Rotate credential WITHOUT agent_id → should clear the link.
	body2 := bytes.NewBufferString(`{"worker_id":"link-worker","allowed_pools":["default"],"allowed_topics":["job.external"]}`)
	req2 := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/workers/credentials", body2), &auth.AuthContext{
		Tenant: "default", Role: "admin", PrincipalID: "admin",
	})
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	s.handleCreateWorkerCredential(rec2, req2)
	if rec2.Code != http.StatusOK && rec2.Code != http.StatusCreated {
		t.Fatalf("rotate: expected 200/201, got %d: %s", rec2.Code, rec2.Body.String())
	}

	// Verify agent link is cleared — GetByWorkerID should return nil.
	stale, err := s.agentIdentityStore.GetByWorkerID(ctx, "link-worker")
	if err != nil {
		t.Fatalf("GetByWorkerID after rotate: %v", err)
	}
	if stale != nil {
		t.Fatalf("STALE LINKAGE: GetByWorkerID returned agent %q after rotate-to-empty — should be nil", stale.ID)
	}
}

func TestRevokeCredentialClearsAgentLink(t *testing.T) {
	// Regression test: revoking a credential must clear the reverse-lookup.
	s, _, _ := newTestGateway(t)
	seedWorkerCredentialAccessConfig(t, s)
	ctx := context.Background()

	agent, err := s.agentIdentityStore.Create(ctx, store.AgentIdentity{
		Name: "revoke-agent", Owner: "admin", RiskTier: "low", Status: "active",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	// Create credential linked to agent.
	body := bytes.NewBufferString(`{"worker_id":"revoke-worker","allowed_pools":["default"],"allowed_topics":["job.external"],"agent_id":"` + agent.ID + `"}`)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/workers/credentials", body), &auth.AuthContext{
		Tenant: "default", Role: "admin", PrincipalID: "admin",
	})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleCreateWorkerCredential(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	// Revoke the credential.
	revokeReq := withAuth(httptest.NewRequest(http.MethodDelete, "/api/v1/workers/credentials/revoke-worker", nil), &auth.AuthContext{
		Tenant: "default", Role: "admin",
	})
	revokeReq.SetPathValue("worker_id", "revoke-worker")
	revokeRec := httptest.NewRecorder()
	s.handleDeleteWorkerCredential(revokeRec, revokeReq)
	if revokeRec.Code != http.StatusNoContent {
		t.Fatalf("revoke: expected 204, got %d: %s", revokeRec.Code, revokeRec.Body.String())
	}

	// Verify agent link is cleared.
	stale, err := s.agentIdentityStore.GetByWorkerID(ctx, "revoke-worker")
	if err != nil {
		t.Fatalf("GetByWorkerID after revoke: %v", err)
	}
	if stale != nil {
		t.Fatalf("STALE LINKAGE: GetByWorkerID returned agent %q after revoke — should be nil", stale.ID)
	}
}

func equalStringSlices(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
