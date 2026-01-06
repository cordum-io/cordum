package scheduler

import (
	"context"
	"time"

	"github.com/yaront1111/coretex-os/core/infra/logging"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
)

// PendingReplayer retries pending jobs that may have been missed or stalled.
type PendingReplayer struct {
	engine       *Engine
	store        JobStore
	pendingAge   time.Duration
	pollInterval time.Duration
	lockKey      string
	lockTTL      time.Duration
}

func NewPendingReplayer(engine *Engine, store JobStore, pendingAge, pollInterval time.Duration) *PendingReplayer {
	if pollInterval <= 0 {
		pollInterval = 30 * time.Second
	}
	if pendingAge <= 0 {
		pendingAge = 5 * time.Minute
	}
	return &PendingReplayer{
		engine:       engine,
		store:        store,
		pendingAge:   pendingAge,
		pollInterval: pollInterval,
		lockKey:      "coretex:replayer:pending",
		lockTTL:      pollInterval * 2,
	}
}

func (r *PendingReplayer) Start(ctx context.Context) {
	if r == nil || r.store == nil || r.engine == nil {
		return
	}
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if r.lockKey != "" && r.lockTTL > 0 {
				ok, err := r.store.TryAcquireLock(ctx, r.lockKey, r.lockTTL)
				if err != nil {
					logging.Error("pending-replayer", "lock acquisition failed", "error", err)
					continue
				}
				if !ok {
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

func (r *PendingReplayer) tick(ctx context.Context) {
	if r.store == nil || r.engine == nil {
		return
	}
	cutoff := time.Now().Add(-r.pendingAge)
	r.replayPending(ctx, cutoff)
}

func (r *PendingReplayer) replayPending(ctx context.Context, cutoff time.Time) {
	store, ok := r.store.(interface {
		GetJobRequest(context.Context, string) (*pb.JobRequest, error)
	})
	if !ok {
		logging.Error("pending-replayer", "job store missing GetJobRequest")
		return
	}

	cutoffMicros := cutoff.UnixNano() / int64(time.Microsecond)
	records, err := r.store.ListJobsByState(ctx, JobStatePending, cutoffMicros, 200)
	if err != nil {
		logging.Error("pending-replayer", "list pending jobs failed", "error", err)
		return
	}
	if len(records) == 0 {
		return
	}

	logging.Info("pending-replayer", "replaying pending jobs", "count", len(records))
	for _, rec := range records {
		req, err := store.GetJobRequest(ctx, rec.ID)
		if err != nil || req == nil {
			logging.Error("pending-replayer", "load job request failed", "job_id", rec.ID, "error", err)
			continue
		}
		if err := r.engine.handleJobRequest(req, rec.TraceID); err != nil {
			logging.Error("pending-replayer", "replay job failed", "job_id", rec.ID, "error", err)
		}
	}
}
