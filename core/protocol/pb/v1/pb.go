package v1

import agentv1 "github.com/coretexos/cap/v2/coretex/agent/v1"

type (
	// CAP bus and safety types.
	BusPacket                       = agentv1.BusPacket
	BusPacket_JobRequest            = agentv1.BusPacket_JobRequest
	BusPacket_JobResult             = agentv1.BusPacket_JobResult
	BusPacket_Heartbeat             = agentv1.BusPacket_Heartbeat
	BusPacket_Alert                 = agentv1.BusPacket_Alert
	BusPacket_JobProgress           = agentv1.BusPacket_JobProgress
	BusPacket_JobCancel             = agentv1.BusPacket_JobCancel
	SystemAlert                     = agentv1.SystemAlert
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
	ActorType                       = agentv1.ActorType
	DecisionType                    = agentv1.DecisionType
	PolicyCheckRequest              = agentv1.PolicyCheckRequest
	PolicyCheckResponse             = agentv1.PolicyCheckResponse
	PolicyConstraints               = agentv1.PolicyConstraints
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
	JobStatus_JOB_STATUS_COMPLETED   = agentv1.JobStatus_JOB_STATUS_SUCCEEDED
	JobStatus_JOB_STATUS_UNSPECIFIED = agentv1.JobStatus_JOB_STATUS_UNSPECIFIED
	JobStatus_JOB_STATUS_PENDING     = agentv1.JobStatus_JOB_STATUS_PENDING
	JobStatus_JOB_STATUS_SCHEDULED   = agentv1.JobStatus_JOB_STATUS_SCHEDULED
	JobStatus_JOB_STATUS_DISPATCHED  = agentv1.JobStatus_JOB_STATUS_DISPATCHED
	JobStatus_JOB_STATUS_RUNNING     = agentv1.JobStatus_JOB_STATUS_RUNNING
	JobStatus_JOB_STATUS_SUCCEEDED   = agentv1.JobStatus_JOB_STATUS_SUCCEEDED
	JobStatus_JOB_STATUS_FAILED      = agentv1.JobStatus_JOB_STATUS_FAILED
	JobStatus_JOB_STATUS_CANCELLED   = agentv1.JobStatus_JOB_STATUS_CANCELLED
	JobStatus_JOB_STATUS_DENIED      = agentv1.JobStatus_JOB_STATUS_DENIED
	JobStatus_JOB_STATUS_TIMEOUT     = agentv1.JobStatus_JOB_STATUS_TIMEOUT

	DecisionType_DECISION_TYPE_UNSPECIFIED   = agentv1.DecisionType_DECISION_TYPE_UNSPECIFIED
	DecisionType_DECISION_TYPE_ALLOW         = agentv1.DecisionType_DECISION_TYPE_ALLOW
	DecisionType_DECISION_TYPE_DENY          = agentv1.DecisionType_DECISION_TYPE_DENY
	DecisionType_DECISION_TYPE_REQUIRE_HUMAN = agentv1.DecisionType_DECISION_TYPE_REQUIRE_HUMAN
	DecisionType_DECISION_TYPE_THROTTLE      = agentv1.DecisionType_DECISION_TYPE_THROTTLE
	DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS = agentv1.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS

	ActorType_ACTOR_TYPE_UNSPECIFIED = agentv1.ActorType_ACTOR_TYPE_UNSPECIFIED
	ActorType_ACTOR_TYPE_HUMAN       = agentv1.ActorType_ACTOR_TYPE_HUMAN
	ActorType_ACTOR_TYPE_SERVICE     = agentv1.ActorType_ACTOR_TYPE_SERVICE
)

var (
	RegisterSafetyKernelServer = agentv1.RegisterSafetyKernelServer
	NewSafetyKernelClient      = agentv1.NewSafetyKernelClient
)
