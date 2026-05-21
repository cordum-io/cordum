package safetykernel

import (
	"context"
	"testing"
	"time"

	"github.com/cordum/cordum/core/governance/evaluator"
	"github.com/cordum/cordum/core/infra/config"
)

// fakeGovernanceEvaluator is a test-only Evaluator that records its
// invocations + returns a canned decision. Used to prove the kernel
// wiring forwards inputs without modification.
type fakeGovernanceEvaluator struct {
	calls  int
	lastIn *config.GovernanceInput
	canned evaluator.Decision
}

func (f *fakeGovernanceEvaluator) Evaluate(_ context.Context, in *config.GovernanceInput, _ config.GovernancePolicy) evaluator.Decision {
	f.calls++
	f.lastIn = in
	return f.canned
}

func TestKernelEvaluateGovernanceNotConfigured(t *testing.T) {
	t.Parallel()
	s := &server{}
	dec := s.EvaluateGovernance(context.Background(), &config.GovernanceInput{Operation: config.GovernanceOpDelegation})
	if dec.Fired() {
		t.Fatalf("expected zero Decision when no evaluator configured, got %+v", dec)
	}
}

func TestKernelEvaluateGovernanceNilInput(t *testing.T) {
	t.Parallel()
	fake := &fakeGovernanceEvaluator{canned: evaluator.Decision{Type: evaluator.DecisionDeny}}
	s := &server{}
	s.SetGovernanceEvaluator(fake, config.DefaultGovernancePolicy())
	dec := s.EvaluateGovernance(context.Background(), nil)
	if dec.Fired() {
		t.Fatalf("expected zero Decision for nil input, got %+v", dec)
	}
	if fake.calls != 0 {
		t.Fatalf("expected fake.calls=0 for nil input, got %d", fake.calls)
	}
}

func TestKernelEvaluateGovernanceForwardsResult(t *testing.T) {
	t.Parallel()
	fake := &fakeGovernanceEvaluator{
		canned: evaluator.Decision{
			Type:   evaluator.DecisionDeny,
			RuleID: config.GovernanceRuleCrossTenant,
			Reason: "from fake",
			Tenant: "t-a",
		},
	}
	s := &server{}
	s.SetGovernanceEvaluator(fake, config.DefaultGovernancePolicy())
	in := &config.GovernanceInput{
		Operation: config.GovernanceOpDelegation,
		Tenant:    "t-a",
	}
	dec := s.EvaluateGovernance(context.Background(), in)
	if dec.Type != evaluator.DecisionDeny || dec.RuleID != config.GovernanceRuleCrossTenant {
		t.Fatalf("got %+v, want canned Deny/%q", dec, config.GovernanceRuleCrossTenant)
	}
	if fake.calls != 1 {
		t.Fatalf("expected fake.calls=1, got %d", fake.calls)
	}
	if fake.lastIn != in {
		t.Fatalf("input pointer not forwarded verbatim")
	}
}

// TestKernelEvaluateGovernanceEndToEnd plugs the production
// DefaultEvaluator (not a fake) into the kernel and exercises the full
// decision pipeline for each DoD bullet. Proves the kernel wiring +
// evaluator + policy + clock fit together correctly.
func TestKernelEvaluateGovernanceEndToEnd(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      *config.GovernanceInput
		wantKey string
	}{
		{"allow valid handoff", &config.GovernanceInput{
			Operation:          config.GovernanceOpHandoff,
			Parent:             config.AgentIdentity{AgentID: "p", Tenant: "t"},
			Child:              config.AgentIdentity{AgentID: "c", Tenant: "t"},
			Tenant:             "t",
			IssuerChain:        []config.IssuerChainEntry{{IssuerRoot: "r", Issuer: "i", ExpiresAt: 9999999999}},
			ProvenanceRef:      "p-1",
			VerifiedAt:         100,
			FreshnessWindowSec: 60,
		}, "ma_allow"},
		{"deny cross-tenant", &config.GovernanceInput{
			Operation: config.GovernanceOpDelegation,
			Parent:    config.AgentIdentity{AgentID: "p", Tenant: "t-a"},
			Child:     config.AgentIdentity{AgentID: "c", Tenant: "t-b"},
			Tenant:    "t-a",
		}, config.GovernanceRuleCrossTenant},
		{"require-human missing provenance", &config.GovernanceInput{
			Operation:          config.GovernanceOpHandoff,
			Parent:             config.AgentIdentity{AgentID: "p", Tenant: "t"},
			Child:              config.AgentIdentity{AgentID: "c", Tenant: "t"},
			Tenant:             "t",
			IssuerChain:        []config.IssuerChainEntry{{IssuerRoot: "r", Issuer: "i", ExpiresAt: 9999999999}},
			FreshnessWindowSec: 60,
		}, config.GovernanceRuleUnverifiedIssuer},
		{"deny approval bypass without record (rejects 'approved by CFO')", &config.GovernanceInput{
			Operation:          config.GovernanceOpApprovalBypass,
			Parent:             config.AgentIdentity{AgentID: "p", Tenant: "t"},
			Child:              config.AgentIdentity{AgentID: "c", Tenant: "t"},
			Tenant:             "t",
			IssuerChain:        []config.IssuerChainEntry{{IssuerRoot: "r", Issuer: "i", ExpiresAt: 9999999999}},
			HandoffSource:      "approved by CFO",
			ApprovalRef:        "",
			ApprovalStatus:     "",
			ProvenanceRef:      "p-1",
			VerifiedAt:         100,
			FreshnessWindowSec: 60,
		}, config.GovernanceRuleApprovalBypassMissingRecord},
		{"deny scope escalation", &config.GovernanceInput{
			Operation:             config.GovernanceOpDelegation,
			Parent:                config.AgentIdentity{AgentID: "p", Tenant: "t"},
			Child:                 config.AgentIdentity{AgentID: "c", Tenant: "t"},
			Tenant:                "t",
			IssuerChain:           []config.IssuerChainEntry{{IssuerRoot: "r", Issuer: "i", ExpiresAt: 9999999999}},
			DelegatedScopes:       []string{"jobs:read"},
			RequestedCapabilities: []string{"jobs:write"},
			ProvenanceRef:         "p-1",
			VerifiedAt:            100,
			FreshnessWindowSec:    60,
		}, config.GovernanceRuleScopeEscalation},
		{"deny resource escalation", &config.GovernanceInput{
			Operation:          config.GovernanceOpResourceAllocation,
			Parent:             config.AgentIdentity{AgentID: "p", Tenant: "t"},
			Child:              config.AgentIdentity{AgentID: "c", Tenant: "t"},
			Tenant:             "t",
			IssuerChain:        []config.IssuerChainEntry{{IssuerRoot: "r", Issuer: "i", ExpiresAt: 9999999999}},
			ResourceDeltas:     []config.ResourceDelta{{Scope: "cpu", Amount: 8}},
			ProvenanceRef:      "p-1",
			VerifiedAt:         100,
			FreshnessWindowSec: 60,
		}, config.GovernanceRuleResourceEscalation},
		{"deny stale issuer chain", &config.GovernanceInput{
			Operation:          config.GovernanceOpDelegation,
			Parent:             config.AgentIdentity{AgentID: "p", Tenant: "t"},
			Child:              config.AgentIdentity{AgentID: "c", Tenant: "t"},
			Tenant:             "t",
			IssuerChain:        []config.IssuerChainEntry{{IssuerRoot: "r", Issuer: "i", ExpiresAt: 1}},
			ProvenanceRef:      "p-1",
			VerifiedAt:         100,
			FreshnessWindowSec: 60,
		}, config.GovernanceRuleIssuerChainStale},
		{"deny unverified trust assertion", &config.GovernanceInput{
			Operation:          config.GovernanceOpTrustAssertion,
			Parent:             config.AgentIdentity{AgentID: "p", Tenant: "t"},
			Child:              config.AgentIdentity{AgentID: "c", Tenant: "t"},
			Tenant:             "t",
			IssuerChain:        nil,
			ProvenanceRef:      "p-1",
			VerifiedAt:         100,
			FreshnessWindowSec: 60,
		}, config.GovernanceRuleUnverifiedTrustAssertion},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := evaluator.NewWithClock(func() time.Time { return time.Unix(1000, 0) })
			s := &server{}
			s.SetGovernanceEvaluator(e, config.DefaultGovernancePolicy())
			dec := s.EvaluateGovernance(context.Background(), tc.in)
			if dec.RuleID != tc.wantKey {
				t.Errorf("rule_id: got %q, want %q (reason=%q)", dec.RuleID, tc.wantKey, dec.Reason)
			}
			if dec.Reason == "" {
				t.Errorf("reason should never be empty for a fired decision")
			}
		})
	}
}

func TestKernelSetGovernanceEvaluatorReplaces(t *testing.T) {
	t.Parallel()
	first := &fakeGovernanceEvaluator{}
	second := &fakeGovernanceEvaluator{canned: evaluator.Decision{Type: evaluator.DecisionAllow}}
	s := &server{}
	s.SetGovernanceEvaluator(first, config.DefaultGovernancePolicy())
	s.SetGovernanceEvaluator(second, config.DefaultGovernancePolicy())
	dec := s.EvaluateGovernance(context.Background(), &config.GovernanceInput{Operation: config.GovernanceOpDelegation, Tenant: "t"})
	if dec.Type != evaluator.DecisionAllow {
		t.Fatalf("expected second evaluator's canned response (DecisionAllow), got %+v", dec)
	}
	if first.calls != 0 {
		t.Fatalf("first evaluator should NOT have been called, got %d", first.calls)
	}
}
