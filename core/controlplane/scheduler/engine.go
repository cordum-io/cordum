package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"sync/atomic"

	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/logging"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	schedulerQueue      = "cordum-scheduler"
	defaultSenderID     = "cordum-scheduler"
	protocolVersionV1   = capsdk.DefaultProtocolVersion
	storeOpTimeout      = 2 * time.Second
	dlqSubject          = capsdk.SubjectDLQ
	jobLockPrefix       = "cordum:scheduler:job:"
	jobLockTTL          = 30 * time.Second
	retryDelayBusy      = 500 * time.Millisecond
	retryDelayStore     = 1 * time.Second
	retryDelayPublish   = 2 * time.Second
	retryDelayNoWorkers = 2 * time.Second
	safetyThrottleDelay = 5 * time.Second
)

// Engine wires together bus interactions, safety checks, and scheduling decisions.
type Engine struct {
	bus      Bus
	safety   SafetyChecker
	registry WorkerRegistry
	strategy SchedulingStrategy
	jobStore JobStore
	metrics  Metrics
	config   ConfigProvider
	stopped  atomic.Bool
}

func jobLockKey(jobID string) string {
	if jobID == "" {
		return ""
	}
	return jobLockPrefix + jobID
}

func (e *Engine) withJobLock(jobID string, ttl time.Duration, fn func() error) error {
	if fn == nil {
		return nil
	}
	if e == nil || e.jobStore == nil || jobID == "" {
		return fn()
	}
	if ttl <= 0 {
		ttl = jobLockTTL
	}
	key := jobLockKey(jobID)
	deadline := time.Now().Add(storeOpTimeout)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), storeOpTimeout)
		ok, err := e.jobStore.TryAcquireLock(ctx, key, ttl)
		cancel()
		if err != nil {
			logging.Error("scheduler", "job lock acquisition failed", "job_id", jobID, "error", err)
			return RetryAfter(err, retryDelayStore)
		}
		if ok {
			break
		}
		if time.Now().After(deadline) {
			return RetryAfter(fmt.Errorf("job lock busy"), retryDelayBusy)
		}
		time.Sleep(25 * time.Millisecond)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), storeOpTimeout)
		_ = e.jobStore.ReleaseLock(ctx, key)
		cancel()
	}()
	return fn()
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

// WithConfig wires an optional effective config provider for dispatch-time injection.
func (e *Engine) WithConfig(cfg ConfigProvider) *Engine {
	e.config = cfg
	return e
}

// Start registers subscriptions for the scheduler.
func (e *Engine) Start() error {
	// Heartbeats must be broadcast to all schedulers to keep a complete view
	// of the worker pool when running multiple scheduler replicas.
	if err := e.bus.Subscribe(capsdk.SubjectHeartbeat, "", e.HandlePacket); err != nil {
		return err
	}
	if err := e.bus.Subscribe(capsdk.SubjectSubmit, schedulerQueue, e.HandlePacket); err != nil {
		return err
	}
	if err := e.bus.Subscribe(capsdk.SubjectResult, schedulerQueue, e.HandlePacket); err != nil {
		return err
	}
	if err := e.bus.Subscribe(capsdk.SubjectCancel, schedulerQueue, e.HandlePacket); err != nil {
		return err
	}
	return nil
}

func (e *Engine) HandlePacket(p *pb.BusPacket) error {
	if p == nil {
		return nil
	}
	if e.stopped.Load() {
		return nil
	}

	switch payload := p.Payload.(type) {
	case *pb.BusPacket_Heartbeat:
		hb := payload.Heartbeat
		if hb == nil {
			return nil
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
		return nil
	case *pb.BusPacket_JobRequest:
		req := payload.JobRequest
		if req == nil {
			return nil
		}
		tenant := ExtractTenant(req)
		logging.Info("scheduler", "job request received",
			"job_id", req.JobId,
			"topic", req.Topic,
			"trace_id", p.TraceId,
			"tenant", tenant,
		)
		return e.handleJobRequest(req, p.TraceId)

	case *pb.BusPacket_JobResult:
		res := payload.JobResult
		if res == nil {
			return nil
		}
		logging.Info("scheduler", "job result received",
			"job_id", res.JobId,
			"status", res.Status.String(),
			"worker_id", res.WorkerId,
			"result_ptr", res.ResultPtr,
		)
		return e.handleJobResult(res)
	case *pb.BusPacket_JobCancel:
		cancelReq := payload.JobCancel
		if cancelReq == nil {
			return nil
		}
		logging.Info("scheduler", "job cancel received",
			"job_id", cancelReq.JobId,
			"reason", cancelReq.Reason,
		)
		if e.jobStore != nil {
			ctx, cancel := context.WithTimeout(context.Background(), storeOpTimeout)
			_, _ = e.jobStore.CancelJob(ctx, cancelReq.JobId)
			cancel()
		}
		return nil
	default:
		// Unknown payloads are ignored for now.
		return nil
	}
}

// Stop prevents new packet handling; bus subscriptions are cleaned up when the bus is closed by caller.
func (e *Engine) Stop() {
	e.stopped.Store(true)
}

func (e *Engine) handleJobRequest(req *pb.JobRequest, traceID string) error {
	if req == nil {
		return nil
	}

	jobID := strings.TrimSpace(req.JobId)
	topic := strings.TrimSpace(req.Topic)
	if jobID == "" || topic == "" {
		logging.Error("scheduler", "invalid job request",
			"trace_id", traceID,
			"job_id", safeJobID(req),
			"topic", safeTopic(req),
		)
		_ = e.setJobState(jobID, JobStateFailed)
		e.incJobsCompleted(topic, pb.JobStatus_JOB_STATUS_FAILED.String())
		return nil
	}

	return e.withJobLock(jobID, jobLockTTL, func() error {
		if e.stopped.Load() {
			return nil
		}

		currentState := JobState("")
		if e.jobStore != nil {
			ctx, cancel := context.WithTimeout(context.Background(), storeOpTimeout)
			state, err := e.jobStore.GetState(ctx, jobID)
			cancel()
			if err == nil {
				currentState = state
				if terminalStates[state] || state == JobStateDispatched || state == JobStateRunning {
					return nil
				}
			}
		}

		e.incJobsReceived(topic)

		if e.jobStore != nil {
			ctx, cancel := context.WithTimeout(context.Background(), storeOpTimeout)
			if traceID != "" {
				if err := e.jobStore.AddJobToTrace(ctx, traceID, jobID); err != nil {
					cancel()
					logging.Error("scheduler", "failed to add job to trace", "job_id", jobID, "trace_id", traceID, "error", err)
					return RetryAfter(err, retryDelayStore)
				}
			}
			if err := e.jobStore.SetJobMeta(ctx, req); err != nil {
				cancel()
				logging.Error("scheduler", "failed to persist job metadata", "job_id", jobID, "error", err)
				return RetryAfter(err, retryDelayStore)
			}
			if store, ok := e.jobStore.(interface {
				SetJobRequest(context.Context, *pb.JobRequest) error
			}); ok {
				if err := store.SetJobRequest(ctx, req); err != nil {
					cancel()
					logging.Error("scheduler", "failed to persist job request", "job_id", jobID, "error", err)
					return RetryAfter(err, retryDelayStore)
				}
			}
			cancel()
		}

		if currentState == "" {
			if err := e.setJobState(jobID, JobStatePending); err != nil {
				return RetryAfter(err, retryDelayStore)
			}
		}

		return e.processJob(req, traceID)
	})
}

func (e *Engine) processJob(req *pb.JobRequest, traceID string) error {
	if req == nil || strings.TrimSpace(req.JobId) == "" || strings.TrimSpace(req.Topic) == "" {
		logging.Error("scheduler", "invalid job request",
			"trace_id", traceID,
			"job_id", safeJobID(req),
			"topic", safeTopic(req),
		)
		_ = e.setJobState(safeJobID(req), JobStateFailed)
		e.incJobsCompleted(safeTopic(req), pb.JobStatus_JOB_STATUS_FAILED.String())
		return nil
	}

	jobID := strings.TrimSpace(req.JobId)
	topic := strings.TrimSpace(req.Topic)

	e.attachEffectiveConfig(req)

	record, err := e.checkSafetyDecision(req)
	if err != nil {
		logging.Error("scheduler", "safety check failed", "job_id", jobID, "error", err)
	}
	switch record.Decision {
	case SafetyAllow, SafetyAllowWithConstraints:
		if record.Constraints != nil {
			applyConstraints(req, record.Constraints)
		}
	case SafetyThrottle:
		logging.Info("safety", "job throttled",
			"job_id", jobID,
			"topic", topic,
			"reason", record.Reason,
			"trace_id", traceID,
			"retry_after", safetyThrottleDelay.String(),
		)
		msg := "safety throttle"
		if strings.TrimSpace(record.Reason) != "" {
			msg = msg + ": " + record.Reason
		}
		return RetryAfter(fmt.Errorf("%s", msg), safetyThrottleDelay)
	case SafetyRequireApproval:
		logging.Info("safety", "job requires human approval",
			"job_id", jobID,
			"topic", topic,
			"reason", record.Reason,
			"trace_id", traceID,
		)
		_ = e.setJobState(jobID, JobStateApproval)
		return nil
	case SafetyDeny:
		logging.Info("safety", "job denied",
			"job_id", jobID,
			"topic", topic,
			"reason", record.Reason,
			"trace_id", traceID,
		)
		_ = e.setJobState(jobID, JobStateDenied)
		e.incSafetyDenied(topic)
		_ = e.emitDLQ(jobID, topic, pb.JobStatus_JOB_STATUS_DENIED, record.Reason, "safety_denied")
		return nil
	default:
		logging.Info("safety", "job denied (unknown decision)",
			"job_id", jobID,
			"topic", topic,
			"reason", record.Reason,
			"trace_id", traceID,
		)
		_ = e.setJobState(jobID, JobStateDenied)
		e.incSafetyDenied(topic)
		_ = e.emitDLQ(jobID, topic, pb.JobStatus_JOB_STATUS_DENIED, record.Reason, "safety_unknown")
		return nil
	}

	if maxRetries := maxRetriesFromConstraints(record.Constraints); maxRetries > 0 && e.jobStore != nil {
		ctx, cancel := context.WithTimeout(context.Background(), storeOpTimeout)
		attempts, err := e.jobStore.GetAttempts(ctx, jobID)
		cancel()
		if err == nil {
			allowedAttempts := int(maxRetries) + 1
			if attempts >= allowedAttempts {
				reason := fmt.Sprintf("max retries exceeded (attempts=%d, max_retries=%d)", attempts, maxRetries)
				_ = e.setJobState(jobID, JobStateFailed)
				_ = e.emitDLQ(jobID, topic, pb.JobStatus_JOB_STATUS_FAILED, reason, "max_retries_exceeded")
				return nil
			}
		}
	}

	if maxConcurrent := maxConcurrentFromConstraints(record.Constraints); maxConcurrent > 0 && e.jobStore != nil {
		tenant := ExtractTenant(req)
		ctx, cancel := context.WithTimeout(context.Background(), storeOpTimeout)
		active, err := e.jobStore.CountActiveByTenant(ctx, tenant)
		cancel()
		if err != nil {
			return RetryAfter(err, retryDelayStore)
		}
		if int64(active) >= maxConcurrent {
			logging.Info("scheduler", "tenant concurrency limit reached",
				"job_id", jobID,
				"tenant", tenant,
				"active", active,
				"limit", maxConcurrent,
			)
			return RetryAfter(ErrTenantLimit, retryDelayNoWorkers)
		}
	}

	if budget := req.GetBudget(); budget != nil && budget.GetDeadlineMs() > 0 && e.jobStore != nil {
		ctx, cancel := context.WithTimeout(context.Background(), storeOpTimeout)
		err := e.jobStore.SetDeadline(ctx, jobID, time.Now().Add(time.Duration(budget.GetDeadlineMs())*time.Millisecond))
		cancel()
		if err != nil {
			return RetryAfter(err, retryDelayStore)
		}
	}

	workers := e.registry.Snapshot()
	subject, err := e.strategy.PickSubject(req, workers)
	if err != nil {
		logging.Error("scheduler", "failed to pick subject",
			"job_id", jobID,
			"topic", topic,
			"error", err,
		)
		if isRetryableSchedulingError(err) {
			return RetryAfter(err, retryDelayNoWorkers)
		}
		_ = e.setJobState(jobID, JobStateFailed)
		e.incJobsCompleted(topic, pb.JobStatus_JOB_STATUS_FAILED.String())
		_ = e.emitDLQ(jobID, topic, pb.JobStatus_JOB_STATUS_FAILED, err.Error(), reasonCodeForSchedulingError(err))
		return nil
	}

	if err := e.setJobState(jobID, JobStateScheduled); err != nil {
		return RetryAfter(err, retryDelayStore)
	}

	if e.bus == nil {
		return RetryAfter(fmt.Errorf("bus unavailable"), retryDelayPublish)
	}
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
			"job_id", jobID,
			"subject", subject,
			"error", err,
		)
		return RetryAfter(err, retryDelayPublish)
	}

	e.incJobsDispatched(topic)
	if err := e.setJobState(jobID, JobStateDispatched); err != nil {
		return RetryAfter(err, retryDelayStore)
	}
	if err := e.setJobState(jobID, JobStateRunning); err != nil {
		return RetryAfter(err, retryDelayStore)
	}
	return nil
}

func isRetryableSchedulingError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrNoWorkers) || errors.Is(err, ErrPoolOverloaded) || errors.Is(err, ErrTenantLimit) {
		return true
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "no workers available") || strings.Contains(msg, "overloaded")
}

func reasonCodeForSchedulingError(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, ErrNoPoolMapping):
		return "no_pool_mapping"
	case errors.Is(err, ErrNoWorkers):
		return "no_workers"
	case errors.Is(err, ErrPoolOverloaded):
		return "pool_overloaded"
	case errors.Is(err, ErrTenantLimit):
		return "tenant_limit"
	default:
		return "dispatch_failed"
	}
}

func (e *Engine) checkSafetyDecision(req *pb.JobRequest) (SafetyDecisionRecord, error) {
	record := SafetyDecisionRecord{}
	if req == nil {
		return record, fmt.Errorf("missing job request")
	}
	jobID := strings.TrimSpace(req.JobId)
	if jobID == "" {
		return record, fmt.Errorf("missing job id")
	}

	approved := false
	if req.Labels != nil {
		if raw := strings.TrimSpace(req.Labels["approval_granted"]); raw != "" && strings.EqualFold(raw, "true") {
			approved = true
		}
	}
	if approved {
		if e.jobStore != nil {
			ctx, cancel := context.WithTimeout(context.Background(), storeOpTimeout)
			prev, err := e.jobStore.GetSafetyDecision(ctx, jobID)
			cancel()
			if err == nil && (prev.ApprovalRequired || prev.Decision == SafetyRequireApproval) && prev.JobHash != "" {
				hash, err := HashJobRequest(req)
				if err == nil && hash == prev.JobHash {
					record = SafetyDecisionRecord{
						Decision:       SafetyAllow,
						Reason:         "approval granted",
						CheckedAt:      time.Now().UTC().UnixNano() / int64(time.Microsecond),
						Constraints:    prev.Constraints,
						PolicySnapshot: prev.PolicySnapshot,
						RuleID:         prev.RuleID,
						JobHash:        prev.JobHash,
					}
					if e.jobStore != nil {
						ctx, cancel := context.WithTimeout(context.Background(), storeOpTimeout)
						if err := e.jobStore.SetSafetyDecision(ctx, jobID, record); err != nil {
							cancel()
							return record, err
						}
						cancel()
					}
					return record, nil
				}
				logging.Warn("scheduler", "approval label ignored (hash mismatch)", "job_id", jobID)
			} else {
				logging.Warn("scheduler", "approval label ignored (no approval record)", "job_id", jobID)
			}
		}
	}

	record, err := e.safety.Check(req)
	if record.CheckedAt == 0 {
		record.CheckedAt = time.Now().UTC().UnixNano() / int64(time.Microsecond)
	}
	if record.ApprovalRequired && (record.Decision == SafetyAllow || record.Decision == SafetyAllowWithConstraints) {
		record.Decision = SafetyRequireApproval
	}
	if record.Decision == SafetyRequireApproval || record.ApprovalRequired {
		if hash, err := HashJobRequest(req); err == nil {
			record.JobHash = hash
		} else {
			logging.Error("scheduler", "job hash failed", "job_id", jobID, "error", err)
		}
	}
	if e.jobStore != nil {
		ctx, cancel := context.WithTimeout(context.Background(), storeOpTimeout)
		if err := e.jobStore.SetSafetyDecision(ctx, jobID, record); err != nil {
			cancel()
			return record, err
		}
		cancel()
	}
	return record, err
}

func (e *Engine) handleJobResult(res *pb.JobResult) error {
	if res == nil {
		return nil
	}
	jobID := strings.TrimSpace(res.JobId)
	if jobID == "" {
		return nil
	}
	return e.withJobLock(jobID, jobLockTTL, func() error {
		status := res.Status
		if status == pb.JobStatus_JOB_STATUS_COMPLETED {
			status = pb.JobStatus_JOB_STATUS_SUCCEEDED
		}
		var topic string
		if e.jobStore != nil {
			ctx, cancel := context.WithTimeout(context.Background(), storeOpTimeout)
			topic, _ = e.jobStore.GetTopic(ctx, jobID)
			cancel()
		}
		if topic == "" {
			topic = "unknown"
		}
		// Idempotency: if job is already terminal, ignore duplicate results.
		if e.jobStore != nil {
			ctx, cancel := context.WithTimeout(context.Background(), storeOpTimeout)
			state, err := e.jobStore.GetState(ctx, jobID)
			cancel()
			if err == nil && terminalStates[state] {
				return nil
			}
		}
		var state JobState
		succeeded := false
		switch status {
		case pb.JobStatus_JOB_STATUS_SUCCEEDED:
			state = JobStateSucceeded
			succeeded = true
		case pb.JobStatus_JOB_STATUS_FAILED:
			state = JobStateFailed
		case pb.JobStatus_JOB_STATUS_TIMEOUT:
			state = JobStateTimeout
		case pb.JobStatus_JOB_STATUS_DENIED:
			state = JobStateDenied
		case pb.JobStatus_JOB_STATUS_CANCELLED:
			state = JobStateCancelled
		default:
			logging.Error("scheduler", "unknown job status",
				"job_id", res.JobId,
				"status", res.Status.String(),
			)
			state = JobStateFailed
		}
		if err := e.setJobState(jobID, state); err != nil {
			return RetryAfter(err, retryDelayStore)
		}
		if res.ResultPtr != "" {
			if err := e.setResultPtr(jobID, res.ResultPtr); err != nil {
				return RetryAfter(err, retryDelayStore)
			}
		}
		e.incJobsCompleted(topic, status.String())
		if !succeeded {
			if err := e.emitDLQ(jobID, topic, status, res.ErrorMessage, res.ErrorCode); err != nil {
				return RetryAfter(err, retryDelayPublish)
			}
		}
		return nil
	})
}

func (e *Engine) setJobState(jobID string, state JobState) error {
	if e.jobStore == nil || jobID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), storeOpTimeout)
	defer cancel()
	if err := e.jobStore.SetState(ctx, jobID, state); err != nil {
		logging.Error("scheduler", "failed to set job state", "job_id", jobID, "state", state, "error", err)
		return err
	}
	return nil
}

func (e *Engine) setResultPtr(jobID, ptr string) error {
	if e.jobStore == nil || jobID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), storeOpTimeout)
	defer cancel()
	if err := e.jobStore.SetResultPtr(ctx, jobID, ptr); err != nil {
		logging.Error("scheduler", "failed to persist result ptr", "job_id", jobID, "error", err)
		return err
	}
	return nil
}

// attachEffectiveConfig resolves and injects the effective config into the job request env.
func (e *Engine) attachEffectiveConfig(req *pb.JobRequest) {
	if e.config == nil || req == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), storeOpTimeout)
	defer cancel()

	env := req.GetEnv()
	stepID := ""
	teamID := ""
	if env != nil {
		stepID = env["step_id"]
		teamID = env["team_id"]
	}
	cfg, err := e.config.Effective(ctx, ExtractTenant(req), teamID, req.GetWorkflowId(), stepID)
	if err != nil || len(cfg) == 0 {
		return
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return
	}
	if req.Env == nil {
		req.Env = map[string]string{}
	}
	req.Env[config.EffectiveConfigEnvVar] = string(data)
}

func applyConstraints(req *pb.JobRequest, constraints *pb.PolicyConstraints) {
	if req == nil || constraints == nil {
		return
	}
	if req.Env == nil {
		req.Env = map[string]string{}
	}
	if data, err := protojson.Marshal(constraints); err == nil {
		req.Env["CORDUM_POLICY_CONSTRAINTS"] = string(data)
	}
	if constraints.GetRedactionLevel() != "" {
		req.Env["CORDUM_REDACTION_LEVEL"] = constraints.GetRedactionLevel()
	}
	if budgets := constraints.GetBudgets(); budgets != nil {
		if req.Budget == nil {
			req.Budget = &pb.Budget{}
		}
		if maxRuntime := budgets.GetMaxRuntimeMs(); maxRuntime > 0 {
			if req.Budget.DeadlineMs == 0 || req.Budget.DeadlineMs > maxRuntime {
				req.Budget.DeadlineMs = maxRuntime
			}
		}
		if maxArtifacts := budgets.GetMaxArtifactBytes(); maxArtifacts > 0 {
			req.Env["CORDUM_MAX_ARTIFACT_BYTES"] = fmt.Sprintf("%d", maxArtifacts)
		}
		if maxConcurrent := budgets.GetMaxConcurrentJobs(); maxConcurrent > 0 {
			req.Env["CORDUM_MAX_CONCURRENT_JOBS"] = fmt.Sprintf("%d", maxConcurrent)
		}
		if maxRetries := budgets.GetMaxRetries(); maxRetries > 0 {
			req.Env["CORDUM_MAX_RETRIES"] = fmt.Sprintf("%d", maxRetries)
		}
	}
}

func maxRetriesFromConstraints(constraints *pb.PolicyConstraints) int64 {
	if constraints == nil || constraints.GetBudgets() == nil {
		return 0
	}
	return int64(constraints.GetBudgets().GetMaxRetries())
}

func maxConcurrentFromConstraints(constraints *pb.PolicyConstraints) int64 {
	if constraints == nil || constraints.GetBudgets() == nil {
		return 0
	}
	return int64(constraints.GetBudgets().GetMaxConcurrentJobs())
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

// CancelJob marks a job as cancelled if not already terminal.
func (e *Engine) CancelJob(ctx context.Context, jobID string) error {
	if e.jobStore == nil {
		return fmt.Errorf("job store not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cctx, cancel := context.WithTimeout(ctx, storeOpTimeout)
	defer cancel()
	_, err := e.jobStore.CancelJob(cctx, jobID)
	if err != nil {
		return err
	}
	e.publishCancel(jobID, "cancelled by request")
	return err
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

// publishCancel emits a cancellation packet to a broadcast subject (best effort).
func (e *Engine) publishCancel(jobID, reason string) {
	if e.bus == nil {
		return
	}
	cancelReq := &pb.JobCancel{
		JobId:       jobID,
		Reason:      reason,
		RequestedBy: defaultSenderID,
	}
	packet := &pb.BusPacket{
		TraceId:         jobID,
		SenderId:        defaultSenderID,
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: protocolVersionV1,
		Payload:         &pb.BusPacket_JobCancel{JobCancel: cancelReq},
	}
	_ = e.bus.Publish(capsdk.SubjectCancel, packet)
}

func (e *Engine) emitDLQ(jobID, topic string, status pb.JobStatus, reason string, reasonCode string) error {
	if e.bus == nil || jobID == "" {
		return nil
	}
	packet := &pb.BusPacket{
		TraceId:         jobID,
		SenderId:        defaultSenderID,
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: protocolVersionV1,
		Payload: &pb.BusPacket_JobResult{
			JobResult: &pb.JobResult{
				JobId:        jobID,
				Status:       status,
				ErrorCode:    reasonCode,
				ErrorMessage: reason,
				ResultPtr:    "",
				WorkerId:     "",
			},
		},
	}
	return e.bus.Publish(dlqSubject, packet)
}
