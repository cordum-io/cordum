package safetykernel

import (
	"context"
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

func compositionServer(t *testing.T, req *pb.PolicyCheckRequest, denySource string) *server {
	t.Helper()
	policy := &config.SafetyPolicy{DefaultDecision: "allow"}
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
		t.Run(denySource, func(t *testing.T) {
			req := compositionRequest()
			srv := compositionServer(t, req, denySource)
			compositionGate(srv)
			resp, err := srv.Check(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY || resp.GetApprovalRequired() || resp.GetApprovalRef() != "" {
				t.Fatalf("want DENY without approval, got %v", resp)
			}
			if n := cacheSize(srv); n != 0 {
				t.Fatalf("gate-fired request cached %d decisions", n)
			}
		})
	}
}

func TestCompositionGateOnlyRemainsUncached(t *testing.T) {
	req := compositionRequest()
	srv := compositionServer(t, req, "")
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
