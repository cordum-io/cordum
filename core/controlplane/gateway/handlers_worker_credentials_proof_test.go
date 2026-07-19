package gateway

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	"github.com/cordum/cordum/core/infra/store"
)

func TestWorkerCredentialAPIEnrollsProofKeyWithoutExposingPEM(t *testing.T) {
	s, _, _ := newTestGateway(t)
	keyPEM := gatewayProofPublicKeyPEM(t)
	agent := createGatewayProofAgent(t, s, "tenant-a")
	body := proofCredentialRequestBody(t, "worker-proof", "key-a", keyPEM, agent.ID)
	rec := createProofCredentialRequest(t, s, "tenant-a", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", rec.Code, rec.Body.String())
	}
	assertPublicProofMetadata(t, rec.Body.Bytes(), "key-a")

	record, err := s.workerCredentialStore.GetByWorkerID(context.Background(), "worker-proof")
	if err != nil || record == nil || record.ProofPublicKeyPEM == "" {
		t.Fatalf("stored proof key missing: record=%+v err=%v", record, err)
	}
	listReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/workers/credentials", nil), &auth.AuthContext{
		Tenant: "tenant-a", Role: "admin",
	})
	listRec := httptest.NewRecorder()
	s.handleListWorkerCredentials(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", listRec.Code, listRec.Body.String())
	}
	assertPublicProofMetadata(t, listRec.Body.Bytes(), "key-a")
}

func TestWorkerCredentialAPIRejectsInvalidProofEnrollment(t *testing.T) {
	validPEM := gatewayProofPublicKeyPEM(t)
	cases := []struct {
		name string
		body map[string]any
	}{
		{"partial", map[string]any{"worker_id": "worker-a", "proof_key_id": "key-a"}},
		{"wrong algorithm", map[string]any{
			"worker_id": "worker-a", "proof_key_id": "key-a", "proof_algorithm": "ECDSA_P384_SHA384", "proof_public_key_pem": validPEM,
		}},
		{"bad pem", map[string]any{
			"worker_id": "worker-a", "proof_key_id": "key-a", "proof_algorithm": workercredentials.ProofAlgorithmECDSAP256SHA256, "proof_public_key_pem": "not pem",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			body, err := json.Marshal(tc.body)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			rec := createProofCredentialRequest(t, s, "default", body)
			requireStableErrorCode(t, rec, http.StatusBadRequest, "WORKER_CRED_BINDING_INVALID")
		})
	}
}

func TestWorkerCredentialAPIRotationPreservesOrReplacesProofKey(t *testing.T) {
	s, _, _ := newTestGateway(t)
	keyA := gatewayProofPublicKeyPEM(t)
	keyB := gatewayProofPublicKeyPEM(t)
	agent := createGatewayProofAgent(t, s, "default")
	if rec := createProofCredentialRequest(t, s, "default", proofCredentialRequestBody(t, "worker-proof", "key-a", keyA, agent.ID)); rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", rec.Code, rec.Body.String())
	}
	withoutProof, err := json.Marshal(map[string]any{"worker_id": "worker-proof"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if rec := createProofCredentialRequest(t, s, "default", withoutProof); rec.Code != http.StatusOK {
		t.Fatalf("preserving rotation status = %d: %s", rec.Code, rec.Body.String())
	}
	record, err := s.workerCredentialStore.GetByWorkerID(context.Background(), "worker-proof")
	if err != nil || record == nil || record.ProofKeyID != "key-a" || record.AgentID != agent.ID {
		t.Fatalf("preserving rotation record=%+v err=%v", record, err)
	}
	linked, err := s.agentIdentityStore.GetByWorkerID(context.Background(), "worker-proof")
	if err != nil || linked == nil || linked.ID != agent.ID {
		t.Fatalf("preserving rotation lost agent link: agent=%+v err=%v", linked, err)
	}
	if rec := createProofCredentialRequest(t, s, "default", proofCredentialRequestBody(t, "worker-proof", "key-b", keyB, agent.ID)); rec.Code != http.StatusOK {
		t.Fatalf("replacement rotation status = %d: %s", rec.Code, rec.Body.String())
	}
	record, err = s.workerCredentialStore.GetByWorkerID(context.Background(), "worker-proof")
	if err != nil || record == nil || record.ProofKeyID != "key-b" || record.ProofPublicKeyPEM == keyA {
		t.Fatalf("replacement rotation record=%+v err=%v", record, err)
	}
}

func TestWorkerCredentialAPIRequiresAgentForProofEnrollment(t *testing.T) {
	s, _, _ := newTestGateway(t)
	body := proofCredentialRequestBody(t, "worker-proof", "key-a", gatewayProofPublicKeyPEM(t), "")
	rec := createProofCredentialRequest(t, s, "default", body)
	requireStableErrorCode(t, rec, http.StatusBadRequest, "WORKER_CRED_BINDING_INVALID")
}

func TestWorkerCredentialAPIRotationRejectsMissingAuthoritativeLink(t *testing.T) {
	s, _, _ := newTestGateway(t)
	agent := createGatewayProofAgent(t, s, "default")
	body := proofCredentialRequestBody(t, "worker-proof", "key-a", gatewayProofPublicKeyPEM(t), agent.ID)
	if rec := createProofCredentialRequest(t, s, "default", body); rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", rec.Code, rec.Body.String())
	}
	if err := s.agentIdentityStore.UnlinkWorker(context.Background(), "worker-proof"); err != nil {
		t.Fatalf("UnlinkWorker: %v", err)
	}
	rotation, err := json.Marshal(map[string]any{"worker_id": "worker-proof"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	rec := createProofCredentialRequest(t, s, "default", rotation)
	requireStableErrorCode(t, rec, http.StatusBadRequest, "WORKER_CRED_BINDING_INVALID")
}

func TestWorkerCredentialAPIRotationRejectsAgentRebindWithoutProof(t *testing.T) {
	s, _, _ := newTestGateway(t)
	agentA := createGatewayProofAgent(t, s, "default")
	agentB := createGatewayProofAgent(t, s, "default")
	body := proofCredentialRequestBody(t, "worker-proof", "key-a", gatewayProofPublicKeyPEM(t), agentA.ID)
	if rec := createProofCredentialRequest(t, s, "default", body); rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", rec.Code, rec.Body.String())
	}
	rotation, err := json.Marshal(map[string]any{"worker_id": "worker-proof", "agent_id": agentB.ID})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	rec := createProofCredentialRequest(t, s, "default", rotation)
	requireStableErrorCode(t, rec, http.StatusBadRequest, "WORKER_CRED_BINDING_INVALID")
	record, err := s.workerCredentialStore.GetByWorkerID(context.Background(), "worker-proof")
	if err != nil || record == nil || record.AgentID != agentA.ID || record.ProofKeyID != "key-a" {
		t.Fatalf("rejected rebind mutated credential: record=%+v err=%v", record, err)
	}
}

func TestWorkerCredentialAPIRejectsInvalidAgentLinkage(t *testing.T) {
	cases := []struct {
		name, agentTenant, status string
	}{
		{"cross tenant", "tenant-b", "active"},
		{"inactive", "tenant-a", "suspended"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			agent, err := s.agentIdentityStore.Create(context.Background(), store.AgentIdentity{
				TenantID: tc.agentTenant, Name: "proof-agent", Owner: tc.agentTenant,
				Status: tc.status, RiskTier: "low",
			})
			if err != nil {
				t.Fatalf("create agent: %v", err)
			}
			body, err := json.Marshal(map[string]any{"worker_id": "worker-proof", "agent_id": agent.ID})
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			rec := createProofCredentialRequest(t, s, "tenant-a", body)
			requireStableErrorCode(t, rec, http.StatusBadRequest, "WORKER_CRED_BINDING_INVALID")
			if record, err := s.workerCredentialStore.GetByWorkerID(context.Background(), "worker-proof"); err != nil || record != nil {
				t.Fatalf("invalid linkage stored credential=%+v err=%v", record, err)
			}
		})
	}
}

func proofCredentialRequestBody(t *testing.T, workerID, keyID, publicPEM, agentID string) []byte {
	t.Helper()
	request := map[string]any{
		"worker_id": workerID, "proof_key_id": keyID,
		"proof_algorithm":      workercredentials.ProofAlgorithmECDSAP256SHA256,
		"proof_public_key_pem": publicPEM,
	}
	if agentID != "" {
		request["agent_id"] = agentID
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return body
}

func createGatewayProofAgent(t *testing.T, s *server, tenant string) *store.AgentIdentity {
	t.Helper()
	agent, err := s.agentIdentityStore.Create(context.Background(), store.AgentIdentity{
		TenantID: tenant, Name: "proof-agent", Owner: tenant, Status: "active", RiskTier: "low",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return agent
}

func createProofCredentialRequest(t *testing.T, s *server, tenant string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/workers/credentials", bytes.NewReader(body)), &auth.AuthContext{
		Tenant: tenant, Role: "admin", PrincipalID: "admin",
	})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleCreateWorkerCredential(rec, req)
	return rec
}

func assertPublicProofMetadata(t *testing.T, body []byte, wantKeyID string) {
	t.Helper()
	text := string(body)
	if !strings.Contains(text, `"proof_key_id":"`+wantKeyID+`"`) ||
		!strings.Contains(text, `"proof_algorithm":"ECDSA_P256_SHA256"`) {
		t.Fatalf("response missing public proof metadata: %s", text)
	}
	for _, forbidden := range []string{"proof_public_key_pem", "credential_hash", "BEGIN PUBLIC KEY"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("response exposed %q: %s", forbidden, text)
		}
	}
}

func gatewayProofPublicKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}
