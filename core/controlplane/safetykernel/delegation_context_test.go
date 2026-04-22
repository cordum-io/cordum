package safetykernel

import (
	"context"
	"testing"

	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestEvaluate_DelegationPredicatesFromLabels(t *testing.T) {
	t.Setenv(envDelegationPolicyEnabled, "true")

	srv := &server{}
	_ = srv.setPolicy(context.Background(), &config.SafetyPolicy{
		DefaultDecision: "allow",
		Rules: []config.PolicyRule{
			{
				ID:       "deny-deep-delegation",
				Decision: "deny",
				Reason:   "delegation too deep",
				Match: config.PolicyMatch{
					Predicate: "delegation.depth > 2",
				},
			},
		},
	}, "snap-delegation")

	resp, err := srv.Evaluate(context.Background(), &pb.PolicyCheckRequest{
		JobId:  "job-1",
		Tenant: "default",
		Topic:  "job.test",
		Labels: map[string]string{
			config.LabelDelegationDepth: "3",
		},
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("decision = %v, want DENY", resp.GetDecision())
	}
	if resp.GetRuleId() != "deny-deep-delegation" {
		t.Fatalf("rule_id = %q, want deny-deep-delegation", resp.GetRuleId())
	}
}

func TestEvaluate_DelegationPredicatesFeatureFlagOff(t *testing.T) {
	t.Setenv(envDelegationPolicyEnabled, "false")

	srv := &server{}
	_ = srv.setPolicy(context.Background(), &config.SafetyPolicy{
		DefaultDecision: "allow",
		Rules: []config.PolicyRule{
			{
				ID:       "deny-deep-delegation",
				Decision: "deny",
				Reason:   "delegation too deep",
				Match: config.PolicyMatch{
					Predicate: "delegation.depth > 2",
				},
			},
		},
	}, "snap-delegation")

	resp, err := srv.Evaluate(context.Background(), &pb.PolicyCheckRequest{
		JobId:  "job-1",
		Tenant: "default",
		Topic:  "job.test",
		Labels: map[string]string{
			config.LabelDelegationDepth: "3",
		},
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("decision = %v, want ALLOW when feature flag is off", resp.GetDecision())
	}
}
