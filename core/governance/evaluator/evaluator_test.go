package evaluator

import (
	"context"
	"testing"
	"time"

	"github.com/cordum/cordum/core/infra/config"
)

func validBaseInput() *config.GovernanceInput {
	return &config.GovernanceInput{
		Operation:          config.GovernanceOpDelegation,
		Parent:             config.AgentIdentity{AgentID: "parent-1", Tenant: "tenant-a"},
		Child:              config.AgentIdentity{AgentID: "child-1", Tenant: "tenant-a"},
		Tenant:             "tenant-a",
		IssuerChain:        []config.IssuerChainEntry{{IssuerRoot: "root-1", Issuer: "issuer-1", JTI: "jti-1", IssuedAt: 1000, ExpiresAt: 999999999999}},
		DelegatedScopes:    []string{"jobs:submit", "memory:read"},
		ProvenanceRef:      "prov-001",
		VerifiedAt:         1500,
		FreshnessWindowSec: 300,
	}
}

func fixedClock() time.Time { return time.Unix(2000, 0) }

func TestEvaluateAllow(t *testing.T) {
	t.Parallel()
	e := NewWithClock(fixedClock)
	in := validBaseInput()
	dec := e.Evaluate(context.Background(), in, config.DefaultGovernancePolicy())
	if dec.Type != DecisionAllow {
		t.Fatalf("type: got %v, want DecisionAllow; rule=%s reason=%s", dec.Type, dec.RuleID, dec.Reason)
	}
}

func TestEvaluateNilInput(t *testing.T) {
	t.Parallel()
	e := New()
	dec := e.Evaluate(context.Background(), nil, config.DefaultGovernancePolicy())
	if dec.Fired() {
		t.Fatalf("nil input should yield DecisionUnspecified; got %v", dec.Type)
	}
}

func TestEvaluateCrossTenantDenied(t *testing.T) {
	t.Parallel()
	e := NewWithClock(fixedClock)
	in := validBaseInput()
	in.Child.Tenant = "tenant-other"
	dec := e.Evaluate(context.Background(), in, config.DefaultGovernancePolicy())
	if dec.Type != DecisionDeny {
		t.Fatalf("type: got %v, want Deny", dec.Type)
	}
	if dec.RuleID != config.GovernanceRuleCrossTenant {
		t.Errorf("rule_id: got %q, want %q", dec.RuleID, config.GovernanceRuleCrossTenant)
	}
}

func TestEvaluateCrossTenantBypassedWhenPolicyOff(t *testing.T) {
	t.Parallel()
	e := NewWithClock(fixedClock)
	in := validBaseInput()
	in.Child.Tenant = "tenant-other"
	policy := config.DefaultGovernancePolicy()
	policy.SameTenantRequired = false
	dec := e.Evaluate(context.Background(), in, policy)
	if dec.RuleID == config.GovernanceRuleCrossTenant {
		t.Fatalf("expected cross-tenant rule to be bypassed when policy.SameTenantRequired=false")
	}
}

func TestEvaluateTrustAssertionNeedsIssuerChain(t *testing.T) {
	t.Parallel()
	e := NewWithClock(fixedClock)
	in := validBaseInput()
	in.Operation = config.GovernanceOpTrustAssertion
	in.IssuerChain = nil
	dec := e.Evaluate(context.Background(), in, config.DefaultGovernancePolicy())
	if dec.Type != DecisionDeny || dec.RuleID != config.GovernanceRuleUnverifiedTrustAssertion {
		t.Fatalf("got %v/%q, want Deny/%q", dec.Type, dec.RuleID, config.GovernanceRuleUnverifiedTrustAssertion)
	}
}

func TestEvaluateApprovalBypassRecord(t *testing.T) {
	t.Parallel()
	e := NewWithClock(fixedClock)

	t.Run("missing ref", func(t *testing.T) {
		t.Parallel()
		in := validBaseInput()
		in.Operation = config.GovernanceOpApprovalBypass
		in.ApprovalRef = ""
		in.ApprovalStatus = "approved"
		dec := e.Evaluate(context.Background(), in, config.DefaultGovernancePolicy())
		if dec.Type != DecisionDeny || dec.RuleID != config.GovernanceRuleApprovalBypassMissingRecord {
			t.Fatalf("got %v/%q", dec.Type, dec.RuleID)
		}
	})
	t.Run("not approved status", func(t *testing.T) {
		t.Parallel()
		in := validBaseInput()
		in.Operation = config.GovernanceOpApprovalBypass
		in.ApprovalRef = "appr-1"
		in.ApprovalStatus = "pending"
		dec := e.Evaluate(context.Background(), in, config.DefaultGovernancePolicy())
		if dec.Type != DecisionDeny || dec.RuleID != config.GovernanceRuleApprovalBypassMissingRecord {
			t.Fatalf("got %v/%q", dec.Type, dec.RuleID)
		}
	})
	t.Run("user-claim only — 'approved by CFO'", func(t *testing.T) {
		t.Parallel()
		// The CRITICAL test: a child agent claims "approved by CFO" in
		// HandoffSource text. Without ApprovalRef/ApprovalStatus from
		// the BACKEND approval store, the rule fires.
		in := validBaseInput()
		in.Operation = config.GovernanceOpApprovalBypass
		in.HandoffSource = "approved by CFO"
		in.ApprovalRef = ""
		in.ApprovalStatus = ""
		dec := e.Evaluate(context.Background(), in, config.DefaultGovernancePolicy())
		if dec.Type != DecisionDeny || dec.RuleID != config.GovernanceRuleApprovalBypassMissingRecord {
			t.Fatalf("user-claimed text MUST NOT count as approval evidence; got %v/%q", dec.Type, dec.RuleID)
		}
	})
}

func TestEvaluateSharedContextUnverifiedWriter(t *testing.T) {
	t.Parallel()
	e := NewWithClock(fixedClock)

	t.Run("policy-state write without verified writer", func(t *testing.T) {
		t.Parallel()
		in := validBaseInput()
		in.Operation = config.GovernanceOpSharedContextWrite
		in.SharedMemoryTargetKey = "tenant-a/policy/x"
		in.WriteKind = config.SharedMemoryWriteSharedPolicyState
		in.PolicyStateMutation = true
		in.Parent.AgentID = "" // simulate missing writer identity
		in.ProvenanceRef = ""
		dec := e.Evaluate(context.Background(), in, config.DefaultGovernancePolicy())
		if dec.Type != DecisionDeny || dec.RuleID != config.GovernanceRuleSharedContextUnverifiedWriter {
			t.Fatalf("got %v/%q", dec.Type, dec.RuleID)
		}
	})

	t.Run("raw write is allowed even without provenance", func(t *testing.T) {
		t.Parallel()
		in := validBaseInput()
		in.Operation = config.GovernanceOpSharedContextWrite
		in.SharedMemoryTargetKey = "tenant-a/raw/x"
		in.WriteKind = config.SharedMemoryWriteRaw
		in.PolicyStateMutation = false
		in.ProvenanceRef = ""
		in.VerifiedAt = 0
		policy := config.DefaultGovernancePolicy()
		policy.RequireVerifiedProvenance[config.GovernanceOpSharedContextWrite] = false
		dec := e.Evaluate(context.Background(), in, policy)
		if dec.Type != DecisionAllow {
			t.Fatalf("raw shared write should ALLOW when provenance not required; got %v/%q/%s", dec.Type, dec.RuleID, dec.Reason)
		}
	})
}

func TestEvaluateUnverifiedIssuerRequiresHuman(t *testing.T) {
	t.Parallel()
	e := NewWithClock(fixedClock)
	in := validBaseInput()
	in.ProvenanceRef = "" // missing
	in.VerifiedAt = 0
	dec := e.Evaluate(context.Background(), in, config.DefaultGovernancePolicy())
	if dec.Type != DecisionRequireHuman || dec.RuleID != config.GovernanceRuleUnverifiedIssuer {
		t.Fatalf("got %v/%q, want RequireHuman/%q", dec.Type, dec.RuleID, config.GovernanceRuleUnverifiedIssuer)
	}
}

func TestEvaluateIssuerChainStale(t *testing.T) {
	t.Parallel()
	e := NewWithClock(fixedClock)
	in := validBaseInput()
	in.IssuerChain[0].ExpiresAt = 1500 // < fixedClock() unix 2000

	t.Run("deny default", func(t *testing.T) {
		t.Parallel()
		dec := e.Evaluate(context.Background(), in, config.DefaultGovernancePolicy())
		if dec.Type != DecisionDeny || dec.RuleID != config.GovernanceRuleIssuerChainStale {
			t.Fatalf("got %v/%q", dec.Type, dec.RuleID)
		}
	})
	t.Run("require_human via policy", func(t *testing.T) {
		t.Parallel()
		policy := config.DefaultGovernancePolicy()
		policy.StaleProvenanceDecision = "require_human"
		dec := e.Evaluate(context.Background(), in, policy)
		if dec.Type != DecisionRequireHuman || dec.RuleID != config.GovernanceRuleIssuerChainStale {
			t.Fatalf("got %v/%q", dec.Type, dec.RuleID)
		}
	})
}

func TestEvaluateAllowedIssuerRoots(t *testing.T) {
	t.Parallel()
	e := NewWithClock(fixedClock)
	in := validBaseInput()
	in.IssuerChain[0].IssuerRoot = "evil-root"
	policy := config.DefaultGovernancePolicy()
	policy.AllowedIssuerRoots = []string{"trusted-root-1", "trusted-root-2"}
	dec := e.Evaluate(context.Background(), in, policy)
	if dec.Type != DecisionDeny || dec.RuleID != config.GovernanceRuleIssuerRootNotAllowed {
		t.Fatalf("got %v/%q", dec.Type, dec.RuleID)
	}
}

func TestEvaluateMaxDelegationDepth(t *testing.T) {
	t.Parallel()
	e := NewWithClock(fixedClock)
	in := validBaseInput()
	// Build a 7-hop chain
	in.IssuerChain = nil
	for i := 0; i < 7; i++ {
		in.IssuerChain = append(in.IssuerChain, config.IssuerChainEntry{IssuerRoot: "root-1", Issuer: "i", ExpiresAt: 999999999999})
	}
	policy := config.DefaultGovernancePolicy()
	policy.MaxDelegationDepth = 5
	dec := e.Evaluate(context.Background(), in, policy)
	if dec.Type != DecisionDeny || dec.RuleID != config.GovernanceRuleChildBypass {
		t.Fatalf("got %v/%q", dec.Type, dec.RuleID)
	}
}

func TestEvaluateScopeEscalation(t *testing.T) {
	t.Parallel()
	e := NewWithClock(fixedClock)
	in := validBaseInput()
	in.DelegatedScopes = []string{"memory:read"}
	in.RequestedCapabilities = []string{"memory:read", "memory:write"} // memory:write is not delegated
	dec := e.Evaluate(context.Background(), in, config.DefaultGovernancePolicy())
	if dec.Type != DecisionDeny || dec.RuleID != config.GovernanceRuleScopeEscalation {
		t.Fatalf("got %v/%q", dec.Type, dec.RuleID)
	}
}

func TestEvaluateResourceEscalation(t *testing.T) {
	t.Parallel()
	e := NewWithClock(fixedClock)
	in := validBaseInput()
	in.Operation = config.GovernanceOpResourceAllocation
	in.ResourceDeltas = []config.ResourceDelta{{Scope: "cpu", Amount: 2}}
	dec := e.Evaluate(context.Background(), in, config.DefaultGovernancePolicy())
	if dec.Type != DecisionDeny || dec.RuleID != config.GovernanceRuleResourceEscalation {
		t.Fatalf("got %v/%q", dec.Type, dec.RuleID)
	}
}

func TestEvaluateChildAgentBypassViaSpoofingLabels(t *testing.T) {
	t.Parallel()
	// This test documents the END-TO-END defense: even if a child
	// agent COULD set _governance.tenant or _ma.parent_agent_id on
	// the wire, the gateway rejects it at handlers_jobs.go before
	// it reaches the evaluator. By the time the evaluator runs, the
	// fields are auth-derived. The proof here is structural: the
	// evaluator NEVER reads from request labels — it only reads
	// from the typed GovernanceInput populated by BuildGovernanceInput.
	// We exercise this by mutating only the typed input and confirming
	// the cross-tenant rule fires.
	e := NewWithClock(fixedClock)
	in := validBaseInput()
	in.Parent.Tenant = "tenant-victim" // mismatches in.Tenant ("tenant-a")
	dec := e.Evaluate(context.Background(), in, config.DefaultGovernancePolicy())
	if dec.Type != DecisionDeny || dec.RuleID != config.GovernanceRuleCrossTenant {
		t.Fatalf("got %v/%q", dec.Type, dec.RuleID)
	}
}

func TestDecisionFired(t *testing.T) {
	t.Parallel()
	zero := Decision{}
	if zero.Fired() {
		t.Fatal("zero-value Decision should not be Fired")
	}
	allow := Decision{Type: DecisionAllow}
	if !allow.Fired() {
		t.Fatal("DecisionAllow should be Fired")
	}
}
