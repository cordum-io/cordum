package workflow

import (
	"context"
	"fmt"
	"strings"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

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

func buildEventAlert(step *Step, payload any) *pb.SystemAlert {
	level := "INFO"
	message := ""
	code := ""
	component := "workflow-engine"

	switch v := payload.(type) {
	case map[string]any:
		if val, ok := v["level"].(string); ok && strings.TrimSpace(val) != "" {
			level = strings.ToUpper(strings.TrimSpace(val))
		}
		if val, ok := v["message"].(string); ok && strings.TrimSpace(val) != "" {
			message = strings.TrimSpace(val)
		}
		if val, ok := v["code"].(string); ok && strings.TrimSpace(val) != "" {
			code = strings.TrimSpace(val)
		}
		if val, ok := v["component"].(string); ok && strings.TrimSpace(val) != "" {
			component = strings.TrimSpace(val)
		}
	case map[string]string:
		if val := strings.TrimSpace(v["level"]); val != "" {
			level = strings.ToUpper(val)
		}
		if val := strings.TrimSpace(v["message"]); val != "" {
			message = val
		}
		if val := strings.TrimSpace(v["code"]); val != "" {
			code = val
		}
		if val := strings.TrimSpace(v["component"]); val != "" {
			component = val
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
		Level:     level,
		Message:   message,
		Component: component,
		Code:      code,
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
	for _, t := range e.pendingTimers {
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
		// Remove ourselves from the pending list while holding the lock.
		for i, pt := range e.pendingTimers {
			if pt == t {
				e.pendingTimers[i] = e.pendingTimers[len(e.pendingTimers)-1]
				e.pendingTimers = e.pendingTimers[:len(e.pendingTimers)-1]
				break
			}
		}
		e.timerMu.Unlock()
		_ = e.StartRun(context.Background(), workflowID, runID)
	})
	e.pendingTimers = append(e.pendingTimers, t)
	e.timerMu.Unlock()
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
	_ = e.store.AppendTimelineEvent(ctx, run.ID, evt)
}
