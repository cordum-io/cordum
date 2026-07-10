package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/internal/testredis"
)

func newTestStore(t *testing.T) *RedisStore {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(srv.Close)
	return NewRedisStore(testredis.NewClient(t, srv.Addr()))
}

func TestRedisStore_GetSessionMissReturnsNotFound(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.GetSession(context.Background(), "tenant-a", "missing", "alice"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSession(miss) err = %v, want ErrNotFound", err)
	}
}

func TestRedisStore_AppendThenGet(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.AppendMessage(ctx, "tenant-a", "alice", "sess-1", CopilotMessage{
		Role: "tool", Content: "cordum_list_jobs", JobIDs: []string{"job-1"},
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	got, err := s.GetSession(ctx, "tenant-a", "sess-1", "alice")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.ID != "sess-1" || got.UserID != "alice" {
		t.Fatalf("session = %+v, want id=sess-1 user=alice", got)
	}
	if len(got.Messages) != 1 || got.Messages[0].JobIDs[0] != "job-1" {
		t.Fatalf("messages = %+v, want 1 with job-1", got.Messages)
	}
}

func TestRedisStore_CrossTenantIsolation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.AppendMessage(ctx, "tenant-a", "alice", "sess-x", CopilotMessage{Role: "tool", Content: "secret"}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	// Same session id, different tenant → must not resolve.
	if _, err := s.GetSession(ctx, "tenant-b", "sess-x", "bob"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant GetSession err = %v, want ErrNotFound", err)
	}
}

func TestRedisStore_AppendIdempotentOnMessageID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	msg := CopilotMessage{ID: "fixed-id", Role: "tool", Content: "x", Timestamp: time.Unix(1000, 0).UTC()}
	for i := 0; i < 3; i++ {
		if err := s.AppendMessage(ctx, "tenant-a", "alice", "sess-i", msg); err != nil {
			t.Fatalf("AppendMessage #%d: %v", i, err)
		}
	}
	got, err := s.GetSession(ctx, "tenant-a", "sess-i", "alice")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("messages = %d, want 1 (idempotent on ID)", len(got.Messages))
	}
}

func TestRedisStore_ConcurrentAppendsNoLostUpdates(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- s.AppendMessage(ctx, "tenant-a", "alice", "sess-c", CopilotMessage{
				ID: fmt.Sprintf("m-%d", i), Role: "tool", Content: fmt.Sprintf("call-%d", i),
			})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent AppendMessage: %v", err)
		}
	}
	got, err := s.GetSession(ctx, "tenant-a", "sess-c", "alice")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if len(got.Messages) != n {
		t.Fatalf("messages = %d, want %d (no lost updates)", len(got.Messages), n)
	}
}

func TestRedisStore_MessageCapKeepsNewest(t *testing.T) {
	s := newTestStore(t)
	s.maxMessages = 3
	ctx := context.Background()
	for i := 0; i < 6; i++ {
		if err := s.AppendMessage(ctx, "tenant-a", "alice", "sess-cap", CopilotMessage{
			ID: fmt.Sprintf("m-%d", i), Role: "tool", Content: fmt.Sprintf("c-%d", i),
		}); err != nil {
			t.Fatalf("AppendMessage #%d: %v", i, err)
		}
	}
	got, err := s.GetSession(ctx, "tenant-a", "sess-cap", "alice")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if len(got.Messages) != 3 {
		t.Fatalf("messages = %d, want 3 (capped)", len(got.Messages))
	}
	if got.Messages[0].ID != "m-3" || got.Messages[2].ID != "m-5" {
		t.Fatalf("cap kept wrong window: %+v", got.Messages)
	}
}

func TestRedisStore_AppendMessage_SamePrincipalSucceeds(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := s.AppendMessage(ctx, "tenant-a", "alice", "sess-owner", CopilotMessage{
			ID: fmt.Sprintf("m-%d", i), Role: "tool", Content: fmt.Sprintf("call-%d", i),
		}); err != nil {
			t.Fatalf("AppendMessage #%d (same principal): %v", i, err)
		}
	}
	got, err := s.GetSession(ctx, "tenant-a", "sess-owner", "alice")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if len(got.Messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(got.Messages))
	}
}

func TestRedisStore_AppendMessage_CrossPrincipalRejected(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.AppendMessage(ctx, "tenant-a", "alice", "sess-guessed", CopilotMessage{
		ID: "m-alice-1", Role: "tool", Content: "alice's real call",
	}); err != nil {
		t.Fatalf("AppendMessage (owner): %v", err)
	}
	// mallory guesses/knows alice's session id and tries to inject a
	// fabricated transcript entry into it.
	err := s.AppendMessage(ctx, "tenant-a", "mallory", "sess-guessed", CopilotMessage{
		ID: "m-mallory-1", Role: "tool", Content: "fabricated by mallory",
	})
	if !errors.Is(err, ErrOwnerMismatch) {
		t.Fatalf("cross-principal AppendMessage err = %v, want ErrOwnerMismatch", err)
	}
	// The fabricated message must not have been written.
	got, err := s.GetSession(ctx, "tenant-a", "sess-guessed", "alice")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if len(got.Messages) != 1 || got.Messages[0].Content != "alice's real call" {
		t.Fatalf("messages = %+v, want only alice's original message", got.Messages)
	}
	if got.UserID != "alice" {
		t.Fatalf("session owner leaked/changed: userID = %q, want alice", got.UserID)
	}
}

func TestRedisStore_AppendMessage_LegacySessionAdoptsFirstWriter(t *testing.T) {
	// A session written before ownership tracking existed decodes with
	// Owner == "". The next writer claims it, and a *different* writer
	// after that is still rejected.
	s := newTestStore(t)
	ctx := context.Background()
	key := sessionKey("tenant-a", "sess-legacy")
	legacy := storedSession{
		Tenant: "tenant-a",
		Session: CopilotSession{
			ID:        "sess-legacy",
			UserID:    "alice",
			CreatedAt: time.Unix(1000, 0).UTC(),
			Messages: []CopilotMessage{
				{ID: "m-0", Role: "tool", Content: "pre-existing", Timestamp: time.Unix(1000, 0).UTC()},
			},
		},
	}
	encoded, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy session: %v", err)
	}
	if err := s.client.Set(ctx, key, encoded, 0).Err(); err != nil {
		t.Fatalf("seed legacy session: %v", err)
	}

	if err := s.AppendMessage(ctx, "tenant-a", "alice", "sess-legacy", CopilotMessage{
		ID: "m-1", Role: "tool", Content: "adopted by alice",
	}); err != nil {
		t.Fatalf("AppendMessage (adopt): %v", err)
	}

	err = s.AppendMessage(ctx, "tenant-a", "mallory", "sess-legacy", CopilotMessage{
		ID: "m-2", Role: "tool", Content: "fabricated",
	})
	if !errors.Is(err, ErrOwnerMismatch) {
		t.Fatalf("post-adoption cross-principal AppendMessage err = %v, want ErrOwnerMismatch", err)
	}
}

func TestNotImplementedStore_SignatureMatches(t *testing.T) {
	var _ Store = NotImplementedStore{}
	if _, err := (NotImplementedStore{}).GetSession(context.Background(), "t", "s", "u"); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("stub err = %v, want ErrNotImplemented", err)
	}
}
