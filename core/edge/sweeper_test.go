package edge

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/redis/go-redis/v9"
)

func TestSessionSweeperRemovesAgedSessionAndPreservesFresh(t *testing.T) {
	ctx := context.Background()
	rec := &abortRecorder{}
	store, client, _, cleanup := newRedisEdgeStore(t, WithRecorder(rec))
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
		LegalHolds:   audit.NewLegalHoldStoreFromClient(client),
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
	store, client, _, cleanup := newRedisEdgeStore(t)
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
		LegalHolds:   audit.NewLegalHoldStoreFromClient(client),
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
	store, client, _, cleanup := newRedisEdgeStore(t)
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
		LegalHolds:   audit.NewLegalHoldStoreFromClient(client),
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
	store, client, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	sweeper, err := NewSessionSweeper(store, SessionSweeperOptions{LegalHolds: audit.NewLegalHoldStoreFromClient(client)})
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

func TestSessionSweeperKeepsSessionsUnderLegalHold(t *testing.T) {
	ctx := context.Background()
	store, client, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	aged := now.Add(-40 * 24 * time.Hour)
	for _, tenant := range []string{"tenant-held", "tenant-broken", "tenant-free"} {
		createSessionAndExecution(t, ctx, store, tenant, "sess-"+tenant, "exec-"+tenant, aged)
		if _, err := store.EndSession(ctx, tenant, "sess-"+tenant, now.Add(-31*24*time.Hour), SessionStatusEnded); err != nil {
			t.Fatalf("EndSession %s: %v", tenant, err)
		}
	}
	holds := audit.NewLegalHoldStoreFromClient(client)
	hold, err := holds.CreateHold(ctx, "tenant-held", "litigation", "test")
	if err != nil {
		t.Fatalf("CreateHold: %v", err)
	}
	// An unreadable hold record must never read as "no hold".
	if err := client.SAdd(ctx, "audit:legal_holds:tenant-broken", "h-broken").Err(); err != nil {
		t.Fatalf("sadd: %v", err)
	}
	if err := client.Set(ctx, "audit:legal_hold:h-broken", "{not json", 0).Err(); err != nil {
		t.Fatalf("set: %v", err)
	}
	sweeper, err := NewSessionSweeper(store, SessionSweeperOptions{
		Now:        func() time.Time { return now },
		LegalHolds: holds,
	})
	if err != nil {
		t.Fatalf("NewSessionSweeper: %v", err)
	}

	if swept, err := sweeper.SweepOnce(ctx); err != nil || swept != 1 {
		t.Fatalf("SweepOnce = %d, %v; want only tenant-free swept", swept, err)
	}
	for tenant, want := range map[string]bool{"tenant-held": true, "tenant-broken": true, "tenant-free": false} {
		if _, ok, err := store.GetSession(ctx, tenant, "sess-"+tenant); err != nil || ok != want {
			t.Fatalf("%s session exists=%v err=%v, want %v", tenant, ok, err, want)
		}
	}

	// Releasing the hold lets retention resume.
	if err := holds.ReleaseHold(ctx, hold.ID, "test"); err != nil {
		t.Fatalf("ReleaseHold: %v", err)
	}
	if swept, err := sweeper.SweepOnce(ctx); err != nil || swept != 1 {
		t.Fatalf("SweepOnce after release = %d, %v; want tenant-held swept", swept, err)
	}

	// Without a legal hold store nothing is deleted at all.
	noHolds, err := NewSessionSweeper(store, SessionSweeperOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewSessionSweeper: %v", err)
	}
	if swept, err := noHolds.SweepOnce(ctx); !errors.Is(err, errSessionSweepNoLegalHolds) || swept != 0 {
		t.Fatalf("SweepOnce without holds = %d, %v; want errSessionSweepNoLegalHolds", swept, err)
	}
	if _, ok, err := store.GetSession(ctx, "tenant-broken", "sess-tenant-broken"); err != nil || !ok {
		t.Fatalf("tenant-broken session exists=%v err=%v, want kept", ok, err)
	}
}

// beforeDeleteHook runs fn once, right before the client first deletes keys.
type beforeDeleteHook struct {
	once sync.Once
	fn   func()
}

func (h *beforeDeleteHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *beforeDeleteHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if name := cmd.Name(); name == "del" || name == "unlink" {
			h.once.Do(h.fn)
		}
		return next(ctx, cmd)
	}
}

func (h *beforeDeleteHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// A hold created while a session is being deleted is acknowledged only once
// that deletion has ended; from then on the tenant's sessions are kept.
func TestSessionSweeperHoldDuringDeleteIsAcknowledgedAfterIt(t *testing.T) {
	ctx := context.Background()
	store, client, _, cleanup := newRedisEdgeStore(t)
	defer cleanup()

	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	aged := now.Add(-40 * 24 * time.Hour)
	for _, id := range []string{"sess-1", "sess-2"} {
		createSessionAndExecution(t, ctx, store, "tenant-a", id, "exec-"+id, aged)
		if _, err := store.EndSession(ctx, "tenant-a", id, now.Add(-31*24*time.Hour), SessionStatusEnded); err != nil {
			t.Fatalf("EndSession %s: %v", id, err)
		}
	}
	holds := audit.NewLegalHoldStoreFromClient(client)
	created := make(chan error, 1)
	client.AddHook(&beforeDeleteHook{fn: func() {
		go func() {
			_, err := holds.CreateHold(ctx, "tenant-a", "litigation", "test")
			created <- err
		}()
		// CreateHold records the hold at once, then waits for this deletion.
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
			if held, err := holds.IsUnderHold(ctx, "tenant-a"); err == nil && held {
				break
			}
			if time.Now().After(deadline) {
				t.Error("hold not recorded while the deletion ran")
				return
			}
		}
		select {
		case err := <-created:
			created <- err
			t.Errorf("CreateHold returned (%v) before the deletion ended", err)
		case <-time.After(30 * time.Millisecond):
		}
	}})
	sweeper, err := NewSessionSweeper(store, SessionSweeperOptions{Now: func() time.Time { return now }, LegalHolds: holds})
	if err != nil {
		t.Fatalf("NewSessionSweeper: %v", err)
	}

	if swept, err := sweeper.SweepOnce(ctx); err != nil || swept != 1 {
		t.Fatalf("SweepOnce = %d, %v; want the deletion in flight to end and the other session kept", swept, err)
	}
	if err := <-created; err != nil {
		t.Fatalf("CreateHold: %v", err)
	}
	var kept int
	for _, id := range []string{"sess-1", "sess-2"} {
		_, ok, err := store.GetSession(ctx, "tenant-a", id)
		if err != nil {
			t.Fatalf("GetSession %s: %v", id, err)
		}
		if ok {
			kept++
		}
	}
	if kept != 1 {
		t.Fatalf("%d sessions kept, want 1", kept)
	}
	if swept, err := sweeper.SweepOnce(ctx); err != nil || swept != 0 {
		t.Fatalf("SweepOnce under hold = %d, %v; want 0", swept, err)
	}
}
