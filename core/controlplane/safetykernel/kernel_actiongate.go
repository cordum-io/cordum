package safetykernel

import (
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/policy/actiongates"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// actionGateResponse maps an ActionGateDecision to the gRPC response
// envelope. MatchedRule is stable across deploys ("actiongate.<id>") so
// SIEM consumers can pivot without parsing free-form Reason fields.
// The PolicySnapshot is the kernel's current snapshot so cache-key
// downstream consumers can disambiguate decisions across policy bumps.
func actionGateResponse(dec actiongates.ActionGateDecision, snapshot string) *pb.PolicyCheckResponse {
	resp := &pb.PolicyCheckResponse{
		Decision:       dec.Decision,
		Reason:         dec.Reason,
		RuleId:         dec.GateID,
		PolicySnapshot: snapshot,
	}
	if dec.Decision == pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN {
		resp.ApprovalRequired = true
	}
	return resp
}

// actionGateAuditEvent constructs the SIEM event recorded when the
// action-gate pipeline short-circuits with a non-allow decision. Extra
// carries gate + sub_reason + safe target metadata only — raw target
// paths / URLs / args are PII-risk and never embedded here.
func actionGateAuditEvent(req *pb.PolicyCheckRequest, input *config.PolicyInput, dec actiongates.ActionGateDecision) audit.SIEMEvent {
	tenant := ""
	if input != nil {
		tenant = input.Tenant
	}
	if tenant == "" {
		tenant = req.GetTenant()
	}
	agentID := ""
	if input != nil {
		agentID = input.Meta.AgentID
	}
	severity := actionGateSeverity(dec)
	extra := make(map[string]string, len(dec.Extra)+2)
	for k, v := range dec.Extra {
		extra[k] = v
	}
	if extra["gate"] == "" && dec.GateID != "" {
		extra["gate"] = dec.GateID
	}
	if extra["sub_reason"] == "" && dec.SubReason != "" {
		extra["sub_reason"] = dec.SubReason
	}
	if input != nil && input.Action != nil && input.Action.TargetResource != nil && input.Action.TargetResource.Type != "" {
		extra["target_type"] = input.Action.TargetResource.Type
	}
	return audit.SIEMEvent{
		Timestamp:   time.Now().UTC(),
		EventType:   audit.EventActionGateDenied,
		Severity:    severity,
		TenantID:    tenant,
		AgentID:     agentID,
		JobID:       req.GetJobId(),
		Action:      dec.GateID,
		Decision:    dec.Decision.String(),
		MatchedRule: dec.GateID,
		Reason:      dec.Reason,
		Extra:       extra,
	}
}

// actionGateSeverity buckets the SIEM severity for a denial. Cross-tenant
// access, credential paths, exfil destinations, and self-approval are
// HIGH because they map to known attacker behaviors. REQUIRE_HUMAN
// outcomes are MEDIUM (process gating, not a hostile probe).
func actionGateSeverity(dec actiongates.ActionGateDecision) string {
	if dec.Decision == pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN {
		return audit.SeverityMedium
	}
	switch {
	case containsAny(dec.SubReason, "cross_tenant", "self_approval", "credential_path", "credential_pattern", "exfil_destination", "metadata_service", "prompt_exfil", "approval_tenant_mismatch"):
		return audit.SeverityHigh
	default:
		return audit.SeverityMedium
	}
}

// containsAny reports whether s contains any of the supplied substrings.
// Local helper to avoid a tiny strings.Contains loop at the call site.
func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if n != "" && indexOfSubstring(s, n) >= 0 {
			return true
		}
	}
	return false
}

func indexOfSubstring(s, sub string) int {
	n := len(s)
	m := len(sub)
	if m == 0 {
		return 0
	}
	if m > n {
		return -1
	}
	for i := 0; i+m <= n; i++ {
		if s[i:i+m] == sub {
			return i
		}
	}
	return -1
}
