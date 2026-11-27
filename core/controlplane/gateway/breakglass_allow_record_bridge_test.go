package gateway

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/mcp"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	wf "github.com/cordum/cordum/core/workflow"
)

// TestBreakGlassAllowRecordBridge pins allow records to the MCP bridge answer.
// The log capture swaps the global logger, so these cases run serially.
func TestBreakGlassAllowRecordBridge(t *testing.T) {
	t.Run("job", breakGlassAllowRecordBridgeJobs)
	t.Run("runs", breakGlassAllowRecordBridgeRuns)
	t.Run("dispatch", breakGlassAllowRecordBridgeDispatch)
}

func breakGlassAllowRecordBridgeJobs(t *testing.T) {
	for _, state := range []string{"team_grace", "enterprise_grace"} {
		for _, tc := range []struct {
			name, tenant    string
			status, records int
		}{
			{"other_tenant", "other", http.StatusForbidden, 0},
			{"missing", "", http.StatusNotFound, 0},
			{"own_tenant", "default", http.StatusOK, 1},
		} {
			t.Run(state+"_"+tc.name, func(t *testing.T) {
				s, ctx, recorder := breakGlassAllowRecordBridgeSetup(t, state)
				job := &pb.JobRequest{JobId: "job-bridge", Topic: "job.test", TenantId: tc.tenant}
				if tc.tenant != "" {
					if err := s.jobStore.SetJobMeta(ctx, job); err != nil {
						t.Fatalf("set job meta: %v", err)
					}
					if err := s.jobStore.SetState(ctx, job.JobId, model.JobStatePending); err != nil {
						t.Fatalf("set job state: %v", err)
					}
				}
				payload, err := s.mcpGetJobPayload(ctx, job.JobId)
				if tc.status == http.StatusOK {
					if err != nil || payload["id"] != job.JobId {
						t.Fatalf("job payload = %v, error = %v", payload, err)
					}
				} else {
					var bridgeErr *mcp.BridgeError
					if !errors.As(err, &bridgeErr) || bridgeErr.StatusCode != tc.status {
						t.Fatalf("error = %v, want bridge status %d", err, tc.status)
					}
				}
				recorder.expect(t, tc.records, auth.PermJobsRead)
			})
		}
	}
}

func breakGlassAllowRecordBridgeRuns(t *testing.T) {
	for _, tc := range []struct {
		name, tenant    string
		status, records int
	}{
		{"other_tenant_workflow", "other", http.StatusForbidden, 0},
		{"missing_workflow", "", http.StatusNotFound, 0},
		{"own_workflow", "default", http.StatusOK, 1},
	} {
		t.Run("team_grace_"+tc.name, func(t *testing.T) {
			s, ctx, recorder := breakGlassAllowRecordBridgeSetup(t, "team_grace")
			const id = "workflow-bridge"
			if tc.tenant != "" {
				if err := s.workflowStore.SaveWorkflow(ctx, &wf.Workflow{ID: id, OrgID: tc.tenant}); err != nil {
					t.Fatalf("save workflow: %v", err)
				}
			}
			status, _, raw, err := s.invokeMCPAnyHandler(ctx, http.MethodGet,
				"/api/v1/workflows/"+id+"/runs", nil, map[string]string{"id": id}, nil, s.handleListRuns)
			if err != nil || status != tc.status {
				t.Fatalf("status = %d, error = %v, body = %s; want %d", status, err, raw, tc.status)
			}
			recorder.expect(t, tc.records, auth.PermWorkflowsRead)
		})
	}
}

func breakGlassAllowRecordBridgeDispatch(t *testing.T) {
	for _, name := range []string{"json_no_write", "any_no_write"} {
		t.Run(name, func(t *testing.T) {
			s, ctx, recorder := breakGlassAllowRecordBridgeSetup(t, "team_grace")
			handler := func(w http.ResponseWriter, r *http.Request) {
				if !s.requireLicensePermission(w, r, auth.PermJobsRead) {
					t.Fatal("grace refused jobs.read")
				}
			}
			var status int
			var raw []byte
			var err error
			if name == "json_no_write" {
				status, _, raw, err = s.invokeMCPJSONHandler(ctx, http.MethodGet, "/test", nil, nil, nil, handler)
			} else {
				status, _, raw, err = s.invokeMCPAnyHandler(ctx, http.MethodGet, "/test", nil, nil, nil, handler)
			}
			if err != nil || status != http.StatusOK || len(raw) != 0 {
				t.Fatalf("status = %d, error = %v, body = %s; want 200 and no body", status, err, raw)
			}
			recorder.expect(t, 1, auth.PermJobsRead)
		})
	}
}

func breakGlassAllowRecordBridgeSetup(t *testing.T, state string) (*server, context.Context, *breakGlassAllowRecorder) {
	t.Helper()
	s, _, _ := newTestGateway(t)
	s.tenant = "default"
	enableTestAuth(s)
	recorder := breakGlassAllowRecordWatch(t, s, breakGlassAllowRecordForce(t, s, state))
	ctx := context.WithValue(context.Background(), auth.ContextKey{}, &auth.AuthContext{
		Tenant: "default", Role: "viewer", PrincipalID: "viewer-1",
	})
	return s, ctx, recorder
}
