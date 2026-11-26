package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/store"
	wf "github.com/cordum/cordum/core/workflow"
	"github.com/redis/go-redis/v9"
)

// runCancelReadFault fails exactly one GET, leaving the engine's reads available.
type runCancelReadFault struct {
	key   string
	fired atomic.Bool
}

func (f *runCancelReadFault) DialHook(next redis.DialHook) redis.DialHook { return next }

func (f *runCancelReadFault) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func (f *runCancelReadFault) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if slices.Equal(cmdWords(cmd), []string{"get", f.key}) && f.fired.CompareAndSwap(false, true) {
			cmd.SetErr(errInjectedRead)
			return errInjectedRead
		}
		return next(ctx, cmd)
	}
}

func TestTenantIsolationGatewayRunCancel(t *testing.T) {
	for _, tc := range []runCancelCase{
		{name: "lookup-error", org: "tenant-b", code: http.StatusServiceUnavailable},
		{name: "empty-org", code: http.StatusNotFound},
		{name: "missing-run", code: http.StatusNotFound},
		{name: "cross-tenant", code: http.StatusNoContent},
		{name: "same-tenant", org: "tenant-b", code: http.StatusNoContent},
		{name: "other-tenant", org: "tenant-b", code: http.StatusForbidden},
		{name: "no-auth", code: http.StatusNoContent},
		{name: "nil-store", org: "tenant-b", code: http.StatusNotFound},
		{name: "whitespace-org", org: " \t", code: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) { testRunCancel(t, tc) })
	}
}

type runCancelCase struct {
	name string
	org  string
	code int
}

func testRunCancel(t *testing.T, tc runCancelCase) {
	t.Helper()
	s, bus, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	runs := s.workflowStore
	s.workflowEng = wf.NewEngine(runs, bus)
	ctx := context.Background()
	run := &wf.WorkflowRun{ID: "run-cancel", WorkflowID: "wf-cancel", OrgID: tc.org,
		Status: wf.RunStatusRunning, Steps: map[string]*wf.StepRun{}}
	mustNoErr(t, "save workflow", runs.SaveWorkflow(ctx, &wf.Workflow{ID: run.WorkflowID, OrgID: tc.org}))
	if tc.name != "missing-run" {
		mustNoErr(t, "create run", runs.CreateRun(ctx, run))
	}
	fault := &runCancelReadFault{key: "wf:run:" + run.ID}
	if tc.name == "lookup-error" {
		s.memStore.(*store.RedisStore).Client().AddHook(fault)
	}
	r := tenantObjectRequest(http.MethodPost, "/api/v1/workflows/wf-cancel/runs/"+run.ID+"/cancel", "tenant-a", run.WorkflowID)
	r.SetPathValue("run_id", run.ID)
	switch tc.name {
	case "cross-tenant":
		auth.FromRequest(r).AllowCrossTenant = true
	case "same-tenant":
		auth.FromRequest(r).Tenant = tc.org
		r.Header.Set("X-Tenant-ID", tc.org)
	case "no-auth":
		s.auth = nil
	case "nil-store":
		s.workflowStore = nil
	}
	rr := httptest.NewRecorder()
	s.handleCancelRun(rr, r)
	if rr.Code != tc.code {
		t.Errorf("status: %d %s, want %d", rr.Code, rr.Body.String(), tc.code)
	}
	if tc.name == "lookup-error" && !fault.fired.Load() {
		t.Error("fault did not fire")
	}
	assertRunCancelEffects(t, s, runs, tc)
}

func assertRunCancelEffects(t *testing.T, s *server, runs *wf.RedisStore, tc runCancelCase) {
	t.Helper()
	got, err := runs.GetRun(context.Background(), "run-cancel")
	if tc.name == "missing-run" {
		if !errors.Is(err, redis.Nil) {
			t.Errorf("effect: missing run lookup %v, want redis.Nil", err)
		}
	} else {
		want := wf.RunStatusRunning
		if tc.code == http.StatusNoContent {
			want = wf.RunStatusCancelled
		}
		if err != nil || got == nil || got.Status != want {
			t.Errorf("effect: run state %v, %v, want %s", got, err, want)
		}
	}
	wantAudit := 0
	if tc.code == http.StatusNoContent {
		wantAudit = 1
	}
	if n := approvalAuditCount(t, s, "run-cancel"); n != wantAudit {
		t.Errorf("effect: %d audit entries, want %d", n, wantAudit)
	}
}
