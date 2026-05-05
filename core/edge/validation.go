package edge

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

// Validate checks the EdgeSession wire contract without mutating the input.
func (s EdgeSession) Validate() error {
	if err := requireString("tenant_id", s.TenantID); err != nil {
		return err
	}
	if err := requireString("session_id", s.SessionID); err != nil {
		return err
	}
	if err := validatePrincipalType(s.PrincipalType); err != nil {
		return err
	}
	if err := validateSessionMode(s.Mode); err != nil {
		return err
	}
	if err := validatePolicyMode(s.PolicyMode); err != nil {
		return err
	}
	if err := validateSessionStatus(s.Status); err != nil {
		return err
	}
	if err := validateRiskSummary(s.RiskSummary); err != nil {
		return err
	}
	if err := requireTime("started_at", s.StartedAt); err != nil {
		return err
	}
	if err := validateOptionalEnd("ended_at", s.StartedAt, s.EndedAt); err != nil {
		return err
	}
	if err := validateEnforcementLayers("enforcement_layers", s.EnforcementLayers); err != nil {
		return err
	}
	if err := validateLabels("labels", s.Labels); err != nil {
		return err
	}
	return nil
}

// Validate checks the AgentExecution wire contract without mutating the input.
func (e AgentExecution) Validate() error {
	if err := requireString("tenant_id", e.TenantID); err != nil {
		return err
	}
	if err := requireString("session_id", e.SessionID); err != nil {
		return err
	}
	if err := requireString("execution_id", e.ExecutionID); err != nil {
		return err
	}
	if err := validateAgentAdapter(e.Adapter); err != nil {
		return err
	}
	if err := validateExecutionMode(e.Mode); err != nil {
		return err
	}
	if e.Attempt < 0 {
		return fmt.Errorf("attempt must be non-negative")
	}
	if err := validateExecutionStatus(e.Status); err != nil {
		return err
	}
	if err := requireTime("started_at", e.StartedAt); err != nil {
		return err
	}
	if err := validateOptionalEnd("ended_at", e.StartedAt, e.EndedAt); err != nil {
		return err
	}
	if err := validateExecutionMetrics(e.Metrics); err != nil {
		return err
	}
	if err := validateLabels("labels", e.Labels); err != nil {
		return err
	}
	return nil
}

// Validate checks the AgentActionEvent wire contract without mutating the input.
func (e AgentActionEvent) Validate() error {
	if err := requireString("tenant_id", e.TenantID); err != nil {
		return err
	}
	if err := requireString("session_id", e.SessionID); err != nil {
		return err
	}
	if err := requireString("execution_id", e.ExecutionID); err != nil {
		return err
	}
	if err := requireString("event_id", e.EventID); err != nil {
		return err
	}
	if e.Seq < 0 {
		return fmt.Errorf("seq must be non-negative")
	}
	if err := requireTime("ts", e.Timestamp); err != nil {
		return err
	}
	if err := validateLayer(e.Layer); err != nil {
		return err
	}
	if strings.TrimSpace(string(e.Kind)) == "" {
		return fmt.Errorf("kind is required")
	}
	if err := validateEdgeDecision(e.Decision); err != nil {
		return err
	}
	if err := validateRuleTier(e.RuleTier); err != nil {
		return err
	}
	if e.DurationMS < 0 {
		return fmt.Errorf("duration_ms must be non-negative")
	}
	if err := validateActionStatus(e.Status); err != nil {
		return err
	}
	if err := validateRedactedInput("input_redacted", e.InputRedacted); err != nil {
		return err
	}
	if len(e.ArtifactPointers) > MaxArtifactPointersPerEvent {
		return fmt.Errorf("artifact_ptrs has %d entries, max %d", len(e.ArtifactPointers), MaxArtifactPointersPerEvent)
	}
	for i, artifact := range e.ArtifactPointers {
		if err := artifact.Validate(); err != nil {
			return fmt.Errorf("artifact_ptrs[%d]: %w", i, err)
		}
	}
	if err := validateLabels("labels", e.Labels); err != nil {
		return err
	}
	return nil
}

// Validate checks the EdgeApproval wire contract without mutating the input.
func (a EdgeApproval) Validate() error {
	if err := requireString("tenant_id", a.TenantID); err != nil {
		return err
	}
	if err := requireString("session_id", a.SessionID); err != nil {
		return err
	}
	if err := requireString("execution_id", a.ExecutionID); err != nil {
		return err
	}
	if err := requireString("event_id", a.EventID); err != nil {
		return err
	}
	if err := requireString("approval_ref", a.ApprovalRef); err != nil {
		return err
	}
	if err := requireString("principal_id", a.PrincipalID); err != nil {
		return err
	}
	if err := requireString("requester", a.Requester); err != nil {
		return err
	}
	if err := requireString("policy_snapshot", a.PolicySnapshot); err != nil {
		return err
	}
	if err := requireString("action_hash", a.ActionHash); err != nil {
		return err
	}
	if err := validateApprovalStatus(a.Status); err != nil {
		return err
	}
	if err := validateApprovalDecisionForStatus(a.Status, a.Decision); err != nil {
		return err
	}
	if err := requireTime("created_at", a.CreatedAt); err != nil {
		return err
	}
	if a.ExpiresAt != nil && a.ExpiresAt.Before(a.CreatedAt) {
		return fmt.Errorf("expires_at must be >= created_at")
	}
	if a.ResolvedAt != nil && a.ResolvedAt.Before(a.CreatedAt) {
		return fmt.Errorf("resolved_at must be >= created_at")
	}
	if a.ConsumedAt != nil && a.ConsumedAt.Before(a.CreatedAt) {
		return fmt.Errorf("consumed_at must be >= created_at")
	}
	if a.ConsumedAt != nil && a.ResolvedAt != nil && a.ConsumedAt.Before(*a.ResolvedAt) {
		return fmt.Errorf("consumed_at must be >= resolved_at")
	}
	if a.Status == ApprovalStatusPending {
		if a.Decision != "" {
			return fmt.Errorf("decision must be empty for pending approval")
		}
		if strings.TrimSpace(a.ResolverID) != "" {
			return fmt.Errorf("resolver_id must be empty for pending approval")
		}
		if strings.TrimSpace(a.ResolvedBy) != "" {
			return fmt.Errorf("resolved_by must be empty for pending approval")
		}
		if strings.TrimSpace(a.ResolutionReason) != "" {
			return fmt.Errorf("resolution_reason must be empty for pending approval")
		}
		if a.ResolvedAt != nil {
			return fmt.Errorf("resolved_at must be empty for pending approval")
		}
		if a.ConsumedAt != nil {
			return fmt.Errorf("consumed_at must be empty for pending approval")
		}
	} else {
		if err := requireString("resolver_id", a.ResolverID); err != nil {
			return err
		}
		if err := requireString("resolved_by", a.ResolvedBy); err != nil {
			return err
		}
		if err := requireString("resolution_reason", a.ResolutionReason); err != nil {
			return err
		}
		if a.ResolvedAt == nil {
			return fmt.Errorf("resolved_at is required")
		}
		if a.ConsumedAt != nil && a.Status != ApprovalStatusApproved {
			return fmt.Errorf("consumed_at is only valid for approved approvals")
		}
	}
	if err := validateLabels("labels", a.Labels); err != nil {
		return err
	}
	if err := validateMetadata("metadata", a.Metadata); err != nil {
		return err
	}
	return nil
}

// Validate checks an ArtifactPointer without dereferencing or loading artifacts.
func (a ArtifactPointer) Validate() error {
	if err := requireString("tenant_id", a.TenantID); err != nil {
		return err
	}
	if err := requireString("session_id", a.SessionID); err != nil {
		return err
	}
	if err := requireString("execution_id", a.ExecutionID); err != nil {
		return err
	}
	if err := requireString("event_id", a.EventID); err != nil {
		return err
	}
	if err := validateArtifactType(a.ArtifactType); err != nil {
		return err
	}
	if err := validateRetentionClass(a.RetentionClass); err != nil {
		return err
	}
	if err := validateRedactionLevel(a.RedactionLevel); err != nil {
		return err
	}
	if err := requireString("sha256", a.SHA256); err != nil {
		return err
	}
	if err := requireString("uri", a.URI); err != nil {
		return err
	}
	if err := requireTime("created_at", a.CreatedAt); err != nil {
		return err
	}
	return nil
}

func requireString(field, value string) error {
	if strings.TrimSpace(value) == "" {
		// EDGE-038: wrap with ErrValidation so gateway handlers can route via
		// errors.Is instead of substring-matching err.Error(). The wrapped
		// message is preserved by %w so existing tests asserting
		// strings.Contains(err.Error(), "is required") keep working.
		return fmt.Errorf("%w: %s is required", ErrValidation, field)
	}
	return nil
}

func requireTime(field string, value time.Time) error {
	if value.IsZero() {
		return fmt.Errorf("%w: %s is required", ErrValidation, field)
	}
	return nil
}

func validateOptionalEnd(field string, started time.Time, ended *time.Time) error {
	if ended == nil {
		return nil
	}
	if ended.IsZero() {
		return fmt.Errorf("%w: %s is required when set", ErrValidation, field)
	}
	if !started.IsZero() && ended.Before(started) {
		return fmt.Errorf("%w: %s must be >= started_at", ErrValidation, field)
	}
	return nil
}

func validatePrincipalType(value PrincipalType) error {
	switch value {
	case PrincipalTypeHuman, PrincipalTypeService, PrincipalTypeUnknown:
		return nil
	default:
		return fmt.Errorf("principal_type has unsafe value %q", value)
	}
}

func validateSessionMode(value SessionMode) error {
	switch value {
	case SessionModeLocalDev, SessionModeEnterpriseManaged, SessionModeWorkflow, SessionModeCI, SessionModeProdRunner:
		return nil
	default:
		return fmt.Errorf("mode has unsafe value %q", value)
	}
}

func validateExecutionMode(value ExecutionMode) error {
	switch value {
	case ExecutionModeLocalDev, ExecutionModeEnterpriseManaged, ExecutionModeWorkflow, ExecutionModeCI, ExecutionModeProdRunner:
		return nil
	default:
		return fmt.Errorf("mode has unsafe value %q", value)
	}
}

func validateAgentAdapter(value AgentAdapter) error {
	switch value {
	case AdapterClaudeCodeHook, AdapterMCPGateway, AdapterLLMProxy, AdapterRuntimeSidecar, AdapterSDKRunner:
		return nil
	default:
		return fmt.Errorf("adapter has unsafe value %q", value)
	}
}

func validatePolicyMode(value PolicyMode) error {
	switch value {
	case PolicyModeObserve, PolicyModeEnforce, PolicyModeEnterpriseStrict:
		return nil
	default:
		return fmt.Errorf("policy_mode has unsafe value %q", value)
	}
}

func validateSessionStatus(value SessionStatus) error {
	switch value {
	case SessionStatusStarting, SessionStatusRunning, SessionStatusWaitingForApproval, SessionStatusDegraded, SessionStatusEnded, SessionStatusFailed:
		return nil
	default:
		return fmt.Errorf("status has unsafe session value %q", value)
	}
}

func validateExecutionStatus(value ExecutionStatus) error {
	switch value {
	case ExecutionStatusRunning, ExecutionStatusWaitingForApproval, ExecutionStatusSucceeded, ExecutionStatusFailed, ExecutionStatusCancelled, ExecutionStatusTimeout, ExecutionStatusDegraded:
		return nil
	default:
		return fmt.Errorf("status has unsafe execution value %q", value)
	}
}

func validateRiskSummary(summary RiskSummary) error {
	if summary.DeniedCount < 0 {
		return fmt.Errorf("denied_count must be non-negative")
	}
	if summary.ApprovalCount < 0 {
		return fmt.Errorf("approval_count must be non-negative")
	}
	if summary.ArtifactCount < 0 {
		return fmt.Errorf("artifact_count must be non-negative")
	}
	switch summary.MaxRisk {
	case RiskLevelLow, RiskLevelMedium, RiskLevelHigh, RiskLevelCritical:
		return nil
	default:
		return fmt.Errorf("max_risk has unsafe value %q", summary.MaxRisk)
	}
}

func validateExecutionMetrics(metrics ExecutionMetrics) error {
	if metrics.Events < 0 {
		return fmt.Errorf("events must be non-negative")
	}
	if metrics.Allow < 0 {
		return fmt.Errorf("allow must be non-negative")
	}
	if metrics.Deny < 0 {
		return fmt.Errorf("deny must be non-negative")
	}
	if metrics.RequireApproval < 0 {
		return fmt.Errorf("require_approval must be non-negative")
	}
	if metrics.Artifacts < 0 {
		return fmt.Errorf("artifacts must be non-negative")
	}
	if math.IsNaN(metrics.LLMCostUSD) || math.IsInf(metrics.LLMCostUSD, 0) || metrics.LLMCostUSD < 0 {
		return fmt.Errorf("llm_cost_usd must be a finite non-negative number")
	}
	return nil
}

func validateLayer(value Layer) error {
	switch value {
	case LayerHook, LayerMCP, LayerLLM, LayerRuntime, LayerWorkflow, LayerSystem:
		return nil
	default:
		return fmt.Errorf("layer has unsafe value %q", value)
	}
}

func validateEdgeDecision(value EdgeDecision) error {
	switch value {
	case DecisionAllow, DecisionDeny, DecisionRequireApproval, DecisionThrottle, DecisionConstrain, DecisionRecorded:
		return nil
	default:
		return fmt.Errorf("decision has unsafe value %q", value)
	}
}

func validateRuleTier(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "global", "workflow", "job":
		return nil
	default:
		return fmt.Errorf("tier has unsafe value %q", value)
	}
}

func validateActionStatus(value ActionStatus) error {
	switch value {
	case ActionStatusOK, ActionStatusBlocked, ActionStatusFailed, ActionStatusDegraded:
		return nil
	default:
		return fmt.Errorf("status has unsafe action value %q", value)
	}
}

func validateApprovalStatus(value ApprovalStatus) error {
	switch value {
	case ApprovalStatusPending, ApprovalStatusApproved, ApprovalStatusRejected, ApprovalStatusExpired, ApprovalStatusInvalidated:
		return nil
	default:
		return fmt.Errorf("status has unsafe approval value %q", value)
	}
}

func validateApprovalDecision(value ApprovalDecision) error {
	switch value {
	case "", ApprovalDecisionApprove, ApprovalDecisionReject, ApprovalDecisionExpire, ApprovalDecisionInvalidate:
		return nil
	default:
		return fmt.Errorf("decision has unsafe approval value %q", value)
	}
}

func validateApprovalDecisionForStatus(status ApprovalStatus, decision ApprovalDecision) error {
	if err := validateApprovalDecision(decision); err != nil {
		return err
	}
	switch status {
	case ApprovalStatusPending:
		if decision != "" {
			return fmt.Errorf("decision must be empty for pending approval")
		}
	case ApprovalStatusApproved:
		if decision != ApprovalDecisionApprove {
			return fmt.Errorf("decision must be approve for approved approval")
		}
	case ApprovalStatusRejected:
		if decision != ApprovalDecisionReject {
			return fmt.Errorf("decision must be reject for rejected approval")
		}
	case ApprovalStatusExpired:
		if decision != ApprovalDecisionExpire {
			return fmt.Errorf("decision must be expire for expired approval")
		}
	case ApprovalStatusInvalidated:
		if decision != ApprovalDecisionInvalidate {
			return fmt.Errorf("decision must be invalidate for invalidated approval")
		}
	}
	return nil
}

func validateArtifactType(value ArtifactType) error {
	switch value {
	case ArtifactTypeTranscript,
		ArtifactTypeDiff,
		ArtifactTypeToolInput,
		ArtifactTypeToolResult,
		ArtifactTypeTestOutput,
		ArtifactTypeMCPRequest,
		ArtifactTypeMCPResponse,
		ArtifactTypeLLMPromptRedacted,
		ArtifactTypeLLMResponseRedacted,
		ArtifactTypeEvidenceBundle:
		return nil
	default:
		return fmt.Errorf("artifact_type has unsafe value %q", value)
	}
}

func validateRetentionClass(value RetentionClass) error {
	switch value {
	case RetentionClassShort, RetentionClassStandard, RetentionClassAudit:
		return nil
	default:
		return fmt.Errorf("retention_class has unsafe value %q", value)
	}
}

func validateRedactionLevel(value RedactionLevel) error {
	switch value {
	case RedactionLevelStandard, RedactionLevelStrict:
		return nil
	default:
		return fmt.Errorf("redaction_level has unsafe value %q", value)
	}
}

func validateLabels(field string, labels Labels) error {
	if len(labels) > MaxLabelEntries {
		return fmt.Errorf("%s has %d entries, max %d", field, len(labels), MaxLabelEntries)
	}
	for key, value := range labels {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("%s key is required", field)
		}
		if len(key) > MaxLabelKeyBytes {
			return fmt.Errorf("%s key %q exceeds %d bytes", field, key, MaxLabelKeyBytes)
		}
		if len(value) > MaxLabelValueBytes {
			return fmt.Errorf("%s value for %q exceeds %d bytes", field, key, MaxLabelValueBytes)
		}
	}
	return nil
}

func validateEnforcementLayers(field string, layers EnforcementLayers) error {
	if len(layers) > MaxEnforcementLayerEntries {
		return fmt.Errorf("%s has %d entries, max %d", field, len(layers), MaxEnforcementLayerEntries)
	}
	for key := range layers {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("%s key is required", field)
		}
		if len(key) > MaxLabelKeyBytes {
			return fmt.Errorf("%s key %q exceeds %d bytes", field, key, MaxLabelKeyBytes)
		}
	}
	return nil
}

func validateMetadata(field string, metadata Metadata) error {
	if len(metadata) > MaxMetadataEntries {
		return fmt.Errorf("%s has %d entries, max %d", field, len(metadata), MaxMetadataEntries)
	}
	for key, value := range metadata {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("%s key is required", field)
		}
		if len(key) > MaxLabelKeyBytes {
			return fmt.Errorf("%s key %q exceeds %d bytes", field, key, MaxLabelKeyBytes)
		}
		if len(value) > MaxLabelValueBytes {
			return fmt.Errorf("%s value for %q exceeds %d bytes", field, key, MaxLabelValueBytes)
		}
	}
	return validateJSONSize(field, metadata, MaxMetadataBytes)
}

func validateRedactedInput(field string, input map[string]any) error {
	return validateJSONSize(field, input, MaxInputRedactedBytes)
}

func validateJSONSize(field string, value any, maxBytes int) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("%s JSON encoding failed: %w", field, err)
	}
	if len(data) > maxBytes {
		return fmt.Errorf("%s JSON size %d exceeds max %d bytes", field, len(data), maxBytes)
	}
	return nil
}
