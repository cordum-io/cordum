package scheduler

import (
	"context"
	"time"

	"github.com/cordum/cordum/core/infra/logging"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// PendingReplayer retries pending jobs that may have been missed or stalled.
type PendingReplayer struct {
	engine       *Engine
	store        JobStore
	metrics      Metrics
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
		pendingAge = 2 * time.Minute
	}
	return &PendingReplayer{
		engine:       engine,
		store:        store,
		pendingAge:   pendingAge,
		pollInterval: pollInterval,
		lockKey:      "cordum:replayer:pending",
		lockTTL:      pollInterval * 2,
	}
}

// WithMetrics attaches a metrics collector to the replayer.
func (r *PendingReplayer) WithMetrics(m Metrics) *PendingReplayer {
	r.metrics = m
	return r
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
				token, err := r.store.TryAcquireLock(ctx, r.lockKey, r.lockTTL)
				if err != nil {
					logging.Error("pending-replayer", "lock acquisition failed", "error", err)
					continue
				}
				if token == "" {
					continue
				}
				r.tick(ctx)
				// Lock held until TTL expiry (pollInterval * 2) — intentional for
				// horizontal scaling. Only one replica may run tick() per TTL window.
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
	r.replayApproved(ctx, cutoff)
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
	replayed := 0
	for _, rec := range records {
		req, err := store.GetJobRequest(ctx, rec.ID)
		if err != nil || req == nil {
			logging.Error("pending-replayer", "load job request failed", "job_id", rec.ID, "error", err)
			continue
		}
		if err := r.engine.handleJobRequest(req, rec.TraceID); err != nil {
			logging.Error("pending-replayer", "replay job failed", "job_id", rec.ID, "error", err)
		} else {
			replayed++
			if r.metrics != nil {
				r.metrics.IncOrphanReplayed(req.Topic)
			}
		}
	}
	if replayed > 0 {
		logging.Info("pending-replayer", "replayed orphaned pending jobs", "count", replayed, "total", len(records))
	}
}

// replayApproved replays jobs stuck in APPROVAL_REQUIRED state that have the
// approval_granted label set. This handles the case where a job was approved
// before a worker was available to process it.
func (r *PendingReplayer) replayApproved(ctx context.Context, cutoff time.Time) {
	store, ok := r.store.(interface {
		GetJobRequest(context.Context, string) (*pb.JobRequest, error)
	})
	if !ok {
		return
	}

	cutoffMicros := cutoff.UnixNano() / int64(time.Microsecond)
	records, err := r.store.ListJobsByState(ctx, JobStateApproval, cutoffMicros, 200)
	if err != nil {
		logging.Error("pending-replayer", "list approval jobs failed", "error", err)
		return
	}
	if len(records) == 0 {
		return
	}

	replayed := 0
	for _, rec := range records {
		req, err := store.GetJobRequest(ctx, rec.ID)
		if err != nil || req == nil {
			continue
		}
		// Only replay jobs that have been approved
		if req.Labels == nil || req.Labels["approval_granted"] != "true" {
			continue
		}
		if err := r.engine.handleJobRequest(req, rec.TraceID); err != nil {
			logging.Error("pending-replayer", "replay approved job failed", "job_id", rec.ID, "error", err)
		} else {
			replayed++
		}
	}
	if replayed > 0 {
		logging.Info("pending-replayer", "replayed approved jobs", "count", replayed)
	}
}
