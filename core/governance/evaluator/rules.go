package evaluator

import (
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/config"
)

// ruleFunc is the canonical shape of a governance rule. Each rule
// inspects the input + policy and returns a non-zero Decision if it
// fires. The first non-zero result from the rule chain short-circuits
// evaluation in the same order rules appear in defaultRuleOrder.
type ruleFunc func(in *config.GovernanceInput, policy config.GovernancePolicy, now time.Time) Decision

// defaultRuleOrder is the canonical evaluation order. Cheap, always-true
// invariants (cross-tenant, trust-assertion-needs-chain) run first;
// policy-dependent escalation checks last. The order matters: a
// cross-tenant operation is denied before we even check provenance,
// because the same-tenant invariant is non-overridable.
func defaultRuleOrder() []ruleFunc {
	return []ruleFunc{
		ruleCrossTenant,
		ruleTrustAssertionNeedsIssuerChain,
		ruleApprovalBypassRecord,
		ruleSharedContextUnverifiedWriter,
		ruleUnverifiedIssuer,
		ruleIssuerChainStale,
		ruleAllowedIssuerRoots,
		ruleMaxDelegationDepth,
		ruleScopeEscalation,
		ruleResourceEscalation,
	}
}

// ruleCrossTenant denies any multi-agent operation that crosses tenant
// boundaries when SameTenantRequired is set. Per-tenant isolation is a
// non-overridable invariant in the default policy; the policy flag
// exists only for explicit cross-tenant operator setups (e.g. shared
// services tenants).
func ruleCrossTenant(in *config.GovernanceInput, policy config.GovernancePolicy, _ time.Time) Decision {
	if !policy.SameTenantRequired {
		return Decision{}
	}
	if in.Parent.Tenant != in.Tenant || in.Child.Tenant != in.Tenant {
		return Decision{
			Type:      DecisionDeny,
			RuleID:    config.GovernanceRuleCrossTenant,
			Reason:    "cross-tenant multi-agent operation rejected",
			Tenant:    in.Tenant,
			SubReason: "parent_or_child_tenant_mismatch",
		}
	}
	return Decision{}
}

// ruleTrustAssertionNeedsIssuerChain denies trust assertions that
// arrive without a verified issuer chain. A trust assertion claims
// "agent X says agent Y is trusted to do Z" — without a backend-
// verified chain it is just a user-claimed string.
func ruleTrustAssertionNeedsIssuerChain(in *config.GovernanceInput, _ config.GovernancePolicy, _ time.Time) Decision {
	if in.Operation != config.GovernanceOpTrustAssertion {
		return Decision{}
	}
	if len(in.IssuerChain) == 0 {
		return Decision{
			Type:   DecisionDeny,
			RuleID: config.GovernanceRuleUnverifiedTrustAssertion,
			Reason: "trust assertion lacks verified issuer chain",
			Tenant: in.Tenant,
		}
	}
	return Decision{}
}

// ruleApprovalBypassRecord denies approval_bypass operations that lack
// a backend approval record. The whole point of approval bypass is the
// CORDUM approval store proof that a human authorized the bypass;
// without that record the operation is indistinguishable from a child
// agent claiming "approved by CFO".
func ruleApprovalBypassRecord(in *config.GovernanceInput, _ config.GovernancePolicy, _ time.Time) Decision {
	if in.Operation != config.GovernanceOpApprovalBypass {
		return Decision{}
	}
	if strings.TrimSpace(in.ApprovalRef) == "" || strings.TrimSpace(in.ApprovalStatus) != "approved" {
		return Decision{
			Type:      DecisionDeny,
			RuleID:    config.GovernanceRuleApprovalBypassMissingRecord,
			Reason:    "approval bypass requires an approved backend record",
			Tenant:    in.Tenant,
			SubReason: "approval_ref_missing_or_status_not_approved",
		}
	}
	return Decision{}
}

// ruleSharedContextUnverifiedWriter denies shared-memory writes that
// mutate policy/trust/directive state without a verified writer. The
// fail-closed default is the most dangerous attack vector for context
// poisoning — a child agent overwriting the parent's trust state and
// downstream agents inheriting compromised policy.
func ruleSharedContextUnverifiedWriter(in *config.GovernanceInput, _ config.GovernancePolicy, _ time.Time) Decision {
	if in.Operation != config.GovernanceOpSharedContextWrite {
		return Decision{}
	}
	if !isPolicyMutatingWriteKind(in.WriteKind) && !in.PolicyStateMutation {
		return Decision{}
	}
	if strings.TrimSpace(in.Parent.AgentID) == "" || strings.TrimSpace(in.ProvenanceRef) == "" {
		return Decision{
			Type:      DecisionDeny,
			RuleID:    config.GovernanceRuleSharedContextUnverifiedWriter,
			Reason:    "shared-context policy/trust-state write requires verified writer + provenance",
			Tenant:    in.Tenant,
			SubReason: "writer_agent_id_or_provenance_ref_missing",
		}
	}
	return Decision{}
}

// ruleUnverifiedIssuer fires when an operation requiring verified
// provenance lacks one. For mid-risk operations (delegation/handoff/
// resource_allocation) the decision is REQUIRE_HUMAN — the missing
// provenance MAY be recoverable via human approval. For trust assertion
// and approval bypass we never reach this rule (denied earlier).
func ruleUnverifiedIssuer(in *config.GovernanceInput, policy config.GovernancePolicy, _ time.Time) Decision {
	if !policy.RequireVerifiedProvenance[in.Operation] {
		return Decision{}
	}
	if strings.TrimSpace(in.ProvenanceRef) != "" && in.VerifiedAt > 0 {
		return Decision{}
	}
	return Decision{
		Type:      DecisionRequireHuman,
		RuleID:    config.GovernanceRuleUnverifiedIssuer,
		Reason:    "verified provenance required for this operation",
		Tenant:    in.Tenant,
		SubReason: "provenance_ref_or_verified_at_missing",
	}
}

// ruleIssuerChainStale denies (or escalates to REQUIRE_HUMAN per
// policy) when any issuer-chain entry has an expired ExpiresAt that
// predates now.
func ruleIssuerChainStale(in *config.GovernanceInput, policy config.GovernancePolicy, now time.Time) Decision {
	if len(in.IssuerChain) == 0 {
		return Decision{}
	}
	nowSec := now.Unix()
	for _, entry := range in.IssuerChain {
		if entry.ExpiresAt > 0 && entry.ExpiresAt < nowSec {
			decision := DecisionDeny
			if strings.EqualFold(policy.StaleProvenanceDecision, "require_human") {
				decision = DecisionRequireHuman
			}
			return Decision{
				Type:      decision,
				RuleID:    config.GovernanceRuleIssuerChainStale,
				Reason:    "issuer chain contains stale entry",
				Tenant:    in.Tenant,
				SubReason: "entry_expires_at_in_past",
			}
		}
	}
	return Decision{}
}

// ruleAllowedIssuerRoots denies when the first chain entry's root is
// not in the operator-configured allowlist. Empty allowlist means no
// constraint (allowlist gating is opt-in).
func ruleAllowedIssuerRoots(in *config.GovernanceInput, policy config.GovernancePolicy, _ time.Time) Decision {
	if len(policy.AllowedIssuerRoots) == 0 || len(in.IssuerChain) == 0 {
		return Decision{}
	}
	root := in.IssuerChain[0].IssuerRoot
	for _, allowed := range policy.AllowedIssuerRoots {
		if allowed == root {
			return Decision{}
		}
	}
	return Decision{
		Type:      DecisionDeny,
		RuleID:    config.GovernanceRuleIssuerRootNotAllowed,
		Reason:    "issuer chain root not in allowlist",
		Tenant:    in.Tenant,
		SubReason: "root_not_allowlisted",
	}
}

// ruleMaxDelegationDepth denies when the issuer chain length exceeds
// policy MaxDelegationDepth. This is the child-bypass guard: a long
// chain of intermediate issuers is the classic attack pattern for a
// child agent attempting to launder authority.
func ruleMaxDelegationDepth(in *config.GovernanceInput, policy config.GovernancePolicy, _ time.Time) Decision {
	if policy.MaxDelegationDepth <= 0 {
		return Decision{}
	}
	if len(in.IssuerChain) > policy.MaxDelegationDepth {
		return Decision{
			Type:      DecisionDeny,
			RuleID:    config.GovernanceRuleChildBypass,
			Reason:    "delegation depth exceeds policy maximum",
			Tenant:    in.Tenant,
			SubReason: "issuer_chain_too_deep",
		}
	}
	return Decision{}
}

// ruleScopeEscalation fires when RequestedCapabilities is not a subset
// of DelegatedScopes. For delegation operations specifically — a
// delegated agent cannot ask for more capabilities than it was granted.
func ruleScopeEscalation(in *config.GovernanceInput, policy config.GovernancePolicy, _ time.Time) Decision {
	if policy.AllowScopeEscalation {
		return Decision{}
	}
	if in.Operation != config.GovernanceOpDelegation {
		return Decision{}
	}
	if len(in.RequestedCapabilities) == 0 {
		return Decision{}
	}
	for _, req := range in.RequestedCapabilities {
		if !containsString(in.DelegatedScopes, req) {
			return Decision{
				Type:      DecisionDeny,
				RuleID:    config.GovernanceRuleScopeEscalation,
				Reason:    "requested capabilities exceed delegated scopes",
				Tenant:    in.Tenant,
				SubReason: "capability_not_in_delegated_scope",
			}
		}
	}
	return Decision{}
}

// ruleResourceEscalation fires when any positive resource delta is
// requested while AllowResourceEscalation is false. Resource deltas
// are append-only by ResourceDelta validation, so any Amount>0 is a
// request for more resources than the parent agent's allocation.
func ruleResourceEscalation(in *config.GovernanceInput, policy config.GovernancePolicy, _ time.Time) Decision {
	if policy.AllowResourceEscalation {
		return Decision{}
	}
	for _, d := range in.ResourceDeltas {
		if d.Amount > 0 {
			return Decision{
				Type:      DecisionDeny,
				RuleID:    config.GovernanceRuleResourceEscalation,
				Reason:    "resource escalation request not authorized",
				Tenant:    in.Tenant,
				SubReason: "positive_amount_delta_in_request",
			}
		}
	}
	return Decision{}
}

// isPolicyMutatingWriteKind reports whether the write_kind touches
// downstream agent policy/trust/directive state. Raw and chat writes
// are passive and stay on the legacy context-engine path.
func isPolicyMutatingWriteKind(k config.SharedMemoryWriteKind) bool {
	switch k {
	case config.SharedMemoryWriteSharedPolicyState,
		config.SharedMemoryWriteSharedTrustState,
		config.SharedMemoryWriteSharedDirective:
		return true
	default:
		return false
	}
}

func containsString(slice []string, s string) bool {
	for _, x := range slice {
		if x == s {
			return true
		}
	}
	return false
}
