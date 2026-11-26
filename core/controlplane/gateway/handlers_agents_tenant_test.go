package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/auth/delegation"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/golang-jwt/jwt/v5"
)

func TestAgentDeniedEventsTenantBound(t *testing.T) {
	for _, tc := range []struct {
		name, caller string
		allTenants   bool
		status       int
		marker       bool
	}{
		{"foreign", "tenant-a", false, http.StatusNotFound, false},
		{"empty-tenant-agent", "tenant-a", false, http.StatusNotFound, false},
		{"owner", "tenant-b", false, http.StatusOK, true},
		{"all-tenants", "tenant-a", true, http.StatusOK, true},
		{"worker-link", "tenant-b", false, http.StatusOK, true},
		{"worker-alias", "tenant-a", false, http.StatusOK, false},
		{"unknown", "tenant-a", false, http.StatusNotFound, false},
		{"no-store", "tenant-a", false, http.StatusServiceUnavailable, false},
		{"no-caller-tenant", "", false, http.StatusBadRequest, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			s.auth = newBasicAuthForTest(t, nil)
			id, agentID := agentTenantSeedDenied(t, s, tc.name)
			req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+id+"/denied-events", nil), &auth.AuthContext{
				Tenant: tc.caller, Role: "admin", AllowCrossTenant: tc.allTenants,
			})
			req.SetPathValue("id", id)
			if tc.allTenants {
				req.Header.Set("X-Tenant-ID", "tenant-b")
			}
			rec := httptest.NewRecorder()
			s.handleAgentDeniedEvents(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.status, rec.Body.String())
			}
			if got := strings.Contains(rec.Body.String(), "agent-tenant-tool-marker"); got != tc.marker {
				t.Fatalf("tool marker present=%v body=%s", got, rec.Body.String())
			}
			if got := strings.Contains(rec.Body.String(), "agent-tenant-own-marker"); got != (tc.name == "worker-alias") {
				t.Fatalf("own marker present=%v body=%s", got, rec.Body.String())
			}
			if tc.status == http.StatusNotFound && !strings.Contains(rec.Body.String(), "AGENT_NOT_FOUND") {
				t.Fatalf("agent code absent: %s", rec.Body.String())
			}
			if tc.status == http.StatusOK {
				var resp struct {
					AgentID string `json:"agent_id"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.AgentID != agentID {
					t.Fatalf("agent_id=%q want=%q err=%v", resp.AgentID, agentID, err)
				}
			}
		})
	}
}

func agentTenantSeedDenied(t *testing.T, s *server, name string) (string, string) {
	t.Helper()
	identity, err := s.agentIdentityStore.Create(context.Background(), store.AgentIdentity{
		TenantID: "tenant-b", Name: "agent-tenant", Owner: "admin", RiskTier: "low",
	})
	if err != nil {
		t.Fatal(err)
	}
	id := identity.ID
	if name == "empty-tenant-agent" {
		identity.TenantID = ""
		data, err := json.Marshal(identity)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.jobStore.Client().Set(context.Background(), "agent:identity:"+id, data, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if name == "unknown" {
		id = "agent-tenant-unknown"
	}
	s.mcpDenyRing = newDenyEventRing(5)
	s.mcpDenyRing.record(denyEventRecord{AgentID: id, ToolName: "agent-tenant-tool-marker"})
	if name == "worker-link" {
		id = "agent-tenant-worker"
		if err := s.agentIdentityStore.LinkWorker(context.Background(), identity.ID, id); err != nil {
			t.Fatal(err)
		}
	}
	if name == "worker-alias" {
		own, err := s.agentIdentityStore.Create(context.Background(), store.AgentIdentity{
			TenantID: "tenant-a", Name: "agent-tenant-own", Owner: "admin", RiskTier: "low",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.agentIdentityStore.LinkWorker(context.Background(), own.ID, identity.ID); err != nil {
			t.Fatal(err)
		}
		s.mcpDenyRing.record(denyEventRecord{AgentID: own.ID, ToolName: "agent-tenant-own-marker"})
		return id, own.ID
	}
	if name == "no-store" {
		s.agentIdentityStore = nil
	}
	return id, identity.ID
}

type agentTenantVerifyCase struct {
	name, caller, tokenKind       string
	pre, post, header             string
	allTenants, noAuth, noService bool
	status                        int
	valid                         bool
	code                          string
}

func TestVerifyDelegationTenantBound(t *testing.T) {
	tab := string(rune(9))
	for _, tc := range []agentTenantVerifyCase{
		{name: "foreign-valid", caller: "tenant-a", status: http.StatusForbidden},
		{name: "foreign-revoked", caller: "tenant-a", tokenKind: "revoked", status: http.StatusForbidden},
		{name: "foreign-lead-space", caller: "tenant-a", pre: " ", status: http.StatusForbidden},
		{name: "foreign-lead-tab", caller: "tenant-a", pre: tab, status: http.StatusForbidden},
		{name: "foreign-trail-space", caller: "tenant-a", post: " ", status: http.StatusForbidden},
		{name: "foreign-header", caller: "tenant-a", header: "tenant-b", status: http.StatusForbidden},
		{name: "no-tenant-claim", caller: "tenant-a", tokenKind: "empty", status: http.StatusForbidden},
		{name: "owner-valid", caller: "tenant-b", status: http.StatusOK, valid: true},
		{name: "owner-padded", caller: "tenant-b", pre: " ", post: " ", status: http.StatusOK, valid: true},
		{name: "owner-revoked", caller: "tenant-b", tokenKind: "revoked", status: http.StatusOK, code: "revoked"},
		{name: "all-tenants", caller: "tenant-a", allTenants: true, status: http.StatusOK, valid: true},
		{name: "malformed", caller: "tenant-a", tokenKind: "malformed", status: http.StatusOK, code: "malformed"},
		{name: "malformed-no-service", caller: "tenant-a", tokenKind: "malformed", noService: true, status: http.StatusOK, code: "malformed"},
		{name: "no-auth-provider", caller: "tenant-a", noAuth: true, status: http.StatusOK, valid: true},
		{name: "no-caller-tenant", status: http.StatusForbidden},
		{name: "no-token-service", caller: "tenant-a", noService: true, status: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) { agentTenantCheckVerify(t, tc) })
	}
}

func agentTenantToken(t *testing.T, s *server, kind string) (string, delegation.DelegationClaims) {
	t.Helper()
	setDelegationKeys(t)
	createDelegationAgent(t, s, "tenant-b", "agent-tenant-issuer", []string{"read"}, []string{"job.alpha"})
	createDelegationAgent(t, s, "tenant-b", "agent-tenant-target", []string{"read"}, []string{"job.alpha"})
	service, err := s.delegationTokenService()
	if err != nil {
		t.Fatal(err)
	}
	token, claims, err := service.IssueDelegationToken(context.Background(), delegation.IssueRequest{
		Tenant: "tenant-b", DelegatingAgentID: "agent-tenant-issuer", TargetAgentID: "agent-tenant-target",
		AllowedActions: []string{"read"}, AllowedTopics: []string{"job.alpha"},
	})
	if err != nil {
		t.Fatal(err)
	}
	switch kind {
	case "revoked":
		revocations := delegation.NewRedisRevocationStoreFromClient(s.jobStore.Client())
		if err := revocations.Revoke(context.Background(), claims.ID, claims.ExpiresAt.Time); err != nil {
			t.Fatal(err)
		}
	case "empty":
		claims.Tenant = ""
		token, err = jwt.NewWithClaims(jwt.SigningMethodNone, claims).SignedString(jwt.UnsafeAllowNoneSignatureType)
		if err != nil {
			t.Fatal(err)
		}
	case "malformed":
		token = "not-a-token"
	}
	return token, claims
}

func agentTenantCheckVerify(t *testing.T, tc agentTenantVerifyCase) {
	t.Helper()
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	token, claims := agentTenantToken(t, s, tc.tokenKind)
	sink := &recordingAuditSender{}
	s.auditExporter = sink
	if tc.noAuth {
		s.auth = nil
	}
	if tc.noService {
		s.jobStore = nil
	}
	body, err := json.Marshal(map[string]string{"token": tc.pre + token + tc.post, "expected_audience": "agent-tenant-target"})
	if err != nil {
		t.Fatal(err)
	}
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/agents/verify-delegation", bytes.NewReader(body)), &auth.AuthContext{
		Tenant: tc.caller, Role: "admin", AllowCrossTenant: tc.allTenants,
	})
	if tc.header != "" {
		req.Header.Set("X-Tenant-ID", tc.header)
	}
	rec := httptest.NewRecorder()
	s.handleVerifyDelegation(rec, req)
	if rec.Code != tc.status {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.status, rec.Body.String())
	}
	if tc.status == http.StatusForbidden {
		agentTenantAssertRefused(t, rec, sink, claims, tc.caller)
		return
	}
	var resp verifyDelegationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Valid != tc.valid || resp.ErrorCode != tc.code {
		t.Fatalf("verdict=%+v want valid=%v code=%q", resp, tc.valid, tc.code)
	}
	if tc.valid && (resp.Sub != claims.Subject || resp.Aud != "agent-tenant-target") {
		t.Fatalf("claims=%+v", resp)
	}
	if tc.code == "malformed" {
		agentTenantAssertMalformedAudit(t, sink, tc.caller)
	}
}

func agentTenantAssertMalformedAudit(t *testing.T, sink *recordingAuditSender, tenant string) {
	t.Helper()
	if len(sink.events) != 1 {
		t.Fatalf("audit count=%d want=1", len(sink.events))
	}
	ev := sink.events[0]
	if ev.Action != "delegation.verify" || ev.TenantID != tenant || ev.Extra["outcome"] != "denied" ||
		ev.Extra["error_code"] != "malformed" || ev.Extra["target"] != "agent-tenant-target" {
		t.Fatalf("audit=%+v", ev)
	}
}

func agentTenantAssertRefused(t *testing.T, rec *httptest.ResponseRecorder, sink *recordingAuditSender, claims delegation.DelegationClaims, tenant string) {
	t.Helper()
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"sub", "aud", "valid", "error_code"} {
		if _, ok := body[key]; ok {
			t.Errorf("response contains %q", key)
		}
	}
	if len(sink.events) != 1 {
		t.Fatalf("audit count=%d want=1", len(sink.events))
	}
	ev := sink.events[0]
	if ev.Action != "delegation.verify" || ev.TenantID != tenant || ev.AgentID != "" || ev.Extra["jti"] != "" || ev.Extra["outcome"] != "denied" {
		t.Fatalf("audit=%+v", ev)
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{claims.Subject, claims.ID} {
		if strings.Contains(string(data), marker) || strings.Contains(rec.Body.String(), marker) {
			t.Error("token identifier present in response or audit")
		}
	}
}
