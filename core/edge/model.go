package edge

import "time"

const (
	// MaxLabelEntries bounds forward-compatible label maps on Edge records.
	MaxLabelEntries = 64
	// MaxLabelKeyBytes bounds each label key after UTF-8 encoding.
	MaxLabelKeyBytes = 128
	// MaxLabelValueBytes bounds each label value after UTF-8 encoding.
	MaxLabelValueBytes = 512
	// MaxEnforcementLayerEntries bounds the session enforcement layer map while
	// still allowing future layer names.
	MaxEnforcementLayerEntries = 16
	// MaxMetadataEntries bounds approval/event metadata maps.
	MaxMetadataEntries = 64
	// MaxMetadataBytes bounds serialized metadata maps.
	MaxMetadataBytes = 16 * 1024
	// MaxInputRedactedBytes bounds serialized redacted input snippets stored on events.
	MaxInputRedactedBytes = 64 * 1024
	// MaxArtifactPointersPerEvent bounds artifact references carried by one event.
	MaxArtifactPointersPerEvent = 32
)

// Labels carries small, forward-compatible tags for indexing and filtering.
type Labels map[string]string

// Metadata carries small forward-compatible metadata. Large or sensitive values
// must be stored as artifacts and referenced by ArtifactPointer instead.
type Metadata map[string]string

// EnforcementLayers records which governance layers were active for a session.
// Keys are intentionally open-ended so future layers can be preserved.
type EnforcementLayers map[string]bool

// PrincipalType identifies the principal that started an Edge session.
type PrincipalType string

const (
	PrincipalTypeHuman   PrincipalType = "human"
	PrincipalTypeService PrincipalType = "service"
	PrincipalTypeUnknown PrincipalType = "unknown"
)

// SessionMode describes where an Edge session is running.
type SessionMode string

const (
	SessionModeLocalDev          SessionMode = "local-dev"
	SessionModeEnterpriseManaged SessionMode = "enterprise-managed"
	SessionModeWorkflow          SessionMode = "workflow"
	SessionModeCI                SessionMode = "ci"
	SessionModeProdRunner        SessionMode = "prod-runner"
)

// ExecutionMode describes where an AgentExecution is running.
type ExecutionMode string

const (
	ExecutionModeLocalDev          ExecutionMode = "local-dev"
	ExecutionModeEnterpriseManaged ExecutionMode = "enterprise-managed"
	ExecutionModeWorkflow          ExecutionMode = "workflow"
	ExecutionModeCI                ExecutionMode = "ci"
	ExecutionModeProdRunner        ExecutionMode = "prod-runner"
)

// AgentAdapter identifies the generic capture/enforcement surface for an execution.
type AgentAdapter string

const (
	AdapterClaudeCodeHook AgentAdapter = "claude-code-hook"
	AdapterMCPGateway     AgentAdapter = "mcp-gateway"
	AdapterLLMProxy       AgentAdapter = "llm-proxy"
	AdapterRuntimeSidecar AgentAdapter = "runtime-sidecar"
	AdapterSDKRunner      AgentAdapter = "sdk-runner"
)

// PolicyMode identifies how policy decisions are enforced for a session.
type PolicyMode string

const (
	PolicyModeObserve          PolicyMode = "observe"
	PolicyModeEnforce          PolicyMode = "enforce"
	PolicyModeEnterpriseStrict PolicyMode = "enterprise-strict"
)

// SessionStatus captures the EdgeSession lifecycle.
type SessionStatus string

const (
	SessionStatusStarting           SessionStatus = "starting"
	SessionStatusRunning            SessionStatus = "running"
	SessionStatusWaitingForApproval SessionStatus = "waiting_for_approval"
	SessionStatusDegraded           SessionStatus = "degraded"
	SessionStatusEnded              SessionStatus = "ended"
	SessionStatusFailed             SessionStatus = "failed"
)

// ExecutionStatus captures an AgentExecution lifecycle without replacing Job state.
type ExecutionStatus string

const (
	ExecutionStatusRunning            ExecutionStatus = "running"
	ExecutionStatusWaitingForApproval ExecutionStatus = "waiting_for_approval"
	ExecutionStatusSucceeded          ExecutionStatus = "succeeded"
	ExecutionStatusFailed             ExecutionStatus = "failed"
	ExecutionStatusCancelled          ExecutionStatus = "cancelled"
	ExecutionStatusTimeout            ExecutionStatus = "timeout"
	ExecutionStatusDegraded           ExecutionStatus = "degraded"
)

// RiskLevel is the maximum risk observed on a session.
type RiskLevel string

const (
	RiskLevelLow      RiskLevel = "low"
	RiskLevelMedium   RiskLevel = "medium"
	RiskLevelHigh     RiskLevel = "high"
	RiskLevelCritical RiskLevel = "critical"
)

// ArtifactType identifies redacted evidence stored outside large event records.
type ArtifactType string

const (
	ArtifactTypeTranscript          ArtifactType = "edge.transcript"
	ArtifactTypeDiff                ArtifactType = "edge.diff"
	ArtifactTypeToolInput           ArtifactType = "edge.tool_input"
	ArtifactTypeToolResult          ArtifactType = "edge.tool_result"
	ArtifactTypeTestOutput          ArtifactType = "edge.test_output"
	ArtifactTypeMCPRequest          ArtifactType = "edge.mcp_request"
	ArtifactTypeMCPResponse         ArtifactType = "edge.mcp_response"
	ArtifactTypeLLMPromptRedacted   ArtifactType = "edge.llm_prompt_redacted"
	ArtifactTypeLLMResponseRedacted ArtifactType = "edge.llm_response_redacted"
	ArtifactTypeEvidenceBundle      ArtifactType = "edge.evidence_bundle"
)

// AllArtifactTypes is the full P0 evidence catalog. Tests and the export
// bundle helper iterate this list rather than hard-coding the set; adding a
// new type means appending here, extending the schema enum, and the
// validateArtifactType switch — those three must stay in sync.
var AllArtifactTypes = []ArtifactType{
	ArtifactTypeTranscript,
	ArtifactTypeDiff,
	ArtifactTypeToolInput,
	ArtifactTypeToolResult,
	ArtifactTypeTestOutput,
	ArtifactTypeMCPRequest,
	ArtifactTypeMCPResponse,
	ArtifactTypeLLMPromptRedacted,
	ArtifactTypeLLMResponseRedacted,
	ArtifactTypeEvidenceBundle,
}

// RetentionClass identifies artifact/event retention posture.
type RetentionClass string

const (
	RetentionClassShort    RetentionClass = "short"
	RetentionClassStandard RetentionClass = "standard"
	RetentionClassAudit    RetentionClass = "audit"
)

// RedactionLevel identifies how aggressively an artifact has been redacted.
type RedactionLevel string

const (
	RedactionLevelStandard RedactionLevel = "standard"
	RedactionLevelStrict   RedactionLevel = "strict"
)

// RiskSummary summarizes governance-relevant counters for a session.
type RiskSummary struct {
	DeniedCount   int       `json:"denied_count"`
	ApprovalCount int       `json:"approval_count"`
	ArtifactCount int       `json:"artifact_count"`
	MaxRisk       RiskLevel `json:"max_risk"`
}

// ExecutionMetrics summarizes events observed during an AgentExecution.
type ExecutionMetrics struct {
	Events          int     `json:"events"`
	Allow           int     `json:"allow"`
	Deny            int     `json:"deny"`
	RequireApproval int     `json:"require_approval"`
	Artifacts       int     `json:"artifacts"`
	LLMCostUSD      float64 `json:"llm_cost_usd"`
}

// EdgeSession is the top-level governed interaction with an agent.
type EdgeSession struct {
	SessionID     string        `json:"session_id"`
	TenantID      string        `json:"tenant_id"`
	PrincipalID   string        `json:"principal_id"`
	PrincipalType PrincipalType `json:"principal_type"`
	AgentProduct  string        `json:"agent_product"`
	AgentVersion  string        `json:"agent_version"`
	// AgentName / PrincipalDisplayName are OPTIONAL, already-sanitized human
	// display labels (task-c8d4b056) — evidence labels only, never an auth
	// authority; principal_id/type stay server-derived. omitempty keeps the
	// wire shape unchanged for sessions created without explicit labels.
	AgentName            string            `json:"agent_name,omitempty"`
	PrincipalDisplayName string            `json:"principal_display_name,omitempty"`
	Mode                 SessionMode       `json:"mode"`
	Repo                 string            `json:"repo"`
	GitRemote            string            `json:"git_remote"`
	GitBranch            string            `json:"git_branch"`
	GitSHA               string            `json:"git_sha"`
	CWD                  string            `json:"cwd"`
	HostID               string            `json:"host_id"`
	DeviceID             string            `json:"device_id"`
	TraceID              string            `json:"trace_id"`
	WorkflowRunID        string            `json:"workflow_run_id"`
	JobID                string            `json:"job_id"`
	PolicySnapshot       string            `json:"policy_snapshot"`
	EnforcementLayers    EnforcementLayers `json:"enforcement_layers"`
	PolicyMode           PolicyMode        `json:"policy_mode"`
	Status               SessionStatus     `json:"status"`
	RiskSummary          RiskSummary       `json:"risk_summary"`
	StartedAt            time.Time         `json:"started_at"`
	EndedAt              *time.Time        `json:"ended_at"`
	Labels               Labels            `json:"labels"`
}

// AgentExecution is evidence/runtime metadata for a concrete governed agent run.
// It may link to job_id/workflow_run_id when real production work exists, but it
// does not replace Scheduler Job lifecycle state.
type AgentExecution struct {
	ExecutionID    string           `json:"execution_id"`
	SessionID      string           `json:"session_id"`
	TenantID       string           `json:"tenant_id"`
	Adapter        AgentAdapter     `json:"adapter"`
	Mode           ExecutionMode    `json:"mode"`
	WorkflowRunID  string           `json:"workflow_run_id"`
	StepID         string           `json:"step_id"`
	JobID          string           `json:"job_id"`
	Attempt        int              `json:"attempt"`
	TraceID        string           `json:"trace_id"`
	WorkerID       string           `json:"worker_id"`
	PolicySnapshot string           `json:"policy_snapshot"`
	Status         ExecutionStatus  `json:"status"`
	StartedAt      time.Time        `json:"started_at"`
	EndedAt        *time.Time       `json:"ended_at"`
	Metrics        ExecutionMetrics `json:"metrics"`
	Labels         Labels           `json:"labels"`
}

// ArtifactPointer references redacted evidence stored outside action events.
type ArtifactPointer struct {
	ArtifactType   ArtifactType   `json:"artifact_type"`
	SessionID      string         `json:"session_id"`
	ExecutionID    string         `json:"execution_id"`
	EventID        string         `json:"event_id"`
	TenantID       string         `json:"tenant_id"`
	RetentionClass RetentionClass `json:"retention_class"`
	RedactionLevel RedactionLevel `json:"redaction_level"`
	SHA256         string         `json:"sha256"`
	URI            string         `json:"uri"`
	CreatedAt      time.Time      `json:"created_at"`
}
