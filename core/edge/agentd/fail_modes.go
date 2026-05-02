package agentd

import (
	"strings"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/claude"
)

// GatewayErrorCategory classifies why agentd could not get a fresh governance
// decision from Gateway. The category drives the fail-mode policy: the same
// "Gateway said nothing" outcome should fail-open in observe but fail-closed
// in enterprise-strict.
//
// GatewayErrorNone is the only category that means "Gateway responded with a
// real decision"; every other category is a degraded path.
type GatewayErrorCategory string

const (
	GatewayErrorNone              GatewayErrorCategory = ""
	GatewayErrorUnavailable       GatewayErrorCategory = "unavailable"
	GatewayErrorTimeout           GatewayErrorCategory = "timeout"
	GatewayErrorMalformed         GatewayErrorCategory = "malformed"
	GatewayErrorPolicyUnavailable GatewayErrorCategory = "policy_unavailable"
)

// FailModeContext is the input to ApplyFailMode. It is the centralized place
// where every failure path encodes "what category of Gateway/governance miss
// happened, and is the action governable enough to require denial?"
type FailModeContext struct {
	// PolicyMode comes from the EdgeSession (observe / enforce / enterprise-strict).
	// Empty falls back to PolicyModeObserve, matching the session-create default.
	PolicyMode edgecore.PolicyMode

	// WorkflowEdgeRequired is true when a Cordum workflow has explicitly tagged
	// this Edge action as requires-edge-governance — even in observe mode the
	// fail-mode then denies on Gateway failure.
	WorkflowEdgeRequired bool

	// ActionIsKnownSafe is true when the EDGE-008 classifier produced a
	// safe-build/test/etc capability with no destructive/network/secrets risk
	// tags. observe lets known-safe through on Gateway failure; enforce still
	// allows known-safe; enterprise-strict still denies.
	ActionIsKnownSafe bool

	// HasFreshDecision is true when Evaluate returned a parsed response with a
	// non-degraded decision in this exact request lifetime. False when Gateway
	// errored, returned a malformed body, or returned degraded=true.
	HasFreshDecision bool

	// GatewayErrorCategory describes the failure type. Always GatewayErrorNone
	// when HasFreshDecision is true.
	GatewayErrorCategory GatewayErrorCategory

	// ApprovalRef is non-empty when the action is part of an approval-retry
	// flow. Fail modes never bless a missing-fresh-decision approval consume.
	ApprovalRef string
}

// FailModeOutcome is the deterministic decision the calling layer should
// surface to cordum-hook (and persist as evidence).
type FailModeOutcome struct {
	Decision     claude.Decision
	Reason       string
	Degraded     bool
	FailClosed   bool
	TerminalCopy string
}

// ApplyFailMode encodes the PRD fail-mode matrix into one decision function.
// It is invoked when Evaluate failed, returned a malformed/degraded response,
// or could not be reached at all. A successful Gateway response with
// HasFreshDecision=true should NOT call ApplyFailMode — that path uses the
// real Gateway decision directly. The two outputs differ deliberately so the
// calling layer can distinguish "agentd allowed this because Gateway said so"
// from "agentd allowed this because the policy mode permits a degraded miss."
//
// Matrix:
//
//	WorkflowEdgeRequired=true       -> always DENY+failClosed (overrides observe)
//	PolicyMode=enterprise-strict    -> always DENY+failClosed
//	PolicyMode=enforce              -> ALLOW only when ActionIsKnownSafe; else DENY
//	PolicyMode=observe (default)    -> ALLOW always (degraded marker on response)
//	ApprovalRef present + no fresh  -> always DENY (never consume on degraded)
//
// The Reason/TerminalCopy strings are sanitized — no raw payloads or secrets —
// and intentionally short so cordum-hook's terminal output stays readable.
func ApplyFailMode(ctx FailModeContext) FailModeOutcome {
	mode := ctx.PolicyMode
	if strings.TrimSpace(string(mode)) == "" {
		mode = edgecore.PolicyModeObserve
	}

	// Defense in depth: an approval retry that lost its Gateway decision must
	// never consume the approval. The CAS itself rejects mismatched snapshots
	// but the agentd-side fail-mode decision should be DENY before the call.
	if strings.TrimSpace(ctx.ApprovalRef) != "" {
		return FailModeOutcome{
			Decision:     claude.DecisionDeny,
			Reason:       "approval retry could not reach Cordum governance; not consuming approval",
			Degraded:     true,
			FailClosed:   true,
			TerminalCopy: failModeTerminalApproval(ctx.GatewayErrorCategory),
		}
	}

	// Workflow-required-edge-governance and enterprise-strict both fail closed
	// on any miss, regardless of action risk.
	if ctx.WorkflowEdgeRequired {
		return FailModeOutcome{
			Decision:     claude.DecisionDeny,
			Reason:       failModeReason("workflow requires Cordum Edge governance", ctx.GatewayErrorCategory),
			Degraded:     true,
			FailClosed:   true,
			TerminalCopy: failModeTerminalCopy("workflow requires Cordum Edge governance for this action; failing closed", ctx.GatewayErrorCategory),
		}
	}
	if mode == edgecore.PolicyModeEnterpriseStrict {
		return FailModeOutcome{
			Decision:     claude.DecisionDeny,
			Reason:       failModeReason("enterprise-strict denies when Cordum is unavailable", ctx.GatewayErrorCategory),
			Degraded:     true,
			FailClosed:   true,
			TerminalCopy: failModeTerminalCopy("Cordum Edge enterprise-strict mode failed closed because governance is unavailable", ctx.GatewayErrorCategory),
		}
	}

	// Enforce: only known-safe actions are allowed through on a Gateway miss.
	// Risky/unknown actions fail closed so the production path cannot silently
	// allow `rm -rf` or a network egress when Gateway is down.
	if mode == edgecore.PolicyModeEnforce {
		if ctx.ActionIsKnownSafe {
			return FailModeOutcome{
				Decision:     claude.DecisionAllow,
				Reason:       failModeReason("known-safe action allowed in enforce mode despite Cordum unavailable", ctx.GatewayErrorCategory),
				Degraded:     true,
				FailClosed:   false,
				TerminalCopy: "",
			}
		}
		return FailModeOutcome{
			Decision:     claude.DecisionDeny,
			Reason:       failModeReason("enforce mode denies risky action when Cordum governance is unavailable", ctx.GatewayErrorCategory),
			Degraded:     true,
			FailClosed:   true,
			TerminalCopy: failModeTerminalCopy("Cordum Edge enforce mode failed closed because governance is unavailable", ctx.GatewayErrorCategory),
		}
	}

	// Observe (default and local-dev): record the action and allow. The
	// degraded marker makes the audit trail honest even when nothing was
	// blocked. observe is intentionally noisy in the audit log but quiet in
	// the terminal so safe actions don't spam users during local development.
	return FailModeOutcome{
		Decision:     claude.DecisionAllow,
		Reason:       failModeReason("observe mode allowed this action; Cordum governance was unavailable", ctx.GatewayErrorCategory),
		Degraded:     true,
		FailClosed:   false,
		TerminalCopy: "",
	}
}

// failModeReason appends the Gateway error category to the base reason so the
// audit event can be filtered by category later (timeout vs malformed vs
// genuinely unavailable). All-empty errors fall through unchanged.
func failModeReason(base string, cat GatewayErrorCategory) string {
	if cat == "" || cat == GatewayErrorNone {
		return base
	}
	return base + " (" + string(cat) + ")"
}

// failModeTerminalCopy is the cordum-hook terminal one-liner for risky/strict
// fail-closed paths. It must be short, sanitized, and self-explanatory.
func failModeTerminalCopy(base string, cat GatewayErrorCategory) string {
	if cat == "" || cat == GatewayErrorNone {
		return base + ". Action was not run."
	}
	return base + " (" + string(cat) + "). Action was not run."
}

// failModeTerminalApproval is the terminal copy when an approval retry was
// caught by the fail mode. Tells the user the approval was NOT consumed and
// they should retry once Gateway is reachable again.
func failModeTerminalApproval(cat GatewayErrorCategory) string {
	if cat == "" || cat == GatewayErrorNone {
		return "Cordum governance unavailable; this approval was NOT consumed. Retry once Cordum is reachable."
	}
	return "Cordum governance unavailable (" + string(cat) + "); this approval was NOT consumed. Retry once Cordum is reachable."
}
