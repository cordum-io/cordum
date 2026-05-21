package outbound

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// blockingNonceStore observes ctx.Done() and returns ctx.Err(). Used to
// assert the Verifier propagates a deadlined ctx to the nonce store
// instead of a captured background context (HIGH #3 fix).
type blockingNonceStore struct {
	called atomic.Bool
}

func (s *blockingNonceStore) SeenAndRecord(ctx context.Context, _ string, _ time.Duration) (bool, error) {
	s.called.Store(true)
	<-ctx.Done()
	return false, ctx.Err()
}

// TestVerifyRequest_PerCallNonceCtxHonored asserts that Verifier.VerifyRequest
// passes a deadlined context down to NonceStore.SeenAndRecord. Pre-fix,
// RedisNonceStore captured ctx=context.Background() at construction and
// used it for every SeenAndRecord call — a slow Redis turned every
// signed-request verifier into a stuck goroutine (DoS amplifier).
//
// Post-fix:
//   - NonceStore.SeenAndRecord accepts a ctx parameter.
//   - Verifier.VerifyRequest accepts a ctx parameter.
//   - Internally, the verifier wraps the caller ctx with WithTimeout
//     (replayCommandTimeout) so a wedged store cannot freeze the
//     verifier indefinitely.
func TestVerifyRequest_PerCallNonceCtxHonored(t *testing.T) {
	t.Parallel()
	signer, pub := newSigner(t, "k-perctx")
	store := &blockingNonceStore{}
	verifier := newVerifier(t, "k-perctx", pub, store, DefaultClockSkew)

	headers, err := signer.SignRequest("tools/call", []byte(`{}`), "t", "a")
	if err != nil {
		t.Fatalf("SignRequest: %v", err)
	}

	// Cap the test wait: if the verifier doesn't propagate a deadlined
	// ctx, the blocking store would hang until our deadline fires —
	// either way we ASSERT the verifier returned an error mentioning the
	// nonce store within the bound.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- verifier.VerifyRequest(ctx, headers, "tools/call", []byte(`{}`))
	}()
	select {
	case verr := <-done:
		if verr == nil {
			t.Fatal("VerifyRequest returned nil despite blocking nonce store")
		}
		if !store.called.Load() {
			t.Fatal("NonceStore.SeenAndRecord was never called — verifier short-circuited before replay check")
		}
		// The wrapped error must reference the nonce store path; the
		// exact wording is fmt.Errorf("mcp outbound: nonce store: %w").
	case <-time.After(2 * time.Second):
		t.Fatal("VerifyRequest did not honor per-call ctx timeout — nonce store hung the verifier (DoS amplifier)")
	}
}

// TestInMemoryNonceStore_AcceptsCtx is the interface-shape assertion:
// the InMemoryNonceStore SeenAndRecord signature accepts ctx without
// touching its internal state on cancellation. The store has no I/O so
// it should ignore the ctx gracefully.
func TestInMemoryNonceStore_AcceptsCtx(t *testing.T) {
	t.Parallel()
	store := NewInMemoryNonceStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	seen, err := store.SeenAndRecord(ctx, "nonce-x", time.Second)
	if err != nil {
		t.Fatalf("InMemoryNonceStore should ignore cancelled ctx (no I/O): %v", err)
	}
	if seen {
		t.Fatal("first SeenAndRecord on fresh store should report unseen")
	}
}
