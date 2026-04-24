package scheduler

// Regression coverage for task-7a2514ae: when a worker transitions
// offline→online, the scheduler must flush pending dispatch for that
// worker's pool rather than waiting for the next poll tick. Tests
// exercise the engine-side heartbeat handler + flush scheduler + per-
// pool debounce latch, with a swap-in flushDispatchFn so no real
// dispatch pipeline is exercised.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// flushSpy records calls to the engine's injected flushDispatchFn.
type flushSpy struct {
	mu        sync.Mutex
	calls     []flushCall
	dispatchN int // number of jobs pretended-dispatched per call
}

type flushCall struct {
	pool    string
	traceID string
}

func (s *flushSpy) fn(_ context.Context, pool, traceID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, flushCall{pool: pool, traceID: traceID})
	return s.dispatchN
}

func (s *flushSpy) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *flushSpy) poolsCalled() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	pools := make([]string, 0, len(s.calls))
	for _, c := range s.calls {
		pools = append(pools, c.pool)
	}
	return pools
}

// flushMetricsSpy records IncDispatchFlushOnWorkerOnline invocations;
// implements the full scheduler.Metrics interface with no-op defaults.
type flushMetricsSpy struct {
	flushCount atomic.Int64
	flushPools sync.Map // pool string → *atomic.Int64
}

func (m *flushMetricsSpy) IncDispatchFlushOnWorkerOnline(pool string) {
	m.flushCount.Add(1)
	if v, ok := m.flushPools.Load(pool); ok {
		v.(*atomic.Int64).Add(1)
		return
	}
	// Create an empty counter for LoadOrStore; only the winner's counter
	// is incremented. Pre-incrementing would lose the loser's increment
	// when it discards its local cnt.
	var cnt atomic.Int64
	actual, loaded := m.flushPools.LoadOrStore(pool, &cnt)
	if loaded {
		actual.(*atomic.Int64).Add(1)
	} else {
		cnt.Add(1)
	}
}

func (m *flushMetricsSpy) poolCount(pool string) int64 {
	if v, ok := m.flushPools.Load(pool); ok {
		return v.(*atomic.Int64).Load()
	}
	return 0
}

// Noop implementations to satisfy the Metrics interface.
func (m *flushMetricsSpy) IncJobsReceived(string)                            {}
func (m *flushMetricsSpy) IncJobsDispatched(string)                          {}
func (m *flushMetricsSpy) IncJobsCompleted(string, string)                   {}
func (m *flushMetricsSpy) IncSafetyDenied(string)                            {}
func (m *flushMetricsSpy) IncSafetyUnavailable(string)                       {}
func (m *flushMetricsSpy) IncOutputPolicyChecked(string)                     {}
func (m *flushMetricsSpy) IncOutputPolicyQuarantined(string)                 {}
func (m *flushMetricsSpy) IncOutputPolicySkipped(string)                     {}
func (m *flushMetricsSpy) IncAsyncOutputTimeout(string)                      {}
func (m *flushMetricsSpy) IncOutputEvaluations(string)                       {}
func (m *flushMetricsSpy) IncOutputDenials(string)                           {}
func (m *flushMetricsSpy) IncOutputRedactions(string)                        {}
func (m *flushMetricsSpy) IncOrphanReplayed(string)                          {}
func (m *flushMetricsSpy) ObserveJobLockWait(float64)                        {}
func (m *flushMetricsSpy) ObserveDispatchLatency(string, float64)            {}
func (m *flushMetricsSpy) ObserveOutputCheckLatency(string, string, float64) {}
func (m *flushMetricsSpy) ObserveOutputEvalDuration(string, float64)         {}
func (m *flushMetricsSpy) SetActiveGoroutines(int)                           {}
func (m *flushMetricsSpy) SetStaleJobs(string, int)                          {}
func (m *flushMetricsSpy) IncDLQEmitFailure(string)                          {}
func (m *flushMetricsSpy) IncJobCancelFailures()                             {}
func (m *flushMetricsSpy) IncValidationRejections()                          {}
func (m *flushMetricsSpy) IncInputFailOpen(string)                           {}
func (m *flushMetricsSpy) IncJobLockAbandoned()                              {}
func (m *flushMetricsSpy) IncResultPtrWriteFailure()                         {}
func (m *flushMetricsSpy) IncDispatchRollback(string)                        {}

// newFlushOnOnlineTestEngine wires an Engine with a flushSpy and a
// metrics spy ready for the heartbeat-flush tests.
func newFlushOnOnlineTestEngine(t *testing.T) (*Engine, *flushSpy, *flushMetricsSpy, *MemoryRegistry) {
	t.Helper()
	reg := newTestRegistry(t)
	metrics := &flushMetricsSpy{}
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), reg, NewNaiveStrategy(), newFakeJobStore(), metrics)
	spy := &flushSpy{dispatchN: 1}
	engine.WithFlushDispatchFn(spy.fn)
	return engine, spy, metrics, reg
}

func newFlushHeartbeatPacket(workerID, pool string) *pb.BusPacket {
	return &pb.BusPacket{
		SenderId:        workerID,
		TraceId:         "trace-" + workerID,
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		CreatedAt:       timestamppb.Now(),
		Payload: &pb.BusPacket_Heartbeat{
			Heartbeat: &pb.Heartbeat{
				WorkerId: workerID,
				Pool:     pool,
				Type:     "cpu",
			},
		},
	}
}

// waitUntil polls cond up to timeout; returns true on success. No
// wall-clock sleep in the hot path: polls at 1ms granularity.
func waitUntil(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return cond()
}

func TestHeartbeatFlushOnOnlineTransition_CallsFlushOnce(t *testing.T) {
	t.Parallel()
	engine, spy, metrics, _ := newFlushOnOnlineTestEngine(t)

	if err := engine.HandlePacket(newFlushHeartbeatPacket("worker-1", "test")); err != nil {
		t.Fatalf("handle packet: %v", err)
	}
	if !waitUntil(200*time.Millisecond, func() bool { return spy.callCount() == 1 }) {
		t.Fatalf("expected 1 flush call, got %d", spy.callCount())
	}
	if got := spy.poolsCalled(); len(got) != 1 || got[0] != "test" {
		t.Fatalf("expected flush for pool=test, got %v", got)
	}
	if !waitUntil(200*time.Millisecond, func() bool { return metrics.poolCount("test") == 1 }) {
		t.Fatalf("expected 1 metric increment for pool=test, got %d", metrics.poolCount("test"))
	}
}

func TestHeartbeatFlushOnOnlineTransition_NoFlushOnRefresh(t *testing.T) {
	t.Parallel()
	engine, spy, _, _ := newFlushOnOnlineTestEngine(t)

	for i := 0; i < 2; i++ {
		if err := engine.HandlePacket(newFlushHeartbeatPacket("worker-refresh", "test")); err != nil {
			t.Fatalf("handle packet %d: %v", i, err)
		}
	}
	// Give any spurious second flush a chance to land.
	if !waitUntil(200*time.Millisecond, func() bool { return spy.callCount() >= 1 }) {
		t.Fatalf("expected at least 1 flush call, got %d", spy.callCount())
	}
	// Wait a beat more to catch a (wrongly-fired) second flush.
	time.Sleep(50 * time.Millisecond)
	if got := spy.callCount(); got != 1 {
		t.Fatalf("expected exactly 1 flush call (only first HB is a transition), got %d", got)
	}
}

func TestHeartbeatFlushOnOnlineTransition_ConcurrentWorkersDebounced(t *testing.T) {
	t.Parallel()
	engine, spy, _, _ := newFlushOnOnlineTestEngine(t)
	// Block the flush function so overlapping heartbeats collide on the
	// per-pool latch instead of racing to serial completion.
	release := make(chan struct{})
	engine.WithFlushDispatchFn(func(ctx context.Context, pool, traceID string) int {
		spy.mu.Lock()
		spy.calls = append(spy.calls, flushCall{pool: pool, traceID: traceID})
		spy.mu.Unlock()
		<-release
		return 1
	})

	for _, w := range []string{"w-a", "w-b", "w-c"} {
		if err := engine.HandlePacket(newFlushHeartbeatPacket(w, "test")); err != nil {
			t.Fatalf("handle packet %s: %v", w, err)
		}
	}
	if !waitUntil(200*time.Millisecond, func() bool { return spy.callCount() >= 1 }) {
		t.Fatal("no flush observed within budget")
	}
	close(release)
	time.Sleep(50 * time.Millisecond)
	if got := spy.callCount(); got != 1 {
		t.Fatalf("expected exactly 1 flush (per-pool latch debounces concurrent online transitions), got %d", got)
	}
}

func TestHeartbeatFlushOnOnlineTransition_EmptyPoolSkipped(t *testing.T) {
	t.Parallel()
	engine, spy, metrics, _ := newFlushOnOnlineTestEngine(t)

	if err := engine.HandlePacket(newFlushHeartbeatPacket("worker-nopool", "")); err != nil {
		t.Fatalf("handle packet: %v", err)
	}
	// Give a spurious flush a chance to fire before asserting absence.
	time.Sleep(50 * time.Millisecond)
	if got := spy.callCount(); got != 0 {
		t.Fatalf("empty pool must not trigger flush, got %d calls", got)
	}
	if got := metrics.flushCount.Load(); got != 0 {
		t.Fatalf("empty pool must not trigger metric, got %d", got)
	}
}

func TestHeartbeatFlushOnOnlineTransition_NoMetricWhenZeroDispatched(t *testing.T) {
	// Observability invariant: the log always fires but the metric only
	// increments when real work was dispatched (otherwise the counter
	// would be dominated by no-op flushes on idle pools).
	t.Parallel()
	engine, spy, metrics, _ := newFlushOnOnlineTestEngine(t)
	spy.dispatchN = 0

	if err := engine.HandlePacket(newFlushHeartbeatPacket("worker-idle", "idlepool")); err != nil {
		t.Fatalf("handle packet: %v", err)
	}
	if !waitUntil(200*time.Millisecond, func() bool { return spy.callCount() == 1 }) {
		t.Fatalf("expected 1 flush call, got %d", spy.callCount())
	}
	time.Sleep(30 * time.Millisecond)
	if got := metrics.flushCount.Load(); got != 0 {
		t.Fatalf("metric must NOT increment when dispatchN=0, got %d", got)
	}
}
