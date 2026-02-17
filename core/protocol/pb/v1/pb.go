package v1

import agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"

type (
	// CAP bus and safety types.
	BusPacket                       = agentv1.BusPacket
	BusPacket_JobRequest            = agentv1.BusPacket_JobRequest
	BusPacket_JobResult             = agentv1.BusPacket_JobResult
	BusPacket_Heartbeat             = agentv1.BusPacket_Heartbeat
	BusPacket_Alert                 = agentv1.BusPacket_Alert
	BusPacket_JobProgress           = agentv1.BusPacket_JobProgress
	BusPacket_JobCancel             = agentv1.BusPacket_JobCancel
	BusPacket_Handshake             = agentv1.BusPacket_Handshake
	SystemAlert                     = agentv1.SystemAlert
	Handshake                       = agentv1.Handshake
	ComponentRole                   = agentv1.ComponentRole
	ErrorCode                       = agentv1.ErrorCode
	AlertSeverity                   = agentv1.AlertSeverity
	JobRequest                      = agentv1.JobRequest
	JobResult                       = agentv1.JobResult
	JobProgress                     = agentv1.JobProgress
	JobCancel                       = agentv1.JobCancel
	Heartbeat                       = agentv1.Heartbeat
	ContextHints                    = agentv1.ContextHints
	Budget                          = agentv1.Budget
	JobMetadata                     = agentv1.JobMetadata
	JobPriority                     = agentv1.JobPriority
	JobStatus                       = agentv1.JobStatus
	Compensation                    = agentv1.Compensation
	ActorType                       = agentv1.ActorType
	DecisionType                    = agentv1.DecisionType
	PolicyCheckRequest              = agentv1.PolicyCheckRequest
	PolicyCheckResponse             = agentv1.PolicyCheckResponse
	PolicyConstraints               = agentv1.PolicyConstraints
	PolicyRemediation               = agentv1.PolicyRemediation
	BudgetConstraints               = agentv1.BudgetConstraints
	SandboxProfile                  = agentv1.SandboxProfile
	ToolchainConstraints            = agentv1.ToolchainConstraints
	DiffConstraints                 = agentv1.DiffConstraints
	ListSnapshotsRequest            = agentv1.ListSnapshotsRequest
	ListSnapshotsResponse           = agentv1.ListSnapshotsResponse
	SafetyKernelClient              = agentv1.SafetyKernelClient
	SafetyKernelServer              = agentv1.SafetyKernelServer
	UnimplementedSafetyKernelServer = agentv1.UnimplementedSafetyKernelServer
)

const (
	JobPriority_JOB_PRIORITY_UNSPECIFIED = agentv1.JobPriority_JOB_PRIORITY_UNSPECIFIED
	JobPriority_JOB_PRIORITY_INTERACTIVE = agentv1.JobPriority_JOB_PRIORITY_INTERACTIVE
	JobPriority_JOB_PRIORITY_BATCH       = agentv1.JobPriority_JOB_PRIORITY_BATCH
	JobPriority_JOB_PRIORITY_CRITICAL    = agentv1.JobPriority_JOB_PRIORITY_CRITICAL

	// Alias for backwards compatibility with older API naming.
	JobStatus_JOB_STATUS_COMPLETED        = agentv1.JobStatus_JOB_STATUS_SUCCEEDED
	JobStatus_JOB_STATUS_UNSPECIFIED      = agentv1.JobStatus_JOB_STATUS_UNSPECIFIED
	JobStatus_JOB_STATUS_PENDING          = agentv1.JobStatus_JOB_STATUS_PENDING
	JobStatus_JOB_STATUS_SCHEDULED        = agentv1.JobStatus_JOB_STATUS_SCHEDULED
	JobStatus_JOB_STATUS_DISPATCHED       = agentv1.JobStatus_JOB_STATUS_DISPATCHED
	JobStatus_JOB_STATUS_RUNNING          = agentv1.JobStatus_JOB_STATUS_RUNNING
	JobStatus_JOB_STATUS_SUCCEEDED        = agentv1.JobStatus_JOB_STATUS_SUCCEEDED
	JobStatus_JOB_STATUS_FAILED           = agentv1.JobStatus_JOB_STATUS_FAILED
	JobStatus_JOB_STATUS_CANCELLED        = agentv1.JobStatus_JOB_STATUS_CANCELLED
	JobStatus_JOB_STATUS_DENIED           = agentv1.JobStatus_JOB_STATUS_DENIED
	JobStatus_JOB_STATUS_TIMEOUT          = agentv1.JobStatus_JOB_STATUS_TIMEOUT
	JobStatus_JOB_STATUS_FAILED_RETRYABLE = agentv1.JobStatus_JOB_STATUS_FAILED_RETRYABLE
	JobStatus_JOB_STATUS_FAILED_FATAL     = agentv1.JobStatus_JOB_STATUS_FAILED_FATAL

	DecisionType_DECISION_TYPE_UNSPECIFIED            = agentv1.DecisionType_DECISION_TYPE_UNSPECIFIED
	DecisionType_DECISION_TYPE_ALLOW                  = agentv1.DecisionType_DECISION_TYPE_ALLOW
	DecisionType_DECISION_TYPE_DENY                   = agentv1.DecisionType_DECISION_TYPE_DENY
	DecisionType_DECISION_TYPE_REQUIRE_HUMAN          = agentv1.DecisionType_DECISION_TYPE_REQUIRE_HUMAN
	DecisionType_DECISION_TYPE_THROTTLE               = agentv1.DecisionType_DECISION_TYPE_THROTTLE
	DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS = agentv1.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS

	ActorType_ACTOR_TYPE_UNSPECIFIED = agentv1.ActorType_ACTOR_TYPE_UNSPECIFIED
	ActorType_ACTOR_TYPE_HUMAN       = agentv1.ActorType_ACTOR_TYPE_HUMAN
	ActorType_ACTOR_TYPE_SERVICE     = agentv1.ActorType_ACTOR_TYPE_SERVICE

	// ErrorCode enum values — protocol errors (100-199)
	ErrorCode_ERROR_CODE_UNSPECIFIED              = agentv1.ErrorCode_ERROR_CODE_UNSPECIFIED
	ErrorCode_ERROR_CODE_PROTOCOL_VERSION_MISMATCH = agentv1.ErrorCode_ERROR_CODE_PROTOCOL_VERSION_MISMATCH
	ErrorCode_ERROR_CODE_PROTOCOL_MALFORMED_PACKET = agentv1.ErrorCode_ERROR_CODE_PROTOCOL_MALFORMED_PACKET
	ErrorCode_ERROR_CODE_PROTOCOL_UNKNOWN_PAYLOAD  = agentv1.ErrorCode_ERROR_CODE_PROTOCOL_UNKNOWN_PAYLOAD
	ErrorCode_ERROR_CODE_PROTOCOL_SIGNATURE_INVALID = agentv1.ErrorCode_ERROR_CODE_PROTOCOL_SIGNATURE_INVALID
	ErrorCode_ERROR_CODE_PROTOCOL_SIGNATURE_MISSING = agentv1.ErrorCode_ERROR_CODE_PROTOCOL_SIGNATURE_MISSING
	// ErrorCode — job errors (200-299)
	ErrorCode_ERROR_CODE_JOB_TIMEOUT            = agentv1.ErrorCode_ERROR_CODE_JOB_TIMEOUT
	ErrorCode_ERROR_CODE_JOB_RESOURCE_EXHAUSTED = agentv1.ErrorCode_ERROR_CODE_JOB_RESOURCE_EXHAUSTED
	ErrorCode_ERROR_CODE_JOB_PERMISSION_DENIED  = agentv1.ErrorCode_ERROR_CODE_JOB_PERMISSION_DENIED
	ErrorCode_ERROR_CODE_JOB_INVALID_INPUT      = agentv1.ErrorCode_ERROR_CODE_JOB_INVALID_INPUT
	ErrorCode_ERROR_CODE_JOB_NOT_FOUND          = agentv1.ErrorCode_ERROR_CODE_JOB_NOT_FOUND
	ErrorCode_ERROR_CODE_JOB_DUPLICATE          = agentv1.ErrorCode_ERROR_CODE_JOB_DUPLICATE
	ErrorCode_ERROR_CODE_JOB_WORKER_UNAVAILABLE = agentv1.ErrorCode_ERROR_CODE_JOB_WORKER_UNAVAILABLE
	// ErrorCode — safety errors (300-399)
	ErrorCode_ERROR_CODE_SAFETY_DENIED           = agentv1.ErrorCode_ERROR_CODE_SAFETY_DENIED
	ErrorCode_ERROR_CODE_SAFETY_POLICY_VIOLATION = agentv1.ErrorCode_ERROR_CODE_SAFETY_POLICY_VIOLATION
	ErrorCode_ERROR_CODE_SAFETY_RISK_TAG_BLOCKED = agentv1.ErrorCode_ERROR_CODE_SAFETY_RISK_TAG_BLOCKED
	// ErrorCode — transport errors (400-499)
	ErrorCode_ERROR_CODE_TRANSPORT_PUBLISH_FAILED   = agentv1.ErrorCode_ERROR_CODE_TRANSPORT_PUBLISH_FAILED
	ErrorCode_ERROR_CODE_TRANSPORT_SUBSCRIBE_FAILED = agentv1.ErrorCode_ERROR_CODE_TRANSPORT_SUBSCRIBE_FAILED
	ErrorCode_ERROR_CODE_TRANSPORT_CONNECTION_LOST  = agentv1.ErrorCode_ERROR_CODE_TRANSPORT_CONNECTION_LOST

	// AlertSeverity enum values
	AlertSeverity_ALERT_SEVERITY_UNSPECIFIED = agentv1.AlertSeverity_ALERT_SEVERITY_UNSPECIFIED
	AlertSeverity_ALERT_SEVERITY_INFO        = agentv1.AlertSeverity_ALERT_SEVERITY_INFO
	AlertSeverity_ALERT_SEVERITY_WARNING     = agentv1.AlertSeverity_ALERT_SEVERITY_WARNING
	AlertSeverity_ALERT_SEVERITY_ERROR       = agentv1.AlertSeverity_ALERT_SEVERITY_ERROR
	AlertSeverity_ALERT_SEVERITY_CRITICAL    = agentv1.AlertSeverity_ALERT_SEVERITY_CRITICAL

	// ComponentRole enum values
	ComponentRole_COMPONENT_ROLE_UNSPECIFIED  = agentv1.ComponentRole_COMPONENT_ROLE_UNSPECIFIED
	ComponentRole_COMPONENT_ROLE_GATEWAY      = agentv1.ComponentRole_COMPONENT_ROLE_GATEWAY
	ComponentRole_COMPONENT_ROLE_SCHEDULER    = agentv1.ComponentRole_COMPONENT_ROLE_SCHEDULER
	ComponentRole_COMPONENT_ROLE_WORKER       = agentv1.ComponentRole_COMPONENT_ROLE_WORKER
	ComponentRole_COMPONENT_ROLE_ORCHESTRATOR = agentv1.ComponentRole_COMPONENT_ROLE_ORCHESTRATOR
	ComponentRole_COMPONENT_ROLE_CONTROLLER   = agentv1.ComponentRole_COMPONENT_ROLE_CONTROLLER
)

var (
	RegisterSafetyKernelServer = agentv1.RegisterSafetyKernelServer
	NewSafetyKernelClient      = agentv1.NewSafetyKernelClient
)
