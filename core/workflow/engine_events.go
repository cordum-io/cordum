package workflow

import (
	"context"
	"fmt"
	"strings"
	"time"

	"log/slog"
	"runtime/debug"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// durableDelayThreshold is the minimum delay duration for Redis-backed durable timers.
// Delays shorter than this use in-memory time.AfterFunc only (fast, no Redis round-trip).
// Set to 3s to limit the data-loss window on crash while avoiding Redis round-trips for trivial delays.
const durableDelayThreshold = 3 * time.Second

// buildEventAlertTestHook is nil in production. Tests may set it to inject a
// panic and verify the alert builder fails closed.
var buildEventAlertTestHook func()

func delayForStep(step *Step, now time.Time) (time.Duration, error) {
	if step == nil {
		return 0, nil
	}
	if step.DelaySec < 0 {
		return 0, fmt.Errorf("delay_sec must be non-negative")
	}
	if step.DelaySec > 0 {
		return time.Duration(step.DelaySec) * time.Second, nil
	}
	if strings.TrimSpace(step.DelayUntil) != "" {
		ts := strings.TrimSpace(step.DelayUntil)
		target, err := time.Parse(time.RFC3339, ts)
		if err != nil {
			return 0, fmt.Errorf("invalid delay_until: %w", err)
		}
		if target.After(now) {
			return target.Sub(now), nil
		}
		return 0, nil
	}
	if step.TimeoutSec > 0 {
		return time.Duration(step.TimeoutSec) * time.Second, nil
	}
	return 0, nil
}

func buildEventAlert(step *Step, payload any) (alert *pb.SystemAlert) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("workflow: buildEventAlert panic",
				"panic", r,
				"stack", string(debug.Stack()),
			)
			alert = &pb.SystemAlert{
				Message:         fmt.Sprintf("event alert build panic: %v", r),
				Severity:        pb.AlertSeverity_ALERT_SEVERITY_ERROR,
				SourceComponent: "workflow-engine",
			}
		}
	}()
	if buildEventAlertTestHook != nil {
		buildEventAlertTestHook()
	}

	level := "INFO"
	message := ""
	component := "workflow-engine"
	traceID := ""
	details := map[string]string{}

	switch v := payload.(type) {
	case map[string]any:
		if val, ok := v["level"].(string); ok && strings.TrimSpace(val) != "" {
			level = strings.ToUpper(strings.TrimSpace(val))
		}
		if val, ok := v["message"].(string); ok && strings.TrimSpace(val) != "" {
			message = strings.TrimSpace(val)
		}
		if val, ok := v["component"].(string); ok && strings.TrimSpace(val) != "" {
			component = strings.TrimSpace(val)
		}
		if val, ok := v["trace_id"].(string); ok && strings.TrimSpace(val) != "" {
			traceID = strings.TrimSpace(val)
		}
		if val, ok := v["details"].(map[string]any); ok {
			for k, raw := range val {
				if s, ok := raw.(string); ok {
					details[k] = s
				} else {
					details[k] = fmt.Sprintf("%v", raw)
				}
			}
		}
		if val, ok := v["details"].(map[string]string); ok {
			for k, s := range val {
				details[k] = s
			}
		}
	case map[string]string:
		if val := strings.TrimSpace(v["level"]); val != "" {
			level = strings.ToUpper(val)
		}
		if val := strings.TrimSpace(v["message"]); val != "" {
			message = val
		}
		if val := strings.TrimSpace(v["component"]); val != "" {
			component = val
		}
		if val := strings.TrimSpace(v["trace_id"]); val != "" {
			traceID = val
		}
	}

	if message == "" && step != nil {
		if step.Name != "" {
			message = step.Name
		} else {
			message = step.ID
		}
	}

	return &pb.SystemAlert{
		Message:         message,
		Severity:        levelToSeverity(level),
		SourceComponent: component,
		Details:         details,
		TraceId:         traceID,
	}
}

func levelToSeverity(level string) pb.AlertSeverity {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "INFO":
		return pb.AlertSeverity_ALERT_SEVERITY_INFO
	case "WARN", "WARNING":
		return pb.AlertSeverity_ALERT_SEVERITY_WARNING
	case "ERROR":
		return pb.AlertSeverity_ALERT_SEVERITY_ERROR
	case "CRITICAL":
		return pb.AlertSeverity_ALERT_SEVERITY_CRITICAL
	default:
		return pb.AlertSeverity_ALERT_SEVERITY_UNSPECIFIED
	}
}

// Stop cancels all pending delay timers and prevents new ones from firing.
// It is safe to call multiple times.
func (e *Engine) Stop() {
	e.timerMu.Lock()
	defer e.timerMu.Unlock()
	if e.stopped != nil {
		select {
		case <-e.stopped:
			return // already stopped
		default:
		}
		close(e.stopped)
	} else {
		e.stopped = make(chan struct{})
		close(e.stopped)
	}
	for t := range e.pendingTimers {
		t.Stop()
	}
	e.pendingTimers = nil
}

// PendingTimers returns the number of active delay timers (for testing).
func (e *Engine) PendingTimers() int {
	e.timerMu.Lock()
	defer e.timerMu.Unlock()
	return len(e.pendingTimers)
}

func (e *Engine) scheduleAfter(delay time.Duration, workflowID, runID string) {
	if delay <= 0 {
		return
	}
	e.timerMu.Lock()
	if e.stopped != nil {
		select {
		case <-e.stopped:
			e.timerMu.Unlock()
			return // engine stopped, discard
		default:
		}
	}
	if e.stopped == nil {
		e.stopped = make(chan struct{})
	}
	stopped := e.stopped

	// Persist to Redis sorted set for crash recovery (delays > threshold only).
	if delay >= durableDelayThreshold && e.store != nil {
		fireAt := time.Now().Add(delay)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := e.store.AddDelayTimer(ctx, workflowID, runID, fireAt); err != nil {
			slog.Warn("failed to persist delay timer",
				"workflow_id", workflowID, "run_id", runID, "error", err)
		}
		cancel()
	}

	var t *time.Timer
	t = time.AfterFunc(delay, func() {
		// Atomically check stopped under timerMu to eliminate TOCTOU race.
		// Stop() also holds timerMu when closing the channel, so this is safe.
		e.timerMu.Lock()
		select {
		case <-stopped:
			e.timerMu.Unlock()
			return
		default:
		}
		// Remove ourselves from the pending map while holding the lock.
		delete(e.pendingTimers, t)
		e.timerMu.Unlock()

		// Resume the run first, then remove the durable timer only on success.
		// This prevents a window where the timer is removed but the run fails
		// to resume — leaving the run stuck until the reconciler catches it.
		// Use a bounded context so a slow Redis doesn't hang indefinitely.
		startCtx, startCancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := e.StartRun(startCtx, workflowID, runID); err != nil {
			startCancel()
			slog.Error("delay timer: StartRun failed, durable timer preserved for poller retry",
				"workflow_id", workflowID, "run_id", runID, "error", err)
			return
		}
		startCancel()
		if e.store != nil {
			rCtx, rCancel := context.WithTimeout(context.Background(), 2*time.Second)
			if err := e.store.RemoveDelayTimer(rCtx, workflowID, runID); err != nil {
				slog.Warn("failed to remove delay timer after successful resume",
					"workflow_id", workflowID, "run_id", runID, "error", err)
			}
			rCancel()
		}
	})
	if e.pendingTimers == nil {
		e.pendingTimers = make(map[*time.Timer]struct{})
	}
	e.pendingTimers[t] = struct{}{}
	e.timerMu.Unlock()
}

// recoverDelayTimers recovers durable delay timers from Redis on engine startup.
// Past-due timers are fired immediately via PopFiredDelays (atomic Lua).
// Future timers are re-scheduled via scheduleAfter (which re-adds to ZSET — idempotent).
func (e *Engine) recoverDelayTimers(ctx context.Context) {
	if e.store == nil {
		return
	}
	now := time.Now()

	// 1. Pop and fire all past-due timers atomically.
	fired, err := e.store.PopFiredDelays(ctx, now)
	if err != nil {
		slog.Warn("failed to pop fired delay timers", "error", err)
	}
	for _, member := range fired {
		wfID, rID := splitDelayMember(member)
		if wfID == "" || rID == "" {
			continue
		}
		slog.Info("recovering past-due delay timer",
			"workflow_id", wfID, "run_id", rID)
		if err := e.StartRun(ctx, wfID, rID); err != nil {
			slog.Error("recovery: StartRun failed for past-due timer, reconciler will retry",
				"workflow_id", wfID, "run_id", rID, "error", err)
		}
	}

	// 2. Re-schedule future timers with remaining delay.
	future, err := e.store.ListFutureDelays(ctx, now)
	if err != nil {
		slog.Warn("failed to list future delay timers", "error", err)
		return
	}
	for _, z := range future {
		member, ok := z.Member.(string)
		if !ok {
			continue
		}
		wfID, rID := splitDelayMember(member)
		if wfID == "" || rID == "" {
			continue
		}
		fireAt := time.Unix(int64(z.Score), 0)
		remaining := fireAt.Sub(now)
		if remaining <= 0 {
			remaining = time.Millisecond // fire immediately
		}
		slog.Info("re-scheduling future delay timer",
			"workflow_id", wfID, "run_id", rID, "remaining", remaining.String())
		// scheduleAfter will re-ZADD (idempotent via ZADD) and set up time.AfterFunc.
		e.scheduleAfter(remaining, wfID, rID)
	}

	total := len(fired) + len(future)
	if total > 0 {
		slog.Info("delay timer recovery complete",
			"fired", len(fired), "rescheduled", len(future))
	}
}

// splitDelayMember parses "workflowID:runID" from a sorted set member.
// Returns empty strings if the format is invalid.
func splitDelayMember(member string) (workflowID, runID string) {
	idx := strings.Index(member, ":")
	if idx <= 0 || idx >= len(member)-1 {
		return "", ""
	}
	return member[:idx], member[idx+1:]
}

// delayPollerInterval is how often the background poller checks for fired durable timers.
const delayPollerInterval = 5 * time.Second

// delayPollerLockKey is the distributed lock for the delay timer poller.
// Only one replica should poll at a time.
const delayPollerLockKey = "cordum:wf:delay:poller"

// staleDelayAge is the age threshold for stale timer cleanup.
// Entries older than this are removed to prevent unbounded ZSET growth.
const staleDelayAge = 1 * time.Hour

// staleCleanupEveryNTicks controls how often stale cleanup runs relative to the poller.
// With delayPollerInterval=5s and N=60, cleanup runs every ~5 minutes.
const staleCleanupEveryNTicks = 60

// startDelayPoller runs a background goroutine that periodically pops fired
// durable delay timers from Redis. This catches timers where the local
// time.AfterFunc was lost (crash, restart, rebalance). Uses a distributed lock
// so only one replica polls at a time. Also periodically cleans stale entries.
func (e *Engine) startDelayPoller(ctx context.Context) {
	if e.store == nil {
		return
	}

	ticker := time.NewTicker(delayPollerInterval)
	defer ticker.Stop()

	tickCount := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tickCount++

			// Acquire distributed lock (best-effort).
			if e.lockMgr.locker != nil {
				lockCtx, lockCancel := context.WithTimeout(ctx, 2*time.Second)
				token, err := e.lockMgr.locker.TryAcquireLock(lockCtx, delayPollerLockKey, delayPollerInterval*2)
				lockCancel()
				if err != nil || token == "" {
					continue // another replica is polling
				}
				// Lock acquired — proceed. Lock expires after 2*interval (no explicit release).
			}

			popCtx, popCancel := context.WithTimeout(ctx, 5*time.Second)
			fired, err := e.store.PopFiredDelays(popCtx, time.Now())
			popCancel()
			if err != nil {
				slog.Warn("delay poller: pop failed", "error", err)
				continue
			}
			for _, member := range fired {
				wfID, rID := splitDelayMember(member)
				if wfID == "" || rID == "" {
					continue
				}
				slog.Info("delay poller: firing recovered timer",
					"workflow_id", wfID, "run_id", rID)
				if err := e.StartRun(ctx, wfID, rID); err != nil {
					slog.Error("delay poller: StartRun failed, reconciler will retry",
						"workflow_id", wfID, "run_id", rID, "error", err)
				}
			}

			// Periodic stale entry cleanup.
			if tickCount%staleCleanupEveryNTicks == 0 {
				cutoff := time.Now().Add(-staleDelayAge)
				cleanCtx, cleanCancel := context.WithTimeout(ctx, 2*time.Second)
				removed, err := e.store.CleanStaleDelays(cleanCtx, cutoff)
				cleanCancel()
				if err != nil {
					slog.Warn("delay poller: stale cleanup failed", "error", err)
				} else if removed > 0 {
					slog.Info("delay poller: cleaned stale timers", "removed", removed)
				}
			}
		}
	}
}

func (e *Engine) appendTimeline(ctx context.Context, run *WorkflowRun, eventType, stepID, jobID, status, resultPtr, message string, data map[string]any) {
	if e == nil || e.store == nil || run == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	evt := &TimelineEvent{
		Time:       time.Now().UTC(),
		Type:       eventType,
		RunID:      run.ID,
		WorkflowID: run.WorkflowID,
		StepID:     stepID,
		JobID:      jobID,
		Status:     status,
		ResultPtr:  resultPtr,
		Message:    message,
		Data:       data,
	}
	if strings.HasPrefix(evt.Type, "step_") {
		attrs := []any{
			"run_id", evt.RunID,
			"workflow_id", evt.WorkflowID,
			"step_id", evt.StepID,
			"job_id", evt.JobID,
			"event_type", evt.Type,
			"status", evt.Status,
		}
		if evt.ResultPtr != "" {
			attrs = append(attrs, "result_ptr", evt.ResultPtr)
		}
		if evt.Message != "" {
			attrs = append(attrs, "detail", evt.Message)
		}
		slog.Info("workflow step transition", attrs...)
	}
	if err := e.store.AppendTimelineEvent(ctx, run.ID, evt); err != nil {
		slog.Error("timeline event append failed", "run_id", run.ID, "event_type", evt.Type, "error", err)
		slog.Info("timeline event fallback", "run_id", run.ID, "step_id", evt.StepID, "type", evt.Type, "message", evt.Message)
	}
}
