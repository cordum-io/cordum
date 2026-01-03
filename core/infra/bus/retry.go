package bus

import (
	"errors"
	"fmt"
	"time"
)

// RetryableError marks a handler error as retryable for buses that support
// explicit ack/nak semantics (e.g. NATS JetStream).
type RetryableError struct {
	Err   error
	Delay time.Duration
}

func (e *RetryableError) Error() string {
	if e == nil {
		return ""
	}
	if e.Delay > 0 {
		return fmt.Sprintf("retry after %s: %v", e.Delay, e.Err)
	}
	return fmt.Sprintf("retry: %v", e.Err)
}

func (e *RetryableError) RetryDelay() time.Duration {
	if e == nil {
		return 0
	}
	return e.Delay
}

func (e *RetryableError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// RetryAfter wraps err with a retry delay.
func RetryAfter(err error, delay time.Duration) error {
	if err == nil {
		err = errors.New("retry requested")
	}
	if delay < 0 {
		delay = 0
	}
	return &RetryableError{Err: err, Delay: delay}
}

// RetryDelay extracts a retry delay from err when it is retryable.
func RetryDelay(err error) (time.Duration, bool) {
	type retryDelayProvider interface {
		RetryDelay() time.Duration
	}
	var rd retryDelayProvider
	if errors.As(err, &rd) {
		delay := rd.RetryDelay()
		if delay < 0 {
			delay = 0
		}
		return delay, true
	}
	return 0, false
}
