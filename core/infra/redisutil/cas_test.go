package redisutil_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/cordum/cordum/core/infra/redisutil"
)

func newMiniredisClient(t *testing.T) (redis.UniversalClient, func()) {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	return client, func() {
		_ = client.Close()
		srv.Close()
	}
}

// TestRetry_SuccessFirstTry asserts the happy path: a tx that returns nil on
// its first Watch iteration returns nil from Retry with exactly one attempt.
func TestRetry_SuccessFirstTry(t *testing.T) {
	client, cleanup := newMiniredisClient(t)
	defer cleanup()

	var attempts atomic.Int32
	err := redisutil.Retry(context.Background(), client, func(tx *redis.Tx) error {
		attempts.Add(1)
		return nil
	}, redisutil.WithKeys("k"))
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("expected 1 attempt, got %d", got)
	}
}

// TestRetry_RetriesOnTxFailedErr forces the fn to return redis.TxFailedErr
// for the first two attempts and nil on the third; Retry must call fn exactly
// 3 times and ultimately return nil.
func TestRetry_RetriesOnTxFailedErr(t *testing.T) {
	client, cleanup := newMiniredisClient(t)
	defer cleanup()

	var attempts atomic.Int32
	err := redisutil.Retry(context.Background(), client, func(tx *redis.Tx) error {
		n := attempts.Add(1)
		if n < 3 {
			return redis.TxFailedErr
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil after 3rd attempt, got %v", err)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}

// TestRetry_MaxAttemptsExhaustedReturnsSentinel asserts that persistent
// TxFailedErr over the whole budget yields ErrMaxAttemptsExceeded (not
// the raw redis.TxFailedErr) and that errors.Is detects both.
func TestRetry_MaxAttemptsExhaustedReturnsSentinel(t *testing.T) {
	client, cleanup := newMiniredisClient(t)
	defer cleanup()

	var attempts atomic.Int32
	err := redisutil.Retry(context.Background(), client, func(tx *redis.Tx) error {
		attempts.Add(1)
		return redis.TxFailedErr
	}, redisutil.WithMaxAttempts(3))
	if err == nil {
		t.Fatal("expected non-nil error after exhausted budget")
	}
	if !errors.Is(err, redisutil.ErrMaxAttemptsExceeded) {
		t.Fatalf("expected errors.Is(err, ErrMaxAttemptsExceeded), got %v", err)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}

// TestRetry_NonTxFailedErrReturnsImmediately asserts that a non-retryable
// error short-circuits the loop at the first failure with NO retries.
// errors.Is must reach the underlying error; ErrMaxAttemptsExceeded must
// NOT appear in the chain.
func TestRetry_NonTxFailedErrReturnsImmediately(t *testing.T) {
	client, cleanup := newMiniredisClient(t)
	defer cleanup()

	sentinel := errors.New("not a tx failure")
	var attempts atomic.Int32
	err := redisutil.Retry(context.Background(), client, func(tx *redis.Tx) error {
		attempts.Add(1)
		return sentinel
	}, redisutil.WithMaxAttempts(5))
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected errors.Is(err, sentinel), got %v", err)
	}
	if errors.Is(err, redisutil.ErrMaxAttemptsExceeded) {
		t.Fatalf("did not expect ErrMaxAttemptsExceeded on non-retryable error, got %v", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("expected exactly 1 attempt, got %d", got)
	}
}

// TestRetry_CtxCancelBetweenAttempts asserts that a cancelled context
// surfaces ctx.Err() on the next iteration boundary and does NOT continue
// retrying.
func TestRetry_CtxCancelBetweenAttempts(t *testing.T) {
	client, cleanup := newMiniredisClient(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	var attempts atomic.Int32
	err := redisutil.Retry(ctx, client, func(tx *redis.Tx) error {
		attempts.Add(1)
		cancel() // cancel ctx after the first Watch succeeds in the eyes of fn
		return redis.TxFailedErr
	}, redisutil.WithMaxAttempts(5))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected errors.Is(err, context.Canceled), got %v", err)
	}
	// The first attempt ran; the ctx.Err() check at the top of the next
	// iteration returns before another Watch call.
	if got := attempts.Load(); got != 1 {
		t.Fatalf("expected exactly 1 attempt before ctx-cancel short-circuit, got %d", got)
	}
}

// TestRetry_CtxAlreadyCancelledReturnsImmediately verifies the ctx.Err()
// check at the top of the loop short-circuits even before the first Watch.
func TestRetry_CtxAlreadyCancelledReturnsImmediately(t *testing.T) {
	client, cleanup := newMiniredisClient(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var attempts atomic.Int32
	err := redisutil.Retry(ctx, client, func(tx *redis.Tx) error {
		attempts.Add(1)
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if got := attempts.Load(); got != 0 {
		t.Fatalf("expected 0 attempts with pre-cancelled ctx, got %d", got)
	}
}

// TestRetry_NonPositiveMaxAttempts guards against misconfiguration: a
// caller that passes 0 or negative attempts should see a clear error
// rather than silently succeeding/failing.
func TestRetry_NonPositiveMaxAttempts(t *testing.T) {
	client, cleanup := newMiniredisClient(t)
	defer cleanup()

	err := redisutil.Retry(context.Background(), client, func(tx *redis.Tx) error {
		return nil
	}, redisutil.WithMaxAttempts(0))
	if err == nil {
		t.Fatal("expected non-nil error on maxAttempts=0")
	}
}

// TestRetry_WrappedErrorExposesLast asserts the %w wrapping in
// ErrMaxAttemptsExceeded preserves the last underlying error for debugging
// — "exhausted retries" alone is not enough for a postmortem.
func TestRetry_WrappedErrorExposesLast(t *testing.T) {
	client, cleanup := newMiniredisClient(t)
	defer cleanup()

	err := redisutil.Retry(context.Background(), client, func(tx *redis.Tx) error {
		return redis.TxFailedErr
	}, redisutil.WithMaxAttempts(2))
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	// The error message must mention both the sentinel and the last error.
	msg := err.Error()
	if !contains(msg, "max CAS retry attempts exceeded") {
		t.Fatalf("error message missing sentinel text: %q", msg)
	}
	if !contains(msg, "redis: transaction failed") {
		t.Fatalf("error message missing last-attempt text: %q", msg)
	}
	_ = fmt.Sprintf // keep import
	_ = time.Millisecond
}

func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
