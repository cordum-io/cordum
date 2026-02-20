package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cordum/cordum/core/infra/logging"
)

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
					logging.Error("reconciler", "lock acquisition failed", "error", err)
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
								logging.Warn("reconciler", "lock renewal failed", "error", err)
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

func (r *Reconciler) handleTimeouts(ctx context.Context, state JobState, cutoff time.Time, timeout ...time.Duration) {
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
		if r.metrics != nil {
			r.metrics.SetStaleJobs(string(state), len(records))
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
			reason := fmt.Sprintf("timeout: %s exceeded", state)
			if len(timeout) > 0 {
				reason = fmt.Sprintf("timeout: %s >%s", state, timeout[0])
			}
			_ = r.store.SetFailureReason(ctx, rec.ID, reason)
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
			_ = r.store.SetFailureReason(ctx, rec.ID, "timeout: deadline expired")
			logging.Info("reconciler", "job deadline expired", "job_id", rec.ID)
		}
	}
}
