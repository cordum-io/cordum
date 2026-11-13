package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
)

func newAgentBindingGateway(t *testing.T) (*server, *stubBus, *stubSafetyClient) {
	t.Helper()
	s, bus, safety := newTestGateway(t)
	for _, suffix := range []string{"a", "b"} {
		createDelegationAgent(t, s, "default", "agent-"+suffix, []string{"read"}, []string{"job.test"})
		if err := s.agentIdentityStore.LinkWorker(context.Background(), "agent-"+suffix, "worker-"+suffix); err != nil {
			t.Fatalf("LinkWorker: %v", err)
		}
	}
	return s, bus, safety
}

func agentBindingRequest(t *testing.T, principal, label string, ac *auth.AuthContext) *http.Request {
	t.Helper()
	body := submitJobRequest{Prompt: "hello", Topic: "job.test", PrincipalId: principal}
	if label != "" {
		body.Labels = map[string]string{"agent_id": label}
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(string(data)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "default")
	if ac != nil {
		req = withAuth(req, ac)
	}
	return req
}

func assertAgentBindingRefused(t *testing.T, rec *httptest.ResponseRecorder, bus *stubBus, safety *stubSafetyClient, message string) {
	t.Helper()
	if rec.Code != http.StatusForbidden {
		t.Errorf("status=%d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), message) {
		t.Errorf("body=%s, want %q", rec.Body.String(), message)
	}
	if safety.lastReq != nil {
		t.Error("policy check reached for refused label")
	}
	if len(bus.published) != 0 {
		t.Errorf("published=%d, want 0", len(bus.published))
	}
}

func TestHandleSubmitJobHTTPRefusesAgentLabelForRequestedPrincipal(t *testing.T) {
	composite, err := auth.NewCompositeAuthProvider(&auth.BasicAuthProvider{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, authenticated, requested string
		provider                       auth.AuthProvider
	}{
		{"basic_other_principal", "worker-a", "worker-b", &auth.BasicAuthProvider{}},
		{"basic_keyed_caller", "", "worker-b", &auth.BasicAuthProvider{}},
		{"test_keyed_caller", "", "worker-b", testAuthProvider{}},
		{"basic_keyed_no_requested_principal", "", "", &auth.BasicAuthProvider{}},
		{"basic_blank_principal", " \t ", "worker-b", &auth.BasicAuthProvider{}},
		{"basic_case_mismatch", "WORKER-B", "worker-b", &auth.BasicAuthProvider{}},
		{"composite_other_principal", "worker-a", "worker-b", composite},
		{"dev_no_auth_context", "", "worker-b", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, bus, safety := newAgentBindingGateway(t)
			s.auth = tc.provider
			var ac *auth.AuthContext
			if tc.provider != nil {
				ac = &auth.AuthContext{Tenant: "default", PrincipalID: tc.authenticated, Role: "admin"}
			}
			req := agentBindingRequest(t, tc.requested, "agent-b", ac)
			rec := httptest.NewRecorder()
			s.handleSubmitJobHTTP(rec, req)
			assertAgentBindingRefused(t, rec, bus, safety, "client-supplied agent_id requires an authenticated principal")
		})
	}
}

func TestHandleSubmitJobHTTPHonorsAgentLabelForAuthenticatedPrincipal(t *testing.T) {
	for _, tc := range []struct{ name, authenticated, requested, label string }{
		{"explicit_principal", "worker-b", "worker-b", "agent-b"},
		{"implicit_principal", "worker-b", "", "agent-b"},
		{"trimmed_principal", " worker-b ", " worker-b ", "agent-b"},
		{"wrong_agent", "worker-b", "worker-b", "agent-a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, bus, safety := newAgentBindingGateway(t)
			s.auth = &auth.BasicAuthProvider{}
			req := agentBindingRequest(t, tc.requested, tc.label,
				&auth.AuthContext{Tenant: "default", PrincipalID: tc.authenticated, Role: "admin"})
			rec := httptest.NewRecorder()
			s.handleSubmitJobHTTP(rec, req)
			if tc.label == "agent-a" {
				assertAgentBindingRefused(t, rec, bus, safety, "client-supplied agent_id does not match authenticated principal")
				return
			}
			if rec.Code != http.StatusOK || safety.lastReq == nil {
				t.Fatalf("status=%d, policy captured=%v, body=%s", rec.Code, safety.lastReq != nil, rec.Body.String())
			}
			if got := safety.lastReq.GetLabels()["agent_id"]; got != "agent-b" {
				t.Errorf("agent_id=%q, want agent-b", got)
			}
		})
	}
}

func TestHandleSubmitJobHTTPWithoutAgentLabelKeepsLinkInjection(t *testing.T) {
	// The keyed and request-chosen cases pin CURRENT behavior; task-491f974b will flip them by ruling.
	for _, tc := range []struct{ name, authenticated, requested string }{
		{"authenticated_worker", "worker-b", ""},
		{"keyed_caller", "", "worker-b"},
		{"request_chosen_principal", "worker-a", "worker-b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, safety := newAgentBindingGateway(t)
			s.auth = &auth.BasicAuthProvider{}
			req := agentBindingRequest(t, tc.requested, "",
				&auth.AuthContext{Tenant: "default", PrincipalID: tc.authenticated, Role: "admin"})
			rec := httptest.NewRecorder()
			s.handleSubmitJobHTTP(rec, req)
			if rec.Code != http.StatusOK || safety.lastReq == nil {
				t.Fatalf("status=%d, policy captured=%v, body=%s", rec.Code, safety.lastReq != nil, rec.Body.String())
			}
			if got := safety.lastReq.GetLabels()["agent_id"]; got != "agent-b" {
				t.Errorf("agent_id=%q, want agent-b", got)
			}
		})
	}
}
