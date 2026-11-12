package mcp

// Resume-path tests (task-cd9df90a, rulings C1-C5). A REQUIRE_HUMAN tool call
// that carries a consumed Edge approval (the ConsumedApproval marker
// handleToolsCall records after a successful claim) must execute exactly once:
// it never mints a second approval, never waits on or adopts another call's
// dedupe slot, and is never answered with the cached "approval pending" result
// of the call that minted it. The dedupe-sensitive cases run against both the
// in-process and the Redis (miniredis) backends. Fakes are prefixed rs.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

const (
	rsServer = "rs-server"
	rsTool   = "rs.delete_record"
	// rsTenant and rsAgent are newAuthedToolCallCtx's tenant and agent.
	rsTenant = "tnt_a"
	rsAgent  = "agent_alpha"
	// rsWait bounds every wait on another goroutine; nothing sleeps to sync.
	rsWait = 5 * time.Second
)

var rsArgs = json.RawMessage(`{"path":"/data/rec-1","record":"rec-1"}`)

// rsPipeline returns one configured decision. With park set, the FIRST
// dispatch closes entered and blocks until park is closed, so that call holds
// its dedupe slot while the test runs another call.
type rsPipeline struct {
	decision PolicyDecision
	park     chan struct{}
	entered  chan struct{}
	calls    atomic.Int64
}

func (p *rsPipeline) Dispatch(context.Context, *config.PolicyInput) (PolicyDecision, bool) {
	if p.calls.Add(1) == 1 && p.park != nil {
		close(p.entered)
		<-p.park
	}
	return p.decision, true
}

// rsUpstream counts executions of the tool.
type rsUpstream struct {
	calls  atomic.Int64
	result *ToolCallResult
}

func (u *rsUpstream) Invoke(context.Context, ToolCallParams) (*ToolCallResult, error) {
	u.calls.Add(1)
	return u.result, nil
}

// rsMinter is a handoff that can only mint, returning ref-1, ref-2, ...
type rsMinter struct {
	mints atomic.Int64
}

func (m *rsMinter) ConsumeActionGateDecision(context.Context, PolicyDecision, ToolCallApprovalContext) (string, error) {
	return fmt.Sprintf("ref-%d", m.mints.Add(1)), nil
}

// rsHandoff also has the optional AuthorizeConsumedApproval method (ruling
// C2): it records what it was asked to authorize and returns authzErr.
type rsHandoff struct {
	rsMinter
	authzErr error
	mu       sync.Mutex
	authz    []ToolCallApprovalContext
}

func (h *rsHandoff) AuthorizeConsumedApproval(_ context.Context, c ToolCallApprovalContext) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.authz = append(h.authz, c)
	return h.authzErr
}

func (h *rsHandoff) authorized() []ToolCallApprovalContext {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]ToolCallApprovalContext(nil), h.authz...)
}

// rsEmitter is a goroutine-safe fakeEventEmitter: the concurrency case emits
// from two calls at once.
type rsEmitter struct {
	mu     sync.Mutex
	events []*edge.AgentActionEvent
}

func (e *rsEmitter) Emit(_ context.Context, evt *edge.AgentActionEvent) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, evt)
	return nil
}

func (e *rsEmitter) ofKind(kind edge.EventKind) []*edge.AgentActionEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []*edge.AgentActionEvent
	for _, evt := range e.events {
		if evt.Kind == kind {
			out = append(out, evt)
		}
	}
	return out
}

type rsFixture struct {
	deps     ToolCallDeps
	pipeline *rsPipeline
	upstream *rsUpstream
	handoff  *rsHandoff
	emitter  *rsEmitter
}

func newRSFixture(decision pb.DecisionType, dedupe DedupeStore) *rsFixture {
	f := &rsFixture{
		pipeline: &rsPipeline{decision: PolicyDecision{Decision: decision, GateID: "mcp", Reason: "rs gate reason"}},
		upstream: &rsUpstream{result: &ToolCallResult{Content: []ContentItem{{Type: "text", Text: `{"deleted":"rec-1"}`}}}},
		handoff:  &rsHandoff{},
		emitter:  &rsEmitter{},
	}
	f.deps = ToolCallDeps{
		Pipeline:        f.pipeline,
		EventEmitter:    f.emitter,
		Redactor:        DefaultRedactor(),
		ArtifactStore:   &fakeArtifactStore{},
		Upstream:        f.upstream,
		ApprovalHandoff: f.handoff,
		Clock:           func() time.Time { return time.Date(2026, 11, 11, 20, 0, 0, 0, time.UTC) },
		DedupeState:     dedupe,
	}
	return f
}

func (f *rsFixture) invoke(ctx context.Context, args json.RawMessage) (*ToolCallResult, error) {
	return InvokeToolWithPolicy(ctx, f.deps, ToolCallParams{Name: rsTool, Arguments: args}, rsServer)
}

func (f *rsFixture) requireCounts(t *testing.T, upstream, mints int64) {
	t.Helper()
	if got := f.upstream.calls.Load(); got != upstream {
		t.Errorf("upstream executions = %d, want %d", got, upstream)
	}
	if got := f.handoff.mints.Load(); got != mints {
		t.Errorf("approval mints = %d, want %d", got, mints)
	}
}

// rsMarker is the ConsumedApproval handleToolsCall records after claiming an
// approval for (tool, args): the binding comes from BuildMCPApprovalBinding,
// exactly as ProcessApprovalClaim derives it.
func rsMarker(tool string, args json.RawMessage) *ConsumedApproval {
	actionHash, inputHash := BuildMCPApprovalBinding(rsTenant, rsServer, ToolCallParams{Name: tool, Arguments: args}, "")
	return &ConsumedApproval{Ref: "edge_appr_rs_1", Tool: tool, ActionHash: actionHash, InputHash: inputHash}
}

// rsResume is the request context of a resume whose claim recorded marker.
func rsResume(marker *ConsumedApproval) context.Context {
	return withConsumedApproval(newAuthedToolCallCtx(), marker)
}

func rsRequirePending(t *testing.T, res *ToolCallResult, err error, ref string) {
	t.Helper()
	want := "approval pending: " + ref
	if err != nil || res == nil || res.IsError || len(res.Content) != 1 || res.Content[0].Text != want {
		t.Fatalf("result = %+v, err = %v; want %q", res, err, want)
	}
}

// rsBackends lists the dedupe backends the cache-sensitive cases run on.
var rsBackends = []struct {
	name string
	open func(t *testing.T) DedupeStore
}{
	{"in_process", func(*testing.T) DedupeStore { return NewInProcessDedupeStore() }},
	{"redis", func(t *testing.T) DedupeStore {
		client, _ := newMiniRedisDedupeBackend(t)
		return NewRedisDedupeStore(client)
	}},
}

// rsReplay is what a dedupe hit replays for result: the result itself
// in-process, its bounded metadata placeholder over Redis (Redis never stores
// result bodies).
func rsReplay(t *testing.T, store DedupeStore, result *ToolCallResult) *ToolCallResult {
	t.Helper()
	if _, ok := store.(*RedisDedupeStore); !ok {
		return result
	}
	meta, ok := redisDedupeResultMetadataFor(result)
	if !ok {
		t.Fatalf("no Redis dedupe metadata for %+v", result)
	}
	return toolCallResultFromRedisDedupeMetadata(*meta)
}

// TestResume_ExecutesOnceOnRequireHuman: a REQUIRE_HUMAN call carrying a
// consumed approval that binds it executes once and never mints (C1). The post
// event and the invocation handle carry the ref (C5), the handoff authorizes
// the call's own binding and pre-event (C2), and the check leaves the marker
// unused so a registry-gated tool can still Use it once.
func TestResume_ExecutesOnceOnRequireHuman(t *testing.T) {
	t.Parallel()
	f := newRSFixture(pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, NewInProcessDedupeStore())
	marker := rsMarker(rsTool, rsArgs)
	handle := &InvocationHandle{}
	res, err := f.invoke(contextWithInvocationHandle(rsResume(marker), handle), rsArgs)
	if err != nil || res != f.upstream.result {
		t.Fatalf("resume = %+v, %v; want the upstream result %+v", res, err, f.upstream.result)
	}
	f.requireCounts(t, 1, 0)
	if posts := f.emitter.ofKind(edge.EventKindMCPToolPost); len(posts) != 1 || posts[0].ApprovalRef != marker.Ref {
		t.Errorf("mcp.tool.post events = %+v; want one with ApprovalRef %q", posts, marker.Ref)
	}
	if handle.approvalStatus != "consumed" || handle.approvalID != marker.Ref {
		t.Errorf("invocation handle = (%q, %q); want (consumed, %q)", handle.approvalStatus, handle.approvalID, marker.Ref)
	}
	pres, authz := f.emitter.ofKind(edge.EventKindMCPToolPre), f.handoff.authorized()
	if len(pres) != 1 || len(authz) != 1 {
		t.Fatalf("pre events = %d, authorizations = %d; want 1 and 1", len(pres), len(authz))
	}
	want := ToolCallApprovalContext{Tenant: rsTenant, AgentID: rsAgent, Server: rsServer, Tool: rsTool,
		ActionHash: marker.ActionHash, Args: rsArgs, EventID: pres[0].EventID}
	if !reflect.DeepEqual(authz[0], want) {
		t.Errorf("authorized %+v; want %+v", authz[0], want)
	}
	if !marker.Use(rsTool, marker.ActionHash, marker.InputHash) {
		t.Error("marker.Use = false after the resume; the action-gate check must not consume it (C1)")
	}
}

// TestResume_NeverServedFromCachedPending: the ref-less call's pending result
// stays cached, but the resume with the same params must evict it and execute,
// never be answered with it after its approval was consumed (C3). The executed
// result is then what identical ref-less calls replay.
func TestResume_NeverServedFromCachedPending(t *testing.T) {
	t.Parallel()
	for _, backend := range rsBackends {
		t.Run(backend.name, func(t *testing.T) {
			t.Parallel()
			store := backend.open(t)
			f := newRSFixture(pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, store)
			first, err := f.invoke(newAuthedToolCallCtx(), rsArgs)
			rsRequirePending(t, first, err, "ref-1")
			resumed, err := f.invoke(rsResume(rsMarker(rsTool, rsArgs)), rsArgs)
			if err != nil || resumed != f.upstream.result {
				t.Fatalf("resume = %+v, %v; want the upstream result, not the cached pending one", resumed, err)
			}
			f.requireCounts(t, 1, 1)
			later, err := f.invoke(newAuthedToolCallCtx(), rsArgs)
			if want := rsReplay(t, store, f.upstream.result); err != nil || !reflect.DeepEqual(later, want) {
				t.Fatalf("later ref-less call = %+v, %v; want the executed result replayed: %+v", later, err, want)
			}
			f.requireCounts(t, 1, 1)
		})
	}
}

type rsOutcome struct {
	res *ToolCallResult
	err error
}

// rsGo runs call on a goroutine. Cleanup unparks the pipeline and waits for
// the goroutine, so no call outlives the test.
func rsGo(t *testing.T, unpark func(), call func() (*ToolCallResult, error)) <-chan rsOutcome {
	t.Helper()
	out := make(chan rsOutcome, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		res, err := call()
		out <- rsOutcome{res: res, err: err}
	}()
	t.Cleanup(func() {
		unpark()
		select {
		case <-done:
		case <-time.After(rsWait):
			t.Error("tool call still running at cleanup")
		}
	})
	return out
}

// TestResume_DoesNotWaitOnInFlightRefless: a ref-less call parked inside the
// pipeline holds the dedupe slot. The resume must neither wait for it nor
// adopt its outcome (C3).
func TestResume_DoesNotWaitOnInFlightRefless(t *testing.T) {
	t.Parallel()
	f := newRSFixture(pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, NewInProcessDedupeStore())
	f.pipeline.park, f.pipeline.entered = make(chan struct{}), make(chan struct{})
	var once sync.Once
	unpark := func() { once.Do(func() { close(f.pipeline.park) }) }
	refless := rsGo(t, unpark, func() (*ToolCallResult, error) { return f.invoke(newAuthedToolCallCtx(), rsArgs) })
	select {
	case <-f.pipeline.entered:
	case <-time.After(rsWait):
		t.Fatal("the ref-less call never reached the pipeline")
	}
	resumed := rsGo(t, unpark, func() (*ToolCallResult, error) {
		return f.invoke(rsResume(rsMarker(rsTool, rsArgs)), rsArgs)
	})
	select {
	case got := <-resumed:
		if got.err != nil || got.res != f.upstream.result {
			t.Fatalf("resume = %+v, %v; want the upstream result", got.res, got.err)
		}
	case <-time.After(rsWait):
		t.Fatal("the resume blocked behind the in-flight ref-less call's dedupe slot; it must run unslotted (C3)")
	}
	f.requireCounts(t, 1, 0)
	select {
	case <-refless:
		t.Fatal("the ref-less call finished while parked")
	default:
	}
	unpark()
	select {
	case got := <-refless:
		rsRequirePending(t, got.res, got.err, "ref-1")
	case <-time.After(rsWait):
		t.Fatal("the ref-less call never finished after release")
	}
}

// TestResume_BindingMismatchFailsClosed: a consumed approval that does not
// bind this exact call fails closed as args_mismatch (C4), with no execution
// and no new mint.
func TestResume_BindingMismatchFailsClosed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(*ConsumedApproval)
	}{
		{"input_hash", func(m *ConsumedApproval) { m.InputHash = "other-input-hash" }},
		{"action_hash", func(m *ConsumedApproval) { m.ActionHash = "other-action-hash" }},
		{"tool", func(m *ConsumedApproval) { m.Tool = "rs.other_tool" }},
		{"incomplete_binding", func(m *ConsumedApproval) { m.InputHash = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newRSFixture(pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, NewInProcessDedupeStore())
			marker := rsMarker(rsTool, rsArgs)
			tc.mutate(marker)
			res, err := f.invoke(rsResume(marker), rsArgs)
			var conflict *edge.ApprovalConflictError
			if !errors.As(err, &conflict) || conflict.Kind != edge.ApprovalConflictKindArgsMismatch || res != nil {
				t.Fatalf("resume = %+v, %v; want *edge.ApprovalConflictError kind %q", res, err, edge.ApprovalConflictKindArgsMismatch)
			}
			f.requireCounts(t, 0, 0)
		})
	}
}

// TestResume_InvariantFloorDenies: the handoff authorizes the consumed
// approval before upstream (the gateway's invariant floor, C2); its refusal
// surfaces and nothing executes or mints.
func TestResume_InvariantFloorDenies(t *testing.T) {
	t.Parallel()
	f := newRSFixture(pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, NewInProcessDedupeStore())
	floor := errors.New("rs: denied by invariant")
	f.handoff.authzErr = floor
	res, err := f.invoke(rsResume(rsMarker(rsTool, rsArgs)), rsArgs)
	if !errors.Is(err, floor) || res != nil {
		t.Fatalf("resume = %+v, %v; want the authorizer's error %v", res, err, floor)
	}
	f.requireCounts(t, 0, 0)
}

// TestResume_HandoffWithoutAuthorizerFailsClosed: a handoff that cannot
// authorize a consumed approval is a gateway misconfiguration (-32097), never
// an unchecked execution or a re-mint.
func TestResume_HandoffWithoutAuthorizerFailsClosed(t *testing.T) {
	t.Parallel()
	f := newRSFixture(pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, NewInProcessDedupeStore())
	minter := &rsMinter{}
	f.deps.ApprovalHandoff = minter
	res, err := f.invoke(rsResume(rsMarker(rsTool, rsArgs)), rsArgs)
	if !errors.Is(err, ErrApprovalGateMisconfigured) || res != nil {
		t.Fatalf("resume = %+v, %v; want ErrApprovalGateMisconfigured", res, err)
	}
	if n := f.upstream.calls.Load(); n != 0 {
		t.Errorf("upstream executions = %d, want 0", n)
	}
	if n := minter.mints.Load(); n != 0 {
		t.Errorf("approval mints = %d, want 0", n)
	}
}

// TestResume_DenyStillDenies: DENY wins over a consumed approval (the claim
// already spent it); nothing executes, mints or is authorized.
func TestResume_DenyStillDenies(t *testing.T) {
	t.Parallel()
	f := newRSFixture(pb.DecisionType_DECISION_TYPE_DENY, NewInProcessDedupeStore())
	res, err := f.invoke(rsResume(rsMarker(rsTool, rsArgs)), rsArgs)
	reason := f.pipeline.decision.Reason
	if err != nil || res == nil || !res.IsError || len(res.Content) != 1 || res.Content[0].Text != reason {
		t.Fatalf("resume = %+v, %v; want the IsError deny result %q", res, err, reason)
	}
	f.requireCounts(t, 0, 0)
	if n := len(f.handoff.authorized()); n != 0 {
		t.Errorf("authorizations = %d, want 0", n)
	}
}

// TestResume_UnconsumedApprovalRefArgFailsClosed: an `_approval_ref` that
// reaches the policy gate unclaimed (no marker) means the claim never ran.
// Minting another approval for it would loop, so it fails closed.
func TestResume_UnconsumedApprovalRefArgFailsClosed(t *testing.T) {
	t.Parallel()
	f := newRSFixture(pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, NewInProcessDedupeStore())
	args := json.RawMessage(`{"_approval_ref":"edge_appr_unclaimed","path":"/data/rec-1","record":"rec-1"}`)
	res, err := f.invoke(newAuthedToolCallCtx(), args)
	if !errors.Is(err, ErrApprovalGateMisconfigured) || res != nil {
		t.Fatalf("call = %+v, %v; want ErrApprovalGateMisconfigured", res, err)
	}
	f.requireCounts(t, 0, 0)
}

// TestRefless_PendingStillCached: identical ref-less retries keep sharing one
// pending approval; C3 leaves the ref-less path unchanged. In-process the retry
// returns the same `approval pending: ref-1`; over Redis it is that result's
// bounded placeholder.
func TestRefless_PendingStillCached(t *testing.T) {
	t.Parallel()
	for _, backend := range rsBackends {
		t.Run(backend.name, func(t *testing.T) {
			t.Parallel()
			store := backend.open(t)
			f := newRSFixture(pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, store)
			first, err := f.invoke(newAuthedToolCallCtx(), rsArgs)
			rsRequirePending(t, first, err, "ref-1")
			second, err := f.invoke(newAuthedToolCallCtx(), rsArgs)
			if want := rsReplay(t, store, first); err != nil || !reflect.DeepEqual(second, want) {
				t.Fatalf("retry = %+v, %v; want the cached pending result %+v", second, err, want)
			}
			f.requireCounts(t, 0, 1)
		})
	}
}

// TestMapHandlerError_ApprovalConflict: a typed approval conflict from the
// policy path (C4) is a -32096 lifecycle error carrying only its typed kind and
// reason, also when wrapped.
func TestMapHandlerError_ApprovalConflict(t *testing.T) {
	t.Parallel()
	srv := NewServer(newChannelTransport(), NewToolRegistry(), NewResourceRegistry(), ServerConfig{
		Name: "test", Version: "0.0.0", ProtocolVersion: DefaultProtocolVersion,
	})
	conflict := &edge.ApprovalConflictError{Kind: edge.ApprovalConflictKindArgsMismatch, Reason: "consumed approval does not bind this call"}
	want := map[string]any{"kind": "args_mismatch", "reason": conflict.Reason}
	for _, err := range []error{conflict, fmt.Errorf("approval handoff: %w", conflict)} {
		rpcErr := srv.mapHandlerError(err)
		if rpcErr == nil || rpcErr.Code != jsonRPCApprovalLifecycleErrorCode || !reflect.DeepEqual(rpcErr.Data, want) {
			t.Errorf("mapHandlerError(%v) = %+v; want code %d with data %v", err, rpcErr, jsonRPCApprovalLifecycleErrorCode, want)
		}
	}
}
