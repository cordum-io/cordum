package agentd

import (
	"context"
	"fmt"
	"strings"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/claude"
)

// DecisionEvidence is the redacted evidence envelope agentd records after a
// Gateway evaluate response (or a degraded fail-mode outcome). It deliberately
// carries the already-redacted/hashed Claude mapper fields only; raw prompt,
// tool payload, transcript path, and local filesystem paths must not be copied
// into AgentActionEvent bodies.
type DecisionEvidence struct {
	State            SessionState
	Request          claude.AgentdRequest
	Response         EvaluateResponse
	ArtifactPointers []edgecore.ArtifactPointer

	CacheHit   bool
	CacheMiss  bool
	Degraded   bool
	FailClosed bool

	ErrorCode    string
	ErrorMessage string
	DurationMS   int
}

// RecordDecisionEvidence writes a best-effort policy decision event and returns
// the hook decision unchanged. Evidence upload failure is observable through the
// returned sanitized error, but it never flips a fresh Gateway allow/deny into a
// different hook result.
func RecordDecisionEvidence(ctx context.Context, writer EventWriter, decision claude.AgentdDecision, evidence DecisionEvidence) (claude.AgentdDecision, error) {
	if writer == nil {
		return decision, nil
	}
	event, err := BuildDecisionEvidenceEvent(evidence)
	if err != nil {
		return decision, fmt.Errorf("build decision evidence: %s", sanitizeEventText(err.Error()))
	}
	if _, err := writer.WriteEvent(ctx, event); err != nil {
		return decision, fmt.Errorf("record decision evidence: %s", sanitizeEventText(err.Error()))
	}
	return decision, nil
}

// BuildDecisionEvidenceEvent converts a redacted agentd evaluate result into
// the Edge action-event contract. Artifact pointers are attached through the
// shared EDGE-013 helper so cross-tenant/session/event pointer mismatches are
// rejected before persistence.
func BuildDecisionEvidenceEvent(evidence DecisionEvidence) (edgecore.AgentActionEvent, error) {
	req := evidence.Request
	resp := evidence.Response
	state := evidence.State

	eventID := strings.TrimSpace(resp.EventID)
	if strings.TrimSpace(eventID) == "" {
		eventID = "agentd-" + randomHex(16)
	}

	input := safeInputRedacted(req.InputRedacted)
	if len(input) == 0 {
		input = map[string]any{
			"event_name": boundMetadataString(req.EventName),
			"tool_name":  boundMetadataString(req.ToolName),
		}
	}

	event := edgecore.AgentActionEvent{
		EventID:        boundMetadataString(eventID),
		SessionID:      boundMetadataString(nonEmpty(req.SessionID, state.SessionID)),
		ExecutionID:    boundMetadataString(nonEmpty(req.ExecutionID, state.ExecutionID)),
		TenantID:       boundMetadataString(nonEmpty(req.TenantID, state.TenantID)),
		PrincipalID:    boundMetadataString(nonEmpty(req.PrincipalID, state.PrincipalID)),
		Timestamp:      time.Now().UTC(),
		Layer:          edgecore.LayerHook,
		Kind:           edgecore.EventKindHookPolicyDecision,
		AgentProduct:   "cordum-agentd",
		ToolName:       boundMetadataString(req.ToolName),
		ToolUseID:      boundMetadataString(req.ToolUseID),
		ActionName:     decisionEvidenceActionName(req),
		Capability:     boundMetadataString(req.Capability),
		RiskTags:       boundedStringSlice(req.RiskTags),
		InputRedacted:  input,
		InputHash:      boundMetadataString(nonEmpty(resp.InputHash, req.InputHash)),
		Decision:       decisionEvidenceDecision(resp),
		DecisionReason: decisionEvidenceReason(resp),
		RuleID:         boundMetadataString(resp.RuleID),
		RuleTier:       decisionEvidenceRuleTier(resp),
		PolicySnapshot: boundMetadataString(nonEmpty(resp.PolicySnapshot, state.PolicySnapshot)),
		ApprovalRef:    boundMetadataString(resp.ApprovalRef),
		DurationMS:     decisionEvidenceDuration(req, resp, evidence.DurationMS),
		Status:         decisionEvidenceStatus(resp, evidence),
		ErrorCode:      boundMetadataString(nonEmpty(evidence.ErrorCode, resp.ErrorCode)),
		ErrorMessage:   sanitizeEventText(nonEmpty(evidence.ErrorMessage, resp.ErrorMessage)),
		Labels:         decisionEvidenceLabels(req, state, evidence),
	}
	for _, ptr := range evidence.ArtifactPointers {
		if err := edgecore.AttachArtifactPointer(&event, ptr); err != nil {
			return edgecore.AgentActionEvent{}, err
		}
	}
	return event, nil
}

func decisionEvidenceActionName(req claude.AgentdRequest) string {
	if strings.TrimSpace(req.ReasonCode) != "" {
		return boundMetadataString(req.ReasonCode)
	}
	if strings.TrimSpace(req.EventName) != "" {
		return boundMetadataString("claude." + strings.ToLower(strings.TrimSpace(req.EventName)) + ".decision")
	}
	return "edge.evaluate"
}

func decisionEvidenceDecision(resp EvaluateResponse) edgecore.EdgeDecision {
	switch strings.ToUpper(strings.TrimSpace(resp.Decision)) {
	case string(edgecore.DecisionAllow):
		return edgecore.DecisionAllow
	case string(edgecore.DecisionDeny):
		return edgecore.DecisionDeny
	case string(edgecore.DecisionRequireApproval):
		return edgecore.DecisionRequireApproval
	case string(edgecore.DecisionThrottle):
		return edgecore.DecisionThrottle
	case string(edgecore.DecisionConstrain):
		return edgecore.DecisionConstrain
	case string(edgecore.DecisionRecorded):
		return edgecore.DecisionRecorded
	default:
		return edgecore.DecisionDeny
	}
}

func decisionEvidenceStatus(resp EvaluateResponse, evidence DecisionEvidence) edgecore.ActionStatus {
	if evidence.Degraded || resp.Degraded {
		return edgecore.ActionStatusDegraded
	}
	switch decisionEvidenceDecision(resp) {
	case edgecore.DecisionAllow, edgecore.DecisionConstrain, edgecore.DecisionRecorded:
		return edgecore.ActionStatusOK
	default:
		return edgecore.ActionStatusBlocked
	}
}

func decisionEvidenceReason(resp EvaluateResponse) string {
	if reason := decisionReason(resp); strings.TrimSpace(reason) != "" {
		return reason
	}
	switch decisionEvidenceDecision(resp) {
	case edgecore.DecisionAllow:
		return "allowed by Cordum Edge"
	case edgecore.DecisionDeny:
		return "denied by Cordum Edge"
	case edgecore.DecisionRequireApproval:
		return "Cordum Edge requires approval"
	case edgecore.DecisionThrottle:
		return "throttled by Cordum Edge"
	case edgecore.DecisionConstrain:
		return "allowed with Cordum Edge constraints"
	default:
		return "recorded by Cordum Edge"
	}
}

func decisionEvidenceDuration(req claude.AgentdRequest, resp EvaluateResponse, explicit int) int {
	if explicit > 0 {
		return explicit
	}
	if resp.TimeoutMS > 0 {
		return resp.TimeoutMS
	}
	if req.DurationMS > 0 {
		return req.DurationMS
	}
	return 0
}

func decisionEvidenceRuleTier(resp EvaluateResponse) string {
	tier := normalizeDecisionTier(resp.RuleTier)
	if tier != "" {
		return tier
	}
	if strings.TrimSpace(resp.RuleID) != "" {
		return "global"
	}
	return ""
}

func normalizeDecisionTier(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "global", "workflow", "job":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func decisionEvidenceLabels(req claude.AgentdRequest, state SessionState, evidence DecisionEvidence) edgecore.Labels {
	labels := edgecore.Labels{
		"source": "cordum-agentd",
	}
	if evidence.CacheHit {
		labels["cache"] = "hit"
	} else if evidence.CacheMiss {
		labels["cache"] = "miss"
	}
	if evidence.Degraded || evidence.Response.Degraded {
		labels["degraded"] = "true"
	}
	if evidence.FailClosed {
		labels["fail_closed"] = "true"
	}
	if state.TraceID != "" {
		labels["trace_id"] = sanitizeEventText(state.TraceID)
	}
	if evidence.Response.ActionHash != "" {
		labels["action_hash"] = sanitizeEventText(evidence.Response.ActionHash)
	} else if req.ActionHash != "" {
		labels["action_hash"] = sanitizeEventText(req.ActionHash)
	}
	if tier := decisionEvidenceRuleTier(evidence.Response); tier != "" {
		labels["tier"] = tier
	}
	for k, v := range req.Labels {
		if unsafeEvidenceLabel(k, v) {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(k), "tier") || strings.EqualFold(strings.TrimSpace(k), "rule_tier") {
			continue
		}
		if len(labels) >= edgecore.MaxLabelEntries {
			break
		}
		labels[boundMetadataString(k)] = sanitizeEventText(v)
	}
	return labels
}

func unsafeEvidenceLabel(key, value string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	if k == "" || isSensitiveMetadataKey(k) {
		return true
	}
	for _, marker := range []string{"path", "cwd", "file", "directory", "dir", "home"} {
		if strings.Contains(k, marker) {
			return true
		}
	}
	v := strings.TrimSpace(value)
	return strings.Contains(v, `\`) || strings.Contains(v, "/Users/") || strings.Contains(strings.ToLower(v), "c:\\users\\")
}

func boundedStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	for i, value := range in {
		out[i] = sanitizeEventText(value)
	}
	return out
}

func sanitizeEventText(value string) string {
	return boundMetadataString(redactSecretLike(strings.TrimSpace(value)))
}
