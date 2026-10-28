package gateway

// The workflow context (workflow_id, workflow.*, run_id, step_id) links a job
// to a workflow run, and the Safety Kernel scopes policy by it. Only the
// workflow engine sets it: job submit drops it from client labels, a step's
// route labels cannot override it, and approvals show it only for jobs the
// engine dispatched (WorkflowId set).

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/infra/resourceio"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	"github.com/cordum/cordum/core/policylabels"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	wf "github.com/cordum/cordum/core/workflow"
	"github.com/stretchr/testify/require"
)

// claimedWorkflowContext passes a job off as the approval step of another
// workflow's run, under every label that carries the workflow context.
func claimedWorkflowContext() map[string]string {
	return map[string]string{
		"workflow_id": "wf-victim", "workflow.id": "wf-victim", "workflow": "wf-victim",
		"workflow_run_id": "run-victim", "workflow.run_id": "run-victim",
		"run_id": "run-victim", "step_id": "approve", "team": "alpha",
	}
}

func assertNoWorkflowContext(t *testing.T, where string, labels map[string]string) {
	t.Helper()
	for k := range labels {
		if policylabels.WorkflowContextLabel(k) {
			t.Fatalf("%s carries client-supplied workflow context %q: %v", where, k, labels)
		}
	}
	if labels["team"] != "alpha" {
		t.Fatalf("%s lost the client's own label: %v", where, labels)
	}
}

func TestSubmitDropsClaimedWorkflowContext(t *testing.T) {
	for transport, submit := range map[string]submitFunc{"http": submitAttemptHTTP, "grpc": submitAttemptGRPC} {
		t.Run(transport, func(t *testing.T) {
			s, _, safety := newTestGateway(t)
			s.tenant = "default"
			jobID, refused := submit(t, s, submitAttempt{principal: "mallory", labels: claimedWorkflowContext()})
			if refused != "" {
				t.Fatalf("submit refused: %s", refused)
			}
			stored, err := s.jobStore.GetJobRequest(context.Background(), jobID)
			if err != nil {
				t.Fatalf("stored request: %v", err)
			}
			if stored.GetWorkflowId() != "" {
				t.Fatalf("client job linked to workflow %q", stored.GetWorkflowId())
			}
			assertNoWorkflowContext(t, "stored request", stored.GetLabels())
			safety.mu.Lock()
			checked := safety.lastReq
			safety.mu.Unlock()
			if checked == nil {
				t.Fatal("no submit-time policy check")
			}
			assertNoWorkflowContext(t, "submit-time policy check", checked.GetLabels())
		})
	}
}

// The dry-run checks each step with the labels its job carries in a real run.
func TestWorkflowDryRunKeepsEngineWorkflowContext(t *testing.T) {
	s, _, safety := newTestGateway(t)
	route := claimedWorkflowContext()
	route["_source"] = "api"
	wfDef := &wf.Workflow{ID: "wf-dryrun-context", OrgID: "default", Steps: map[string]*wf.Step{
		"deploy": {ID: "deploy", Type: wf.StepTypeWorker, Topic: "job.deploy", RouteLabels: route},
	}}
	require.NoError(t, s.workflowStore.SaveWorkflow(context.Background(), wfDef))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/"+wfDef.ID+"/dry-run", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("X-Tenant-ID", "default")
	req.SetPathValue("id", wfDef.ID)
	rec := httptest.NewRecorder()
	s.handleWorkflowDryRun(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	safety.mu.Lock()
	labels := safety.lastReq.GetLabels()
	safety.mu.Unlock()
	for k, want := range map[string]string{"workflow_id": wfDef.ID, "step_id": "deploy", "_source": "workflow", "team": "alpha"} {
		require.Equal(t, want, labels[k], "label %s in %v", k, labels)
	}
	for _, k := range []string{"workflow.id", "workflow", "workflow_run_id", "workflow.run_id", "run_id"} {
		require.NotContains(t, labels, k)
	}
}

// A job the engine did not dispatch has no workflow linkage, whatever its
// labels or input payload claim (here as stored by a submit that kept them):
// approvals neither show the claimed run's context nor hide the job because
// that run ended.
func TestApprovalsIgnoreUnlinkedWorkflowClaims(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.memoryResourceReader.Compatibility = resourceio.LegacyCompatibility{Enabled: true}
	ctx := context.Background()
	now := time.Now().UTC()
	require.NoError(t, s.workflowStore.CreateRun(ctx, &wf.WorkflowRun{
		ID: "run-victim", WorkflowID: "wf-victim", Status: wf.RunStatusSucceeded,
		Steps: map[string]*wf.StepRun{}, CreatedAt: now, UpdatedAt: now,
	}))

	jobID := "job-claims-workflow"
	ctxKey := store.MakeContextKey(jobID)
	ctxPtr := store.PointerForKey(ctxKey)
	require.NoError(t, s.memStore.PutContext(ctx, ctxKey, []byte(`{"kind":"workflow_approval_context","version":1,"workflow":{"workflow_id":"wf-victim","run_id":"run-victim","step_id":"approve","workflow_name":"Payroll","step_name":"Manager Approval"},"decision":{"amount":50000,"currency":"USD","vendor":"Mallory Ltd"}}`)))
	req := &pb.JobRequest{JobId: jobID, Topic: "job.default", TenantId: "default", ContextPtr: ctxPtr, Labels: claimedWorkflowContext()}
	require.NoError(t, s.jobStore.SetJobMeta(ctx, req))
	require.NoError(t, s.jobStore.SetJobRequest(ctx, req))
	require.NoError(t, s.jobStore.SetState(ctx, jobID, model.JobStateApproval))
	require.NoError(t, s.jobStore.SetTenant(ctx, jobID, "default"))
	require.NoError(t, s.jobStore.SetSafetyDecision(ctx, jobID, model.SafetyDecisionRecord{
		Decision: model.SafetyRequireApproval, ApprovalRequired: true, JobHash: "hash-" + jobID,
	}))

	rr := httptest.NewRecorder()
	s.handleListApprovals(rr, httptest.NewRequest(http.MethodGet, "/api/v1/approvals?include_resolved=false", nil))
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var resp struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 1, "the job must be listed, not filtered on the claimed run")
	item := resp.Items[0]
	for _, k := range []string{"workflow_id", "workflow_run_id", "workflow_step_id", "workflow_name", "step_name"} {
		require.NotContains(t, item, k)
	}
	summary, ok := item["decision_summary"].(map[string]any)
	require.True(t, ok, "expected decision_summary")
	for k, v := range summary {
		if text, ok := v.(string); ok && strings.Contains(text, "Manager Approval") {
			t.Fatalf("decision summary %s shows the claimed step: %q", k, text)
		}
	}
}
