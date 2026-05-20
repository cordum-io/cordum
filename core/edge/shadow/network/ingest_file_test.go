package network

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestFileIngestor_ClosesOnCtxCancel(t *testing.T) {
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- streamFile(ctx, pr, "blocked-pipe", make(chan LogRecord))
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("streamFile error = %v, want context.Canceled", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("streamFile did not return within 500ms after ctx cancel")
	}
}
