package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/claude"
)

func TestEvaluatorCallsGatewayAndRecordsDecisionEvidence(t *testing.T) {
	t.Parallel()

	evaluate := &stubEvaluateClient{resp: &EvaluateResponse{
		Decision:           string(edgecore.DecisionAllow),
		Reason:             "safe allow",
		RuleID:             "edge.safe.allow",
		PolicySnapshot:     "snap-eval",
		EventID:            "evt-eval-decision",
		ActionHash:         "sha256:action-eval",
		InputHash:          "sha256:input-eval",
		PermissionDecision: "allow",
		CacheEligible:      true,
	}}
	writer := &captureEventWriter{}
	evaluator := NewEvaluator(EvaluatorConfig{
		Client:      evaluate,
		EventWriter: writer,
		State:       evaluatorTestState(edgecore.PolicyModeEnforce),
		HookTimeout: time.Second,
	})

	decision, err := evaluator.EvaluateHook(context.Background(), claude.AgentdRequest{
		EventName:      "PreToolUse",
		SessionID:      "edge_sess_eval",
		ExecutionID:    "edge_exec_eval",
		TenantID:       "tenant-eval",
		PrincipalID:    "principal-eval",
		ToolName:       "Bash",
		ToolUseID:      "toolu-eval",
		TranscriptPath: `C:\Users\yaron\secret-transcript.jsonl`,
		Prompt:         "raw prompt sk-evaluator-secret",
		ToolInput:      map[string]any{"command": "echo Bearer raw-evaluator-secret"},
		InputRedacted:  map[string]any{"command": "echo Bearer raw-evaluator-secret"},
		InputHash:      "sha256:input-eval",
		ActionHash:     "sha256:action-eval",
		Capability:     "exec.shell",
		RiskTags:       []string{"exec", "test"},
		Labels:         map[string]string{"command.class": "safe"},
	})
	if err != nil {
		t.Fatalf("EvaluateHook: %v", err)
	}
	if decision.Decision != claude.DecisionAllow {
		t.Fatalf("decision = %#v, want allow", decision)
	}
	if len(evaluate.requests) != 1 {
		t.Fatalf("evaluate request count = %d, want 1", len(evaluate.requests))
	}
	req := evaluate.requests[0]
	if req.TenantID != "tenant-eval" || req.PrincipalID != "principal-eval" || req.SessionID != "edge_sess_eval" || req.ExecutionID != "edge_exec_eval" {
		t.Fatalf("evaluate identity not forwarded: %#v", req)
	}
	if req.Kind != string(edgecore.EventKindHookPreToolUse) || req.Layer != string(edgecore.LayerHook) || req.ToolName != "Bash" {
		t.Fatalf("evaluate hook metadata = %#v", req)
	}
	if req.InputHash != "sha256:input-eval" || req.InputRedacted["command"] != "echo Bearer [REDACTED]" {
		t.Fatalf("evaluate redacted input/hash = %#v / %q", req.InputRedacted, req.InputHash)
	}
	if len(writer.events) != 1 {
		t.Fatalf("decision events written = %d, want 1", len(writer.events))
	}
	event := writer.events[0]
	// EDGE-039: evaluator must NOT reuse Gateway's resp.EventID for the agentd
	// evidence event. Reusing it caused events/batch flush to fail with 409
	// IdempotencyWindowExpired because Gateway evaluate already persisted that
	// event_id. The agentd evidence event must have a fresh "agentd-" id.
	if event.EventID == "evt-eval-decision" {
		t.Fatalf("agentd evidence event reused Gateway resp.EventID; want fresh agentd-* id, got %q", event.EventID)
	}
	if !strings.HasPrefix(event.EventID, "agentd-") {
		t.Fatalf("agentd evidence event id = %q, want agentd- prefix", event.EventID)
	}
	if event.Kind != edgecore.EventKindHookPolicyDecision || event.Decision != edgecore.DecisionAllow {
		t.Fatalf("decision event = %#v", event)
	}
	payload, _ := json.Marshal(event)
	for _, forbidden := range []string{"raw-evaluator-secret", "evaluator-secret", "secret-transcript", `C:\\Users\\yaron`} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("decision event leaked %q: %s", forbidden, payload)
		}
	}
}

func TestEvaluatorCoalescesConcurrentIdenticalRequests(t *testing.T) {
	t.Parallel()

	client := newBlockingEvaluateClient(coalescedAllowResponse(), nil)
	writer := &concurrentCaptureEventWriter{}
	cache := NewSafeAllowCache(SafeAllowCacheConfig{Enabled: true, TTL: time.Minute, MaxEntries: 4}, fixedClock{now: time.Date(2026, 5, 2, 16, 30, 0, 0, time.UTC)})
	evaluator := NewEvaluator(EvaluatorConfig{
		Client:      client,
		EventWriter: writer,
		State:       evaluatorTestState(edgecore.PolicyModeEnforce),
		Cache:       cache,
		HookTimeout: time.Second,
	})

	decisions, errs := runConcurrentEvaluateHooks(t, evaluator, evaluatorCoalesceRequest(), 20, client.release)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d EvaluateHook error = %v", i, err)
		}
		if decisions[i].Decision != claude.DecisionAllow {
			t.Fatalf("caller %d decision = %#v, want allow", i, decisions[i])
		}
	}
	if calls := client.callCount(); calls != 1 {
		t.Fatalf("gateway evaluate calls = %d, want 1", calls)
	}
	if events := writer.snapshot(); len(events) != 1 || !strings.HasPrefix(events[0].EventID, "agentd-") || events[0].Decision != edgecore.DecisionAllow {
		t.Fatalf("decision evidence events = %#v, want exactly one coalesced allow event with fresh agentd-* id", events)
	}
	cache.mu.Lock()
	recordCount := len(cache.records)
	cache.mu.Unlock()
	if recordCount != 1 {
		t.Fatalf("cache records = %d, want 1 coalesced safe-allow record", recordCount)
	}
}

func TestEvaluatorCoalesceErrorPathDoesNotPoisonSlot(t *testing.T) {
	t.Parallel()

	client := newBlockingEvaluateClient(nil, ErrGatewayTimeout)
	writer := &concurrentCaptureEventWriter{}
	evaluator := NewEvaluator(EvaluatorConfig{
		Client:      client,
		EventWriter: writer,
		State:       evaluatorTestState(edgecore.PolicyModeEnterpriseStrict),
		Cache:       NewSafeAllowCache(SafeAllowCacheConfig{Enabled: true, TTL: time.Minute, MaxEntries: 4}, fixedClock{now: time.Date(2026, 5, 2, 16, 45, 0, 0, time.UTC)}),
		HookTimeout: time.Second,
	})

	decisions, errs := runConcurrentEvaluateHooks(t, evaluator, evaluatorCoalesceRequest(), 20, client.release)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d EvaluateHook error = %v", i, err)
		}
		if decisions[i].Decision != claude.DecisionDeny {
			t.Fatalf("caller %d decision = %#v, want fail-closed deny", i, decisions[i])
		}
	}
	if calls := client.callCount(); calls != 1 {
		t.Fatalf("gateway calls after coalesced error batch = %d, want 1", calls)
	}
	client.setResult(coalescedAllowResponse(), nil)
	decision, err := evaluator.EvaluateHook(context.Background(), evaluatorCoalesceRequest())
	if err != nil || decision.Decision != claude.DecisionAllow {
		t.Fatalf("post-error EvaluateHook = %#v, %v; want fresh allow", decision, err)
	}
	if calls := client.callCount(); calls != 2 {
		t.Fatalf("gateway calls after retry = %d, want 2 proving slot was not poisoned", calls)
	}
	if events := writer.snapshot(); len(events) != 2 || events[0].Status != edgecore.ActionStatusDegraded || !strings.HasPrefix(events[1].EventID, "agentd-") {
		t.Fatalf("evidence events after retry = %#v, want degraded then fresh allow with agentd-* id", events)
	}
}

func TestEvaluatorFailModeWritesDegradedFailClosedEvidence(t *testing.T) {
	t.Parallel()

	evaluate := &stubEvaluateClient{err: ErrGatewayTimeout}
	writer := &captureEventWriter{}
	evaluator := NewEvaluator(EvaluatorConfig{
		Client:      evaluate,
		EventWriter: writer,
		State:       evaluatorTestState(edgecore.PolicyModeEnterpriseStrict),
		HookTimeout: time.Second,
	})

	decision, err := evaluator.EvaluateHook(context.Background(), claude.AgentdRequest{
		EventName:     "PreToolUse",
		SessionID:     "edge_sess_eval",
		ExecutionID:   "edge_exec_eval",
		TenantID:      "tenant-eval",
		PrincipalID:   "principal-eval",
		ToolName:      "Bash",
		InputRedacted: map[string]any{"command": "rm -rf /tmp/project"},
		InputHash:     "sha256:input-rm",
		ActionHash:    "sha256:action-rm",
		RiskTags:      []string{"destructive"},
	})
	if err != nil {
		t.Fatalf("EvaluateHook returned transport error to hook: %v", err)
	}
	if decision.Decision != claude.DecisionDeny || !strings.Contains(strings.ToLower(decision.Reason), "enterprise-strict") {
		t.Fatalf("decision = %#v, want enterprise-strict deny", decision)
	}
	if len(writer.events) != 1 {
		t.Fatalf("events written = %d, want degraded evidence", len(writer.events))
	}
	event := writer.events[0]
	if event.Status != edgecore.ActionStatusDegraded || event.Decision != edgecore.DecisionDeny {
		t.Fatalf("degraded event status/decision = %q/%q", event.Status, event.Decision)
	}
	if event.Labels["fail_closed"] != "true" || event.Labels["degraded"] != "true" || event.ErrorCode != string(GatewayErrorTimeout) {
		t.Fatalf("degraded event labels/error = %#v / %q", event.Labels, event.ErrorCode)
	}
}

func TestEvaluatorEvidenceFailureDoesNotFlipFreshDecision(t *testing.T) {
	t.Parallel()

	evaluator := NewEvaluator(EvaluatorConfig{
		Client: &stubEvaluateClient{resp: &EvaluateResponse{
			Decision:           string(edgecore.DecisionAllow),
			PolicySnapshot:     "snap-eval",
			EventID:            "evt-evidence-fail",
			PermissionDecision: "allow",
		}},
		EventWriter: &captureEventWriter{err: errors.New("redis unavailable: Bearer evidence-secret")},
		State:       evaluatorTestState(edgecore.PolicyModeEnterpriseStrict),
		HookTimeout: time.Second,
	})

	decision, err := evaluator.EvaluateHook(context.Background(), claude.AgentdRequest{
		EventName:     "PreToolUse",
		SessionID:     "edge_sess_eval",
		ExecutionID:   "edge_exec_eval",
		TenantID:      "tenant-eval",
		PrincipalID:   "principal-eval",
		ToolName:      "Bash",
		InputRedacted: map[string]any{"command": "npm test"},
		InputHash:     "sha256:input-safe",
		ActionHash:    "sha256:action-safe",
		Labels:        map[string]string{"command.class": "safe"},
	})
	if err != nil {
		t.Fatalf("EvaluateHook returned evidence write failure: %v", err)
	}
	if decision.Decision != claude.DecisionAllow {
		t.Fatalf("fresh Gateway allow flipped after evidence failure: %#v", decision)
	}
}

func TestEvaluatorRecordsObservabilityForCacheAndEvidenceFailure(t *testing.T) {
	t.Parallel()

	evaluate := &stubEvaluateClient{resp: &EvaluateResponse{
		Decision:           string(edgecore.DecisionAllow),
		PolicySnapshot:     "snap-eval",
		EventID:            "evt-metrics-cache",
		ActionHash:         "sha256:action-metrics",
		InputHash:          "sha256:input-metrics",
		PermissionDecision: "allow",
		CacheEligible:      true,
	}}
	recorder := &captureRecorder{}
	evaluator := NewEvaluator(EvaluatorConfig{
		Client:      evaluate,
		EventWriter: &captureEventWriter{err: errors.New("event sink unavailable")},
		State:       evaluatorTestState(edgecore.PolicyModeEnforce),
		Cache: NewSafeAllowCache(SafeAllowCacheConfig{
			Enabled:    true,
			TTL:        time.Minute,
			MaxEntries: 4,
		}, fixedClock{now: time.Date(2026, 5, 2, 16, 0, 0, 0, time.UTC)}),
		Recorder:    recorder,
		HookTimeout: time.Second,
	})
	req := evaluatorMetricsRequest()
	if decision, err := evaluator.EvaluateHook(context.Background(), req); err != nil || decision.Decision != claude.DecisionAllow {
		t.Fatalf("first EvaluateHook = %#v, %v; want allow", decision, err)
	}
	if decision, err := evaluator.EvaluateHook(context.Background(), req); err != nil || decision.Decision != claude.DecisionAllow {
		t.Fatalf("second EvaluateHook = %#v, %v; want cached allow", decision, err)
	}
	if len(evaluate.requests) != 1 {
		t.Fatalf("gateway evaluate calls = %d, want 1 (second call should be cache hit)", len(evaluate.requests))
	}
	if !recorder.hasCacheResult("miss") || !recorder.hasCacheResult("hit") {
		t.Fatalf("cache lookup metrics = %#v, want miss and hit", recorder.cacheLookups)
	}
	if !recorder.hasActionDecision("allow") {
		t.Fatalf("action decision metrics = %#v, want allow", recorder.actionDecisions)
	}
	if !recorder.hasDegradedReason("evidence_write_failed") {
		t.Fatalf("degraded metrics = %#v, want evidence_write_failed", recorder.degraded)
	}
	if len(recorder.evaluateLatency) == 0 || len(recorder.hookLatency) == 0 {
		t.Fatalf("latency metrics evaluate=%d hook=%d, want both", len(recorder.evaluateLatency), len(recorder.hookLatency))
	}
}

func TestEvaluatorRecordsObservabilityForEnterpriseStrictFailClosed(t *testing.T) {
	t.Parallel()

	recorder := &captureRecorder{}
	evaluator := NewEvaluator(EvaluatorConfig{
		Client:      &stubEvaluateClient{err: ErrGatewayTimeout},
		EventWriter: &captureEventWriter{},
		State:       evaluatorTestState(edgecore.PolicyModeEnterpriseStrict),
		Recorder:    recorder,
		HookTimeout: time.Second,
	})
	decision, err := evaluator.EvaluateHook(context.Background(), claude.AgentdRequest{
		EventName:     "PreToolUse",
		SessionID:     "edge_sess_eval",
		ExecutionID:   "edge_exec_eval",
		TenantID:      "tenant-eval",
		PrincipalID:   "principal-eval",
		ToolName:      "Bash",
		InputRedacted: map[string]any{"command": "rm -rf /tmp/project"},
		InputHash:     "sha256:input-rm",
		ActionHash:    "sha256:action-rm",
		RiskTags:      []string{"destructive"},
	})
	if err != nil {
		t.Fatalf("EvaluateHook: %v", err)
	}
	if decision.Decision != claude.DecisionDeny {
		t.Fatalf("decision = %#v, want deny", decision)
	}
	if !recorder.hasFailClosedReason(string(GatewayErrorTimeout)) {
		t.Fatalf("fail-closed metrics = %#v, want timeout", recorder.failClosed)
	}
	if !recorder.hasDegradedReason(string(GatewayErrorTimeout)) {
		t.Fatalf("degraded metrics = %#v, want timeout", recorder.degraded)
	}
	if !recorder.hasActionDecision("deny") {
		t.Fatalf("action decision metrics = %#v, want deny", recorder.actionDecisions)
	}
}

func TestEvaluatorRecordsObservabilityForInlineApprovalWait(t *testing.T) {
	t.Parallel()

	recorder := &captureRecorder{}
	evaluator := NewEvaluator(EvaluatorConfig{
		Client: &stubEvaluateClient{resp: &EvaluateResponse{
			Decision:       string(edgecore.DecisionRequireApproval),
			PolicySnapshot: "snap-eval",
			EventID:        "evt-metrics-approval",
			ApprovalRef:    "edge_appr_metrics",
			ApprovalURL:    "/edge/approvals/edge_appr_metrics",
			ActionHash:     "sha256:action-approval",
			InputHash:      "sha256:input-approval",
		}},
		EventWriter:    &captureEventWriter{},
		State:          evaluatorTestState(edgecore.PolicyModeEnforce),
		ApprovalWaiter: &fakeApprovalWaiter{result: ApprovalWaitResult{Status: ApprovalWaitApproved, Reason: "approved"}},
		ApprovalConfig: ApprovalDecisionConfig{InlineWaitEnabled: true, InlineWaitTimeout: time.Second, PolicyMode: edgecore.PolicyModeEnforce},
		Recorder:       recorder,
		HookTimeout:    2 * time.Second,
	})
	decision, err := evaluator.EvaluateHook(context.Background(), evaluatorMetricsRequest())
	if err != nil {
		t.Fatalf("EvaluateHook: %v", err)
	}
	if decision.Decision != claude.DecisionAllow {
		t.Fatalf("decision = %#v, want inline approval allow", decision)
	}
	if len(recorder.approvalRequested) != 1 {
		t.Fatalf("approval requested metrics = %#v, want one", recorder.approvalRequested)
	}
	if !recorder.hasApprovalResolved("approved") {
		t.Fatalf("approval resolved metrics = %#v, want approved", recorder.approvalResolved)
	}
}

func TestLocalServerUsesConfiguredEvaluator(t *testing.T) {
	t.Parallel()

	server, err := NewLocalServer(LocalServerConfig{
		BindURL:      "http://127.0.0.1:8765/v1/edge/hooks/claude",
		Nonce:        "nonce-123",
		MaxBodyBytes: 1 << 20,
		State:        evaluatorTestState(edgecore.PolicyModeEnforce),
		Evaluator: stubAgentdClientFunc(func(context.Context, claude.AgentdRequest) (claude.AgentdDecision, error) {
			return claude.AgentdDecision{Decision: claude.DecisionAllow}, nil
		}),
	})
	if err != nil {
		t.Fatalf("NewLocalServer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/edge/hooks/claude", strings.NewReader(`{"event_name":"PreToolUse","session_id":"edge_sess_eval","execution_id":"edge_exec_eval","tool_name":"Bash"}`))
	req.Header.Set("X-Cordum-Agentd-Nonce", "nonce-123")
	rr := httptest.NewRecorder()
	server.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status = %d body=%q, want 200", rr.Code, rr.Body.String())
	}
	var decision claude.AgentdDecision
	if err := json.Unmarshal(rr.Body.Bytes(), &decision); err != nil {
		t.Fatalf("decode decision: %v", err)
	}
	if decision.Decision != claude.DecisionAllow {
		t.Fatalf("decision = %#v, want configured evaluator allow", decision)
	}
}

type stubEvaluateClient struct {
	resp     *EvaluateResponse
	err      error
	requests []EvaluateRequest
}

func (s *stubEvaluateClient) Evaluate(_ context.Context, req EvaluateRequest) (*EvaluateResponse, error) {
	s.requests = append(s.requests, req)
	if s.err != nil {
		return nil, s.err
	}
	if s.resp == nil {
		return nil, errors.New("missing test response")
	}
	out := cloneEvaluateResponse(*s.resp)
	return &out, nil
}

type blockingEvaluateClient struct {
	mu       sync.Mutex
	resp     *EvaluateResponse
	err      error
	calls    int
	requests []EvaluateRequest

	started      chan struct{}
	release      chan struct{}
	closeStarted sync.Once
}

func newBlockingEvaluateClient(resp *EvaluateResponse, err error) *blockingEvaluateClient {
	return &blockingEvaluateClient{
		resp:    resp,
		err:     err,
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (c *blockingEvaluateClient) Evaluate(ctx context.Context, req EvaluateRequest) (*EvaluateResponse, error) {
	c.mu.Lock()
	c.calls++
	c.requests = append(c.requests, req)
	c.closeStarted.Do(func() { close(c.started) })
	c.mu.Unlock()

	select {
	case <-c.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	c.mu.Lock()
	resp, err := c.resp, c.err
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, errors.New("missing test response")
	}
	out := cloneEvaluateResponse(*resp)
	return &out, nil
}

func (c *blockingEvaluateClient) setResult(resp *EvaluateResponse, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resp = resp
	c.err = err
}

func (c *blockingEvaluateClient) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

type concurrentCaptureEventWriter struct {
	mu     sync.Mutex
	events []edgecore.AgentActionEvent
}

func (w *concurrentCaptureEventWriter) WriteEvent(_ context.Context, event edgecore.AgentActionEvent) (edgecore.AgentActionEvent, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.events = append(w.events, event)
	return event, nil
}

func (w *concurrentCaptureEventWriter) snapshot() []edgecore.AgentActionEvent {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]edgecore.AgentActionEvent(nil), w.events...)
}

func runConcurrentEvaluateHooks(t *testing.T, evaluator *Evaluator, req claude.AgentdRequest, n int, release chan<- struct{}) ([]claude.AgentdDecision, []error) {
	t.Helper()
	decisions := make([]claude.AgentdDecision, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	var waiters sync.WaitGroup
	start := make(chan struct{})
	waiters.Add(n)
	evaluator.coalesceWaitHook = waiters.Done
	defer func() { evaluator.coalesceWaitHook = nil }()
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			decisions[i], errs[i] = evaluator.EvaluateHook(context.Background(), req)
		}(i)
	}
	close(start)
	waitForCoalesceWaiters(t, &waiters)
	close(release)
	wg.Wait()
	return decisions, errs
}

func waitForCoalesceWaiters(t *testing.T, wg *sync.WaitGroup) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("coalesced callers did not all register before timeout")
	}
}

func evaluatorCoalesceRequest() claude.AgentdRequest {
	req := evaluatorMetricsRequest()
	req.ActionHash = "sha256:action-coalesce"
	req.InputHash = "sha256:input-coalesce"
	req.InputRedacted = map[string]any{"command": "npm test -- --runInBand"}
	return req
}

func coalescedAllowResponse() *EvaluateResponse {
	return &EvaluateResponse{
		Decision:           string(edgecore.DecisionAllow),
		Reason:             "coalesced safe allow",
		PolicySnapshot:     "snap-eval",
		EventID:            "evt-coalesced-allow",
		ActionHash:         "sha256:action-coalesce",
		InputHash:          "sha256:input-coalesce",
		PermissionDecision: "allow",
		CacheEligible:      true,
	}
}

type stubAgentdClientFunc func(context.Context, claude.AgentdRequest) (claude.AgentdDecision, error)

func (f stubAgentdClientFunc) EvaluateHook(ctx context.Context, req claude.AgentdRequest) (claude.AgentdDecision, error) {
	return f(ctx, req)
}

// TestEvaluatorUserPromptSubmitEnforceCallsGateway is the EDGE-049 regression
// guard. Yaron's `cordum-claude` real-Claude session (policy_mode=enforce)
// returned "Cordum Edge enforce mode failed closed because governance is
// unavailable" for every UserPromptSubmit hook. The wrapper-spawned agentd
// emitted no /api/v1/edge/evaluate outgoing diagnostic, proving Evaluate was
// never called — the path bailed inside evaluateHook before reaching
// client.Evaluate. This test asserts the production-path: a fresh enforce-
// mode UserPromptSubmit hook MUST invoke client.Evaluate exactly once and
// MUST return the gateway's actual decision (not a fail-mode fallback).
func TestEvaluatorUserPromptSubmitEnforceCallsGateway(t *testing.T) {
	t.Parallel()

	evaluate := &stubEvaluateClient{resp: &EvaluateResponse{
		Decision:           string(edgecore.DecisionAllow),
		Reason:             "user prompt allowed",
		PolicySnapshot:     "snap-eval",
		EventID:            "evt-userprompt",
		ActionHash:         "sha256:action-userprompt",
		InputHash:          "sha256:input-userprompt",
		PermissionDecision: "allow",
	}}
	writer := &captureEventWriter{}
	evaluator := NewEvaluator(EvaluatorConfig{
		Client:      evaluate,
		EventWriter: writer,
		State:       evaluatorTestState(edgecore.PolicyModeEnforce),
		HookTimeout: time.Second,
	})

	decision, err := evaluator.EvaluateHook(context.Background(), claude.AgentdRequest{
		EventName:     "UserPromptSubmit",
		SessionID:     "edge_sess_eval",
		ExecutionID:   "edge_exec_eval",
		TenantID:      "tenant-eval",
		PrincipalID:   "principal-eval",
		Prompt:        "Hi",
		InputRedacted: map[string]any{"prompt_redacted": "Hi"},
		InputHash:     "sha256:input-userprompt",
		ActionHash:    "sha256:action-userprompt",
		Capability:    "edge.unknown",
		RiskTags:      []string{"review_required", "unknown"},
		Labels:        map[string]string{"command.class": "unknown"},
	})
	if err != nil {
		t.Fatalf("EvaluateHook returned error: %v", err)
	}
	if decision.Decision != claude.DecisionAllow {
		t.Fatalf("decision = %q, want allow (gateway responded ALLOW); fail-mode fallback would indicate the bug", decision.Decision)
	}
	if len(evaluate.requests) != 1 {
		t.Fatalf("client.Evaluate called %d times, want 1; zero means evaluateHook short-circuited (the EDGE-049 bug)", len(evaluate.requests))
	}
	got := evaluate.requests[0]
	if got.Kind != string(edgecore.EventKindHookUserPromptSubmit) {
		t.Errorf("forwarded kind = %q, want %q", got.Kind, edgecore.EventKindHookUserPromptSubmit)
	}
	if got.SessionID != "edge_sess_eval" || got.ExecutionID != "edge_exec_eval" || got.PrincipalID != "principal-eval" {
		t.Errorf("forwarded identity = %#v, want session/execution/principal eval", got)
	}
}

func evaluatorTestState(mode edgecore.PolicyMode) SessionState {
	return SessionState{
		SessionID:      "edge_sess_eval",
		ExecutionID:    "edge_exec_eval",
		TenantID:       "tenant-eval",
		PrincipalID:    "principal-eval",
		PolicySnapshot: "snap-eval",
		PolicyMode:     mode,
	}
}

func evaluatorMetricsRequest() claude.AgentdRequest {
	return claude.AgentdRequest{
		EventName:     "PreToolUse",
		SessionID:     "edge_sess_eval",
		ExecutionID:   "edge_exec_eval",
		TenantID:      "tenant-eval",
		PrincipalID:   "principal-eval",
		ToolName:      "Bash",
		InputRedacted: map[string]any{"command": "npm test"},
		InputHash:     "sha256:input-metrics",
		ActionHash:    "sha256:action-metrics",
		Capability:    "exec.shell",
		RiskTags:      []string{"exec", "test"},
		Labels:        map[string]string{"command.class": "safe"},
		DurationMS:    17,
	}
}

type captureRecorder struct {
	mu                sync.Mutex
	actionDecisions   []recordActionDecisionCall
	cacheLookups      []recordCacheLookupCall
	degraded          []recordReasonCall
	failClosed        []recordReasonCall
	approvalRequested []recordApprovalCall
	approvalResolved  []recordApprovalResolvedCall
	evaluateLatency   []recordEvaluateLatencyCall
	hookLatency       []recordHookLatencyCall
	shutdownForced    []string
}

type recordActionDecisionCall struct {
	tenant, layer, kind, decision, mode string
}

type recordCacheLookupCall struct {
	tenant, layer, kind, result string
}

type recordReasonCall struct {
	tenant, mode, component, reason string
}

type recordApprovalCall struct {
	tenant, layer, kind string
}

type recordApprovalResolvedCall struct {
	tenant, layer, kind, outcome string
}

type recordEvaluateLatencyCall struct {
	tenant, layer, kind, decision string
	duration                      time.Duration
}

type recordHookLatencyCall struct {
	tenant, hookEvent, decision string
	duration                    time.Duration
}

func (r *captureRecorder) RecordSessionCreated(string, string, string)         {}
func (r *captureRecorder) RecordSessionEnded(string, string, string)           {}
func (r *captureRecorder) SetSessionsActive(string, string, int)               {}
func (r *captureRecorder) RecordExecutionStarted(string, string, string)       {}
func (r *captureRecorder) RecordExecutionEnded(string, string, string)         {}
func (r *captureRecorder) RecordCreateExecutionAborted(string)                 {}
func (r *captureRecorder) ObserveSessionCleanupDuration(time.Duration)         {}
func (r *captureRecorder) AddSessionCleanupKeysDeleted(int)                    {}
func (r *captureRecorder) RecordSessionCleanupDeadline()                       {}
func (r *captureRecorder) RecordSessionEventCapRejected()                      {}
func (r *captureRecorder) RecordSessionSwept()                                 {}
func (r *captureRecorder) RecordEventPersisted(string, string, string, string) {}
func (r *captureRecorder) RecordEventRedacted(string)                          {}
func (r *captureRecorder) RecordHookTimeout(string)                            {}
func (r *captureRecorder) RecordApprovalEnqueueAborted(string)                 {}
func (r *captureRecorder) RecordAppendEventsAborted(string)                    {}
func (r *captureRecorder) RecordIdempotencyTTLExtended(string)                 {}
func (r *captureRecorder) RecordIdempotencyWindowExpired(string)               {}
func (r *captureRecorder) RecordAgentdResponseWriteAborted(string)             {}
func (r *captureRecorder) RecordEdgeExportRequestRejected(string)              {}
func (r *captureRecorder) RecordRedactionFailed(string, string)                {}
func (r *captureRecorder) RecordAgentdShutdownForced(reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.shutdownForced = append(r.shutdownForced, reason)
}

func (r *captureRecorder) shutdownForcedSnapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.shutdownForced...)
}

func (r *captureRecorder) RecordActionDecision(tenant, layer, kind, decision, mode string) {
	r.actionDecisions = append(r.actionDecisions, recordActionDecisionCall{tenant: tenant, layer: layer, kind: kind, decision: decision, mode: mode})
}

func (r *captureRecorder) RecordActionDenied(string, string, string, string) {}

func (r *captureRecorder) RecordApprovalRequested(tenant, layer, kind string) {
	r.approvalRequested = append(r.approvalRequested, recordApprovalCall{tenant: tenant, layer: layer, kind: kind})
}

func (r *captureRecorder) RecordApprovalResolved(tenant, layer, kind, outcome string) {
	r.approvalResolved = append(r.approvalResolved, recordApprovalResolvedCall{tenant: tenant, layer: layer, kind: kind, outcome: outcome})
}

func (r *captureRecorder) RecordDegraded(tenant, mode, component, reasonCode string) {
	r.degraded = append(r.degraded, recordReasonCall{tenant: tenant, mode: mode, component: component, reason: reasonCode})
}

func (r *captureRecorder) RecordFailClosed(tenant, mode, reasonCode string) {
	r.failClosed = append(r.failClosed, recordReasonCall{tenant: tenant, mode: mode, reason: reasonCode})
}

func (r *captureRecorder) RecordArtifactExport(string, string, string) {}

func (r *captureRecorder) ObserveHookLatency(tenant, hookEvent, decision string, duration time.Duration) {
	r.hookLatency = append(r.hookLatency, recordHookLatencyCall{tenant: tenant, hookEvent: hookEvent, decision: decision, duration: duration})
}

func (r *captureRecorder) ObserveEvaluateLatency(tenant, layer, kind, decision string, duration time.Duration) {
	r.evaluateLatency = append(r.evaluateLatency, recordEvaluateLatencyCall{tenant: tenant, layer: layer, kind: kind, decision: decision, duration: duration})
}

func (r *captureRecorder) RecordCacheLookup(tenant, layer, kind, result string) {
	r.cacheLookups = append(r.cacheLookups, recordCacheLookupCall{tenant: tenant, layer: layer, kind: kind, result: result})
}

func (r *captureRecorder) AddStreamClients(string, int) {}
func (r *captureRecorder) RecordStreamEventSent(string) {}
func (r *captureRecorder) RecordStreamDrop(string)      {}

func (r *captureRecorder) hasCacheResult(result string) bool {
	for _, call := range r.cacheLookups {
		if call.result == result {
			return true
		}
	}
	return false
}

func (r *captureRecorder) hasActionDecision(decision string) bool {
	for _, call := range r.actionDecisions {
		if call.decision == decision {
			return true
		}
	}
	return false
}

func (r *captureRecorder) hasDegradedReason(reason string) bool {
	for _, call := range r.degraded {
		if call.reason == reason {
			return true
		}
	}
	return false
}

func (r *captureRecorder) hasFailClosedReason(reason string) bool {
	for _, call := range r.failClosed {
		if call.reason == reason {
			return true
		}
	}
	return false
}

func (r *captureRecorder) hasApprovalResolved(outcome string) bool {
	for _, call := range r.approvalResolved {
		if call.outcome == outcome {
			return true
		}
	}
	return false
}
