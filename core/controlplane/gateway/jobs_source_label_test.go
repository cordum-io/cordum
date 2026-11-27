package gateway

// _source is the provenance label that policy rules match: "api", "pack" or
// "workflow". pack_id is a field the caller sets, so it alone does not make a
// job a pack job. Only a job that a worker credential submits is "pack".

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestSubmitSourceLabelFollowsCredentialNotPackID(t *testing.T) {
	for _, tc := range []struct {
		name   string
		linked bool
		packID string
		want   string
	}{
		{name: "direct caller with pack_id", packID: "some-pack", want: "api"},
		{name: "direct caller", want: "api"},
		{name: "worker credential with pack_id", linked: true, packID: "some-pack", want: "pack"},
		{name: "worker credential without pack_id", linked: true, want: "api"},
	} {
		for _, transport := range []string{"http", "grpc"} {
			t.Run(transport+"/"+tc.name, func(t *testing.T) {
				s, _, safety := newTestGateway(t)
				s.tenant = "default"
				// Resolve the job principal from the credential, as in production.
				s.auth = &auth.BasicAuthProvider{}
				if tc.linked {
					createDelegationAgent(t, s, "default", "agent-pack", []string{"read"}, []string{"job.default"})
					if err := s.agentIdentityStore.LinkWorker(context.Background(), "agent-pack", "p"); err != nil {
						t.Fatalf("link worker: %v", err)
					}
				}

				if transport == "http" {
					body, err := json.Marshal(map[string]any{"prompt": "hello", "topic": "job.default", "pack_id": tc.packID})
					if err != nil {
						t.Fatalf("marshal: %v", err)
					}
					req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("X-Tenant-ID", "default")
					req = withAuth(req, &auth.AuthContext{Tenant: "default", Role: "operator", PrincipalID: "p"})
					rec := httptest.NewRecorder()
					s.handleSubmitJobHTTP(rec, req)
					if rec.Code != http.StatusOK {
						t.Fatalf("submit status=%d body=%s", rec.Code, rec.Body.String())
					}
				} else if _, err := s.SubmitJob(grpcAgentBindingCaller(""), &pb.SubmitJobRequest{
					Prompt: "hello", Topic: "job.default", OrgId: "default", PrincipalId: "p", PackId: tc.packID,
				}); err != nil {
					t.Fatalf("grpc submit: %v", err)
				}

				safety.mu.Lock()
				last := safety.lastReq
				safety.mu.Unlock()
				if got := last.GetLabels()["_source"]; got != tc.want {
					t.Errorf("labels _source = %q, want %q", got, tc.want)
				}
				if got := last.GetMeta().GetLabels()["_source"]; got != tc.want {
					t.Errorf("meta.labels _source = %q, want %q", got, tc.want)
				}
			})
		}
	}
}
