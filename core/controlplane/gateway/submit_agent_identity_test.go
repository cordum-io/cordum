package gateway

// Job submit binds agent_id to the caller's credential over HTTP and gRPC
// alike: a claimed agent_id must match the agent linked to the credential's
// own principal, and naming another worker as principal_id never lends the
// job that worker's agent.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/store"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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

func TestSubmitBindsAgentIDToCredential(t *testing.T) {
	for transport, submit := range map[string]submitFunc{"http": submitAttemptHTTP, "grpc": submitAttemptGRPC} {
		t.Run(transport, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			s.tenant = "default"
			ctx := context.Background()
			agents := map[string]string{}
			for _, worker := range []string{"worker-a", "worker-b"} {
				agent, err := s.agentIdentityStore.Create(ctx, store.AgentIdentity{
					TenantID: "default", Name: worker + "-agent", Owner: "admin", RiskTier: "low", Status: "active",
				})
				if err != nil {
					t.Fatalf("create agent: %v", err)
				}
				if err := s.agentIdentityStore.LinkWorker(ctx, agent.ID, worker); err != nil {
					t.Fatalf("link worker: %v", err)
				}
				agents[worker] = agent.ID
			}

			for _, tc := range []struct {
				name      string
				attempt   submitAttempt
				refused   string
				wantAgent string
			}{
				{"linked credential gets its agent", submitAttempt{principal: "worker-a"}, "", agents["worker-a"]},
				{"own agent may be claimed", submitAttempt{principal: "worker-a", labels: map[string]string{"agent_id": agents["worker-a"]}}, "", agents["worker-a"]},
				{"agent claimed without a linked credential", submitAttempt{principal: "mallory", labels: map[string]string{"agent_id": agents["worker-b"]}}, "denied", ""},
				{"another worker's agent claimed", submitAttempt{principal: "worker-a", labels: map[string]string{"agent_id": agents["worker-b"]}}, "denied", ""},
				{"another worker named as principal", submitAttempt{principal: "mallory", requested: "worker-b"}, "", ""},
				{"governance label", submitAttempt{principal: "worker-a", labels: map[string]string{"_governance.tenant": "other"}}, "invalid", ""},
			} {
				t.Run(tc.name, func(t *testing.T) {
					jobID, refused := submit(t, s, tc.attempt)
					if refused != tc.refused {
						t.Fatalf("refused = %q, want %q", refused, tc.refused)
					}
					if refused != "" {
						return
					}
					req, err := s.jobStore.GetJobRequest(ctx, jobID)
					if err != nil {
						t.Fatalf("stored request: %v", err)
					}
					if got := req.GetLabels()["agent_id"]; got != tc.wantAgent {
						t.Fatalf("agent_id = %q, want %q", got, tc.wantAgent)
					}
				})
			}
		})
	}
}
