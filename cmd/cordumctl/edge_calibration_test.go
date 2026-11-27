package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/calibration"
)

const calibrationTestID = "cal-0123456789abcdef0123456789abcdef"

type calibrationRecorded struct {
	method, path string
	body         []byte
}

// newCalibrationServer answers every request with status and body and
// records each request it receives.
func newCalibrationServer(t *testing.T, status int, body []byte) (*httptest.Server, func() []calibrationRecorded) {
	t.Helper()
	t.Setenv("CORDUM_TLS_CA", "")
	var mu sync.Mutex
	var got []calibrationRecorded
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, calibrationRecorded{method: r.Method, path: r.URL.Path, body: data})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []calibrationRecorded {
		mu.Lock()
		defer mu.Unlock()
		return append([]calibrationRecorded(nil), got...)
	}
}

// calibrationTestReport fills every section; Rules has an empty rule id and
// TopCommandClasses is empty.
func calibrationTestReport() calibration.Report {
	return calibration.Report{
		ID: calibrationTestID, TenantID: "acme",
		Since: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), Until: time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC),
		SessionsObserved: 3, EventsScanned: 6, Truncated: true,
		Rules:             []calibration.RuleCount{{Deny: 1}, {RuleID: "deny-rm", Deny: 2}, {RuleID: "approve-push", RequireApproval: 3}},
		NeverFired:        []string{"rule-quiet"},
		TopTools:          []calibration.NameCount{{Name: "Bash", Count: 4}},
		TopCommandClasses: []calibration.NameCount{},
		ApprovalsPerDay:   []calibration.DayCount{{Day: "2026-11-01", Count: 2}, {Day: "2026-11-02", Count: 1}},
		ShadowRules:       []calibration.ShadowRuleCount{{BundleID: "candidate", RuleID: "deny-net", Deny: 5, RequireApproval: 1}},
		Assurance: map[edge.AssuranceState]int{
			edge.AssuranceStateCompatibility: 2, edge.AssuranceStateExecutorEnforced: 0, edge.AssuranceStateHookMediated: 0,
			edge.AssuranceStateStale: 1, edge.AssuranceStateUnsupported: 0,
		},
	}
}

func calibrationTestBody(t *testing.T, report calibration.Report) []byte {
	t.Helper()
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	return body
}

func runCalibrationCLI(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := runEdgeCalibrationReportCmd(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func decodeCalibrationPost(t *testing.T, got []calibrationRecorded) edgeCalibrationRequest {
	t.Helper()
	if len(got) != 1 || got[0].method != http.MethodPost || got[0].path != edgeCalibrationPath {
		t.Fatalf("requests = %+v, want one POST %s", got, edgeCalibrationPath)
	}
	var body edgeCalibrationRequest
	if err := json.Unmarshal(got[0].body, &body); err != nil {
		t.Fatalf("decode POST body %q: %v", got[0].body, err)
	}
	return body
}

func TestEdgeCalibrationCLIRendersSummary(t *testing.T) {
	srv, requests := newCalibrationServer(t, http.StatusCreated, calibrationTestBody(t, calibrationTestReport()))
	code, stdout, stderr := runCalibrationCLI("--gateway", srv.URL, "--since", "2026-11-01T00:00:00Z",
		"--until", "2026-11-03T00:00:00Z", "--repos", "org/a, org/b", "--labels", "team=edge,env=prod")
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	wantBody := edgeCalibrationRequest{
		Since: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), Until: time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC),
		Repos: []string{"org/a", "org/b"}, Labels: map[string]string{"team": "edge", "env": "prod"},
	}
	if body := decodeCalibrationPost(t, requests()); !reflect.DeepEqual(body, wantBody) {
		t.Fatalf("POST body = %+v, want %+v", body, wantBody)
	}
	want := `Calibration report cal-0123456789abcdef0123456789abcdef
Tenant: acme  Window: 2026-11-01T00:00:00Z to 2026-11-03T00:00:00Z  Truncated: yes
Policy decisions recorded in observe-mode sessions (3 sessions, 6 events):
  (no rule)  deny=1  require_approval=0
  deny-rm  deny=2  require_approval=0
  approve-push  deny=0  require_approval=3
Rules with no matching Edge event:
  rule-quiet
Most affected tools:
  Bash  4
Most affected command classes:
  (none)
Approval requests per UTC day (mean 1.5):
  2026-11-01  2
  2026-11-02  1
Candidate shadow bundles, what each would decide (tenant-wide, not filtered by scope):
  candidate/deny-net  deny=5  require_approval=1
Assurance state of observed sessions (at report time):
  compatibility  2
  executor-enforced  0
  hook-mediated  0
  stale  1
  unsupported  0
`
	if stdout != want {
		t.Fatalf("summary:\n%s\nwant:\n%s", stdout, want)
	}
}

func TestEdgeCalibrationCLIEmptyReport(t *testing.T) {
	report := calibrationTestReport()
	report.SessionsObserved, report.EventsScanned, report.Rules = 0, 0, nil
	srv, requests := newCalibrationServer(t, http.StatusCreated, calibrationTestBody(t, report))
	code, stdout, stderr := runCalibrationCLI("--gateway", srv.URL, "--until", "2026-11-03T00:00:00Z")
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	body := decodeCalibrationPost(t, requests())
	if wantSince := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC); !body.Since.Equal(wantSince) {
		t.Fatalf("default since = %s, want %s (until minus 7 days)", body.Since, wantSince)
	}
	want := "Policy decisions recorded in observe-mode sessions (0 sessions, 0 events):\n" +
		"  No observe-mode sessions matched this window and scope.\n" +
		"Rules with no matching Edge event:\n"
	if !strings.Contains(stdout, want) {
		t.Fatalf("summary:\n%s\nwant it to contain:\n%s", stdout, want)
	}
}

func TestEdgeCalibrationCLIJSONPassThrough(t *testing.T) {
	served := calibrationTestBody(t, calibrationTestReport())
	srv, _ := newCalibrationServer(t, http.StatusCreated, served)
	code, stdout, stderr := runCalibrationCLI("--gateway", srv.URL, "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if stdout != string(served) {
		t.Fatalf("--json output:\n%s\nwant the server body:\n%s", stdout, served)
	}
}

func TestEdgeCalibrationCLIGetByID(t *testing.T) {
	srv, requests := newCalibrationServer(t, http.StatusOK, calibrationTestBody(t, calibrationTestReport()))
	code, stdout, stderr := runCalibrationCLI("get", calibrationTestID, "--gateway", srv.URL)
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	got := requests()
	if len(got) != 1 || got[0].method != http.MethodGet || got[0].path != edgeCalibrationPath+"/"+calibrationTestID || len(got[0].body) != 0 {
		t.Fatalf("requests = %+v, want one GET %s/%s with no body", got, edgeCalibrationPath, calibrationTestID)
	}
	if !strings.HasPrefix(stdout, "Calibration report "+calibrationTestID+"\n") {
		t.Fatalf("summary does not start with the report id:\n%s", stdout)
	}
}

func TestEdgeCalibrationCLIFailedRequest(t *testing.T) {
	srv, _ := newCalibrationServer(t, http.StatusNotFound, []byte(`{"error":"calibration report not found"}`))
	code, stdout, stderr := runCalibrationCLI("get", calibrationTestID, "--gateway", srv.URL)
	if code != 1 || stdout != "" || !strings.Contains(stderr, "status 404") {
		t.Fatalf("exit %d, stdout %q, stderr %q; want exit 1, no output and the 404 on stderr", code, stdout, stderr)
	}
}

func TestEdgeCalibrationCLIUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"bad since", []string{"--since", "yesterday"}},
		{"label without equals", []string{"--labels", "team"}},
		{"get without id", []string{"get"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, requests := newCalibrationServer(t, http.StatusCreated, calibrationTestBody(t, calibrationTestReport()))
			code, stdout, stderr := runCalibrationCLI(append([]string{"--gateway", srv.URL}, tc.args...)...)
			if code != 2 || stdout != "" || !strings.Contains(stderr, "usage: cordumctl edge calibration-report") {
				t.Fatalf("exit %d, stdout %q, stderr %q; want exit 2 with the usage", code, stdout, stderr)
			}
			if got := requests(); len(got) != 0 {
				t.Fatalf("a usage error sent %d request(s): %+v", len(got), got)
			}
		})
	}
}
