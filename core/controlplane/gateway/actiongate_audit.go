package gateway

import (
	"context"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/policy/actiongates"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// emitPolicyActionGateAudit records gateway-local action-gate short-circuits
// before the Safety Kernel call is skipped. The event intentionally carries
// only tenant/principal, gate/mode/sub_reason/decision, and coarse action
// metadata; raw paths, URLs, args, approval refs, tool payloads, and secrets
// are never copied from the request or gate Extra map.
func (s *server) emitPolicyActionGateAudit(
	ctx context.Context,
	tenant string,
	principalID string,
	mode string,
	req *policyCheckRequest,
	dec actiongates.ActionGateDecision,
) {
	if s == nil {
		return
	}
	edgecore.SendSIEMEvent(s.auditExporter, policyActionGateAuditEvent(tenant, principalID, mode, req, dec))
}

func policyActionGateAuditEvent(
	tenant string,
	principalID string,
	mode string,
	req *policyCheckRequest,
	dec actiongates.ActionGateDecision,
) audit.SIEMEvent {
	gate := firstNonEmpty(dec.GateID, dec.Extra["gate"], "actiongate")
	subReason := firstNonEmpty(dec.SubReason, dec.Extra["sub_reason"], "unspecified")
	extra := map[string]string{}
	addActionGateAuditExtra(extra, "mode", mode)
	addActionGateAuditExtra(extra, "gate", gate)
	addActionGateAuditExtra(extra, "sub_reason", subReason)
	addPolicyActionMetadata(extra, req, dec)

	return audit.SIEMEvent{
		Timestamp:   time.Now().UTC(),
		EventType:   audit.EventActionGateDenied,
		Severity:    policyActionGateAuditSeverity(dec, subReason),
		TenantID:    boundedActionGateAuditValue(tenant, 80),
		Action:      boundedActionGateAuditValue(gate, 80),
		Decision:    boundedActionGateAuditValue(dec.Decision.String(), 64),
		MatchedRule: boundedActionGateAuditValue(gate, 80),
		Reason:      policyActionGateAuditReason(dec),
		Identity:    boundedActionGateAuditValue(principalID, 80),
		Extra:       extra,
	}
}

func addPolicyActionMetadata(extra map[string]string, req *policyCheckRequest, dec actiongates.ActionGateDecision) {
	if req != nil && req.Action != nil {
		addActionGateAuditExtra(extra, "kind", string(req.Action.Kind))
		addActionGateAuditExtra(extra, "verb", string(req.Action.Verb))
		if req.Action.TargetResource != nil {
			addActionGateAuditExtra(extra, "target_type", req.Action.TargetResource.Type)
		}
	}
	if extra["kind"] == "" {
		addActionGateAuditExtra(extra, "kind", dec.Extra["kind"])
	}
	if extra["verb"] == "" {
		addActionGateAuditExtra(extra, "verb", dec.Extra["verb"])
	}
	if extra["target_type"] == "" {
		addActionGateAuditExtra(extra, "target_type", dec.Extra["target_type"])
	}
}

func addActionGateAuditExtra(extra map[string]string, key string, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	extra[key] = boundedActionGateAuditValue(value, 80)
}

func policyActionGateAuditSeverity(dec actiongates.ActionGateDecision, subReason string) string {
	if dec.Decision == pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN {
		return audit.SeverityMedium
	}
	lower := strings.ToLower(subReason)
	for _, marker := range []string{
		"cross_tenant",
		"self_approval",
		"credential_path",
		"credential_pattern",
		"exfil_destination",
		"metadata_service",
		"prompt_exfil",
		"approval_tenant_mismatch",
	} {
		if strings.Contains(lower, marker) {
			return audit.SeverityHigh
		}
	}
	return audit.SeverityMedium
}

func policyActionGateAuditReason(dec actiongates.ActionGateDecision) string {
	code := strings.TrimSpace(dec.Code)
	if code == "" {
		return "action_gate_short_circuit"
	}
	return boundedActionGateAuditValue(code, 80)
}

func boundedActionGateAuditValue(value string, max int) string {
	v := strings.TrimSpace(value)
	if max <= 0 {
		max = 64
	}
	if len(v) <= max {
		return v
	}
	return v[:max] + "…"
}
