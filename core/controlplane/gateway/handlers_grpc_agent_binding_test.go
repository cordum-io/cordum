package gateway

import (
	"context"
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestSubmitJobGRPCBindsClientAgentID pins gRPC SubmitJob to the HTTP rule:
// a client labels.agent_id must equal the agent linked to the principal in
// the request tenant, or the submit is refused before any policy check.
func TestSubmitJobGRPCBindsClientAgentID(t *testing.T) {
	cases := []struct {
		name       string
		linkTenant string // tenant of agent-source, linked to "p"; "" = no link
		label      string
		wantCode   codes.Code
		wantAgent  string
	}{
		{name: "unlinked_client_label", label: "agent-other", wantCode: codes.PermissionDenied},
		{name: "foreign_link_client_label", linkTenant: "tenant-b", label: "agent-source", wantCode: codes.PermissionDenied},
		{name: "own_link_other_label", linkTenant: "default", label: "agent-other", wantCode: codes.PermissionDenied},
		{name: "own_link_matching_label", linkTenant: "default", label: "agent-source", wantCode: codes.OK, wantAgent: "agent-source"},
		{name: "own_link_no_label", linkTenant: "default", wantCode: codes.OK, wantAgent: "agent-source"},
		{name: "unlinked_no_label", wantCode: codes.OK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, bus, safetyClient := newTestGateway(t)
			s.tenant = "default"
			createDelegationAgent(t, s, "default", "agent-other", []string{"read"}, []string{"job.default"})
			if tc.linkTenant != "" {
				createDelegationAgent(t, s, tc.linkTenant, "agent-source", []string{"read"}, []string{"job.default"})
				if err := s.agentIdentityStore.LinkWorker(context.Background(), "agent-source", "p"); err != nil {
					t.Fatalf("link worker: %v", err)
				}
			}
			req := &pb.SubmitJobRequest{Prompt: "hello", Topic: "job.default", OrgId: "default", PrincipalId: "p"}
			if tc.label != "" {
				req.Labels = map[string]string{"agent_id": tc.label}
			}
			_, err := s.SubmitJob(context.Background(), req)
			if got := status.Code(err); got != tc.wantCode {
				t.Fatalf("SubmitJob code = %v (err %v), want %v", got, err, tc.wantCode)
			}
			if tc.wantCode != codes.OK {
				if safetyClient.lastReq != nil || len(bus.published) != 0 {
					t.Fatalf("refused submit reached policy=%v publishes=%d", safetyClient.lastReq != nil, len(bus.published))
				}
				return
			}
			if safetyClient.lastReq == nil {
				t.Fatal("expected policy check")
			}
			if got := safetyClient.lastReq.GetLabels()["agent_id"]; got != tc.wantAgent {
				t.Fatalf("policy agent_id = %q, want %q", got, tc.wantAgent)
			}
		})
	}
}
