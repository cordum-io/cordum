package testutil

import (
	"context"
	"testing"
	"time"
)

// TestContext returns a context derived from the test's deadline (set via
// go test -timeout). If no deadline is set, falls back to 30 seconds.
// The context is automatically cancelled when the test finishes.
func TestContext(t *testing.T) context.Context {
	t.Helper()
	deadline, ok := t.Deadline()
	if !ok {
		deadline = time.Now().Add(30 * time.Second)
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	t.Cleanup(cancel)
	return ctx
}
