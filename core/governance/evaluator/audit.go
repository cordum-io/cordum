package evaluator

import (
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/model"
)

// BuildEvidence assembles a compact, non-secret governance evidence
// record from the typed input + the evaluator's decision. Callers
// attach the returned value to model.SafetyDecisionRecord.Governance
// for audit emission.
//
// The returned record is cardinality-bounded: IDs / hashes / refs /
// stable rule constants only. Raw tokens, private prompts, secrets,
// and full shared-memory payloads are NEVER copied (the input doesn't
// carry those fields to begin with — this is defense-in-depth).
//
// Returns nil when either argument is missing.
func BuildEvidence(in *config.GovernanceInput, dec Decision) *model.GovernanceDecisionEvidence {
	if in == nil {
		return nil
	}
	ev := &model.GovernanceDecisionEvidence{
		Operation:             string(in.Operation),
		RuleID:                dec.RuleID,
		Decision:              decisionString(dec.Type),
		Reason:                dec.Reason,
		SubReason:             dec.SubReason,
		ParentAgentID:         in.Parent.AgentID,
		ChildAgentID:          in.Child.AgentID,
		ProvenanceRef:         in.ProvenanceRef,
		ProvenanceVerified:    in.ProvenanceRef != "" && in.VerifiedAt > 0,
		ApprovalRef:           in.ApprovalRef,
		ApprovalStatus:        in.ApprovalStatus,
		RequestedCapabilities: append([]string(nil), in.RequestedCapabilities...),
	}
	if len(in.IssuerChain) > 0 {
		ev.IssuerRoot = in.IssuerChain[0].IssuerRoot
		ev.ParentIssuer = in.IssuerChain[len(in.IssuerChain)-1].Issuer
		ev.JTI = in.IssuerChain[0].JTI
	}
	if len(in.ResourceDeltas) > 0 {
		scopes := make([]string, 0, len(in.ResourceDeltas))
		for _, d := range in.ResourceDeltas {
			scopes = append(scopes, d.Scope)
		}
		ev.ResourceScopes = scopes
	}
	return ev
}

// decisionString maps the evaluator's DecisionKind enum to a stable
// audit-string. Matches the wire-level safety decision vocabulary so
// SIEM consumers can pivot on the same values across action gates,
// rule evaluator, and multi-agent governance.
func decisionString(k DecisionKind) string {
	switch k {
	case DecisionAllow:
		return "ALLOW"
	case DecisionDeny:
		return "DENY"
	case DecisionRequireHuman:
		return "REQUIRE_HUMAN"
	default:
		return ""
	}
}
