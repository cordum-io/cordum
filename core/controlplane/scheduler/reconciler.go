package scheduler

import (
	"context"
	"sync"
	"time"

	"github.com/cordum/cordum/core/infra/logging"
)

// Reconciler periodically inspects job state to enforce timeouts and cleanup.
type Reconciler struct {
	store           JobStore
	dispatchTimeout time.Duration
	runningTimeout  time.Duration
	pollInterval    time.Duration
	lockKey         string
	lockTTL         time.Duration
	mu              sync.RWMutex
}

func NewReconciler(store JobStore, dispatchTimeout, runningTimeout, pollInterval time.Duration) *Reconciler {
	return &Reconciler{
		store:           store,
		dispatchTimeout: dispatchTimeout,
		runningTimeout:  runningTimeout,
		pollInterval:    pollInterval,
		lockKey:         "cordum:reconciler:default",
		lockTTL:         pollInterval * 2,
	}
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
				ok, err := r.store.TryAcquireLock(ctx, r.lockKey, r.lockTTL)
				if err != nil {
					logging.Error("reconciler", "lock acquisition failed", "error", err)
					continue
				}
				if !ok {
					// Another reconciler is active.
					continue
				}
				r.tick(ctx)
				_ = r.store.ReleaseLock(ctx, r.lockKey)
			} else {
				r.tick(ctx)
			}
		}
	}
}

func (r *Reconciler) tick(ctx context.Context) {
	now := time.Now()
	dispatchTimeout, runningTimeout := r.currentTimeouts()
	r.handleTimeouts(ctx, JobStateDispatched, now.Add(-dispatchTimeout))
	r.handleTimeouts(ctx, JobStateRunning, now.Add(-runningTimeout))
	r.handleDeadlineExpirations(ctx, now)
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

func (r *Reconciler) currentTimeouts() (time.Duration, time.Duration) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.dispatchTimeout, r.runningTimeout
}

func (r *Reconciler) handleTimeouts(ctx context.Context, state JobState, cutoff time.Time) {
	const maxIterations = 100
	const maxRetriesPerJob = 3

	failed := make(map[string]int)

	for i := 0; i < maxIterations; i++ {
		cutoffMicros := cutoff.UnixNano() / int64(time.Microsecond)
		records, err := r.store.ListJobsByState(ctx, state, cutoffMicros, 200)
		if err != nil {
			logging.Error("reconciler", "list jobs", "state", state, "error", err)
			return
		}
		if len(records) == 0 {
			return
		}

		progress := 0
		for _, rec := range records {
			if failed[rec.ID] >= maxRetriesPerJob {
				continue
			}
			if err := r.store.SetState(ctx, rec.ID, JobStateTimeout); err != nil {
				failed[rec.ID]++
				logging.Error("reconciler", "mark timeout", "job_id", rec.ID, "error", err, "retry", failed[rec.ID])
				continue
			}
			logging.Info("reconciler", "job timed out", "job_id", rec.ID, "from_state", state)
			progress++
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
	logging.Error("reconciler", "max iterations reached while processing timeouts", "state", state)
}

func (r *Reconciler) handleDeadlineExpirations(ctx context.Context, now time.Time) {
	records, err := r.store.ListExpiredDeadlines(ctx, now.Unix(), 200)
	if err != nil {
		logging.Error("reconciler", "list expired deadlines", "error", err)
		return
	}
	for _, rec := range records {
		if err := r.store.SetState(ctx, rec.ID, JobStateTimeout); err != nil {
			logging.Error("reconciler", "mark deadline timeout", "job_id", rec.ID, "error", err)
		} else {
			logging.Info("reconciler", "job deadline expired", "job_id", rec.ID)
		}
	}
}
