package edge

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

// TestNoopRecorderImplementsRecorder pins that the no-op recorder
// satisfies the Recorder interface so callers can wire it as a default
// without depending on the Prometheus implementation that lands in step-7.
func TestNoopRecorderImplementsRecorder(t *testing.T) {
	var r Recorder = NewNoopRecorder()
	// Exercise every method to catch interface drift early.
	r.RecordSessionCreated("tenant-a", "local-dev", "claude-code")
	r.RecordSessionEnded("tenant-a", "local-dev", "ended")
	r.SetSessionsActive("tenant-a", "local-dev", 3)
	r.RecordExecutionStarted("tenant-a", "local-dev", "claude-code")
	r.RecordExecutionEnded("tenant-a", "local-dev", "succeeded")
	r.RecordActionDecision("tenant-a", "hook", "hook.pre_tool_use", "allow", "local-dev")
	r.RecordActionDenied("tenant-a", "hook", "hook.pre_tool_use", "destructive_command")
	r.RecordApprovalRequested("tenant-a", "hook", "hook.pre_tool_use")
	r.RecordApprovalResolved("tenant-a", "hook", "hook.pre_tool_use", "approved")
	r.RecordDegraded("tenant-a", "local-dev", "agentd", "gateway_unavailable")
	r.RecordFailClosed("tenant-a", "enterprise-strict", "gateway_unavailable")
	r.RecordArtifactExport("tenant-a", "edge.session_export", "ok")
	r.ObserveHookLatency("tenant-a", "PreToolUse", "allow", 50*time.Millisecond)
	r.ObserveEvaluateLatency("tenant-a", "hook", "hook.pre_tool_use", "allow", 25*time.Millisecond)
	r.RecordCacheLookup("tenant-a", "hook", "hook.pre_tool_use", "hit")
	r.AddStreamClients("tenant-a", 1)
	r.RecordStreamDrop("client_buffer_full")
}

// TestNormalizeDecisionBoundsLabelCardinality pins the decision-label
// allowlist. Arbitrary or future-enum strings MUST collapse to "other"
// (or "unknown" for empty). High-cardinality input like raw command
// strings, error messages, or user-supplied enum-shaped values MUST NEVER
// appear in metric label output.
func TestNormalizeDecisionBoundsLabelCardinality(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		// Allowed values (case-insensitive).
		{"allow", "allow"},
		{"ALLOW", "allow"},
		{"Allow ", "allow"},
		{"deny", "deny"},
		{"DENY", "deny"},
		{"require_approval", "require_approval"},
		{"REQUIRE_APPROVAL", "require_approval"},
		{"throttle", "throttle"},
		{"constrain", "constrain"},
		{"degraded", "degraded"},
		{"recorded", "recorded"},
		// Empty -> unknown.
		{"", "unknown"},
		{" ", "unknown"},
		// Disallowed -> other.
		{"banana", "other"},
		{"rm -rf /tmp/xyz", "other"},
		{"sk-test-secret-leaked", "other"},
		{"Bearer abc.def.ghi", "other"},
		{"Authorization: Bearer ...", "other"},
		{"deny\nallow", "deny"}, // newline-truncates per lowerTrim
	} {
		t.Run(tc.input, func(t *testing.T) {
			if got := NormalizeDecision(tc.input); got != tc.want {
				t.Errorf("NormalizeDecision(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestNormalizeLayerBoundsLabelCardinality(t *testing.T) {
	for _, tc := range []struct {
		input, want string
	}{
		{"hook", "hook"},
		{"HOOK", "hook"},
		{"mcp", "mcp"},
		{"llm", "llm"},
		{"runtime", "runtime"},
		{"workflow", "workflow"},
		{"system", "system"},
		{"", "unknown"},
		{"banana", "other"},
		{"hook; DROP TABLE sessions", "other"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			if got := NormalizeLayer(tc.input); got != tc.want {
				t.Errorf("NormalizeLayer(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestNormalizeKindBoundsLabelCardinality(t *testing.T) {
	// Allowed kinds are the documented prefixes.
	for _, allowed := range []string{
		"hook.pre_tool_use",
		"hook.post_tool_use",
		"hook.user_prompt_submit",
		"session.started",
		"execution.ended",
		"mcp.tool_call",
		"approval.requested",
		"runtime.process_exec",
	} {
		if got := NormalizeKind(allowed); got != allowed {
			t.Errorf("NormalizeKind(%q) = %q, want passthrough", allowed, got)
		}
	}
	// Disallowed shapes (no prefix match, raw command, free-form
	// reason strings) MUST collapse to "other".
	for _, disallowed := range []string{
		"unknown_kind",
		"rm -rf /tmp/data",
		"Authorization: Bearer secret",
		"sql injection attempt",
		"sk-test-token-leaked",
	} {
		if got := NormalizeKind(disallowed); got != "other" {
			t.Errorf("NormalizeKind(%q) = %q, want other", disallowed, got)
		}
	}
	if got := NormalizeKind(""); got != "unknown" {
		t.Errorf("NormalizeKind(\"\") = %q, want unknown", got)
	}
}

func TestNormalizeApprovalOutcomeBoundsLabelCardinality(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"approved", "approved"},
		{"APPROVED", "approved"},
		{"rejected", "rejected"},
		{"expired", "expired"},
		{"timeout", "timeout"},
		{"invalidated", "invalidated"},
		{"consumed", "consumed"},
		{"", "unknown"},
		{"banana", "other"},
	} {
		if got := NormalizeApprovalOutcome(tc.input); got != tc.want {
			t.Errorf("NormalizeApprovalOutcome(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestNormalizeStreamDropReasonBoundsLabelCardinality(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"marshal_error", "marshal_error"},
		{"client_buffer_full", "client_buffer_full"},
		{"tenant_filter", "tenant_filter"},
		{"stopped", "stopped"},
		{"", "unknown"},
		{"network read error: connection reset by peer", "other"},
		{"sk-test-token-leaked-as-reason", "other"},
	} {
		if got := NormalizeStreamDropReason(tc.input); got != tc.want {
			t.Errorf("NormalizeStreamDropReason(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// TestEventLogAttrsEmitsOnlyBoundedFields pins the EDGE-014 step-4 log
// attribute contract: EventLogAttrs returns only safe Edge IDs, normalized
// layer/kind, bounded tool_name/decision/status, and input_hash/duration.
// No raw secret-shaped value injected anywhere in the source AgentActionEvent
// (Decision, Status, Reason, Labels, InputRedacted, ToolName) may appear in
// the resulting slog.Attr slice.
func TestEventLogAttrsEmitsOnlyBoundedFields(t *testing.T) {
	const rawSecret = "Authorization: Bearer edge014-log-attr-secret-xyz"
	event := AgentActionEvent{
		EventID:     "evt-edge014-attr-1",
		SessionID:   "edge_sess_attr",
		ExecutionID: "edge_exec_attr",
		TenantID:    "tenant-edge014",
		PrincipalID: "principal-edge014",
		Timestamp:   time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC),
		Layer:       LayerHook,
		Kind:        EventKindHookPreToolUse,
		ToolName:    "Bash",
		ActionName:  "bash.exec",
		Capability:  "exec.shell",
		RiskTags:    []string{"exec"},
		Labels:      Labels{"command.class": "safe", "raw": rawSecret},
		InputRedacted: map[string]any{
			"command": rawSecret,
		},
		Decision:   "ALLOW",
		Status:     ActionStatusOK,
		InputHash:  "sha256:abcdef0123456789",
		DurationMS: 142,
	}

	attrs := EventLogAttrs(event)
	gotKeys := make(map[string]any, len(attrs))
	var rendered strings.Builder
	for _, a := range attrs {
		gotKeys[a.Key] = a.Value.Any()
		rendered.WriteString(a.Key)
		rendered.WriteString("=")
		rendered.WriteString(a.Value.String())
		rendered.WriteString(";")
	}
	out := rendered.String()

	for _, want := range []string{"tenant_id", "session_id", "execution_id", "event_id", "layer", "kind", "tool_name", "decision", "status", "input_hash", "duration_ms"} {
		if _, ok := gotKeys[want]; !ok {
			t.Errorf("EventLogAttrs missing required key %q; rendered=%s", want, out)
		}
	}

	for _, marker := range []string{rawSecret, "Authorization", "Bearer ", "command", "raw", "input_redacted", "labels", "principal_id", "risk_tags", "capability", "action_name"} {
		if strings.Contains(out, marker) {
			t.Errorf("EventLogAttrs leaked %q in attrs: %s", marker, out)
		}
	}

	if got := gotKeys["decision"]; got != "allow" {
		t.Errorf("decision attr = %v, want lowercase normalized 'allow'", got)
	}
	if got := gotKeys["layer"]; got != "hook" {
		t.Errorf("layer attr = %v, want 'hook'", got)
	}
	if got := gotKeys["kind"]; got != "hook.pre_tool_use" {
		t.Errorf("kind attr = %v, want 'hook.pre_tool_use'", got)
	}
}

// TestEventLogAttrsBoundsHugeIDs proves EventLogAttrs clamps malicious /
// pathological ID lengths so a single log line can't blow up.
func TestEventLogAttrsBoundsHugeIDs(t *testing.T) {
	hugeID := strings.Repeat("a", 4096)
	event := AgentActionEvent{
		TenantID:  hugeID,
		SessionID: hugeID,
		Layer:     LayerHook,
		Kind:      EventKindHookPreToolUse,
	}
	attrs := EventLogAttrs(event)
	for _, a := range attrs {
		s := a.Value.String()
		if len(s) > 200 {
			t.Errorf("attr %q value len = %d > 200; bounded ID expected", a.Key, len(s))
		}
	}
}

// TestEventLogAttrsCollapsesUntrustedDecision proves a free-form
// Decision value (e.g. an attacker-supplied "Authorization: Bearer ...")
// collapses to "other" via NormalizeDecision and never reaches the log
// attribute as a raw value.
func TestEventLogAttrsCollapsesUntrustedDecision(t *testing.T) {
	event := AgentActionEvent{
		TenantID: "tenant-edge014",
		Layer:    LayerHook,
		Kind:     EventKindHookPreToolUse,
		Decision: "Authorization: Bearer attacker-token-xyz",
	}
	attrs := EventLogAttrs(event)
	for _, a := range attrs {
		if a.Key != "decision" {
			continue
		}
		got := a.Value.String()
		if got == string(event.Decision) || strings.Contains(got, "Bearer") {
			t.Fatalf("decision attr leaked raw input: %q", got)
		}
		if got != "other" {
			t.Fatalf("decision attr = %q, want collapsed 'other'", got)
		}
	}
}

// TestSessionLogAttrsEmitsOnlyBoundedFields mirrors the AgentActionEvent
// test for EdgeSession. Inject synthetic secrets into AgentVersion (a
// free-form-ish field) and Mode and assert nothing leaks.
func TestSessionLogAttrsEmitsOnlyBoundedFields(t *testing.T) {
	const rawSecret = "ghp_edge014-session-attr-leak-token-abcdef"
	session := EdgeSession{
		TenantID:     "tenant-edge014",
		SessionID:    "edge_sess_session_attr",
		AgentProduct: "claude-code",
		AgentVersion: rawSecret,
		Mode:         "local-dev",
		Status:       SessionStatusRunning,
		StartedAt:    time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC),
	}
	attrs := SessionLogAttrs(session)
	var rendered strings.Builder
	for _, a := range attrs {
		rendered.WriteString(a.Key)
		rendered.WriteString("=")
		rendered.WriteString(a.Value.String())
		rendered.WriteString(";")
	}
	if strings.Contains(rendered.String(), rawSecret) {
		t.Errorf("SessionLogAttrs included raw AgentVersion secret: %s", rendered.String())
	}
	if strings.Contains(rendered.String(), "ghp_") {
		t.Errorf("SessionLogAttrs leaked github token marker: %s", rendered.String())
	}
}

// emitAttrsToHandler is a tiny helper used by TestEventLogAttrsThroughSlog
// to exercise the full slog pipeline (so any surprises in attribute
// rendering surface in tests). It returns the rendered text.
func emitAttrsToHandler(attrs []slog.Attr, msg string) string {
	var buf strings.Builder
	h := slog.NewTextHandler(&buf, nil)
	logger := slog.New(h)
	args := make([]any, 0, len(attrs)*2)
	for _, a := range attrs {
		args = append(args, a)
	}
	logger.Info(msg, args...)
	return buf.String()
}

// TestEventLogAttrsThroughSlog runs EventLogAttrs through a real
// slog.TextHandler and asserts the rendered line carries the bounded
// keys and never the raw secret.
func TestEventLogAttrsThroughSlog(t *testing.T) {
	const rawSecret = "sk-edge014-slog-pipeline-secret"
	event := AgentActionEvent{
		EventID:     "evt-edge014-slog-1",
		SessionID:   "edge_sess_slog",
		ExecutionID: "edge_exec_slog",
		TenantID:    "tenant-edge014",
		Layer:       LayerHook,
		Kind:        EventKindHookPreToolUse,
		ToolName:    "Bash",
		Decision:    DecisionAllow,
		Status:      ActionStatusOK,
		InputHash:   "sha256:" + strings.Repeat("a", 64),
		Labels:      Labels{"command.class": "safe", "leak": rawSecret},
		InputRedacted: map[string]any{
			"command": rawSecret,
		},
	}
	out := emitAttrsToHandler(EventLogAttrs(event), "edge action")
	if strings.Contains(out, rawSecret) {
		t.Fatalf("slog output leaked raw secret: %s", out)
	}
	for _, want := range []string{"tenant_id=", "session_id=", "execution_id=", "event_id=", "layer=hook", "kind=hook.pre_tool_use", "tool_name=Bash", "decision=allow", "status=ok", "input_hash=sha256:"} {
		if !strings.Contains(out, want) {
			t.Errorf("slog output missing %q in: %s", want, out)
		}
	}
}

// TestRecorderInterfaceForbidsRawSecretLeak documents the contract that
// raw secret-shaped inputs MUST collapse to bounded labels via the
// Normalize* helpers before reaching a Prometheus recorder. The no-op
// recorder accepts any input (it does nothing); the test pins the
// invariant via the normalizers, which the step-7 Prometheus recorder
// MUST call before forwarding to a CounterVec.WithLabelValues call.
func TestRecorderInterfaceForbidsRawSecretLeak(t *testing.T) {
	const rawSecret = "Authorization: Bearer edge014-test-secret-token-12345"
	for _, value := range []string{
		rawSecret,
		"sk-leaked-token-abcdefghij",
		"ghp_leakedtokenabcdefghij1234567890",
		"AKIAIOSFODNN7EXAMPLE",
		"rm -rf / && echo done",
		"/home/user/.ssh/id_rsa",
	} {
		if got := NormalizeDecision(value); got == value {
			t.Errorf("NormalizeDecision did not bound %q -> %q (raw value would leak as label)", value, got)
		}
		if got := NormalizeLayer(value); got == value {
			t.Errorf("NormalizeLayer did not bound %q (raw value would leak as label)", value)
		}
		if got := NormalizeKind(value); got == value {
			t.Errorf("NormalizeKind did not bound %q (raw value would leak as label)", value)
		}
		if got := NormalizeApprovalOutcome(value); got == value {
			t.Errorf("NormalizeApprovalOutcome did not bound %q (raw value would leak as label)", value)
		}
		if got := NormalizeStreamDropReason(value); got == value {
			t.Errorf("NormalizeStreamDropReason did not bound %q (raw value would leak as label)", value)
		}
	}
}
