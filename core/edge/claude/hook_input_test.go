package claude

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

func TestReadHookInputClosesBlockingReaderOnContextCancellation(t *testing.T) {
	reader := newCloseAwareBlockingReader()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err := readHookInput(ctx, reader, DefaultMaxInputBytes)
	elapsed := time.Since(started)

	if !errors.Is(err, errInputTimeout) {
		t.Fatalf("readHookInput error = %v, want %v", err, errInputTimeout)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("readHookInput took %s after context cancellation, want prompt return", elapsed)
	}
	select {
	case <-reader.closed:
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("blocking reader was not closed after context cancellation; read goroutine still depends on stdin unblocking")
	}
}

type closeAwareBlockingReader struct {
	closed chan struct{}
	once   sync.Once
}

func newCloseAwareBlockingReader() *closeAwareBlockingReader {
	return &closeAwareBlockingReader{closed: make(chan struct{})}
}

func (r *closeAwareBlockingReader) Read([]byte) (int, error) {
	<-r.closed
	return 0, io.ErrClosedPipe
}

func (r *closeAwareBlockingReader) Close() error {
	r.once.Do(func() {
		close(r.closed)
	})
	return nil
}
