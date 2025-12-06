package scheduler

import (
	"context"
	"log"

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
}

func NewEngine(bus Bus, safety SafetyChecker, registry WorkerRegistry, strategy SchedulingStrategy, jobStore JobStore) *Engine {
	return &Engine{
		bus:      bus,
		safety:   safety,
		registry: registry,
		strategy: strategy,
		jobStore: jobStore,
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
		log.Printf("[SCHEDULER] heartbeat worker_id=%s type=%s cpu=%.1f gpu=%.1f active_jobs=%d",
			hb.WorkerId, hb.Type, hb.CpuLoad, hb.GpuUtilization, hb.ActiveJobs)
		e.registry.UpdateHeartbeat(hb)
	case *pb.BusPacket_JobRequest:
		req := payload.JobRequest
		if req == nil {
			return
		}
		log.Printf("[SCHEDULER] job request id=%s topic=%s", req.JobId, req.Topic)
		e.setJobState(req.JobId, JobStatePending)
		e.processJob(req, packet.TraceId)
	case *pb.BusPacket_JobResult:
		res := payload.JobResult
		if res == nil {
			return
		}
		log.Printf("[SCHEDULER] job result id=%s status=%s worker_id=%s",
			res.JobId, res.Status.String(), res.WorkerId)
		e.handleJobResult(res)
	default:
		// Unknown payloads are ignored for now.
	}
}

func (e *Engine) processJob(req *pb.JobRequest, traceID string) {
	if req == nil || req.JobId == "" || req.Topic == "" {
		log.Printf("[SCHEDULER] invalid job request trace_id=%s job_id=%q topic=%q", traceID, safeJobID(req), safeTopic(req))
		return
	}

	decision, reason := e.safety.Check(req)
	if decision == SafetyDeny {
		log.Printf("[SAFETY] blocked job_id=%s reason=%s", req.JobId, reason)
		e.setJobState(req.JobId, JobStateDenied)
		return
	}

	workers := e.registry.Snapshot()
	subject, err := e.strategy.PickSubject(req, workers)
	if err != nil {
		log.Printf("[SCHEDULER] failed to pick subject for job_id=%s: %v", req.JobId, err)
		return
	}

	log.Printf("[SCHEDULER] dispatching job_id=%s trace_id=%s subject=%s", req.JobId, traceID, subject)
	e.setJobState(req.JobId, JobStateRunning)

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
		log.Printf("[SCHEDULER] failed to publish job_id=%s to subject=%s: %v", req.JobId, subject, err)
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
