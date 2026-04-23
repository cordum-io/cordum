package safetykernel

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/policyshadow"
)

// Shadow metric callbacks. Assigned by metrics.go init(); kept as
// package-level vars (nil-safe) so the bootstrap can reference them
// without an ordering dependency between this file and metrics.go.
var (
	shadowDroppedCallback    func(reason ShadowDropReason)
	shadowEmittedCallback    func(decision string, diff ShadowDiff, latency time.Duration)
	shadowQueueDepthCallback func(delta int64)
)

// envShadowEvalDisabled is the emergency-rollback env var. When set to
// a truthy value the kernel skips every shadow-evaluation wiring step
// and the evaluator field stays nil — evaluate()'s nil-check in
// kernel.go makes the dual-eval a no-op. Intended for ops to turn off
// the feature without a redeploy if a shadow policy is causing issues.
const envShadowEvalDisabled = "CORDUM_SHADOW_EVAL_DISABLED"

// shadowEvalDefaultRefresh is the loader poll cadence. Chosen so a
// newly-activated shadow is visible within a ~15s window while costing
// one configsvc scan per tenant per interval.
const shadowEvalDefaultRefresh = 15 * time.Second

// setupShadowEvaluation constructs the loader + evaluator, attaches
// them to srv via SetShadowEvaluator, and returns both so the caller
// can defer their Close(). Any of {configsvc nil, env disabled, nats
// absent} falls back to a no-op evaluator — the kernel still boots
// and serves the active policy. Logs emitted at INFO describe the
// final state so operators can grep for "safety-kernel: shadow
// evaluation" and see whether it's live.
func setupShadowEvaluation(srv *server, loader *policyLoader, natsBus *bus.NatsBus) (*ShadowLoader, *ShadowEvaluator) {
	if srv == nil {
		return nil, nil
	}
	if envTruthy(os.Getenv(envShadowEvalDisabled)) {
		slog.Info("safety-kernel: shadow evaluation disabled by env", "var", envShadowEvalDisabled)
		return nil, nil
	}
	if loader == nil || loader.configSvc == nil {
		slog.Info("safety-kernel: shadow evaluation disabled — configsvc unavailable")
		return nil, nil
	}
	store := policyshadow.NewStore(loader.configSvc)
	auditSender := chooseShadowAuditSender(natsBus)
	tenantsFn := func() []string {
		srv.mu.RLock()
		defer srv.mu.RUnlock()
		if srv.policy == nil || len(srv.policy.Tenants) == 0 {
			return nil
		}
		out := make([]string, 0, len(srv.policy.Tenants))
		for t := range srv.policy.Tenants {
			out = append(out, t)
		}
		return out
	}
	shadowLoader := NewShadowLoader(store, shadowEvalDefaultRefresh, tenantsFn)
	// Metric callbacks are wired in metrics.go (step-6). If a future
	// refactor moves them, this is the single seam to update.
	shadowEvaluator := NewShadowEvaluator(shadowLoader, auditSender, ShadowEvaluatorOptions{
		Workers:            shadowEvalDefaultWorkers,
		QueueSize:          shadowEvalDefaultQueueSize,
		ShadowTimeout:      30 * time.Second,
		DropCallback:       shadowDroppedCallback,
		EmitCallback:       shadowEmittedCallback,
		QueueDepthCallback: shadowQueueDepthCallback,
	})
	srv.SetShadowEvaluator(shadowEvaluator)
	slog.Info("safety-kernel: shadow evaluation enabled",
		"refresh", shadowEvalDefaultRefresh,
		"workers", shadowEvalDefaultWorkers,
		"queue_size", shadowEvalDefaultQueueSize,
		"audit_transport", auditSenderName(auditSender),
	)
	return shadowLoader, shadowEvaluator
}

// shadowEvalDefault* are the production pool sizes. 64 workers × 1000
// queue matches the plan. If a future tuning reveals backpressure
// issues these become env-driven; for now they stay constants.
const (
	shadowEvalDefaultWorkers   = 64
	shadowEvalDefaultQueueSize = 1000
)

// envTruthy accepts the usual truthy encodings. Mirrors what
// input_policy.go and config.Load() do elsewhere — keeping the
// behaviour uniform across env vars.
func envTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// chooseShadowAuditSender picks the audit sink for shadow_eval events.
// Prefer the NATS publisher so shadow events ride the same chain as
// safety.decision events — downstream consumers (gateway's
// NATSAuditConsumer + Chainer) treat them uniformly. When NATS is
// unavailable the sender is nil and the evaluator emits nothing; drop
// callback still fires so the dropped_total counter reflects reality.
func chooseShadowAuditSender(natsBus *bus.NatsBus) audit.AuditSender {
	if natsBus == nil {
		return nil
	}
	// A nil fallback BufferedExporter is fine — the NATSAuditPublisher
	// only falls back when the NATS publish itself errors; at that
	// point losing a shadow_eval event is acceptable (observability
	// only). Passing a non-nil local exporter would double-count in
	// the gateway consumer which also reads from the bus.
	return audit.NewNATSAuditPublisher(natsBus, nil)
}

// auditSenderName is used only for the boot log; tells operators
// which path shadow events are taking.
func auditSenderName(sender audit.AuditSender) string {
	switch sender.(type) {
	case nil:
		return "disabled"
	case *audit.NATSAuditPublisher:
		return "nats"
	default:
		return "custom"
	}
}

// ShadowEvaluator runs the Phase-2 dual evaluation: for every active
// PolicyCheckRequest, evaluate the tenant's shadow bundles against the
// same input and emit a shadow_eval SIEMEvent describing the diff.
//
// Epic rails this implementation is built around:
//   - Shadow evaluation must NEVER affect the actual decision. The
//     Submit call is non-blocking — the worker pool executes the
//     shadow bundle asynchronously AFTER the active decision has been
//     returned. Submit copies the PolicyInput by value so the caller
//     can reuse or mutate the original freely.
//   - Shadow results are emitted as a dedicated audit event type
//     (EventShadowEval), never mixed with safety.decision.
//   - The active path has a strict latency budget, so the shadow
//     evaluator bounds its resource usage: 64 workers by default, a
//     1000-slot queue, drop-on-overflow with a counter. Dropped shadow
//     events are surfaced via the metric + a rate-limited log; they
//     are NEVER retried in-band because a retry loop could starve the
//     active path.
//
// Close() stops the workers and waits for in-flight jobs to complete
// within a bounded drain period.

// shadowJob carries everything a worker needs to run a single shadow
// evaluation. The active decision fields are captured at Submit time
// so the worker doesn't race with a caller reusing the PolicyInput.
type shadowJob struct {
	tenantID       string
	jobID          string
	agentID        string
	activeDecision config.PolicyDecision
	// input is a by-value copy so the caller can safely mutate the
	// original after Submit returns. PolicyInput contains maps (Labels)
	// and slices (Meta.RiskTags) — the copy here is shallow; callers
	// must not mutate the contents concurrently with Submit.
	input config.PolicyInput
}

// ShadowDropReason categorises why a shadow evaluation was not emitted
// — used by the metrics layer to break down dropped_total. Exported
// for step-6 metric registration.
type ShadowDropReason string

const (
	ShadowDropQueueFull ShadowDropReason = "queue_full"
	ShadowDropClosed    ShadowDropReason = "closed"
)

// ShadowDiff is the classification the audit event's `diff` field
// carries. Results-API queries (task-0f2ba204) filter on these values.
type ShadowDiff string

const (
	ShadowDiffEscalated      ShadowDiff = "escalated"
	ShadowDiffRelaxed        ShadowDiff = "relaxed"
	ShadowDiffApprovalDiffer ShadowDiff = "approval_differ"
	ShadowDiffUnchanged      ShadowDiff = "unchanged"
)

// ShadowEvaluatorOptions configures a ShadowEvaluator. Zero-value
// defaults keep the kernel wiring minimal: workers=64, queue=1000,
// shadowTimeout=30s.
type ShadowEvaluatorOptions struct {
	Workers       int
	QueueSize     int
	ShadowTimeout time.Duration
	// DropCallback is invoked on every drop with the reason. Optional —
	// step-6 wires it to the dropped_total Prometheus counter. Tests
	// inject a capturing stub.
	DropCallback func(reason ShadowDropReason)
	// EmitCallback is invoked on every emitted shadow_eval event with
	// the shadow verdict, diff label, and evaluation latency. Optional
	// — step-6 wires it to the total counter + duration histogram. The
	// decision string is one of {allow, deny, require_approval,
	// throttle, allow_with_constraints} — same domain as the active
	// policy's PolicyDecision.Decision.
	EmitCallback func(decision string, diff ShadowDiff, latency time.Duration)
	// QueueDepthCallback is invoked when the queue depth changes
	// (delta = +1 on enqueue, -1 on dequeue). Step-6 wires it to the
	// queue_depth gauge.
	QueueDepthCallback func(delta int64)
}

// ShadowEvaluator is safe for concurrent Submit and Close calls.
type ShadowEvaluator struct {
	loader      *ShadowLoader
	auditSender audit.AuditSender

	queue         chan shadowJob
	shadowTimeout time.Duration

	dropCB  func(ShadowDropReason)
	emitCB  func(decision string, diff ShadowDiff, latency time.Duration)
	depthCB func(int64)

	closed   atomic.Bool
	wg       sync.WaitGroup
	stopOnce sync.Once
}

// NewShadowEvaluator constructs the evaluator and spins up the worker
// pool. The evaluator begins accepting Submit calls immediately; Close
// drains in-flight jobs and stops the workers.
//
// auditSender may be nil in dev/degraded modes — the evaluator still
// runs so the dropped counter and diff classification exercise their
// code paths, but the final Send is skipped. A nil loader is also
// tolerated (the worker sees an empty snapshot and exits cleanly).
func NewShadowEvaluator(loader *ShadowLoader, auditSender audit.AuditSender, opts ShadowEvaluatorOptions) *ShadowEvaluator {
	workers := opts.Workers
	if workers <= 0 {
		workers = 64
	}
	queueSize := opts.QueueSize
	if queueSize <= 0 {
		queueSize = 1000
	}
	timeout := opts.ShadowTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	se := &ShadowEvaluator{
		loader:        loader,
		auditSender:   auditSender,
		queue:         make(chan shadowJob, queueSize),
		shadowTimeout: timeout,
		dropCB:        opts.DropCallback,
		emitCB:        opts.EmitCallback,
		depthCB:       opts.QueueDepthCallback,
	}
	se.wg.Add(workers)
	for range workers {
		go se.runWorker()
	}
	return se
}

// Submit enqueues a shadow evaluation for the given active decision +
// input. Non-blocking: if the queue is full (burst overload) the event
// is dropped and DropCallback is invoked with queue_full. On a closed
// evaluator the drop reason is "closed".
//
// The input is copied by value so the caller may reuse or mutate the
// original PolicyInput immediately after return. Shallow-copying is
// sufficient here because the worker never mutates Meta.RiskTags /
// Labels — it only reads, and shadow.Evaluate does the same.
func (e *ShadowEvaluator) Submit(activeDecision config.PolicyDecision, input config.PolicyInput, tenantID, jobID string) {
	if e == nil {
		return
	}
	if e.closed.Load() {
		if e.dropCB != nil {
			e.dropCB(ShadowDropClosed)
		}
		return
	}
	job := shadowJob{
		tenantID:       tenantID,
		jobID:          jobID,
		agentID:        input.Meta.AgentID,
		activeDecision: activeDecision,
		input:          input,
	}
	select {
	case e.queue <- job:
		if e.depthCB != nil {
			e.depthCB(+1)
		}
	default:
		if e.dropCB != nil {
			e.dropCB(ShadowDropQueueFull)
		}
	}
}

// Close stops the workers and waits for in-flight jobs to drain.
// Idempotent and safe to call from multiple goroutines.
func (e *ShadowEvaluator) Close() {
	if e == nil {
		return
	}
	e.stopOnce.Do(func() {
		e.closed.Store(true)
		close(e.queue)
	})
	e.wg.Wait()
}

func (e *ShadowEvaluator) runWorker() {
	defer e.wg.Done()
	for job := range e.queue {
		if e.depthCB != nil {
			e.depthCB(-1)
		}
		e.evaluate(job)
	}
}

// evaluate runs the dual-eval for one submission. It isolates each
// shadow bundle's Evaluate call behind a panic recover so one
// malformed rule can't poison the rest of the tenant's shadows or
// crash the worker.
func (e *ShadowEvaluator) evaluate(job shadowJob) {
	if e.loader == nil {
		return
	}
	compiled, meta := e.loader.Snapshot()
	tenantCompiled, ok := compiled[job.tenantID]
	if !ok || len(tenantCompiled) == 0 {
		return
	}
	tenantMeta := meta[job.tenantID]
	// Bounded background ctx so a cancelled caller-ctx doesn't snip
	// the shadow eval mid-flight. shadowTimeout is the absolute wall-
	// clock budget for processing every shadow for this submission;
	// the loop-top ctx.Err() check below enforces that budget at
	// per-bundle granularity (one slow bundle can still exceed the
	// timeout by its own eval time, but subsequent bundles are
	// skipped). Partial shadow-event counts are expected behavior on
	// timeout.
	ctx, cancel := context.WithTimeout(context.Background(), e.shadowTimeout)
	defer cancel()
	for bundleID, policy := range tenantCompiled {
		if err := ctx.Err(); err != nil {
			return
		}
		start := time.Now()
		shadowDecision, evalErr := evalShadowSafely(ctx, policy, job.input)
		latency := time.Since(start)
		if evalErr != nil {
			// Panic-recovered evaluations emit a warn + keep going.
			// Returning here would silently drop the whole tenant's
			// remaining shadows on one bad policy — worse than one
			// missing event.
			slog.Warn("shadow-eval: policy evaluation panicked",
				"tenant", job.tenantID,
				"bundle_id", bundleID,
				"error", evalErr,
			)
			continue
		}
		diff := classifyShadowDiff(job.activeDecision, shadowDecision)
		if e.emitCB != nil {
			e.emitCB(shadowDecision.Decision, diff, latency)
		}
		if e.auditSender == nil {
			continue
		}
		shadowBundleID := ""
		if sp, ok := tenantMeta[bundleID]; ok {
			shadowBundleID = sp.ShadowBundleID
		}
		event := audit.SIEMEvent{
			Timestamp: time.Now().UTC(),
			EventType: audit.EventShadowEval,
			Severity:  severityForDiff(diff),
			TenantID:  job.tenantID,
			AgentID:   job.agentID,
			JobID:     job.jobID,
			Action:    string(diff),
			Decision:  shadowDecision.Decision,
			Reason:    shadowDecision.Reason,
			Extra: map[string]string{
				"shadow_bundle_id": shadowBundleID,
				"bundle_id":        bundleID,
				"active_verdict":   job.activeDecision.Decision,
				"shadow_verdict":   shadowDecision.Decision,
				"diff":             string(diff),
				"active_rule_id":   job.activeDecision.RuleID,
				"shadow_rule_id":   shadowDecision.RuleID,
				"latency_ms":       fmt.Sprintf("%d", latency.Milliseconds()),
			},
		}
		e.auditSender.Send(event)
	}
}

// evalShadowSafely wraps shadow.Evaluate in a panic recover so one
// broken rule can't bring down a kernel worker. Returns the decision
// on success, or (zero-decision, error) if Evaluate panicked.
//
// ctx is plumbed for future ctx-aware policy engines; today's
// config.SafetyPolicy.Evaluate takes no ctx, so this function does
// not actually cancel the evaluate call itself. The caller in
// evaluate() uses ctx to bound the loop between iterations, which is
// the correct granularity while policy.Evaluate remains synchronous.
func evalShadowSafely(ctx context.Context, policy *config.SafetyPolicy, input config.PolicyInput) (decision config.PolicyDecision, err error) {
	_ = ctx
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("shadow evaluate panic: %v: %s", r, string(debug.Stack()))
		}
	}()
	decision = policy.Evaluate(input)
	return decision, nil
}

// classifyShadowDiff collapses two PolicyDecisions into one of the four
// diff labels. The comparison keys on normalised decision strings plus
// the ApprovalRequired flag — matching the active kernel's own
// decision space (allow | deny | require_approval | throttle).
func classifyShadowDiff(active, shadow config.PolicyDecision) ShadowDiff {
	aDec := strings.ToLower(strings.TrimSpace(active.Decision))
	sDec := strings.ToLower(strings.TrimSpace(shadow.Decision))

	aRA := active.ApprovalRequired || aDec == "require_approval"
	sRA := shadow.ApprovalRequired || sDec == "require_approval"

	if aDec == sDec && aRA == sRA {
		return ShadowDiffUnchanged
	}
	// Approval differ takes precedence over raw allow/deny diffs only
	// when one side is require_approval and the other is a terminal
	// allow/deny; that's the case where a shadow would block with a
	// human review step that the active path skips (or vice versa).
	if aRA != sRA {
		return ShadowDiffApprovalDiffer
	}
	// Escalated: shadow is stricter than active (active allows /
	// throttles, shadow denies).
	if aDec == "allow" && sDec == "deny" {
		return ShadowDiffEscalated
	}
	if aDec == "throttle" && sDec == "deny" {
		return ShadowDiffEscalated
	}
	// Relaxed: shadow is more permissive (active denies, shadow allows).
	if aDec == "deny" && sDec == "allow" {
		return ShadowDiffRelaxed
	}
	if aDec == "deny" && sDec == "throttle" {
		return ShadowDiffRelaxed
	}
	// Any other combination (e.g. throttle vs allow) — different but
	// not clearly escalated/relaxed. Classify as approval_differ so
	// operators see it on the dashboard and can inspect manually.
	return ShadowDiffApprovalDiffer
}

// severityForDiff sets the SIEMEvent Severity based on the diff class.
// Escalated is the risky case (shadow would have blocked a live job)
// — Medium; the rest are Info so dashboards can prioritise.
func severityForDiff(d ShadowDiff) string {
	switch d {
	case ShadowDiffEscalated:
		return audit.SeverityMedium
	case ShadowDiffRelaxed, ShadowDiffApprovalDiffer:
		return audit.SeverityLow
	default:
		return audit.SeverityInfo
	}
}
