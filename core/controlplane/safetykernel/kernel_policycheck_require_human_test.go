package safetykernel

import (
	"context"
	"testing"

	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// TestKernel_PolicyCheck_EndToEndDecisionType_REQUIRE_HUMAN exercises the
// full (*server).Evaluate() request path — the production "kernel.PolicyCheck()"
// entry point — end-to-end for the 5 false-positive scenarios from
// task-96f931fe DoD #4 + architect amendment comment-79a9e609 §(3).
//
// The companion helper-level test
// TestShouldDowngradeDenyToRequireHuman_FalsePositiveScenarios (in
// decision_threshold_test.go) pins the downgrade helper's return value —
// the unit-level guarantee. THIS test pins the producer-wiring at
// kernel.go:920 (where the helper's result feeds Decision) plus the
// threshold load at kernel.go:1497 (where RequireHumanThreshold is captured
// from the active SafetyPolicy). A future change that bypasses the dispatch
// — e.g., a new input-rule branch that returns DENY without consulting the
// helper — would ship undetected against helper-level tests but is caught
// here.
//
// Filed as task-e5091133 per QA recommendation on task-96f931fe (msg-27dd1e72)
// after the carrier shipped at HEAD 0f49d0cd.
func TestKernel_PolicyCheck_EndToEndDecisionType_REQUIRE_HUMAN(t *testing.T) {
	threshold := config.RequireHumanThreshold{
		MinSeverityForDeny:      "high",
		MinConfidenceForDeny:    0.8,
		DowngradeWhenPromptOnly: true,
	}

	cases := []struct {
		name    string
		ruleID  string
		keyword string
		content []byte
	}{
		{
			name:    "defensive /etc/passwd mention — prompt-only education content",
			ruleID:  "scanner_secret_path_leak",
			keyword: "/etc/passwd",
			content: []byte("security runbook: never log /etc/passwd contents"),
		},
		{
			name:    "rm -rf mention in defensive runbook — prompt-only",
			ruleID:  "scanner_destructive_mention",
			keyword: "rm -rf",
			content: []byte("incident write-up: never run rm -rf on production hosts"),
		},
		{
			name:    "API-key rotation procedure with no key value",
			ruleID:  "scanner_api_key_rotation_docs",
			keyword: "api-key rotation",
			content: []byte("see the api-key rotation runbook for the procedure"),
		},
		{
			name:    "approval-token logging in compliance docs — no token value",
			ruleID:  "scanner_approval_token_log",
			keyword: "approval-token logging",
			content: []byte("compliance doc: approval-token logging is forbidden"),
		},
		{
			name:    "metadata-service 169.254.169.254 education — no outbound URL action",
			ruleID:  "url_metadata_education",
			keyword: "169.254.169.254",
			content: []byte("cloud security: never trust 169.254.169.254 metadata service responses"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Fresh kernel per subtest so any state Evaluate() touches under the
			// kernel's RLock cannot bleed across cases. Mirrors the hermetic-test
			// posture from kernel_invariants_test.go.
			srv := &server{}
			policy := &config.SafetyPolicy{
				DefaultDecision: "allow",
				RequireHuman:    threshold,
				InputRules: []config.InputPolicyRule{{
					ID:       tc.ruleID,
					Severity: "medium",
					Match: config.InputPolicyMatch{
						Topics:   []string{"job.test"},
						Keywords: []string{tc.keyword},
					},
					Decision: "deny",
				}},
			}
			if err := srv.setPolicyWithInvariants(context.Background(), policy, nil, "cfg:e2e-fp", 0); err != nil {
				t.Fatalf("setPolicyWithInvariants: %v", err)
			}

			resp, err := srv.Evaluate(context.Background(), &pb.PolicyCheckRequest{
				JobId:        "job-" + tc.ruleID,
				Topic:        "job.test",
				Tenant:       "default",
				InputContent: tc.content,
			})
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}

			// Explicit equality on the REQUIRE_HUMAN target — mutation-resistant
			// per Yaron senior-QA directive (and [[feedback_qa_senior_review]]):
			// never NotEqual against ALLOW, because a regression that returned
			// DENY or THROTTLE would pass that weaker check. The end-to-end
			// dispatch at kernel.go:920 must produce exactly REQUIRE_HUMAN for
			// each ambiguous-input scenario.
			if got := resp.GetDecision(); got != pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN {
				t.Fatalf("end-to-end Evaluate decision = %v, want REQUIRE_HUMAN (rule=%q reason=%q)",
					got, resp.GetRuleId(), resp.GetReason())
			}
			if resp.GetRuleId() != tc.ruleID {
				t.Fatalf("ruleId = %q, want %q (the configured input rule must fire end-to-end)",
					resp.GetRuleId(), tc.ruleID)
			}
			if !resp.GetApprovalRequired() {
				t.Fatalf("ApprovalRequired = false, want true (REQUIRE_HUMAN must set approval_required at kernel.go:947)")
			}
		})
	}
}

// TestKernel_PolicyCheck_EndToEndDecisionType_HighSeverityActionBoundStaysDeny
// guards the cross-rule isolation contract from
// TestShouldDowngradeDenyToRequireHuman_HighSeverityActionBoundStaysDeny at
// the integration level: a high-severity action-bound DENY must NOT leak
// into the REQUIRE_HUMAN downgrade path even with the threshold installed.
// This pins the helper's else-branch end-to-end so an over-broad future
// generalization of the downgrade rule is caught at the kernel.PolicyCheck
// boundary, not just the helper.
//
// The actionExtractor installation mirrors production: the gateway middleware
// wires (*server).SetActionDescriptorExtractor at startup. A nil extractor —
// the gRPC-only path — falls into the prompt-only branch covered by the
// sibling FP test.
func TestKernel_PolicyCheck_EndToEndDecisionType_HighSeverityActionBoundStaysDeny(t *testing.T) {
	srv := &server{}
	srv.SetActionDescriptorExtractor(func(_ context.Context, _ *pb.PolicyCheckRequest) *config.ActionDescriptor {
		return &config.ActionDescriptor{
			Kind: config.ActionKindFile,
			TargetResource: &config.ActionTargetResource{
				Type: "file",
			},
		}
	})

	policy := &config.SafetyPolicy{
		DefaultDecision: "allow",
		RequireHuman: config.RequireHumanThreshold{
			MinSeverityForDeny:      "high",
			MinConfidenceForDeny:    0.8,
			DowngradeWhenPromptOnly: true,
		},
		InputRules: []config.InputPolicyRule{{
			ID:       "scanner_high_severity_action_bound",
			Severity: "high",
			Match: config.InputPolicyMatch{
				Topics:   []string{"job.test"},
				Keywords: []string{"AKIA"},
			},
			Decision: "deny",
		}},
	}
	if err := srv.setPolicyWithInvariants(context.Background(), policy, nil, "cfg:e2e-action-bound", 0); err != nil {
		t.Fatalf("setPolicyWithInvariants: %v", err)
	}

	resp, err := srv.Evaluate(context.Background(), &pb.PolicyCheckRequest{
		JobId:        "job-high-sev-action-bound",
		Topic:        "job.test",
		Tenant:       "default",
		InputContent: []byte("credentials leak: AKIAIOSFODNN7EXAMPLE"),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	// Explicit equality on DENY — a regression that downgrades action-bound
	// high-severity denies would flip this to REQUIRE_HUMAN and fail here.
	if got := resp.GetDecision(); got != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("end-to-end Evaluate decision = %v, want DENY (rule=%q reason=%q) — action-bound high-severity must NOT downgrade",
			got, resp.GetRuleId(), resp.GetReason())
	}
	if resp.GetRuleId() != "scanner_high_severity_action_bound" {
		t.Fatalf("ruleId = %q, want %q", resp.GetRuleId(), "scanner_high_severity_action_bound")
	}
}
