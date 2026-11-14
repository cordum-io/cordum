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
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/policylabels"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	wf "github.com/cordum/cordum/core/workflow"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

type submitAttempt struct {
	principal string // the credential's principal
	requested string // principal_id the caller asks for
	labels    map[string]string
}

// submitFunc returns the job ID, or why the submit was refused ("denied", "invalid").
type submitFunc func(t *testing.T, s *server, a submitAttempt) (jobID, refused string)

func submitAttemptHTTP(t *testing.T, s *server, a submitAttempt) (string, string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"prompt": "hello", "topic": "job.default", "principal_id": a.requested, "labels": a.labels,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "default")
	req = withAuth(req, &auth.AuthContext{Tenant: "default", Role: "user", PrincipalID: a.principal})
	rec := httptest.NewRecorder()
	s.handleSubmitJobHTTP(rec, req)
	switch rec.Code {
	case http.StatusForbidden:
		return "", "denied"
	case http.StatusBadRequest:
		return "", "invalid"
	case http.StatusOK:
	default:
		t.Fatalf("submit status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.JobID == "" {
		t.Fatalf("submit response %s: %v", rec.Body.String(), err)
	}
	return resp.JobID, ""
}

func submitAttemptGRPC(t *testing.T, s *server, a submitAttempt) (string, string) {
	t.Helper()
	ctx := context.WithValue(context.Background(), auth.ContextKey{}, &auth.AuthContext{
		Tenant: "default", Role: "user", PrincipalID: a.principal,
	})
	resp, err := s.SubmitJob(ctx, &pb.SubmitJobRequest{
		Prompt: "hello", Topic: "job.default", OrgId: "default", PrincipalId: a.requested, Labels: a.labels,
	})
	switch status.Code(err) {
	case codes.OK:
		return resp.GetJobId(), ""
	case codes.PermissionDenied:
		return "", "denied"
	case codes.InvalidArgument:
		return "", "invalid"
	}
	t.Fatalf("grpc submit: %v", err)
	return "", ""
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
