package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/evals/runner"
	"github.com/cordum/cordum/core/governance"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/licensing"
	"github.com/cordum/cordum/core/model"
)

const (
	adminBreakGlassGraceCode    = "license.grace_restricted"
	adminBreakGlassDegradedCode = "license.degraded"
	adminBreakGlassHealthPath   = "/api/v1/governance/health?tenant=default"
	adminBreakGlassAllowPolicy  = `rules:
  - id: allow-all
    match:
      topics:
        - job.*
    decision: allow
`
)

// adminBreakGlassState is a forced license state and the break-glass state it maps to.
type adminBreakGlassState struct {
	name, status string
	plan         licensing.Plan
	breakGlass   licensing.BreakGlassState
}

var adminBreakGlassStates = []adminBreakGlassState{
	{"valid", "active", licensing.PlanEnterprise, licensing.BreakGlassStateValid},
	{"warning", "warning", licensing.PlanEnterprise, licensing.BreakGlassStateValid},
	{"grace", "grace", licensing.PlanEnterprise, licensing.BreakGlassStateGrace},
	{"degraded", "fallback", licensing.PlanCommunity, licensing.BreakGlassStateInvalid},
	{"invalid", "fallback", licensing.PlanCommunity, licensing.BreakGlassStateInvalid},
	{"missing", "active", licensing.PlanCommunity, licensing.BreakGlassStateValid},
}

// adminBreakGlassRoute is an admin route, the permission its break-glass decision names,
// and whether grace allows it. run sends one admin request; want is the expected 503 code,
// or "" when the request answers.
type adminBreakGlassRoute struct {
	name, permission string
	graceAllowed     bool
	run              func(t *testing.T, st adminBreakGlassState, want string)
}

var adminBreakGlassRoutes = []adminBreakGlassRoute{
	{"eval_run", auth.PermEvalsRunsExecute, false, adminBreakGlassEvalRun},
	{"eval_delete", auth.PermEvalsRunsDelete, false, adminBreakGlassEvalDelete},
	{"governance_health", auth.PermGovernanceRead, true, adminBreakGlassGovernanceHealth},
	{"resign", auth.PermPolicyWrite, false, adminBreakGlassResign},
}

// TestAdminRoutesBreakGlassDecision pins the license break-glass decision of four admin
// routes in every license state. A refusal answers the break-glass 503 and changes nothing,
// and the same server answers an admin's session read in every state.
func TestAdminRoutesBreakGlassDecision(t *testing.T) {
	for _, st := range adminBreakGlassStates {
		t.Run(st.name, func(t *testing.T) {
			for _, route := range adminBreakGlassRoutes {
				t.Run(route.name, func(t *testing.T) {
					want := adminBreakGlassWant(st, route)
					logs, restore := captureBreakGlassLogger(t)
					t.Cleanup(restore)
					route.run(t, st, want)
					if st.breakGlass != licensing.BreakGlassStateValid {
						decision := "allow"
						if want != "" {
							decision = "deny"
						}
						adminBreakGlassLogged(t, logs, route.permission, decision)
					}
				})
			}
			if st.name == "degraded" {
				t.Run("viewer_governance_health", func(t *testing.T) { adminBreakGlassViewerHealth(t, st) })
				t.Run("viewer_resign", func(t *testing.T) { adminBreakGlassViewerResign(t, st) })
			}
		})
	}
}

func adminBreakGlassWant(st adminBreakGlassState, route adminBreakGlassRoute) string {
	switch {
	case st.breakGlass == licensing.BreakGlassStateValid:
		return ""
	case st.breakGlass == licensing.BreakGlassStateGrace && route.graceAllowed:
		return ""
	case st.breakGlass == licensing.BreakGlassStateGrace:
		return adminBreakGlassGraceCode
	default:
		return adminBreakGlassDegradedCode
	}
}

func adminBreakGlassForce(t *testing.T, s *server, st adminBreakGlassState) {
	t.Helper()
	s.entitlements.ForceStateWithStatus(st.plan, licensing.DefaultEntitlements(st.plan), nil, st.status)
	if got := s.currentBreakGlassState(); got != st.breakGlass {
		t.Fatalf("break-glass state = %q, want %q", got, st.breakGlass)
	}
}

// adminBreakGlassBefore is the Redis keyspace and the bus publish count before a request.
type adminBreakGlassBefore struct {
	keyspace  map[string]string
	published int
}

func adminBreakGlassSnapshot(t *testing.T, s *server, bus *stubBus) adminBreakGlassBefore {
	t.Helper()
	return adminBreakGlassBefore{keyspace: storeWriterKeyspace(t, s), published: storeWriterPublished(bus)}
}

func (b adminBreakGlassBefore) unchanged(t *testing.T, s *server, bus *stubBus) {
	t.Helper()
	if got := storeWriterKeyspace(t, s); !reflect.DeepEqual(got, b.keyspace) {
		t.Fatalf("refused request changed the keyspace:\nbefore %v\nafter  %v", b.keyspace, got)
	}
	if got := storeWriterPublished(bus); got != b.published {
		t.Fatalf("refused request published %d bus messages", got-b.published)
	}
}

// adminBreakGlassRefused checks the break-glass 503 body: exactly its six keys, the expected
// code and the forced break-glass state.
func adminBreakGlassRefused(t *testing.T, rr *httptest.ResponseRecorder, st adminBreakGlassState, want string) {
	t.Helper()
	var body map[string]any
	if rr.Code != http.StatusServiceUnavailable || json.Unmarshal(rr.Body.Bytes(), &body) != nil {
		t.Fatalf("response = %d %s, want 503 %s", rr.Code, rr.Body.String(), want)
	}
	for _, key := range []string{"error", "code", "error_code", "status", "state", "upgrade_url"} {
		if _, ok := body[key]; !ok {
			t.Fatalf("503 body %s lacks %q", rr.Body.String(), key)
		}
	}
	if len(body) != 6 || body["code"] != want || body["error_code"] != want || body["state"] != string(st.breakGlass) {
		t.Fatalf("503 body = %s, want code and error_code %s and state %s", rr.Body.String(), want, st.breakGlass)
	}
}

// adminBreakGlassLogged requires a break-glass log line with this permission and decision.
func adminBreakGlassLogged(t *testing.T, logs *bytes.Buffer, permission, decision string) {
	t.Helper()
	for _, line := range strings.Split(logs.String(), "\n") {
		var entry struct {
			Msg        string `json:"msg"`
			Decision   string `json:"decision"`
			Permission string `json:"permission"`
		}
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Msg == "license break-glass decision" &&
			entry.Permission == permission && entry.Decision == decision {
			return
		}
	}
	t.Fatalf("no break-glass %s line for %s in the log:\n%s", decision, permission, logs.String())
}

func adminBreakGlassServe(t *testing.T, s *server, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	if err := s.registerRoutes(mux); err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

// adminBreakGlassSessionControl is the positive control: an admin's session read, which
// break-glass allows in every license state, answers 200 on the same server.
func adminBreakGlassSessionControl(t *testing.T, s *server) {
	t.Helper()
	rr := adminBreakGlassServe(t, s, adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)))
	if rr.Code != http.StatusOK {
		t.Fatalf("session control = %d %s, want 200", rr.Code, rr.Body.String())
	}
}

func adminBreakGlassEvalRun(t *testing.T, st adminBreakGlassState, want string) {
	prev := evalRunAsyncSpawn
	evalRunAsyncSpawn = func(fn func()) { fn() }
	t.Cleanup(func() { evalRunAsyncSpawn = prev })

	s, bus, _ := newTestGateway(t)
	enableTestAuth(s)
	seedPolicyBundle(t, s, "secops/base", adminBreakGlassAllowPolicy)
	dataset := seedEvalRunDataset(t, s, "breakglass-run", 1, []evalRunEntrySpec{
		{ID: "entry-1", Topic: "job.deploy", Expected: model.SafetyAllow},
	})
	adminBreakGlassForce(t, s, st)
	mux := bindEvalRunRoutes(t, s)
	before := adminBreakGlassSnapshot(t, s, bus)
	rr := evalRunPost(t, mux, dataset.ID, map[string]any{"use_current_policy": true}, "admin")
	if want != "" {
		adminBreakGlassRefused(t, rr, st, want)
		before.unchanged(t, s, bus)
	} else {
		var result runner.RunResult
		if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &result) != nil || result.RunID == "" {
			t.Fatalf("eval run = %d %s, want 200 with a run", rr.Code, rr.Body.String())
		}
		if _, err := s.evalRunStore.GetRun(context.Background(), testEvalTenant, result.RunID); err != nil {
			t.Fatalf("run %s is not stored: %v", result.RunID, err)
		}
	}
	adminBreakGlassSessionControl(t, s)
}

func adminBreakGlassEvalDelete(t *testing.T, st adminBreakGlassState, want string) {
	ctx := context.Background()
	s, bus, _ := newTestGateway(t)
	enableTestAuth(s)
	dataset := seedEvalRunDataset(t, s, "breakglass-delete", 1, []evalRunEntrySpec{
		{ID: "entry-1", Topic: "job.deploy", Expected: model.SafetyAllow},
	})
	run := runner.RunResult{
		RunID:          "run-breakglass-delete",
		DatasetID:      dataset.ID,
		DatasetName:    dataset.Name,
		DatasetVersion: dataset.Version,
		Tenant:         testEvalTenant,
		PolicySnapshot: "snap-breakglass",
		StartedAt:      time.Date(2026, time.November, 26, 9, 0, 0, 0, time.UTC),
		CompletedAt:    time.Date(2026, time.November, 26, 9, 0, 1, 0, time.UTC),
		Summary:        runner.RunSummary{Total: 1, Passed: 1},
	}
	if err := s.evalRunStore.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	adminBreakGlassForce(t, s, st)
	mux := bindEvalRunRoutes(t, s)
	before := adminBreakGlassSnapshot(t, s, bus)
	rr := evalRunDelete(t, mux, run.RunID, "?force=true", "admin")
	_, getErr := s.evalRunStore.GetRun(ctx, testEvalTenant, run.RunID)
	if want != "" {
		adminBreakGlassRefused(t, rr, st, want)
		before.unchanged(t, s, bus)
		if getErr != nil {
			t.Fatalf("refused delete removed the run: %v", getErr)
		}
	} else if rr.Code != http.StatusNoContent || !errors.Is(getErr, store.ErrEvalRunNotFound) {
		t.Fatalf("eval delete = %d %s, run lookup error %v; want 204 and the run gone", rr.Code, rr.Body.String(), getErr)
	}
	adminBreakGlassSessionControl(t, s)
}

func adminBreakGlassGovernanceHealth(t *testing.T, st adminBreakGlassState, want string) {
	s, bus, _ := newTestGateway(t)
	enableTestAuth(s)
	adminBreakGlassForce(t, s, st)
	before := adminBreakGlassSnapshot(t, s, bus)
	rr := adminBreakGlassServe(t, s, adminCtx(httptest.NewRequest(http.MethodGet, adminBreakGlassHealthPath, nil)))
	if want != "" {
		adminBreakGlassRefused(t, rr, st, want)
		before.unchanged(t, s, bus)
	} else {
		var score governance.HealthScore
		if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &score) != nil || score.Grade == "" {
			t.Fatalf("governance health = %d %s, want 200 with a grade", rr.Code, rr.Body.String())
		}
	}
	adminBreakGlassSessionControl(t, s)
}

func adminBreakGlassResign(t *testing.T, st adminBreakGlassState, want string) {
	storeWriterEnv(t, true, "")
	storeWriterKey(t, "trusted")
	s, bus, _ := newTestGateway(t)
	storeWriterSeed(t, s, map[string]any{"custom/a": policyContent})
	adminBreakGlassForce(t, s, st)
	before := adminBreakGlassSnapshot(t, s, bus)
	if want != "" {
		rr := resignTestRequest(t, s, "custom/a", "admin", http.StatusServiceUnavailable)
		adminBreakGlassRefused(t, rr, st, want)
		before.unchanged(t, s, bus)
	} else {
		resignTestRequest(t, s, "custom/a", "admin", http.StatusOK)
		if err := storeWriterVerify(t, storeWriterBundle(t, s, "custom/a")); err != nil {
			t.Fatalf("re-signed bundle does not verify: %v", err)
		}
		entries, err := s.loadPolicyAudit(context.Background())
		if err != nil || len(entries) != 1 || entries[0].Action != "resign" || entries[0].ResourceID != "custom/a" {
			t.Fatalf("policy audit = %#v (error %v), want one resign entry for custom/a", entries, err)
		}
	}
	adminBreakGlassSessionControl(t, s)
}

// adminBreakGlassViewerHealth checks that the role gate answers before the license decision.
func adminBreakGlassViewerHealth(t *testing.T, st adminBreakGlassState) {
	s, bus, _ := newTestGateway(t)
	enableTestAuth(s)
	adminBreakGlassForce(t, s, st)
	before := adminBreakGlassSnapshot(t, s, bus)
	req := withAuth(httptest.NewRequest(http.MethodGet, adminBreakGlassHealthPath, nil), &auth.AuthContext{
		Role: "viewer", Tenant: "default", PrincipalID: "viewer@example.com",
	})
	if rr := adminBreakGlassServe(t, s, req); rr.Code != http.StatusForbidden {
		t.Fatalf("viewer governance health = %d %s, want 403", rr.Code, rr.Body.String())
	}
	before.unchanged(t, s, bus)
}

// adminBreakGlassViewerResign checks that the role gate answers before the license decision.
func adminBreakGlassViewerResign(t *testing.T, st adminBreakGlassState) {
	storeWriterEnv(t, true, "")
	storeWriterKey(t, "trusted")
	s, bus, _ := newTestGateway(t)
	storeWriterSeed(t, s, map[string]any{"custom/a": policyContent})
	adminBreakGlassForce(t, s, st)
	before := adminBreakGlassSnapshot(t, s, bus)
	resignTestRequest(t, s, "custom/a", "viewer", http.StatusForbidden)
	before.unchanged(t, s, bus)
}
