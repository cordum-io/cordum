package network

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestStdinIngestor_StreamCancelsOnContextDone(t *testing.T) {
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()

	ingest := NewStdinIngestor(pr)
	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan error, 1)
	out := make(chan LogRecord)

	go func() {
		doneCh <- ingest.Stream(ctx, out)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-doneCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Stream error = %v, want nil or context.Canceled", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Stream did not return within 500ms after cancel")
	}
}
