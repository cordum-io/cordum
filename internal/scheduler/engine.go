package scheduler

import (
	"context"

	"github.com/yaront1111/cortex-os/core/internal/infrastructure/logging"
	pb "github.com/yaront1111/cortex-os/core/pkg/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	schedulerQueue    = "cortex-scheduler"
	defaultSenderID   = "cortex-scheduler"
	protocolVersionV1 = 1
)

// Engine wires together bus interactions, safety checks, and scheduling decisions.
type Engine struct {
	bus      Bus
	safety   SafetyChecker
	registry WorkerRegistry
	strategy SchedulingStrategy
	jobStore JobStore
	metrics  Metrics
}

func NewEngine(bus Bus, safety SafetyChecker, registry WorkerRegistry, strategy SchedulingStrategy, jobStore JobStore, metrics Metrics) *Engine {
	return &Engine{
		bus:      bus,
		safety:   safety,
		registry: registry,
		strategy: strategy,
		jobStore: jobStore,
		metrics:  metrics,
	}
}

// Start registers subscriptions for the scheduler.
func (e *Engine) Start() error {
	if err := e.bus.Subscribe("sys.heartbeat.>", schedulerQueue, e.HandlePacket); err != nil {
		return err
	}
	if err := e.bus.Subscribe("sys.job.submit", schedulerQueue, e.HandlePacket); err != nil {
		return err
	}
	if err := e.bus.Subscribe("sys.job.result", schedulerQueue, e.HandlePacket); err != nil {
		return err
	}
	return nil
}

// HandlePacket routes incoming bus packets to the appropriate handlers.
func (e *Engine) HandlePacket(packet *pb.BusPacket) {
	if packet == nil {
		return
	}

	switch payload := packet.Payload.(type) {
	case *pb.BusPacket_Heartbeat:
		hb := payload.Heartbeat
		if hb == nil {
			return
		}
		logging.Info("scheduler", "heartbeat received",
			"worker_id", hb.WorkerId,
			"type", hb.Type,
			"cpu", hb.CpuLoad,
			"gpu", hb.GpuUtilization,
			"active_jobs", hb.ActiveJobs,
			"pool", hb.Pool,
		)
		e.registry.UpdateHeartbeat(hb)
	case *pb.BusPacket_JobRequest:
		req := payload.JobRequest
		if req == nil {
			return
		}
		logging.Info("scheduler", "job request received",
			"job_id", req.JobId,
			"topic", req.Topic,
			"trace_id", packet.TraceId,
		)
		e.incJobsReceived(req.Topic)
		e.setJobState(req.JobId, JobStatePending)
		e.processJob(req, packet.TraceId)
	case *pb.BusPacket_JobResult:
		res := payload.JobResult
		if res == nil {
			return
		}
		logging.Info("scheduler", "job result received",
			"job_id", res.JobId,
			"status", res.Status.String(),
			"worker_id", res.WorkerId,
			"result_ptr", res.ResultPtr,
		)
		e.handleJobResult(res)
	default:
		// Unknown payloads are ignored for now.
	}
}

func (e *Engine) processJob(req *pb.JobRequest, traceID string) {
	if req == nil || req.JobId == "" || req.Topic == "" {
		logging.Error("scheduler", "invalid job request",
			"trace_id", traceID,
			"job_id", safeJobID(req),
			"topic", safeTopic(req),
		)
		return
	}

	decision, reason := e.safety.Check(req)
	if decision == SafetyDeny {
		logging.Info("safety", "job denied",
			"job_id", req.JobId,
			"topic", req.Topic,
			"reason", reason,
			"trace_id", traceID,
		)
		e.setJobState(req.JobId, JobStateDenied)
		e.incSafetyDenied(req.Topic)
		return
	}

	workers := e.registry.Snapshot()
	subject, err := e.strategy.PickSubject(req, workers)
	if err != nil {
		logging.Error("scheduler", "failed to pick subject",
			"job_id", req.JobId,
			"topic", req.Topic,
			"error", err,
		)
		return
	}

	logging.Info("scheduler", "dispatching job",
		"job_id", req.JobId,
		"trace_id", traceID,
		"subject", subject,
		"topic", req.Topic,
	)
	e.setJobState(req.JobId, JobStateRunning)
	e.incJobsDispatched(req.Topic)

	packet := &pb.BusPacket{
		TraceId:         traceID,
		SenderId:        defaultSenderID,
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: protocolVersionV1,
		Payload: &pb.BusPacket_JobRequest{
			JobRequest: req,
		},
	}

	if err := e.bus.Publish(subject, packet); err != nil {
		logging.Error("scheduler", "failed to publish job",
			"job_id", req.JobId,
			"subject", subject,
			"error", err,
		)
	}
}

func (e *Engine) handleJobResult(res *pb.JobResult) {
	if res == nil {
		return
	}
	state := JobStateCompleted
	if res.Status == pb.JobStatus_JOB_STATUS_FAILED {
		state = JobStateFailed
	}
	e.setJobState(res.JobId, state)
	if res.ResultPtr != "" {
		e.setResultPtr(res.JobId, res.ResultPtr)
	}
	e.incJobsCompleted(res.JobId, res.Status.String())
}

func (e *Engine) setJobState(jobID string, state JobState) {
	if e.jobStore == nil {
		return
	}
	_ = e.jobStore.SetState(context.Background(), jobID, state)
}

func (e *Engine) setResultPtr(jobID, ptr string) {
	if e.jobStore == nil {
		return
	}
	_ = e.jobStore.SetResultPtr(context.Background(), jobID, ptr)
}

func safeJobID(req *pb.JobRequest) string {
	if req == nil {
		return ""
	}
	return req.JobId
}

func safeTopic(req *pb.JobRequest) string {
	if req == nil {
		return ""
	}
	return req.Topic
}

func (e *Engine) incJobsReceived(topic string) {
	if e.metrics != nil {
		e.metrics.IncJobsReceived(topic)
	}
}

func (e *Engine) incJobsDispatched(topic string) {
	if e.metrics != nil {
		e.metrics.IncJobsDispatched(topic)
	}
}

func (e *Engine) incJobsCompleted(topic, status string) {
	if e.metrics != nil {
		e.metrics.IncJobsCompleted(topic, status)
	}
}

func (e *Engine) incSafetyDenied(topic string) {
	if e.metrics != nil {
		e.metrics.IncSafetyDenied(topic)
	}
}
