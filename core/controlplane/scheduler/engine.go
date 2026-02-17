package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"sync"
	"sync/atomic"

	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/logging"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	capvalidate "github.com/cordum-io/cap/v2/sdk/go"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
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
	safetyCheckTimeout  = 3 * time.Second

	// maxSchedulingRetries caps the number of scheduling attempts before
	// a job is moved to FAILED + DLQ. With exponential backoff (1s→30s max)
	// this allows ~25 minutes of retries before giving up.
	maxSchedulingRetries = 50
	outputPolicyReason   = "output_quarantined"
	outputPolicyAsync    = "output_quarantined_async"
	outputPolicyAudit    = "sys.audit.output_policy"
)

// Engine wires together bus interactions, safety checks, and scheduling decisions.
type Engine struct {
	bus                 Bus
	safety              SafetyChecker
	outputSafety        OutputSafetyChecker
	outputSafetyEnabled atomic.Bool
	asyncFailMode       string // "closed" (default, quarantine on error) or "open" (allow on error)
	registry            WorkerRegistry
	strategy            SchedulingStrategy
	jobStore            JobStore
	dlqSink             DLQSink
	metrics             Metrics
	config              ConfigProvider
	saga                *SagaManager
	stopped             atomic.Bool
	activeHandlers      atomic.Int64
	wg                  sync.WaitGroup
	ctx                 context.Context
	cancel              context.CancelFunc
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
	lockStart := time.Now()
	deadline := lockStart.Add(storeOpTimeout)
	var (
		token string
		err   error
	)
	for {
		ctx, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
		token, err = e.jobStore.TryAcquireLock(ctx, key, ttl)
		cancel()
		if err != nil {
			logging.Error("scheduler", "job lock acquisition failed", "job_id", jobID, "error", err)
			return RetryAfter(err, retryDelayStore)
		}
		if token != "" {
			break
		}
		if time.Now().After(deadline) {
			return RetryAfter(fmt.Errorf("job lock busy"), retryDelayBusy)
		}
		backoff := time.NewTimer(25 * time.Millisecond)
		select {
		case <-e.ctx.Done():
			backoff.Stop()
			return e.ctx.Err()
		case <-backoff.C:
		}
	}
	if e.metrics != nil {
		e.metrics.ObserveJobLockWait(time.Since(lockStart).Seconds())
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), storeOpTimeout)
		defer cancel()
		if err := e.jobStore.ReleaseLock(ctx, key, token); err != nil {
			logging.Warn("scheduler", "job lock release failed, retrying",
				"job_id", jobID, "error", err)
			ctx2, cancel2 := context.WithTimeout(context.Background(), storeOpTimeout)
			defer cancel2()
			if err2 := e.jobStore.ReleaseLock(ctx2, key, token); err2 != nil {
				logging.Error("scheduler", "job lock release retry failed, lock will expire via TTL",
					"job_id", jobID, "ttl", ttl, "error", err2)
			}
		}
	}()
	return fn()
}

func NewEngine(bus Bus, safety SafetyChecker, registry WorkerRegistry, strategy SchedulingStrategy, jobStore JobStore, metrics Metrics) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	return &Engine{
		bus:      bus,
		safety:   safety,
		registry: registry,
		strategy: strategy,
		jobStore: jobStore,
		metrics:  metrics,
		ctx:      ctx,
		cancel:   cancel,
	}
}

// WithConfig wires an optional effective config provider for dispatch-time injection.
func (e *Engine) WithConfig(cfg ConfigProvider) *Engine {
	e.config = cfg
	return e
}

// WithSaga wires a saga manager for compensation tracking.
func (e *Engine) WithSaga(saga *SagaManager) *Engine {
	e.saga = saga
	return e
}

// WithDLQSink wires optional durable DLQ persistence.
func (e *Engine) WithDLQSink(sink DLQSink) *Engine {
	e.dlqSink = sink
	return e
}

// WithOutputSafety wires an optional output safety checker.
func (e *Engine) WithOutputSafety(c OutputSafetyChecker) *Engine {
	e.outputSafety = c
	return e
}

// WithOutputChecker wires an optional output safety checker.
// Alias kept for plan/docs terminology compatibility.
func (e *Engine) WithOutputChecker(c OutputSafetyChecker) *Engine {
	return e.WithOutputSafety(c)
}

// WithOutputSafetyEnabled toggles output safety checks.
func (e *Engine) WithOutputSafetyEnabled(enabled bool) *Engine {
	e.outputSafetyEnabled.Store(enabled)
	return e
}

// WithAsyncFailMode sets the behavior when async output checks fail/timeout.
// "closed" (default) quarantines on error; "open" allows on error with a warning.
func (e *Engine) WithAsyncFailMode(mode string) *Engine {
	if mode == "open" || mode == "closed" {
		e.asyncFailMode = mode
	}
	return e
}

func (e *Engine) isAsyncFailClosed() bool {
	return e.asyncFailMode != "open"
}

// Start registers subscriptions for the scheduler.
func (e *Engine) Start() error {
	// Heartbeats must be broadcast to all schedulers to keep a complete view
	// of the worker pool when running multiple scheduler replicas.
	if err := e.bus.Subscribe(capsdk.SubjectHeartbeat, "", e.HandlePacket); err != nil {
		return fmt.Errorf("subscribe heartbeat: %w", err)
	}
	if err := e.bus.Subscribe(capsdk.SubjectSubmit, schedulerQueue, e.HandlePacket); err != nil {
		return fmt.Errorf("subscribe submit: %w", err)
	}
	if err := e.bus.Subscribe(capsdk.SubjectResult, schedulerQueue, e.HandlePacket); err != nil {
		return fmt.Errorf("subscribe result: %w", err)
	}
	if err := e.bus.Subscribe(capsdk.SubjectCancel, schedulerQueue, e.HandlePacket); err != nil {
		return fmt.Errorf("subscribe cancel: %w", err)
	}
	// Handshakes broadcast to all replicas (like heartbeats).
	if err := e.bus.Subscribe(capsdk.SubjectHandshake, "", e.HandlePacket); err != nil {
		return fmt.Errorf("subscribe handshake: %w", err)
	}

	// Periodic registry stats logging for diagnostics
	type registryStatter interface {
		Stats() (int, map[string]int)
	}
	if statter, ok := e.registry.(registryStatter); ok {
		go func() {
			ticker := time.NewTicker(60 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-e.ctx.Done():
					return
				case <-ticker.C:
					total, byPool := statter.Stats()
					logging.Info("scheduler", "registry stats",
						"total_workers", total,
						"pools", fmt.Sprintf("%v", byPool),
					)
				}
			}
		}()
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

	e.wg.Add(1)
	count := e.activeHandlers.Add(1)
	if e.metrics != nil {
		e.metrics.SetActiveGoroutines(int(count))
	}
	defer func() {
		c := e.activeHandlers.Add(-1)
		if e.metrics != nil {
			e.metrics.SetActiveGoroutines(int(c))
		}
		e.wg.Done()
	}()

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
		if err := capvalidate.ValidateJobRequest(req); err != nil {
			logging.Warn("scheduler", "invalid job request rejected",
				"job_id", req.GetJobId(),
				"validation_error", err.Error(),
				"trace_id", p.TraceId,
			)
			if e.metrics != nil {
				e.metrics.IncValidationRejections()
			}
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
		if err := capvalidate.ValidateJobResult(res); err != nil {
			logging.Warn("scheduler", "invalid job result rejected",
				"job_id", res.GetJobId(),
				"validation_error", err.Error(),
				"trace_id", p.TraceId,
			)
			if e.metrics != nil {
				e.metrics.IncValidationRejections()
			}
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
			ctx, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
			defer cancel()
			_, err := e.jobStore.CancelJob(ctx, cancelReq.JobId)
			if err != nil {
				logging.Error("scheduler", "cancel job failed",
					"job_id", cancelReq.JobId,
					"error", err,
				)
				if e.metrics != nil {
					e.metrics.IncJobCancelFailures()
				}
				return err // return error so NATS redelivers the message
			}
		}
		logging.Info("scheduler", "job cancelled",
			"job_id", cancelReq.JobId,
		)
		return nil
	case *pb.BusPacket_Handshake:
		hs := payload.Handshake
		if hs == nil {
			return nil
		}
		logging.Info("scheduler", "handshake received",
			"component_id", hs.ComponentId,
			"role", hs.Role.String(),
			"sdk_version", hs.SdkVersion,
			"supported_versions", hs.SupportedVersions,
		)
		if hs.Role == pb.ComponentRole_COMPONENT_ROLE_WORKER {
			e.registry.UpdateHandshake(hs)
		}
		return nil
	default:
		// Unknown payloads are ignored for now.
		return nil
	}
}

// Stop prevents new packet handling, then waits for in-flight handlers
// to complete with a 10s deadline.  Bus subscriptions are cleaned up
// when the bus is closed by the caller.
//
// The background goroutine waiting on wg.Wait() will exit once all
// in-flight handlers complete; context cancellation should ensure this
// happens within storeOpTimeout.
func (e *Engine) Stop() {
	e.stopped.Store(true)
	if e.cancel != nil {
		e.cancel()
	}
	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		logging.Warn("scheduler", "graceful shutdown deadline exceeded, some handlers still in-flight")
	}
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
		if err := e.setJobState(jobID, JobStateFailed); err != nil {
			logging.Error("scheduler", "state transition failed", "job_id", jobID, "target_state", JobStateFailed, "error", err)
		}
		e.incJobsCompleted(topic, pb.JobStatus_JOB_STATUS_FAILED.String())
		return nil
	}

	return e.withJobLock(jobID, jobLockTTL, func() error {
		if e.stopped.Load() {
			return nil
		}

		currentState := JobState("")
		if e.jobStore != nil {
			ctx, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
			defer cancel()
			state, err := e.jobStore.GetState(ctx, jobID)
			if err == nil {
				currentState = state
				if terminalStates[state] || state == JobStateDispatched || state == JobStateRunning {
					return nil
				}
			}
		}

		e.incJobsReceived(topic)

		if e.jobStore != nil {
			ctx, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
			defer cancel()
			if traceID != "" {
				if err := e.jobStore.AddJobToTrace(ctx, traceID, jobID); err != nil {
					logging.Error("scheduler", "failed to add job to trace", "job_id", jobID, "trace_id", traceID, "error", err)
					return RetryAfter(err, retryDelayStore)
				}
			}
			if err := e.jobStore.SetJobMeta(ctx, req); err != nil {
				logging.Error("scheduler", "failed to persist job metadata", "job_id", jobID, "error", err)
				return RetryAfter(err, retryDelayStore)
			}
			if store, ok := e.jobStore.(interface {
				SetJobRequest(context.Context, *pb.JobRequest) error
			}); ok {
				if err := store.SetJobRequest(ctx, req); err != nil {
					logging.Error("scheduler", "failed to persist job request", "job_id", jobID, "error", err)
					return RetryAfter(err, retryDelayStore)
				}
			}
		}

		if currentState == "" {
			if err := e.setJobState(jobID, JobStatePending); err != nil {
				return RetryAfter(err, retryDelayStore)
			}
		}

		if err := e.processJob(req, traceID); err != nil {
			if isRetryableSchedulingError(err) {
				return nil
			}
			return err
		}
		return nil
	})
}

func (e *Engine) processJob(req *pb.JobRequest, traceID string) error {
	if req == nil || strings.TrimSpace(req.JobId) == "" || strings.TrimSpace(req.Topic) == "" {
		logging.Error("scheduler", "invalid job request",
			"trace_id", traceID,
			"job_id", safeJobID(req),
			"topic", safeTopic(req),
		)
		if err := e.setJobState(safeJobID(req), JobStateFailed); err != nil {
			logging.Error("scheduler", "state transition failed", "job_id", safeJobID(req), "target_state", JobStateFailed, "error", err)
		}
		e.incJobsCompleted(safeTopic(req), pb.JobStatus_JOB_STATUS_FAILED.String())
		return nil
	}

	jobID := strings.TrimSpace(req.JobId)
	topic := strings.TrimSpace(req.Topic)
	dispatchStart := time.Now()

	// Fetch attempt count for exponential backoff on retries.
	attempts := 0
	if e.jobStore != nil {
		ctx, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
		defer cancel()
		a, err := e.jobStore.GetAttempts(ctx, jobID)
		if err == nil {
			attempts = a
		}
	}

	// Give up after maxSchedulingRetries to prevent infinite NAK loops
	// (e.g. job.default with no workers). Job goes to DLQ for investigation.
	if attempts >= maxSchedulingRetries {
		reason := fmt.Sprintf("max scheduling retries exceeded (attempts=%d)", attempts)
		logging.Warn("scheduler", "giving up on job", "job_id", jobID, "topic", topic, "attempts", attempts)
		if err := e.setJobState(jobID, JobStateFailed); err != nil {
			logging.Error("scheduler", "state transition failed", "job_id", jobID, "target_state", JobStateFailed, "error", err)
		}
		if err := e.emitDLQWithRetry(jobID, topic, pb.JobStatus_JOB_STATUS_FAILED, reason, "max_scheduling_retries"); err != nil {
			logging.Error("scheduler", "dlq emit failed", "job_id", jobID, "error", err)
		}
		e.incJobsCompleted(topic, pb.JobStatus_JOB_STATUS_FAILED.String())
		return nil
	}

	e.attachEffectiveConfig(req)

	record, err := e.checkSafetyDecision(req)
	if err != nil {
		logging.Error("scheduler", "safety check failed", "job_id", jobID, "error", err)
		record.Decision = SafetyUnavailable
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
	case SafetyUnavailable:
		logging.Warn("safety", "safety kernel unavailable, requeueing job",
			"job_id", jobID,
			"topic", topic,
			"reason", record.Reason,
			"trace_id", traceID,
		)
		if e.metrics != nil {
			e.metrics.IncSafetyUnavailable(topic)
		}
		return RetryAfter(fmt.Errorf("safety unavailable: %s", record.Reason), safetyThrottleDelay)
	case SafetyRequireApproval:
		logging.Info("safety", "job requires human approval",
			"job_id", jobID,
			"topic", topic,
			"reason", record.Reason,
			"trace_id", traceID,
		)
		if err := e.setJobState(jobID, JobStateApproval); err != nil {
			logging.Error("scheduler", "state transition failed", "job_id", jobID, "target_state", JobStateApproval, "error", err)
		}
		return nil
	case SafetyDeny:
		logging.Info("safety", "job denied",
			"job_id", jobID,
			"topic", topic,
			"reason", record.Reason,
			"trace_id", traceID,
		)
		if err := e.setJobState(jobID, JobStateDenied); err != nil {
			logging.Error("scheduler", "state transition failed", "job_id", jobID, "target_state", JobStateDenied, "error", err)
		}
		e.incSafetyDenied(topic)
		if err := e.emitDLQWithRetry(jobID, topic, pb.JobStatus_JOB_STATUS_DENIED, record.Reason, "safety_denied"); err != nil {
			logging.Error("scheduler", "dlq emit failed", "job_id", jobID, "error", err)
		}
		return nil
	default:
		logging.Info("safety", "job denied (unknown decision)",
			"job_id", jobID,
			"topic", topic,
			"reason", record.Reason,
			"trace_id", traceID,
		)
		if err := e.setJobState(jobID, JobStateDenied); err != nil {
			logging.Error("scheduler", "state transition failed", "job_id", jobID, "target_state", JobStateDenied, "error", err)
		}
		e.incSafetyDenied(topic)
		if err := e.emitDLQWithRetry(jobID, topic, pb.JobStatus_JOB_STATUS_DENIED, record.Reason, "safety_unknown"); err != nil {
			logging.Error("scheduler", "dlq emit failed", "job_id", jobID, "error", err)
		}
		return nil
	}

	if maxRetries := maxRetriesFromConstraints(record.Constraints); maxRetries > 0 && e.jobStore != nil {
		ctx, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
		defer cancel()
		attempts, err := e.jobStore.GetAttempts(ctx, jobID)
		if err == nil {
			allowedAttempts := int(maxRetries) + 1
			if attempts >= allowedAttempts {
				reason := fmt.Sprintf("max retries exceeded (attempts=%d, max_retries=%d)", attempts, maxRetries)
				if err := e.setJobState(jobID, JobStateFailed); err != nil {
					logging.Error("scheduler", "state transition failed", "job_id", jobID, "target_state", JobStateFailed, "error", err)
				}
				if err := e.emitDLQWithRetry(jobID, topic, pb.JobStatus_JOB_STATUS_FAILED, reason, "max_retries_exceeded"); err != nil {
					logging.Error("scheduler", "dlq emit failed", "job_id", jobID, "error", err)
				}
				return nil
			}
		}
	}

	if maxConcurrent := maxConcurrentFromConstraints(record.Constraints); maxConcurrent > 0 && e.jobStore != nil {
		tenant := ExtractTenant(req)
		ctx, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
		defer cancel()
		active, err := e.jobStore.CountActiveByTenant(ctx, tenant)
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
		ctx, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
		defer cancel()
		err := e.jobStore.SetDeadline(ctx, jobID, time.Now().Add(time.Duration(budget.GetDeadlineMs())*time.Millisecond))
		if err != nil {
			return RetryAfter(err, retryDelayStore)
		}
	}

	workers := e.registry.Snapshot()
	if len(workers) == 0 {
		logging.Warn("scheduler", "no workers in registry",
			"topic", topic,
			"job_id", jobID,
		)
	}
	subject, err := e.strategy.PickSubject(req, workers)
	if err != nil {
		if errors.Is(err, ErrNoPoolMapping) {
			logging.Warn("scheduler", "no pool mapping for topic, will retry",
				"job_id", jobID,
				"topic", topic,
				"error", err,
			)
		} else {
			logging.Error("scheduler", "failed to pick subject",
				"job_id", jobID,
				"topic", topic,
				"error", err,
			)
		}
		if isRetryableSchedulingError(err) {
			if inc, ok := e.jobStore.(interface {
				IncrAttempts(context.Context, string) error
			}); ok {
				ctx2, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
				_ = inc.IncrAttempts(ctx2, jobID)
				cancel()
			}
			return RetryAfter(err, backoffDelay(attempts, backoffBase, backoffMax))
		}
		if err := e.setJobState(jobID, JobStateFailed); err != nil {
			logging.Error("scheduler", "state transition failed", "job_id", jobID, "target_state", JobStateFailed, "error", err)
		}
		e.incJobsCompleted(topic, pb.JobStatus_JOB_STATUS_FAILED.String())
		if err := e.emitDLQWithRetry(jobID, topic, pb.JobStatus_JOB_STATUS_FAILED, err.Error(), reasonCodeForSchedulingError(err)); err != nil {
			logging.Error("scheduler", "dlq emit failed", "job_id", jobID, "error", err)
		}
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
		return RetryAfter(err, backoffDelay(attempts, backoffBase, backoffMax))
	}

	e.incJobsDispatched(topic)
	if e.metrics != nil {
		e.metrics.ObserveDispatchLatency(topic, time.Since(dispatchStart).Seconds())
	}
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
	if errors.Is(err, ErrNoWorkers) || errors.Is(err, ErrPoolOverloaded) || errors.Is(err, ErrTenantLimit) || errors.Is(err, ErrNoPoolMapping) {
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
			ctx, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
			defer cancel()
			prev, err := e.jobStore.GetSafetyDecision(ctx, jobID)
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
						ctx, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
						defer cancel()
						if err := e.jobStore.SetSafetyDecision(ctx, jobID, record); err != nil {
							return record, err
						}
					}
					return record, nil
				}
				logging.Warn("scheduler", "approval label ignored (hash mismatch)", "job_id", jobID)
			} else {
				logging.Warn("scheduler", "approval label ignored (no approval record)", "job_id", jobID)
			}
		}
	}

	safetyCtx, safetyCancel := context.WithTimeout(e.ctx, safetyCheckTimeout)
	defer safetyCancel()

	record, err := e.safety.Check(safetyCtx, req)
	if safetyCtx.Err() != nil && e.ctx.Err() != nil {
		return record, e.ctx.Err()
	}
	if safetyCtx.Err() != nil {
		record.Decision = SafetyUnavailable
		err = fmt.Errorf("safety check timeout (defense-in-depth, %s)", safetyCheckTimeout)
		logging.Warn("scheduler", "safety check timed out", "job_id", jobID, "timeout", safetyCheckTimeout)
	}
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
		ctx, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
		defer cancel()
		if err := e.jobStore.SetSafetyDecision(ctx, jobID, record); err != nil {
			return record, err
		}
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
	// Auto-populate structured ErrorCodeEnum from legacy string ErrorCode
	// when the enum is unset but the string is present.
	if res.ErrorCodeEnum == pb.ErrorCode_ERROR_CODE_UNSPECIFIED && res.ErrorCode != "" {
		res.ErrorCodeEnum = mapStringToErrorCode(res.ErrorCode)
	}
	return e.withJobLock(jobID, jobLockTTL, func() error {
		status := res.Status
		if status == pb.JobStatus_JOB_STATUS_COMPLETED {
			status = pb.JobStatus_JOB_STATUS_SUCCEEDED
		}
		var topic string
		if e.jobStore != nil {
			ctx, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
			defer cancel()
			topic, _ = e.jobStore.GetTopic(ctx, jobID)
		}
		if topic == "" {
			topic = "unknown"
		}
		// Idempotency: if job is already terminal, ignore duplicate results.
		if e.jobStore != nil {
			ctx, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
			defer cancel()
			state, err := e.jobStore.GetState(ctx, jobID)
			if err == nil && terminalStates[state] {
				return nil
			}
		}
		var jobReq *pb.JobRequest
		needsJobReq := e.saga != nil || (status == pb.JobStatus_JOB_STATUS_SUCCEEDED && e.outputSafetyEnabled.Load() && e.outputSafety != nil)
		if needsJobReq && e.jobStore != nil {
			if store, ok := e.jobStore.(interface {
				GetJobRequest(context.Context, string) (*pb.JobRequest, error)
			}); ok {
				ctx, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
				defer cancel()
				jobReq, _ = store.GetJobRequest(ctx, jobID)
			}
		}
		if status == pb.JobStatus_JOB_STATUS_SUCCEEDED && e.saga != nil && jobReq != nil {
			if err := e.saga.RecordCompensation(e.ctx, jobReq); err != nil {
				logging.Error("scheduler", "record compensation failed", "job_id", jobID, "error", err)
			}
		}
		if status == pb.JobStatus_JOB_STATUS_FAILED_FATAL && e.saga != nil {
			workflowID := ""
			if jobReq != nil {
				workflowID = strings.TrimSpace(jobReq.WorkflowId)
				if workflowID == "" && jobReq.Labels != nil {
					workflowID = strings.TrimSpace(jobReq.Labels["workflow_id"])
				}
			}
			if workflowID != "" {
				e.wg.Add(1)
				go func(wfID string) {
					defer e.wg.Done()
					ctx, cancel := context.WithTimeout(e.ctx, 30*time.Second)
					defer cancel()
					start := time.Now()
					if err := e.saga.Rollback(ctx, wfID); err != nil {
						logging.Error("scheduler", "saga rollback failed", "workflow_id", wfID, "duration", time.Since(start), "error", err)
					} else {
						logging.Info("scheduler", "saga rollback completed", "workflow_id", wfID, "duration", time.Since(start))
					}
				}(workflowID)
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
		case pb.JobStatus_JOB_STATUS_FAILED_RETRYABLE:
			state = JobStateFailed
		case pb.JobStatus_JOB_STATUS_FAILED_FATAL:
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

		var outputRecord OutputSafetyRecord
		if succeeded {
			persistOutputRecord := false
			outputRecord = e.checkOutputSafety(jobID, topic, res, jobReq)
			switch outputRecord.Decision {
			case OutputQuarantine, OutputDeny:
				state = JobStateQuarantined
				succeeded = false
			case OutputRedact:
				before := outputRecord
				outputRecord = e.materializeRedaction(jobID, topic, res, jobReq, outputRecord)
				if before.Decision != outputRecord.Decision ||
					before.Reason != outputRecord.Reason ||
					before.RedactedPtr != outputRecord.RedactedPtr ||
					before.Phase != outputRecord.Phase ||
					before.CheckDurationMs != outputRecord.CheckDurationMs {
					persistOutputRecord = true
				}
				if ptr := strings.TrimSpace(outputRecord.RedactedPtr); ptr != "" {
					res.ResultPtr = ptr
				} else {
					outputRecord.Decision = OutputQuarantine
					if strings.TrimSpace(outputRecord.Reason) == "" {
						outputRecord.Reason = "output redaction required but sanitized output unavailable"
					}
					state = JobStateQuarantined
					succeeded = false
					persistOutputRecord = true
				}
			}
			if persistOutputRecord {
				e.persistOutputSafety(jobID, outputRecord)
			}
		}

		if err := e.setJobState(jobID, state); err != nil {
			return RetryAfter(err, retryDelayStore)
		}
		if res.ResultPtr != "" {
			if err := e.setResultPtr(jobID, res.ResultPtr); err != nil {
				return RetryAfter(err, retryDelayStore)
			}
		}
		if succeeded && e.outputSafetyEnabled.Load() && e.outputSafety != nil && jobReq != nil {
			e.startAsyncOutputCheck(jobID, topic, res, jobReq)
		}
		completionStatus := status.String()
		if state == JobStateQuarantined {
			completionStatus = string(JobStateQuarantined)
		}
		e.incJobsCompleted(topic, completionStatus)
		if state == JobStateQuarantined {
			reason := strings.TrimSpace(outputRecord.Reason)
			if reason == "" {
				reason = "output quarantined by policy"
			}
			e.emitOutputAuditEvent(jobID, topic, outputPolicyReason, reason, outputRecord.Decision)
			if err := e.emitDLQWithRetry(jobID, topic, pb.JobStatus_JOB_STATUS_DENIED, reason, outputPolicyReason); err != nil {
				return RetryAfter(err, retryDelayPublish)
			}
		} else if !succeeded && status != pb.JobStatus_JOB_STATUS_FAILED_RETRYABLE {
			if err := e.emitDLQWithRetry(jobID, topic, status, res.ErrorMessage, res.ErrorCode); err != nil {
				return RetryAfter(err, retryDelayPublish)
			}
		}
		return nil
	})
}

func (e *Engine) checkOutputSafety(jobID, topic string, res *pb.JobResult, req *pb.JobRequest) OutputSafetyRecord {
	record := OutputSafetyRecord{
		Decision:    OutputAllow,
		Phase:       "sync",
		CheckedAt:   time.Now().UTC().UnixNano() / int64(time.Microsecond),
		OriginalPtr: strings.TrimSpace(res.GetResultPtr()),
	}
	if !e.outputSafetyEnabled.Load() || e.outputSafety == nil {
		e.incOutputPolicySkipped(topic)
		return record
	}
	if req == nil {
		logging.Error("scheduler", "output policy skipped: missing job request", "job_id", jobID)
		e.incOutputPolicySkipped(topic)
		return record
	}

	start := time.Now()
	checked, err := e.outputSafety.CheckOutputMeta(res, req)
	elapsed := time.Since(start)
	record.CheckDurationMs = elapsed.Milliseconds()
	e.observeOutputCheckLatency(topic, "sync", float64(record.CheckDurationMs)/1000.0)
	e.observeOutputEvalDuration(topic, elapsed.Seconds())
	if err != nil {
		logging.Error("scheduler", "output policy check failed", "job_id", jobID, "error", err)
		e.incOutputPolicySkipped(topic)
		return record
	}
	e.incOutputPolicyChecked(topic)
	e.incOutputEvaluations(topic)

	if checked.Decision != "" {
		record.Decision = checked.Decision
	}
	if checked.Reason != "" {
		record.Reason = checked.Reason
	}
	record.RuleID = checked.RuleID
	record.PolicySnapshot = checked.PolicySnapshot
	record.Findings = checked.Findings
	record.RedactedPtr = checked.RedactedPtr
	if checked.OriginalPtr != "" {
		record.OriginalPtr = checked.OriginalPtr
	}
	if checked.CheckedAt != 0 {
		record.CheckedAt = checked.CheckedAt
	}
	if checked.CheckDurationMs > 0 {
		record.CheckDurationMs = checked.CheckDurationMs
	}
	if checked.Phase != "" {
		record.Phase = checked.Phase
	}
	if record.Decision == OutputQuarantine || record.Decision == OutputDeny {
		e.incOutputPolicyQuarantined(topic)
		e.incOutputDenials(topic)
	}
	if record.Decision == OutputRedact {
		e.incOutputRedactions(topic)
	}
	e.persistOutputSafety(jobID, record)
	return record
}

func (e *Engine) startAsyncOutputCheck(jobID, topic string, res *pb.JobResult, req *pb.JobRequest) {
	if e.outputSafety == nil || !e.outputSafetyEnabled.Load() || res == nil || req == nil || jobID == "" {
		return
	}
	resCopy, ok := proto.Clone(res).(*pb.JobResult)
	if !ok || resCopy == nil {
		resCopy = res
	}
	reqCopy, ok := proto.Clone(req).(*pb.JobRequest)
	if !ok || reqCopy == nil {
		reqCopy = req
	}

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		ctx, cancel := context.WithTimeout(e.ctx, 30*time.Second)
		defer cancel()

		start := time.Now()
		record, err := e.outputSafety.CheckOutputContent(ctx, resCopy, reqCopy)
		elapsed := time.Since(start)
		e.observeOutputCheckLatency(topic, "async", elapsed.Seconds())
		e.observeOutputEvalDuration(topic, elapsed.Seconds())
		if err != nil {
			e.incAsyncOutputTimeout(topic)
			if e.isAsyncFailClosed() {
				logging.Error("scheduler", "async output check failed, fail-closed: quarantining", "job_id", jobID, "error", err)
				record = OutputSafetyRecord{
					Decision:        OutputQuarantine,
					Reason:          "async output check error — fail-closed: " + err.Error(),
					Phase:           "async",
					CheckedAt:       time.Now().UTC().UnixNano() / int64(time.Microsecond),
					CheckDurationMs: elapsed.Milliseconds(),
					OriginalPtr:     strings.TrimSpace(resCopy.GetResultPtr()),
				}
				// Fall through to process the quarantine decision below.
			} else {
				logging.Warn("scheduler", "async output check failed, fail-open: allowing", "job_id", jobID, "error", err)
				e.incOutputPolicySkipped(topic)
				return
			}
		}
		e.incOutputPolicyChecked(topic)
		e.incOutputEvaluations(topic)

		if record.Decision == "" {
			record.Decision = OutputAllow
		}
		if record.Phase == "" {
			record.Phase = "async"
		}
		if record.CheckedAt == 0 {
			record.CheckedAt = time.Now().UTC().UnixNano() / int64(time.Microsecond)
		}
		if record.CheckDurationMs == 0 {
			record.CheckDurationMs = elapsed.Milliseconds()
		}
		if record.OriginalPtr == "" {
			record.OriginalPtr = strings.TrimSpace(resCopy.GetResultPtr())
		}
		e.persistOutputSafety(jobID, record)

		if record.Decision == OutputRedact {
			e.incOutputRedactions(topic)
		}
		if record.Decision != OutputQuarantine && record.Decision != OutputDeny {
			return
		}
		e.incOutputPolicyQuarantined(topic)
		e.incOutputDenials(topic)
		if err := e.withJobLock(jobID, jobLockTTL, func() error {
			if e.jobStore != nil {
				stateCtx, stateCancel := context.WithTimeout(e.ctx, storeOpTimeout)
				defer stateCancel()
				curr, getErr := e.jobStore.GetState(stateCtx, jobID)
				if getErr == nil {
					if curr == JobStateQuarantined {
						return nil
					}
					if curr != JobStateSucceeded {
						// Only downgrade from succeeded to quarantined.
						return nil
					}
				}
			}
			if err := e.setJobState(jobID, JobStateQuarantined); err != nil {
				return err
			}
			reason := strings.TrimSpace(record.Reason)
			if reason == "" {
				reason = "output quarantined by async policy"
			}
			e.emitOutputAuditEvent(jobID, topic, outputPolicyAsync, reason, record.Decision)
			return e.emitDLQWithRetry(jobID, topic, pb.JobStatus_JOB_STATUS_DENIED, reason, outputPolicyAsync)
		}); err != nil {
			logging.Error("scheduler", "async quarantine transition failed", "job_id", jobID, "error", err)
		}
	}()
}

func (e *Engine) materializeRedaction(jobID, topic string, res *pb.JobResult, req *pb.JobRequest, current OutputSafetyRecord) OutputSafetyRecord {
	if e.outputSafety == nil || res == nil || req == nil {
		current.Decision = OutputQuarantine
		if strings.TrimSpace(current.Reason) == "" {
			current.Reason = "output redaction required but checker unavailable"
		}
		return current
	}
	if strings.TrimSpace(current.RedactedPtr) != "" {
		return current
	}

	ctx, cancel := context.WithTimeout(e.ctx, 5*time.Second)
	defer cancel()
	start := time.Now()
	record, err := e.outputSafety.CheckOutputContent(ctx, res, req)
	elapsed := time.Since(start)
	e.observeOutputCheckLatency(topic, "sync_redact", elapsed.Seconds())
	e.observeOutputEvalDuration(topic, elapsed.Seconds())
	if err != nil {
		logging.Error("scheduler", "redaction materialization failed", "job_id", jobID, "error", err)
		current.Decision = OutputQuarantine
		if strings.TrimSpace(current.Reason) == "" {
			current.Reason = "output redaction required but sanitized output unavailable"
		}
		current.CheckDurationMs += elapsed.Milliseconds()
		return current
	}
	e.incOutputPolicyChecked(topic)
	e.incOutputEvaluations(topic)
	e.incOutputRedactions(topic)

	if record.Decision == "" {
		record.Decision = OutputRedact
	}
	if record.Reason == "" {
		record.Reason = current.Reason
	}
	if record.RuleID == "" {
		record.RuleID = current.RuleID
	}
	if record.PolicySnapshot == "" {
		record.PolicySnapshot = current.PolicySnapshot
	}
	if record.CheckedAt == 0 {
		record.CheckedAt = time.Now().UTC().UnixNano() / int64(time.Microsecond)
	}
	if record.CheckDurationMs == 0 {
		record.CheckDurationMs = elapsed.Milliseconds()
	}
	if record.OriginalPtr == "" {
		record.OriginalPtr = strings.TrimSpace(res.GetResultPtr())
	}
	if record.Decision == OutputRedact && strings.TrimSpace(record.RedactedPtr) == "" {
		record.Decision = OutputQuarantine
		if strings.TrimSpace(record.Reason) == "" {
			record.Reason = "output redaction required but sanitized output unavailable"
		}
	}
	return record
}

func (e *Engine) persistOutputSafety(jobID string, record OutputSafetyRecord) {
	if jobID == "" || e.jobStore == nil {
		return
	}
	ctx, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
	defer cancel()
	if err := e.jobStore.SetOutputDecision(ctx, jobID, record); err != nil {
		logging.Error("scheduler", "persist output safety failed", "job_id", jobID, "error", err)
	}
}

func (e *Engine) setJobState(jobID string, state JobState) error {
	if e.jobStore == nil || jobID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
	defer cancel()
	if err := e.jobStore.SetState(ctx, jobID, state); err != nil {
		logging.Error("scheduler", "failed to set job state", "job_id", jobID, "state", state, "error", err)
		return fmt.Errorf("set job state: %w", err)
	}
	return nil
}

func (e *Engine) setResultPtr(jobID, ptr string) error {
	if e.jobStore == nil || jobID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
	defer cancel()
	if err := e.jobStore.SetResultPtr(ctx, jobID, ptr); err != nil {
		logging.Error("scheduler", "failed to persist result ptr", "job_id", jobID, "error", err)
		return fmt.Errorf("set result ptr: %w", err)
	}
	return nil
}

// attachEffectiveConfig resolves and injects the effective config into the job request env.
func (e *Engine) attachEffectiveConfig(req *pb.JobRequest) {
	if e.config == nil || req == nil {
		return
	}
	ctx, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
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
		ctx = e.ctx
	}
	cctx, cancel := context.WithTimeout(ctx, storeOpTimeout)
	defer cancel()
	_, err := e.jobStore.CancelJob(cctx, jobID)
	if err != nil {
		return fmt.Errorf("cancel job: %w", err)
	}
	e.publishCancel(jobID, "cancelled by request")
	return nil
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

func (e *Engine) incOutputPolicyChecked(topic string) {
	if e.metrics != nil {
		e.metrics.IncOutputPolicyChecked(topic)
	}
}

func (e *Engine) incOutputPolicyQuarantined(topic string) {
	if e.metrics != nil {
		e.metrics.IncOutputPolicyQuarantined(topic)
	}
}

func (e *Engine) incOutputPolicySkipped(topic string) {
	if e.metrics != nil {
		e.metrics.IncOutputPolicySkipped(topic)
	}
}

func (e *Engine) incAsyncOutputTimeout(topic string) {
	if e.metrics != nil {
		e.metrics.IncAsyncOutputTimeout(topic)
	}
}

func (e *Engine) observeOutputCheckLatency(topic, phase string, seconds float64) {
	if e.metrics != nil {
		e.metrics.ObserveOutputCheckLatency(topic, phase, seconds)
	}
}

func (e *Engine) incOutputEvaluations(topic string) {
	if e.metrics != nil {
		e.metrics.IncOutputEvaluations(topic)
	}
}

func (e *Engine) incOutputDenials(topic string) {
	if e.metrics != nil {
		e.metrics.IncOutputDenials(topic)
	}
}

func (e *Engine) incOutputRedactions(topic string) {
	if e.metrics != nil {
		e.metrics.IncOutputRedactions(topic)
	}
}

func (e *Engine) observeOutputEvalDuration(topic string, seconds float64) {
	if e.metrics != nil {
		e.metrics.ObserveOutputEvalDuration(topic, seconds)
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
	if jobID == "" {
		return nil
	}

	createdAt := time.Now().UTC()
	if e.dlqSink != nil {
		ctx, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
		err := e.dlqSink.Add(ctx, DLQEntry{
			JobID:      jobID,
			Topic:      topic,
			Status:     status.String(),
			Reason:     reason,
			ReasonCode: reasonCode,
			CreatedAt:  createdAt,
		})
		cancel()
		if err != nil {
			return fmt.Errorf("dlq sink add failed: %w", err)
		}
	}

	if e.bus == nil {
		return nil
	}
	packet := &pb.BusPacket{
		TraceId:         jobID,
		SenderId:        defaultSenderID,
		CreatedAt:       timestamppb.New(createdAt),
		ProtocolVersion: protocolVersionV1,
		Payload: &pb.BusPacket_JobResult{
			JobResult: &pb.JobResult{
				JobId:         jobID,
				Status:        status,
				ErrorCode:     reasonCode,
				ErrorCodeEnum: mapStringToErrorCode(reasonCode),
				ErrorMessage:  reason,
				ResultPtr:     "",
				WorkerId:      "",
			},
		},
	}
	return e.bus.Publish(dlqSubject, packet)
}

// emitDLQWithRetry wraps emitDLQ with one retry on failure. On final failure,
// increments dlq_emit_failures_total metric.
func (e *Engine) emitDLQWithRetry(jobID, topic string, status pb.JobStatus, reason string, reasonCode string) error {
	err := e.emitDLQ(jobID, topic, status, reason, reasonCode)
	if err == nil {
		return nil
	}
	logging.Warn("scheduler", "dlq emit failed, retrying", "job_id", jobID, "error", err)
	retryTimer := time.NewTimer(500 * time.Millisecond)
	select {
	case <-e.ctx.Done():
		retryTimer.Stop()
		return e.ctx.Err()
	case <-retryTimer.C:
	}
	err = e.emitDLQ(jobID, topic, status, reason, reasonCode)
	if err != nil {
		logging.Error("scheduler", "dlq emit failed after retry", "job_id", jobID, "reason_code", reasonCode, "error", err)
		if e.metrics != nil {
			e.metrics.IncDLQEmitFailure(topic)
		}
	}
	return err
}

// mapStringToErrorCode converts legacy string error codes to the structured
// ErrorCode enum. Returns ERROR_CODE_UNSPECIFIED for unknown codes.
func mapStringToErrorCode(code string) pb.ErrorCode {
	switch code {
	case "approval_rejected", "policy_denied":
		return pb.ErrorCode_ERROR_CODE_SAFETY_DENIED
	case "policy_violation":
		return pb.ErrorCode_ERROR_CODE_SAFETY_POLICY_VIOLATION
	case "max_scheduling_retries":
		return pb.ErrorCode_ERROR_CODE_JOB_RESOURCE_EXHAUSTED
	case "timeout":
		return pb.ErrorCode_ERROR_CODE_JOB_TIMEOUT
	case "permission_denied":
		return pb.ErrorCode_ERROR_CODE_JOB_PERMISSION_DENIED
	default:
		return pb.ErrorCode_ERROR_CODE_UNSPECIFIED
	}
}

func (e *Engine) emitOutputAuditEvent(jobID, topic, code, reason string, decision OutputDecision) {
	if e == nil || e.bus == nil || strings.TrimSpace(jobID) == "" {
		return
	}
	msg := strings.TrimSpace(reason)
	if msg == "" {
		msg = "output policy event"
	}
	if topic != "" {
		msg = fmt.Sprintf("%s (topic=%s)", msg, topic)
	}
	status := pb.JobStatus_JOB_STATUS_DENIED
	if decision == OutputRedact {
		status = pb.JobStatus_JOB_STATUS_SUCCEEDED
	}
	packet := &pb.BusPacket{
		TraceId:         jobID,
		SenderId:        defaultSenderID,
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: protocolVersionV1,
		Payload: &pb.BusPacket_JobProgress{
			JobProgress: &pb.JobProgress{
				JobId:   jobID,
				StepId:  "output_policy",
				Percent: 100,
				Status:  status,
				Message: msg,
			},
		},
	}
	if err := e.bus.Publish(outputPolicyAudit, packet); err != nil {
		logging.Error("scheduler", "output audit event publish failed", "job_id", jobID, "code", code, "error", err)
	}
}
