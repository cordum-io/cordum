// Package redisutil holds small, self-contained Redis helpers that are
// shared across gateway + scheduler + store packages. The package
// intentionally depends only on go-redis and the standard library — it
// must not import cordum core types so any core package can safely
// depend on it without circular-import risk.
package redisutil

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// ErrMaxAttemptsExceeded is returned by Retry when the transaction
// function kept returning redis.TxFailedErr for the whole attempt
// budget. The wrapped underlying error from the last attempt is
// available via errors.Unwrap / %w.
var ErrMaxAttemptsExceeded = errors.New("redisutil: max CAS retry attempts exceeded")

// Option configures a call to Retry. The functional-options pattern
// keeps the common case (3 attempts, zero watched keys) to a single
// positional argument list.
type Option func(*config)

type config struct {
	maxAttempts int
	keys        []string
}

// WithMaxAttempts overrides the default 3-attempt retry budget.
// Callers that used mcpCASMaxAttempts = 5 or similar constants
// should pass WithMaxAttempts(5) to preserve their existing cap.
func WithMaxAttempts(n int) Option {
	return func(c *config) { c.maxAttempts = n }
}

// WithKeys supplies the WATCH key list for the underlying
// client.Watch call. Zero keys (the default) matches the bare
// go-redis Watch(ctx, fn) signature.
func WithKeys(keys ...string) Option {
	return func(c *config) { c.keys = append(c.keys, keys...) }
}

// Retry runs fn inside a Redis WATCH transaction, retrying on
// redis.TxFailedErr up to the configured attempt budget (default 3).
//
// Return semantics — unchanged from the inline retry loops this
// helper replaces:
//   - nil on success (fn returned nil within the budget).
//   - Any non-TxFailedErr bubbles immediately from the first attempt
//     that sees it; no retry, no wrapping. Callers can errors.Is /
//     errors.As the underlying cause as before.
//   - ErrMaxAttemptsExceeded (wrapping the last TxFailedErr) when the
//     full budget is exhausted on retryable conflicts.
//   - ctx.Err() when the context is cancelled between attempts.
//
// Closures passed as fn that mutate outer-scoped variables keep their
// usual semantics: they run fresh on every attempt, so the closure
// should reset any intermediate state it doesn't want to carry across
// retries. This matches the behaviour of passing fn directly to
// client.Watch in the pre-refactor code.
func Retry(ctx context.Context, client redis.UniversalClient, fn func(*redis.Tx) error, opts ...Option) error {
	cfg := config{maxAttempts: 3}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.maxAttempts <= 0 {
		return fmt.Errorf("redisutil: non-positive maxAttempts=%d", cfg.maxAttempts)
	}

	var lastErr error
	for attempt := 0; attempt < cfg.maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := client.Watch(ctx, fn, cfg.keys...)
		if err == nil {
			return nil
		}
		lastErr = err
		if errors.Is(err, redis.TxFailedErr) {
			continue
		}
		return err
	}
	return fmt.Errorf("%w (last: %v)", ErrMaxAttemptsExceeded, lastErr)
}
