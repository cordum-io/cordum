package edge

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSessionSweeperRemovesAgedSessionAndPreservesFresh(t *testing.T) {
	ctx := context.Background()
	rec := &abortRecorder{}
	store, _, _, cleanup := newRedisEdgeStore(t, WithRecorder(rec))
	defer cleanup()

	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	agedStarted := now.Add(-40 * 24 * time.Hour)
	freshStarted := now.Add(-10 * 24 * time.Hour)
	createSessionAndExecution(t, ctx, store, "tenant-a", "sess-aged-sweep", "exec-aged-sweep", agedStarted)
	createSessionAndExecution(t, ctx, store, "tenant-a", "sess-fresh-sweep", "exec-fresh-sweep", freshStarted)
	if _, err := store.EndSession(ctx, "tenant-a", "sess-aged-sweep", now.Add(-31*24*time.Hour), SessionStatusEnded); err != nil {
		t.Fatalf("EndSession aged: %v", err)
	}

	sweeper, err := NewSessionSweeper(store, SessionSweeperOptions{
		RetentionTTL: DefaultSessionRetentionTTL,
		Interval:     time.Hour,
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewSessionSweeper: %v", err)
	}
	swept, err := sweeper.SweepOnce(ctx)
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if swept != 1 {
		t.Fatalf("swept = %d, want 1", swept)
	}
	if got := rec.SnapshotSessionsSwept(); got != 1 {
		t.Fatalf("sessions_swept metric = %d, want 1", got)
	}
	if _, ok, err := store.GetSession(ctx, "tenant-a", "sess-aged-sweep"); err != nil || ok {
		t.Fatalf("aged session exists=%v err=%v, want removed", ok, err)
	}
	if _, ok, err := store.GetSession(ctx, "tenant-a", "sess-fresh-sweep"); err != nil || !ok {
		t.Fatalf("fresh session exists=%v err=%v, want preserved", ok, err)
	}
}

func TestSessionSweeperIgnoresHeartbeatKeys(t *testing.T) {
	ctx := context.Background()
	store, _, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	agedStarted := now.Add(-40 * 24 * time.Hour)
	freshStarted := now.Add(-10 * 24 * time.Hour)
	createSessionAndExecution(t, ctx, store, "tenant-a", "sess-aged-heartbeat-sweep", "exec-aged-heartbeat-sweep", agedStarted)
	createSessionAndExecution(t, ctx, store, "tenant-a", "sess-fresh-heartbeat", "exec-fresh-heartbeat", freshStarted)
	if _, err := store.EndSession(ctx, "tenant-a", "sess-aged-heartbeat-sweep", now.Add(-31*24*time.Hour), SessionStatusEnded); err != nil {
		t.Fatalf("EndSession aged: %v", err)
	}
	if err := store.TouchHeartbeat(ctx, "tenant-a", "sess-fresh-heartbeat"); err != nil {
		t.Fatalf("TouchHeartbeat fresh: %v", err)
	}

	sweeper, err := NewSessionSweeper(store, SessionSweeperOptions{
		RetentionTTL: DefaultSessionRetentionTTL,
		Interval:     time.Hour,
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewSessionSweeper: %v", err)
	}
	swept, err := sweeper.SweepOnce(ctx)
	if err != nil {
		t.Fatalf("SweepOnce with heartbeat side key: %v", err)
	}
	if swept != 1 {
		t.Fatalf("swept = %d, want 1", swept)
	}
	if _, ok, err := store.GetSession(ctx, "tenant-a", "sess-aged-heartbeat-sweep"); err != nil || ok {
		t.Fatalf("aged session exists=%v err=%v, want removed", ok, err)
	}
	if _, ok, err := store.GetSession(ctx, "tenant-a", "sess-fresh-heartbeat"); err != nil || !ok {
		t.Fatalf("fresh heartbeat session exists=%v err=%v, want preserved", ok, err)
	}
	alive, err := store.HeartbeatAlive(ctx, "tenant-a", "sess-fresh-heartbeat")
	if err != nil {
		t.Fatalf("HeartbeatAlive fresh: %v", err)
	}
	if !alive {
		t.Fatalf("fresh heartbeat was not preserved")
	}
}

func TestSessionSweeperAfterDeleteSessionIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store, _, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	agedStarted := now.Add(-40 * 24 * time.Hour)
	createSessionAndExecution(t, ctx, store, "tenant-a", "sess-delete-before-sweep", "exec-delete-before-sweep", agedStarted)
	if _, err := store.EndSession(ctx, "tenant-a", "sess-delete-before-sweep", now.Add(-31*24*time.Hour), SessionStatusEnded); err != nil {
		t.Fatalf("EndSession aged: %v", err)
	}
	if err := store.DeleteSession(ctx, "tenant-a", "sess-delete-before-sweep"); err != nil {
		t.Fatalf("DeleteSession first pass: %v", err)
	}
	sweeper, err := NewSessionSweeper(store, SessionSweeperOptions{
		RetentionTTL: DefaultSessionRetentionTTL,
		Interval:     time.Hour,
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewSessionSweeper: %v", err)
	}
	swept, err := sweeper.SweepOnce(ctx)
	if err != nil {
		t.Fatalf("SweepOnce after DeleteSession: %v", err)
	}
	if swept != 0 {
		t.Fatalf("swept after explicit DeleteSession = %d, want 0", swept)
	}
}

func TestSessionSweeperRunRespectsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store, _, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	sweeper, err := NewSessionSweeper(store, SessionSweeperOptions{Interval: time.Millisecond})
	if err != nil {
		t.Fatalf("NewSessionSweeper: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		sweeper.Run(ctx)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sweeper.Run did not return after context cancellation")
	}
}

func TestSessionSweeperRejectsInvalidOptions(t *testing.T) {
	store, _, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	if _, err := NewSessionSweeper(nil, SessionSweeperOptions{}); err == nil {
		t.Fatal("NewSessionSweeper nil store succeeded, want error")
	}
	if _, err := NewSessionSweeper(store, SessionSweeperOptions{RetentionTTL: -time.Second}); err == nil {
		t.Fatal("NewSessionSweeper negative retention succeeded, want error")
	}
	if _, err := NewSessionSweeper(store, SessionSweeperOptions{Interval: -time.Second}); err == nil {
		t.Fatal("NewSessionSweeper negative interval succeeded, want error")
	}
}

func TestSessionSweeperSweepOnceReturnsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store, _, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	sweeper, err := NewSessionSweeper(store, SessionSweeperOptions{})
	if err != nil {
		t.Fatalf("NewSessionSweeper: %v", err)
	}
	if _, err := sweeper.SweepOnce(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("SweepOnce canceled error = %v, want context.Canceled", err)
	}
}

func TestExpireApprovalsSweeper_RunsPeriodically(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	rec := &abortRecorder{}
	store, _, _, cleanup := newRedisEdgeStore(t, WithClock(func() time.Time { return now }), WithRecorder(rec))
	defer cleanup()

	createApprovalParents(t, ctx, store, "tenant-a", "sess-appr-sweep", "exec-appr-sweep", "event-appr-sweep", now)
	req := validApprovalRequest("tenant-a", "sess-appr-sweep", "exec-appr-sweep", "event-appr-sweep", now)
	req.ExpiresAt = now.Add(time.Minute)
	approval, err := store.EnqueueApproval(ctx, req)
	if err != nil {
		t.Fatalf("EnqueueApproval: %v", err)
	}
	now = now.Add(2 * time.Minute)

	sweeper, err := NewApprovalSweeper(store, ApprovalSweeperOptions{
		Interval: 10 * time.Millisecond,
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewApprovalSweeper: %v", err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go sweeper.Run(runCtx)

	deadline := time.After(time.Second)
	for {
		got, ok, err := store.GetApproval(ctx, "tenant-a", approval.ApprovalRef)
		if err != nil {
			t.Fatalf("GetApproval: %v", err)
		}
		if ok && got.Status == ApprovalStatusExpired {
			durations, expired := rec.SnapshotApprovalSweepMetrics()
			if durations == 0 || expired == 0 {
				t.Fatalf("approval sweep metrics durations=%d expired=%d, want non-zero", durations, expired)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("approval %s did not expire via sweeper; got=%#v", approval.ApprovalRef, got)
		case <-time.After(10 * time.Millisecond):
		}
	}
}
