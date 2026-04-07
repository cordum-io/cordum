package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/topicregistry"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
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
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/workers/credentials", body), &AuthContext{
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

	listReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/workers/credentials", nil), &AuthContext{
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

	issued, err := s.workerCredentialStore.Create(context.Background(), workercredentials.IssueInput{
		WorkerID:  "external-worker",
		CreatedBy: "tester",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	req := withAuth(httptest.NewRequest(http.MethodDelete, "/api/v1/workers/credentials/external-worker", nil), &AuthContext{
		Tenant: "default",
		Role:   "admin",
	})
	req.SetPathValue("worker_id", "external-worker")
	rr := httptest.NewRecorder()

	s.handleDeleteWorkerCredential(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}

	record, err := s.workerCredentialStore.Get(context.Background(), "external-worker")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if record == nil || !record.Revoked() {
		t.Fatalf("expected revoked record, got %+v", record)
	}

	record, ok, err := s.workerCredentialStore.Verify(context.Background(), "external-worker", issued.Token)
	if err != nil {
		t.Fatalf("Verify revoked: %v", err)
	}
	if ok {
		t.Fatal("expected revoked credential verification to fail")
	}
	if record == nil || !record.Revoked() {
		t.Fatalf("expected revoked record from Verify, got %+v", record)
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
