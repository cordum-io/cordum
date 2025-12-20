package safetykernel

import (
	"context"
	"testing"

	"github.com/yaront1111/coretex-os/core/infra/config"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
)

func TestCheckRequiresTenantAndTopic(t *testing.T) {
	s := &server{}

	resp, err := s.Check(context.Background(), &pb.PolicyCheckRequest{Topic: "job.chat.simple"})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("expected deny for missing tenant, got %s", resp.GetDecision().String())
	}

	resp, err = s.Check(context.Background(), &pb.PolicyCheckRequest{Tenant: "default"})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("expected deny for missing topic, got %s", resp.GetDecision().String())
	}
}

func TestCheckAppliesTenantPolicy(t *testing.T) {
	policy := &config.SafetyPolicy{
		DefaultTenant: "default",
		Tenants: map[string]config.TenantPolicy{
			"default": {
				AllowTopics: []string{"job.chat.*"},
				DenyTopics:  []string{"job.chat.secret"},
			},
		},
	}
	s := &server{policy: policy}

	resp, _ := s.Check(context.Background(), &pb.PolicyCheckRequest{Tenant: "default", Topic: "job.chat.simple"})
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("expected allow, got %s (%s)", resp.GetDecision().String(), resp.GetReason())
	}

	resp, _ = s.Check(context.Background(), &pb.PolicyCheckRequest{Tenant: "default", Topic: "job.code.llm"})
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("expected deny for non-allowed topic, got %s", resp.GetDecision().String())
	}

	resp, _ = s.Check(context.Background(), &pb.PolicyCheckRequest{Tenant: "default", Topic: "job.chat.secret"})
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("expected deny for denied topic, got %s", resp.GetDecision().String())
	}
}

func TestCheckAppliesEffectiveConfigRestrictions(t *testing.T) {
	policy := &config.SafetyPolicy{
		DefaultTenant: "default",
		Tenants: map[string]config.TenantPolicy{
			"default": {AllowTopics: []string{"job.*"}},
		},
	}
	s := &server{policy: policy}

	effective := []byte(`{"safety":{"denied_topics":["job.chat.*"]}}`)
	resp, _ := s.Check(context.Background(), &pb.PolicyCheckRequest{
		Tenant:          "default",
		Topic:           "job.chat.simple",
		EffectiveConfig: effective,
	})
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("expected deny by effective config, got %s (%s)", resp.GetDecision().String(), resp.GetReason())
	}
}
