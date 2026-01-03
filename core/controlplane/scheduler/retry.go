package scheduler

import (
	"errors"
	"fmt"
	"time"
)

type retryableError struct {
	err   error
	delay time.Duration
}

func (e *retryableError) Error() string {
	if e == nil {
		return ""
	}
	if e.delay > 0 {
		return fmt.Sprintf("retry after %s: %v", e.delay, e.err)
	}
	return fmt.Sprintf("retry: %v", e.err)
}

func (e *retryableError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (e *retryableError) RetryDelay() time.Duration {
	if e == nil {
		return 0
	}
	return e.delay
}

// RetryAfter marks an error as retryable for ack/nak-capable buses.
func RetryAfter(err error, delay time.Duration) error {
	if err == nil {
		err = errors.New("retry requested")
	}
	if delay < 0 {
		delay = 0
	}
	return &retryableError{err: err, delay: delay}
}
