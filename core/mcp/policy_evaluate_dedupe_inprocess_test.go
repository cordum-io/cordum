package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordum/cordum/core/edge"
)

// erroringUpstreamCaller returns a configured error on every call so we
// can exercise the error-path delete contract in dedupeFinish.
type erroringUpstreamCaller struct {
	count atomic.Int32
	err   error
}

func (e *erroringUpstreamCaller) Invoke(_ context.Context, _ ToolCallParams) (*ToolCallResult, error) {
	e.count.Add(1)
	return nil, e.err
}

type blockingPanicUpstreamCaller struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *blockingPanicUpstreamCaller) Invoke(context.Context, ToolCallParams) (*ToolCallResult, error) {
	p.once.Do(func() { close(p.entered) })
	<-p.release
	panic("panic from upstream")
}

type loadedSignalDedupeStore struct {
	DedupeStore
	loaded chan struct{}
	once   sync.Once
}

func (s *loadedSignalDedupeStore) LoadOrStore(key string, value any) (any, bool) {
	actual, loaded := s.DedupeStore.LoadOrStore(key, value)
	if loaded {
		s.once.Do(func() { close(s.loaded) })
	}
	return actual, loaded
}

type deleteGateDedupeStore struct {
	inner              DedupeStore
	deleteEntered      chan struct{}
	releaseDelete      chan struct{}
	loadedDuringDelete chan struct{}
	deleteOnce         sync.Once
	releaseOnce        sync.Once
	loadedOnce         sync.Once
}

func newDeleteGateDedupeStore(inner DedupeStore) *deleteGateDedupeStore {
	return &deleteGateDedupeStore{
		inner:              inner,
		deleteEntered:      make(chan struct{}),
		releaseDelete:      make(chan struct{}),
		loadedDuringDelete: make(chan struct{}),
	}
}

func (s *deleteGateDedupeStore) LoadOrStore(key string, value any) (any, bool) {
	actual, loaded := s.inner.LoadOrStore(key, value)
	if loaded {
		select {
		case <-s.deleteEntered:
			s.loadedOnce.Do(func() { close(s.loadedDuringDelete) })
		default:
		}
	}
	return actual, loaded
}

func (s *deleteGateDedupeStore) Store(key string, value any) {
	s.inner.Store(key, value)
}

func (s *deleteGateDedupeStore) Delete(key string) {
	s.deleteOnce.Do(func() {
		close(s.deleteEntered)
		<-s.releaseDelete
	})
	s.inner.Delete(key)
}

func (s *deleteGateDedupeStore) release() {
	s.releaseOnce.Do(func() { close(s.releaseDelete) })
}

func TestPolicyEvaluate_DedupeFinishOnPanic(t *testing.T) {
	upstream := &blockingPanicUpstreamCaller{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	store := &loadedSignalDedupeStore{
		DedupeStore: NewInProcessDedupeStore(),
		loaded:      make(chan struct{}),
	}
	deps := newToolCallDepsFixture(&fakePolicyDispatcher{}, &fakeEventEmitter{}, &fakeArtifactStore{})
	deps.Upstream = upstream
	deps.DedupeState = store
	ctx := newAuthedToolCallCtx()
	params := ToolCallParams{Name: "fs.read_file", Arguments: json.RawMessage(`{"path":"/tmp/a"}`)}

	recovered := make(chan any, 1)
	go invokeAndRecoverPanic(ctx, deps, params, recovered)
	waitForPanicTestSignal(t, upstream.entered)

	loserErr := make(chan error, 1)
	go func() {
		_, err := InvokeToolWithPolicy(ctx, deps, params, "local-fs")
		loserErr <- err
	}()
	waitForPanicTestSignal(t, store.loaded)
	close(upstream.release)

	if rec := <-recovered; rec == nil {
		t.Fatal("InvokeToolWithPolicy did not propagate upstream panic")
	}
	err := <-loserErr
	if err == nil || !strings.Contains(err.Error(), "panic from upstream") {
		t.Fatalf("dedupe loser error = %v, want panic failure", err)
	}
}

func invokeAndRecoverPanic(ctx context.Context, deps ToolCallDeps, params ToolCallParams, recovered chan<- any) {
	defer func() { recovered <- recover() }()
	_, _ = InvokeToolWithPolicy(ctx, deps, params, "local-fs")
}

func waitForPanicTestSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for panic test synchronization")
	}
}

// TestInProcessDedupeStore_RetryCollapses asserts the in-process store
// satisfies the same retry-idempotent contract the old `*sync.Map`
// satisfied: 5 sequential retries of the same logical call collapse to
// one upstream invocation + one pre+post pair via the DedupeStore
// abstraction. This is the (a)+(b) coverage from the plan.
func TestInProcessDedupeStore_RetryCollapses(t *testing.T) {
	t.Parallel()
	pipeline := &fakePolicyDispatcher{}
	emitter := &fakeEventEmitter{}
	upstream := &fakeUpstreamToolCaller{
		result: &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}},
	}
	deps := newToolCallDepsFixture(pipeline, emitter, &fakeArtifactStore{})
	deps.Upstream = upstream
	deps.DedupeState = NewInProcessDedupeStore()

	ctx := newAuthedToolCallCtx()
	params := ToolCallParams{
		Name:      "fs.read_file",
		Arguments: json.RawMessage(`{"path":"/etc/hostname"}`),
	}
	for i := 0; i < 5; i++ {
		if _, err := InvokeToolWithPolicy(ctx, deps, params, "local-fs"); err != nil {
			t.Fatalf("retry %d: %v", i, err)
		}
	}
	if upstream.calls != 1 {
		t.Fatalf("upstream invoked %d; want 1 (in-process store must dedupe)", upstream.calls)
	}
	if len(emitter.events) != 2 {
		t.Fatalf("emitter saw %d events; want 2 (pre+post)", len(emitter.events))
	}
}

// TestInProcessDedupeStore_ErrorDeletesEntry asserts (c): when the
// upstream tool returns an error, the dedupe entry MUST be deleted so
// the next retry fires a fresh upstream call instead of receiving a
// sticky cached error forever. Without delete-on-error, a transient
// 503 would permanently poison the dedupe slot for the lifetime of the
// process.
func TestInProcessDedupeStore_ErrorDeletesEntry(t *testing.T) {
	t.Parallel()
	pipeline := &fakePolicyDispatcher{}
	emitter := &fakeEventEmitter{}
	upstream := &erroringUpstreamCaller{err: errors.New("transient_503")}
	deps := newToolCallDepsFixture(pipeline, emitter, &fakeArtifactStore{})
	deps.Upstream = upstream
	deps.DedupeState = NewInProcessDedupeStore()

	ctx := newAuthedToolCallCtx()
	params := ToolCallParams{
		Name:      "fs.read_file",
		Arguments: json.RawMessage(`{"path":"/etc/hostname"}`),
	}

	// 3 sequential retries on a forever-failing upstream — each MUST
	// reach upstream because the prior error deleted the dedupe slot.
	for i := 0; i < 3; i++ {
		_, _ = InvokeToolWithPolicy(ctx, deps, params, "local-fs")
	}
	if got := int(upstream.count.Load()); got != 3 {
		t.Fatalf("upstream invoked %d times across 3 retries on error; want 3 (errors must NOT cache)", got)
	}
}

func TestInProcessDedupe_ErrorPath_NewArrivalDoesNotInheritStaleError(t *testing.T) {
	t.Parallel()
	transientErr := errors.New("transient_upstream_timeout")
	store := newDeleteGateDedupeStore(NewInProcessDedupeStore())
	defer store.release()
	deps := ToolCallDeps{DedupeState: store}
	const key = "dedupe-error-race"

	winner, outcome := dedupeBegin(context.Background(), deps, key)
	if winner == nil || outcome != nil {
		t.Fatalf("initial dedupeBegin = winner:%v outcome:%v; want first caller to win", winner, outcome)
	}

	finishDone := make(chan struct{})
	go func() {
		defer close(finishDone)
		dedupeFinish(deps, key, winner, nil, transientErr)
	}()

	select {
	case <-store.deleteEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("dedupeFinish did not enter the error-path delete gate")
	}

	arrivalCtx, cancelArrival := context.WithCancel(context.Background())
	defer cancelArrival()
	arrivalDone := make(chan *dedupeOutcome, 1)
	go func() {
		freshWinner, arrivedOutcome := dedupeBegin(arrivalCtx, deps, key)
		if freshWinner != nil {
			dedupeFinish(deps, key, freshWinner, &ToolCallResult{}, nil)
		}
		arrivalDone <- arrivedOutcome
	}()

	select {
	case <-store.loadedDuringDelete:
	case <-time.After(2 * time.Second):
		t.Fatal("new arrival did not observe the in-process entry while delete was gated")
	}

	select {
	case arrivedOutcome := <-arrivalDone:
		if arrivedOutcome != nil && errors.Is(arrivedOutcome.err, transientErr) {
			t.Fatalf("new arrival inherited stale transient error during close-before-delete gap: %v", arrivedOutcome.err)
		}
	case <-time.After(200 * time.Millisecond):
		cancelArrival()
		arrivedOutcome := <-arrivalDone
		if arrivedOutcome == nil || !errors.Is(arrivedOutcome.err, context.Canceled) {
			t.Fatalf("arrival while delete was gated returned %+v; want context cancellation without stale transient error", arrivedOutcome)
		}
	}

	store.release()
	select {
	case <-finishDone:
	case <-time.After(2 * time.Second):
		t.Fatal("dedupeFinish did not complete after releasing delete gate")
	}

	freshWinner, staleOutcome := dedupeBegin(context.Background(), deps, key)
	if freshWinner == nil || staleOutcome != nil {
		t.Fatalf("post-error retry = winner:%v outcome:%v; want a fresh winner after delete", freshWinner, staleOutcome)
	}
	dedupeFinish(deps, key, freshWinner, &ToolCallResult{}, nil)
}

// TestInProcessDedupeStore_ContextCancelReleasesWaiter asserts (d): a
// waiter blocked on an in-flight singleflight slot whose ctx is
// cancelled MUST return promptly with ctx.Err() — it must not block
// indefinitely waiting for the winner's upstream call to complete.
// This is the SIGTERM-bound caller contract.
func TestInProcessDedupeStore_ContextCancelReleasesWaiter(t *testing.T) {
	t.Parallel()
	pipeline := &fakePolicyDispatcher{}
	emitter := &atomicEventEmitter{}
	upstream := &blockingUpstreamCaller{
		result: &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}},
		gate:   make(chan struct{}),
	}
	store := NewInProcessDedupeStore()
	deps := ToolCallDeps{
		Pipeline:      pipeline,
		EventEmitter:  emitter,
		Redactor:      DefaultRedactor(),
		ArtifactStore: &fakeArtifactStore{},
		Upstream:      upstream,
		Clock:         func() time.Time { return time.Date(2026, 5, 15, 17, 0, 0, 0, time.UTC) },
		DedupeState:   store,
	}

	ctxWinner := newAuthedToolCallCtx()
	ctxWaiter, cancelWaiter := context.WithCancel(newAuthedToolCallCtx())
	defer cancelWaiter()
	params := ToolCallParams{
		Name:      "fs.read_file",
		Arguments: json.RawMessage(`{"path":"/etc/hostname"}`),
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Winner blocks on the upstream gate forever (in this test).
		_, _ = InvokeToolWithPolicy(ctxWinner, deps, params, "local-fs")
	}()
	// Give the winner a moment to take the slot.
	time.Sleep(20 * time.Millisecond)

	waiterDone := make(chan struct{})
	go func() {
		// This caller blocks on the winner's done channel via dedupeBegin.
		_, _ = InvokeToolWithPolicy(ctxWaiter, deps, params, "local-fs")
		close(waiterDone)
	}()
	// Give the waiter a moment to enter the wait.
	time.Sleep(20 * time.Millisecond)
	cancelWaiter()

	select {
	case <-waiterDone:
	case <-time.After(2 * time.Second):
		t.Fatal("waiter did not return promptly after ctx cancel; dedupeBegin must respect ctx.Done()")
	}

	// Release the winner so the test cleans up.
	close(upstream.gate)
	wgDone := make(chan struct{})
	go func() { wg.Wait(); close(wgDone) }()
	select {
	case <-wgDone:
	case <-time.After(2 * time.Second):
		t.Fatal("winner did not return after gate release")
	}
}

// TestInProcessDedupeStore_DirectUnitContract is a direct unit test of
// the abstraction itself: LoadOrStore is atomic check-write; Store
// overwrites; Delete clears. Locks the contract independently of the
// InvokeToolWithPolicy flow so a future refactor that swaps the
// implementation cannot silently regress the semantics.
func TestInProcessDedupeStore_DirectUnitContract(t *testing.T) {
	t.Parallel()
	store := NewInProcessDedupeStore()
	type marker struct{ id int }

	// LoadOrStore on empty: not loaded.
	first := &marker{id: 1}
	actual, loaded := store.LoadOrStore("k1", first)
	if loaded {
		t.Fatalf("first LoadOrStore reports loaded=true on empty store")
	}
	if got, ok := actual.(*marker); !ok || got.id != 1 {
		t.Fatalf("first LoadOrStore returned %+v; want *marker{id:1}", actual)
	}

	// LoadOrStore on populated key: loaded with the original value.
	second := &marker{id: 2}
	actual, loaded = store.LoadOrStore("k1", second)
	if !loaded {
		t.Fatalf("second LoadOrStore reports loaded=false; want true (key already present)")
	}
	if got, ok := actual.(*marker); !ok || got.id != 1 {
		t.Fatalf("second LoadOrStore returned %+v; want the FIRST value (id=1)", actual)
	}

	// Store overwrites.
	store.Store("k1", second)
	actual, loaded = store.LoadOrStore("k1", &marker{id: 3})
	if !loaded {
		t.Fatalf("LoadOrStore after Store reports loaded=false")
	}
	if got, ok := actual.(*marker); !ok || got.id != 2 {
		t.Fatalf("Store did not overwrite: got %+v want id=2", actual)
	}

	// Delete clears.
	store.Delete("k1")
	actual, loaded = store.LoadOrStore("k1", &marker{id: 4})
	if loaded {
		t.Fatalf("LoadOrStore after Delete reports loaded=true; want false")
	}
	if got, ok := actual.(*marker); !ok || got.id != 4 {
		t.Fatalf("LoadOrStore after Delete returned %+v; want id=4", actual)
	}
}

// ensure imports referenced in this file stay alive even if a future
// edit drops one.
var _ = edge.EventKindMCPToolPre
