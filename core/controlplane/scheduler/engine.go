package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"sync"
	"sync/atomic"

	capvalidate "github.com/cordum-io/cap/v2/sdk/go"
	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/topicregistry"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	"github.com/cordum/cordum/core/infra/config"
	cordumotel "github.com/cordum/cordum/core/infra/otel"
	infraSchema "github.com/cordum/cordum/core/infra/schema"
	infraStore "github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/licensing"
	"github.com/cordum/cordum/core/model"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/cordum/cordum/core/protocol/protoutil"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
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
	jobLockTTL          = 60 * time.Second
	retryDelayBusy      = 500 * time.Millisecond
	retryDelayStore     = 1 * time.Second
	retryDelayPublish   = 2 * time.Second
	retryDelayNoWorkers = 2 * time.Second
	safetyThrottleDelay = 5 * time.Second
	safetyCheckTimeout  = 3 * time.Second
	maxRenewalFailures  = 3
	workerCountKey      = "cordum:workers:count"
	workerCountTTL      = 2 * time.Minute

	// maxSchedulingRetries caps the number of scheduling attempts before
	// a job is moved to FAILED + DLQ. With exponential backoff (1s→30s max)
	// this allows ~25 minutes of retries before giving up.
	maxSchedulingRetries = 50
	maxDispatchRetries   = 3
	outputPolicyReason   = "output_quarantined"
	outputPolicyAsync    = "output_quarantined_async"
	outputPolicyAudit    = "sys.audit.output_policy"
)

var errApprovalHashFenceRead = errors.New("approval hash fence read failed")

// otelMetricsBridge is an optional interface for OTEL metrics dual-emission.
// Implemented by cordumotel.SchedulerMetricsBridge.
type otelMetricsBridge interface {
	RecordJobReceived(ctx context.Context, topic string)
	RecordJobCompleted(ctx context.Context, topic, status string)
	RecordSafetyDenied(ctx context.Context, topic string)
}

// Engine wires together bus interactions, safety checks, and scheduling decisions.
type Engine struct {
	bus                     Bus
	safety                  SafetyChecker
	outputSafety            OutputSafetyChecker
	outputSafetyEnabled     atomic.Bool
	asyncFailOpen           atomic.Bool // true = allow on output policy error, false = quarantine
	inputFailOpen           atomic.Bool // true = allow when kernel down, false = requeue
	registry                WorkerRegistry
	strategy                SchedulingStrategy
	jobStore                JobStore
	decisionLog             model.DecisionLogStore
	dlqSink                 DLQSink
	metrics                 Metrics
	config                  ConfigProvider
	topicRegistry           *topicregistry.Service
	workerCredentialCache   *WorkerCredentialCache
	sessionMiddleware       *SessionTokenMiddleware
	agentResolver           *AgentResolver
	workerAttestation       WorkerAttestationMode
	workerReadinessRequired bool
	schemaRegistry          *infraSchema.Registry
	schemaEnforcement       infraSchema.EnforcementMode
	saga                    *SagaManager
	entitlements            *licensing.EntitlementResolver
	contextClient           redis.UniversalClient // optional, for loading payloads referenced by ContextPtr
	failModeResolver        *FailModeResolver
	dispatchGate            *DispatchGate
	dispatchAuditSink       AuditSink
	trustMetrics            *WorkerTrustMetrics
	otelMetrics             otelMetricsBridge     // optional OTEL dual-emission bridge
	counterClient           redis.UniversalClient // optional, for operational counters shared across services
	stopped                 atomic.Bool
	activeHandlers          atomic.Int64
	activeRenewals          atomic.Int64
	wg                      sync.WaitGroup
	ctx                     context.Context
	cancel                  context.CancelFunc
	traceCtxMu              sync.Mutex
	lastTraceCtx            context.Context
	flushLatch              flushLatch // per-pool in-flight guard for flush-on-worker-online (task-7a2514ae)
	// flushDispatchFn can be swapped by tests via WithFlushDispatchFn. When
	// nil, the default defaultFlushDispatchForPool runs. The function
	// returns the number of pending jobs it dispatched.
	flushDispatchFn func(ctx context.Context, pool, traceID string) int
}

func jobLockKey(jobID string) string {
	if jobID == "" {
		return ""
	}
	return jobLockPrefix + jobID
}

// errLockAbandoned is the sentinel error used to cancel the fence context when
// lock renewal fails consecutively. Callers must check context.Cause to detect.
var errLockAbandoned = errors.New("job lock abandoned: renewal failed")

func (e *Engine) withJobLock(jobID string, ttl time.Duration, fn func(context.Context) error) error {
	if fn == nil {
		return nil
	}
	if e == nil || e.jobStore == nil || jobID == "" {
		return fn(e.ctx)
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
			slog.Error("job lock acquisition failed", "job_id", jobID, "error", err)
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

	// fenceCtx is cancelled when lock ownership can no longer be guaranteed.
	// All store operations inside fn must derive timeouts from fenceCtx.
	fenceCtx, fenceCancel := context.WithCancelCause(e.ctx) // #nosec G118 -- fenceCancel called in deferred cleanup below
	var abandoned atomic.Bool

	// Start lock renewal goroutine at ttl/3 interval to prevent expiry
	// during long-running safety checks or routing decisions.
	renewCtx, renewCancel := context.WithCancel(e.ctx)
	renewDone := make(chan struct{})
	go func() {
		defer close(renewDone)
		e.activeRenewals.Add(1)
		defer e.activeRenewals.Add(-1)
		ticker := time.NewTicker(ttl / 3)
		defer ticker.Stop()
		consecutiveFailures := 0
		for {
			select {
			case <-renewCtx.Done():
				return
			case <-ticker.C:
				rCtx, rCancel := context.WithTimeout(renewCtx, storeOpTimeout)
				if err := e.jobStore.RenewLock(rCtx, key, token, ttl); err != nil {
					consecutiveFailures++
					if consecutiveFailures >= maxRenewalFailures {
						slog.Error("lock renewal abandoned, fencing critical section",
							"job_id", jobID, "failures", consecutiveFailures, "error", err)
						abandoned.Store(true)
						fenceCancel(errLockAbandoned)
						if e.metrics != nil {
							e.metrics.IncJobLockAbandoned()
						}
						rCancel()
						return
					}
					slog.Warn("job lock renewal failed",
						"job_id", jobID, "attempt", consecutiveFailures, "error", err)
				} else {
					consecutiveFailures = 0
				}
				rCancel()
			}
		}
	}()
	defer func() {
		// Stop renewal goroutine BEFORE releasing lock to avoid
		// renewing a released lock. Wait for goroutine to exit to
		// ensure no in-flight renewal races with release.
		renewCancel()
		<-renewDone
		fenceCancel(nil) // no-op if already cancelled

		// Skip lock release after abandonment — another handler may
		// already hold the lock and releasing would drop their lock.
		if abandoned.Load() {
			slog.Warn("skipping lock release after abandonment",
				"job_id", jobID)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), storeOpTimeout)
		defer cancel()
		if err := e.jobStore.ReleaseLock(ctx, key, token); err != nil {
			slog.Warn("job lock release failed, retrying",
				"job_id", jobID, "error", err)
			ctx2, cancel2 := context.WithTimeout(context.Background(), storeOpTimeout)
			defer cancel2()
			if err2 := e.jobStore.ReleaseLock(ctx2, key, token); err2 != nil {
				slog.Error("job lock release retry failed, lock will expire via TTL",
					"job_id", jobID, "ttl", ttl, "error", err2)
			}
		}
	}()

	fnErr := fn(fenceCtx)
	// If lock was abandoned during fn execution, wrap the error to signal
	// that state mutations may be incomplete — do NOT retry, as another
	// handler may already be processing the same job.
	if abandoned.Load() {
		if fnErr != nil {
			return fmt.Errorf("lock abandoned during execution: %w", fnErr)
		}
		return errLockAbandoned
	}
	return fnErr
}

func NewEngine(bus Bus, safety SafetyChecker, registry WorkerRegistry, strategy SchedulingStrategy, jobStore JobStore, metrics Metrics) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	var contextClient redis.UniversalClient
	if provider, ok := jobStore.(interface{ Client() redis.UniversalClient }); ok {
		contextClient = provider.Client()
	}
	return &Engine{
		bus:                     bus,
		safety:                  safety,
		registry:                registry,
		strategy:                strategy,
		jobStore:                jobStore,
		decisionLog:             NoopDecisionLogStore{},
		metrics:                 metrics,
		contextClient:           contextClient,
		workerAttestation:       ParseWorkerAttestationMode(os.Getenv("WORKER_ATTESTATION")),
		workerReadinessRequired: workerReadinessRequiredFromEnv(),
		schemaEnforcement:       infraSchema.ParseEnforcementMode(os.Getenv("SCHEMA_ENFORCEMENT")),
		ctx:                     ctx,
		cancel:                  cancel,
	}
}

// WithConfig wires an optional effective config provider for dispatch-time injection.
func (e *Engine) WithConfig(cfg ConfigProvider) *Engine {
	e.config = cfg
	return e
}

// WithTopicRegistry wires the canonical topic registry used for direct bus validation.
func (e *Engine) WithTopicRegistry(registry *topicregistry.Service) *Engine {
	e.topicRegistry = registry
	return e
}

// WithDispatchGate wires the optional session-authority dispatch gate used by
// the heartbeat-demotion rollout. Nil preserves legacy heartbeat-TTL semantics.
func (e *Engine) WithDispatchGate(gate *DispatchGate) *Engine {
	if e == nil {
		return nil
	}
	e.dispatchGate = gate
	return e
}

// WithDispatchAuditSink wires the audit sink used for heartbeat disagreement
// events emitted during warn-mode dispatch.
func (e *Engine) WithDispatchAuditSink(sink AuditSink) *Engine {
	if e == nil {
		return nil
	}
	e.dispatchAuditSink = sink
	return e
}

// WithTrustMetrics wires the optional trust metrics bridge used by the
// heartbeat-demotion rollout.
func (e *Engine) WithTrustMetrics(metrics *WorkerTrustMetrics) *Engine {
	if e == nil {
		return nil
	}
	e.trustMetrics = metrics
	return e
}

func (e *Engine) WithSchemaRegistry(registry *infraSchema.Registry) *Engine {
	e.schemaRegistry = registry
	return e
}

func (e *Engine) WithWorkerCredentialCache(cache *WorkerCredentialCache) *Engine {
	e.workerCredentialCache = cache
	return e
}

func (e *Engine) WithAgentResolver(resolver *AgentResolver) *Engine {
	e.agentResolver = resolver
	return e
}

func (e *Engine) WithWorkerAttestationMode(mode WorkerAttestationMode) *Engine {
	e.workerAttestation = mode.Normalized()
	return e
}

// WithSessionMiddleware wires the Phase-2 session-token verification
// middleware. When set (handshake mode warn/enforce), inbound heartbeat /
// job_result / job_cancel packets are verified against the worker's
// session token via verifySessionToken; when nil the gate admits — the
// documented back-compat default for deployments that have not opted into
// CORDUM_SDK_HANDSHAKE. Mirrors WithWorkerAttestationMode.
func (e *Engine) WithSessionMiddleware(mw *SessionTokenMiddleware) *Engine {
	e.sessionMiddleware = mw
	return e
}

func (e *Engine) WithContextClient(client redis.UniversalClient) *Engine {
	e.contextClient = client
	return e
}

func (e *Engine) WithSchemaEnforcement(mode infraSchema.EnforcementMode) *Engine {
	e.schemaEnforcement = mode.Normalized()
	return e
}

func (e *Engine) schemaValidationMode() infraSchema.EnforcementMode {
	if e == nil {
		return infraSchema.EnforcementWarn
	}
	return e.schemaEnforcement.Normalized()
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

// WithCounterClient wires an optional Redis client for operational counters
// shared across services (e.g., fail-open count visible to gateway).
func (e *Engine) WithCounterClient(c redis.UniversalClient) *Engine {
	e.counterClient = c
	return e
}

// WithEntitlements wires the shared runtime entitlement resolver used for
// scheduler-side tier enforcement. Nil falls back to community defaults.
func (e *Engine) WithEntitlements(resolver *licensing.EntitlementResolver) *Engine {
	e.entitlements = resolver
	return e
}

func (e *Engine) currentEntitlements() licensing.Entitlements {
	if e != nil && e.entitlements != nil {
		return e.entitlements.Entitlements()
	}
	return licensing.DefaultEntitlements(licensing.PlanCommunity)
}

func (e *Engine) currentWorkerCount(ctx context.Context) (int, error) {
	if e == nil {
		return 0, nil
	}
	if e.counterClient != nil {
		count, err := e.counterClient.SCard(ctx, workerCountKey).Result()
		if err == nil {
			return int(count), nil
		}
		if !errors.Is(err, redis.Nil) {
			slog.Warn("scheduler worker count read failed", "error", err)
		}
	}
	if e.registry == nil {
		return 0, nil
	}
	return len(e.registry.Snapshot()), nil
}

func (e *Engine) persistWorkerCount(ctx context.Context) {
	if e == nil || e.counterClient == nil || e.registry == nil {
		return
	}
	snapshot := e.registry.Snapshot()
	pipe := e.counterClient.TxPipeline()
	pipe.Del(ctx, workerCountKey)
	if len(snapshot) > 0 {
		members := make([]any, 0, len(snapshot))
		for workerID := range snapshot {
			workerID = strings.TrimSpace(workerID)
			if workerID != "" {
				members = append(members, workerID)
			}
		}
		if len(members) > 0 {
			pipe.SAdd(ctx, workerCountKey, members...)
			pipe.Expire(ctx, workerCountKey, workerCountTTL)
		}
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		slog.Warn("scheduler worker count persist failed", "error", err)
	}
}

// WithAsyncFailMode sets the behavior when async output checks fail/timeout.
// "closed" (default) quarantines on error; "open" allows on error with a warning.
func (e *Engine) WithAsyncFailMode(mode string) *Engine {
	e.asyncFailOpen.Store(mode == "open")
	return e
}

// isAsyncFailOpen reports whether the engine allows output through when the
// async output safety check fails or times out.
func (e *Engine) isAsyncFailOpen() bool {
	return e.asyncFailOpen.Load()
}

// WithInputFailMode sets the behavior when the safety kernel is unreachable.
// "closed" (default) requeues the job; "open" allows the job through with a warning.
func (e *Engine) WithInputFailMode(mode string) *Engine {
	e.inputFailOpen.Store(mode == "open")
	return e
}

// WithFlushDispatchFn replaces the default flush-on-worker-online
// dispatch function. Used by tests to intercept the flush pipeline
// without exercising the full dispatch stack. The function signature
// is (ctx, pool, traceID) → dispatched-count; a nil fn restores the
// default (defaultFlushDispatchForPool). See task-7a2514ae.
func (e *Engine) WithFlushDispatchFn(fn func(ctx context.Context, pool, traceID string) int) *Engine {
	e.flushDispatchFn = fn
	return e
}

func (e *Engine) isInputFailOpen() bool {
	return e.inputFailOpen.Load()
}

// WithOTELMetrics wires an optional OTEL metrics bridge for dual-emission
// alongside Prometheus. When set, key scheduler events (job received, completed,
// safety denied) are recorded via both Prometheus and OTEL instruments.
func (e *Engine) WithOTELMetrics(bridge otelMetricsBridge) *Engine {
	e.otelMetrics = bridge
	return e
}

// WithFailModeResolver wires a per-tenant fail mode resolver. When set, the
// resolver's per-tenant overrides take precedence over the global atomic flags.
func (e *Engine) WithFailModeResolver(r *FailModeResolver) *Engine {
	e.failModeResolver = r
	return e
}

// FailModeResolver returns the per-tenant fail mode resolver, or nil if not
// configured. Used by the config reload loop to update system defaults.
func (e *Engine) FailModeResolver() *FailModeResolver {
	return e.failModeResolver
}

// isInputFailOpenForTenant returns the input fail mode for a specific tenant.
// If a FailModeResolver is configured, the per-tenant override is used.
// Otherwise, the global atomic flag is returned.
func (e *Engine) isInputFailOpenForTenant(orgID string) bool {
	if e.failModeResolver != nil {
		return e.failModeResolver.InputFailOpen(orgID)
	}
	return e.inputFailOpen.Load()
}

// isAsyncFailOpenForTenant returns the async output fail mode for a specific
// tenant. If a FailModeResolver is configured, the per-tenant override is used.
// Otherwise, the global atomic flag is returned.
func (e *Engine) isAsyncFailOpenForTenant(orgID string) bool {
	if e.failModeResolver != nil {
		return e.failModeResolver.AsyncFailOpen(orgID)
	}
	return e.asyncFailOpen.Load()
}

// isApprovalGateTopic returns true if the topic is a synthetic approval gate
// subject, not an actual worker topic. Only these topics should be auto-completed
// with synthetic SUCCEEDED results; worker topics must always be dispatched.
func isApprovalGateTopic(topic string) bool {
	return topic == capsdk.SubjectApprovalGate || topic == capsdk.SubjectWorkflowApprovalGate
}

// Start registers subscriptions for the scheduler.
func (e *Engine) Start() error {
	// Log resolved fail-mode configuration at startup so operators have a
	// durable record of which fail-mode origin will apply on safety-kernel
	// outage. Audit-fix task-2a52e7da finding #11 — surfaces "are we in
	// global fail-open vs per-tenant fail-open mode?" without grepping logs
	// for the per-job WARN line.
	failModeOrigin := "global_default"
	if e.failModeResolver != nil {
		failModeOrigin = "per_tenant_resolver"
	}
	slog.Info("scheduler input fail-mode resolved at startup",
		"input_fail_open", e.inputFailOpen.Load(),
		"async_fail_open", e.asyncFailOpen.Load(),
		"fail_mode_origin", failModeOrigin,
	)
	// Heartbeats must be broadcast to all schedulers to keep a complete view
	// of the worker pool when running multiple scheduler replicas.
	if err := e.bus.Subscribe(capsdk.SubjectHeartbeat, "", e.HandlePacket); err != nil {
		return fmt.Errorf("subscribe heartbeat: %w", err)
	}
	// Use context-aware subscriptions for job submit/result/cancel to propagate
	// W3C trace context from NATS headers into scheduler spans.
	if cs, ok := e.bus.(model.ContextSubscriber); ok {
		if err := cs.SubscribeWithContext(capsdk.SubjectSubmit, schedulerQueue, e.HandlePacketWithContext); err != nil {
			return fmt.Errorf("subscribe submit: %w", err)
		}
		if err := cs.SubscribeWithContext(capsdk.SubjectResult, schedulerQueue, e.HandlePacketWithContext); err != nil {
			return fmt.Errorf("subscribe result: %w", err)
		}
		if err := cs.SubscribeWithContext(capsdk.SubjectCancel, schedulerQueue, e.HandlePacketWithContext); err != nil {
			return fmt.Errorf("subscribe cancel: %w", err)
		}
	} else {
		if err := e.bus.Subscribe(capsdk.SubjectSubmit, schedulerQueue, e.HandlePacket); err != nil {
			return fmt.Errorf("subscribe submit: %w", err)
		}
		if err := e.bus.Subscribe(capsdk.SubjectResult, schedulerQueue, e.HandlePacket); err != nil {
			return fmt.Errorf("subscribe result: %w", err)
		}
		if err := e.bus.Subscribe(capsdk.SubjectCancel, schedulerQueue, e.HandlePacket); err != nil {
			return fmt.Errorf("subscribe cancel: %w", err)
		}
	}
	// Handshakes broadcast to all replicas (like heartbeats).
	if err := e.bus.Subscribe(capsdk.SubjectHandshake, "", e.HandlePacket); err != nil {
		return fmt.Errorf("subscribe handshake: %w", err)
	}
	if e.workerAttestationMode().Enabled() && e.workerCredentialCache != nil {
		if err := e.workerCredentialCache.Refresh(e.ctx); err != nil {
			slog.Warn("worker credential cache initial refresh failed", "error", err)
		}
		if err := e.bus.Subscribe(capsdk.SubjectConfigChanged, "", e.handleConfigChangedPacket); err != nil {
			return fmt.Errorf("subscribe config changed: %w", err)
		}
	}

	// Periodic registry stats logging for diagnostics
	type registryStatter interface {
		Stats() (int, map[string]int)
	}
	if statter, ok := e.registry.(registryStatter); ok {
		go func() {
			defer func() {
				if p := recover(); p != nil {
					slog.Error("panic in registry stats logger",
						"panic", p, "stack", string(debug.Stack()))
				}
			}()
			ticker := time.NewTicker(60 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-e.ctx.Done():
					return
				case <-ticker.C:
					total, byPool := statter.Stats()
					slog.Info("registry stats",
						"total_workers", total,
						"pools", fmt.Sprintf("%v", byPool),
					)
				}
			}
		}()
	}

	return nil
}

func (e *Engine) eligibleWorkers(ctx context.Context) (map[string]*pb.Heartbeat, []HeartbeatDisagreement) {
	if e == nil || e.registry == nil {
		return map[string]*pb.Heartbeat{}, nil
	}
	if ctx == nil {
		ctx = e.ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if e.dispatchGate == nil {
		return e.registry.Snapshot(), nil
	}
	return e.dispatchGate.EligibleWorkers(ctx, e.registry)
}

func (e *Engine) recordDispatchDisagreements(jobID, topic string, disagreements []HeartbeatDisagreement) {
	if len(disagreements) == 0 {
		return
	}
	for _, disagreement := range disagreements {
		e.emitHeartbeatDisagreement(jobID, topic, disagreement)
	}
}

func (e *Engine) emitHeartbeatDisagreement(jobID, topic string, disagreement HeartbeatDisagreement) {
	if e == nil {
		return
	}
	slog.Error("heartbeat/session disagreement during dispatch",
		"job_id", jobID,
		"topic", topic,
		"worker_id", disagreement.WorkerID,
		"tenant", disagreement.Tenant,
		"jti", disagreement.JTI,
		"direction", disagreement.Direction,
		"session_auth_alive", disagreement.SessionAuthAlive,
		"heartbeat_alive", disagreement.HeartbeatAlive,
	)
	if e.dispatchAuditSink == nil {
		return
	}
	ctx := e.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	extra := map[string]string{
		"worker_id":          disagreement.WorkerID,
		"tenant":             disagreement.Tenant,
		"jti":                disagreement.JTI,
		"direction":          disagreement.Direction,
		"session_auth_alive": fmt.Sprintf("%t", disagreement.SessionAuthAlive),
		"heartbeat_alive":    fmt.Sprintf("%t", disagreement.HeartbeatAlive),
	}
	if topic != "" {
		extra["topic"] = topic
	}
	e.dispatchAuditSink.Emit(ctx, audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: audit.EventHeartbeatDisagreement,
		Severity:  audit.SeverityMedium,
		TenantID:  disagreement.Tenant,
		AgentID:   disagreement.WorkerID,
		JobID:     jobID,
		Action:    "dispatch.heartbeat_disagreement",
		Reason:    disagreement.Direction,
		Extra:     extra,
	})
}

// emitApprovalRevisionMismatch records a SIEM event when the approval
// fast-path refuses to short-circuit to SafetyAllow because the
// approval_snapshot label on the resubmitted JobRequest does not match
// (base-compared) the PolicySnapshot currently stored on the job's
// SafetyDecisionRecord. Callers fall through to a full safety re-check;
// this event gives SIEM operators a tamper/stale-approval signal
// independent of whether the subsequent re-check allows or denies.
func (e *Engine) emitApprovalRevisionMismatch(req *pb.JobRequest, stored, asserted string) {
	if e == nil || req == nil {
		return
	}
	jobID := strings.TrimSpace(req.GetJobId())
	topic := strings.TrimSpace(req.GetTopic())
	if e.dispatchAuditSink == nil {
		return
	}
	ctx := e.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	extra := map[string]string{
		"stored_snapshot":   stored,
		"asserted_snapshot": asserted,
	}
	if topic != "" {
		extra["topic"] = topic
	}
	// Canonical tenant source is req.TenantId (set by the gateway from the
	// authenticated principal). Fall back to the "tenant" label only when
	// the request body omits it, so SIEM alerts are scoped correctly.
	tenant := strings.TrimSpace(req.TenantId)
	if tenant == "" && req.Labels != nil {
		tenant = strings.TrimSpace(req.Labels["tenant"])
	}
	e.dispatchAuditSink.Emit(ctx, audit.SIEMEvent{
		Timestamp:     time.Now().UTC(),
		EventType:     audit.EventApprovalRevisionMismatch,
		Severity:      audit.SeverityHigh,
		TenantID:      tenant,
		JobID:         jobID,
		Action:        "scheduler.approval_fast_path_reject",
		Reason:        "approval_snapshot label does not match stored PolicySnapshot",
		PolicyVersion: snapshotBase(stored),
		Extra:         extra,
	})
}

// HandlePacketWithContext is the context-aware version of HandlePacket.
// It receives trace context extracted from NATS headers and threads it
// through to processJob for distributed tracing continuity.
func (e *Engine) HandlePacketWithContext(ctx context.Context, p *pb.BusPacket) error {
	// Store the trace context so processJob can pick it up.
	// We merge the incoming trace context with the engine's lifecycle context.
	if ctx != nil && ctx != context.Background() {
		// Use the trace span from the incoming context if present.
		e.traceCtxMu.Lock()
		e.lastTraceCtx = ctx
		e.traceCtxMu.Unlock()
	}
	return e.HandlePacket(p)
}

func (e *Engine) HandlePacket(p *pb.BusPacket) (err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("scheduler: packet subscription panic",
				"panic", r,
				"stack", string(debug.Stack()),
				"trace_id", safeTraceID(p),
				"sender_id", safeSenderID(p),
			)
			err = RetryAfter(fmt.Errorf("scheduler packet handler panic: %v", r), retryDelayStore)
		}
	}()
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
		if !e.verifySessionToken(p, hb.GetWorkerId(), "heartbeat") {
			return nil
		}
		if !e.allowWorkerHeartbeat(p, hb) {
			return nil
		}
		slog.Info("heartbeat received",
			"worker_id", hb.WorkerId,
			"type", hb.Type,
			"cpu", hb.CpuLoad,
			"gpu", hb.GpuUtilization,
			"active_jobs", hb.ActiveJobs,
			"pool", hb.Pool,
		)
		isOnlineTransition := false
		if r, ok := e.registry.(interface {
			UpdateHeartbeatWithTransition(*pb.Heartbeat) bool
		}); ok {
			isOnlineTransition = r.UpdateHeartbeatWithTransition(hb)
		} else {
			e.registry.UpdateHeartbeat(hb)
		}
		if e.trustMetrics != nil {
			e.trustMetrics.ObserveHeartbeat(hb)
		}
		if e.counterClient != nil {
			ctx, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
			e.persistWorkerCount(ctx)
			cancel()
		}
		if isOnlineTransition {
			if pool := strings.TrimSpace(hb.GetPool()); pool != "" {
				e.scheduleFlushOnWorkerOnline(pool, hb.GetWorkerId(), p.GetTraceId())
			}
		}
		return nil
	case *pb.BusPacket_JobRequest:
		req := payload.JobRequest
		if req == nil {
			return nil
		}
		if err := capvalidate.ValidateJobRequest(req); err != nil {
			slog.Warn("invalid job request rejected",
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

		// Cross-check tenant: the gateway sets req.TenantId from the authenticated
		// org. If env["tenant_id"] carries a different value, a crafted request may
		// have injected a mismatched tenant. Log + deny.
		if authTenant := strings.TrimSpace(req.GetTenantId()); authTenant != "" {
			envTenant := strings.TrimSpace(req.GetEnv()["tenant_id"])
			if envTenant != "" && envTenant != authTenant {
				slog.Error("SECURITY: tenant mismatch between auth and env — rejecting job",
					"component", "scheduler", "job_id", req.GetJobId(),
					"auth_tenant", authTenant, "env_tenant", envTenant)
				if e.metrics != nil {
					e.metrics.IncValidationRejections()
				}
				return nil
			}
		}

		slog.Info("job request received",
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
		if !e.verifySessionToken(p, res.GetWorkerId(), "job_result") {
			return nil
		}
		if err := capvalidate.ValidateJobResult(res); err != nil {
			slog.Warn("invalid job result rejected",
				"job_id", res.GetJobId(),
				"validation_error", err.Error(),
				"trace_id", p.TraceId,
			)
			if e.metrics != nil {
				e.metrics.IncValidationRejections()
			}
			return nil
		}
		slog.Info("job result received",
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
		if !e.verifySessionToken(p, strings.TrimSpace(cancelReq.GetRequestedBy()), "job_cancel") {
			return nil
		}
		slog.Info("job cancel received",
			"job_id", cancelReq.JobId,
			"reason", cancelReq.Reason,
		)
		if e.jobStore != nil {
			ctx, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
			defer cancel()
			_, err := e.jobStore.CancelJob(ctx, cancelReq.JobId)
			if err != nil {
				slog.Error("cancel job failed",
					"job_id", cancelReq.JobId,
					"error", err,
				)
				if e.metrics != nil {
					e.metrics.IncJobCancelFailures()
				}
				return err // return error so NATS redelivers the message
			}
		}
		slog.Info("job cancelled",
			"job_id", cancelReq.JobId,
		)
		return nil
	case *pb.BusPacket_Handshake:
		hs := payload.Handshake
		if hs == nil {
			return nil
		}
		slog.Info("handshake received",
			"component_id", hs.ComponentId,
			"role", hs.Role.String(),
			"sdk_version", hs.SdkVersion,
			"supported_versions", hs.SupportedVersions,
			"ready_topics", readyTopicsFromHandshake(hs),
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

func (e *Engine) handleConfigChangedPacket(p *pb.BusPacket) (err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("scheduler: config subscription panic",
				"panic", r,
				"stack", string(debug.Stack()),
				"trace_id", safeTraceID(p),
				"sender_id", safeSenderID(p),
			)
			err = nil
		}
	}()
	if p == nil || e.workerCredentialCache == nil {
		return nil
	}
	alert := p.GetAlert()
	if alert == nil || !strings.EqualFold(strings.TrimSpace(alert.GetMessage()), "config changed") {
		return nil
	}
	scope := strings.TrimSpace(alert.GetDetails()["scope"])
	scopeID := strings.TrimSpace(alert.GetDetails()["scope_id"])
	if scope != string(configsvc.ScopeSystem) || scopeID != "workers" {
		return nil
	}
	parentCtx := e.ctx
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	go func(traceID, senderID, scope, scopeID string) {
		ctx, cancel := context.WithTimeout(parentCtx, 5*time.Second)
		defer cancel()
		if err := e.workerCredentialCache.Refresh(ctx); err != nil {
			slog.Warn("worker credential cache refresh failed",
				"scope", scope,
				"scope_id", scopeID,
				"trace_id", traceID,
				"sender_id", senderID,
				"error", err,
			)
			return
		}
	}(safeTraceID(p), safeSenderID(p), scope, scopeID)
	return nil
}

func (e *Engine) workerAttestationMode() WorkerAttestationMode {
	return e.workerAttestation.Normalized()
}

func (e *Engine) verifySessionToken(packet *pb.BusPacket, workerID, packetType string) bool {
	if e == nil || e.sessionMiddleware == nil {
		return true
	}
	ctx := e.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	result := e.sessionMiddleware.Verify(ctx, workerID, packet)
	switch result.Verdict {
	case TokenVerdictPass, TokenVerdictWarnMissing:
		if result.Err != nil {
			slog.Warn("session token missing; admitting packet",
				"packet_type", packetType,
				"worker_id", workerID,
				"mode", e.sessionMiddleware.Mode().String(),
				"error", result.Err,
			)
		}
		return true
	case TokenVerdictRejectMissing, TokenVerdictRejectInvalid:
		fields := []any{
			"packet_type", packetType,
			"worker_id", workerID,
			"mode", e.sessionMiddleware.Mode().String(),
			"verdict", result.Verdict.String(),
		}
		if result.Err != nil {
			fields = append(fields, "error", result.Err)
		}
		slog.Error("session token rejected inbound packet", fields...)
		return false
	default:
		return true
	}
}

func (e *Engine) allowWorkerHeartbeat(packet *pb.BusPacket, hb *pb.Heartbeat) bool {
	if hb == nil {
		return true
	}

	workerID := strings.TrimSpace(hb.GetWorkerId())
	mode := e.workerAttestationMode()
	if mode.Enabled() {
		senderID := safeSenderID(packet)
		token := authTokenFromPacket(packet)

		reject := func(reason string, record *workercredentials.Credential, err error) bool {
			fields := []any{
				"worker_id", workerID,
				"sender_id", senderID,
				"pool", strings.TrimSpace(hb.GetPool()),
				"mode", mode,
				"reason", reason,
			}
			if record != nil {
				fields = append(fields, "pack_id", record.PackID)
			}
			if err != nil {
				fields = append(fields, "error", err)
			}
			if mode.Enforced() {
				slog.Error("worker heartbeat rejected: attestation failed", fields...)
				return false
			}
			slog.Warn("worker heartbeat accepted without attestation", fields...)
			return true
		}

		if senderID == "" || senderID != workerID {
			return reject("sender_id_mismatch", nil, nil)
		}
		if e.workerCredentialCache == nil {
			return reject("credential_cache_unavailable", nil, nil)
		}
		if token == "" {
			return reject("auth_token_missing", nil, nil)
		}

		record, ok, err := e.workerCredentialCache.Verify(workerID, token)
		if err != nil {
			return reject("credential_verify_error", nil, err)
		}
		if !ok || record == nil {
			return reject("credential_invalid", nil, nil)
		}
		if !poolAllowed(record.AllowedPools, strings.TrimSpace(hb.GetPool())) {
			return reject("pool_not_allowed", record, nil)
		}
	}

	if workerID != "" && e.registry != nil && !e.registry.IsAlive(workerID) {
		countCtx, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
		defer cancel()
		currentWorkers, err := e.currentWorkerCount(countCtx)
		if err != nil {
			slog.Warn("worker limit check skipped: count unavailable",
				"worker_id", workerID,
				"error", err,
			)
			return true
		}
		if limitErr := licensing.CheckWorkerLimit(int64(currentWorkers+1), e.currentEntitlements()); limitErr != nil {
			slog.Warn("worker heartbeat rejected: tier worker limit exceeded",
				"worker_id", workerID,
				"current", currentWorkers,
				"allowed", limitErr.Allowed,
				"upgrade_url", limitErr.UpgradeURL,
			)
			return false
		}
	}
	return true
}

func poolAllowed(allowed []string, pool string) bool {
	if len(allowed) == 0 {
		return true
	}
	pool = strings.TrimSpace(pool)
	for _, candidate := range allowed {
		if strings.TrimSpace(candidate) == pool {
			return true
		}
	}
	return false
}

func authTokenFromPacket(packet *pb.BusPacket) string {
	if packet == nil {
		return ""
	}
	// Typed field first: cap/v2 SDK workers set the declared
	// BusPacket.auth_token (field 18); mirror cap/sdk/go
	// runtime.ExtractSessionToken so the worker-attestation gate reads the
	// same field the SDK writes (previously this saw only legacy unknown
	// fields and rejected every typed-token heartbeat).
	if token := strings.TrimSpace(packet.GetAuthToken()); token != "" {
		return token
	}
	// Legacy fallback: token encoded as an unknown field by older senders.
	raw := packet.ProtoReflect().GetUnknown()
	for len(raw) > 0 {
		fieldNum, wireType, tagLen := protowire.ConsumeTag(raw)
		if tagLen < 0 {
			return ""
		}
		raw = raw[tagLen:]
		if fieldNum == 18 && wireType == protowire.BytesType {
			value, valueLen := protowire.ConsumeBytes(raw)
			if valueLen < 0 {
				return ""
			}
			return strings.TrimSpace(string(value))
		}
		valueLen := protowire.ConsumeFieldValue(fieldNum, wireType, raw)
		if valueLen < 0 {
			return ""
		}
		raw = raw[valueLen:]
	}
	return ""
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
		slog.Warn("graceful shutdown deadline exceeded, some handlers still in-flight")
	}
}

// ActiveRenewals returns the current number of active lock renewal goroutines.
func (e *Engine) ActiveRenewals() int64 {
	return e.activeRenewals.Load()
}

func (e *Engine) handleJobRequest(req *pb.JobRequest, traceID string) error {
	if req == nil {
		return nil
	}

	jobID := strings.TrimSpace(req.JobId)
	topic := strings.TrimSpace(req.Topic)
	if jobID == "" || topic == "" {
		slog.Error("invalid job request",
			"trace_id", traceID,
			"job_id", safeJobID(req),
			"topic", safeTopic(req),
		)
		if err := e.setJobState(jobID, JobStateFailed); err != nil {
			slog.Error("state transition failed, retrying", "job_id", jobID, "target_state", JobStateFailed, "error", err)
			return RetryAfter(err, retryDelayStore)
		}
		e.incJobsCompleted(topic, pb.JobStatus_JOB_STATUS_FAILED.String())
		return nil
	}

	return e.withJobLock(jobID, jobLockTTL, func(lockCtx context.Context) error {
		if e.stopped.Load() {
			return nil
		}

		currentState := JobState("")
		if e.jobStore != nil {
			state, err := func() (JobState, error) {
				ctx, cancel := context.WithTimeout(lockCtx, storeOpTimeout)
				defer cancel()
				return e.jobStore.GetState(ctx, jobID)
			}()
			if err == nil {
				currentState = state
				if state == JobStateDispatched || state == JobStateRunning {
					return nil
				}
				if terminalStates[state] {
					// Job already in terminal state. This is a redelivery — likely
					// after a DLQ emit failure. Attempt best-effort DLQ emit for
					// states that require one (DENIED, FAILED). Duplicate DLQ
					// publishes are harmless (at-least-once NATS semantics).
					if state == JobStateDenied || state == JobStateFailed {
						dlqStatus := pb.JobStatus_JOB_STATUS_FAILED
						if state == JobStateDenied {
							dlqStatus = pb.JobStatus_JOB_STATUS_DENIED
						}
						if dlqErr := e.emitDLQWithRetry(jobID, topic, dlqStatus, "terminal state redelivery", "redelivery_dlq_retry"); dlqErr != nil {
							slog.Error("dlq emit failed on terminal redelivery", "job_id", jobID, "topic", topic, "error", dlqErr)
						}
					}
					return nil
				}
			} else if !errors.Is(err, redis.Nil) {
				// Non-nil, non-"key not found" error — fail closed to prevent
				// duplicate dispatch when we can't determine current state.
				slog.Error("state read failed, failing closed",
					"job_id", jobID, "error", err)
				return RetryAfter(err, retryDelayStore)
			}
		}

		e.incJobsReceived(topic)

		if e.jobStore != nil {
			if err := func() error {
				ctx, cancel := context.WithTimeout(lockCtx, storeOpTimeout)
				defer cancel()
				if traceID != "" {
					if err := e.jobStore.AddJobToTrace(ctx, traceID, jobID); err != nil {
						slog.Error("failed to add job to trace", "job_id", jobID, "trace_id", traceID, "error", err)
						return RetryAfter(err, retryDelayStore)
					}
				}
				if err := e.jobStore.SetJobMeta(ctx, req); err != nil {
					slog.Error("failed to persist job metadata", "job_id", jobID, "error", err)
					return RetryAfter(err, retryDelayStore)
				}
				if store, ok := e.jobStore.(interface {
					SetJobRequest(context.Context, *pb.JobRequest) error
				}); ok {
					if err := store.SetJobRequest(ctx, req); err != nil {
						slog.Error("failed to persist job request", "job_id", jobID, "error", err)
						return RetryAfter(err, retryDelayStore)
					}
				}
				return nil
			}(); err != nil {
				return err
			}
		}

		switch currentState {
		case "":
			if err := e.setJobState(jobID, JobStatePending); err != nil {
				return RetryAfter(err, retryDelayStore)
			}
		case JobStateScheduled:
			if inc, ok := e.jobStore.(interface {
				IncrAttempts(context.Context, string) error
			}); ok {
				ctx, cancel := context.WithTimeout(lockCtx, storeOpTimeout)
				if err := inc.IncrAttempts(ctx, jobID); err != nil {
					slog.Warn("attempt counter increment failed on scheduled replay", "job_id", jobID, "error", err)
				}
				cancel()
			}
		}

		if err := e.processJob(lockCtx, req, traceID); err != nil {
			// Retryable scheduling errors (no workers available) are ACKed — the
			// job is already persisted above and the reconciler will pick it up.
			if isRetryableSchedulingError(err) {
				return nil
			}
			// Other errors propagate to the bus: RetryAfter → NAK for retry,
			// plain error → ACK (non-retryable). Job metadata is already stored
			// at this point, so ACK on non-retryable error is safe.
			return err
		}
		return nil
	})
}

func (e *Engine) processJob(lockCtx context.Context, req *pb.JobRequest, traceID string) error {
	tracer := cordumotel.Tracer("cordum-scheduler")
	// Use upstream trace context (from NATS headers) as parent if available,
	// so scheduler spans become children of the gateway/submitter trace.
	e.traceCtxMu.Lock()
	parentCtx := e.lastTraceCtx
	e.lastTraceCtx = nil
	e.traceCtxMu.Unlock()
	if parentCtx != nil {
		lockCtx = oteltrace.ContextWithSpanContext(lockCtx, oteltrace.SpanContextFromContext(parentCtx))
	}
	lockCtx, span := tracer.Start(lockCtx, "scheduler.dispatch",
		oteltrace.WithSpanKind(oteltrace.SpanKindInternal),
	)
	defer span.End()
	if traceID != "" {
		span.SetAttributes(attribute.String("cordum.packet_trace_id", traceID))
	}
	if req != nil {
		span.SetAttributes(
			attribute.String("cordum.job_id", strings.TrimSpace(req.JobId)),
			attribute.String("cordum.topic", strings.TrimSpace(req.Topic)),
		)
		if req.TenantId != "" {
			span.SetAttributes(attribute.String("cordum.tenant", req.TenantId))
		}
		if req.Labels != nil {
			if agentID := strings.TrimSpace(req.Labels["agent_id"]); agentID != "" {
				span.SetAttributes(attribute.String("cordum.agent_id", agentID))
			}
		}
	}

	// Dual-emit: record job received in OTEL alongside Prometheus.
	if e.otelMetrics != nil && req != nil {
		e.otelMetrics.RecordJobReceived(lockCtx, strings.TrimSpace(req.Topic))
	}

	if req == nil || strings.TrimSpace(req.JobId) == "" || strings.TrimSpace(req.Topic) == "" {
		slog.Error("invalid job request",
			"trace_id", traceID,
			"job_id", safeJobID(req),
			"topic", safeTopic(req),
		)
		if err := e.setJobState(safeJobID(req), JobStateFailed); err != nil {
			slog.Error("state transition failed, retrying", "job_id", safeJobID(req), "target_state", JobStateFailed, "error", err)
			return RetryAfter(err, retryDelayStore)
		}
		e.incJobsCompleted(safeTopic(req), pb.JobStatus_JOB_STATUS_FAILED.String())
		return nil
	}

	jobID := strings.TrimSpace(req.JobId)
	topic := strings.TrimSpace(req.Topic)
	dispatchStart := time.Now()
	reg, registryEmpty, err := e.topicRegistration(lockCtx, req.GetTenantId(), topic)
	if err != nil {
		return RetryAfter(err, retryDelayStore)
	} else if !registryEmpty && (reg == nil || reg.Status == topicregistry.StatusDisabled) {
		slog.Warn("unknown topic rejected",
			"job_id", jobID,
			"topic", topic,
			"trace_id", traceID,
		)
		if err := e.setJobState(jobID, JobStateFailed); err != nil {
			return RetryAfter(err, retryDelayStore)
		}
		e.incJobsCompleted(topic, pb.JobStatus_JOB_STATUS_FAILED.String())
		if err := e.emitDLQWithRetry(jobID, topic, pb.JobStatus_JOB_STATUS_FAILED, "unknown topic", "unknown_topic"); err != nil {
			return RetryAfter(err, retryDelayPublish)
		}
		return nil
	}
	if handled, err := e.applySubmitSchemaValidation(lockCtx, req, traceID, reg); err != nil {
		return err
	} else if handled {
		return nil
	}

	// Fetch attempt count for exponential backoff on retries.
	attempts := 0
	if e.jobStore != nil {
		ctx, cancel := context.WithTimeout(lockCtx, storeOpTimeout)
		a, err := e.jobStore.GetAttempts(ctx, jobID)
		cancel()
		if errors.Is(err, redis.Nil) {
			attempts = 0
		} else if err != nil {
			slog.Warn("scheduler: GetAttempts error, treating as max scheduling retries",
				"job_id", jobID,
				"topic", topic,
				"trace_id", traceID,
				"max_attempts", maxSchedulingRetries,
				"error", err,
			)
			attempts = maxSchedulingRetries
		} else {
			attempts = a
		}
	}

	// Give up after maxSchedulingRetries to prevent infinite NAK loops
	// (e.g. job.default with no workers). Job goes to DLQ for investigation.
	if attempts >= maxSchedulingRetries {
		reason := fmt.Sprintf("max scheduling retries exceeded (attempts=%d)", attempts)
		slog.Warn("giving up on job", "job_id", jobID, "topic", topic, "attempts", attempts)
		if err := e.setJobState(jobID, JobStateFailed); err != nil {
			slog.Error("state transition failed, retrying", "job_id", jobID, "target_state", JobStateFailed, "error", err)
			return RetryAfter(err, retryDelayStore)
		}
		if err := e.emitDLQWithRetry(jobID, topic, pb.JobStatus_JOB_STATUS_FAILED, reason, "max_scheduling_retries"); err != nil {
			slog.Error("dlq emit failed, retrying", "job_id", jobID, "error", err)
			return RetryAfter(err, retryDelayPublish)
		}
		e.incJobsCompleted(topic, pb.JobStatus_JOB_STATUS_FAILED.String())
		return nil
	}

	e.attachEffectiveConfig(req)

	verifyCtx, verifyCancel := context.WithTimeout(lockCtx, storeOpTimeout)
	lineage, hasDelegation, delegationErr := e.verifyDelegationBeforeDispatch(verifyCtx, req)
	verifyCancel()
	if delegationErr != nil {
		reason, reasonCode, retry := classifyDelegationDispatchError(delegationErr)
		slog.Warn("delegation dispatch verification failed",
			"job_id", jobID,
			"topic", topic,
			"reason_code", reasonCode,
			"error", delegationErr,
		)
		if retry {
			return RetryAfter(delegationErr, retryDelayStore)
		}
		if hasDelegation {
			e.emitDelegationDispatchFailureAudit(lockCtx, req, reasonCode)
		}
		if hasDelegation && e.jobStore != nil {
			ctx, cancel := context.WithTimeout(lockCtx, storeOpTimeout)
			failureReason := reason
			if reasonCode != "" {
				failureReason = reasonCode
			}
			if err := e.jobStore.SetFailureReason(ctx, jobID, failureReason); err != nil {
				slog.Warn("failed to persist delegation dispatch failure reason", "job_id", jobID, "error", err)
			}
			cancel()
		}
		if err := e.setJobState(jobID, JobStateFailed); err != nil {
			slog.Error("state transition failed, retrying", "job_id", jobID, "target_state", JobStateFailed, "error", err)
			return RetryAfter(err, retryDelayStore)
		}
		e.incJobsCompleted(topic, pb.JobStatus_JOB_STATUS_FAILED.String())
		if err := e.emitDLQWithRetry(jobID, topic, pb.JobStatus_JOB_STATUS_FAILED, reason, reasonCode); err != nil {
			slog.Error("dlq emit failed, retrying", "job_id", jobID, "error", err)
			return RetryAfter(err, retryDelayPublish)
		}
		return nil
	}
	if hasDelegation {
		e.emitDelegationLineageAudit(lockCtx, req, lineage)
	}

	record, err := e.checkSafetyDecisionTraced(lockCtx, req)
	if err != nil {
		if errors.Is(err, errApprovalHashFenceRead) {
			slog.Error("approval hash fence read failed, retrying without fail-open",
				"job_id", jobID, "topic", topic, "error", err, "trace_id", traceID)
			return RetryAfter(err, retryDelayStore)
		}
		slog.Error("safety check failed", "job_id", jobID, "error", err)
		record.Decision = SafetyUnavailable
	}
	switch record.Decision {
	case SafetyAllow, SafetyAllowWithConstraints:
		if record.Constraints != nil {
			applyConstraints(req, record.Constraints)
		}
		// Approval gate auto-complete: when an approval gate job is processed,
		// publish a synthetic success result instead of dispatching to workers.
		// IMPORTANT: Only auto-complete actual approval gate topics, NOT regular
		// worker jobs that went through the approval flow. Worker jobs with
		// reason "approval granted" must still be dispatched to their handlers.
		if isApprovalGateTopic(topic) {
			pkt := &pb.BusPacket{
				TraceId:         traceID,
				SenderId:        defaultSenderID,
				CreatedAt:       timestamppb.Now(),
				ProtocolVersion: protocolVersionV1,
				Payload: &pb.BusPacket_JobResult{
					JobResult: &pb.JobResult{
						JobId:    jobID,
						WorkerId: defaultSenderID,
						Status:   pb.JobStatus_JOB_STATUS_SUCCEEDED,
					},
				},
			}
			if e.bus != nil {
				if err := e.bus.Publish(capsdk.SubjectResult, pkt); err != nil {
					return RetryAfter(err, retryDelayPublish)
				}
			}
			if err := e.setJobState(jobID, JobStateSucceeded); err != nil {
				return RetryAfter(err, retryDelayStore)
			}
			return nil
		}
	case SafetyThrottle:
		slog.Info("job throttled",
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
		if e.metrics != nil {
			e.metrics.IncSafetyUnavailable(topic)
		}
		if e.isInputFailOpenForTenant(req.GetTenantId()) {
			slog.Warn("safety kernel unavailable — FAIL-OPEN: allowing job",
				"job_id", jobID, "topic", topic, "reason", record.Reason,
				"trace_id", traceID, "input_fail_mode", "open")
			if e.metrics != nil {
				e.metrics.IncInputFailOpen(topic)
			}
			if e.counterClient != nil {
				e.counterClient.Incr(lockCtx, "cordum:scheduler:input_fail_open_total")
			}
			// Emit dedicated audit event so SIEM can detect bypass-on-unavailable
			// without inferring it from WARN logs. The event carries fail_mode_origin
			// so reviewers can distinguish global-default fail-open (env var) from
			// per-tenant fail-open (FailModeResolver). Audit-fix task-2a52e7da #11.
			e.emitSafetyBypassAuditEvent(lockCtx, req, topic, record.Reason)
			record.Decision = SafetyAllow
			record.Reason = "fail-open: safety unavailable — " + record.Reason
			// Tag job so dashboard can show bypass warning
			if req.Labels == nil {
				req.Labels = map[string]string{}
			}
			req.Labels["safety_bypassed"] = "true"
			req.Labels["safety_bypass_reason"] = record.Reason
			// Fall through to allow
		} else {
			slog.Warn("safety kernel unavailable, requeueing job",
				"job_id", jobID, "topic", topic, "reason", record.Reason,
				"trace_id", traceID, "input_fail_mode", "closed")
			return RetryAfter(fmt.Errorf("safety unavailable: %s", record.Reason), safetyThrottleDelay)
		}
	case SafetyRequireApproval:
		slog.Info("job requires human approval",
			"job_id", jobID,
			"topic", topic,
			"reason", record.Reason,
			"trace_id", traceID,
		)
		if err := e.setJobState(jobID, JobStateApproval); err != nil {
			slog.Error("state transition failed, retrying", "job_id", jobID, "target_state", JobStateApproval, "error", err)
			return RetryAfter(err, retryDelayStore)
		}
		return nil
	case SafetyDeny:
		slog.Info("job denied",
			"job_id", jobID,
			"topic", topic,
			"reason", record.Reason,
			"trace_id", traceID,
		)
		if err := e.setJobState(jobID, JobStateDenied); err != nil {
			slog.Error("state transition failed, retrying", "job_id", jobID, "target_state", JobStateDenied, "error", err)
			return RetryAfter(err, retryDelayStore)
		}
		e.incSafetyDenied(topic)
		if err := e.emitDLQWithRetry(jobID, topic, pb.JobStatus_JOB_STATUS_DENIED, record.Reason, "safety_denied"); err != nil {
			slog.Error("dlq emit failed, retrying", "job_id", jobID, "error", err)
			return RetryAfter(err, retryDelayPublish)
		}
		return nil
	default:
		slog.Info("job denied (unknown decision)",
			"job_id", jobID,
			"topic", topic,
			"reason", record.Reason,
			"trace_id", traceID,
		)
		if err := e.setJobState(jobID, JobStateDenied); err != nil {
			slog.Error("state transition failed, retrying", "job_id", jobID, "target_state", JobStateDenied, "error", err)
			return RetryAfter(err, retryDelayStore)
		}
		e.incSafetyDenied(topic)
		if err := e.emitDLQWithRetry(jobID, topic, pb.JobStatus_JOB_STATUS_DENIED, record.Reason, "safety_unknown"); err != nil {
			slog.Error("dlq emit failed, retrying", "job_id", jobID, "error", err)
			return RetryAfter(err, retryDelayPublish)
		}
		return nil
	}

	if maxRetries := maxRetriesFromConstraints(record.Constraints); maxRetries > 0 && e.jobStore != nil {
		ctx, cancel := context.WithTimeout(lockCtx, storeOpTimeout)
		attempts, err := e.jobStore.GetAttempts(ctx, jobID)
		cancel()
		allowedAttempts := int(maxRetries) + 1
		if errors.Is(err, redis.Nil) {
			attempts = 0
		} else if err != nil {
			slog.Warn("scheduler: GetAttempts error, treating as max policy retries",
				"job_id", jobID,
				"topic", topic,
				"trace_id", traceID,
				"max_retries", maxRetries,
				"allowed_attempts", allowedAttempts,
				"error", err,
			)
			attempts = allowedAttempts
		}
		if attempts >= allowedAttempts {
			reason := fmt.Sprintf("max retries exceeded (attempts=%d, max_retries=%d)", attempts, maxRetries)
			if err := e.setJobState(jobID, JobStateFailed); err != nil {
				slog.Error("state transition failed, retrying", "job_id", jobID, "target_state", JobStateFailed, "error", err)
				return RetryAfter(err, retryDelayStore)
			}
			if err := e.emitDLQWithRetry(jobID, topic, pb.JobStatus_JOB_STATUS_FAILED, reason, "max_retries_exceeded"); err != nil {
				slog.Error("dlq emit failed, retrying", "job_id", jobID, "error", err)
				return RetryAfter(err, retryDelayPublish)
			}
			return nil
		}
	}

	if maxConcurrent := maxConcurrentFromConstraints(record.Constraints); maxConcurrent > 0 && e.jobStore != nil {
		tenant := ExtractTenant(req)
		ctx, cancel := context.WithTimeout(lockCtx, storeOpTimeout)
		active, err := e.jobStore.CountActiveByTenant(ctx, tenant)
		cancel()
		if err != nil {
			return RetryAfter(err, retryDelayStore)
		}
		if int64(active) >= maxConcurrent {
			slog.Info("tenant concurrency limit reached",
				"job_id", jobID,
				"tenant", tenant,
				"active", active,
				"limit", maxConcurrent,
			)
			return RetryAfter(ErrTenantLimit, retryDelayNoWorkers)
		}
	}
	if e.jobStore != nil {
		tenant := ExtractTenant(req)
		if strings.TrimSpace(tenant) != "" {
			ctx, cancel := context.WithTimeout(lockCtx, storeOpTimeout)
			active, err := e.jobStore.CountActiveByTenant(ctx, tenant)
			cancel()
			if err != nil {
				return RetryAfter(err, retryDelayStore)
			}
			if limitErr := licensing.CheckJobConcurrency(int64(active+1), e.currentEntitlements()); limitErr != nil {
				slog.Info("tier concurrency limit reached",
					"job_id", jobID,
					"tenant", tenant,
					"active", active,
					"allowed", limitErr.Allowed,
					"upgrade_url", limitErr.UpgradeURL,
				)
				return RetryAfter(limitErr, retryDelayNoWorkers)
			}
		}
	}

	if budget := req.GetBudget(); budget != nil && budget.GetDeadlineMs() > 0 && e.jobStore != nil {
		ctx, cancel := context.WithTimeout(lockCtx, storeOpTimeout)
		err := e.jobStore.SetDeadline(ctx, jobID, time.Now().Add(time.Duration(budget.GetDeadlineMs())*time.Millisecond))
		cancel()
		if err != nil {
			return RetryAfter(err, retryDelayStore)
		}
	}

	workers, disagreements := e.eligibleWorkers(lockCtx)
	e.recordDispatchDisagreements(jobID, topic, disagreements)
	var readiness map[string]WorkerReadiness
	if e.workerReadinessRequired {
		readiness = e.registry.ReadinessSnapshot()
	}
	if len(workers) == 0 {
		slog.Warn("no workers in registry",
			"topic", topic,
			"job_id", jobID,
		)
	}
	var subject string
	for dispatchAttempt := range maxDispatchRetries {
		if dispatchAttempt > 0 {
			workers, disagreements = e.eligibleWorkers(lockCtx)
			e.recordDispatchDisagreements(jobID, topic, disagreements)
			if e.workerReadinessRequired {
				readiness = e.registry.ReadinessSnapshot()
			}
		}
		subject, err = e.strategy.PickSubject(req, workers, readiness)
		if err != nil {
			break
		}
		workerID := extractWorkerFromSubject(subject)
		if workerID == "" {
			break
		}
		if e.dispatchGate == nil {
			if e.registry.IsAlive(workerID) {
				break
			}
		} else {
			eligible, disagreement := e.dispatchGate.IsWorkerEligible(lockCtx, workerID, e.registry.IsAlive)
			if disagreement != nil {
				e.recordDispatchDisagreements(jobID, topic, []HeartbeatDisagreement{*disagreement})
			}
			if eligible {
				break
			}
		}
		slog.Warn("stale worker detected, retrying dispatch",
			"job_id", jobID,
			"worker", workerID,
			"attempt", dispatchAttempt+1,
			"topic", topic,
		)
		subject = ""
		err = fmt.Errorf("%w: pool %q (stale worker %s)", ErrNoWorkers, topic, workerID)
	}
	if err != nil {
		if errors.Is(err, ErrNoPoolMapping) {
			slog.Warn("no pool mapping for topic, will retry",
				"job_id", jobID,
				"topic", topic,
				"error", err,
			)
		} else {
			slog.Error("failed to pick subject",
				"job_id", jobID,
				"topic", topic,
				"error", err,
			)
		}
		if isRetryableSchedulingError(err) {
			if inc, ok := e.jobStore.(interface {
				IncrAttempts(context.Context, string) error
			}); ok {
				ctx2, cancel := context.WithTimeout(lockCtx, storeOpTimeout)
				if incrErr := inc.IncrAttempts(ctx2, jobID); incrErr != nil {
					slog.Warn("attempt counter increment failed", "job_id", jobID, "error", incrErr)
				}
				cancel()
			}
			return RetryAfter(err, backoffDelay(attempts, backoffBase, backoffMax))
		}
		dispatchErr := err // capture before setJobState shadows err
		if err := e.setJobState(jobID, JobStateFailed); err != nil {
			slog.Error("state transition failed, retrying", "job_id", jobID, "target_state", JobStateFailed, "error", err)
			return RetryAfter(err, retryDelayStore)
		}
		e.incJobsCompleted(topic, pb.JobStatus_JOB_STATUS_FAILED.String())
		if err := e.emitDLQWithRetry(jobID, topic, pb.JobStatus_JOB_STATUS_FAILED, dispatchErr.Error(), reasonCodeForSchedulingError(dispatchErr)); err != nil {
			slog.Error("dlq emit failed, retrying", "job_id", jobID, "error", err)
			return RetryAfter(err, retryDelayPublish)
		}
		return nil
	}

	if err := e.setJobState(jobID, JobStateScheduled); err != nil {
		return RetryAfter(err, retryDelayStore)
	}

	if e.bus == nil {
		return RetryAfter(fmt.Errorf("bus unavailable"), retryDelayPublish)
	}

	// Set DISPATCHED state BEFORE publishing to NATS to prevent duplicate
	// dispatch on redelivery. If publish fails, we roll back to SCHEDULED.
	if err := e.setJobState(jobID, JobStateDispatched); err != nil {
		return RetryAfter(err, retryDelayStore)
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

	publishErr := e.publishWithTrace(lockCtx, subject, packet)
	if err := publishErr; err != nil {
		slog.Error("failed to publish job, rolling back to SCHEDULED",
			"job_id", jobID,
			"subject", subject,
			"error", err,
		)
		if rbErr := e.setJobState(jobID, JobStateScheduled); rbErr != nil {
			slog.Error("dispatch rollback failed",
				"job_id", jobID, "error", rbErr)
		}
		if e.metrics != nil {
			e.metrics.IncDispatchRollback(topic)
		}
		return RetryAfter(err, backoffDelay(attempts, backoffBase, backoffMax))
	}

	e.incJobsDispatched(topic)
	if e.metrics != nil {
		e.metrics.ObserveDispatchLatency(topic, time.Since(dispatchStart).Seconds())
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
	var limitErr *licensing.TierLimitError
	if errors.As(err, &limitErr) {
		return true
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
	var limitErr *licensing.TierLimitError
	switch {
	case errors.As(err, &limitErr):
		return "tier_limit_exceeded"
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

func (e *Engine) topicRegistration(ctx context.Context, tenantID, topic string) (*topicregistry.Registration, bool, error) {
	if e == nil {
		return nil, true, nil
	}
	if e.topicRegistry == nil {
		mode := e.schemaValidationMode()
		if mode.Enforced() {
			return nil, false, fmt.Errorf("topic registry unavailable")
		}
		if mode == infraSchema.EnforcementWarn {
			slog.Warn("topic registry unavailable; skipping topic/schema validation",
				"topic", strings.TrimSpace(topic),
				"mode", mode,
			)
		}
		return nil, true, nil
	}
	return e.topicRegistry.GetForTenant(ctx, tenantID, topic)
}

func (e *Engine) applySubmitSchemaValidation(ctx context.Context, req *pb.JobRequest, traceID string, reg *topicregistry.Registration) (bool, error) {
	mode := e.schemaValidationMode()
	if !mode.Enabled() || reg == nil {
		return false, nil
	}
	schemaID := strings.TrimSpace(reg.InputSchemaID)
	if schemaID == "" {
		return false, nil
	}

	payloadJSON, violations, err := e.loadSubmitValidationPayload(ctx, req)
	if err != nil {
		return false, RetryAfter(err, retryDelayStore)
	}
	if len(violations) > 0 {
		return e.handleSchemaViolations(req, traceID, schemaID, mode, violations)
	}
	if e.schemaRegistry == nil {
		return e.failSchemaValidation(req, traceID, schemaID, "schema registry unavailable", "schema_registry_unavailable")
	}
	schemaJSON, err := e.schemaRegistry.Get(ctx, schemaID)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return e.failSchemaValidation(req, traceID, schemaID, "topic input schema not found", "schema_not_found")
		}
		return false, RetryAfter(err, retryDelayStore)
	}
	violations, err = infraSchema.ValidateJSONPayload(ctx, e.schemaRegistry, schemaID, schemaJSON, payloadJSON)
	if err != nil {
		return e.failSchemaValidation(req, traceID, schemaID, "topic input schema invalid", "schema_invalid")
	}
	if len(violations) == 0 {
		return false, nil
	}
	return e.handleSchemaViolations(req, traceID, schemaID, mode, violations)
}

func (e *Engine) loadSubmitValidationPayload(ctx context.Context, req *pb.JobRequest) ([]byte, []infraSchema.Violation, error) {
	if req == nil {
		return []byte("null"), nil, nil
	}
	ptr := strings.TrimSpace(req.GetContextPtr())
	if ptr == "" {
		return nil, []infraSchema.Violation{{Path: "$.context_ptr", Message: "context payload required for schema validation"}}, nil
	}
	if e == nil || e.contextClient == nil {
		return nil, []infraSchema.Violation{{Path: "$.context_ptr", Message: "context payload unavailable for schema validation"}}, nil
	}
	key, err := infraStore.KeyFromPointer(ptr)
	if err != nil {
		return nil, []infraSchema.Violation{{Path: "$.context_ptr", Message: "invalid context pointer"}}, nil
	}
	data, err := e.contextClient.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, []infraSchema.Violation{{Path: "$.context_ptr", Message: "context payload not found"}}, nil
		}
		return nil, nil, fmt.Errorf("load context payload for %s: %w", strings.TrimSpace(req.GetJobId()), err)
	}
	return data, nil, nil
}

func (e *Engine) handleSchemaViolations(req *pb.JobRequest, traceID, schemaID string, mode infraSchema.EnforcementMode, violations []infraSchema.Violation) (bool, error) {
	if len(violations) == 0 {
		return false, nil
	}
	jobID := strings.TrimSpace(req.GetJobId())
	topic := strings.TrimSpace(req.GetTopic())
	if mode.Enforced() {
		slog.Warn("job request rejected by schema validation",
			"job_id", jobID,
			"topic", topic,
			"schema_id", schemaID,
			"trace_id", traceID,
			"violations", violations,
		)
		if err := e.setJobState(jobID, JobStateFailed); err != nil {
			return false, RetryAfter(err, retryDelayStore)
		}
		e.incJobsCompleted(topic, pb.JobStatus_JOB_STATUS_FAILED.String())
		if err := e.emitDLQWithRetry(jobID, topic, pb.JobStatus_JOB_STATUS_FAILED, "schema validation failed", "schema_validation_failed"); err != nil {
			return false, RetryAfter(err, retryDelayPublish)
		}
		return true, nil
	}
	slog.Warn("job request violated topic input schema",
		"job_id", jobID,
		"topic", topic,
		"schema_id", schemaID,
		"trace_id", traceID,
		"mode", mode,
		"violations", violations,
	)
	return false, nil
}

func (e *Engine) failSchemaValidation(req *pb.JobRequest, traceID, schemaID, reason, reasonCode string) (bool, error) {
	jobID := strings.TrimSpace(req.GetJobId())
	topic := strings.TrimSpace(req.GetTopic())
	slog.Error("schema validation unavailable for job request",
		"job_id", jobID,
		"topic", topic,
		"schema_id", schemaID,
		"trace_id", traceID,
		"reason", reason,
	)
	if err := e.setJobState(jobID, JobStateFailed); err != nil {
		return false, RetryAfter(err, retryDelayStore)
	}
	e.incJobsCompleted(topic, pb.JobStatus_JOB_STATUS_FAILED.String())
	if err := e.emitDLQWithRetry(jobID, topic, pb.JobStatus_JOB_STATUS_FAILED, reason, reasonCode); err != nil {
		return false, RetryAfter(err, retryDelayPublish)
	}
	return true, nil
}

func (e *Engine) checkSafetyDecisionTraced(ctx context.Context, req *pb.JobRequest) (SafetyDecisionRecord, error) {
	tracer := cordumotel.Tracer("cordum-scheduler")
	_, safetySpan := tracer.Start(ctx, "scheduler.safety_check",
		oteltrace.WithSpanKind(oteltrace.SpanKindClient),
	)
	defer safetySpan.End()

	if req != nil {
		safetySpan.SetAttributes(attribute.String("cordum.job_id", strings.TrimSpace(req.JobId)))
	}

	record, err := e.checkSafetyDecision(ctx, req)
	if err != nil {
		safetySpan.SetStatus(otelcodes.Error, err.Error())
		safetySpan.RecordError(err)
	} else {
		safetySpan.SetAttributes(
			attribute.String("cordum.safety_decision", string(record.Decision)),
			attribute.String("cordum.safety_rule_id", record.RuleID),
		)
	}
	return record, err
}

// checkSafetyDecision evaluates safety for a job. The caller's ctx MUST be the
// lock-fenced context from withJobLock — any state mutations derived from this
// call (notably the approval fast-path SetSafetyDecision) must be cancelled
// when the lock is lost to prevent stale-lock writes racing a takeover handler.
func (e *Engine) checkSafetyDecision(ctx context.Context, req *pb.JobRequest) (SafetyDecisionRecord, error) {
	if ctx == nil {
		ctx = e.ctx
	}
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
			storeCtx, cancel := context.WithTimeout(ctx, storeOpTimeout)
			defer cancel()
			prev, err := e.jobStore.GetSafetyDecision(storeCtx, jobID)
			if err == nil && (prev.ApprovalRequired || prev.Decision == SafetyRequireApproval) && prev.JobHash != "" {
				hash, err := HashJobRequest(req)
				if err == nil && hash == prev.JobHash {
					// Bind the fast-path to the PolicySnapshot this approval
					// was admitted against. The gateway stamps approval_snapshot
					// onto the resubmitted request when it resolves the
					// approval; we compare it (base only, to ignore config-
					// overlay churn) against the stored safety record's
					// PolicySnapshot — which ResolveApproval rotates to the
					// human-approved snapshot on approve. A mismatch means the
					// stored safety decision drifted after admission (reconciler
					// invalidated it, or the label predates this binding), so
					// we fall through to a full safety check instead of
					// short-circuiting to SafetyAllow.
					asserted := strings.TrimSpace(req.Labels["approval_snapshot"])
					stored := strings.TrimSpace(prev.PolicySnapshot)
					// Workflow approval gates (sys.approval.gate /
					// sys.workflow.approval.gate) carry no policy decision —
					// SafetyBasic returns an empty PolicySnapshot for them.
					// There is no TOCTOU concern to bind to, so skip the
					// snapshot match for gate topics. Regular jobs still
					// require the binding.
					isGate := isApprovalGateTopic(strings.TrimSpace(req.GetTopic()))
					if !isGate && (asserted == "" || stored == "" || snapshotBase(asserted) != snapshotBase(stored)) {
						e.emitApprovalRevisionMismatch(req, stored, asserted)
						slog.Warn("approval fast-path rejected: policy snapshot mismatch",
							"job_id", jobID,
							"stored_snapshot", stored,
							"asserted_snapshot", asserted,
						)
					} else {
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
							// Derive the store-op timeout from the caller's
							// lock-fenced ctx, NOT e.ctx. If the lock is lost
							// mid-evaluation the fenced ctx is cancelled and
							// this write must not complete after the takeover
							// handler has already mutated the job state.
							writeCtx, cancel := context.WithTimeout(ctx, storeOpTimeout)
							defer cancel()
							if err := e.jobStore.SetSafetyDecision(writeCtx, jobID, record); err != nil {
								return record, err
							}
							e.appendDecisionLog(req, record)
						}
						return record, nil
					}
				} else {
					slog.Warn("approval label ignored (hash mismatch)", "job_id", jobID)
				}
			} else {
				slog.Warn("approval label ignored (no approval record)", "job_id", jobID)
			}
		}
	}

	// Check for engine shutdown BEFORE starting the safety call. During
	// shutdown, we must not allow jobs through via fail-open. Return an
	// error so the job is not processed (it will be redelivered when the
	// engine restarts).
	if e.ctx.Err() != nil {
		return SafetyDecisionRecord{
			Decision:  SafetyDeny,
			Reason:    "engine shutting down",
			CheckedAt: time.Now().UTC().UnixNano() / int64(time.Microsecond),
		}, e.ctx.Err()
	}

	safetyCtx, safetyCancel := context.WithTimeout(ctx, safetyCheckTimeout)
	defer safetyCancel()

	record, err := e.safety.Check(safetyCtx, req)
	// If the engine context was cancelled during the safety call (shutdown
	// race), return immediately — do not proceed to fail-open logic.
	if e.ctx.Err() != nil {
		return record, e.ctx.Err()
	}
	if safetyCtx.Err() != nil {
		record.Decision = SafetyUnavailable
		err = fmt.Errorf("safety check timeout (defense-in-depth, %s)", safetyCheckTimeout)
		slog.Warn("safety check timed out", "job_id", jobID, "timeout", safetyCheckTimeout)
	}
	if record.CheckedAt == 0 {
		record.CheckedAt = time.Now().UTC().UnixNano() / int64(time.Microsecond)
	}
	if record.ApprovalRequired && (record.Decision == SafetyAllow || record.Decision == SafetyAllowWithConstraints) {
		record.Decision = SafetyRequireApproval
	}
	if record.Decision == SafetyRequireApproval || record.ApprovalRequired {
		var existingHash string
		if e.jobStore != nil {
			storeCtx, cancel := context.WithTimeout(ctx, storeOpTimeout)
			prev, prevErr := e.jobStore.GetSafetyDecision(storeCtx, jobID)
			cancel()
			if prevErr != nil {
				return record, fmt.Errorf("%w: load existing safety decision hash for %s: %w", errApprovalHashFenceRead, jobID, prevErr)
			}
			if prev.JobHash != "" {
				existingHash = prev.JobHash
			}
		}
		// Preserve the JobHash computed at gateway submit time — overwriting with
		// the post-mutation hash here would cause the reconciler to classify this
		// approval as invalidate_stale_request. See task-035cdc8e / commit 297937c7.
		if existingHash != "" {
			record.JobHash = existingHash
		} else if hash, err := HashJobRequest(req); err == nil {
			record.JobHash = hash
		} else {
			slog.Error("job hash failed", "job_id", jobID, "error", err)
		}
	}
	if e.jobStore != nil {
		storeCtx, cancel := context.WithTimeout(ctx, storeOpTimeout)
		defer cancel()
		if err := e.jobStore.SetSafetyDecision(storeCtx, jobID, record); err != nil {
			return record, err
		}
		e.appendDecisionLog(req, record)
	}
	return record, err
}

func (e *Engine) appendDecisionLog(req *pb.JobRequest, record SafetyDecisionRecord) {
	if e == nil || e.decisionLog == nil || req == nil {
		return
	}
	ctx, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
	defer cancel()
	if err := e.decisionLog.AppendDecision(ctx, buildDecisionLogRecord(req, record)); err != nil {
		slog.Warn("decision log append failed", "job_id", strings.TrimSpace(req.GetJobId()), "error", err)
	}
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
	return e.withJobLock(jobID, jobLockTTL, func(lockCtx context.Context) error {
		status := res.Status
		if status == pb.JobStatus_JOB_STATUS_COMPLETED {
			status = pb.JobStatus_JOB_STATUS_SUCCEEDED
		}
		var topic string
		if e.jobStore != nil {
			ctx, cancel := context.WithTimeout(lockCtx, storeOpTimeout)
			defer cancel()
			var topicErr error
			topic, topicErr = e.jobStore.GetTopic(ctx, jobID)
			if topicErr != nil {
				slog.Warn("topic lookup failed", "job_id", jobID, "error", topicErr)
			}
		}
		if topic == "" {
			topic = "unknown"
		}
		// Idempotency: if job is already terminal, ignore duplicate results.
		if e.jobStore != nil {
			ctx, cancel := context.WithTimeout(lockCtx, storeOpTimeout)
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
				ctx, cancel := context.WithTimeout(lockCtx, storeOpTimeout)
				defer cancel()
				var reqErr error
				jobReq, reqErr = store.GetJobRequest(ctx, jobID)
				if reqErr != nil {
					slog.Error("job request lookup failed, saga/output safety may be skipped",
						"job_id", jobID, "error", reqErr)
				}
			}
		}
		if status == pb.JobStatus_JOB_STATUS_SUCCEEDED && e.saga != nil && jobReq != nil {
			if err := e.saga.RecordCompensation(lockCtx, jobReq); err != nil {
				slog.Error("record compensation failed", "job_id", jobID, "error", err)
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
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					start := time.Now()
					if err := e.saga.Rollback(ctx, wfID); err != nil {
						slog.Error("saga rollback failed", "workflow_id", wfID, "duration", time.Since(start), "error", err)
					} else {
						slog.Info("saga rollback completed", "workflow_id", wfID, "duration", time.Since(start))
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
			// Map to non-terminal Retrying — the workflow engine (or higher-level
			// retry coordinator) will re-dispatch with a fresh jobID. Marking
			// terminal Failed here (combined with the DLQ-suppression at the
			// FAILED_RETRYABLE branch below) would silently drop retries because
			// any subsequent dispatch packet for the same jobID short-circuits at
			// the terminal-state guard in handleJobRequest.
			state = JobStateRetrying
		case pb.JobStatus_JOB_STATUS_FAILED_FATAL:
			state = JobStateFailed
		case pb.JobStatus_JOB_STATUS_TIMEOUT:
			state = JobStateTimeout
		case pb.JobStatus_JOB_STATUS_DENIED:
			state = JobStateDenied
		case pb.JobStatus_JOB_STATUS_CANCELLED:
			state = JobStateCancelled
		default:
			slog.Error("unknown job status",
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

		// Write result pointer BEFORE terminal state so that a retry after
		// partial failure can still persist the pointer.  If we set terminal
		// state first and then SetResultPtr fails, the idempotency guard
		// (terminal-state check at the top of handleJobResult) blocks
		// reprocessing, permanently orphaning the result data.
		if res.ResultPtr != "" {
			if err := e.setResultPtr(jobID, res.ResultPtr); err != nil {
				e.incResultPtrWriteFailure()
				slog.Error("result ptr write failed, keeping job in current state for retry",
					"job_id", jobID, "result_ptr", res.ResultPtr, "error", err)
				return RetryAfter(err, retryDelayStore)
			}
		}
		e.setWorkerID(jobID, strings.TrimSpace(res.GetWorkerId()))
		e.setAgentInfoFromWorker(jobID, strings.TrimSpace(res.GetWorkerId()))
		if err := e.setJobState(jobID, state); err != nil {
			return RetryAfter(err, retryDelayStore)
		}
		if succeeded && e.outputSafetyEnabled.Load() && e.outputSafety != nil && jobReq != nil {
			e.startAsyncOutputCheck(jobID, topic, res, jobReq)
		}
		completionStatus := status.String()
		if state == JobStateQuarantined {
			completionStatus = string(JobStateQuarantined)
		}
		e.incJobsCompleted(topic, completionStatus)
		if e.otelMetrics != nil {
			e.otelMetrics.RecordJobCompleted(context.Background(), topic, completionStatus)
		}
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
		slog.Error("output policy skipped: missing job request", "job_id", jobID)
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
		// DeadlineExceeded under burst load is tolerated: the record
		// stays at OutputAllow (fail-open on meta) and the async content
		// phase still runs. Log at WARN so monitoring doesn't page on
		// transient safety-kernel latency; downgrade from ERROR.
		if isDeadlineExceeded(err) {
			slog.Warn("output policy meta check timed out", "job_id", jobID, "topic", topic, "error", err)
		} else {
			slog.Error("output policy check failed", "job_id", jobID, "error", err)
		}
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
	reqCopy, err := protoutil.CloneJobRequest(req)
	if err != nil {
		// Best-effort: fall back to the original on any clone failure so
		// the output-safety goroutine still runs. Matches the pre-migration
		// behavior at this site; the protoutil helper just routes through
		// one canonical guard instead of an ad-hoc inline one.
		reqCopy = req
	}

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer func() {
			if p := recover(); p != nil {
				slog.Error("panic in async output safety check — failing closed: quarantining output",
					"job_id", jobID, "topic", topic, "panic", p,
					"stack", string(debug.Stack()))
				// Fail closed: persist a quarantine record so the output is
				// not silently allowed through unchecked.
				qRecord := OutputSafetyRecord{
					Decision:    OutputQuarantine,
					Reason:      fmt.Sprintf("async output check panic — fail-closed: %v", p),
					Phase:       "async",
					CheckedAt:   time.Now().UTC().UnixNano() / int64(time.Microsecond),
					OriginalPtr: strings.TrimSpace(resCopy.GetResultPtr()),
				}
				e.persistOutputSafety(jobID, qRecord)
				e.incOutputPolicyQuarantined(topic)
				e.incOutputDenials(topic)
				// Best-effort quarantine state transition.
				if qErr := e.withJobLock(jobID, jobLockTTL, func(lockCtx context.Context) error {
					if e.jobStore != nil {
						return e.jobStore.SetState(lockCtx, jobID, JobStateQuarantined)
					}
					return nil
				}); qErr != nil {
					slog.Error("panic recovery quarantine state transition failed",
						"job_id", jobID, "error", qErr)
					if dlqErr := e.emitDLQWithRetry(jobID, topic, pb.JobStatus_JOB_STATUS_FAILED,
						fmt.Sprintf("quarantine_panic_failed: %v", p), "quarantine_panic_failed"); dlqErr != nil {
						slog.Error("panic recovery DLQ emit also failed", "job_id", jobID, "error", dlqErr)
					}
				}
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		start := time.Now()
		record, err := e.outputSafety.CheckOutputContent(ctx, resCopy, reqCopy)
		elapsed := time.Since(start)
		e.observeOutputCheckLatency(topic, "async", elapsed.Seconds())
		e.observeOutputEvalDuration(topic, elapsed.Seconds())
		if err != nil {
			e.incAsyncOutputTimeout(topic)
			if e.isAsyncFailOpenForTenant(reqCopy.GetTenantId()) {
				slog.Warn("async output check failed, fail-open: allowing", "job_id", jobID, "error", err)
				e.incOutputPolicySkipped(topic)
				return
			}
			slog.Error("async output check failed, fail-closed: quarantining", "job_id", jobID, "error", err)
			record = OutputSafetyRecord{
				Decision:        OutputQuarantine,
				Reason:          "async output check error — fail-closed: " + err.Error(),
				Phase:           "async",
				CheckedAt:       time.Now().UTC().UnixNano() / int64(time.Microsecond),
				CheckDurationMs: elapsed.Milliseconds(),
				OriginalPtr:     strings.TrimSpace(resCopy.GetResultPtr()),
			}
			// Fall through to process the quarantine decision below.
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
		reason := strings.TrimSpace(record.Reason)
		if reason == "" {
			reason = "output quarantined by async policy"
		}

		// Retry the quarantine state transition up to 3 times with backoff.
		// If all retries fail, emit a DLQ entry so the failure is tracked.
		const maxQuarantineRetries = 3
		var lastErr error
		for attempt := 1; attempt <= maxQuarantineRetries; attempt++ {
			lastErr = e.withJobLock(jobID, jobLockTTL, func(lockCtx context.Context) error {
				if e.jobStore != nil {
					stateCtx, stateCancel := context.WithTimeout(lockCtx, storeOpTimeout)
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
				e.emitOutputAuditEvent(jobID, topic, outputPolicyAsync, reason, record.Decision)
				return e.emitDLQWithRetry(jobID, topic, pb.JobStatus_JOB_STATUS_DENIED, reason, outputPolicyAsync)
			})
			if lastErr == nil {
				break
			}
			slog.Error("async quarantine transition failed",
				"job_id", jobID, "topic", topic, "attempt", attempt,
				"max_attempts", maxQuarantineRetries, "error", lastErr)
			if attempt < maxQuarantineRetries {
				time.Sleep(time.Duration(attempt) * time.Second)
			}
		}

		// Safety net: if quarantine state transition failed after all retries,
		// emit a DLQ entry so the failure is visible to operators. The job
		// remains in SUCCEEDED state, which is wrong, but at least the DLQ
		// entry ensures the issue is discoverable.
		if lastErr != nil {
			slog.Error("CRITICAL: quarantine transition exhausted all retries — job remains in succeeded state",
				"job_id", jobID, "topic", topic, "error", lastErr,
				"output_decision", string(record.Decision), "reason", reason)
			if dlqErr := e.emitDLQWithRetry(jobID, topic, pb.JobStatus_JOB_STATUS_FAILED,
				fmt.Sprintf("quarantine_state_failed: %v", lastErr), "quarantine_failed"); dlqErr != nil {
				slog.Error("dlq emit failed during quarantine recovery", "job_id", jobID, "topic", topic, "error", dlqErr)
			}
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
		slog.Error("redaction materialization failed", "job_id", jobID, "error", err)
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
		slog.Error("persist output safety failed", "job_id", jobID, "error", err)
	}
}

func (e *Engine) setJobState(jobID string, state JobState) error {
	if e.jobStore == nil || jobID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
	defer cancel()
	if err := e.jobStore.SetState(ctx, jobID, state); err != nil {
		slog.Error("failed to set job state", "job_id", jobID, "state", state, "error", err)
		return fmt.Errorf("set job state: %w", err)
	}
	slog.Info("job state changed", "job_id", jobID, "new_state", string(state))
	return nil
}

func (e *Engine) setResultPtr(jobID, ptr string) error {
	if e.jobStore == nil || jobID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
	defer cancel()
	if err := e.jobStore.SetResultPtr(ctx, jobID, ptr); err != nil {
		slog.Error("failed to persist result ptr", "job_id", jobID, "error", err)
		return fmt.Errorf("set result ptr: %w", err)
	}
	return nil
}

func (e *Engine) setWorkerID(jobID, workerID string) {
	if e.jobStore == nil || jobID == "" || workerID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
	defer cancel()
	if err := e.jobStore.SetWorkerID(ctx, jobID, workerID); err != nil {
		slog.Warn("worker_id write failed", "job_id", jobID, "worker_id", workerID, "error", err)
	}
}

// setAgentInfoFromWorker resolves the worker's agent identity and persists it
// in job metadata for audit events. Uses the cached AgentResolver to avoid
// per-job Redis lookups.
func (e *Engine) setAgentInfoFromWorker(jobID, workerID string) {
	if e.agentResolver == nil || e.jobStore == nil || jobID == "" || workerID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
	defer cancel()
	info := e.agentResolver.Resolve(ctx, workerID)
	if store, ok := e.jobStore.(interface {
		SetAgentInfo(context.Context, string, string, string, string) error
	}); ok {
		if err := store.SetAgentInfo(ctx, jobID, info.AgentID, info.Name, info.RiskTier); err != nil {
			slog.Warn("agent_info write failed", "job_id", jobID, "agent_id", info.AgentID, "error", err)
		}
	}
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
	} else {
		policyConstraintsSerialiseFailedTotal.Inc()
		slog.Error("scheduler: serialise policy constraints failed",
			"audit_event", "policy_constraints_serialise_failed",
			"job_id", req.GetJobId(),
			"err", err)
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

func safeTraceID(packet *pb.BusPacket) string {
	if packet == nil {
		return ""
	}
	return strings.TrimSpace(packet.GetTraceId())
}

func safeSenderID(packet *pb.BusPacket) string {
	if packet == nil {
		return ""
	}
	return strings.TrimSpace(packet.GetSenderId())
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

// publishWithTrace publishes a BusPacket, propagating trace context through
// NATS headers when the bus supports it (ContextPublisher interface).
func (e *Engine) publishWithTrace(ctx context.Context, subject string, packet *pb.BusPacket) error {
	if cp, ok := e.bus.(model.ContextPublisher); ok {
		return cp.PublishWithContext(ctx, subject, packet)
	}
	return e.bus.Publish(subject, packet)
}

func (e *Engine) incJobsDispatched(topic string) {
	if e.metrics != nil {
		e.metrics.IncJobsDispatched(topic)
	}
}

func (e *Engine) incResultPtrWriteFailure() {
	if e.metrics != nil {
		e.metrics.IncResultPtrWriteFailure()
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
	if e.otelMetrics != nil {
		e.otelMetrics.RecordSafetyDenied(context.Background(), topic)
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
	if err := e.bus.Publish(capsdk.SubjectCancel, packet); err != nil {
		slog.Error("job cancel publish failed", "job_id", jobID, "error", err)
	}
}

func (e *Engine) replayApprovalPublish(traceID string, req *pb.JobRequest, approval ApprovalRecord) error {
	if req == nil {
		return fmt.Errorf("approval replay requires job request")
	}
	switch approval.PublishTarget {
	case ApprovalPublishTargetSubmit:
		return e.handleJobRequest(req, traceID)
	case ApprovalPublishTargetDLQ, ApprovalPublishTargetDLQAndResult:
		if e.bus == nil {
			return nil
		}
		jobID := strings.TrimSpace(req.GetJobId())
		if jobID == "" {
			return fmt.Errorf("approval replay requires job id")
		}
		errorMessage := strings.TrimSpace(approval.Reason)
		if errorMessage == "" {
			errorMessage = "approval rejected"
		}
		packet := &pb.BusPacket{
			TraceId:         traceID,
			SenderId:        defaultSenderID,
			CreatedAt:       timestamppb.Now(),
			ProtocolVersion: protocolVersionV1,
			Payload: &pb.BusPacket_JobResult{
				JobResult: &pb.JobResult{
					JobId:         jobID,
					Status:        pb.JobStatus_JOB_STATUS_DENIED,
					ErrorCode:     "approval_rejected",
					ErrorCodeEnum: pb.ErrorCode_ERROR_CODE_SAFETY_DENIED,
					ErrorMessage:  errorMessage,
				},
			},
		}
		if err := e.bus.Publish(capsdk.SubjectDLQ, packet); err != nil {
			return fmt.Errorf("replay approval dlq publish: %w", err)
		}
		if approval.PublishTarget == ApprovalPublishTargetDLQAndResult {
			if err := e.bus.Publish(capsdk.SubjectResult, packet); err != nil {
				return fmt.Errorf("replay approval result publish: %w", err)
			}
		}
		return nil
	default:
		return nil
	}
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
	slog.Warn("dlq emit failed, retrying", "job_id", jobID, "error", err)
	retryTimer := time.NewTimer(500 * time.Millisecond)
	select {
	case <-e.ctx.Done():
		retryTimer.Stop()
		return e.ctx.Err()
	case <-retryTimer.C:
	}
	err = e.emitDLQ(jobID, topic, status, reason, reasonCode)
	if err != nil {
		slog.Error("dlq emit failed after retry", "job_id", jobID, "reason_code", reasonCode, "error", err)
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

// emitSafetyBypassAuditEvent records a SIEM audit event for the fail-open
// admit path so reviewers can correlate bypassed jobs without inferring from
// WARN logs. Carries fail_mode_origin to distinguish global-default fail-open
// from a per-tenant override. Audit-fix task-2a52e7da finding #11.
func (e *Engine) emitSafetyBypassAuditEvent(ctx context.Context, req *pb.JobRequest, topic, reason string) {
	if e == nil || e.dispatchAuditSink == nil || req == nil {
		return
	}
	origin := "global_default"
	if e.failModeResolver != nil {
		origin = "tenant_override"
	}
	extra := map[string]string{
		"fail_mode_origin": origin,
	}
	if topic != "" {
		extra["topic"] = topic
	}
	if r := strings.TrimSpace(reason); r != "" {
		extra["reason"] = r
	}
	e.dispatchAuditSink.Emit(ctx, audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: audit.EventSafetyBypassAdmit,
		Severity:  audit.SeverityHigh,
		TenantID:  strings.TrimSpace(req.GetTenantId()),
		JobID:     strings.TrimSpace(req.GetJobId()),
		Action:    "admit",
		Reason:    strings.TrimSpace(reason),
		Extra:     extra,
	})
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
		slog.Error("output audit event publish failed", "job_id", jobID, "code", code, "error", err)
	}
}

// extractWorkerFromSubject parses a NATS direct subject "worker.{id}.jobs"
// and returns the worker ID. Returns empty string for non-direct subjects.
func extractWorkerFromSubject(subject string) string {
	if !strings.HasPrefix(subject, "worker.") || !strings.HasSuffix(subject, ".jobs") {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(subject, "worker."), ".jobs")
}

// scheduleFlushOnWorkerOnline kicks off a non-blocking flush of any
// pending-dispatch jobs for the worker's pool after the scheduler
// detects an offline→online transition (task-7a2514ae). The flush
// runs on a tracked goroutine (e.wg) so a subsequent Stop() drains
// cleanly. A per-pool latch collapses concurrent heartbeats from a
// freshly-scaled fleet into a single flush per pool.
//
// The metric is intentionally only incremented when real dispatch
// work occurred — observability wants "how often does scale-from-zero
// actually unlock queued jobs", not "how often did we attempt to
// look". The INFO log always fires so operators can count attempts
// too.
func (e *Engine) scheduleFlushOnWorkerOnline(pool, workerID, traceID string) {
	if e == nil || pool == "" {
		return
	}
	if e.stopped.Load() {
		return
	}
	if !e.flushLatch.acquire(pool) {
		return
	}
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer e.flushLatch.release(pool)

		ctx := context.Background()

		flushFn := e.flushDispatchFn
		if flushFn == nil {
			flushFn = e.defaultFlushDispatchForPool
		}
		dispatched := flushFn(ctx, pool, traceID)

		if dispatched > 0 && e.metrics != nil {
			e.metrics.IncDispatchFlushOnWorkerOnline(pool)
		}
		slog.Info("flush on worker online",
			"pool", pool,
			"worker_id", workerID,
			"dispatched", dispatched,
			"trace_id", traceID,
		)
	}()
}

// defaultFlushDispatchForPool is the production implementation of the
// flush-on-worker-online dispatch step. Lists pending and scheduled
// jobs from the job store, filters to the requested pool by topic
// match (strategies route by topic and a worker's pool drives the
// eligible-topic set), and replays each through e.handleJobRequest —
// the same entry point PendingReplayer uses for its poll-tick scans.
// Returns the number of jobs successfully re-dispatched.
func (e *Engine) defaultFlushDispatchForPool(ctx context.Context, pool, traceID string) int {
	if e == nil || e.jobStore == nil || pool == "" {
		return 0
	}

	cutoffMicros := time.Now().Add(-defaultFlushLookback).UnixNano() / int64(time.Microsecond)
	states := []JobState{JobStatePending, JobStateScheduled}
	dispatched := 0
	for _, state := range states {
		if ctx.Err() != nil {
			return dispatched
		}
		records, err := e.jobStore.ListJobsByState(ctx, state, cutoffMicros, defaultFlushBatchSize)
		if err != nil {
			slog.Warn("flush on worker online: list jobs failed",
				"pool", pool,
				"state", state,
				"trace_id", traceID,
				"error", err,
			)
			continue
		}
		for _, rec := range records {
			if ctx.Err() != nil {
				return dispatched
			}
			getter, ok := e.jobStore.(interface {
				GetJobRequest(context.Context, string) (*pb.JobRequest, error)
			})
			if !ok {
				return dispatched
			}
			req, err := getter.GetJobRequest(ctx, rec.ID)
			if err != nil || req == nil {
				continue
			}
			if !reqBelongsToPool(req, pool) {
				continue
			}
			if err := e.handleJobRequest(req, rec.TraceID); err != nil {
				slog.Warn("flush on worker online: replay failed",
					"pool", pool,
					"job_id", rec.ID,
					"trace_id", traceID,
					"error", err,
				)
				continue
			}
			dispatched++
		}
	}
	return dispatched
}

// reqBelongsToPool reports whether a job request is addressed to the
// given pool. Callers can tag a request with a "pool" label (simple,
// explicit) OR a "target_pool" label. Requests without a pool label
// fall back to the topic prefix check: workers by convention
// advertise pool="X" and subscribe to "job.X.*", so any req whose
// topic starts with "job.<pool>." is considered pool-addressed.
// Requests with a non-matching explicit pool label are rejected even
// if the topic prefix would match, so operators can disambiguate
// cross-pool topic reuse.
func reqBelongsToPool(req *pb.JobRequest, pool string) bool {
	if req == nil || pool == "" {
		return false
	}
	if labels := req.GetLabels(); labels != nil {
		if explicit := strings.TrimSpace(labels["pool"]); explicit != "" {
			return explicit == pool
		}
		if explicit := strings.TrimSpace(labels["target_pool"]); explicit != "" {
			return explicit == pool
		}
	}
	topic := strings.TrimSpace(req.GetTopic())
	if topic == "" {
		return false
	}
	return strings.HasPrefix(topic, "job."+pool+".") || topic == "job."+pool
}

const (
	// defaultFlushLookback bounds how far back ListJobsByState looks
	// when flushing pending-dispatch after a worker-online transition.
	// Five minutes matches the original incident window (the 5-min
	// dispatch lag observed on 2026-04-22) — any job older than that
	// is already in the PendingReplayer's steady-state range.
	defaultFlushLookback = 5 * time.Minute
	// defaultFlushBatchSize caps each ListJobsByState call. Matches
	// the PendingReplayer batch size so flush and replay pressure on
	// the store are comparable.
	defaultFlushBatchSize = 200
)
