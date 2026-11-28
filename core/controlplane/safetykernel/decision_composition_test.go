package safetykernel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/policy/actiongates"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

func compositionRequest() *pb.PolicyCheckRequest {
	return &pb.PolicyCheckRequest{
		JobId: "composition-job", Topic: "job.composition", Tenant: "default",
		Labels: map[string]string{"mcp.server": "blocked.example.com", "mcp.tool": "read"},
	}
}

func compositionPolicy(approvalSource string) *config.SafetyPolicy {
	policy := &config.SafetyPolicy{DefaultDecision: "allow"}
	if approvalSource == "policy" {
		policy.Rules = []config.PolicyRule{{
			ID: "composition-approval", Decision: "require_approval",
			Match: config.PolicyMatch{Topics: []string{"job.composition"}},
		}}
	}
	if approvalSource == "input" {
		policy.InputRules = []config.InputPolicyRule{{
			ID: "composition-input-approval", Decision: "require_approval",
			Match: config.InputPolicyMatch{Topics: []string{"job.composition"}},
		}}
	}
	return policy
}

func compositionServer(t *testing.T, req *pb.PolicyCheckRequest, denySource, approvalSource string) *server {
	t.Helper()
	policy := compositionPolicy(approvalSource)
	deny := config.PolicyRule{
		ID: "composition-deny", Decision: "deny",
		Match: config.PolicyMatch{Topics: []string{req.GetTopic()}},
	}
	var invariants *config.SafetyPolicy
	switch denySource {
	case "invariant":
		invariants = &config.SafetyPolicy{Rules: []config.PolicyRule{deny}}
	case "policy":
		policy.Rules = append(policy.Rules, deny)
	case "tenant_mcp":
		policy.Tenants = map[string]config.TenantPolicy{"default": {
			AllowTopics: []string{"job.*"},
			MCP:         config.MCPPolicy{DenyServers: []string{"blocked.example.com"}},
		}}
	case "effective_config":
		req.EffectiveConfig = []byte(`{"safety":{"denied_topics":["job.composition"]}}`)
	case "input":
		policy.InputRules = append(policy.InputRules, config.InputPolicyRule{
			ID: "composition-input-deny", Decision: "deny",
			Match: config.InputPolicyMatch{Topics: []string{req.GetTopic()}},
		})
	case "":
	default:
		t.Fatalf("unknown deny source %q", denySource)
	}
	srv := &server{cacheTTL: time.Minute}
	if err := srv.setPolicyWithInvariants(context.Background(), policy, invariants, "composition", 0); err != nil {
		t.Fatal(err)
	}
	if approvalSource == "gate" {
		compositionGate(srv)
	}
	return srv
}

func compositionGate(srv *server) *stubGate {
	gate := &stubGate{id: "actiongate.composition", out: actiongates.ActionGateDecision{
		Decision: pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
		GateID:   "actiongate.composition", Reason: "approval required",
	}}
	srv.SetActionGatePipeline(actiongates.NewPipeline(gate))
	srv.SetActionDescriptorExtractor(func(context.Context, *pb.PolicyCheckRequest) *config.ActionDescriptor {
		return &config.ActionDescriptor{Kind: config.ActionKindMutation, Verb: config.ActionVerbDelete}
	})
	return gate
}

func TestCompositionDenyOutranksApproval(t *testing.T) {
	for _, denySource := range []string{"invariant", "policy", "tenant_mcp", "effective_config", "input"} {
		for _, approvalSource := range []string{"gate", "policy", "input"} {
			t.Run(denySource+"/"+approvalSource, func(t *testing.T) {
				req := compositionRequest()
				srv := compositionServer(t, req, denySource, approvalSource)
				resp, err := srv.Check(context.Background(), req)
				if err != nil {
					t.Fatal(err)
				}
				if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY || resp.GetApprovalRequired() || resp.GetApprovalRef() != "" {
					t.Fatalf("want DENY without approval, got %v", resp)
				}
				if n := cacheSize(srv); approvalSource == "gate" && n != 0 {
					t.Fatalf("gate-fired request cached %d decisions", n)
				}
			})
		}
	}
}

func TestCompositionGateOnlyRemainsUncached(t *testing.T) {
	req := compositionRequest()
	srv := compositionServer(t, req, "", "")
	gate := compositionGate(srv)
	for _, decision := range []pb.DecisionType{pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, pb.DecisionType_DECISION_TYPE_DENY} {
		gate.out.Decision = decision
		resp, err := srv.Check(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if want := actionGateResponse(gate.out, srv.snapshot); !proto.Equal(resp, want) {
			t.Fatalf("got %v, want gate response %v", resp, want)
		}
		if n := cacheSize(srv); n != 0 {
			t.Fatalf("gate-fired request cached %d decisions", n)
		}
	}
}

func TestCompositionApprovalWithoutDeny(t *testing.T) {
	for _, source := range []string{"policy", "input"} {
		t.Run(source, func(t *testing.T) {
			req := compositionRequest()
			srv := compositionServer(t, req, "", source)
			resp, err := srv.Check(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN || !resp.GetApprovalRequired() || resp.GetApprovalRef() != req.GetJobId() {
				t.Fatalf("want approval for this job, got %v", resp)
			}
		})
	}
}

func TestCompositionOldCacheEntryIsNotServed(t *testing.T) {
	req := compositionRequest()
	srv := compositionServer(t, req, "policy", "policy")
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	oldKey := srv.snapshot + ":" + hex.EncodeToString(sum[:])
	srv.cache[oldKey] = cacheEntry{
		resp: &pb.PolicyCheckResponse{
			Decision: pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, ApprovalRequired: true,
		},
		expires: time.Now().Add(time.Hour), policyVersion: srv.policyVersion.Load(),
	}
	if srv.getCachedDecision(oldKey) == nil {
		t.Fatal("old cache fixture is not live")
	}
	resp, err := srv.Check(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY || resp.GetApprovalRequired() || resp.GetApprovalRef() != "" {
		t.Fatalf("old composition served: %v", resp)
	}
}

func TestCompositionPolicyFirstMatchBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, first string
		deny        config.PolicyRule
		want        pb.DecisionType
	}{
		{"allow_unchanged", "allow", config.PolicyRule{Decision: "deny"}, pb.DecisionType_DECISION_TYPE_ALLOW},
		{"unmatched_deny", "require_approval", config.PolicyRule{Decision: "deny", Match: config.PolicyMatch{Topics: []string{"job.other"}}}, pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN},
		{"velocity_deny", "require_approval", config.PolicyRule{Decision: "deny", Velocity: &config.VelocityConfig{}}, pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN},
		{"normalized_deny", "require_human", config.PolicyRule{ID: "deny", Decision: "block"}, pb.DecisionType_DECISION_TYPE_DENY},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := &server{policy: &config.SafetyPolicy{Rules: []config.PolicyRule{
				{ID: "first", Decision: tc.first}, tc.deny,
			}}}
			resp, err := srv.Check(context.Background(), compositionRequest())
			if err != nil {
				t.Fatal(err)
			}
			if resp.GetDecision() != tc.want {
				t.Fatalf("got %v, want %v", resp, tc.want)
			}
		})
	}
}

func TestCompositionInputApprovalPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, base, first, last, wantRule string
		want                              pb.DecisionType
	}{
		{"first_approval_kept", "allow", "require_approval", "require_approval", "first", pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN},
		{"base_approval_kept", "require_approval", "require_approval", "require_approval", "base", pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN},
		{"downgraded_deny_kept", "allow", "deny", "require_approval", "first", pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN},
		{"deny_after_downgrade", "allow", "deny", "deny", "last", pb.DecisionType_DECISION_TYPE_DENY},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := &server{
				policy:                &config.SafetyPolicy{Rules: []config.PolicyRule{{ID: "base", Decision: tc.base}}},
				requireHumanThreshold: config.RequireHumanThreshold{MinSeverityForDeny: "high"},
				inputRules: []compiledInputRule{
					{id: "first", decision: tc.first, severity: "low"},
					{id: "last", decision: tc.last, severity: "critical"},
				},
			}
			resp, err := srv.Check(context.Background(), compositionRequest())
			if err != nil {
				t.Fatal(err)
			}
			if resp.GetDecision() != tc.want || resp.GetRuleId() != tc.wantRule {
				t.Fatalf("got %v, want %v from %s", resp, tc.want, tc.wantRule)
			}
		})
	}
}
