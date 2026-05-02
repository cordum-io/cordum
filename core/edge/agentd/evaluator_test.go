package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
	if event.EventID != "evt-eval-decision" || event.Kind != edgecore.EventKindHookPolicyDecision || event.Decision != edgecore.DecisionAllow {
		t.Fatalf("decision event = %#v", event)
	}
	payload, _ := json.Marshal(event)
	for _, forbidden := range []string{"raw-evaluator-secret", "evaluator-secret", "secret-transcript", `C:\\Users\\yaron`} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("decision event leaked %q: %s", forbidden, payload)
		}
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

type stubAgentdClientFunc func(context.Context, claude.AgentdRequest) (claude.AgentdDecision, error)

func (f stubAgentdClientFunc) EvaluateHook(ctx context.Context, req claude.AgentdRequest) (claude.AgentdDecision, error) {
	return f(ctx, req)
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
	actionDecisions   []recordActionDecisionCall
	cacheLookups      []recordCacheLookupCall
	degraded          []recordReasonCall
	failClosed        []recordReasonCall
	approvalRequested []recordApprovalCall
	approvalResolved  []recordApprovalResolvedCall
	evaluateLatency   []recordEvaluateLatencyCall
	hookLatency       []recordHookLatencyCall
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

func (r *captureRecorder) RecordSessionCreated(string, string, string)   {}
func (r *captureRecorder) RecordSessionEnded(string, string, string)     {}
func (r *captureRecorder) SetSessionsActive(string, string, int)         {}
func (r *captureRecorder) RecordExecutionStarted(string, string, string) {}
func (r *captureRecorder) RecordExecutionEnded(string, string, string)   {}

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
