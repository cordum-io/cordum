package bus

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRetryableError(t *testing.T) {
	err := &RetryableError{Err: errors.New("boom"), Delay: 0}
	if err.Error() == "" || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("unexpected error string: %s", err.Error())
	}
	if err.RetryDelay() != 0 {
		t.Fatalf("expected zero delay")
	}
	if err.Unwrap() == nil {
		t.Fatalf("expected unwrap error")
	}

	err = &RetryableError{Err: errors.New("later"), Delay: 2 * time.Second}
	if !strings.Contains(err.Error(), "retry after") {
		t.Fatalf("unexpected error string: %s", err.Error())
	}
	if err.RetryDelay() != 2*time.Second {
		t.Fatalf("unexpected delay")
	}
}

func TestRetryDelayNonRetryable(t *testing.T) {
	if delay, ok := RetryDelay(errors.New("no")); ok || delay != 0 {
		t.Fatalf("expected no retry delay")
	}
}

func TestRetryAfterClamp(t *testing.T) {
	err := RetryAfter(nil, -5*time.Second)
	if delay, ok := RetryDelay(err); !ok || delay != 0 {
		t.Fatalf("expected clamped delay")
	}
}
