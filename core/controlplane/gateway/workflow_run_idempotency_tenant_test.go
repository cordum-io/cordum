package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	wf "github.com/cordum/cordum/core/workflow"
)

func TestWorkflowRunIdempotencyTenantBound(t *testing.T) {
	for _, name := range []string{"start-route-key", "old-format-key"} {
		t.Run(name, func(t *testing.T) {
			s := wfRunIdemTenantGateway(t)
			const key = "tenant-run-key"
			var runB string
			if name == "old-format-key" {
				runB = wfRunIdemTenantOldKey(t, s, key)
			} else {
				runB, _ = wfRunIdemTenantStart(t, s, "tenant-b", "wf-B", key)
			}
			beforeB := wfRunIdemTenantSnapshot(t, s, runB)
			runA, bodyA := wfRunIdemTenantStart(t, s, "tenant-a", "wf-A", key)
			if strings.Contains(bodyA, runB) {
				t.Fatal("tenant-a response contains tenant-b run id")
			}
			gotA, err := s.workflowStore.GetRun(context.Background(), runA)
			if err != nil || gotA == nil || gotA.OrgID != "tenant-a" || gotA.WorkflowID != "wf-A" {
				t.Fatalf("tenant-a run = %+v, %v; want tenant-a on wf-A", gotA, err)
			}
			if again, _ := wfRunIdemTenantStart(t, s, "tenant-b", "wf-B", key); again != runB {
				t.Fatal("tenant-b retry must return its own run")
			}
			if again, body := wfRunIdemTenantStart(t, s, "tenant-a", "wf-A", key); again != runA || strings.Contains(body, runB) {
				t.Fatal("tenant-a retry must return only its own run")
			}
			if !bytes.Equal(beforeB, wfRunIdemTenantSnapshot(t, s, runB)) {
				t.Fatal("tenant-b run must remain unchanged")
			}
			if name == "old-format-key" {
				got, err := s.jobStore.Client().Get(context.Background(), "wf:run:idempotency:"+key).Result()
				if err != nil || got != runB {
					t.Fatalf("old-format key = %q, %v; want tenant-b run", got, err)
				}
			}
		})
	}
}

func wfRunIdemTenantGateway(t *testing.T) *server {
	t.Helper()
	s, _, _ := newTestGateway(t)
	for tenant, id := range map[string]string{"tenant-a": "wf-A", "tenant-b": "wf-B"} {
		def := &wf.Workflow{ID: id, OrgID: tenant, Steps: map[string]*wf.Step{
			"step": {ID: "step", Type: wf.StepTypeWorker, Topic: "job.test"},
		}}
		if err := s.workflowStore.SaveWorkflow(context.Background(), def); err != nil {
			t.Fatalf("save workflow: %v", err)
		}
	}
	return s
}

func wfRunIdemTenantStart(t *testing.T, s *server, tenant, workflowID, key string) (string, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/"+workflowID+"/runs", strings.NewReader(`{}`))
	req.Header.Set("Idempotency-Key", key)
	req.SetPathValue("id", workflowID)
	req = withAuth(req, &auth.AuthContext{Tenant: tenant, PrincipalID: "user-" + tenant, Role: "admin"})
	rec := httptest.NewRecorder()
	s.handleStartRun(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s start status = %d; want 200", tenant, rec.Code)
	}
	var response struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || response.RunID == "" {
		t.Fatalf("%s start response must contain a run id: %v", tenant, err)
	}
	return response.RunID, rec.Body.String()
}

func wfRunIdemTenantSnapshot(t *testing.T, s *server, runID string) []byte {
	t.Helper()
	run, err := s.workflowStore.GetRun(context.Background(), runID)
	if err != nil || run == nil {
		t.Fatalf("get run: %v", err)
	}
	raw, err := json.Marshal(run)
	if err != nil {
		t.Fatalf("encode run: %v", err)
	}
	return raw
}

func wfRunIdemTenantOldKey(t *testing.T, s *server, key string) string {
	t.Helper()
	ctx := context.Background()
	run := &wf.WorkflowRun{ID: "run-tenant-b", WorkflowID: "wf-B", OrgID: "tenant-b", Status: wf.RunStatusPending, Steps: map[string]*wf.StepRun{}}
	if err := s.workflowStore.CreateRun(ctx, run); err != nil {
		t.Fatalf("create tenant-b run: %v", err)
	}
	if err := s.jobStore.Client().Set(ctx, "wf:run:idempotency:"+key, run.ID, 0).Err(); err != nil {
		t.Fatalf("set old-format key: %v", err)
	}
	return run.ID
}
