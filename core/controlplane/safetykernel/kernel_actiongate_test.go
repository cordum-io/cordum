package safetykernel

import (
	"context"
	"sync"
	"testing"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/policy/actiongates"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// stubGate is a minimal ActionGate the kernel test uses to drive the
// pipeline without depending on real gates' fakes.
type stubGate struct {
	id  string
	out actiongates.ActionGateDecision
}

func (g *stubGate) ID() string { return g.id }

func (g *stubGate) Evaluate(_ context.Context, _ *config.PolicyInput) actiongates.ActionGateDecision {
	return g.out
}

// TestCheckActionGateShortCircuits proves the kernel's Check path runs
// the action-gate pipeline BEFORE legacy rule evaluation, and that a
// firing gate decision is mapped to the response without consulting
// rules.
func TestCheckActionGateShortCircuits(t *testing.T) {
	// Permissive default policy: if rules ran, the response would be ALLOW.
	srv := &server{policy: &config.SafetyPolicy{
		DefaultTenant:   "default",
		DefaultDecision: "allow",
		Tenants: map[string]config.TenantPolicy{
			"default": {AllowTopics: []string{"job.*"}},
		},
	}}

	denied := actiongates.ActionGateDecision{
		Decision:  pb.DecisionType_DECISION_TYPE_DENY,
		GateID:    "actiongate.test",
		Code:      actiongates.CodeAccessDenied,
		Reason:    "denied by stub gate",
		SubReason: "cross_tenant:test_sub",
		Extra: map[string]string{
			"gate":       "actiongate.test",
			"sub_reason": "cross_tenant:test_sub",
		},
	}
	srv.SetActionGatePipeline(actiongates.NewPipeline(&stubGate{id: "actiongate.test", out: denied}))
	srv.SetActionDescriptorExtractor(func(_ context.Context, _ *pb.PolicyCheckRequest) *config.ActionDescriptor {
		return &config.ActionDescriptor{Kind: config.ActionKindMutation, Verb: config.ActionVerbDelete}
	})

	var (
		sinkMu     sync.Mutex
		sinkEvents []audit.SIEMEvent
	)
	srv.SetActionGateAuditSink(func(_ context.Context, e audit.SIEMEvent) {
		sinkMu.Lock()
		defer sinkMu.Unlock()
		sinkEvents = append(sinkEvents, e)
	})

	req := &pb.PolicyCheckRequest{
		JobId:  "job-actiongate-1",
		Topic:  "job.default",
		Tenant: "default",
	}

	resp, err := srv.Check(context.Background(), req)
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("decision = %v, want DENY (gate fired)", resp.GetDecision())
	}
	if resp.GetReason() != "denied by stub gate" {
		t.Fatalf("reason = %q, want gate reason", resp.GetReason())
	}
	if resp.GetRuleId() != "actiongate.test" {
		t.Fatalf("rule_id = %q, want actiongate.test", resp.GetRuleId())
	}

	sinkMu.Lock()
	defer sinkMu.Unlock()
	if len(sinkEvents) != 1 {
		t.Fatalf("audit sink received %d events, want 1", len(sinkEvents))
	}
	ev := sinkEvents[0]
	if ev.EventType != audit.EventActionGateDenied {
		t.Fatalf("event_type = %q, want %q", ev.EventType, audit.EventActionGateDenied)
	}
	if ev.Severity != audit.SeverityHigh {
		t.Fatalf("severity = %q, want HIGH for cross_tenant", ev.Severity)
	}
	if ev.MatchedRule != "actiongate.test" || ev.Action != "actiongate.test" {
		t.Fatalf("identifying fields = matched=%q action=%q", ev.MatchedRule, ev.Action)
	}
	if ev.JobID != "job-actiongate-1" {
		t.Fatalf("job_id = %q, want propagated from request", ev.JobID)
	}
	if ev.Extra["gate"] != "actiongate.test" || ev.Extra["sub_reason"] != "cross_tenant:test_sub" {
		t.Fatalf("extra = %#v, missing gate/sub_reason", ev.Extra)
	}
}

// TestCheckNoActionGateExtractorFallsThrough proves that a kernel with
// a pipeline but no extractor leaves input.Action nil and falls back to
// the legacy rule path. Same setup as the short-circuit test minus the
// extractor → the policy now decides.
func TestCheckNoActionGateExtractorFallsThrough(t *testing.T) {
	srv := &server{policy: &config.SafetyPolicy{
		DefaultTenant:   "default",
		DefaultDecision: "allow",
		Tenants: map[string]config.TenantPolicy{
			"default": {AllowTopics: []string{"job.*"}},
		},
	}}
	denied := actiongates.ActionGateDecision{
		Decision: pb.DecisionType_DECISION_TYPE_DENY,
		GateID:   "actiongate.test",
		Reason:   "should not fire",
	}
	srv.SetActionGatePipeline(actiongates.NewPipeline(&stubGate{id: "actiongate.test", out: denied}))
	// NOTE: no extractor wired -> input.Action stays nil -> pipeline no-op.

	req := &pb.PolicyCheckRequest{
		JobId:  "job-fallthrough",
		Topic:  "job.default",
		Tenant: "default",
	}
	resp, err := srv.Check(context.Background(), req)
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("decision = %v, want ALLOW (legacy path, default allow)", resp.GetDecision())
	}
	if resp.GetRuleId() == "actiongate.test" {
		t.Fatalf("rule_id = %q, want non-gate id (pipeline must have been skipped)", resp.GetRuleId())
	}
}

// TestCheckActionGateRequireHumanMediumSeverity exercises the severity
// bucket for REQUIRE_HUMAN — MEDIUM, not HIGH.
func TestCheckActionGateRequireHumanMediumSeverity(t *testing.T) {
	srv := &server{policy: &config.SafetyPolicy{
		DefaultTenant:   "default",
		DefaultDecision: "allow",
		Tenants:         map[string]config.TenantPolicy{"default": {AllowTopics: []string{"job.*"}}},
	}}
	rh := actiongates.ActionGateDecision{
		Decision:  pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
		GateID:    "actiongate.test",
		Code:      actiongates.CodeRequireHuman,
		Reason:    "human approval required",
		SubReason: "missing_approval",
	}
	srv.SetActionGatePipeline(actiongates.NewPipeline(&stubGate{id: "actiongate.test", out: rh}))
	srv.SetActionDescriptorExtractor(func(_ context.Context, _ *pb.PolicyCheckRequest) *config.ActionDescriptor {
		return &config.ActionDescriptor{Kind: config.ActionKindMutation, Verb: config.ActionVerbDelete}
	})
	var sinkEv audit.SIEMEvent
	srv.SetActionGateAuditSink(func(_ context.Context, e audit.SIEMEvent) { sinkEv = e })

	resp, err := srv.Check(context.Background(), &pb.PolicyCheckRequest{
		JobId: "job-rh", Topic: "job.default", Tenant: "default",
	})
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN {
		t.Fatalf("decision = %v, want REQUIRE_HUMAN", resp.GetDecision())
	}
	if !resp.GetApprovalRequired() {
		t.Fatal("approval_required = false, want true")
	}
	if sinkEv.Severity != audit.SeverityMedium {
		t.Fatalf("severity = %q, want MEDIUM for REQUIRE_HUMAN", sinkEv.Severity)
	}
}
