package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/licensing"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// failingAgentStore stands in for an agent-store outage: every Redis call
// fails, while the job store, bus and policy client stay healthy.
func failingAgentStore() *store.AgentIdentityStore {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	_ = client.Close()
	return store.NewAgentIdentityStoreFromClient(client)
}

// linkLookupCase describes one submit. grpc callers: "" is the authenticated
// principal p, "anonymous" has no auth context and no principal, "blank" is
// an authenticated whitespace principal. http callers: "" is the
// authenticated worker-b, "unlinked" the authenticated worker-c, "request"
// has no auth context and names worker-b in principal_id, "empty" has no
// auth context and no principal_id.
type linkLookupCase struct {
	caller  string
	label   string
	unlink  bool // grpc: leave p unlinked
	dangle  bool // delete the linked agent's record, leaving a dangling link
	failing bool // swap in an agent store whose every call fails
}

// linkLookupResult is what one submit returned and left behind.
type linkLookupResult struct {
	ok        bool
	code      codes.Code // grpc
	status    int        // http
	message   string     // grpc status message, or the http JSON error
	raw       string     // full grpc error text, or the http body
	policy    bool
	agentID   string
	published int
	jobs      int
}

func (r *linkLookupResult) observe(t *testing.T, s *server, bus *stubBus, safety *stubSafetyClient) {
	t.Helper()
	jobs, err := s.jobStore.ListRecentJobs(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListRecentJobs: %v", err)
	}
	r.jobs = len(jobs)
	bus.mu.Lock()
	r.published = len(bus.published)
	bus.mu.Unlock()
	safety.mu.Lock()
	defer safety.mu.Unlock()
	if safety.lastReq != nil {
		r.policy = true
		r.agentID = safety.lastReq.GetLabels()["agent_id"]
	}
}

func deleteAgentRecord(t *testing.T, s *server, agentID string) {
	t.Helper()
	if err := s.jobStore.Client().Del(context.Background(), "agent:identity:"+agentID).Err(); err != nil {
		t.Fatalf("delete agent record %s: %v", agentID, err)
	}
}

func runLinkLookupGRPC(t *testing.T, c linkLookupCase) linkLookupResult {
	t.Helper()
	s, bus, safety := newTestGateway(t)
	s.tenant = "default"
	createDelegationAgent(t, s, "default", "agent-source", []string{"read"}, []string{"job.default"})
	createDelegationAgent(t, s, "default", "agent-other", []string{"read"}, []string{"job.default"})
	if !c.unlink {
		if err := s.agentIdentityStore.LinkWorker(context.Background(), "agent-source", "p"); err != nil {
			t.Fatalf("link worker: %v", err)
		}
	}
	if c.dangle {
		deleteAgentRecord(t, s, "agent-source")
	}
	if c.failing {
		s.agentIdentityStore = failingAgentStore()
	}
	ctx, principal := grpcAgentBindingCaller(""), "p"
	switch c.caller {
	case "anonymous":
		ctx, principal = context.Background(), ""
	case "blank":
		ctx = context.WithValue(context.Background(), auth.ContextKey{}, &auth.AuthContext{Tenant: "default", PrincipalID: " ", Role: "admin"})
		principal = " "
	}
	req := &pb.SubmitJobRequest{Prompt: "hello", Topic: "job.default", OrgId: "default", PrincipalId: principal}
	if c.label != "" {
		req.Labels = map[string]string{"agent_id": c.label}
	}
	_, err := s.SubmitJob(ctx, req)
	res := linkLookupResult{ok: err == nil, code: status.Code(err)}
	if err != nil {
		res.message = status.Convert(err).Message()
		res.raw = err.Error()
	}
	res.observe(t, s, bus, safety)
	return res
}

func runLinkLookupHTTP(t *testing.T, c linkLookupCase) linkLookupResult {
	t.Helper()
	s, bus, safety := newAgentBindingGateway(t) // worker-a -> agent-a, worker-b -> agent-b
	if c.dangle {
		deleteAgentRecord(t, s, "agent-b")
	}
	if c.failing {
		s.agentIdentityStore = failingAgentStore()
	}
	s.auth = &auth.BasicAuthProvider{}
	principal, ac := "", &auth.AuthContext{Tenant: "default", PrincipalID: "worker-b", Role: "admin"}
	switch c.caller {
	case "unlinked":
		ac.PrincipalID = "worker-c"
	case "request":
		s.auth, ac, principal = nil, nil, "worker-b"
	case "empty":
		s.auth, ac = nil, nil
	}
	rec := httptest.NewRecorder()
	s.handleSubmitJobHTTP(rec, agentBindingRequest(t, principal, c.label, ac))
	res := linkLookupResult{ok: rec.Code == http.StatusOK, status: rec.Code, raw: rec.Body.String()}
	var body struct {
		Error string `json:"error"`
	}
	if !res.ok {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
		}
		res.message = body.Error
	}
	res.observe(t, s, bus, safety)
	return res
}

func runLinkLookup(t *testing.T, transport string, c linkLookupCase) linkLookupResult {
	t.Helper()
	if transport == "grpc" {
		return runLinkLookupGRPC(t, c)
	}
	return runLinkLookupHTTP(t, c)
}

// TestSubmitAgentLinkLookupFailureFailsClosed pins that a failed worker-link
// lookup is never read as "no link": the submit stops with Unavailable / 503
// and a generic message before the policy check, job persistence and bus
// publish. Each case first runs the same submit with a healthy store, so the
// zero-effect assertions are known to see a policy check, a publish and a job.
func TestSubmitAgentLinkLookupFailureFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, transport, caller, label, wantAgent string
	}{
		{"grpc_no_label", "grpc", "", "", "agent-source"},
		{"grpc_label", "grpc", "", "agent-source", "agent-source"},
		{"http_no_label", "http", "", "", "agent-b"},
		{"http_label", "http", "", "agent-b", "agent-b"},
		// HTTP still looks up a request-named principal; task-491f974b may stop that lookup and flip this case.
		{"http_request_principal", "http", "request", "", "agent-b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := linkLookupCase{caller: tc.caller, label: tc.label}
			healthy := runLinkLookup(t, tc.transport, c)
			if !healthy.ok || !healthy.policy || healthy.agentID != tc.wantAgent || healthy.published == 0 || healthy.jobs != 1 {
				t.Fatalf("healthy control = %+v, want ok, policy agent_id %q, a publish and one job", healthy, tc.wantAgent)
			}
			c.failing = true
			got := runLinkLookup(t, tc.transport, c)
			if tc.transport == "grpc" {
				if got.code != codes.Unavailable || got.message != "agent identity store unavailable" {
					t.Fatalf("code=%v message=%q, want Unavailable %q", got.code, got.message, "agent identity store unavailable")
				}
			} else if got.status != http.StatusServiceUnavailable || got.message != "service unavailable" {
				t.Fatalf("status=%d error=%q, want 503 %q", got.status, got.message, "service unavailable")
			}
			if strings.Contains(got.raw, "closed") || strings.Contains(got.raw, "redis") {
				t.Fatalf("response leaks the store error: %q", got.raw)
			}
			if got.policy || got.published != 0 || got.jobs != 0 {
				t.Fatalf("failed lookup had effects: policy=%v published=%d jobs=%d", got.policy, got.published, got.jobs)
			}
		})
	}
}

// TestSubmitAgentLinkLookupKeepsNoLinkBehavior pins today's behavior where
// the store reports no link (absent or dangling), and where the principal is
// empty or blank, so no lookup runs even while the store is down.
func TestSubmitAgentLinkLookupKeepsNoLinkBehavior(t *testing.T) {
	const noCredential = "client-supplied agent_id requires an authenticated worker credential"
	for _, tc := range []struct {
		name, transport string
		c               linkLookupCase
		wantRefusal     string // "" = the submit proceeds with no agent_id
	}{
		{"grpc_anonymous_failing_store", "grpc", linkLookupCase{caller: "anonymous", failing: true}, ""},
		{"grpc_blank_principal_failing_store", "grpc", linkLookupCase{caller: "blank", failing: true}, ""},
		{"http_empty_principal_failing_store", "http", linkLookupCase{caller: "empty", failing: true}, ""},
		{"grpc_absent_link_no_label", "grpc", linkLookupCase{unlink: true}, ""},
		{"grpc_absent_link_label", "grpc", linkLookupCase{unlink: true, label: "agent-other"}, noCredential},
		{"grpc_dangling_link_no_label", "grpc", linkLookupCase{dangle: true}, ""},
		{"grpc_dangling_link_label", "grpc", linkLookupCase{dangle: true, label: "agent-source"}, noCredential},
		{"http_absent_link_no_label", "http", linkLookupCase{caller: "unlinked"}, ""},
		{"http_absent_link_label", "http", linkLookupCase{caller: "unlinked", label: "agent-b"}, noCredential},
		{"http_dangling_link_no_label", "http", linkLookupCase{dangle: true}, ""},
		{"http_dangling_link_label", "http", linkLookupCase{dangle: true, label: "agent-b"}, noCredential},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runLinkLookup(t, tc.transport, tc.c)
			if tc.wantRefusal == "" {
				if !got.ok || !got.policy || got.agentID != "" || got.published == 0 || got.jobs != 1 {
					t.Fatalf("got %+v, want ok, a policy check with no agent_id, a publish and one job", got)
				}
				return
			}
			wantCode, wantStatus := codes.PermissionDenied, http.StatusForbidden
			if got.ok || got.message != tc.wantRefusal || (tc.transport == "grpc" && got.code != wantCode) || (tc.transport == "http" && got.status != wantStatus) {
				t.Fatalf("got %+v, want refusal %q", got, tc.wantRefusal)
			}
			if got.policy || got.published != 0 || got.jobs != 0 {
				t.Fatalf("refused submit had effects: policy=%v published=%d jobs=%d", got.policy, got.published, got.jobs)
			}
		})
	}
}

// TestDelegateAgentLinkLookupFailureFailsClosed pins delegation issue: a
// failed caller-link lookup returns 503 and audits outcome "error" with a
// generic reason, never a 403 "denied"; an empty principal is still denied
// without a lookup.
func TestDelegateAgentLinkLookupFailureFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, principal string
		failing         bool
		wantStatus      int
		wantOutcome     string
	}{
		{"healthy_control", "worker-a", false, http.StatusCreated, "ok"},
		{"store_failure", "worker-a", true, http.StatusServiceUnavailable, "error"},
		{"empty_principal_keeps_denial", "", true, http.StatusForbidden, "denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			enableTestAuth(s)
			setTestEntitlements(t, s, licensing.PlanEnterprise, func(e *licensing.Entitlements) {
				e.AgentIdentity = true
				e.RBAC = true
			})
			setDelegationKeys(t)
			putTestRole(t, s, "operator", auth.PermAgentsDelegate)
			createDelegationAgent(t, s, "default", "agent-a", []string{"read"}, []string{"job.alpha"})
			createDelegationAgent(t, s, "default", "agent-b", []string{"read"}, []string{"job.alpha"})
			if err := s.agentIdentityStore.LinkWorker(context.Background(), "agent-a", "worker-a"); err != nil {
				t.Fatalf("link worker: %v", err)
			}
			sink := &recordingAuditSender{}
			s.auditExporter = sink
			if tc.failing {
				s.agentIdentityStore = failingAgentStore()
			}
			req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-a/delegate",
				strings.NewReader(`{"target_agent_id":"agent-b","allowed_actions":["read"],"allowed_topics":["job.alpha"]}`)),
				// operator normalizes to admin for the route's role gate but is not
				// "admin" in handleDelegateAgent, so it takes the caller-link check.
				&auth.AuthContext{Tenant: "default", PrincipalID: tc.principal, Role: "operator"})
			req.SetPathValue("id", "agent-a")
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			s.handleDelegateAgent(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status=%d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "closed") || strings.Contains(rec.Body.String(), "redis") {
				t.Fatalf("response leaks the store error: %s", rec.Body.String())
			}
			if len(sink.events) != 1 {
				t.Fatalf("audit events=%d, want 1: %+v", len(sink.events), sink.events)
			}
			event := sink.events[0]
			if event.Action != "delegation.issue" || event.Extra["outcome"] != tc.wantOutcome {
				t.Fatalf("audit action=%q outcome=%q, want delegation.issue %q", event.Action, event.Extra["outcome"], tc.wantOutcome)
			}
			if tc.wantOutcome == "error" {
				var body struct {
					Error string `json:"error"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error != "service unavailable" {
					t.Fatalf("error body=%s (decode err %v), want %q", rec.Body.String(), err, "service unavailable")
				}
				if event.Reason != "agent identity store unavailable" {
					t.Fatalf("audit reason=%q, want the generic %q", event.Reason, "agent identity store unavailable")
				}
			}
		})
	}
}
