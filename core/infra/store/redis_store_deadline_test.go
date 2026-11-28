package store

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// opDeadlineHook records the context deadline of the last command named cmd.
type opDeadlineHook struct {
	mu       sync.Mutex
	cmd      string
	seen     bool
	has      bool
	deadline time.Time
}

func (h *opDeadlineHook) reset(cmd string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cmd, h.seen, h.has, h.deadline = cmd, false, false, time.Time{}
}

func (h *opDeadlineHook) recorded() (seen, has bool, deadline time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.seen, h.has, h.deadline
}

func (h *opDeadlineHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *opDeadlineHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.mu.Lock()
		if strings.EqualFold(cmd.Name(), h.cmd) {
			h.deadline, h.has = ctx.Deadline()
			h.seen = true
		}
		h.mu.Unlock()
		return next(ctx, cmd)
	}
}

func (h *opDeadlineHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func TestRedisStoreOpDeadline(t *testing.T) {
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	hook := &opDeadlineHook{}
	client.AddHook(hook)
	s := NewRedisStoreFromClient(client)
	if err := srv.Set("k", "v"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ops := []struct {
		name, cmd string
		call      func(context.Context) error
	}{
		{"PutContext", "set", func(ctx context.Context) error { return s.PutContext(ctx, "k", []byte("v")) }},
		{"DeleteContext", "del", func(ctx context.Context) error { return s.DeleteContext(ctx, "absent") }},
		{"GetContext", "get", func(ctx context.Context) error { _, err := s.GetContext(ctx, "k"); return err }},
		{"PutResult", "set", func(ctx context.Context) error { return s.PutResult(ctx, "k", []byte("v")) }},
		{"GetResult", "get", func(ctx context.Context) error { _, err := s.GetResult(ctx, "k"); return err }},
	}
	record := func(t *testing.T, cmd string, call func() error) (bool, time.Time) {
		t.Helper()
		hook.reset(cmd)
		if err := call(); err != nil {
			t.Fatalf("call: %v", err)
		}
		seen, has, deadline := hook.recorded()
		if !seen {
			t.Fatalf("no %s command reached Redis", cmd)
		}
		return has, deadline
	}
	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			for _, c := range []struct {
				name    string
				timeout time.Duration
			}{{"caller_5s", 5 * time.Second}, {"caller_500ms", 500 * time.Millisecond}} {
				t.Run(c.name, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
					defer cancel()
					want, _ := ctx.Deadline()
					has, got := record(t, op.cmd, func() error { return op.call(ctx) })
					if !has || !got.Equal(want) {
						t.Fatalf("deadline = %v (set %v), want the caller's %v", got, has, want)
					}
				})
			}
			t.Run("no_deadline", func(t *testing.T) {
				before := time.Now()
				has, got := record(t, op.cmd, func() error { return op.call(context.Background()) })
				after := time.Now()
				lo, hi := before.Add(defaultRedisOpTimeout), after.Add(defaultRedisOpTimeout)
				if !has || got.Before(lo) || got.After(hi) {
					t.Fatalf("deadline = %v (set %v), want within [%v, %v]", got, has, lo, hi)
				}
			})
		})
	}
}
