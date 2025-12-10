package v1

import (
	apiv1 "github.com/yaront1111/cortex-os/core/pkg/pb/v1/api/proto/v1"
)

type (
	BusPacket                       = apiv1.BusPacket
	BusPacket_JobRequest            = apiv1.BusPacket_JobRequest
	BusPacket_JobResult             = apiv1.BusPacket_JobResult
	BusPacket_Heartbeat             = apiv1.BusPacket_Heartbeat
	BusPacket_Alert                 = apiv1.BusPacket_Alert
	SystemAlert                     = apiv1.SystemAlert
	JobRequest                      = apiv1.JobRequest
	JobResult                       = apiv1.JobResult
	Heartbeat                       = apiv1.Heartbeat
	JobPriority                     = apiv1.JobPriority
	JobStatus                       = apiv1.JobStatus
	SubmitJobRequest                = apiv1.SubmitJobRequest
	SubmitJobResponse               = apiv1.SubmitJobResponse
	GetJobStatusRequest             = apiv1.GetJobStatusRequest
	GetJobStatusResponse            = apiv1.GetJobStatusResponse
	CortexApiClient                 = apiv1.CortexApiClient
	CortexApiServer                 = apiv1.CortexApiServer
	UnimplementedCortexApiServer    = apiv1.UnimplementedCortexApiServer
	PolicyCheckRequest              = apiv1.PolicyCheckRequest
	PolicyCheckResponse             = apiv1.PolicyCheckResponse
	DecisionType                    = apiv1.DecisionType
	SafetyKernelClient              = apiv1.SafetyKernelClient
	SafetyKernelServer              = apiv1.SafetyKernelServer
	UnimplementedSafetyKernelServer = apiv1.UnimplementedSafetyKernelServer
)

const (
	JobPriority_JOB_PRIORITY_UNSPECIFIED = apiv1.JobPriority_JOB_PRIORITY_UNSPECIFIED
	JobPriority_JOB_PRIORITY_BATCH       = apiv1.JobPriority_JOB_PRIORITY_BATCH
	JobPriority_JOB_PRIORITY_INTERACTIVE = apiv1.JobPriority_JOB_PRIORITY_INTERACTIVE
	JobPriority_JOB_PRIORITY_CRITICAL    = apiv1.JobPriority_JOB_PRIORITY_CRITICAL

	JobStatus_JOB_STATUS_UNSPECIFIED = apiv1.JobStatus_JOB_STATUS_UNSPECIFIED
	JobStatus_JOB_STATUS_COMPLETED   = apiv1.JobStatus_JOB_STATUS_COMPLETED
	JobStatus_JOB_STATUS_FAILED      = apiv1.JobStatus_JOB_STATUS_FAILED
	JobStatus_JOB_STATUS_TIMEOUT     = apiv1.JobStatus_JOB_STATUS_TIMEOUT
	JobStatus_JOB_STATUS_DENIED      = apiv1.JobStatus_JOB_STATUS_DENIED
	JobStatus_JOB_STATUS_CANCELLED   = apiv1.JobStatus_JOB_STATUS_CANCELLED
)

var (
	JobPriority_name  = apiv1.JobPriority_name
	JobPriority_value = apiv1.JobPriority_value
	JobStatus_name    = apiv1.JobStatus_name
	JobStatus_value   = apiv1.JobStatus_value

	File_api_proto_v1_packet_proto    = apiv1.File_api_proto_v1_packet_proto
	File_api_proto_v1_job_proto       = apiv1.File_api_proto_v1_job_proto
	File_api_proto_v1_heartbeat_proto = apiv1.File_api_proto_v1_heartbeat_proto
	File_api_proto_v1_api_proto       = apiv1.File_api_proto_v1_api_proto
	File_api_proto_v1_safety_proto    = apiv1.File_api_proto_v1_safety_proto
)

const (
	DecisionType_DECISION_TYPE_UNSPECIFIED = apiv1.DecisionType_DECISION_TYPE_UNSPECIFIED
	DecisionType_DECISION_TYPE_ALLOW       = apiv1.DecisionType_DECISION_TYPE_ALLOW
	DecisionType_DECISION_TYPE_DENY        = apiv1.DecisionType_DECISION_TYPE_DENY
)

var (
	RegisterCortexApiServer    = apiv1.RegisterCortexApiServer
	NewCortexApiClient         = apiv1.NewCortexApiClient
	RegisterSafetyKernelServer = apiv1.RegisterSafetyKernelServer
	NewSafetyKernelClient      = apiv1.NewSafetyKernelClient
)
