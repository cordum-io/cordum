package actiongates

import (
	"context"

	"github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// Gate IDs are stable identifiers emitted to audit / matched_rule and surfaced
// in HTTP error envelopes. Changing them is a breaking API change.
const (
	GateIDTenant     = "actiongate.tenant"
	GateIDFile       = "actiongate.file"
	GateIDURL        = "actiongate.url"
	GateIDMCP        = "actiongate.mcp"
	GateIDMutation   = "actiongate.mutation"
	GateIDProvenance = "actiongate.provenance"
)

// Decision Codes carried on ActionGateDecision. These map to HTTP status at
// the gateway boundary; do not invent new codes without updating the mapping
// in core/controlplane/gateway/actiongates_http.go.
const (
	CodeUnauthorized       = "unauthorized"
	CodeAccessDenied       = "access_denied"
	CodeNotFound           = "not_found"
	CodeConflict           = "conflict"
	CodeInternalError      = "internal_error"
	CodeServiceUnavailable = "service_unavailable"
	// CodeResolverError maps DNS validation failures to HTTP 503 at the
	// gateway boundary; URLGate uses it to fail closed on uncertain hosts.
	CodeResolverError = "RESOLVER_ERROR"
	CodeRequireHuman  = "require_human"
)

// ActionGateDecision is the output of a single gate. A zero-value decision
// (Decision == DECISION_TYPE_UNSPECIFIED) indicates the gate did not fire and
// the pipeline continues. A populated non-allow Decision short-circuits the
// pipeline. Production action gates are blockers / approval checks: they allow,
// deny, throttle, or require human approval, but they do not emit enforceable
// runtime constraints.
//
// Reason MUST be sanitized for user/client display. SubReason is for audit
// only and may carry the internal "why" (e.g. "approval_consumed",
// "self_approval", "cross_tenant"). Extra holds non-PII gate-specific
// breadcrumbs (gate, sub_reason, sanitized target_type) for SIEM.
//
// Constraints is reserved for non-production/test adapters or a future typed
// action-gate design. Production action gates must not populate it or imply
// that generic map entries are enforced; typed runtime constraints are owned
// by policy bundles and SafetyKernel PolicyConstraints.
type ActionGateDecision struct {
	Decision    pb.DecisionType
	GateID      string
	Code        string
	Reason      string
	SubReason   string
	Extra       map[string]string
	Constraints map[string]any
}

// Fired reports whether the gate produced a real outcome. Gates that don't
// apply to an input return a zero-value decision so the pipeline knows to
// continue.
func (d ActionGateDecision) Fired() bool {
	return d.Decision != pb.DecisionType_DECISION_TYPE_UNSPECIFIED
}

// Allowed reports whether the gate explicitly allowed the action. The
// pipeline does not short-circuit on Allowed; subsequent gates still run.
func (d ActionGateDecision) Allowed() bool {
	switch d.Decision {
	case pb.DecisionType_DECISION_TYPE_ALLOW, pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS:
		return true
	}
	return false
}

// ActionGate is the contract every gate implements. Evaluate MUST be
// idempotent and side-effect free (gates are evaluated in order and may be
// re-run during simulate). Cancellation via ctx is respected.
type ActionGate interface {
	ID() string
	Evaluate(ctx context.Context, input *config.PolicyInput) ActionGateDecision
}

// ApprovalLookup resolves Cordum EdgeApproval records for action gates.
// Authorization gates MUST resolve the caller-supplied approval_ref with
// LookupByApprovalRef, then compare the returned record's ActionHash against
// CanonicalActionHash(input.Action) before allowing. LookupByActionHash is kept
// for explicit audit / legacy lookup needs and must not be used as a substitute
// for binding a user-supplied approval_ref. Implementations MUST be safe for
// concurrent use and MUST respect ctx cancellation. A miss is signalled by
// (nil, false, nil); errors propagate as (nil, false, err) and gates fail
// closed. Lookup methods must not consume approvals; single-use mutation is
// owned by the Edge approval claim/CAS path at execute time.
type ApprovalLookup interface {
	LookupByApprovalRef(ctx context.Context, tenant string, approvalRef string) (*edge.EdgeApproval, bool, error)
	LookupByActionHash(ctx context.Context, tenant string, actionHash string) (*edge.EdgeApproval, bool, error)
}

// ResourceLookup resolves an ActionTargetResource to a backend record. The
// mutation gate uses this to map missing-target to HTTP 404 instead of
// allowing the underlying call to fail silently. Returning (false, nil)
// means the target does not exist; (false, err) is treated as
// service_unavailable.
type ResourceLookup interface {
	ResourceExists(ctx context.Context, tenant string, resource config.ActionTargetResource) (bool, error)
}

// ReachabilityProbe answers whether an MCP server is currently reachable.
// Implementations should cache (LRU, short TTL) to keep gate latency
// deterministic.
type ReachabilityProbe interface {
	MCPServerReachable(ctx context.Context, server string) (bool, error)
}

// HostResolver maps a hostname to one or more IPs. Decoupled from net.LookupIP
// so URL-gate DNS-rebinding checks can be tested deterministically.
type HostResolver interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
}
