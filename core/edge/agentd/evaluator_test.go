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
