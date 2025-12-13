package v1

import (
	agentv1 "github.com/coretexos/cap/v2/go/coretex/agent/v1"
	apiv1 "github.com/yaront1111/coretex-os/core/protocol/pb/v1/api/proto/v1"
)

type (
	// CAP bus and safety types.
	BusPacket                       = agentv1.BusPacket
	BusPacket_JobRequest            = agentv1.BusPacket_JobRequest
	BusPacket_JobResult             = agentv1.BusPacket_JobResult
	BusPacket_Heartbeat             = agentv1.BusPacket_Heartbeat
	BusPacket_Alert                 = agentv1.BusPacket_Alert
	SystemAlert                     = agentv1.SystemAlert
	JobRequest                      = agentv1.JobRequest
	JobResult                       = agentv1.JobResult
	Heartbeat                       = agentv1.Heartbeat
	ContextHints                    = agentv1.ContextHints
	Budget                          = agentv1.Budget
	JobPriority                     = agentv1.JobPriority
	JobStatus                       = agentv1.JobStatus
	DecisionType                    = agentv1.DecisionType
	PolicyCheckRequest              = agentv1.PolicyCheckRequest
	PolicyCheckResponse             = agentv1.PolicyCheckResponse
	SafetyKernelClient              = agentv1.SafetyKernelClient
	SafetyKernelServer              = agentv1.SafetyKernelServer
	UnimplementedSafetyKernelServer = agentv1.UnimplementedSafetyKernelServer

	// Local API/context types remain under the same pb package for now.
	ModelMessage                     = apiv1.ModelMessage
	BuildWindowRequest               = apiv1.BuildWindowRequest
	BuildWindowResponse              = apiv1.BuildWindowResponse
	UpdateMemoryRequest              = apiv1.UpdateMemoryRequest
	UpdateMemoryResponse             = apiv1.UpdateMemoryResponse
	IngestRepoRequest                = apiv1.IngestRepoRequest
	IngestRepoResponse               = apiv1.IngestRepoResponse
	SubmitJobRequest                 = apiv1.SubmitJobRequest
	SubmitJobResponse                = apiv1.SubmitJobResponse
	GetJobStatusRequest              = apiv1.GetJobStatusRequest
	GetJobStatusResponse             = apiv1.GetJobStatusResponse
	CoretexApiClient                 = apiv1.CoretexApiClient
	CoretexApiServer                 = apiv1.CoretexApiServer
	UnimplementedCoretexApiServer    = apiv1.UnimplementedCoretexApiServer
	ContextMode                      = apiv1.ContextMode
	ContextEngineClient              = apiv1.ContextEngineClient
	ContextEngineServer              = apiv1.ContextEngineServer
	UnimplementedContextEngineServer = apiv1.UnimplementedContextEngineServer
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

	ContextMode_CONTEXT_MODE_UNSPECIFIED = apiv1.ContextMode_CONTEXT_MODE_UNSPECIFIED
	ContextMode_CONTEXT_MODE_RAW         = apiv1.ContextMode_CONTEXT_MODE_RAW
	ContextMode_CONTEXT_MODE_CHAT        = apiv1.ContextMode_CONTEXT_MODE_CHAT
	ContextMode_CONTEXT_MODE_RAG         = apiv1.ContextMode_CONTEXT_MODE_RAG
)

var (
	RegisterSafetyKernelServer  = agentv1.RegisterSafetyKernelServer
	NewSafetyKernelClient       = agentv1.NewSafetyKernelClient
	RegisterCoretexApiServer    = apiv1.RegisterCoretexApiServer
	NewCoretexApiClient         = apiv1.NewCoretexApiClient
	RegisterContextEngineServer = apiv1.RegisterContextEngineServer
	NewContextEngineClient      = apiv1.NewContextEngineClient
)
