package actiongates

import (
	"context"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// RiskTagRequiresProvenance is the canonical risk-tag string that marks a
// non-mutation action as requiring backend-verified provenance. Adding it
// to ActionDescriptor.RiskTags from upstream classifiers brings an action
// into the provenance gate's scope without it being a destructive verb.
const RiskTagRequiresProvenance = "requires_provenance"

// UnverifiedClaimReason is the exact Reason emitted when the gate rejects
// an action carrying only ClaimText with no ApprovalRef. The wording is a
// product / docs contract: it appears in audit logs and HTTP error
// envelopes so red-team reports and DPO reviews can grep for it.
const UnverifiedClaimReason = "approval claim text is not a substitute for a backend-verified approval record"

// ChainStatus enumerates the high-level outcomes a chain verifier reports
// for a single approval's audit slice. The values intentionally mirror
// the labels used by core/audit.VerifyResult so a production wiring can
// translate VerifyStatus values directly without an extra lookup table.
type ChainStatus string

const (
	// ChainStatusOK means every event in the approval's window verified.
	ChainStatusOK ChainStatus = "ok"
	// ChainStatusPartial means the verifier could not attest the full
	// window because retention trimmed older events. Not tampering.
	ChainStatusPartial ChainStatus = "partial"
	// ChainStatusCompromised means at least one event in the window failed
	// hash, linkage, or HMAC verification. Hard fail-closed signal.
	ChainStatusCompromised ChainStatus = "compromised"
)

// ChainVerifyOutcome is the verifier's structured answer about an
// approval's audit slice. Status is mandatory; HasEvidenceGap means the
// approval's own seq (or an immediately neighboring seq) is missing from
// the chain — typically a tamper signal independent of Status.
type ChainVerifyOutcome struct {
	Status         ChainStatus
	HasEvidenceGap bool
	// Detail is optional free-form context (e.g. "first gap at seq=1234")
	// surfaced into the gate's SubReason for audit. Never PII.
	Detail string
}

// ChainVerifier resolves the audit-chain status that covers a given
// approval. Production wires this to core/audit.VerifyChain bounded by
// the approval's CreatedAt..max(ResolvedAt, ConsumedAt, now) window.
// Implementations MUST be safe for concurrent use.
type ChainVerifier interface {
	VerifyForApproval(ctx context.Context, tenant string, approval *edge.EdgeApproval) (ChainVerifyOutcome, error)
}

// ProvenanceGateOptions configures the gate.
type ProvenanceGateOptions struct {
	Approvals     ApprovalLookup
	ChainVerifier ChainVerifier
}

// ProvenanceGate is the explicit, deterministic refusal of "approved by
// CFO"-style claims. It only fires when the action either is a destructive
// mutation OR carries the requires_provenance risk tag. In scope:
//
//  1. ClaimText with empty ApprovalRef → DENY/access_denied/unverified_claim
//     with the verbatim UnverifiedClaimReason.
//  2. No claim at all → REQUIRE_HUMAN (system should issue an approval).
//  3. ApprovalRef resolves to no record → DENY/not_found.
//  4. Resolved approval is tenant/self/status/decision/consumed/expired/hash invalid
//     → DENY with the same stable code/sub_reason as MutationGate.
//  5. Chain verification reports compromised → DENY/internal_error/audit_chain_compromised.
//  6. Chain verifier reports an evidence gap → DENY/internal_error/audit_evidence_missing.
//  7. Verifier error or nil verifier → DENY/service_unavailable fail-closed.
//
// Like MutationGate, ProvenanceGate resolves approval_ref first and compares
// the resolved record's ActionHash second. Evaluate remains side-effect free:
// execute-time single-use consumption stays in the Edge ClaimApproval CAS path.
type ProvenanceGate struct {
	approvals     ApprovalLookup
	chainVerifier ChainVerifier
}

// NewProvenanceGate returns a gate bound to opts.Approvals and
// opts.ChainVerifier. Nil Approvals causes destructive actions to fail
// closed with Code=internal_error; nil ChainVerifier fails closed with
// Code=service_unavailable so operators can distinguish dependency
// outage/misconfiguration from audit-chain tampering.
func NewProvenanceGate(opts ProvenanceGateOptions) *ProvenanceGate {
	return &ProvenanceGate{approvals: opts.Approvals, chainVerifier: opts.ChainVerifier}
}

func (g *ProvenanceGate) ID() string { return GateIDProvenance }

func (g *ProvenanceGate) Evaluate(ctx context.Context, in *config.PolicyInput) ActionGateDecision {
	if in == nil || in.Action == nil {
		return ActionGateDecision{}
	}
	act := in.Action
	if !requiresProvenance(act) {
		return ActionGateDecision{}
	}

	actx := auth.FromContext(ctx)
	if actx == nil || strings.TrimSpace(actx.Tenant) == "" {
		return provDecision(pb.DecisionType_DECISION_TYPE_DENY, act, CodeUnauthorized,
			"authentication required", "missing_auth")
	}

	// Claim text without a backend reference is the explicit "approved
	// by CFO" rejection — the centerpiece of this gate.
	hasClaimText := act.ApprovalClaim != nil && strings.TrimSpace(act.ApprovalClaim.ClaimText) != ""
	hasApprovalRef := act.ApprovalClaim != nil && strings.TrimSpace(act.ApprovalClaim.ApprovalRef) != ""

	if hasClaimText && !hasApprovalRef {
		dec := provDecision(pb.DecisionType_DECISION_TYPE_DENY, act, CodeAccessDenied,
			UnverifiedClaimReason, "unverified_claim")
		// Length-bounded claim digest for audit; never echo the raw text
		// (could be PII / abuse vector).
		dec.Extra["claim_present"] = "true"
		dec.Extra["claim_chars"] = sanitizeClaimLen(act.ApprovalClaim.ClaimText)
		return dec
	}

	if !hasApprovalRef {
		return provDecision(pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, act, CodeRequireHuman,
			"action requires backend-verified human approval", "missing_approval")
	}

	if g.approvals == nil {
		return provDecision(pb.DecisionType_DECISION_TYPE_DENY, act, CodeInternalError,
			"approval lookup unavailable", "approval_lookup_failed:nil_lookup")
	}
	approval, failure := bindApprovalRef(ctx, g.approvals, actx, act, act.ApprovalClaim.ApprovalRef)
	if failure.failed() {
		return provDecision(failure.Decision, act, failure.Code, failure.Reason, failure.SubReason)
	}

	if g.chainVerifier == nil {
		return provDecision(pb.DecisionType_DECISION_TYPE_DENY, act, CodeServiceUnavailable,
			"audit chain verifier unavailable", "audit_chain_verifier_unavailable")
	}
	outcome, verr := g.chainVerifier.VerifyForApproval(ctx, actx.Tenant, approval)
	if verr != nil {
		return provDecision(pb.DecisionType_DECISION_TYPE_DENY, act, CodeServiceUnavailable,
			"audit chain verification failed", "audit_chain_verify_failed:"+sanitizeErr(verr))
	}
	if outcome.Status == ChainStatusCompromised {
		return provDecision(pb.DecisionType_DECISION_TYPE_DENY, act, CodeInternalError,
			"audit chain reports tampering", "audit_chain_compromised:"+chainDetail(outcome))
	}
	if outcome.HasEvidenceGap {
		return provDecision(pb.DecisionType_DECISION_TYPE_DENY, act, CodeInternalError,
			"audit evidence for this approval is missing", "audit_evidence_missing:"+chainDetail(outcome))
	}

	// Status OK or Partial. Partial only means retention-trimmed prefix;
	// for an in-window-only verifier this still counts as authenticated.
	subReason := "provenance_ok:" + string(outcome.Status)
	return ActionGateDecision{
		Decision:  pb.DecisionType_DECISION_TYPE_ALLOW,
		GateID:    GateIDProvenance,
		Reason:    "approval backed by verified audit chain",
		SubReason: subReason,
		Extra:     provExtra(act, subReason),
	}
}

func requiresProvenance(act *config.ActionDescriptor) bool {
	if act.Kind == config.ActionKindMutation && IsDestructiveVerb(act.Verb) {
		return true
	}
	for _, tag := range act.RiskTags {
		if strings.EqualFold(strings.TrimSpace(tag), RiskTagRequiresProvenance) {
			return true
		}
	}
	return false
}

func provDecision(decision pb.DecisionType, act *config.ActionDescriptor, code, reason, sub string) ActionGateDecision {
	return ActionGateDecision{
		Decision:  decision,
		GateID:    GateIDProvenance,
		Code:      code,
		Reason:    reason,
		SubReason: sub,
		Extra:     provExtra(act, sub),
	}
}

func provExtra(act *config.ActionDescriptor, sub string) map[string]string {
	out := map[string]string{
		"gate":       GateIDProvenance,
		"sub_reason": sub,
		"kind":       string(act.Kind),
	}
	if act.Verb != "" {
		out["verb"] = string(act.Verb)
	}
	if act.TargetResource != nil && act.TargetResource.Type != "" {
		out["target_type"] = act.TargetResource.Type
	}
	return out
}

func chainDetail(o ChainVerifyOutcome) string {
	d := strings.TrimSpace(o.Detail)
	if d == "" {
		return string(o.Status)
	}
	return string(o.Status) + ":" + d
}

// sanitizeClaimLen returns a coarse length bucket for the raw claim text
// so the SIEM record can show "the user did present a claim" without
// echoing the content (PII / phishing vector).
func sanitizeClaimLen(s string) string {
	n := len(s)
	switch {
	case n == 0:
		return "0"
	case n <= 32:
		return "<=32"
	case n <= 128:
		return "<=128"
	case n <= 512:
		return "<=512"
	default:
		return ">512"
	}
}
