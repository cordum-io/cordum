package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
)

type hookRecordingRecorder struct {
	edgecore.NoopRecorder
	actionDecisions []hookActionDecisionCall
	actionDenied    []hookActionDeniedCall
	hookLatency     []hookLatencyCall
	degraded        []hookReasonCall
	failClosed      []hookReasonCall
	hookTimeout     []string
}

type hookActionDecisionCall struct {
	tenant, layer, kind, decision, mode string
}

type hookActionDeniedCall struct {
	tenant, layer, kind, reason string
}

type hookLatencyCall struct {
	tenant, hookEvent, decision string
	duration                    time.Duration
}

type hookReasonCall struct {
	tenant, mode, component, reason string
}

func (r *hookRecordingRecorder) RecordActionDecision(tenant, layer, kind, decision, mode string) {
	r.actionDecisions = append(r.actionDecisions, hookActionDecisionCall{tenant: tenant, layer: layer, kind: kind, decision: decision, mode: mode})
}

func (r *hookRecordingRecorder) RecordActionDenied(tenant, layer, kind, reason string) {
	r.actionDenied = append(r.actionDenied, hookActionDeniedCall{tenant: tenant, layer: layer, kind: kind, reason: reason})
}

func (r *hookRecordingRecorder) ObserveHookLatency(tenant, hookEvent, decision string, duration time.Duration) {
	r.hookLatency = append(r.hookLatency, hookLatencyCall{tenant: tenant, hookEvent: hookEvent, decision: decision, duration: duration})
}

func (r *hookRecordingRecorder) RecordDegraded(tenant, mode, component, reason string) {
	r.degraded = append(r.degraded, hookReasonCall{tenant: tenant, mode: mode, component: component, reason: reason})
}

func (r *hookRecordingRecorder) RecordFailClosed(tenant, mode, reason string) {
	r.failClosed = append(r.failClosed, hookReasonCall{tenant: tenant, mode: mode, reason: reason})
}

func (r *hookRecordingRecorder) RecordHookTimeout(phase string) {
	r.hookTimeout = append(r.hookTimeout, phase)
}

type fakeAgentdClient struct {
	calls int
	fn    func(context.Context, AgentdRequest) (AgentdDecision, error)
}

func (f *fakeAgentdClient) EvaluateHook(ctx context.Context, req AgentdRequest) (AgentdDecision, error) {
	f.calls++
	if f.fn == nil {
		return AgentdDecision{Decision: DecisionAllow, Reason: "allowed by policy"}, nil
	}
	return f.fn(ctx, req)
}

func TestRunPreToolUseAllowWritesOnlyClaudeDecisionJSON(t *testing.T) {
	stdin := hookInput(`{
		"hook_event_name":"PreToolUse",
		"session_id":"sess-123",
		"cwd":"/repo",
		"permission_mode":"default",
		"tool_name":"Bash",
		"tool_use_id":"toolu_123",
		"tool_input":{"command":"npm test"}
	}`)
	agentd := &fakeAgentdClient{fn: func(ctx context.Context, req AgentdRequest) (AgentdDecision, error) {
		if req.EventName != "PreToolUse" || req.ToolName != "Bash" || req.SessionID != "sess-123" || req.ToolUseID != "toolu_123" {
			t.Fatalf("unexpected agentd request: %#v", req)
		}
		if len(req.RawPayload) == 0 || !bytes.Contains(req.RawPayload, []byte(`"npm test"`)) {
			t.Fatalf("raw payload was not forwarded in-memory to local agentd: %q", req.RawPayload)
		}
		return AgentdDecision{Decision: DecisionAllow, Reason: "allowed by policy"}, nil
	}}

	code, stdout, stderr := runHook(t, RunOptions{
		Args:   []string{"claude", "pre-tool-use"},
		Stdin:  stdin,
		Agentd: agentd,
	})

	if code != 0 {
		t.Fatalf("exit code = %d, stderr=%q", code, stderr)
	}
	assertCompactJSON(t, stdout, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"allowed by policy"}}`)
	if stderr != "" {
		t.Fatalf("stderr should be empty for clean allow, got %q", stderr)
	}
	if agentd.calls != 1 {
		t.Fatalf("agentd calls = %d, want 1", agentd.calls)
	}
}

func TestRunRecordsHookObservabilityForAgentdDecision(t *testing.T) {
	recorder := &hookRecordingRecorder{}
	agentd := &fakeAgentdClient{fn: func(context.Context, AgentdRequest) (AgentdDecision, error) {
		return AgentdDecision{Decision: DecisionAllow, Reason: "allowed by policy"}, nil
	}}

	code, stdout, stderr := runHook(t, RunOptions{
		Args:     []string{"claude", "pre-tool-use"},
		Stdin:    hookInput(`{"hook_event_name":"PreToolUse","session_id":"sess-secret-should-not-be-label","tool_name":"Bash","tool_input":{"command":"npm test"}}`),
		Agentd:   agentd,
		Recorder: recorder,
		Env: map[string]string{
			"CORDUM_TENANT_ID":         "tenant-edge014",
			"CORDUM_EDGE_SESSION_ID":   "edge_sess_hook_metrics",
			"CORDUM_EDGE_EXECUTION_ID": "edge_exec_hook_metrics",
			"CORDUM_EDGE_MODE":         "local-dev",
		},
	})

	if code != 0 {
		t.Fatalf("exit code = %d, stderr=%q", code, stderr)
	}
	assertCompactJSON(t, stdout, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"allowed by policy"}}`)
	if stderr != "" {
		t.Fatalf("stderr should stay empty for clean allow, got %q", stderr)
	}
	if len(recorder.actionDecisions) != 1 {
		t.Fatalf("action decision calls = %#v, want one", recorder.actionDecisions)
	}
	if got := recorder.actionDecisions[0]; got != (hookActionDecisionCall{
		tenant:   "tenant-edge014",
		layer:    "hook",
		kind:     "hook.pre_tool_use",
		decision: "allow",
		mode:     "local-dev",
	}) {
		t.Fatalf("action decision call = %#v", got)
	}
	if len(recorder.hookLatency) != 1 {
		t.Fatalf("hook latency calls = %#v, want one", recorder.hookLatency)
	}
	if got := recorder.hookLatency[0]; got.tenant != "tenant-edge014" || got.hookEvent != "PreToolUse" || got.decision != "allow" || got.duration < 0 {
		t.Fatalf("hook latency call = %#v", got)
	}
	if len(recorder.degraded) != 0 || len(recorder.failClosed) != 0 || len(recorder.actionDenied) != 0 {
		t.Fatalf("unexpected error-path metrics denied=%#v degraded=%#v failClosed=%#v", recorder.actionDenied, recorder.degraded, recorder.failClosed)
	}
}

func TestRunRecordsHookObservabilityForFailClosedAgentdOutage(t *testing.T) {
	recorder := &hookRecordingRecorder{}
	agentd := &fakeAgentdClient{fn: func(context.Context, AgentdRequest) (AgentdDecision, error) {
		return AgentdDecision{}, errors.New("agentd unavailable with Bearer sk-test-secret")
	}}

	code, stdout, stderr := runHook(t, RunOptions{
		Args:     []string{"claude", "pre-tool-use"},
		Stdin:    hookInput(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rm -rf /tmp/secret"}}`),
		Agentd:   agentd,
		Recorder: recorder,
		Env: map[string]string{
			"CORDUM_TENANT_ID":          "tenant-edge014",
			"CORDUM_EDGE_MODE":          "enterprise-strict",
			"CORDUM_AGENTD_FAIL_CLOSED": "true",
		},
	})

	if code != 0 {
		t.Fatalf("exit code = %d, stderr=%q", code, stderr)
	}
	assertCompactJSON(t, stdout, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Cordum Edge unavailable; blocking by fail-closed policy"}}`)
	assertNoSyntheticSecrets(t, stdout+stderr)
	if len(recorder.actionDecisions) != 1 || recorder.actionDecisions[0].decision != "deny" || recorder.actionDecisions[0].kind != "hook.pre_tool_use" {
		t.Fatalf("action decision calls = %#v, want deny hook.pre_tool_use", recorder.actionDecisions)
	}
	if len(recorder.actionDenied) != 1 || recorder.actionDenied[0].reason != "agentd_unavailable" {
		t.Fatalf("action denied calls = %#v, want agentd_unavailable", recorder.actionDenied)
	}
	if len(recorder.degraded) != 1 || recorder.degraded[0].component != "hook" || recorder.degraded[0].reason != "agentd_unavailable" {
		t.Fatalf("degraded calls = %#v, want hook/agentd_unavailable", recorder.degraded)
	}
	if len(recorder.failClosed) != 1 || recorder.failClosed[0].reason != "agentd_unavailable" {
		t.Fatalf("fail-closed calls = %#v, want agentd_unavailable", recorder.failClosed)
	}
	if len(recorder.hookLatency) != 1 || recorder.hookLatency[0].decision != "deny" || recorder.hookLatency[0].hookEvent != "PreToolUse" {
		t.Fatalf("hook latency calls = %#v, want PreToolUse deny", recorder.hookLatency)
	}
}

func TestRunRecordsHookTimeoutMetricForAgentdDeadline(t *testing.T) {
	recorder := &hookRecordingRecorder{}
	agentd := &fakeAgentdClient{fn: func(context.Context, AgentdRequest) (AgentdDecision, error) {
		return AgentdDecision{}, context.DeadlineExceeded
	}}

	code, _, stderr := runHook(t, RunOptions{
		Args:     []string{"claude", "pre-tool-use"},
		Stdin:    hookInput(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"npm test"}}`),
		Agentd:   agentd,
		Recorder: recorder,
		Env: map[string]string{
			"CORDUM_AGENTD_FAIL_CLOSED": "true",
		},
	})

	if code != 0 {
		t.Fatalf("exit code = %d, stderr=%q", code, stderr)
	}
	if len(recorder.hookTimeout) != 1 || recorder.hookTimeout[0] != "gateway" {
		t.Fatalf("hook timeout calls = %#v, want [gateway]", recorder.hookTimeout)
	}
	if len(recorder.actionDenied) != 1 || recorder.actionDenied[0].reason != "agentd_timeout" {
		t.Fatalf("action denied calls = %#v, want agentd_timeout", recorder.actionDenied)
	}
}

func TestRunPreToolUseDenyWritesDenyReasonForClaude(t *testing.T) {
	agentd := &fakeAgentdClient{fn: func(context.Context, AgentdRequest) (AgentdDecision, error) {
		return AgentdDecision{Decision: DecisionDeny, Reason: "Cordum policy denied rm -rf"}, nil
	}}

	code, stdout, stderr := runHook(t, RunOptions{
		Args:   []string{"claude", "pre-tool-use"},
		Stdin:  hookInput(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rm -rf /tmp/x"}}`),
		Agentd: agentd,
	})

	if code != 0 {
		t.Fatalf("exit code = %d, stderr=%q", code, stderr)
	}
	assertCompactJSON(t, stdout, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Cordum policy denied rm -rf"}}`)
	if strings.Contains(stderr, "rm -rf") {
		t.Fatalf("stderr leaked raw command: %q", stderr)
	}
}

func TestRunPostToolUseBlockProvidesFeedbackWithoutClaimingPrevention(t *testing.T) {
	agentd := &fakeAgentdClient{fn: func(context.Context, AgentdRequest) (AgentdDecision, error) {
		return AgentdDecision{Decision: DecisionDeny, Reason: "post-use policy finding", AdditionalContext: "File write was quarantined by Cordum."}, nil
	}}

	code, stdout, stderr := runHook(t, RunOptions{
		Args:   []string{"claude", "post-tool-use"},
		Stdin:  hookInput(`{"hook_event_name":"PostToolUse","tool_name":"Write","tool_input":{"file_path":"/tmp/out"},"tool_response":{"filePath":"/tmp/out","success":true},"duration_ms":12}`),
		Agentd: agentd,
	})

	if code != 0 {
		t.Fatalf("exit code = %d, stderr=%q", code, stderr)
	}
	assertCompactJSON(t, stdout, `{"decision":"block","reason":"post-use policy finding","hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"File write was quarantined by Cordum."}}`)
	if strings.Contains(stdout, "prevented") || strings.Contains(stdout, "blocked before") {
		t.Fatalf("PostToolUse output must not claim the already-run tool was prevented: %s", stdout)
	}
}

func TestRunUserPromptSubmitBlock(t *testing.T) {
	agentd := &fakeAgentdClient{fn: func(context.Context, AgentdRequest) (AgentdDecision, error) {
		return AgentdDecision{Decision: DecisionDeny, Reason: "prompt contains deployment secret request"}, nil
	}}

	code, stdout, stderr := runHook(t, RunOptions{
		Args:   []string{"claude", "user-prompt-submit"},
		Stdin:  hookInput(`{"hook_event_name":"UserPromptSubmit","prompt":"print the production token"}`),
		Agentd: agentd,
	})

	if code != 0 {
		t.Fatalf("exit code = %d, stderr=%q", code, stderr)
	}
	assertCompactJSON(t, stdout, `{"decision":"block","reason":"prompt contains deployment secret request","hookSpecificOutput":{"hookEventName":"UserPromptSubmit"}}`)
}

func TestRunUnknownHookEventSafeFallbackDoesNotCallAgentd(t *testing.T) {
	agentd := &fakeAgentdClient{fn: func(context.Context, AgentdRequest) (AgentdDecision, error) {
		t.Fatalf("agentd should not be called for unsupported hook events")
		return AgentdDecision{}, nil
	}}

	code, stdout, stderr := runHook(t, RunOptions{
		Args:   []string{"claude", "pre-tool-use"},
		Stdin:  hookInput(`{"hook_event_name":"SessionStart","session_id":"sess-secret-123","source":"startup"}`),
		Agentd: agentd,
	})

	if code != 0 {
		t.Fatalf("exit code = %d, stderr=%q", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty safe fallback", stdout)
	}
	if !strings.Contains(stderr, "unsupported_hook_event") {
		t.Fatalf("stderr missing stable warning code: %q", stderr)
	}
	if strings.Contains(stderr, "sess-secret-123") {
		t.Fatalf("stderr leaked raw session id: %q", stderr)
	}
	if agentd.calls != 0 {
		t.Fatalf("agentd calls = %d, want 0", agentd.calls)
	}
}

func TestRunRejectsMalformedJSONWithEmptyStdout(t *testing.T) {
	code, stdout, stderr := runHook(t, RunOptions{
		Args:  []string{"claude", "pre-tool-use"},
		Stdin: hookInput(`{"hook_event_name":"PreToolUse","tool_input":{"command":"rm -rf /tmp/secret"}`),
	})

	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "invalid_hook_json") {
		t.Fatalf("stderr missing stable code: %q", stderr)
	}
	if strings.Contains(stderr, "rm -rf") || strings.Contains(stderr, "secret") {
		t.Fatalf("stderr leaked raw malformed payload: %q", stderr)
	}
}

func TestRunRejectsOversizeStdinWithEmptyStdout(t *testing.T) {
	code, stdout, stderr := runHook(t, RunOptions{
		Args:          []string{"claude", "pre-tool-use"},
		Stdin:         strings.NewReader(`{"hook_event_name":"PreToolUse","padding":"` + strings.Repeat("x", 64) + `"}`),
		MaxInputBytes: 32,
	})

	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "hook_input_too_large") {
		t.Fatalf("stderr missing oversize code: %q", stderr)
	}
	if len(stderr) > 512 {
		t.Fatalf("stderr should be bounded, got len=%d: %q", len(stderr), stderr)
	}
}

func TestRunHonorsContextCancellationDuringInputRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	code, stdout, stderr := runHookWithContext(t, ctx, RunOptions{
		Args:  []string{"claude", "pre-tool-use"},
		Stdin: blockingReader{},
	})

	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "hook_input_timeout") {
		t.Fatalf("stderr missing timeout code: %q", stderr)
	}
}

func TestRunStrictModeDeniesWhenAgentdUnavailable(t *testing.T) {
	agentd := &fakeAgentdClient{fn: func(context.Context, AgentdRequest) (AgentdDecision, error) {
		return AgentdDecision{}, errors.New("agentd unavailable with bearer sk-test-secret")
	}}

	code, stdout, stderr := runHook(t, RunOptions{
		Args:   []string{"claude", "pre-tool-use"},
		Stdin:  hookInput(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"npm test"}}`),
		Agentd: agentd,
		Env:    map[string]string{"CORDUM_AGENTD_FAIL_CLOSED": "true"},
	})

	if code != 0 {
		t.Fatalf("exit code = %d, stderr=%q", code, stderr)
	}
	assertCompactJSON(t, stdout, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Cordum Edge unavailable; blocking by fail-closed policy"}}`)
	if !strings.Contains(stderr, "agentd_unavailable") {
		t.Fatalf("stderr missing stable outage code: %q", stderr)
	}
	assertNoSyntheticSecrets(t, stderr)
}

func TestRunStrictModeDeniesWhenAgentdTimesOut(t *testing.T) {
	agentd := &fakeAgentdClient{fn: func(ctx context.Context, req AgentdRequest) (AgentdDecision, error) {
		<-ctx.Done()
		return AgentdDecision{}, ctx.Err()
	}}

	// 50ms is enough for the in-memory hook input to parse, then the fake
	// agentd waits for ctx.Done() so the timeout is consumed by the agentd
	// call — the path this test is asserting. A nanosecond-scale timeout
	// would trip during stdin parsing before reaching the agentd child budget.
	code, stdout, stderr := runHook(t, RunOptions{
		Args:    []string{"claude", "pre-tool-use"},
		Stdin:   hookInput(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"npm test"}}`),
		Agentd:  agentd,
		Env:     map[string]string{"CORDUM_AGENTD_FAIL_CLOSED": "true"},
		Timeout: 50 * time.Millisecond,
	})

	if code != 0 {
		t.Fatalf("exit code = %d, stderr=%q", code, stderr)
	}
	assertCompactJSON(t, stdout, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Cordum Edge timeout; blocking by fail-closed policy"}}`)
	if !strings.Contains(stderr, "agentd_timeout") {
		t.Fatalf("stderr missing timeout code: %q", stderr)
	}
}

func TestRunRejectsHookTimeoutAtClaudeDeadline(t *testing.T) {
	agentd := &fakeAgentdClient{}
	code, stdout, stderr := runHook(t, RunOptions{
		Args:   []string{"claude", "pre-tool-use"},
		Stdin:  hookInput(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"npm test"}}`),
		Agentd: agentd,
		Env: map[string]string{
			"CORDUM_AGENTD_HOOK_TIMEOUT": "5s",
			"CORDUM_AGENTD_FAIL_CLOSED":  "true",
		},
	})

	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stderr=%q", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if agentd.calls != 0 {
		t.Fatalf("agentd calls = %d, want 0 before timeout config is fixed", agentd.calls)
	}
	if !strings.Contains(stderr, "invalid_hook_timeout") || !strings.Contains(stderr, "strictly below") {
		t.Fatalf("stderr missing clear timeout validation error: %q", stderr)
	}
}

func TestSlowAgentdDoesNotConsumeResponseWriteReserve(t *testing.T) {
	var agentdElapsed time.Duration
	agentd := &fakeAgentdClient{fn: func(ctx context.Context, req AgentdRequest) (AgentdDecision, error) {
		started := time.Now()
		<-ctx.Done()
		agentdElapsed = time.Since(started)
		return AgentdDecision{}, ctx.Err()
	}}

	started := time.Now()
	code, stdout, stderr := runHook(t, RunOptions{
		Args:   []string{"claude", "pre-tool-use"},
		Stdin:  hookInput(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"npm test"}}`),
		Agentd: agentd,
		Env:    map[string]string{"CORDUM_AGENTD_FAIL_CLOSED": "true"},
	})
	elapsed := time.Since(started)

	if code != 0 {
		t.Fatalf("exit code = %d, stderr=%q", code, stderr)
	}
	assertCompactJSON(t, stdout, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Cordum Edge timeout; blocking by fail-closed policy"}}`)
	if !strings.Contains(stderr, "agentd_timeout") {
		t.Fatalf("stderr missing timeout code: %q", stderr)
	}
	if agentdElapsed < DefaultAgentdPostBudget-500*time.Millisecond {
		t.Fatalf("agentd timeout fired too early after %s", agentdElapsed)
	}
	if agentdElapsed >= DefaultHookTimeout-(ResponseWriteReserve/4) {
		t.Fatalf("agentd consumed response-write reserve: agentd=%s hook=%s reserve=%s", agentdElapsed, DefaultHookTimeout, ResponseWriteReserve)
	}
	if elapsed >= ClaudeHookDeadline {
		t.Fatalf("hook elapsed = %s, want under Claude deadline %s", elapsed, ClaudeHookDeadline)
	}
}

func runHook(t *testing.T, opts RunOptions) (int, string, string) {
	t.Helper()
	return runHookWithContext(t, context.Background(), opts)
}

func runHookWithContext(t *testing.T, ctx context.Context, opts RunOptions) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	opts.Stdout = &stdout
	opts.Stderr = &stderr
	code := Run(ctx, opts)
	return code, stdout.String(), stderr.String()
}

func hookInput(s string) io.Reader { return strings.NewReader(s) }

type blockingReader struct{}

func (blockingReader) Read([]byte) (int, error) {
	select {}
}

func assertCompactJSON(t *testing.T, got, want string) {
	t.Helper()
	var gotBuf, wantBuf bytes.Buffer
	if err := json.Compact(&gotBuf, []byte(got)); err != nil {
		t.Fatalf("stdout is not valid JSON: %v; raw=%q", err, got)
	}
	if err := json.Compact(&wantBuf, []byte(want)); err != nil {
		t.Fatalf("test expected JSON is invalid: %v", err)
	}
	if gotBuf.String() != wantBuf.String() {
		t.Fatalf("JSON stdout mismatch\n got: %s\nwant: %s", gotBuf.String(), wantBuf.String())
	}
}

func assertNoSyntheticSecrets(t *testing.T, text string) {
	t.Helper()
	for _, secret := range []string{"sk-test-secret", "ghp_testtoken", "AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"} {
		if strings.Contains(text, secret) {
			t.Fatalf("synthetic secret %q leaked in %q", secret, text)
		}
	}
}
