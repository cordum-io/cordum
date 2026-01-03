package safetykernel

import (
	"context"
	"testing"

	"github.com/yaront1111/coretex-os/core/infra/config"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
)

func TestCheckMCPPolicyDenies(t *testing.T) {
	srv := &server{policy: &config.SafetyPolicy{
		DefaultTenant: "default",
		Tenants: map[string]config.TenantPolicy{
			"default": {
				AllowTopics: []string{"job.*"},
				MCP: config.MCPPolicy{
					DenyServers: []string{"blocked.example.com"},
				},
			},
		},
	}}

	req := &pb.PolicyCheckRequest{
		JobId:  "job-1",
		Topic:  "job.default",
		Tenant: "default",
		Labels: map[string]string{
			"mcp.server": "blocked.example.com",
			"mcp.tool":   "read",
		},
	}

	resp, err := srv.Check(context.Background(), req)
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("expected deny, got %v", resp.GetDecision())
	}
}

func TestCheckMCPPolicyRequiresFieldWhenAllowlistSet(t *testing.T) {
	srv := &server{policy: &config.SafetyPolicy{
		DefaultTenant: "default",
		Tenants: map[string]config.TenantPolicy{
			"default": {
				AllowTopics: []string{"job.*"},
				MCP: config.MCPPolicy{
					AllowServers: []string{"github.com"},
				},
			},
		},
	}}

	req := &pb.PolicyCheckRequest{
		JobId:  "job-2",
		Topic:  "job.default",
		Tenant: "default",
		Labels: map[string]string{
			"mcp.tool": "read",
		},
	}

	resp, err := srv.Check(context.Background(), req)
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("expected deny when mcp.server missing, got %v", resp.GetDecision())
	}
}
