package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"log/slog"

	schemas "github.com/cordum/cordum/core/infra/schema"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/licensing"
	"github.com/cordum/cordum/core/model"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Prometheus metrics for workflow lock observability.
var lockFallbackTotal = prometheus.NewCounter(prometheus.CounterOpts{
	Namespace: "cordum",
	Subsystem: "workflow",
	Name:      "lock_fallback_total",
	Help:      "Number of times distributed lock acquisition failed and fell back to local-only locking.",
})

func init() {
	prometheus.MustRegister(lockFallbackTotal)
}

// Engine coordinates workflow runs, dispatching steps as jobs and updating run state.
type Engine struct {
	store           *RedisStore
	bus             model.Bus
	mem             store.Store
	lockMgr         lockManager // per-run locks to avoid global serialization
	maxForEachItems int
	// optional callbacks for observability or hooks
	OnStepDispatched func(runID, stepID, jobID string)
	OnStepFinished   func(runID, stepID string, status StepStatus)
	config           ConfigProvider
	schemaRegistry   *schemas.Registry
	outputSafety     model.OutputSafetyChecker // optional output policy enforcement on step results
	entitlements     *licensing.EntitlementResolver

	// timerMu guards pendingTimers. pendingTimers tracks cancellable delay
	// timers so they can be stopped on engine shutdown without leaking goroutines.
	timerMu       sync.Mutex
	pendingTimers []*time.Timer
	stopped       chan struct{} // closed by Stop(); nil until first use
}

// RunLocker is an optional distributed lock provider for cross-replica
// mutual exclusion on workflow runs. When nil, only in-process locking is used.
type RunLocker interface {
	TryAcquireLock(ctx context.Context, key string, ttl time.Duration) (string, error)
	ReleaseLock(ctx context.Context, key string, token string) error
}

// RunLockRenewer is an optional extension of RunLocker that supports TTL renewal.
// If the RunLocker also implements this interface, locks are renewed periodically.
type RunLockRenewer interface {
	RenewLock(ctx context.Context, key, token string, ttl time.Duration) error
}

const runLockTTL = 30 * time.Second

// lockManager provides per-run mutual exclusion with safe cleanup.
// Two-layer locking: local mutex first (fast, prevents intra-process
// contention and Redis round-trips), then optional Redis lock (distributed).
type lockManager struct {
	mu     sync.Mutex
	locks  map[string]*runLock
	locker RunLocker // optional distributed lock; nil = local-only
	ctx    context.Context // engine lifecycle context; renewal goroutines derive from this
}

type runLock struct {
	mu       sync.Mutex
	refs     int32
	terminal bool
}

// acquire obtains the per-run lock for runID and returns a release function.
// The bool return indicates whether the lock was acquired (true) or another
// replica holds the distributed lock and the caller should skip (false).
// When ok is false, the release function is nil and no cleanup is needed.
// If a distributed locker is set, a Redis lock is acquired after the local mutex.
// On Redis error (unreachable), degrades to local-only lock.
// On lock contention (another replica holds it), returns (nil, false) to skip.
func (lm *lockManager) acquire(runID string) (func(), bool) {
	lm.mu.Lock()
	lock, ok := lm.locks[runID]
	if !ok {
		lock = &runLock{}
		lm.locks[runID] = lock
	}
	lock.refs++
	lm.mu.Unlock()

	// Local mutex first (per task rail: local before Redis).
	lock.mu.Lock()

	// Distributed lock — skip on contention, degrade to local-only on error.
	var redisToken string
	var renewCancel context.CancelFunc
	var renewDone chan struct{}
	if lm.locker != nil {
		key := runLockKey(runID)
		ctx, cancel := context.WithTimeout(lm.ctx, 2*time.Second)
		token, err := lm.locker.TryAcquireLock(ctx, key, runLockTTL)
		cancel()
		if err != nil {
			lockFallbackTotal.Inc()
			slog.Error("distributed lock failed — using local-only lock, cross-replica race possible",
				"component", "workflow", "run_id", runID, "error", err)
		} else if token == "" {
			// Another replica holds the lock — skip this run.
			lock.mu.Unlock()
			lm.mu.Lock()
			lock.refs--
			if lock.refs == 0 && lock.terminal {
				delete(lm.locks, runID)
			}
			lm.mu.Unlock()
			return nil, false
		} else {
			redisToken = token
			// Start renewal goroutine if the locker supports it.
			if renewer, ok := lm.locker.(RunLockRenewer); ok {
				var renewCtx context.Context
				renewCtx, renewCancel = context.WithCancel(lm.ctx) // derives from engine context; stops renewal on shutdown
				renewDone = make(chan struct{})
				go func() {
					defer close(renewDone)
					ticker := time.NewTicker(runLockTTL / 3)
					defer ticker.Stop()
					for {
						select {
						case <-renewCtx.Done():
							return
						case <-ticker.C:
							rCtx, rCancel := context.WithTimeout(renewCtx, 2*time.Second)
							if err := renewer.RenewLock(rCtx, key, token, runLockTTL); err != nil {
								slog.Warn("run lock renewal failed",
									"run_id", runID, "error", err)
							}
							rCancel()
						}
					}
				}()
			}
		}
	}

	return func() {
		// Stop renewal goroutine and wait for it to finish before releasing.
		if renewCancel != nil {
			renewCancel()
			<-renewDone
		}
		// Release Redis lock BEFORE local mutex (per task rail).
		if redisToken != "" && lm.locker != nil {
			rCtx, rCancel := context.WithTimeout(context.Background(), 2*time.Second)
			if err := lm.locker.ReleaseLock(rCtx, runLockKey(runID), redisToken); err != nil {
				slog.Warn("distributed run lock release failed",
					"run_id", runID, "error", err)
			}
			rCancel()
		}
		lock.mu.Unlock()
		lm.mu.Lock()
		lock.refs--
		if lock.refs == 0 && lock.terminal {
			delete(lm.locks, runID)
		}
		lm.mu.Unlock()
	}, true
}

// markTerminal flags a run as completed so its lock entry is cleaned up
// once all active holders release. The Redis lock key is released by the
// acquire() release function; any stale keys expire via TTL.
func (lm *lockManager) markTerminal(runID string) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	lock, ok := lm.locks[runID]
	if !ok {
		return
	}
	lock.terminal = true
	if lock.refs == 0 {
		delete(lm.locks, runID)
	}
}

const (
	maxInlineResultBytes       = 256 << 10
	defaultMaxForEachItems     = 1000
	defaultLoopMaxIter         = 100
	switchBranchNotTakenReason = "switch_branch_not_taken"
)

// ConfigProvider supplies effective config given identity context.
type ConfigProvider interface {
	Effective(ctx context.Context, orgID, teamID, workflowID, stepID string) (map[string]any, error)
}

// NewEngine creates a workflow engine bound to a Redis workflow store and bus.
func NewEngine(store *RedisStore, bus model.Bus) *Engine {
	return &Engine{
		store:           store,
		bus:             bus,
		maxForEachItems: defaultMaxForEachItems,
		lockMgr:         lockManager{locks: make(map[string]*runLock), ctx: context.Background()},
	}
}

// WithMemory sets an optional memory store used to persist per-step job context payloads.
func (e *Engine) WithMemory(s store.Store) *Engine {
	e.mem = s
	return e
}

// WithConfig sets an optional config provider.
func (e *Engine) WithConfig(cfg ConfigProvider) *Engine {
	e.config = cfg
	return e
}

// WithSchemaRegistry sets an optional schema registry for validating inputs/outputs.
func (e *Engine) WithSchemaRegistry(registry *schemas.Registry) *Engine {
	e.schemaRegistry = registry
	return e
}

// WithOutputSafety sets an optional output safety checker for inter-step policy enforcement.
func (e *Engine) WithOutputSafety(c model.OutputSafetyChecker) *Engine {
	e.outputSafety = c
	return e
}

// WithEntitlements wires the shared runtime entitlement resolver used for
// workflow tier enforcement. Nil falls back to community defaults.
func (e *Engine) WithEntitlements(resolver *licensing.EntitlementResolver) *Engine {
	e.entitlements = resolver
	return e
}

// WithContext sets the parent context for the lock manager. Cancelling this
// context stops all lock renewal goroutines, preventing Redis ops after shutdown.
// If not called, defaults to context.Background().
func (e *Engine) WithContext(ctx context.Context) *Engine {
	e.lockMgr.ctx = ctx
	return e
}

// WithRunLocker sets a distributed lock provider for cross-replica run locking.
// When set, the engine acquires a Redis-backed lock in addition to the local mutex.
func (e *Engine) WithRunLocker(locker RunLocker) *Engine {
	e.lockMgr.locker = locker
	return e
}

// WithMaxForEachItems sets the maximum number of items allowed in for_each fan-out.
// Values <= 0 are ignored and the default remains in effect.
func (e *Engine) WithMaxForEachItems(limit int) *Engine {
	if limit > 0 {
		e.maxForEachItems = limit
	}
	return e
}

// lockRun acquires a per-run mutex and returns an unlock function.
// The bool return indicates whether the lock was acquired. When false, the
// caller should skip processing — another replica owns this run.
func (e *Engine) lockRun(runID string) (func(), bool) {
	return e.lockMgr.acquire(runID)
}

func (e *Engine) currentEntitlements() licensing.Entitlements {
	if e != nil && e.entitlements != nil {
		return e.entitlements.Entitlements()
	}
	return licensing.DefaultEntitlements(licensing.PlanCommunity)
}

func (e *Engine) approvalModeLimit() string {
	mode := strings.TrimSpace(e.currentEntitlements().ApprovalMode)
	if mode == "" {
		return string(licensing.ApprovalModeSingle)
	}
	return mode
}

func requestedApprovalModeForWorkflow(steps map[string]*Step) string {
	approvalIDs := make(map[string]struct{})
	for stepID, step := range steps {
		if step != nil && step.Type == StepTypeApproval {
			approvalIDs[stepID] = struct{}{}
		}
	}
	switch len(approvalIDs) {
	case 0:
		return ""
	case 1:
		return string(licensing.ApprovalModeSingle)
	}
	for stepID := range approvalIDs {
		step := steps[stepID]
		if step == nil {
			continue
		}
		for _, dep := range step.DependsOn {
			if _, ok := approvalIDs[strings.TrimSpace(dep)]; ok {
				return string(licensing.ApprovalModeCustom)
			}
		}
		if target := strings.TrimSpace(step.OnError); target != "" {
			if _, ok := approvalIDs[target]; ok {
				return string(licensing.ApprovalModeCustom)
			}
		}
	}
	return string(licensing.ApprovalModeMulti)
}

func (e *Engine) validateWorkflowDefinition(wf *Workflow) error {
	if wf == nil {
		return nil
	}
	if limitErr := licensing.CheckWorkflowSteps(int64(len(wf.Steps)), e.currentEntitlements()); limitErr != nil {
		return limitErr
	}
	if requested := requestedApprovalModeForWorkflow(wf.Steps); requested != "" {
		if limitErr := licensing.CheckApprovalMode(requested, e.approvalModeLimit()); limitErr != nil {
			return limitErr
		}
	}
	return nil
}

// HandleJobResult updates step/run state and dispatches next steps if ready.
func (e *Engine) HandleJobResult(ctx context.Context, res *pb.JobResult) error {
	if res == nil || res.JobId == "" {
		return nil
	}
	runID, stepID := splitJobID(res.JobId)
	if runID == "" || stepID == "" {
		return nil
	}

	slog.Debug("step result received", "component", "workflow", "runId", runID, "traceId", runID, "stepId", stepID, "jobId", res.JobId)

	unlock, ok := e.lockRun(runID)
	if !ok {
		return nil // Another replica owns this run.
	}
	defer unlock()

	run, err := e.store.GetRun(ctx, runID)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			slog.Info("run not found (deleted or never existed)", "run_id", runID)
			return fmt.Errorf("%w: %s", ErrRunNotFound, runID)
		}
		slog.Error("get run failed (transient)", "run_id", runID, "error", err)
		return fmt.Errorf("get run %s: %w", runID, err)
	}
	switch run.Status {
	case RunStatusSucceeded, RunStatusFailed, RunStatusDenied, RunStatusCancelled, RunStatusTimedOut:
		e.markRunTerminal(run.ID)
		return nil
	}
	wfDef, err := e.store.GetWorkflow(ctx, run.WorkflowID)
	if err != nil {
		slog.Error("get workflow failed", "workflow_id", run.WorkflowID, "error", err)
		return fmt.Errorf("get workflow %s: %w", run.WorkflowID, err)
	}

	now := time.Now().UTC()
	if timedOut, err := e.enforceWorkflowTimeout(ctx, wfDef, run, now); err != nil {
		slog.Error("enforce workflow timeout", "run_id", run.ID, "error", err)
		return nil
	} else if timedOut {
		return nil
	}

	prevStatus := run.Status
	baseStepID, childKey := splitForEachStep(stepID)
	stepDef := wfDef.Steps[baseStepID]
	attempt := parseAttempt(res.JobId)

	if childKey != "" {
		parent := run.Steps[baseStepID]
		if parent == nil {
			parent = &StepRun{StepID: baseStepID}
		}
		if parent.Children == nil {
			parent.Children = make(map[string]*StepRun)
		}
		child := parent.Children[stepID]
		if child == nil {
			child = run.Steps[stepID]
			if child == nil {
				child = &StepRun{StepID: stepID}
			}
		}
		if child.JobID != "" && child.JobID != res.JobId {
			return nil
		}
		if child.JobID == "" {
			child.JobID = res.JobId
		}
		if attempt > child.Attempts {
			child.Attempts = attempt
		}
		if child.JobID == res.JobId && shouldIgnoreProcessedResult(child) {
			return nil
		}
		retry, delay := applyResult(child, res, stepDef)
		if !retry && child.Status == StepStatusSucceeded && res.ResultPtr != "" {
			if err := e.validateStepOutput(stepDef, res.ResultPtr); err != nil {
				child.Status = StepStatusFailed
				child.CompletedAt = &now
				child.Error = map[string]any{"message": err.Error()}
				e.appendTimeline(ctx, run, "step_output_invalid", stepID, res.JobId, string(child.Status), res.ResultPtr, err.Error(), nil)
			}
		}
		if !retry && (child.Status == StepStatusSucceeded || child.Status == StepStatusFailed || child.Status == StepStatusDenied || child.Status == StepStatusCancelled || child.Status == StepStatusTimedOut) {
			e.appendTimeline(ctx, run, "step_completed", stepID, res.JobId, string(child.Status), res.ResultPtr, res.ErrorMessage, nil)
		}
		if !retry && child.Status == StepStatusSucceeded && res.ResultPtr != "" {
			if !e.checkStepOutputPolicy(ctx, run, stepID, child, res) {
				recordStepOutput(ctx, e.mem, run, stepID, stepDef, res.ResultPtr, false)
			}
		}
		parent.Children[stepID] = child
		run.Steps[stepID] = child
		if stepDef != nil && stepDef.Type == StepTypeLoop {
			// Loop parent status is finalized by the loop scheduler logic, not raw child aggregation.
			parent.Status = StepStatusRunning
			parent.CompletedAt = nil
		} else {
			parent.Status = aggregateChildren(parent)
			if parent.Status == StepStatusSucceeded || parent.Status == StepStatusFailed || parent.Status == StepStatusDenied || parent.Status == StepStatusTimedOut {
				parent.CompletedAt = &now
			}
		}
		// Cancel running/pending forEach siblings when parent fails, is denied, or times out.
		if (parent.Status == StepStatusFailed || parent.Status == StepStatusDenied || parent.Status == StepStatusTimedOut) && stepDef != nil && stepDef.ForEach != "" {
			e.cancelForEachSiblings(ctx, run, parent, now)
		}
		run.Steps[baseStepID] = parent
		if parent.Status == StepStatusFailed || parent.Status == StepStatusDenied || parent.Status == StepStatusTimedOut {
			e.activateOnErrorHandler(ctx, run, wfDef, baseStepID, parent, now)
		}
		if retry && delay > 0 {
			e.scheduleAfter(delay, run.WorkflowID, run.ID)
		}
		if e.OnStepFinished != nil && !retry && (child.Status == StepStatusSucceeded || child.Status == StepStatusFailed || child.Status == StepStatusDenied || child.Status == StepStatusCancelled || child.Status == StepStatusTimedOut) {
			e.OnStepFinished(run.ID, stepID, child.Status)
		}
	} else {
		stepRun := run.Steps[stepID]
		if stepRun != nil && stepRun.JobID != "" && stepRun.JobID != res.JobId {
			return nil
		}
		if stepRun == nil {
			stepRun = &StepRun{StepID: stepID}
		}
		if stepRun.JobID == "" {
			stepRun.JobID = res.JobId
		}
		if attempt > stepRun.Attempts {
			stepRun.Attempts = attempt
		}
		if stepRun.JobID == res.JobId && shouldIgnoreProcessedResult(stepRun) {
			return nil
		}
		retry, delay := applyResult(stepRun, res, stepDef)
		if !retry && stepRun.Status == StepStatusSucceeded && res.ResultPtr != "" {
			if err := e.validateStepOutput(stepDef, res.ResultPtr); err != nil {
				stepRun.Status = StepStatusFailed
				stepRun.CompletedAt = &now
				stepRun.Error = map[string]any{"message": err.Error()}
				e.appendTimeline(ctx, run, "step_output_invalid", stepID, res.JobId, string(stepRun.Status), res.ResultPtr, err.Error(), nil)
			}
		}
		if !retry && (stepRun.Status == StepStatusSucceeded || stepRun.Status == StepStatusFailed || stepRun.Status == StepStatusDenied || stepRun.Status == StepStatusCancelled || stepRun.Status == StepStatusTimedOut) {
			e.appendTimeline(ctx, run, "step_completed", stepID, res.JobId, string(stepRun.Status), res.ResultPtr, res.ErrorMessage, nil)
		}
		if !retry && stepRun.Status == StepStatusSucceeded && res.ResultPtr != "" {
			if !e.checkStepOutputPolicy(ctx, run, stepID, stepRun, res) {
				recordStepOutput(ctx, e.mem, run, stepID, stepDef, res.ResultPtr, true)
			}
		}
		run.Steps[stepID] = stepRun
		if !retry && (stepRun.Status == StepStatusFailed || stepRun.Status == StepStatusDenied || stepRun.Status == StepStatusTimedOut) {
			e.activateOnErrorHandler(ctx, run, wfDef, stepID, stepRun, now)
		}
		if retry && delay > 0 {
			e.scheduleAfter(delay, run.WorkflowID, run.ID)
		}
		if e.OnStepFinished != nil && !retry && (stepRun.Status == StepStatusSucceeded || stepRun.Status == StepStatusFailed || stepRun.Status == StepStatusDenied || stepRun.Status == StepStatusCancelled || stepRun.Status == StepStatusTimedOut) {
			e.OnStepFinished(run.ID, stepID, stepRun.Status)
		}
	}

	run.UpdatedAt = now
	updateRunStatus(run, wfDef, now)
	if prevStatus != run.Status {
		slog.Debug("run state transition", "component", "workflow", "runId", run.ID, "traceId", run.ID, "from", string(prevStatus), "to", string(run.Status))
		e.appendTimeline(ctx, run, "run_status", "", "", string(run.Status), "", "", nil)
	}

	if err := e.store.UpdateRun(ctx, run); err != nil {
		slog.Error("update run", "run_id", run.ID, "error", err)
		return nil
	}
	if isTerminalRunStatus(run.Status) {
		e.markRunTerminal(run.ID)
	}

	if run.Status == RunStatusRunning {
		if err := e.scheduleReady(ctx, wfDef, run); err != nil {
			slog.Error("schedule ready", "run_id", run.ID, "error", err)
		}
	}
	return nil
}

// cancelForEachSiblings cancels running/pending forEach children when a sibling
// failure causes the parent to be marked Failed. Mirrors cancelParallelChildren.
func (e *Engine) cancelForEachSiblings(ctx context.Context, run *WorkflowRun, parent *StepRun, now time.Time) int {
	if parent == nil || parent.Children == nil {
		return 0
	}
	cancelled := 0
	for childID, child := range parent.Children {
		if child == nil || isTerminalStepStatus(child.Status) {
			continue
		}
		if child.JobID != "" {
			if err := e.publishJobCancel(child.JobID, "forEach sibling failed"); err != nil {
				slog.Error("cancel forEach orphan publish failed",
					"job_id", child.JobID, "step_id", childID, "error", err)
			}
		}
		child.Status = StepStatusCancelled
		child.CompletedAt = &now
		if child.Error == nil {
			child.Error = map[string]any{"message": "cancelled: forEach sibling failed"}
		}
		parent.Children[childID] = child
		if run != nil {
			run.Steps[childID] = child
		}
		slog.Warn("cancelled forEach orphan",
			"run_id", run.ID, "parent_step", parent.StepID, "cancelled_child", childID, "child_job_id", child.JobID)
		e.appendTimeline(ctx, run, "step_foreach_orphan_cancelled", childID, child.JobID, string(child.Status), "", "forEach sibling failed", nil)
		cancelled++
	}
	return cancelled
}

// activateOnErrorHandler activates the on_error handler step for a failed/timed-out step.
// Copies error context from stepRun.Error into the handler's Input["error"].
// No-op if the step has no OnError defined or the handler is already terminal.
func (e *Engine) activateOnErrorHandler(ctx context.Context, run *WorkflowRun, wfDef *Workflow, stepID string, stepRun *StepRun, now time.Time) {
	if run == nil || wfDef == nil || stepRun == nil {
		return
	}
	stepDef := wfDef.Steps[stepID]
	if stepDef == nil || stepDef.OnError == "" {
		return
	}
	if _, ok := wfDef.Steps[stepDef.OnError]; !ok {
		return
	}
	targetSR := run.Steps[stepDef.OnError]
	if targetSR == nil {
		targetSR = &StepRun{StepID: stepDef.OnError}
	}
	if targetSR.Status != "" && targetSR.Status != StepStatusPending {
		return // already activated or terminal
	}
	targetSR.Status = StepStatusPending
	if targetSR.Input == nil {
		targetSR.Input = make(map[string]any)
	}
	errCtx := make(map[string]any)
	if stepRun.Error != nil {
		for k, v := range stepRun.Error {
			errCtx[k] = v
		}
	}
	errCtx["step_id"] = stepID
	targetSR.Input["error"] = errCtx
	run.Steps[stepDef.OnError] = targetSR
	e.appendTimeline(ctx, run, "step_error_redirect", stepID, "", string(stepRun.Status), "", stepDef.OnError, nil)
	slog.Info("on_error handler activated",
		"run_id", run.ID, "step_id", stepID, "handler", stepDef.OnError, "trigger", string(stepRun.Status))
}

func (e *Engine) scheduleReady(ctx context.Context, wfDef *Workflow, run *WorkflowRun) error {
	if wfDef == nil || run == nil {
		return fmt.Errorf("workflow/run required")
	}
	if run.Status == RunStatusCancelled || run.Status == RunStatusFailed || run.Status == RunStatusDenied || run.Status == RunStatusSucceeded || run.Status == RunStatusTimedOut {
		e.markRunTerminal(run.ID)
		return nil
	}
	now := time.Now().UTC()
	prevStatus := run.Status
	if run.Status == RunStatusPending {
		run.Status = RunStatusRunning
		run.StartedAt = &now
	}
	if timedOut, err := e.enforceWorkflowTimeout(ctx, wfDef, run, now); err != nil {
		return fmt.Errorf("workflow schedule ready enforce timeout for run %s: %w", run.ID, err)
	} else if timedOut {
		return nil
	}
	parallelChildOwners := collectParallelChildOwners(wfDef)
	loopBodyOwners := collectLoopBodyOwners(wfDef)

	// Multi-pass: inline-completing steps (condition, switch, transform, etc.)
	// may unblock dependents already iterated in the same pass (Go map order
	// is random). Re-iterate until no new inline completions occur.
	maxPasses := len(wfDef.Steps)
	if maxPasses < 1 {
		maxPasses = 1
	}
	for pass := 0; pass < maxPasses; pass++ {
		terminalBefore := countTerminalSteps(run)
		for stepID, step := range wfDef.Steps {
			if ownerID, managed := parallelChildOwners[stepID]; managed && ownerID != stepID {
				// Child definitions listed by a parallel step are orchestrated by the parent handler.
				continue
			}
			if ownerID, managed := loopBodyOwners[stepID]; managed && ownerID != stepID {
				// Body definitions listed by a loop step are orchestrated by the parent loop handler.
				continue
			}
			parentSR := run.Steps[stepID]
			if parentSR == nil {
				parentSR = &StepRun{StepID: stepID}
			}
			if parentSR.Status != "" && parentSR.Status != StepStatusPending && parentSR.Status != StepStatusWaiting {
				// For-each steps may remain RUNNING while new children need dispatching as capacity frees up.
				if step.ForEach == "" && step.Type != StepTypeParallel && step.Type != StepTypeLoop && step.Type != StepTypeSubWorkflow {
					if step.Type != StepTypeDelay {
						continue
					}
				} else if parentSR.Status != StepStatusRunning {
					continue
				}
			}
			if !depsSatisfied(step, run, wfDef) {
				continue
			}
			// on_error target steps are only dispatched when explicitly activated.
			if isOnErrorTarget(wfDef, stepID) {
				if parentSR.Status == "" || (parentSR.Input == nil || parentSR.Input["error"] == nil) {
					continue
				}
			}
			// condition gate (non-condition/non-switch steps only)
			if step.Condition != "" && step.Type != StepTypeCondition && step.Type != StepTypeSwitch {
				ok, err := evalCondition(step.Condition, buildEvalScope(run, nil))
				if err != nil {
					slog.Error("condition eval failed", "step_id", stepID, "error", err)
					parentSR.Status = StepStatusFailed
					if parentSR.StartedAt == nil {
						parentSR.StartedAt = &now
					}
					parentSR.CompletedAt = &now
					parentSR.Error = map[string]any{"message": err.Error()}
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_condition_failed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}
				if !ok {
					parentSR.Status = StepStatusSucceeded
					t := now
					parentSR.StartedAt = &t
					parentSR.CompletedAt = &t
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_condition_skipped", stepID, "", string(parentSR.Status), "", "condition false", nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}
			}

			// Condition steps evaluate an expression and store the boolean result.
			if step.Type == StepTypeCondition {
				if parentSR.Status == "" || parentSR.Status == StepStatusPending {
					parentSR.Status = StepStatusRunning
					parentSR.StartedAt = &now
					condExpr := strings.TrimSpace(step.Condition)
					if condExpr == "" {
						parentSR.Status = StepStatusFailed
						parentSR.Error = map[string]any{"message": "condition expression required"}
						parentSR.CompletedAt = &now
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_condition_failed", stepID, "", string(parentSR.Status), "", "condition expression required", nil)
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}
					ok, err := evalCondition(condExpr, buildEvalScope(run, nil))
					if err != nil {
						parentSR.Status = StepStatusFailed
						parentSR.Error = map[string]any{"message": err.Error()}
						parentSR.CompletedAt = &now
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_condition_failed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}
					if err := e.validateInlineOutput(step, ok); err != nil {
						parentSR.Status = StepStatusFailed
						parentSR.Error = map[string]any{"message": err.Error()}
						parentSR.CompletedAt = &now
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_condition_failed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}
					parentSR.Status = StepStatusSucceeded
					parentSR.CompletedAt = &now
					parentSR.Output = ok
					run.Steps[stepID] = parentSR
					recordStepInlineOutput(run, stepID, step, ok)
					e.appendTimeline(ctx, run, "step_condition_evaluated", stepID, "", string(parentSR.Status), "", "", map[string]any{"value": ok})
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
				}
				run.Steps[stepID] = parentSR
				continue
			}

			// Approval steps dispatch a gate job so they appear in the unified
			// /approvals list. The scheduler places the job in APPROVAL_REQUIRED
			// state; once approved, it auto-completes and the result flows back
			// through HandleJobResult to advance the workflow. The approval job
			// must carry a persisted context_ptr with structured human decision
			// data; if payload build or context persistence fails we fail the step
			// before entering WAITING so operators never see a blind approval.
			if step.Type == StepTypeApproval {
				if parentSR.Status == "" || parentSR.Status == StepStatusPending {
					jobID := fmt.Sprintf("%s:%s@1", run.ID, stepID)
					req := e.buildApprovalGateRequest(ctx, wfDef, run, step, stepID, jobID)
					payload, err := e.buildApprovalPayload(wfDef, run, step, stepID, now)
					if err != nil {
						parentSR.Status = StepStatusFailed
						parentSR.Error = map[string]any{"message": err.Error()}
						run.Steps[stepID] = parentSR
						slog.Error("approval payload build failed", "run_id", run.ID, "step_id", stepID, "error", err)
						e.appendTimeline(ctx, run, "step_failed", stepID, jobID, string(parentSR.Status), "", err.Error(), nil)
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}
					ptr, err := e.putJobContext(ctx, jobID, payload)
					if err != nil || strings.TrimSpace(ptr) == "" {
						if err == nil {
							err = errors.New("approval context store unavailable")
						}
						parentSR.Status = StepStatusFailed
						parentSR.Error = map[string]any{"message": err.Error()}
						parentSR.Input = payload
						run.Steps[stepID] = parentSR
						slog.Error("approval context store failed", "run_id", run.ID, "step_id", stepID, "job_id", jobID, "error", err)
						e.appendTimeline(ctx, run, "step_failed", stepID, jobID, string(parentSR.Status), "", err.Error(), map[string]any{"approval_payload": payload})
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}
					req.ContextPtr = ptr

					parentSR.Status = StepStatusWaiting
					parentSR.StartedAt = &now
					parentSR.JobID = jobID
					parentSR.Attempts = 1
					if parentSR.Input != nil {
						for k, v := range parentSR.Input {
							if _, exists := payload[k]; !exists {
								payload[k] = v
							}
						}
					}
					parentSR.Input = payload
					run.Status = RunStatusWaiting
					run.Steps[stepID] = parentSR

					if err := e.store.UpdateRun(ctx, run); err != nil {
						slog.Error("approval gate pre-dispatch persist failed", "run_id", run.ID, "step_id", stepID, "error", err)
						parentSR.Status = StepStatusPending
						parentSR.JobID = ""
						parentSR.Attempts = 0
						parentSR.StartedAt = nil
						run.Steps[stepID] = parentSR
						continue
					}

					packet := makeJobPacket(run.ID, req)
					if err := e.bus.Publish(capsdk.SubjectSubmit, packet); err != nil {
						slog.Error("approval gate dispatch failed", "run_id", run.ID, "step_id", stepID, "error", err)
						parentSR.Status = StepStatusPending
						parentSR.JobID = ""
						parentSR.Attempts = 0
						parentSR.StartedAt = nil
						run.Steps[stepID] = parentSR
						if updateErr := e.store.UpdateRun(ctx, run); updateErr != nil {
							slog.Error("workflow: failed to persist approval gate revert",
								"run_id", run.ID, "step_id", stepID, "error", updateErr)
						}
					} else {
						e.appendTimeline(ctx, run, "step_waiting", stepID, jobID, string(parentSR.Status), "", "approval requested", map[string]any{"context_ptr": req.ContextPtr})
						if e.OnStepDispatched != nil {
							e.OnStepDispatched(run.ID, stepID, jobID)
						}
					}
				}
				run.Steps[stepID] = parentSR
				continue
			}

			// Delay steps wait for a timer before succeeding.
			if step.Type == StepTypeDelay {
				delay, err := delayForStep(step, now)
				if err != nil {
					parentSR.Status = StepStatusFailed
					parentSR.Error = map[string]any{"message": err.Error()}
					parentSR.CompletedAt = &now
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_delay_failed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
					continue
				}
				if parentSR.Status == "" || parentSR.Status == StepStatusPending {
					parentSR.Status = StepStatusRunning
					parentSR.StartedAt = &now
					if delay <= 0 {
						parentSR.Status = StepStatusSucceeded
						parentSR.CompletedAt = &now
						parentSR.NextAttemptAt = nil
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_delay_completed", stepID, "", string(parentSR.Status), "", "delay completed", nil)
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}
					next := now.Add(delay)
					parentSR.NextAttemptAt = &next
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_delay_started", stepID, "", string(parentSR.Status), "", "delay started", map[string]any{"delay_ms": delay.Milliseconds()})
					e.scheduleAfter(delay, run.WorkflowID, run.ID)
					continue
				}
				if parentSR.Status == StepStatusRunning && parentSR.NextAttemptAt != nil && !parentSR.NextAttemptAt.After(now) {
					parentSR.Status = StepStatusSucceeded
					parentSR.CompletedAt = &now
					parentSR.NextAttemptAt = nil
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_delay_completed", stepID, "", string(parentSR.Status), "", "delay completed", nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}
				run.Steps[stepID] = parentSR
				continue
			}

			// Emit event steps publish a system alert to the bus.
			if step.Type == StepTypeNotify {
				if parentSR.Status == "" || parentSR.Status == StepStatusPending {
					payload, err := evalTemplates(step.Input, buildEvalScope(run, nil))
					if err != nil {
						parentSR.Status = StepStatusFailed
						parentSR.Error = map[string]any{"message": err.Error()}
						parentSR.CompletedAt = &now
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_event_failed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
						continue
					}
					if e.bus == nil {
						parentSR.Status = StepStatusFailed
						parentSR.Error = map[string]any{"message": "bus unavailable"}
						parentSR.CompletedAt = &now
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_event_failed", stepID, "", string(parentSR.Status), "", "bus unavailable", nil)
						continue
					}
					alert := buildEventAlert(step, payload)
					if alert.TraceId == "" {
						alert.TraceId = run.ID
					}
					packet := &pb.BusPacket{
						TraceId:         run.ID,
						SenderId:        "workflow-engine",
						CreatedAt:       timestamppb.Now(),
						ProtocolVersion: capsdk.DefaultProtocolVersion,
						Payload:         &pb.BusPacket_Alert{Alert: alert},
					}
					if err := e.bus.Publish(capsdk.SubjectWorkflowEvent, packet); err != nil {
						parentSR.Status = StepStatusFailed
						parentSR.Error = map[string]any{"message": err.Error()}
						parentSR.CompletedAt = &now
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_event_failed", stepID, "", string(parentSR.Status), "", err.Error(), map[string]any{"payload": payload})
						continue
					}
					parentSR.Status = StepStatusSucceeded
					parentSR.StartedAt = &now
					parentSR.CompletedAt = &now
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_event_emitted", stepID, "", string(parentSR.Status), "", alert.Message, map[string]any{"payload": payload})
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
				}
				continue
			}

			// Transform steps evaluate input expressions inline and store the result.
			if step.Type == StepTypeTransform {
				if parentSR.Status == "" || parentSR.Status == StepStatusPending {
					parentSR.Status = StepStatusRunning
					parentSR.StartedAt = &now
					if step.Input == nil {
						// No input → succeed with empty output.
						result := map[string]any{}
						parentSR.Status = StepStatusSucceeded
						parentSR.CompletedAt = &now
						parentSR.Output = result
						run.Steps[stepID] = parentSR
						recordStepInlineOutput(run, stepID, step, result)
						e.appendTimeline(ctx, run, "step_transform_completed", stepID, "", string(parentSR.Status), "", "", map[string]any{"keys": 0})
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}
					result, err := evalTemplates(step.Input, buildEvalScope(run, nil))
					if err != nil {
						parentSR.Status = StepStatusFailed
						parentSR.Error = map[string]any{"message": fmt.Sprintf("transform expression error: %s", err.Error())}
						parentSR.CompletedAt = &now
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_transform_failed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}
					if err := e.validateInlineOutput(step, result); err != nil {
						parentSR.Status = StepStatusFailed
						parentSR.Error = map[string]any{"message": err.Error()}
						parentSR.CompletedAt = &now
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_transform_failed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}
					parentSR.Status = StepStatusSucceeded
					parentSR.CompletedAt = &now
					parentSR.Output = result
					run.Steps[stepID] = parentSR
					recordStepInlineOutput(run, stepID, step, result)
					e.appendTimeline(ctx, run, "step_transform_completed", stepID, "", string(parentSR.Status), "", "", nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
				}
				run.Steps[stepID] = parentSR
				continue
			}

			// Switch steps evaluate expression and choose a single branch (inline, no dispatch).
			if step.Type == StepTypeSwitch {
				if parentSR.Status == "" || parentSR.Status == StepStatusPending {
					parentSR.Status = StepStatusRunning
					parentSR.StartedAt = &now
				}
				condExpr := strings.TrimSpace(step.Condition)
				if condExpr == "" {
					parentSR.Status = StepStatusFailed
					parentSR.CompletedAt = &now
					parentSR.Error = map[string]any{"message": "switch expression required"}
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_switch_evaluated", stepID, "", string(parentSR.Status), "", "switch expression required", nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}
				scope := buildEvalScope(run, nil)
				switchValue, err := Eval(condExpr, scope)
				if err != nil {
					parentSR.Status = StepStatusFailed
					parentSR.CompletedAt = &now
					parentSR.Error = map[string]any{"message": err.Error()}
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_switch_evaluated", stepID, "", string(parentSR.Status), "", err.Error(), nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}
				cases, err := parseSwitchCases(step, scope)
				if err != nil {
					parentSR.Status = StepStatusFailed
					parentSR.CompletedAt = &now
					parentSR.Error = map[string]any{"message": err.Error()}
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_switch_evaluated", stepID, "", string(parentSR.Status), "", err.Error(), nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}
				defaultStepID, err := parseSwitchDefault(step, scope)
				if err != nil {
					parentSR.Status = StepStatusFailed
					parentSR.CompletedAt = &now
					parentSR.Error = map[string]any{"message": err.Error()}
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_switch_evaluated", stepID, "", string(parentSR.Status), "", err.Error(), nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}

				invalidTarget := ""
				for _, candidate := range cases {
					if wfDef.Steps[candidate.StepID] == nil {
						invalidTarget = fmt.Sprintf("switch case target step %q not found", candidate.StepID)
						break
					}
				}
				if invalidTarget == "" && defaultStepID != "" && wfDef.Steps[defaultStepID] == nil {
					invalidTarget = fmt.Sprintf("switch default target step %q not found", defaultStepID)
				}
				if invalidTarget != "" {
					parentSR.Status = StepStatusFailed
					parentSR.CompletedAt = &now
					parentSR.Error = map[string]any{"message": invalidTarget}
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_switch_evaluated", stepID, "", string(parentSR.Status), "", invalidTarget, nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}

				targetStepID := ""
				matchedCase := "default"
				for _, candidate := range cases {
					if switchValueEquals(switchValue, candidate.MatchValue) {
						targetStepID = candidate.StepID
						matchedCase = fmt.Sprint(candidate.MatchValue)
						break
					}
				}
				if targetStepID == "" {
					targetStepID = defaultStepID
				}
				if strings.TrimSpace(targetStepID) == "" {
					parentSR.Status = StepStatusFailed
					parentSR.CompletedAt = &now
					parentSR.Error = map[string]any{"message": "no matching case"}
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_switch_evaluated", stepID, "", string(parentSR.Status), "", "no matching case", map[string]any{"value": switchValue})
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}

				unmatchedTargets := map[string]struct{}{}
				for _, candidate := range cases {
					if candidate.StepID == "" || candidate.StepID == targetStepID {
						continue
					}
					unmatchedTargets[candidate.StepID] = struct{}{}
				}
				if defaultStepID != "" && defaultStepID != targetStepID {
					unmatchedTargets[defaultStepID] = struct{}{}
				}
				for branchStepID := range unmatchedTargets {
					branchRun := run.Steps[branchStepID]
					if branchRun == nil {
						branchRun = &StepRun{StepID: branchStepID}
					}
					if isTerminalStepStatus(branchRun.Status) {
						continue
					}
					branchRun.Status = StepStatusCancelled
					if branchRun.StartedAt == nil {
						branchRun.StartedAt = &now
					}
					branchRun.CompletedAt = &now
					branchRun.Error = map[string]any{
						"message": "branch not taken",
						"reason":  switchBranchNotTakenReason,
					}
					run.Steps[branchStepID] = branchRun
				}

				output := map[string]any{
					"matched_case":  matchedCase,
					"matched_value": switchValue,
					"target_step":   targetStepID,
				}
				if err := e.validateInlineOutput(step, output); err != nil {
					parentSR.Status = StepStatusFailed
					parentSR.CompletedAt = &now
					parentSR.Error = map[string]any{"message": err.Error()}
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_switch_evaluated", stepID, "", string(parentSR.Status), "", err.Error(), nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}

				parentSR.Status = StepStatusSucceeded
				parentSR.CompletedAt = &now
				parentSR.Error = nil
				parentSR.Output = output
				run.Steps[stepID] = parentSR
				recordStepInlineOutput(run, stepID, step, output)

				// Mark selected branch step as pending so scheduleReady handles it on
				// the next iteration, respecting the target step's type and deps.
				targetSR := run.Steps[targetStepID]
				if targetSR == nil {
					targetSR = &StepRun{StepID: targetStepID, Status: StepStatusPending}
				}
				if targetSR.Status == "" {
					targetSR.Status = StepStatusPending
				}
				run.Steps[targetStepID] = targetSR

				e.appendTimeline(ctx, run, "step_switch_evaluated", stepID, "", string(parentSR.Status), "", "", output)
				if e.OnStepFinished != nil {
					e.OnStepFinished(run.ID, stepID, parentSR.Status)
				}
				continue
			}

			// Sub-workflow steps orchestrate a child workflow run and wait for completion.
			if step.Type == StepTypeSubWorkflow {
				scope := buildEvalScope(run, nil)
				childWorkflowID, inputMapping, outputMapping, err := resolveSubWorkflowConfig(step, scope)
				if err != nil {
					parentSR.Status = StepStatusFailed
					if parentSR.StartedAt == nil {
						parentSR.StartedAt = &now
					}
					parentSR.CompletedAt = &now
					parentSR.Error = map[string]any{"message": err.Error()}
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_subworkflow_completed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}

				childRunID := strings.TrimSpace(parentSR.JobID)
				if childRunID == "" {
					callStack := normalizeCallStackForRun(run)
					if containsString(callStack, childWorkflowID) {
						msg := fmt.Sprintf("circular workflow reference detected: %s", childWorkflowID)
						parentSR.Status = StepStatusFailed
						if parentSR.StartedAt == nil {
							parentSR.StartedAt = &now
						}
						parentSR.CompletedAt = &now
						parentSR.Error = map[string]any{"message": msg}
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_subworkflow_completed", stepID, "", string(parentSR.Status), "", msg, nil)
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}

					childInput, err := normalizeSubWorkflowInput(run, inputMapping)
					if err != nil {
						parentSR.Status = StepStatusFailed
						if parentSR.StartedAt == nil {
							parentSR.StartedAt = &now
						}
						parentSR.CompletedAt = &now
						parentSR.Error = map[string]any{"message": err.Error()}
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_subworkflow_completed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}

					if _, err := e.store.GetWorkflow(ctx, childWorkflowID); err != nil {
						msg := fmt.Sprintf("get child workflow: %v", err)
						parentSR.Status = StepStatusFailed
						if parentSR.StartedAt == nil {
							parentSR.StartedAt = &now
						}
						parentSR.CompletedAt = &now
						parentSR.Error = map[string]any{"message": msg}
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_subworkflow_completed", stepID, "", string(parentSR.Status), "", msg, nil)
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}

					childRunID = uuid.NewString()
					childLabels := cloneStringMap(run.Labels)
					childMetadata := cloneStringMap(run.Metadata)
					if childMetadata == nil {
						childMetadata = map[string]string{}
					}
					childStack := append(append([]string{}, callStack...), childWorkflowID)
					childMetadata["parent_run_id"] = run.ID
					childMetadata["parent_step_id"] = stepID
					childMetadata["call_stack"] = encodeCallStack(childStack)
					if run.DryRun || run.Metadata["dry_run"] == "true" {
						childMetadata["dry_run"] = "true"
						if childLabels == nil {
							childLabels = map[string]string{}
						}
						childLabels["dry_run"] = "true"
					}

					childRun := &WorkflowRun{
						ID:          childRunID,
						WorkflowID:  childWorkflowID,
						OrgID:       run.OrgID,
						TeamID:      run.TeamID,
						Input:       childInput,
						Context:     map[string]any{},
						Status:      RunStatusPending,
						Steps:       map[string]*StepRun{},
						TriggeredBy: run.TriggeredBy,
						CreatedAt:   now,
						UpdatedAt:   now,
						Labels:      childLabels,
						Metadata:    childMetadata,
						DryRun:      run.DryRun || run.Metadata["dry_run"] == "true",
					}
					if err := e.store.CreateRun(ctx, childRun); err != nil {
						msg := fmt.Sprintf("create child run: %v", err)
						parentSR.Status = StepStatusFailed
						if parentSR.StartedAt == nil {
							parentSR.StartedAt = &now
						}
						parentSR.CompletedAt = &now
						parentSR.Error = map[string]any{"message": msg}
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_subworkflow_completed", stepID, "", string(parentSR.Status), "", msg, nil)
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}
					if err := e.StartRun(ctx, childWorkflowID, childRunID); err != nil {
						msg := fmt.Sprintf("start child run: %v", err)
						parentSR.Status = StepStatusFailed
						if parentSR.StartedAt == nil {
							parentSR.StartedAt = &now
						}
						parentSR.CompletedAt = &now
						parentSR.Error = map[string]any{"message": msg}
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_subworkflow_completed", stepID, childRunID, string(parentSR.Status), "", msg, nil)
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}

					parentSR.Status = StepStatusRunning
					if parentSR.StartedAt == nil {
						parentSR.StartedAt = &now
					}
					parentSR.CompletedAt = nil
					parentSR.JobID = childRunID
					parentSR.Error = nil
					parentSR.Output = map[string]any{
						"workflow_id": childWorkflowID,
						"run_id":      childRunID,
						"status":      string(RunStatusRunning),
					}
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_subworkflow_started", stepID, childRunID, string(parentSR.Status), "", "", map[string]any{
						"workflow_id": childWorkflowID,
						"run_id":      childRunID,
					})
					continue
				}

				childRun, err := e.store.GetRun(ctx, childRunID)
				if err != nil {
					msg := fmt.Sprintf("get child run: %v", err)
					parentSR.Status = StepStatusFailed
					if parentSR.StartedAt == nil {
						parentSR.StartedAt = &now
					}
					parentSR.CompletedAt = &now
					parentSR.Error = map[string]any{"message": msg}
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_subworkflow_completed", stepID, childRunID, string(parentSR.Status), "", msg, nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}
				parentSR.Output = map[string]any{
					"workflow_id": childWorkflowID,
					"run_id":      childRunID,
					"status":      string(childRun.Status),
				}
				switch childRun.Status {
				case RunStatusPending, RunStatusRunning, RunStatusWaiting:
					parentSR.Status = StepStatusRunning
					run.Steps[stepID] = parentSR
					continue
				case RunStatusSucceeded:
					mappedOutput, err := buildSubWorkflowOutput(run, childRun, outputMapping)
					if err != nil {
						parentSR.Status = StepStatusFailed
						parentSR.CompletedAt = &now
						parentSR.Error = map[string]any{"message": err.Error()}
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_subworkflow_completed", stepID, childRunID, string(parentSR.Status), "", err.Error(), nil)
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}
					if err := e.validateInlineOutput(step, mappedOutput); err != nil {
						parentSR.Status = StepStatusFailed
						parentSR.CompletedAt = &now
						parentSR.Error = map[string]any{"message": err.Error()}
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_subworkflow_completed", stepID, childRunID, string(parentSR.Status), "", err.Error(), nil)
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}
					parentSR.Status = StepStatusSucceeded
					parentSR.CompletedAt = &now
					parentSR.Error = nil
					parentSR.Output = mappedOutput
					run.Steps[stepID] = parentSR
					recordStepInlineOutput(run, stepID, step, mappedOutput)
					e.appendTimeline(ctx, run, "step_subworkflow_completed", stepID, childRunID, string(parentSR.Status), "", "", map[string]any{
						"workflow_id":  childWorkflowID,
						"run_id":       childRunID,
						"child_status": string(childRun.Status),
					})
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				case RunStatusFailed, RunStatusDenied, RunStatusCancelled, RunStatusTimedOut:
					msg := fmt.Sprintf("child run %s ended with status %s", childRunID, childRun.Status)
					if childRun.Error != nil {
						if childMsg, ok := childRun.Error["message"].(string); ok && strings.TrimSpace(childMsg) != "" {
							msg = childMsg
						}
					}
					if childStepMsg := subWorkflowChildErrorMessage(childRun); childStepMsg != "" {
						msg = childStepMsg
					}
					parentSR.Status = subWorkflowTerminalStatus(childRun.Status)
					parentSR.CompletedAt = &now
					parentSR.Error = map[string]any{
						"message":      msg,
						"child_run_id": childRunID,
						"child_status": string(childRun.Status),
					}
					parentSR.Output = map[string]any{
						"workflow_id": childWorkflowID,
						"run_id":      childRunID,
						"status":      string(childRun.Status),
					}
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_subworkflow_completed", stepID, childRunID, string(parentSR.Status), "", msg, map[string]any{
						"workflow_id":  childWorkflowID,
						"run_id":       childRunID,
						"child_status": string(childRun.Status),
					})
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				default:
					parentSR.Status = StepStatusRunning
					run.Steps[stepID] = parentSR
					continue
				}
			}

			if step.Type == StepTypeParallel || step.Type == StepTypeLoop {
				var (
					childStepIDs []string
					strategy     string
					required     int
					err          error
				)
				if step.Type == StepTypeParallel {
					childStepIDs, strategy, required, err = resolveParallelConfig(step, buildEvalScope(run, nil))
					if err != nil {
						parentSR.Status = StepStatusFailed
						if parentSR.StartedAt == nil {
							parentSR.StartedAt = &now
						}
						parentSR.CompletedAt = &now
						parentSR.Error = map[string]any{"message": err.Error()}
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_parallel_completed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}
				}

				// Loop repeats a body step until condition/until stops or cap is reached.
				if step.Type == StepTypeLoop {
					maxIterations, conditionExpr, untilExpr, bodyStepID, err := resolveLoopConfig(step)
					if err != nil {
						parentSR.Status = StepStatusFailed
						if parentSR.StartedAt == nil {
							parentSR.StartedAt = &now
						}
						parentSR.CompletedAt = &now
						parentSR.Error = map[string]any{"message": err.Error()}
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_loop_completed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}
					bodyStep := step
					if bodyStepID != "" {
						bodyStep = wfDef.Steps[bodyStepID]
						if bodyStep == nil {
							msg := fmt.Sprintf("loop body_step %q not found", bodyStepID)
							parentSR.Status = StepStatusFailed
							if parentSR.StartedAt == nil {
								parentSR.StartedAt = &now
							}
							parentSR.CompletedAt = &now
							parentSR.Error = map[string]any{"message": msg}
							run.Steps[stepID] = parentSR
							e.appendTimeline(ctx, run, "step_loop_completed", stepID, "", string(parentSR.Status), "", msg, nil)
							if e.OnStepFinished != nil {
								e.OnStepFinished(run.ID, stepID, parentSR.Status)
							}
							continue
						}
					}
					if parentSR.Children == nil {
						parentSR.Children = make(map[string]*StepRun)
					}
					if parentSR.Status == "" || parentSR.Status == StepStatusPending {
						parentSR.Status = StepStatusRunning
						if parentSR.StartedAt == nil {
							parentSR.StartedAt = &now
						}
					}

					hasActive := false
					hasFailed := false
					for _, child := range parentSR.Children {
						if child == nil {
							continue
						}
						switch child.Status {
						case StepStatusFailed, StepStatusCancelled, StepStatusTimedOut:
							hasFailed = true
						case StepStatusRunning, StepStatusWaiting, StepStatusPending:
							hasActive = true
						}
					}
					if hasFailed {
						parentSR.Status = StepStatusFailed
						parentSR.CompletedAt = &now
						if parentSR.Error == nil {
							parentSR.Error = map[string]any{"message": "loop child iteration failed"}
						}
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_loop_completed", stepID, "", string(parentSR.Status), "", "loop child iteration failed", nil)
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}
					if hasActive {
						run.Steps[stepID] = parentSR
						continue
					}

					nextIndex := len(parentSR.Children)
					var previousOutput any
					if nextIndex > 0 {
						previousOutput = loopPreviousOutput(run, fmt.Sprintf("%s[%d]", stepID, nextIndex-1))
					}
					scope := buildLoopEvalScope(run, nextIndex, previousOutput)

					if nextIndex >= maxIterations {
						if conditionExpr != "" || untilExpr != "" {
							msg := fmt.Sprintf("loop max_iterations exceeded (%d)", maxIterations)
							parentSR.Status = StepStatusFailed
							parentSR.CompletedAt = &now
							parentSR.Error = map[string]any{"message": msg}
							run.Steps[stepID] = parentSR
							e.appendTimeline(ctx, run, "step_loop_completed", stepID, "", string(parentSR.Status), "", msg, map[string]any{"iterations": nextIndex, "max_iterations": maxIterations})
							if e.OnStepFinished != nil {
								e.OnStepFinished(run.ID, stepID, parentSR.Status)
							}
							continue
						}
						parentSR.Status = StepStatusSucceeded
						parentSR.CompletedAt = &now
						parentSR.Error = nil
						parentSR.Output = map[string]any{
							"iterations":  nextIndex,
							"last_output": previousOutput,
						}
						run.Steps[stepID] = parentSR
						recordStepInlineOutput(run, stepID, step, parentSR.Output)
						e.appendTimeline(ctx, run, "step_loop_completed", stepID, "", string(parentSR.Status), "", "", map[string]any{"iterations": nextIndex, "max_iterations": maxIterations, "reason": "max_iterations_reached"})
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}

					shouldContinue := true
					if conditionExpr != "" {
						ok, err := evalCondition(conditionExpr, scope)
						if err != nil {
							parentSR.Status = StepStatusFailed
							parentSR.CompletedAt = &now
							parentSR.Error = map[string]any{"message": err.Error()}
							run.Steps[stepID] = parentSR
							e.appendTimeline(ctx, run, "step_loop_completed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
							if e.OnStepFinished != nil {
								e.OnStepFinished(run.ID, stepID, parentSR.Status)
							}
							continue
						}
						shouldContinue = shouldContinue && ok
					}
					if untilExpr != "" {
						ok, err := evalCondition(untilExpr, scope)
						if err != nil {
							parentSR.Status = StepStatusFailed
							parentSR.CompletedAt = &now
							parentSR.Error = map[string]any{"message": err.Error()}
							run.Steps[stepID] = parentSR
							e.appendTimeline(ctx, run, "step_loop_completed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
							if e.OnStepFinished != nil {
								e.OnStepFinished(run.ID, stepID, parentSR.Status)
							}
							continue
						}
						if ok {
							shouldContinue = false
						}
					}
					if !shouldContinue {
						parentSR.Status = StepStatusSucceeded
						parentSR.CompletedAt = &now
						parentSR.Error = nil
						parentSR.Output = map[string]any{
							"iterations":  nextIndex,
							"last_output": previousOutput,
						}
						run.Steps[stepID] = parentSR
						recordStepInlineOutput(run, stepID, step, parentSR.Output)
						e.appendTimeline(ctx, run, "step_loop_completed", stepID, "", string(parentSR.Status), "", "", map[string]any{"iterations": nextIndex, "max_iterations": maxIterations, "reason": "condition_stopped"})
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}

					childID := fmt.Sprintf("%s[%d]", stepID, nextIndex)
					child := parentSR.Children[childID]
					if child == nil {
						child = &StepRun{StepID: childID, Status: StepStatusPending}
					}
					if child.NextAttemptAt != nil && child.NextAttemptAt.After(now) {
						parentSR.Children[childID] = child
						run.Steps[childID] = child
						run.Steps[stepID] = parentSR
						continue
					}
					jobID := fmt.Sprintf("%s:%s@%d", run.ID, childID, child.Attempts+1)
					req := e.buildJobRequest(ctx, wfDef, run, bodyStep, childID, jobID)
					if req.Env == nil {
						req.Env = map[string]string{}
					}
					req.Env["loop_index"] = fmt.Sprintf("%d", nextIndex)
					req.Env["loop_iteration"] = fmt.Sprintf("%d", nextIndex+1)
					if encoded, err := json.Marshal(previousOutput); err == nil {
						req.Env["loop_previous_output"] = string(encoded)
					}
					payload, err := e.buildLoopPayload(run, bodyStep, scope)
					if err != nil {
						child.Status = StepStatusFailed
						child.Error = map[string]any{"message": err.Error()}
						parentSR.Children[childID] = child
						run.Steps[childID] = child
						run.Steps[stepID] = parentSR
						continue
					}
					if ptr, err := e.putJobContext(ctx, jobID, payload); err != nil {
						child.Status = StepStatusFailed
						child.Error = map[string]any{"message": err.Error()}
						child.Input = payload
						parentSR.Children[childID] = child
						run.Steps[childID] = child
						run.Steps[stepID] = parentSR
						continue
					} else if ptr != "" {
						req.ContextPtr = ptr
					}
					packet := makeJobPacket(run.ID, req)
					if err := e.bus.Publish(capsdk.SubjectSubmit, packet); err != nil {
						slog.Error("publish loop step", "run_id", run.ID, "step_id", childID, "error", err)
						child.Status = StepStatusFailed
						child.Error = map[string]any{"message": err.Error()}
					} else {
						child.Status = StepStatusRunning
						child.StartedAt = &now
						child.Attempts++
						child.JobID = jobID
						child.Input = payload
						e.appendTimeline(ctx, run, "step_loop_iteration", stepID, child.JobID, string(child.Status), "", "", map[string]any{
							"index":       nextIndex,
							"iteration":   nextIndex + 1,
							"body_step":   bodyStep.ID,
							"context_ptr": req.ContextPtr,
						})
						e.appendTimeline(ctx, run, "step_dispatched", childID, jobID, string(child.Status), "", "", map[string]any{
							"loop_index": nextIndex,
						})
						if e.OnStepDispatched != nil {
							e.OnStepDispatched(run.ID, childID, jobID)
						}
					}
					parentSR.Children[childID] = child
					run.Steps[childID] = child
					run.Steps[stepID] = parentSR
					continue
				}
				if ownerID, exists := parallelChildOwners[stepID]; exists && ownerID != stepID {
					msg := fmt.Sprintf("parallel step id %q is reserved as child of %q", stepID, ownerID)
					parentSR.Status = StepStatusFailed
					if parentSR.StartedAt == nil {
						parentSR.StartedAt = &now
					}
					parentSR.CompletedAt = &now
					parentSR.Error = map[string]any{"message": msg}
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_parallel_completed", stepID, "", string(parentSR.Status), "", msg, nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}
				if parentSR.Children == nil {
					parentSR.Children = make(map[string]*StepRun)
				}
				if len(childStepIDs) == 0 {
					parentSR.Status = StepStatusSucceeded
					parentSR.StartedAt = &now
					parentSR.CompletedAt = &now
					parentSR.Output = map[string]any{}
					run.Steps[stepID] = parentSR
					recordStepInlineOutput(run, stepID, step, map[string]any{})
					e.appendTimeline(ctx, run, "step_parallel_completed", stepID, "", string(parentSR.Status), "", "", map[string]any{
						"strategy": strategy,
						"required": required,
						"total":    0,
					})
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}

				if parentSR.Status == "" || parentSR.Status == StepStatusPending {
					parentSR.Status = StepStatusRunning
					if parentSR.StartedAt == nil {
						parentSR.StartedAt = &now
					}
					e.appendTimeline(ctx, run, "step_parallel_started", stepID, "", string(parentSR.Status), "", "", map[string]any{
						"strategy": strategy,
						"required": required,
						"total":    len(childStepIDs),
					})
				}

				activeChildren := make(map[string]struct{}, len(childStepIDs))
				initErr := ""
				for _, childStepID := range childStepIDs {
					activeChildren[childStepID] = struct{}{}
					childDef := wfDef.Steps[childStepID]
					if childDef == nil {
						initErr = fmt.Sprintf("parallel child step %q not found", childStepID)
						break
					}
					if ownerID, managed := parallelChildOwners[childStepID]; managed && ownerID != stepID {
						initErr = fmt.Sprintf("parallel child step %q is reserved by %q", childStepID, ownerID)
						break
					}
					child := run.Steps[childStepID]
					if child == nil {
						child = &StepRun{StepID: childStepID, Status: StepStatusPending}
					} else if child.Status == "" {
						child.Status = StepStatusPending
					}
					parentSR.Children[childStepID] = child
					run.Steps[childStepID] = child
				}
				for childID := range parentSR.Children {
					if _, ok := activeChildren[childID]; !ok {
						delete(parentSR.Children, childID)
					}
				}
				if initErr != "" {
					parentSR.Status = StepStatusFailed
					if parentSR.StartedAt == nil {
						parentSR.StartedAt = &now
					}
					parentSR.CompletedAt = &now
					parentSR.Error = map[string]any{"message": initErr}
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_parallel_completed", stepID, "", string(parentSR.Status), "", initErr, nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}

				// Terminal evaluation before dispatching new work.
				succeededCount, failedCount, runningCount := summarizeParallelChildren(parentSR, childStepIDs)
				if done, success, message := evaluateParallelOutcome(strategy, required, len(childStepIDs), succeededCount, failedCount); done {
					if success {
						cancelled := e.cancelParallelChildren(parentSR, run, childStepIDs, now)
						aggregated := aggregateParallelOutputs(run, childStepIDs)
						if err := e.validateInlineOutput(step, aggregated); err != nil {
							parentSR.Status = StepStatusFailed
							parentSR.CompletedAt = &now
							parentSR.Error = map[string]any{"message": err.Error()}
							run.Steps[stepID] = parentSR
							e.appendTimeline(ctx, run, "step_parallel_completed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
							if e.OnStepFinished != nil {
								e.OnStepFinished(run.ID, stepID, parentSR.Status)
							}
							continue
						}
						parentSR.Status = StepStatusSucceeded
						parentSR.CompletedAt = &now
						parentSR.Error = nil
						parentSR.Output = aggregated
						run.Steps[stepID] = parentSR
						recordStepInlineOutput(run, stepID, step, aggregated)
						e.appendTimeline(ctx, run, "step_parallel_completed", stepID, "", string(parentSR.Status), "", "", map[string]any{
							"strategy":        strategy,
							"required":        required,
							"total":           len(childStepIDs),
							"succeeded_count": succeededCount,
							"failed_count":    failedCount,
							"cancelled_count": cancelled,
						})
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}
					cancelled := e.cancelParallelChildren(parentSR, run, childStepIDs, now)
					parentSR.Status = StepStatusFailed
					parentSR.CompletedAt = &now
					parentSR.Error = map[string]any{"message": message}
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_parallel_completed", stepID, "", string(parentSR.Status), "", message, map[string]any{
						"strategy":        strategy,
						"required":        required,
						"total":           len(childStepIDs),
						"succeeded_count": succeededCount,
						"failed_count":    failedCount,
						"cancelled_count": cancelled,
					})
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}

				// Dispatch ready children with optional max_parallel throttling.
				for _, childStepID := range childStepIDs {
					if step.MaxParallel > 0 && runningCount >= step.MaxParallel {
						break
					}
					childDef := wfDef.Steps[childStepID]
					if childDef == nil {
						continue
					}
					child := run.Steps[childStepID]
					if child == nil {
						child = &StepRun{StepID: childStepID, Status: StepStatusPending}
					}
					if child.Status != "" && child.Status != StepStatusPending {
						continue
					}
					if child.NextAttemptAt != nil && child.NextAttemptAt.After(now) {
						continue
					}
					jobID := fmt.Sprintf("%s:%s@%d", run.ID, childStepID, child.Attempts+1)
					req := e.buildJobRequest(ctx, wfDef, run, childDef, childStepID, jobID)
					payload, err := e.buildJobPayload(run, childDef, nil)
					if err != nil {
						child.Status = StepStatusFailed
						child.Error = map[string]any{"message": err.Error()}
						parentSR.Children[childStepID] = child
						run.Steps[childStepID] = child
						continue
					}
					if ptr, err := e.putJobContext(ctx, jobID, payload); err != nil {
						child.Status = StepStatusFailed
						child.Error = map[string]any{"message": err.Error()}
						child.Input = payload
						parentSR.Children[childStepID] = child
						run.Steps[childStepID] = child
						continue
					} else if ptr != "" {
						req.ContextPtr = ptr
					}
					packet := makeJobPacket(run.ID, req)
					if err := e.bus.Publish(capsdk.SubjectSubmit, packet); err != nil {
						slog.Error("publish parallel child step", "run_id", run.ID, "step_id", childStepID, "error", err)
						child.Status = StepStatusFailed
						child.Error = map[string]any{"message": err.Error()}
					} else {
						child.Status = StepStatusRunning
						child.StartedAt = &now
						child.Attempts++
						child.JobID = jobID
						child.Input = payload
						runningCount++
						var data map[string]any
						if req.ContextPtr != "" {
							data = map[string]any{"context_ptr": req.ContextPtr}
						}
						e.appendTimeline(ctx, run, "step_dispatched", childStepID, jobID, string(child.Status), "", "", data)
						if e.OnStepDispatched != nil {
							e.OnStepDispatched(run.ID, childStepID, jobID)
						}
					}
					parentSR.Children[childStepID] = child
					run.Steps[childStepID] = child
				}
				parentSR.Status = StepStatusRunning
				run.Steps[stepID] = parentSR
				continue
			}

			// For-each fan-out.
			if step.ForEach != "" {
				// Evaluate the expression ONCE and store the resolved items to
				// prevent index-shift bugs when scheduleReady is re-entered
				// (e.g. after retries or max_parallel throttling).
				items := parentSR.ResolvedItems
				if items == nil {
					var err error
					items, err = evalForEach(step.ForEach, buildEvalScope(run, nil))
					if err != nil {
						slog.Error("for_each eval failed", "step_id", stepID, "error", err)
						parentSR.Status = StepStatusFailed
						if parentSR.StartedAt == nil {
							parentSR.StartedAt = &now
						}
						parentSR.CompletedAt = &now
						parentSR.Error = map[string]any{"message": err.Error()}
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_foreach_failed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}
					if e.maxForEachItems > 0 && len(items) > e.maxForEachItems {
						msg := fmt.Sprintf("for_each fan-out exceeds limit (%d > %d)", len(items), e.maxForEachItems)
						parentSR.Status = StepStatusFailed
						if parentSR.StartedAt == nil {
							parentSR.StartedAt = &now
						}
						parentSR.CompletedAt = &now
						parentSR.Error = map[string]any{"message": msg}
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_foreach_failed", stepID, "", string(parentSR.Status), "", msg, map[string]any{
							"count": len(items),
							"limit": e.maxForEachItems,
						})
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}
					parentSR.ResolvedItems = items
				}
				if parentSR.Children == nil {
					parentSR.Children = make(map[string]*StepRun)
				}
				if len(items) == 0 {
					parentSR.Status = StepStatusSucceeded
					parentSR.StartedAt = &now
					parentSR.CompletedAt = &now
					parentSR.Output = []any{}
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_completed", stepID, "", string(parentSR.Status), "", "", map[string]any{"items": 0})
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}
				// Pre-create children so the parent doesn't incorrectly aggregate to SUCCEEDED
				// before all items have been processed (e.g. when max_parallel throttles dispatch).
				for idx := range items {
					childID := fmt.Sprintf("%s[%d]", stepID, idx)
					if parentSR.Children[childID] != nil {
						continue
					}
					child := &StepRun{StepID: childID, Status: StepStatusPending}
					parentSR.Children[childID] = child
					run.Steps[childID] = child
				}
				parentSR.Status = StepStatusRunning
				if parentSR.StartedAt == nil {
					parentSR.StartedAt = &now
				}
				runningChildren := 0
				for _, child := range parentSR.Children {
					if child != nil && child.Status == StepStatusRunning {
						runningChildren++
					}
				}
				for idx, item := range items {
					if step.MaxParallel > 0 && runningChildren >= step.MaxParallel {
						break
					}
					childID := fmt.Sprintf("%s[%d]", stepID, idx)
					child := parentSR.Children[childID]
					if child == nil {
						child = &StepRun{StepID: childID, Status: StepStatusPending}
					}
					if child.Status != "" && child.Status != StepStatusPending {
						continue
					}
					if child.NextAttemptAt != nil && child.NextAttemptAt.After(now) {
						continue
					}
					jobID := fmt.Sprintf("%s:%s@%d", run.ID, childID, child.Attempts+1)
					req := e.buildJobRequest(ctx, wfDef, run, step, childID, jobID)
					// Attach for-each metadata
					if req.Env == nil {
						req.Env = map[string]string{}
					}
					req.Env["foreach_index"] = fmt.Sprintf("%d", idx)
					if data, err := json.Marshal(item); err == nil {
						req.Env["foreach_item"] = string(data)
					}
					payload, err := e.buildJobPayload(run, step, item)
					if err != nil {
						child.Status = StepStatusFailed
						child.Error = map[string]any{"message": err.Error()}
						parentSR.Children[childID] = child
						run.Steps[childID] = child
						continue
					}
					if ptr, err := e.putJobContext(ctx, jobID, payload); err != nil {
						child.Status = StepStatusFailed
						child.Error = map[string]any{"message": err.Error()}
						child.Input = payload
						parentSR.Children[childID] = child
						run.Steps[childID] = child
						continue
					} else if ptr != "" {
						req.ContextPtr = ptr
					}
					packet := makeJobPacket(run.ID, req)
					if err := e.bus.Publish(capsdk.SubjectSubmit, packet); err != nil {
						slog.Error("publish foreach step", "run_id", run.ID, "step_id", childID, "error", err)
						child.Status = StepStatusFailed
						child.Error = map[string]any{"message": err.Error()}
					} else {
						child.Status = StepStatusRunning
						child.StartedAt = &now
						child.Attempts++
						child.JobID = jobID
						child.Input = payload
						child.Item = item
						runningChildren++
						data := map[string]any{"foreach_index": idx}
						if req.ContextPtr != "" {
							data["context_ptr"] = req.ContextPtr
						}
						e.appendTimeline(ctx, run, "step_dispatched", childID, jobID, string(child.Status), "", "", data)
						if e.OnStepDispatched != nil {
							e.OnStepDispatched(run.ID, childID, jobID)
						}
					}
					parentSR.Children[childID] = child
					run.Steps[childID] = child
				}
				run.Steps[stepID] = parentSR
				continue
			}

			// Storage steps read/write/delete run context or artifact store inline (no job dispatch).
			if step.Type == StepTypeStorage {
				if parentSR.Status == "" || parentSR.Status == StepStatusPending {
					parentSR.Status = StepStatusRunning
					parentSR.StartedAt = &now

					scope := buildEvalScope(run, nil)
					evalInput, evalErr := evalTemplates(step.Input, scope)
					if evalErr != nil {
						parentSR.Status = StepStatusFailed
						parentSR.Error = map[string]any{"message": fmt.Sprintf("storage input expression error: %s", evalErr.Error())}
						parentSR.CompletedAt = &now
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_storage_failed", stepID, "", string(parentSR.Status), "", evalErr.Error(), nil)
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}
					inputMap, _ := evalInput.(map[string]any)
					if inputMap == nil {
						inputMap = map[string]any{}
					}

					operation, _ := inputMap["operation"].(string)
					key, _ := inputMap["key"].(string)

					if run.Context == nil {
						run.Context = map[string]any{}
					}

					var output map[string]any
					switch operation {
					case "read":
						if key == "" {
							parentSR.Status = StepStatusFailed
							parentSR.Error = map[string]any{"message": "storage read: key is required"}
							parentSR.CompletedAt = &now
							run.Steps[stepID] = parentSR
							e.appendTimeline(ctx, run, "step_storage_failed", stepID, "", string(parentSR.Status), "", "key is required", nil)
							if e.OnStepFinished != nil {
								e.OnStepFinished(run.ID, stepID, parentSR.Status)
							}
							continue
						}
						val, found := getContextPath(run.Context, key)
						if !found {
							parentSR.Status = StepStatusFailed
							msg := fmt.Sprintf("storage read: key %q not found", key)
							parentSR.Error = map[string]any{"message": msg}
							parentSR.CompletedAt = &now
							run.Steps[stepID] = parentSR
							e.appendTimeline(ctx, run, "step_storage_failed", stepID, "", string(parentSR.Status), "", msg, nil)
							if e.OnStepFinished != nil {
								e.OnStepFinished(run.ID, stepID, parentSR.Status)
							}
							continue
						}
						output = map[string]any{"operation": "read", "key": key, "value": val}

					case "write":
						if key == "" {
							parentSR.Status = StepStatusFailed
							parentSR.Error = map[string]any{"message": "storage write: key is required"}
							parentSR.CompletedAt = &now
							run.Steps[stepID] = parentSR
							e.appendTimeline(ctx, run, "step_storage_failed", stepID, "", string(parentSR.Status), "", "key is required", nil)
							if e.OnStepFinished != nil {
								e.OnStepFinished(run.ID, stepID, parentSR.Status)
							}
							continue
						}
						val := inputMap["value"]
						if pathErr := setContextPath(run.Context, key, val); pathErr != nil {
							slog.Warn("workflow: setContextPath failed for storage write",
								"run_id", run.ID, "key", key, "error", pathErr)
						}
						output = map[string]any{"operation": "write", "key": key, "value": val}

					case "delete":
						if key == "" {
							parentSR.Status = StepStatusFailed
							parentSR.Error = map[string]any{"message": "storage delete: key is required"}
							parentSR.CompletedAt = &now
							run.Steps[stepID] = parentSR
							e.appendTimeline(ctx, run, "step_storage_failed", stepID, "", string(parentSR.Status), "", "key is required", nil)
							if e.OnStepFinished != nil {
								e.OnStepFinished(run.ID, stepID, parentSR.Status)
							}
							continue
						}
						deleteContextPath(run.Context, key)
						output = map[string]any{"operation": "delete", "key": key}

					default:
						parentSR.Status = StepStatusFailed
						msg := fmt.Sprintf("unknown storage operation: %q", operation)
						parentSR.Error = map[string]any{"message": msg}
						parentSR.CompletedAt = &now
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_storage_failed", stepID, "", string(parentSR.Status), "", msg, nil)
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}

					if err := e.validateInlineOutput(step, output); err != nil {
						parentSR.Status = StepStatusFailed
						parentSR.Error = map[string]any{"message": err.Error()}
						parentSR.CompletedAt = &now
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_storage_failed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}

					parentSR.Status = StepStatusSucceeded
					parentSR.CompletedAt = &now
					parentSR.Output = output
					run.Steps[stepID] = parentSR
					recordStepInlineOutput(run, stepID, step, output)
					e.appendTimeline(ctx, run, "step_storage_completed", stepID, "", string(parentSR.Status), "", "", output)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
				}
				run.Steps[stepID] = parentSR
				continue
			}

			// Warn when a step type has no dedicated handler and falls through to generic dispatch.
			switch step.Type {
			case StepTypeWorker, StepTypeCondition, StepTypeApproval, StepTypeDelay, StepTypeNotify, StepTypeTransform, StepTypeStorage, StepTypeSwitch, StepTypeParallel, StepTypeLoop, StepTypeSubWorkflow:
				// Handled above.
			default:
				slog.Warn("step has no dedicated handler, dispatching as generic job", "run_id", run.ID, "step_id", stepID, "step_type", string(step.Type))
			}

			// Respect backoff windows for retrying steps.
			if parentSR.NextAttemptAt != nil && parentSR.NextAttemptAt.After(now) {
				run.Steps[stepID] = parentSR
				continue
			}

			jobID := fmt.Sprintf("%s:%s@%d", run.ID, stepID, parentSR.Attempts+1)
			req := e.buildJobRequest(ctx, wfDef, run, step, stepID, jobID)
			// Set idempotency key for crash safety if not already set by workflow author.
			if req.Meta == nil {
				req.Meta = &pb.JobMetadata{}
			}
			if strings.TrimSpace(req.Meta.IdempotencyKey) == "" {
				req.Meta.IdempotencyKey = fmt.Sprintf("wf:%s:%s:%d", run.ID, stepID, parentSR.Attempts+1)
			}
			payload, err := e.buildJobPayload(run, step, nil)
			if err != nil {
				parentSR.Status = StepStatusFailed
				parentSR.Error = map[string]any{"message": err.Error()}
				run.Steps[stepID] = parentSR
				slog.Error("step input build failed", "run_id", run.ID, "step_id", stepID, "error", err)
				e.appendTimeline(ctx, run, "step_failed", stepID, jobID, string(parentSR.Status), "", err.Error(), nil)
				if e.OnStepFinished != nil {
					e.OnStepFinished(run.ID, stepID, parentSR.Status)
				}
				continue
			}
			if ptr, err := e.putJobContext(ctx, jobID, payload); err != nil {
				parentSR.Status = StepStatusFailed
				parentSR.Error = map[string]any{"message": err.Error()}
				parentSR.Input = payload
				run.Steps[stepID] = parentSR
				slog.Error("step context store failed", "run_id", run.ID, "step_id", stepID, "job_id", jobID, "error", err)
				e.appendTimeline(ctx, run, "step_failed", stepID, jobID, string(parentSR.Status), "", err.Error(), nil)
				if e.OnStepFinished != nil {
					e.OnStepFinished(run.ID, stepID, parentSR.Status)
				}
				continue
			} else if ptr != "" {
				req.ContextPtr = ptr
			}

			// Persist state BEFORE dispatch (crash safety: if engine crashes after dispatch
			// but before persist, on recovery the step is already RUNNING with a JobID, and
			// the idempotency key prevents the scheduler from processing a duplicate).
			parentSR.Status = StepStatusRunning
			parentSR.StartedAt = &now
			parentSR.Attempts++
			parentSR.JobID = jobID
			// Merge pre-existing Input keys (e.g. error context from on_error
			// handlers) into payload so they are preserved after overwrite and
			// also forwarded to the dispatched job.
			if parentSR.Input != nil {
				for k, v := range parentSR.Input {
					if _, exists := payload[k]; !exists {
						payload[k] = v
					}
				}
			}
			parentSR.Input = payload
			run.Steps[stepID] = parentSR
			if err := e.store.UpdateRun(ctx, run); err != nil {
				slog.Error("pre-dispatch persist failed", "run_id", run.ID, "step_id", stepID, "error", err)
				parentSR.Status = StepStatusPending
				parentSR.Attempts--
				parentSR.JobID = ""
				parentSR.StartedAt = nil
				run.Steps[stepID] = parentSR
				continue
			}

			// Dispatch to NATS — state is already persisted so a crash here is safe.
			slog.Debug("step dispatching", "component", "workflow", "runId", run.ID, "traceId", run.ID, "stepId", stepID, "jobId", jobID, "stepType", string(step.Type))
			packet := makeJobPacket(run.ID, req)
			if err := e.bus.Publish(capsdk.SubjectSubmit, packet); err != nil {
				slog.Error("publish step", "run_id", run.ID, "step_id", stepID, "error", err)
				// Revert to pending for retry on next scheduleReady; idempotency key
				// prevents duplicate execution if the message was actually delivered.
				parentSR.Status = StepStatusPending
				parentSR.Attempts--
				parentSR.JobID = ""
				parentSR.StartedAt = nil
				run.Steps[stepID] = parentSR
				if updateErr := e.store.UpdateRun(ctx, run); updateErr != nil {
					slog.Error("workflow: failed to persist dispatch revert",
						"run_id", run.ID, "step_id", stepID, "error", updateErr)
				}
			} else {
				var data map[string]any
				if req.ContextPtr != "" {
					data = map[string]any{"context_ptr": req.ContextPtr}
				}
				e.appendTimeline(ctx, run, "step_dispatched", stepID, jobID, string(parentSR.Status), "", "", data)
				if e.OnStepDispatched != nil {
					e.OnStepDispatched(run.ID, stepID, jobID)
				}
			}
		}
		// Activate on_error handlers for any steps that failed inline during
		// this pass (condition, switch, transform, storage, subworkflow, etc.).
		// The helper is idempotent — safe to call on already-activated handlers.
		for sid, sr := range run.Steps {
			if sr != nil && (sr.Status == StepStatusFailed || sr.Status == StepStatusTimedOut) {
				e.activateOnErrorHandler(ctx, run, wfDef, sid, sr, now)
			}
		}
		if countTerminalSteps(run) <= terminalBefore {
			break
		}
	}

	updateRunStatus(run, wfDef, now)
	if prevStatus != run.Status {
		e.appendTimeline(ctx, run, "run_status", "", "", string(run.Status), "", "", nil)
	}
	run.UpdatedAt = now
	if err := e.store.UpdateRun(ctx, run); err != nil {
		return fmt.Errorf("workflow schedule ready update run %s: %w", run.ID, err)
	}
	if isTerminalRunStatus(run.Status) {
		e.markRunTerminal(run.ID)
	}
	return nil
}

// countTerminalSteps returns the number of steps in a run that have reached a
// terminal status (succeeded, failed, cancelled, timed_out). Used by the
// multi-pass loop in scheduleReady to detect inline completions.
func countTerminalSteps(run *WorkflowRun) int {
	count := 0
	for _, sr := range run.Steps {
		if sr != nil && isTerminalStepStatus(sr.Status) {
			count++
		}
	}
	return count
}

type switchCase struct {
	MatchValue any
	StepID     string
}
