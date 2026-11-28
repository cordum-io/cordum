package audit

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
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

// contextCheckingMockExporter simulates context-aware HTTP/SIEM exporters.
type contextCheckingMockExporter struct {
	mu           sync.Mutex
	batches      [][]SIEMEvent
	observedErrs []error
}

func (m *contextCheckingMockExporter) Export(ctx context.Context, events []SIEMEvent) error {
	err := ctx.Err()

	m.mu.Lock()
	defer m.mu.Unlock()
	m.observedErrs = append(m.observedErrs, err)
	if err != nil {
		return fmt.Errorf("audit export: %w", err)
	}
	cp := make([]SIEMEvent, len(events))
	copy(cp, events)
	m.batches = append(m.batches, cp)
	return nil
}

func (m *contextCheckingMockExporter) Close() error { return nil }

func (m *contextCheckingMockExporter) totalEvents() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, b := range m.batches {
		n += len(b)
	}
	return n
}

func (m *contextCheckingMockExporter) cancelledContextCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, err := range m.observedErrs {
		if err != nil {
			n++
		}
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

func TestBufferedExporter_DrainOnClose_ContextAware(t *testing.T) {
	mock := &contextCheckingMockExporter{}
	buf := NewBufferedExporter(mock, WithBatchSize(100), WithFlushInterval(10*time.Second))

	for i := 0; i < 7; i++ {
		buf.Send(SIEMEvent{Action: "drain-context-aware"})
	}
	if err := buf.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := mock.totalEvents(); got != 7 {
		t.Fatalf("total events after context-aware drain = %d, want 7", got)
	}
	if got := mock.cancelledContextCalls(); got != 0 {
		t.Fatalf("export calls with cancelled context = %d, want 0", got)
	}
}

// deadlineRecordingExporter blocks until its context ends and records the
// first call's start time and context deadline plus the total call count.
type deadlineRecordingExporter struct {
	mu       sync.Mutex
	calls    int
	callAt   time.Time
	deadline time.Time
	ok       bool
}

func (d *deadlineRecordingExporter) Export(ctx context.Context, _ []SIEMEvent) error {
	d.mu.Lock()
	d.calls++
	if d.calls == 1 {
		d.callAt = time.Now()
		d.deadline, d.ok = ctx.Deadline()
	}
	d.mu.Unlock()
	<-ctx.Done()
	return ctx.Err()
}

func (d *deadlineRecordingExporter) Close() error { return nil }

func (d *deadlineRecordingExporter) snapshot() (callAt, deadline time.Time, ok bool, calls int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.callAt, d.deadline, d.ok, d.calls
}

func TestBufferedExporter_CloseLatencyBounded(t *testing.T) {
	exporter := &deadlineRecordingExporter{}
	drainTimeout := 300 * time.Millisecond
	buf := NewBufferedExporter(exporter,
		WithBatchSize(100),
		WithFlushInterval(10*time.Second),
		WithDrainTimeout(drainTimeout),
	)
	for i := 0; i < 3; i++ {
		buf.Send(SIEMEvent{Action: "blocked-drain"})
	}

	// The bounds below are deterministic under CPU load: the deadline gap is
	// computed from the export context (it can only shrink when the scheduler
	// stalls), the call count is checked after Close returned, and the liveness
	// bound is half an export timeout. A drain that ignores WithDrainTimeout, or
	// retries that ignore the drain context, break the gap, the count or the bound.
	closeWithin := func(label string) error {
		t.Helper()
		done := make(chan error, 1)
		go func() { done <- buf.Close() }()
		select {
		case err := <-done:
			return err
		case <-time.After(defaultExportTimeout / 2):
			t.Fatalf("%s still blocked after %s: drain is not bounded by WithDrainTimeout", label, defaultExportTimeout/2)
			return nil
		}
	}
	if err := closeWithin("Close"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	callAt, deadline, ok, calls := exporter.snapshot()
	if calls == 0 || !ok {
		t.Fatalf("expected a deadline-bound Export call during drain, calls=%d deadline ok=%v", calls, ok)
	}
	if gap := deadline.Sub(callAt); gap > drainTimeout {
		t.Fatalf("first Export deadline is %s after the call, want <= WithDrainTimeout %s", gap, drainTimeout)
	}
	if calls != 1 {
		t.Fatalf("Export calls = %d, want 1: retries must not reach the exporter after the drain context expires", calls)
	}

	if err := closeWithin("second Close"); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, _, _, after := exporter.snapshot(); after != calls {
		t.Fatalf("second Close changed Export calls from %d to %d", calls, after)
	}
}

func TestBufferedExporter_CloseEmptyQueueFast(t *testing.T) {
	mock := &contextCheckingMockExporter{}
	buf := NewBufferedExporter(mock,
		WithBatchSize(100),
		WithFlushInterval(10*time.Second),
		WithDrainTimeout(300*time.Millisecond),
	)

	start := time.Now()
	if err := buf.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("Close empty queue elapsed %s, want <= 100ms", elapsed)
	}
	if got := mock.totalEvents(); got != 0 {
		t.Fatalf("total events after empty close = %d, want 0", got)
	}
}

func TestBufferedExporter_SendAfterCloseDrops(t *testing.T) {
	mock := &contextCheckingMockExporter{}
	buf := NewBufferedExporter(mock, WithBatchSize(100), WithFlushInterval(10*time.Second))
	if err := buf.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	for i := 0; i < 3; i++ {
		buf.Send(SIEMEvent{Action: "after-close"})
	}
	if got := len(buf.ch); got != 0 {
		t.Fatalf("buffer len after Send on closed exporter = %d, want 0", got)
	}
	if got := mock.totalEvents(); got != 0 {
		t.Fatalf("exported events after Send on closed exporter = %d, want 0", got)
	}
}

func TestBufferedExporter_RetryOnFailure(t *testing.T) {
	mock := &mockExporter{failNext: 2} // fail twice, succeed on third
	buf := NewBufferedExporter(mock, WithBatchSize(1), WithFlushInterval(10*time.Second), WithRetryBackoff(10*time.Millisecond))

	buf.Send(SIEMEvent{Action: "retry-test"})

	// Wait for retries (10ms + 20ms backoff with test-speed backoff)
	time.Sleep(200 * time.Millisecond)

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
	mock := &mockExporter{failNext: defaultMaxRetries, exportCalled: called}
	buf := &BufferedExporter{exporter: mock}
	before := testutil.ToFloat64(auditExportBacklog)
	// This direct retry call represents one already accepted event.
	auditExportBacklog.Inc()

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
	require.Equal(t, before, testutil.ToFloat64(auditExportBacklog))
}

type backlogBlockingExporter struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *backlogBlockingExporter) Export(ctx context.Context, _ []SIEMEvent) error {
	b.once.Do(func() { close(b.started) })
	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *backlogBlockingExporter) Close() error { return nil }

func TestBufferedExporterExportBacklog(t *testing.T) {
	t.Run("queued_and_in_flight", testBufferedExporterPendingBacklog)
	t.Run("retries_exhausted", testBufferedExporterDroppedBacklog)
	t.Run("no_exporter", testBufferedExporterMissingBacklog)
}

func testBufferedExporterPendingBacklog(t *testing.T) {
	t.Setenv("CORDUM_AUDIT_BUFFER_SIZE", "2")
	before := testutil.ToFloat64(auditExportBacklog)
	exp := &backlogBlockingExporter{started: make(chan struct{}), release: make(chan struct{})}
	buf := NewBufferedExporter(exp, WithBatchSize(1), WithFlushInterval(time.Hour))
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(exp.release) }) }
	t.Cleanup(func() { release(); require.NoError(t, buf.Close()) })
	buf.Send(testEvent())
	select {
	case <-exp.started:
	case <-time.After(2 * time.Second):
		t.Fatal("export did not start")
	}
	buf.Send(testEvent())
	buf.Send(testEvent())
	require.Equal(t, before+3, testutil.ToFloat64(auditExportBacklog))
	buf.Send(testEvent()) // Full channel: the rejected event must not remain counted.
	require.Equal(t, before+3, testutil.ToFloat64(auditExportBacklog))
	release()
	require.Eventually(t, func() bool { return testutil.ToFloat64(auditExportBacklog) == before }, 2*time.Second, time.Millisecond)
	require.NoError(t, buf.Close())
	buf.Send(testEvent())
	require.Equal(t, before, testutil.ToFloat64(auditExportBacklog), "closed sends do not add backlog")
}

func testBufferedExporterDroppedBacklog(t *testing.T) {
	t.Setenv("CORDUM_AUDIT_EXPORT_MAX_RETRIES", "3")
	before := testutil.ToFloat64(auditExportBacklog)
	drops := testutil.ToFloat64(auditBatchDrops)
	buf := NewBufferedExporter(&alwaysFailExporter{}, WithBatchSize(1), WithFlushInterval(time.Hour), WithRetryBackoff(time.Millisecond))
	t.Cleanup(func() { require.NoError(t, buf.Close()) })
	buf.Send(testEvent())
	require.Eventually(t, func() bool { return testutil.ToFloat64(auditBatchDrops) == drops+1 }, 2*time.Second, time.Millisecond)
	require.Eventually(t, func() bool { return testutil.ToFloat64(auditExportBacklog) == before }, 2*time.Second, time.Millisecond)
}

func testBufferedExporterMissingBacklog(t *testing.T) {
	before := testutil.ToFloat64(auditExportBacklog)
	buf := NewBufferedExporter(nil, WithBatchSize(100), WithFlushInterval(time.Hour))
	t.Cleanup(func() { require.NoError(t, buf.Close()) })
	buf.Send(testEvent())
	require.Equal(t, before+1, testutil.ToFloat64(auditExportBacklog))
	require.NoError(t, buf.Close())
	require.Equal(t, before, testutil.ToFloat64(auditExportBacklog))
}
