package edge

import "time"

// Layer identifies the governance surface that produced an action event.
type Layer string

const (
	LayerHook     Layer = "hook"
	LayerMCP      Layer = "mcp"
	LayerLLM      Layer = "llm"
	LayerRuntime  Layer = "runtime"
	LayerWorkflow Layer = "workflow"
	LayerSystem   Layer = "system"
)

// EventKind identifies the semantic event. Constants cover the current roadmap,
// but validation intentionally allows future non-empty kind values.
type EventKind string

const (
	EventKindSessionStarted   EventKind = "session.started"
	EventKindSessionHeartbeat EventKind = "session.heartbeat"
	EventKindSessionDegraded  EventKind = "session.degraded"
	EventKindSessionEnded     EventKind = "session.ended"

	EventKindExecutionStarted EventKind = "execution.started"
	EventKindExecutionEnded   EventKind = "execution.ended"

	EventKindHookUserPromptSubmit   EventKind = "hook.user_prompt_submit"
	EventKindHookPreToolUse         EventKind = "hook.pre_tool_use"
	EventKindHookPolicyDecision     EventKind = "hook.policy_decision"
	EventKindHookPermissionRequest  EventKind = "hook.permission_request"
	EventKindHookPostToolUse        EventKind = "hook.post_tool_use"
	EventKindHookPostToolUseFailure EventKind = "hook.post_tool_use_failure"
	EventKindHookConfigChange       EventKind = "hook.config_change"
	EventKindHookFileChanged        EventKind = "hook.file_changed"

	EventKindApprovalRequested EventKind = "approval.requested"
	EventKindApprovalGranted   EventKind = "approval.granted"
	EventKindApprovalRejected  EventKind = "approval.rejected"

	EventKindArtifactCreated EventKind = "artifact.created"
	EventKindPolicyDenied    EventKind = "policy.denied"
	EventKindPolicyDegraded  EventKind = "policy.degraded"
	EventKindTerminalLine    EventKind = "terminal.line"

	EventKindMCPToolPre            EventKind = "mcp.tool.pre"
	EventKindMCPToolPost           EventKind = "mcp.tool.post"
	EventKindMCPToolFailed         EventKind = "mcp.tool.failed"
	EventKindMCPServerConnected    EventKind = "mcp.server.connected"
	EventKindMCPServerFailed       EventKind = "mcp.server.failed"
	EventKindLLMRequestPre         EventKind = "llm.request.pre"
	EventKindLLMRequestPost        EventKind = "llm.request.post"
	EventKindLLMStreamChunk        EventKind = "llm.stream.chunk"
	EventKindLLMCostRecorded       EventKind = "llm.cost.recorded"
	EventKindLLMDataRedacted       EventKind = "llm.data.redacted"
	EventKindLLMPolicyDenied       EventKind = "llm.policy.denied"
	EventKindRuntimeProcessExec    EventKind = "runtime.process.exec"
	EventKindRuntimeFileRead       EventKind = "runtime.file.read"
	EventKindRuntimeFileWrite      EventKind = "runtime.file.write"
	EventKindRuntimeNetworkConnect EventKind = "runtime.network.connect"
	EventKindRuntimeDNSQuery       EventKind = "runtime.dns.query"
	EventKindShadowAgentDetected   EventKind = "shadow_agent.detected"
	EventKindShadowAgentResolved   EventKind = "shadow_agent.resolved"
)

// AgentActionEvent is the compliance/evidence unit for governed agent activity.
type AgentActionEvent struct {
	EventID          string            `json:"event_id"`
	SessionID        string            `json:"session_id"`
	ExecutionID      string            `json:"execution_id"`
	TenantID         string            `json:"tenant_id"`
	PrincipalID      string            `json:"principal_id"`
	Seq              int               `json:"seq"`
	Timestamp        time.Time         `json:"ts"`
	Layer            Layer             `json:"layer"`
	Kind             EventKind         `json:"kind"`
	AgentProduct     string            `json:"agent_product"`
	ToolName         string            `json:"tool_name"`
	ToolUseID        string            `json:"tool_use_id"`
	ActionName       string            `json:"action_name"`
	Capability       string            `json:"capability"`
	RiskTags         []string          `json:"risk_tags"`
	InputRedacted    map[string]any    `json:"input_redacted"`
	InputHash        string            `json:"input_hash"`
	Decision         EdgeDecision      `json:"decision"`
	DecisionReason   string            `json:"decision_reason"`
	RuleID           string            `json:"rule_id"`
	RuleTier         string            `json:"tier"`
	PolicySnapshot   string            `json:"policy_snapshot"`
	ApprovalRef      string            `json:"approval_ref"`
	ArtifactPointers []ArtifactPointer `json:"artifact_ptrs"`
	DurationMS       int               `json:"duration_ms"`
	Status           ActionStatus      `json:"status"`
	ErrorCode        string            `json:"error_code"`
	ErrorMessage     string            `json:"error_message"`
	Labels           Labels            `json:"labels"`
	// Constraints carries the structured `_constraints` map emitted by a
	// policy gate when its decision was ALLOW_WITH_CONSTRAINTS. Only set on
	// pre/post/failed events whose decision is `constrain`; omitempty keeps
	// the wire payload identical to legacy ALLOW events. The shape mirrors
	// the agentd evaluate response (core/edge/agentd EvaluateResponse) so
	// downstream consumers see one canonical constraint payload across the
	// hook and MCP surfaces.
	Constraints map[string]any `json:"constraints,omitempty"`
}
