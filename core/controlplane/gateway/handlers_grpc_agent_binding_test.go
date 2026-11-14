package gateway

import (
	"context"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestSubmitJobGRPCBindsClientAgentID pins gRPC SubmitJob to the HTTP rule:
// a client labels.agent_id must equal the agent linked to the authenticated
// principal in the request tenant, or the submit is refused before any
// policy check. The request's own principal_id never makes a label valid,
// and with the label omitted it never yields an inferred agent.
func TestSubmitJobGRPCBindsClientAgentID(t *testing.T) {
	cases := []struct {
		name       string
		caller     string // "" = authenticated as "p"; "anonymous" = no auth context; "keyed" = auth context with no principal
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
		{name: "anonymous_request_principal_label", caller: "anonymous", linkTenant: "default", label: "agent-source", wantCode: codes.PermissionDenied},
		{name: "keyed_request_principal_label", caller: "keyed", linkTenant: "default", label: "agent-source", wantCode: codes.PermissionDenied},
		{name: "anonymous_request_principal_no_label", caller: "anonymous", linkTenant: "default", wantCode: codes.OK},
		{name: "keyed_request_principal_no_label", caller: "keyed", linkTenant: "default", wantCode: codes.OK},
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
			_, err := s.SubmitJob(grpcAgentBindingCaller(tc.caller), req)
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

// TestSubmitJobGRPCRejectsReservedGovernanceLabels pins gRPC SubmitJob to the
// HTTP rule: a client _governance.* or _ma.* label is refused with
// InvalidArgument before any policy check, job write or publish, and an
// ordinary label still submits.
func TestSubmitJobGRPCRejectsReservedGovernanceLabels(t *testing.T) {
	cases := []struct {
		name     string
		label    string
		wantCode codes.Code
	}{
		{name: "governance_label", label: "_governance.tenant", wantCode: codes.InvalidArgument},
		{name: "multi_agent_label", label: "_ma.parent_agent_id", wantCode: codes.InvalidArgument},
		{name: "ordinary_label", label: "team", wantCode: codes.OK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, bus, safetyClient := newTestGateway(t)
			s.tenant = "default"
			req := &pb.SubmitJobRequest{Prompt: "hello", Topic: "job.default", OrgId: "default", PrincipalId: "p",
				Labels: map[string]string{tc.label: "x"}}
			_, err := s.SubmitJob(grpcAgentBindingCaller(""), req)
			if got := status.Code(err); got != tc.wantCode {
				t.Fatalf("SubmitJob code = %v (err %v), want %v", got, err, tc.wantCode)
			}
			jobs, err := s.jobStore.ListRecentJobs(context.Background(), 10)
			if err != nil {
				t.Fatalf("ListRecentJobs: %v", err)
			}
			reached := safetyClient.lastReq != nil || len(bus.published) != 0 || len(jobs) != 0
			if reached != (tc.wantCode == codes.OK) {
				t.Fatalf("submit reached policy=%v publishes=%d jobs=%d", safetyClient.lastReq != nil, len(bus.published), len(jobs))
			}
		})
	}
}

func grpcAgentBindingCaller(caller string) context.Context {
	switch caller {
	case "anonymous":
		return context.Background()
	case "keyed":
		return context.WithValue(context.Background(), auth.ContextKey{}, &auth.AuthContext{Tenant: "default", Role: "admin"})
	default:
		return context.WithValue(context.Background(), auth.ContextKey{}, &auth.AuthContext{Tenant: "default", PrincipalID: "p", Role: "admin"})
	}
}
