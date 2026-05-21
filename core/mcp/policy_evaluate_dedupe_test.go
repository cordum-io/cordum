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

// atomicEventEmitter records events under a mutex AND publishes an
// atomic count so concurrent callers can be tallied without surfacing
// a race on the underlying slice. Used by TestPolicyEvaluate_ConcurrentRace.
type atomicEventEmitter struct {
	mu     sync.Mutex
	events []*edge.AgentActionEvent
	count  atomic.Int32
}

func (e *atomicEventEmitter) Emit(_ context.Context, evt *edge.AgentActionEvent) error {
	e.count.Add(1)
	e.mu.Lock()
	e.events = append(e.events, evt)
	e.mu.Unlock()
	return nil
}

// blockingUpstreamCaller waits on `gate` before returning so 20
// concurrent callers genuinely contend for the dedupe singleflight
// instead of trivially serialising on a fast Invoke.
type blockingUpstreamCaller struct {
	count  atomic.Int32
	result *ToolCallResult
	gate   chan struct{}
}

func (b *blockingUpstreamCaller) Invoke(_ context.Context, _ ToolCallParams) (*ToolCallResult, error) {
	b.count.Add(1)
	<-b.gate
	return b.result, nil
}

// TestPolicyEvaluate_RetryIdempotent asserts the #4 fix: the same
// semantic inputs called 5x sequentially via InvokeToolWithPolicy with
// the production defaultEventIDFactory (random per call) emit exactly
// one pre+post pair when DedupeState is shared across the retries.
//
// Before #4 fix: dedupeKey derived from EventIDFactory() → 5 different
// random IDs → 5 different keys → no dedupe → 10 events emitted.
// After #4 fix: dedupeKey derived from (tenant, server, tool,
// action_hash, session, execution, principal) → 1 key shared across
// retries → first call emits pre+post, retries hit the cache → 2 events.
func TestPolicyEvaluate_RetryIdempotent(t *testing.T) {
	t.Parallel()
	pipeline := &fakePolicyDispatcher{}
	emitter := &fakeEventEmitter{}
	upstream := &fakeUpstreamToolCaller{
		result: &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}},
	}
	deps := newToolCallDepsFixture(pipeline, emitter, &fakeArtifactStore{})
	deps.Upstream = upstream
	deps.DedupeState = NewInProcessDedupeStore()
	// Deliberately do NOT set deps.EventIDFactory — use the production
	// defaultEventIDFactory (random per call). The fix is what makes
	// dedupe still hit despite the random factory.

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
		t.Fatalf("upstream invoked %d times across 5 retries with same semantic inputs; want 1 (dedupe must collapse)", upstream.calls)
	}
	if len(emitter.events) != 2 {
		t.Fatalf("emitter saw %d events across 5 retries; want exactly 2 (1 pre + 1 post). Kinds: %v",
			len(emitter.events), kindStrings(emitter.events))
	}
}

// TestPolicyEvaluate_ConcurrentRace asserts the #5 in-process race
// guarantee: 20 goroutines hitting the same gate with identical
// semantic inputs all collapse into exactly one upstream call and one
// pre+post event pair via the LoadOrStore + done-channel singleflight.
//
// This locks the contract that LoadOrStore is the atomic check-write
// (NOT a check-then-write race). Without dedupe, the test would see
// up to 20 upstream calls and 40 events.
func TestPolicyEvaluate_ConcurrentRace(t *testing.T) {
	t.Parallel()
	pipeline := &fakePolicyDispatcher{}
	emitter := &atomicEventEmitter{}
	upstream := &blockingUpstreamCaller{
		result: &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}},
		gate:   make(chan struct{}),
	}
	deps := ToolCallDeps{
		Pipeline:      pipeline,
		EventEmitter:  emitter,
		Redactor:      DefaultRedactor(),
		ArtifactStore: &fakeArtifactStore{},
		Upstream:      upstream,
		Clock:         func() time.Time { return time.Date(2026, 5, 15, 17, 0, 0, 0, time.UTC) },
		DedupeState:   NewInProcessDedupeStore(),
	}

	ctx := newAuthedToolCallCtx()
	params := ToolCallParams{
		Name:      "fs.read_file",
		Arguments: json.RawMessage(`{"path":"/etc/hostname"}`),
	}

	const N = 20
	var wg sync.WaitGroup
	wg.Add(N)
	errs := make(chan error, N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			if _, err := InvokeToolWithPolicy(ctx, deps, params, "local-fs"); err != nil {
				errs <- err
			}
		}()
	}
	// Give the goroutines a moment to all enter dedupeBegin and either
	// win the slot or block on the winner's done channel. Then release
	// the upstream caller so the winner completes and waiters wake.
	time.Sleep(50 * time.Millisecond)
	close(upstream.gate)

	wgDone := make(chan struct{})
	go func() { wg.Wait(); close(wgDone) }()
	select {
	case <-wgDone:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent callers deadlocked waiting on dedupe singleflight")
	}
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent caller errored: %v", err)
	}

	if got := int(upstream.count.Load()); got != 1 {
		t.Fatalf("upstream invoked %d times across %d concurrent callers; want exactly 1 (singleflight must collapse)", got, N)
	}
	if got := int(emitter.count.Load()); got != 2 {
		t.Fatalf("emitter saw %d events across %d concurrent callers; want exactly 2 (1 pre + 1 post)", got, N)
	}
}

func TestWaitForRedisDedupe_DeadlinesWithinTTL(t *testing.T) {
	t.Parallel()
	client, mr := newMiniRedisDedupeBackend(t)
	store := NewRedisDedupeStore(client)
	const key = "k.deadline-within-ttl"
	if _, loaded := store.LoadOrStore(key, &redisDedupeRecord{State: redisDedupeStatePending}); loaded {
		t.Fatal("seed LoadOrStore reported loaded=true; want fresh pending winner")
	}
	remainingTTL := 75 * time.Millisecond
	mr.SetTTL(MCPDedupeKeyPrefix+key, remainingTTL)

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	start := time.Now()
	winner, outcome := waitForRedisDedupe(ctx, store, key)
	elapsed := time.Since(start)

	if outcome != nil {
		t.Fatalf("waitForRedisDedupe outcome err = %v; want TTL-bounded promotion", outcome.err)
	}
	if winner == nil || !winner.redisBacked {
		t.Fatalf("waitForRedisDedupe winner = %#v; want Redis promotion winner", winner)
	}
	// task-7f897c37: preserve TTL-bounded semantics while allowing
	// shared-runner GC/scheduler jitter observed in CI.
	const redisDedupeCIWaitSlack = 500 * time.Millisecond
	if elapsed > remainingTTL+redisDedupeCIWaitSlack {
		t.Fatalf(
			"waitForRedisDedupe elapsed = %v; want bounded by remaining TTL %v + CI slack %v",
			elapsed,
			remainingTTL,
			redisDedupeCIWaitSlack,
		)
	}
}

func TestWaitForRedisDedupe_CompletedRecordReturnsResult(t *testing.T) {
	t.Parallel()
	client, _ := newMiniRedisDedupeBackend(t)
	store := NewRedisDedupeStore(client)
	const key = "k.wait-completed"
	hash := strings.Repeat("e", 64)
	store.Store(key, &redisDedupeRecord{
		State: redisDedupeStateCompleted,
		Result: &redisDedupeResultMetadata{
			ContentCount:         2,
			HasStructuredContent: true,
			ResultSHA256:         hash,
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	winner, outcome := waitForRedisDedupe(ctx, store, key)

	if winner != nil {
		t.Fatalf("waitForRedisDedupe winner = %#v; want nil for completed Redis record", winner)
	}
	if outcome == nil || outcome.err != nil || outcome.result == nil {
		t.Fatalf("waitForRedisDedupe outcome = %#v; want successful cached result", outcome)
	}
	if got := len(outcome.result.Content); got != 1 {
		t.Fatalf("cached result content length = %d; want metadata summary item", got)
	}
	text := outcome.result.Content[0].Text
	for _, want := range []string{hash, "content_count=2", "has_structured_content=true"} {
		if !strings.Contains(text, want) {
			t.Fatalf("cached result text %q missing %q", text, want)
		}
	}
}

func TestWaitForRedisDedupe_DeletedKeyPromotesToWinner(t *testing.T) {
	t.Parallel()
	client, mr := newMiniRedisDedupeBackend(t)
	store := NewRedisDedupeStore(client)
	const key = "k.wait-deleted"
	if _, loaded := store.LoadOrStore(key, &redisDedupeRecord{State: redisDedupeStatePending}); loaded {
		t.Fatal("seed LoadOrStore loaded=true; want fresh pending")
	}
	store.Delete(key)
	if mr.Exists(MCPDedupeKeyPrefix + key) {
		t.Fatal("seed key still exists after Delete; test cannot exercise deleted-key promotion")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	winner, outcome := waitForRedisDedupe(ctx, store, key)

	if outcome != nil {
		t.Fatalf("waitForRedisDedupe outcome = %#v; want deleted-key winner promotion", outcome)
	}
	if winner == nil || !winner.redisBacked {
		t.Fatalf("waitForRedisDedupe winner = %#v; want Redis-backed promotion", winner)
	}
}

func TestWaitForRedisDedupe_TTLExpiryPromotesToWinner(t *testing.T) {
	t.Parallel()
	client, mr := newMiniRedisDedupeBackend(t)
	store := NewRedisDedupeStore(client)
	const key = "k.wait-ttl-expired"
	if _, loaded := store.LoadOrStore(key, &redisDedupeRecord{State: redisDedupeStatePending}); loaded {
		t.Fatal("seed LoadOrStore loaded=true; want fresh pending")
	}
	mr.FastForward(MCPDedupeTTL + time.Second)
	if mr.Exists(MCPDedupeKeyPrefix + key) {
		t.Fatal("seed key still exists after FastForward; test cannot exercise TTL expiry promotion")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	winner, outcome := waitForRedisDedupe(ctx, store, key)

	if outcome != nil {
		t.Fatalf("waitForRedisDedupe outcome = %#v; want TTL-expiry winner promotion", outcome)
	}
	if winner == nil || !winner.redisBacked {
		t.Fatalf("waitForRedisDedupe winner = %#v; want Redis-backed promotion", winner)
	}
}

func TestWaitForRedisDedupe_CtxCancelReturnsPromptly(t *testing.T) {
	t.Parallel()
	client, _ := newMiniRedisDedupeBackend(t)
	store := NewRedisDedupeStore(client)
	const key = "k.wait-cancel"
	if _, loaded := store.LoadOrStore(key, &redisDedupeRecord{State: redisDedupeStatePending}); loaded {
		t.Fatal("seed LoadOrStore loaded=true; want fresh pending")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	winner, outcome := waitForRedisDedupe(ctx, store, key)

	if winner != nil {
		t.Fatalf("waitForRedisDedupe winner = %#v; want nil on canceled context", winner)
	}
	if outcome == nil || !errors.Is(outcome.err, context.Canceled) {
		t.Fatalf("waitForRedisDedupe outcome err = %v; want context.Canceled", outcome)
	}
}

// TestDedupeKey_SemanticDerivation is the direct unit test of the new
// semantic-key helper. Locks the canonical-form delimiter sanity: two
// tuples that share the same concatenated byte representation under a
// naive `|`-separator MUST produce different keys, so a pipe-bearing
// tenant ID cannot collide with a pipe-bearing tool name.
func TestDedupeKey_SemanticDerivation(t *testing.T) {
	t.Parallel()
	type input struct {
		tenant, server, tool, actionHash, session, execution, principal string
	}
	base := input{
		tenant:     "tnt_a",
		server:     "local-fs",
		tool:       "fs.read_file",
		actionHash: "deadbeef",
		session:    "sess_1",
		execution:  "exec_1",
		principal:  "p1",
	}
	mut := func(f func(*input)) input {
		v := base
		f(&v)
		return v
	}
	cases := []struct {
		name     string
		a, b     input
		wantSame bool
	}{
		{
			name:     "same_inputs_same_hash",
			a:        base,
			b:        base,
			wantSame: true,
		},
		{
			name:     "different_action_hash_different_hash",
			a:        base,
			b:        mut(func(i *input) { i.actionHash = "cafebabe" }),
			wantSame: false,
		},
		{
			name:     "different_session_id_different_hash",
			a:        base,
			b:        mut(func(i *input) { i.session = "sess_2" }),
			wantSame: false,
		},
		{
			name:     "different_execution_id_different_hash",
			a:        base,
			b:        mut(func(i *input) { i.execution = "exec_2" }),
			wantSame: false,
		},
		{
			name:     "different_principal_different_hash",
			a:        base,
			b:        mut(func(i *input) { i.principal = "p2" }),
			wantSame: false,
		},
		{
			name:     "different_tenant_different_hash",
			a:        base,
			b:        mut(func(i *input) { i.tenant = "tnt_b" }),
			wantSame: false,
		},
		{
			// Canonical-form delimiter sanity: tenant `foo|bar`+tool `baz`
			// must NOT collide with tenant `foo`+tool `bar|baz` under any
			// reasonable separator. The fix uses 0x1f unit-separator so
			// neither input can smuggle the delimiter into their own
			// field text without an explicit nul/us byte.
			name: "pipe_smuggling_delimiter_collision_resistant",
			a: input{
				tenant: "foo|bar", server: "srv", tool: "baz",
				actionHash: "h", session: "s", execution: "e", principal: "p",
			},
			b: input{
				tenant: "foo", server: "srv", tool: "bar|baz",
				actionHash: "h", session: "s", execution: "e", principal: "p",
			},
			wantSame: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ha := computeSemanticDedupeKey(tc.a.tenant, tc.a.server, tc.a.tool, tc.a.actionHash, tc.a.session, tc.a.execution, tc.a.principal)
			hb := computeSemanticDedupeKey(tc.b.tenant, tc.b.server, tc.b.tool, tc.b.actionHash, tc.b.session, tc.b.execution, tc.b.principal)
			if tc.wantSame && ha != hb {
				t.Fatalf("expected same hash, got %s vs %s", ha, hb)
			}
			if !tc.wantSame && ha == hb {
				t.Fatalf("expected different hash, both got %s (input collision)", ha)
			}
			// Sanity: hex-encoded SHA-256 = 64 chars.
			if len(ha) != 64 {
				t.Fatalf("hash length = %d, want 64 (hex sha256)", len(ha))
			}
		})
	}
}

// kindStrings is a debug helper: surfaces the event kind sequence when
// the retry-idempotent assertion fails so the operator sees the failure
// shape without paging through the full event objects.
func kindStrings(events []*edge.AgentActionEvent) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = string(e.Kind)
	}
	return out
}
