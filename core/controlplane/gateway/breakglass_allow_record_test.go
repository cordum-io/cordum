package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/licensing"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

const (
	breakGlassAllowRecordKey      = "emergency-key"
	breakGlassAllowRecordOtherJob = "job-breakglass-other-tenant"
)

// TestBreakGlassAllowRecord pins when the break-glass allow record (audit event, decision
// log line and allow metric) is written: once the gateway answers the request, and only for
// an answer below 400 or a connection upgrade. No t.Parallel: the log capture swaps the
// global logger.
func TestBreakGlassAllowRecord(t *testing.T) {
	t.Run("routes", breakGlassAllowRecordRoutes)
	t.Run("answer", breakGlassAllowRecordAnswers)
}

// breakGlassAllowRecordForce forces a license state and checks its break-glass state and
// RBAC entitlement. Only enterprise grace has the RBAC entitlement.
func breakGlassAllowRecordForce(t *testing.T, s *server, name string) licensing.BreakGlassState {
	t.Helper()
	want, rbac := licensing.BreakGlassStateGrace, false
	switch name {
	case "team_grace":
		s.entitlements.ForceStateWithStatus(licensing.PlanTeam, licensing.DefaultEntitlements(licensing.PlanTeam), nil, "grace")
	case "enterprise_grace":
		s.entitlements.ForceStateWithStatus(licensing.PlanEnterprise, licensing.DefaultEntitlements(licensing.PlanEnterprise), nil, "grace")
		rbac = true
	case "degraded":
		setBreakGlassState(t, s, "degraded")
		want = licensing.BreakGlassStateDegraded
	default:
		t.Fatalf("unknown license state %q", name)
	}
	if got := s.currentBreakGlassState(); got != want {
		t.Fatalf("break-glass state = %q, want %q", got, want)
	}
	if got := auth.RBACEntitled(s.currentEntitlements()); got != rbac {
		t.Fatalf("rbac entitled = %t, want %t", got, rbac)
	}
	return want
}

// breakGlassAllowRecorder counts allow records three ways: audit events, decision log
// lines and the allow metric.
type breakGlassAllowRecorder struct {
	sink   *testAuditSender
	logs   *bytes.Buffer
	state  licensing.BreakGlassState
	before float64
}

func breakGlassAllowRecordWatch(t *testing.T, s *server, state licensing.BreakGlassState) *breakGlassAllowRecorder {
	t.Helper()
	logs, restore := captureBreakGlassLogger(t)
	t.Cleanup(restore)
	sink := &testAuditSender{}
	s.auditExporter = sink
	return &breakGlassAllowRecorder{sink: sink, logs: logs, state: state, before: breakGlassDecisionCount(t, "allow", state)}
}

// expect requires exactly want allow records of each kind, each naming permission and the state.
func (c *breakGlassAllowRecorder) expect(t *testing.T, want int, permission string) {
	t.Helper()
	events := 0
	for i := 0; i < c.sink.Len(); i++ {
		ev := c.sink.Get(i)
		if ev.EventType != audit.EventLicenseBreakglassActivated {
			continue
		}
		events++
		if ev.Extra["decision"] != "allow" || ev.Extra["permission"] != permission || ev.Extra["state"] != string(c.state) {
			t.Fatalf("allow audit event extra = %v, want permission %s and state %s", ev.Extra, permission, c.state)
		}
	}
	lines := 0
	for _, line := range strings.Split(c.logs.String(), "\n") {
		var entry struct {
			Msg        string `json:"msg"`
			Decision   string `json:"decision"`
			Permission string `json:"permission"`
			State      string `json:"state"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil || entry.Msg != "license break-glass decision" || entry.Decision != "allow" {
			continue
		}
		lines++
		if entry.Permission != permission || entry.State != string(c.state) {
			t.Fatalf("allow log line = %s, want permission %s and state %s", line, permission, c.state)
		}
	}
	metric := breakGlassDecisionCount(t, "allow", c.state) - c.before
	if events != want || lines != want || metric != float64(want) {
		t.Fatalf("allow records: %d audit events, %d log lines, metric +%v; want %d of each", events, lines, metric, want)
	}
}

// breakGlassAllowRecordRoute is one request through registerRoutes, its answer and its
// allow-record count.
type breakGlassAllowRecordRoute struct {
	name, state, permission string
	status, records         int
	request                 func(t *testing.T, s *server) *http.Request
}

var breakGlassAllowRecordRouteCases = []breakGlassAllowRecordRoute{
	{"team_grace_eval_runs_viewer", "team_grace", auth.PermEvalsRunsRead, http.StatusForbidden, 0, breakGlassAllowRecordEvalRuns},
	{"enterprise_grace_eval_runs_viewer", "enterprise_grace", auth.PermEvalsRunsRead, http.StatusForbidden, 0, breakGlassAllowRecordEvalRuns},
	{"enterprise_grace_job_other_tenant", "enterprise_grace", auth.PermJobsRead, http.StatusForbidden, 0, breakGlassAllowRecordOtherTenantJob},
	{"team_grace_edge_upstreams_no_tenant", "team_grace", auth.PermMCPRead, http.StatusBadRequest, 0, breakGlassAllowRecordUpstreams},
	{"degraded_login_bad_key", "degraded", licensing.BreakGlassPermissionAuthLogin, http.StatusUnauthorized, 0, breakGlassAllowRecordLogin("wrong-key")},
	{"team_grace_jobs_viewer", "team_grace", auth.PermJobsRead, http.StatusOK, 1, breakGlassAllowRecordJobs},
	{"enterprise_grace_jobs_viewer", "enterprise_grace", auth.PermJobsRead, http.StatusOK, 1, breakGlassAllowRecordJobs},
	{"degraded_login", "degraded", licensing.BreakGlassPermissionAuthLogin, http.StatusOK, 1, breakGlassAllowRecordLogin(breakGlassAllowRecordKey)},
}

func breakGlassAllowRecordRoutes(t *testing.T) {
	for _, tc := range breakGlassAllowRecordRouteCases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			s.tenant = "default"
			enableTestAuth(s)
			state := breakGlassAllowRecordForce(t, s, tc.state)
			req := tc.request(t, s)
			mux := http.NewServeMux()
			if err := s.registerRoutes(mux); err != nil {
				t.Fatalf("registerRoutes: %v", err)
			}
			recorder := breakGlassAllowRecordWatch(t, s, state)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)
			if rr.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rr.Code, tc.status, rr.Body.String())
			}
			recorder.expect(t, tc.records, tc.permission)
		})
	}
}

func breakGlassAllowRecordAs(method, target, role string) *http.Request {
	return withAuth(httptest.NewRequest(method, target, nil), &auth.AuthContext{
		Tenant: "default", Role: role, PrincipalID: role + "-1",
	})
}

func breakGlassAllowRecordEvalRuns(*testing.T, *server) *http.Request {
	return breakGlassAllowRecordAs(http.MethodGet, "/api/v1/evals/datasets/ds-1/runs", "viewer")
}

func breakGlassAllowRecordJobs(*testing.T, *server) *http.Request {
	return breakGlassAllowRecordAs(http.MethodGet, "/api/v1/jobs", "viewer")
}

func breakGlassAllowRecordUpstreams(*testing.T, *server) *http.Request {
	return breakGlassAllowRecordAs(http.MethodGet, "/api/v1/edge/mcp/upstreams", "admin")
}

// breakGlassAllowRecordOtherTenantJob seeds a job of tenant other and asks for it as a
// viewer of tenant default.
func breakGlassAllowRecordOtherTenantJob(t *testing.T, s *server) *http.Request {
	t.Helper()
	ctx := context.Background()
	job := &pb.JobRequest{JobId: breakGlassAllowRecordOtherJob, Topic: "job.test", TenantId: "other"}
	if err := s.jobStore.SetJobMeta(ctx, job); err != nil {
		t.Fatalf("set job meta: %v", err)
	}
	if err := s.jobStore.SetState(ctx, job.JobId, model.JobStatePending); err != nil {
		t.Fatalf("set job state: %v", err)
	}
	return breakGlassAllowRecordAs(http.MethodGet, "/api/v1/jobs/"+job.JobId, "viewer")
}

// breakGlassAllowRecordLogin logs in with an API key, as TestBreakGlass_DegradedStateLoginSucceeds does.
func breakGlassAllowRecordLogin(password string) func(*testing.T, *server) *http.Request {
	return func(t *testing.T, s *server) *http.Request {
		t.Helper()
		s.auth = newBasicAuthForTest(t, map[string]string{
			"CORDUM_API_KEYS": `[{"key":"` + breakGlassAllowRecordKey + `","role":"admin","principal_id":"breakglass","tenant":"default"}]`,
		})
		body := `{"username":"breakglass","password":"` + password + `"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		return req
	}
}

// breakGlassAllowRecordAnswer is a handler behind s.instrumented that takes the license
// decision on jobs.read, before its answer or, with late set, after it. inside >= 0 counts
// the records right after the answer, inside the handler.
type breakGlassAllowRecordAnswer struct {
	name          string
	late          bool
	inside, after int
	answer        func(t *testing.T, w http.ResponseWriter, r *http.Request)
}

var breakGlassAllowRecordAnswerCases = []breakGlassAllowRecordAnswer{
	{"header_403", false, -1, 0, breakGlassAllowRecordStatus(http.StatusForbidden)},
	{"header_400", false, -1, 0, breakGlassAllowRecordStatus(http.StatusBadRequest)},
	{"redirect_302", false, -1, 1, func(_ *testing.T, w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/next", http.StatusFound)
	}},
	{"write_body", false, 1, 1, breakGlassAllowRecordWrite(1)},
	{"flush", false, 1, 1, func(t *testing.T, w http.ResponseWriter, _ *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("writer does not flush")
		}
		flusher.Flush()
	}},
	{"write_twice", false, -1, 1, breakGlassAllowRecordWrite(2)},
	{"hijack", false, 1, 1, func(t *testing.T, w http.ResponseWriter, _ *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("writer does not hijack")
		}
		if _, _, err := hijacker.Hijack(); err != nil {
			t.Fatalf("hijack: %v", err)
		}
	}},
	{"no_write", false, -1, 1, func(*testing.T, http.ResponseWriter, *http.Request) {}},
	{"after_answer_200", true, -1, 1, breakGlassAllowRecordStatus(http.StatusOK)},
	{"after_answer_403", true, -1, 0, breakGlassAllowRecordStatus(http.StatusForbidden)},
}

func breakGlassAllowRecordStatus(code int) func(*testing.T, http.ResponseWriter, *http.Request) {
	return func(_ *testing.T, w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
}

func breakGlassAllowRecordWrite(times int) func(*testing.T, http.ResponseWriter, *http.Request) {
	return func(t *testing.T, w http.ResponseWriter, _ *http.Request) {
		for i := 0; i < times; i++ {
			if _, err := w.Write([]byte("ok")); err != nil {
				t.Fatalf("write: %v", err)
			}
		}
	}
}

// breakGlassAllowRecordHijacker is a response writer that can be hijacked over net.Pipe.
type breakGlassAllowRecordHijacker struct {
	*httptest.ResponseRecorder
	conn net.Conn
}

func (h *breakGlassAllowRecordHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return h.conn, bufio.NewReadWriter(bufio.NewReader(h.conn), bufio.NewWriter(h.conn)), nil
}

func breakGlassAllowRecordAnswers(t *testing.T) {
	for _, tc := range breakGlassAllowRecordAnswerCases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			s.tenant = "default"
			enableTestAuth(s)
			recorder := breakGlassAllowRecordWatch(t, s, breakGlassAllowRecordForce(t, s, "team_grace"))
			gate := func(w http.ResponseWriter, r *http.Request) {
				if !s.requireLicensePermission(w, r, auth.PermJobsRead) {
					t.Fatal("grace refused jobs.read")
				}
			}
			h := s.instrumented("/test", func(w http.ResponseWriter, r *http.Request) {
				if !tc.late {
					gate(w, r)
				}
				tc.answer(t, w, r)
				if tc.late {
					gate(w, r)
				}
				if tc.inside >= 0 {
					recorder.expect(t, tc.inside, auth.PermJobsRead)
				}
			})
			client, server := net.Pipe()
			t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
			w := &breakGlassAllowRecordHijacker{ResponseRecorder: httptest.NewRecorder(), conn: server}
			h(w, breakGlassAllowRecordAs(http.MethodGet, "/test", "viewer"))
			recorder.expect(t, tc.after, auth.PermJobsRead)
		})
	}
}
