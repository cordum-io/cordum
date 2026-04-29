package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	infraStore "github.com/cordum/cordum/core/infra/store"
)

// SnapshotProvider returns the current safety kernel policy snapshot hash.
// Used by the reconciler to detect and auto-invalidate stale approvals.
type SnapshotProvider interface {
	CurrentPolicySnapshot(ctx context.Context) string
}

// Reconciler periodically inspects job state to enforce timeouts and cleanup.
type Reconciler struct {
	store            JobStore
	dispatchTimeout  time.Duration
	runningTimeout   time.Duration
	scheduledTimeout time.Duration
	pollInterval     time.Duration
	lockKey          string
	lockTTL          time.Duration
	mu               sync.RWMutex
	metrics          Metrics
	approvalMetrics  approvalMetrics
	snapshotProvider SnapshotProvider
}

type approvalMetrics interface {
	SetApprovalQueueDepth(riskTier string, depth int)
	IncApprovalExpired()
	IncApprovalDecision(decision string)
}

type approvalDepthCounter interface {
	CountJobsByState(ctx context.Context, state JobState) (int64, error)
}

type approvalRepairStore interface {
	InspectApprovalRepair(ctx context.Context, jobID string) (*infraStore.ApprovalRepairSnapshot, error)
	ApplyApprovalRepair(ctx context.Context, params infraStore.ApprovalRepairApplyParams) (*infraStore.ApprovalRepairResult, error)
}

func NewReconciler(store JobStore, dispatchTimeout, runningTimeout, pollInterval time.Duration) *Reconciler {
	return &Reconciler{
		store:            store,
		dispatchTimeout:  dispatchTimeout,
		runningTimeout:   runningTimeout,
		scheduledTimeout: 60 * time.Second,
		pollInterval:     pollInterval,
		lockKey:          "cordum:reconciler:default",
		lockTTL:          pollInterval * 2,
	}
}

// WithMetrics attaches a Metrics implementation to the reconciler.
func (r *Reconciler) WithMetrics(m Metrics) *Reconciler {
	r.metrics = m
	return r
}

// WithApprovalMetrics attaches approval workflow metrics to the reconciler.
func (r *Reconciler) WithApprovalMetrics(m approvalMetrics) *Reconciler {
	r.approvalMetrics = m
	return r
}

// WithSnapshotProvider attaches a safety kernel snapshot provider so the
// reconciler can detect and auto-invalidate approvals whose policy snapshot
// has become stale.
func (r *Reconciler) WithSnapshotProvider(p SnapshotProvider) *Reconciler {
	r.snapshotProvider = p
	return r
}

// Start runs the reconciliation loop until the context is cancelled.
func (r *Reconciler) Start(ctx context.Context) {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if r.lockKey != "" && r.store != nil && r.lockTTL > 0 {
				token, err := r.store.TryAcquireLock(ctx, r.lockKey, r.lockTTL)
				if err != nil {
					slog.Error("lock acquisition failed", "error", err)
					continue
				}
				if token == "" {
					// Another reconciler is active.
					continue
				}

				// Renew lock during tick() to prevent expiry on slow ticks.
				renewCtx, renewCancel := context.WithCancel(ctx)
				renewDone := make(chan struct{})
				go func() {
					defer close(renewDone)
					t := time.NewTicker(r.lockTTL / 3)
					defer t.Stop()
					for {
						select {
						case <-renewCtx.Done():
							return
						case <-t.C:
							rCtx, rc := context.WithTimeout(ctx, 2*time.Second)
							if err := r.store.RenewLock(rCtx, r.lockKey, token, r.lockTTL); err != nil {
								slog.Warn("lock renewal failed", "error", err)
							}
							rc()
						}
					}
				}()

				r.tick(ctx)
				renewCancel()
				<-renewDone
			} else {
				r.tick(ctx)
			}
		}
	}
}

func (r *Reconciler) tick(ctx context.Context) {
	now := time.Now()
	dispatchTimeout, runningTimeout, scheduledTimeout := r.currentTimeouts()
	r.handleTimeouts(ctx, JobStateScheduled, now.Add(-scheduledTimeout), scheduledTimeout)
	r.handleTimeouts(ctx, JobStateDispatched, now.Add(-dispatchTimeout), dispatchTimeout)
	r.handleTimeouts(ctx, JobStateRunning, now.Add(-runningTimeout), runningTimeout)
	r.handleDeadlineExpirations(ctx, now)
	r.handleApprovalRepairs(ctx, now)
}

// UpdateTimeouts replaces dispatch/running timeouts at runtime.
func (r *Reconciler) UpdateTimeouts(dispatchTimeout, runningTimeout time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if dispatchTimeout > 0 {
		r.dispatchTimeout = dispatchTimeout
	}
	if runningTimeout > 0 {
		r.runningTimeout = runningTimeout
	}
}

func (r *Reconciler) currentTimeouts() (time.Duration, time.Duration, time.Duration) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.dispatchTimeout, r.runningTimeout, r.scheduledTimeout
}

func timeoutFailureReason(state JobState, timeout ...time.Duration) string {
	if len(timeout) > 0 {
		return fmt.Sprintf("timeout: %s >%s", state, timeout[0])
	}
	return fmt.Sprintf("timeout: %s exceeded", state)
}

func (r *Reconciler) handleTimeouts(ctx context.Context, state JobState, cutoff time.Time, timeout ...time.Duration) {
	const maxIterations = 100
	const maxRetriesPerJob = 3

	cutoffMicros := cutoff.UnixNano() / int64(time.Microsecond)
	reason := timeoutFailureReason(state, timeout...)
	var failed map[string]int

	for i := 0; i < maxIterations; i++ {
		records, err := r.store.ListJobsByState(ctx, state, cutoffMicros, 200)
		if err != nil {
			slog.Error("list jobs", "state", state, "error", err)
			return
		}
		if r.metrics != nil {
			r.metrics.SetStaleJobs(string(state), len(records))
		}
		if len(records) == 0 {
			return
		}

		progress := 0
		firstJobID := ""
		lastJobID := ""
		for _, rec := range records {
			if failed != nil && failed[rec.ID] >= maxRetriesPerJob {
				continue
			}
			if err := r.store.SetState(ctx, rec.ID, JobStateTimeout); err != nil {
				if failed == nil {
					failed = make(map[string]int)
				}
				failed[rec.ID]++
				slog.Error("mark timeout", "job_id", rec.ID, "error", err, "retry", failed[rec.ID])
				continue
			}
			if frErr := r.store.SetFailureReason(ctx, rec.ID, reason); frErr != nil {
				slog.Warn("failed to set failure reason", "job_id", rec.ID, "reason", reason, "error", frErr)
			}
			if progress == 0 {
				firstJobID = rec.ID
			}
			lastJobID = rec.ID
			if state == JobStateApproval {
				r.recordApprovalExpiry(ctx)
			}
			progress++
		}

		if progress > 0 {
			slog.Warn(
				"jobs timed out",
				"from_state", state,
				"count", progress,
				"reason", reason,
				"first_job_id", firstJobID,
				"last_job_id", lastJobID,
			)
		}

		if progress == 0 {
			// If we made no progress, wait a bit before retrying to avoid tight loops.
			select {
			case <-ctx.Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
	slog.Error("max iterations reached while processing timeouts", "state", state)
}

func (r *Reconciler) handleDeadlineExpirations(ctx context.Context, now time.Time) {
	records, err := r.store.ListExpiredDeadlines(ctx, now.Unix(), 200)
	if err != nil {
		slog.Error("list expired deadlines", "error", err)
		return
	}
	for _, rec := range records {
		if err := r.store.SetState(ctx, rec.ID, JobStateTimeout); err != nil {
			slog.Error("mark deadline timeout", "job_id", rec.ID, "error", err)
		} else {
			if frErr := r.store.SetFailureReason(ctx, rec.ID, "timeout: deadline expired"); frErr != nil {
				slog.Warn("failed to set failure reason", "job_id", rec.ID, "reason", "timeout: deadline expired", "error", frErr)
			}
			slog.Warn("job deadline expired", "job_id", rec.ID)
			if rec.State == JobStateApproval {
				r.recordApprovalExpiry(ctx)
			}
		}
	}
}

func (r *Reconciler) recordApprovalExpiry(ctx context.Context) {
	if r == nil || r.approvalMetrics == nil {
		return
	}
	r.approvalMetrics.IncApprovalExpired()
	r.approvalMetrics.IncApprovalDecision("expired")
	r.syncApprovalQueueDepth(ctx)
}

func (r *Reconciler) syncApprovalQueueDepth(ctx context.Context) {
	if r == nil || r.approvalMetrics == nil || r.store == nil {
		return
	}
	counter, ok := r.store.(approvalDepthCounter)
	if !ok {
		return
	}
	count, err := counter.CountJobsByState(ctx, JobStateApproval)
	if err != nil {
		slog.Warn("approval queue depth sync failed", "error", err)
		return
	}
	r.approvalMetrics.SetApprovalQueueDepth("all", int(count))
}

func (r *Reconciler) handleApprovalRepairs(ctx context.Context, now time.Time) {
	repairStore, ok := r.store.(approvalRepairStore)
	if !ok {
		return
	}

	// Pre-fetch the current policy snapshot once per repair sweep so we can
	// detect approvals that have become stale due to policy reloads.
	var currentSnapshot string
	if r.snapshotProvider != nil {
		currentSnapshot = r.snapshotProvider.CurrentPolicySnapshot(ctx)
	}

	const maxIterations = 100
	cutoffMicros := now.Add(time.Second).UnixNano() / int64(time.Microsecond)
	for i := 0; i < maxIterations; i++ {
		records, err := r.store.ListJobsByState(ctx, JobStateApproval, cutoffMicros, 200)
		if err != nil {
			slog.Error("list approval repairs", "error", err)
			return
		}
		if len(records) == 0 {
			return
		}

		progress := 0
		for _, rec := range records {
			snapshot, err := repairStore.InspectApprovalRepair(ctx, rec.ID)
			if err != nil {
				slog.Warn("inspect approval repair failed", "job_id", rec.ID, "error", err)
				continue
			}
			classifyOpts := infraStore.ApprovalRepairClassifyOptions{}
			if currentSnapshot != "" {
				storedSnap := snapshot.SafetyRecord.PolicySnapshot
				if storedSnap != "" && snapshotBase(currentSnapshot) != snapshotBase(storedSnap) {
					classifyOpts.StaleSnapshot = true
				}
			}
			plan := infraStore.ClassifyApprovalRepair(*snapshot, classifyOpts)
			if !plan.Repairable || !autoApplyApprovalRepair(plan) {
				continue
			}
			repaired, err := repairStore.ApplyApprovalRepair(ctx, infraStore.ApprovalRepairApplyParams{
				JobID: rec.ID,
				Plan:  plan,
				Actor: "system/reconciler",
				Note:  "auto-reconciled inconsistent approval state",
			})
			if err != nil {
				slog.Warn("approval auto-repair failed", "job_id", rec.ID, "kind", plan.Kind, "error", err)
				continue
			}
			slog.Info("approval auto-repaired",
				"job_id", rec.ID,
				"kind", plan.Kind,
				"target_state", repaired.State,
				"publish_target", repaired.ApprovalRecord.PublishTarget,
				"publish_status", repaired.ApprovalRecord.PublishStatus)
			progress++
		}

		if progress > 0 {
			r.syncApprovalQueueDepth(ctx)
		}
		if progress == 0 {
			return
		}
	}
	slog.Error("max iterations reached while processing approval repairs")
}

func autoApplyApprovalRepair(plan infraStore.ApprovalRepairPlan) bool {
	switch plan.Kind {
	case infraStore.ApprovalRepairApplyApprovedResolution,
		infraStore.ApprovalRepairApplyRejectedResolution,
		infraStore.ApprovalRepairInvalidateStaleRequest,
		infraStore.ApprovalRepairInvalidateStaleSnapshot:
		return true
	default:
		return false
	}
}

// snapshotBase returns the base policy hash from a combined snapshot string.
// Combined snapshots have the form "base|cfg:hash"; this extracts just "base"
// so that config-overlay changes don't affect approval-binding comparisons.
// Shared by the reconciler's stale-snapshot detection and the engine's
// approval fast-path revision check.
func snapshotBase(snap string) string {
	if i := strings.Index(snap, "|"); i >= 0 {
		return snap[:i]
	}
	return snap
}
