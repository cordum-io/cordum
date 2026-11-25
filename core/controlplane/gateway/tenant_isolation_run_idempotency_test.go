package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	wf "github.com/cordum/cordum/core/workflow"
)

func TestTenantIsolationWorkflowRunIdempotencyKey(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()
	for tenant, wfID := range map[string]string{"tenant-a": "wf-iso-a", "tenant-b": "wf-iso-b"} {
		def := &wf.Workflow{ID: wfID, OrgID: tenant, Steps: map[string]*wf.Step{
			"step": {ID: "step", Type: wf.StepTypeWorker, Topic: "job.test"},
		}}
		if err := s.workflowStore.SaveWorkflow(ctx, def); err != nil {
			t.Fatalf("save %s: %v", wfID, err)
		}
	}
	start := func(tenant, wfID string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/"+wfID+"/runs", strings.NewReader(`{}`))
		req.Header.Set("Idempotency-Key", "shared-run-key")
		req.SetPathValue("id", wfID)
		req = withAuth(req, &auth.AuthContext{Tenant: tenant, PrincipalID: "user-" + tenant, Role: "admin"})
		rec := httptest.NewRecorder()
		s.handleStartRun(rec, req)
		var resp map[string]string
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &resp) != nil || resp["run_id"] == "" {
			t.Fatalf("%s start %s = %d %s; want 200 with a run_id", tenant, wfID, rec.Code, rec.Body.String())
		}
		return resp["run_id"]
	}
	snapshot := func(runID string) []byte {
		t.Helper()
		run, err := s.workflowStore.GetRun(ctx, runID)
		if err != nil || run == nil {
			t.Fatalf("get run %s: %v", runID, err)
		}
		raw, _ := json.Marshal(run)
		return raw
	}

	runA := start("tenant-a", "wf-iso-a")
	beforeA := snapshot(runA)

	// Tenant B reuses tenant A's Idempotency-Key on its own workflow.
	runB := start("tenant-b", "wf-iso-b")
	if runB == runA {
		t.Fatalf("tenant-b received tenant-a's run id %s", runA)
	}
	gotB, err := s.workflowStore.GetRun(ctx, runB)
	if err != nil || gotB == nil || gotB.OrgID != "tenant-b" || gotB.WorkflowID != "wf-iso-b" {
		t.Fatalf("tenant-b run %s = %+v, %v; want its own run", runB, gotB, err)
	}
	if after := snapshot(runA); string(after) != string(beforeA) {
		t.Fatalf("tenant-a run changed:\nbefore %s\nafter  %s", beforeA, after)
	}

	// Each tenant's retry still returns its own run.
	if again := start("tenant-b", "wf-iso-b"); again != runB {
		t.Fatalf("tenant-b retry = %s; want %s", again, runB)
	}
	if again := start("tenant-a", "wf-iso-a"); again != runA {
		t.Fatalf("tenant-a retry = %s; want %s", again, runA)
	}
}
