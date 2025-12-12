package scheduler

import (
	"context"
	"time"

	"github.com/yaront1111/coretex-os/core/infra/logging"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	schedulerQueue    = "coretex-scheduler"
	defaultSenderID   = "coretex-scheduler"
	protocolVersionV1 = 1
	storeOpTimeout    = 2 * time.Second
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
	if err := e.bus.Subscribe("sys.heartbeat", schedulerQueue, e.HandlePacket); err != nil {
		return err
	}
	if err := e.bus.Subscribe("sys.job.submit", schedulerQueue, e.HandlePacket); err != nil {
		return err
	}
	if err := e.bus.Subscribe("sys.job.result", "coretex-scheduler", e.HandlePacket); err != nil {
		return err
	}
	return nil
}

func (e *Engine) HandlePacket(p *pb.BusPacket) {
	if p == nil {
		return
	}

	switch payload := p.Payload.(type) {
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
		tenant := req.GetTenantId()
		if tenant == "" {
			tenant = req.GetEnv()["tenant_id"]
		}
		logging.Info("scheduler", "job request received",
			"job_id", req.JobId,
			"topic", req.Topic,
			"trace_id", p.TraceId,
			"tenant", tenant,
		)
		e.incJobsReceived(req.Topic)
		e.setJobState(req.JobId, JobStatePending)
		if e.jobStore != nil {
			ctx, cancel := context.WithTimeout(context.Background(), storeOpTimeout)
			if err := e.jobStore.AddJobToTrace(ctx, p.TraceId, req.JobId); err != nil {
				logging.Error("scheduler", "failed to add job to trace", "job_id", req.JobId, "trace_id", p.TraceId, "error", err)
			}
			if err := e.jobStore.SetJobMeta(ctx, req); err != nil {
				logging.Error("scheduler", "failed to persist job metadata", "job_id", req.JobId, "error", err)
			}
			cancel()
		}
		e.processJob(req, p.TraceId)

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
		e.setJobState(safeJobID(req), JobStateFailed)
		e.incJobsCompleted(safeTopic(req), pb.JobStatus_JOB_STATUS_FAILED.String())
		return
	}

	decision, reason := e.safety.Check(req)
	if e.jobStore != nil {
		ctx, cancel := context.WithTimeout(context.Background(), storeOpTimeout)
		if err := e.jobStore.SetSafetyDecision(ctx, req.JobId, safetyDecisionString(decision), reason); err != nil {
			logging.Error("scheduler", "failed to persist safety decision", "job_id", req.JobId, "error", err)
		}
		cancel()
	}
	if decision != SafetyAllow {
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
		e.setJobState(req.JobId, JobStateFailed)
		e.incJobsCompleted(req.Topic, pb.JobStatus_JOB_STATUS_FAILED.String())
		return
	}

	logging.Info("scheduler", "dispatching job",
		"job_id", req.JobId,
		"trace_id", traceID,
		"subject", subject,
		"topic", req.Topic,
	)

	if budget := req.GetBudget(); budget != nil && budget.GetDeadlineMs() > 0 && e.jobStore != nil {
		ctx, cancel := context.WithTimeout(context.Background(), storeOpTimeout)
		if err := e.jobStore.SetDeadline(ctx, req.JobId, time.Now().Add(time.Duration(budget.GetDeadlineMs())*time.Millisecond)); err != nil {
			logging.Error("scheduler", "failed to persist deadline", "job_id", req.JobId, "error", err)
		}
		cancel()
	}

	e.setJobState(req.JobId, JobStateScheduled)
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
		e.setJobState(req.JobId, JobStateFailed)
		e.incJobsCompleted(req.Topic, pb.JobStatus_JOB_STATUS_FAILED.String())
		return
	}
	e.setJobState(req.JobId, JobStateDispatched)
	e.setJobState(req.JobId, JobStateRunning)
}

func (e *Engine) handleJobResult(res *pb.JobResult) {
	if res == nil {
		return
	}
	var topic string
	if e.jobStore != nil {
		topic, _ = e.jobStore.GetTopic(context.Background(), res.JobId)
	}
	if topic == "" {
		topic = "unknown"
	}
	state := JobStateSucceeded
	switch res.Status {
	case pb.JobStatus_JOB_STATUS_FAILED:
		state = JobStateFailed
	case pb.JobStatus_JOB_STATUS_TIMEOUT:
		state = JobStateTimeout
	case pb.JobStatus_JOB_STATUS_DENIED:
		state = JobStateDenied
	case pb.JobStatus_JOB_STATUS_CANCELLED:
		state = JobStateCancelled
	}
	e.setJobState(res.JobId, state)
	if res.ResultPtr != "" {
		e.setResultPtr(res.JobId, res.ResultPtr)
	}
	e.incJobsCompleted(topic, res.Status.String())
}

func (e *Engine) setJobState(jobID string, state JobState) {
	if e.jobStore == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), storeOpTimeout)
	defer cancel()
	if err := e.jobStore.SetState(ctx, jobID, state); err != nil {
		logging.Error("scheduler", "failed to set job state", "job_id", jobID, "state", state, "error", err)
	}
}

func (e *Engine) setResultPtr(jobID, ptr string) {
	if e.jobStore == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), storeOpTimeout)
	defer cancel()
	if err := e.jobStore.SetResultPtr(ctx, jobID, ptr); err != nil {
		logging.Error("scheduler", "failed to persist result ptr", "job_id", jobID, "error", err)
	}
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

func safetyDecisionString(dec SafetyDecision) string {
	switch dec {
	case SafetyAllow:
		return "ALLOW"
	case SafetyDeny:
		return "DENY"
	case SafetyRequireHuman:
		return "REQUIRE_HUMAN"
	case SafetyThrottle:
		return "THROTTLE"
	default:
		return "UNKNOWN"
	}
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
