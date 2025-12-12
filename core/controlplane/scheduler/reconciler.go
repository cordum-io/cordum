package scheduler

import (
	"context"
	"time"

	"github.com/yaront1111/coretex-os/core/infra/logging"
)

// Reconciler periodically inspects job state to enforce timeouts and cleanup.
type Reconciler struct {
	store           JobStore
	dispatchTimeout time.Duration
	runningTimeout  time.Duration
	pollInterval    time.Duration
}

func NewReconciler(store JobStore, dispatchTimeout, runningTimeout, pollInterval time.Duration) *Reconciler {
	return &Reconciler{
		store:           store,
		dispatchTimeout: dispatchTimeout,
		runningTimeout:  runningTimeout,
		pollInterval:    pollInterval,
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
			r.tick(ctx)
		}
	}
}

func (r *Reconciler) tick(ctx context.Context) {
	now := time.Now()
	r.handleTimeouts(ctx, JobStateDispatched, now.Add(-r.dispatchTimeout))
	r.handleTimeouts(ctx, JobStateRunning, now.Add(-r.runningTimeout))
	r.handleDeadlineExpirations(ctx, now)
}

func (r *Reconciler) handleTimeouts(ctx context.Context, state JobState, cutoff time.Time) {
	for {
		records, err := r.store.ListJobsByState(ctx, state, cutoff.Unix(), 200)
		if err != nil {
			logging.Error("reconciler", "list jobs", "state", state, "error", err)
			return
		}
		if len(records) == 0 {
			return
		}
		for _, rec := range records {
			if err := r.store.SetState(ctx, rec.ID, JobStateTimeout); err != nil {
				logging.Error("reconciler", "mark timeout", "job_id", rec.ID, "error", err)
			} else {
				logging.Info("reconciler", "job timed out", "job_id", rec.ID, "from_state", state)
			}
		}
		// continue looping in case there are more
	}
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
