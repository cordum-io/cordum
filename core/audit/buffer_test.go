package audit

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// mockExporter records Export calls for testing.
type mockExporter struct {
	mu       sync.Mutex
	batches  [][]SIEMEvent
	failNext int // number of times to fail before succeeding

	exportCalled chan struct{}
}

func (m *mockExporter) Export(_ context.Context, events []SIEMEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.exportCalled != nil {
		select {
		case m.exportCalled <- struct{}{}:
		default:
		}
	}
	if m.failNext > 0 {
		m.failNext--
		return errors.New("mock export failure")
	}
	cp := make([]SIEMEvent, len(events))
	copy(cp, events)
	m.batches = append(m.batches, cp)
	return nil
}

func (m *mockExporter) Close() error { return nil }

func (m *mockExporter) totalEvents() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, b := range m.batches {
		n += len(b)
	}
	return n
}

func TestBufferedExporter_FlushOnBatchSize(t *testing.T) {
	mock := &mockExporter{}
	buf := NewBufferedExporter(mock, WithBatchSize(5), WithFlushInterval(10*time.Second))

	for i := 0; i < 5; i++ {
		buf.Send(SIEMEvent{Action: "test"})
	}
	// Wait for flush to happen
	time.Sleep(100 * time.Millisecond)

	if got := mock.totalEvents(); got != 5 {
		t.Errorf("total events = %d, want 5", got)
	}
	if err := buf.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestBufferedExporter_FlushOnTimer(t *testing.T) {
	mock := &mockExporter{}
	buf := NewBufferedExporter(mock, WithBatchSize(100), WithFlushInterval(50*time.Millisecond))

	buf.Send(SIEMEvent{Action: "timer-test"})

	// Wait for timer-based flush
	time.Sleep(200 * time.Millisecond)

	if got := mock.totalEvents(); got != 1 {
		t.Errorf("total events = %d, want 1", got)
	}
	if err := buf.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestBufferedExporter_DrainOnClose(t *testing.T) {
	mock := &mockExporter{}
	buf := NewBufferedExporter(mock, WithBatchSize(100), WithFlushInterval(10*time.Second))

	for i := 0; i < 7; i++ {
		buf.Send(SIEMEvent{Action: "drain-test"})
	}
	// Close immediately — should drain all 7 events
	if err := buf.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := mock.totalEvents(); got != 7 {
		t.Errorf("total events after drain = %d, want 7", got)
	}
}

func TestBufferedExporter_RetryOnFailure(t *testing.T) {
	mock := &mockExporter{failNext: 2} // fail twice, succeed on third
	buf := NewBufferedExporter(mock, WithBatchSize(1), WithFlushInterval(10*time.Second))

	buf.Send(SIEMEvent{Action: "retry-test"})

	// Wait for retries (1s + 2s backoff in production, but test should complete)
	time.Sleep(5 * time.Second)

	if got := mock.totalEvents(); got != 1 {
		t.Errorf("total events = %d, want 1 (after retries)", got)
	}
	if err := buf.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestBufferedExporter_DropWhenFull(t *testing.T) {
	// Use a mock that blocks to fill the channel
	slowMock := &mockExporter{}
	buf := &BufferedExporter{
		exporter:      slowMock,
		ch:            make(chan SIEMEvent, 2), // tiny buffer
		done:          make(chan struct{}),
		batchSize:     100,
		flushInterval: 10 * time.Second,
	}
	// Don't start the loop — channel will fill up

	buf.Send(SIEMEvent{Action: "1"})
	buf.Send(SIEMEvent{Action: "2"})
	// Third send should be dropped (non-blocking)
	buf.Send(SIEMEvent{Action: "3-dropped"})

	if len(buf.ch) != 2 {
		t.Errorf("channel len = %d, want 2 (third should be dropped)", len(buf.ch))
	}
}

func TestBufferedExporter_ExportWithRetryCancels(t *testing.T) {
	called := make(chan struct{}, 1)
	mock := &mockExporter{failNext: maxRetries, exportCalled: called}
	buf := &BufferedExporter{exporter: mock}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		buf.exportWithRetry(ctx, []SIEMEvent{{Action: "cancel-test"}})
		close(done)
	}()

	select {
	case <-called:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("export was not attempted")
	}

	cancel()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("exportWithRetry did not cancel promptly")
	}
}
