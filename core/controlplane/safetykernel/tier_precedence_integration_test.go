//go:build integration

package safetykernel

import (
	"context"
	"testing"

	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/policylabels"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestEDGE053_TierPrecedenceIntegration(t *testing.T) {
	srv := &server{scanners: map[string]OutputScanner{}}
	policy := tierPrecedencePolicy()
	invariants := tierPrecedenceInvariants()
	const snapshot = "cfg:edge053-tier-precedence"

	if err := srv.setPolicyWithInvariants(context.Background(), policy, invariants, snapshot, 0); err != nil {
		t.Fatalf("setPolicyWithInvariants: %v", err)
	}

	cases := []struct {
		name       string
		labels     map[string]string
		want       pb.DecisionType
		wantRuleID string
	}{
		{
			name:       "GlobalOnlyAllowsRead",
			labels:     map[string]string{"operation": "read"},
			want:       pb.DecisionType_DECISION_TYPE_ALLOW,
			wantRuleID: "global-allow-read",
		},
		{
			name:       "WorkflowOverridesGlobal",
			labels:     map[string]string{"workflow_id": "deploy-prod", "operation": "read"},
			want:       pb.DecisionType_DECISION_TYPE_DENY,
			wantRuleID: "workflow-deny-read",
		},
		{
			name: "JobOverridesWorkflow",
			labels: map[string]string{
				"workflow_id":                   "deploy-prod",
				policylabels.PolicyAttachmentID: "job/abc/policy",
				"operation":                     "read",
			},
			want:       pb.DecisionType_DECISION_TYPE_ALLOW,
			wantRuleID: "job-allow-read",
		},
		{
			name: "InvariantDenyUncrossable",
			labels: map[string]string{
				"workflow_id":                   "deploy-prod",
				policylabels.PolicyAttachmentID: "job/abc/policy",
				"operation":                     "read",
				"path":                          ".env",
			},
			want:       pb.DecisionType_DECISION_TYPE_DENY,
			wantRuleID: "inv-deny-env-read",
		},
		{
			name: "EdgeActionJobOverridesWorkflow",
			labels: map[string]string{
				"workflow_id":                   "deploy-prod",
				policylabels.PolicyAttachmentID: "job/abc/policy",
				"operation":                     "read",
				"edge_case":                     "true",
			},
			want:       pb.DecisionType_DECISION_TYPE_ALLOW,
			wantRuleID: "job-allow-edge-read",
		},
		{
			name:       "NoMatchUsesWorkflowDefault",
			labels:     map[string]string{"workflow_id": "deploy-prod", "operation": "write"},
			want:       pb.DecisionType_DECISION_TYPE_DENY,
			wantRuleID: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := srv.Evaluate(context.Background(), &pb.PolicyCheckRequest{
				JobId:  "job-edge053",
				Topic:  tierPrecedenceTopic(tc.labels),
				Tenant: "default",
				Labels: tc.labels,
			})
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if resp.GetDecision() != tc.want {
				t.Fatalf("decision = %v, want %v (rule=%q reason=%q)",
					resp.GetDecision(), tc.want, resp.GetRuleId(), resp.GetReason())
			}
			if resp.GetRuleId() != tc.wantRuleID {
				t.Fatalf("rule_id = %q, want %q", resp.GetRuleId(), tc.wantRuleID)
			}
			if resp.GetPolicySnapshot() != snapshot {
				t.Fatalf("policy_snapshot = %q, want %q", resp.GetPolicySnapshot(), snapshot)
			}
		})
	}
}

func tierPrecedencePolicy() *config.SafetyPolicy {
	return &config.SafetyPolicy{
		DefaultDecision: "allow",
		TierDefaults: []config.PolicyTierDefault{
			{
				Tier:     config.PolicyTierWorkflow,
				Selector: config.PolicySelector{WorkflowID: "deploy-prod"},
				Decision: "deny",
			},
		},
		Rules: []config.PolicyRule{
			{
				ID:       "global-allow-read",
				Tier:     config.PolicyTierGlobal,
				Match:    tierPrecedenceReadMatch(),
				Decision: "allow",
				Reason:   "global policy allows safe reads",
			},
			{
				ID:       "workflow-deny-read",
				Tier:     config.PolicyTierWorkflow,
				Selector: config.PolicySelector{WorkflowID: "deploy-prod"},
				Match:    tierPrecedenceReadMatch(),
				Decision: "deny",
				Reason:   "deploy-prod workflow denies reads",
			},
			{
				ID:       "job-allow-read",
				Tier:     config.PolicyTierJob,
				Selector: config.PolicySelector{JobID: "job/abc/policy"},
				Match:    tierPrecedenceReadMatch(),
				Decision: "allow",
				Reason:   "single job attachment allows this read",
			},
			{
				ID:       "global-allow-edge-read",
				Tier:     config.PolicyTierGlobal,
				Match:    tierPrecedenceEdgeReadMatch(),
				Decision: "allow",
				Reason:   "global policy allows safe Edge reads",
			},
			{
				ID:       "workflow-deny-edge-read",
				Tier:     config.PolicyTierWorkflow,
				Selector: config.PolicySelector{WorkflowID: "deploy-prod"},
				Match:    tierPrecedenceEdgeReadMatch(),
				Decision: "deny",
				Reason:   "deploy-prod workflow denies Edge reads",
			},
			{
				ID:       "job-allow-edge-read",
				Tier:     config.PolicyTierJob,
				Selector: config.PolicySelector{JobID: "job/abc/policy"},
				Match:    tierPrecedenceEdgeReadMatch(),
				Decision: "allow",
				Reason:   "single job attachment allows this Edge read",
			},
		},
	}
}

func tierPrecedenceInvariants() *config.SafetyPolicy {
	return &config.SafetyPolicy{
		Rules: []config.PolicyRule{
			{
				ID: "inv-deny-env-read",
				Match: config.PolicyMatch{
					Topics: []string{"job.read"},
					Labels: map[string]string{"path": ".env"},
				},
				Decision: "deny",
				Reason:   "global invariant blocks .env reads",
			},
		},
	}
}

func tierPrecedenceReadMatch() config.PolicyMatch {
	return config.PolicyMatch{
		Topics: []string{"job.read"},
		Labels: map[string]string{"operation": "read"},
	}
}

func tierPrecedenceEdgeReadMatch() config.PolicyMatch {
	return config.PolicyMatch{
		Topics: []string{EdgeActionTopic},
		Labels: map[string]string{"operation": "read", "edge_case": "true"},
	}
}

func tierPrecedenceTopic(labels map[string]string) string {
	if labels["edge_case"] == "true" {
		return EdgeActionTopic
	}
	return "job.read"
}
