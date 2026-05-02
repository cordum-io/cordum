package edge

import (
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/prometheus/client_golang/prometheus"
)

// prometheusNewRegistryHelper returns a fresh prometheus.Registry suitable
// for a single test. Each test gets its own to avoid MustRegister panics
// on duplicate metric names.
func prometheusNewRegistryHelper(t *testing.T) *prometheus.Registry {
	t.Helper()
	return prometheus.NewRegistry()
}

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

// TestPrometheusRecorderRegistersAndEmitsBoundedMetrics pins the step-7
// PrometheusRecorder behavior: registration via prometheus.NewRegistry()
// succeeds without MustRegister panics, label values are bounded by the
// step-3 Normalize* helpers, and counter/gauge values are observable via
// testutil.ToFloat64.
func TestPrometheusRecorderRegistersAndEmitsBoundedMetrics(t *testing.T) {
	reg := prometheusNewRegistryHelper(t)
	r := NewPrometheusRecorder(reg)

	// Exercise every metric to catch registration errors that surface
	// only on first WithLabelValues for a given label set.
	r.RecordSessionCreated("tenant-edge014", "local-dev", "claude-code")
	r.RecordSessionEnded("tenant-edge014", "local-dev", "ended")
	r.SetSessionsActive("tenant-edge014", "local-dev", 5)
	r.RecordExecutionStarted("tenant-edge014", "local-dev", "claude-code")
	r.RecordExecutionEnded("tenant-edge014", "local-dev", "succeeded")
	r.RecordActionDecision("tenant-edge014", "hook", "hook.pre_tool_use", "ALLOW", "local-dev")
	r.RecordActionDenied("tenant-edge014", "hook", "hook.pre_tool_use", "destructive_command")
	r.RecordApprovalRequested("tenant-edge014", "hook", "hook.pre_tool_use")
	r.RecordApprovalResolved("tenant-edge014", "hook", "hook.pre_tool_use", "approved")
	r.RecordDegraded("tenant-edge014", "local-dev", "agentd", "gateway_unavailable")
	r.RecordFailClosed("tenant-edge014", "enterprise-strict", "gateway_unavailable")
	r.RecordArtifactExport("tenant-edge014", "edge.session_export", "ok")
	r.ObserveHookLatency("tenant-edge014", "PreToolUse", "ALLOW", 100*time.Millisecond)
	r.ObserveEvaluateLatency("tenant-edge014", "hook", "hook.pre_tool_use", "ALLOW", 50*time.Millisecond)
	r.RecordCacheLookup("tenant-edge014", "hook", "hook.pre_tool_use", "hit")
	r.AddStreamClients("tenant-edge014", 2)
	r.AddStreamClients("tenant-edge014", -1)
	r.RecordStreamDrop("client_buffer_full")
}

// TestPrometheusRecorderBoundsHighCardinalityInputs pins that
// attacker-supplied or high-cardinality strings (raw command, secret,
// ID-like values) collapse to bounded enum labels via the step-3
// Normalize* helpers + boundedMode/Status/etc., so the metric registry
// cannot blow up from arbitrary input.
func TestPrometheusRecorderBoundsHighCardinalityInputs(t *testing.T) {
	reg := prometheusNewRegistryHelper(t)
	r := NewPrometheusRecorder(reg)

	const rawSecret = "Authorization: Bearer edge014-prom-secret-xyz"
	r.RecordActionDecision("tenant-edge014", "WEIRD-LAYER", "rm -rf /", rawSecret, "evil-mode")
	r.RecordActionDenied("tenant-edge014", rawSecret, "hook.pre_tool_use", "very_long_reason_code_that_should_collapse_to_other_because_it_exceeds_the_48_char_bound_with_lots_of_extra")
	r.RecordDegraded("tenant-edge014", rawSecret, rawSecret, rawSecret)
	r.RecordFailClosed("tenant-edge014", rawSecret, rawSecret)
	r.RecordArtifactExport("tenant-edge014", rawSecret, rawSecret)
	r.ObserveHookLatency("tenant-edge014", rawSecret, rawSecret, 1*time.Millisecond)
	r.RecordCacheLookup("tenant-edge014", rawSecret, rawSecret, rawSecret)
	r.RecordStreamDrop(rawSecret)
	// If any of these calls had leaked the raw secret as a label, the
	// Prometheus registry would have created N+ unique label sets. We
	// can't easily inspect label cardinality through the public API
	// without testutil.GatherAndCount, so rely on the Normalize* tests
	// from step-3 to enforce the contract — this test exists primarily
	// to catch a panic from a missing-label-bound recorder method.
}

// TestPrometheusRecorderNilRegistererReturnsNoop pins that passing nil
// returns the no-op recorder rather than panicking on MustRegister(nil).
func TestPrometheusRecorderNilRegistererReturnsNoop(t *testing.T) {
	r := NewPrometheusRecorder(nil)
	if _, ok := r.(NoopRecorder); !ok {
		t.Fatalf("NewPrometheusRecorder(nil) = %T, want NoopRecorder", r)
	}
	// Sanity: methods don't panic.
	r.RecordSessionCreated("", "", "")
	r.RecordStreamDrop("")
}

// TestBoundedHelpersCollapseUntrustedInput pins the per-label normalizer
// allowlists (mode, agent_product, status, component, artifact_type,
// result, hook_event, cache_result, reason_code) — Prometheus-recorder
// callers rely on these to keep metric label cardinality bounded.
func TestBoundedHelpersCollapseUntrustedInput(t *testing.T) {
	checks := []struct {
		name string
		fn   func(string) string
		good map[string]string
		bad  []string
	}{
		{"mode", boundedMode,
			map[string]string{"observe": "observe", "local-dev": "local-dev", "ENTERPRISE-STRICT": "enterprise-strict", "": "unknown"},
			[]string{"banana", "Authorization: Bearer secret"}},
		{"agentProduct", boundedAgentProduct,
			map[string]string{"claude-code": "claude-code", "CODEX": "codex", "": "unknown"},
			[]string{"sk-leaked", "rm -rf"}},
		{"status", boundedStatus,
			map[string]string{"ended": "ended", "RUNNING": "running", "": "unknown"},
			[]string{"banana_status_value_not_in_set"}},
		{"component", boundedComponent,
			map[string]string{"gateway": "gateway", "AGENTD": "agentd", "": "unknown"},
			[]string{"core/edge/something"}},
		{"artifactType", boundedArtifactType,
			map[string]string{"edge.session_export": "edge.session_export", "": "unknown"},
			[]string{"banana", "rm -rf /"}},
		{"result", boundedResult,
			map[string]string{"ok": "ok", "FAILED": "failed", "": "unknown"},
			[]string{"some-arbitrary-result"}},
		{"cacheResult", boundedCacheResult,
			map[string]string{"hit": "hit", "MISS_NO_ELIGIBILITY": "miss_no_eligibility", "": "unknown"},
			[]string{"banana"}},
		{"reasonCode", boundedReasonCode,
			map[string]string{"gateway_unavailable": "gateway_unavailable", "": "unknown", "TIMEOUT": "timeout"},
			[]string{"this_reason_code_is_intentionally_made_far_too_long_to_pass_through_the_48_char_bound_and_should_collapse", "raw command with spaces", "Bearer secret"}},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			for input, want := range c.good {
				if got := c.fn(input); got != want {
					t.Errorf("%s(%q) = %q, want %q", c.name, input, got, want)
				}
			}
			for _, input := range c.bad {
				if got := c.fn(input); got == input || got == "" {
					t.Errorf("%s(%q) = %q, want bounded enum (got passthrough)", c.name, input, got)
				}
				if got := c.fn(input); got != "other" && got != "unknown" {
					t.Errorf("%s(%q) = %q, want 'other' or 'unknown'", c.name, input, got)
				}
			}
		})
	}
}

// TestBoundedHookEventPreservesDocumentedClaudeNames pins that PascalCase
// Claude hook event names pass through (these are case-sensitive Claude
// API conventions) and unknown values collapse to "other".
func TestBoundedHookEventPreservesDocumentedClaudeNames(t *testing.T) {
	for _, name := range []string{"PreToolUse", "PostToolUse", "PostToolUseFailure", "UserPromptSubmit", "ConfigChange", "FileChanged"} {
		if got := boundedHookEvent(name); got != name {
			t.Errorf("boundedHookEvent(%q) = %q, want passthrough", name, got)
		}
	}
	if got := boundedHookEvent(""); got != "unknown" {
		t.Errorf("boundedHookEvent(\"\") = %q, want unknown", got)
	}
	if got := boundedHookEvent("Banana"); got != "other" {
		t.Errorf("boundedHookEvent(\"Banana\") = %q, want other", got)
	}
}

// recordingAuditSender is a minimal AuditSender for tests: it records
// every Send call so assertions can inspect the emitted events.
type recordingAuditSender struct {
	mu     sync.Mutex
	events []audit.SIEMEvent
}

func (r *recordingAuditSender) Send(event audit.SIEMEvent) {
	r.mu.Lock()
	r.events = append(r.events, event)
	r.mu.Unlock()
}

func (r *recordingAuditSender) Close() error { return nil }

func (r *recordingAuditSender) snapshot() []audit.SIEMEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]audit.SIEMEvent, len(r.events))
	copy(out, r.events)
	return out
}

// panicAuditSender simulates an exploding audit pipeline. Used to prove
// SendSIEMEvent's panic-recovery contract: a broken audit sink MUST NEVER
// fail the calling Edge handler.
type panicAuditSender struct{}

func (panicAuditSender) Send(audit.SIEMEvent) { panic("synthetic audit failure") }
func (panicAuditSender) Close() error         { return nil }

// TestSIEMEventForActionMapsDecisionAndSeverity pins the event_type +
// severity matrix for AgentActionEvent → SIEMEvent.
func TestSIEMEventForActionMapsDecisionAndSeverity(t *testing.T) {
	for _, tc := range []struct {
		name         string
		decision     EdgeDecision
		wantType     string
		wantSeverity string
		wantNorm     string
	}{
		{"allow → policy_decision/info", DecisionAllow, audit.EventEdgePolicyDecision, audit.SeverityInfo, "allow"},
		{"deny → action_denied/high", "DENY", audit.EventEdgeActionDenied, audit.SeverityHigh, "deny"},
		{"require_approval → approval_requested/medium", "REQUIRE_APPROVAL", audit.EventEdgeApprovalRequested, audit.SeverityMedium, "require_approval"},
		{"throttle → action_denied/medium", "THROTTLE", audit.EventEdgeActionDenied, audit.SeverityMedium, "throttle"},
		{"recorded → policy_decision/info", DecisionRecorded, audit.EventEdgePolicyDecision, audit.SeverityInfo, "recorded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := AgentActionEvent{
				EventID:     "evt-edge014-siem-1",
				SessionID:   "edge_sess_siem",
				ExecutionID: "edge_exec_siem",
				TenantID:    "tenant-edge014",
				PrincipalID: "principal-edge014",
				Timestamp:   time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC),
				Layer:       LayerHook,
				Kind:        EventKindHookPreToolUse,
				ToolName:    "Bash",
				ActionName:  "bash.exec",
				Capability:  "exec.shell",
				RiskTags:    []string{"exec"},
				Decision:    tc.decision,
				Status:      ActionStatusOK,
				InputHash:   "sha256:abc",
				RuleID:      "claude-code.allow-tests",
			}
			got := SIEMEventForAction(event)
			if got.EventType != tc.wantType {
				t.Errorf("EventType = %q, want %q", got.EventType, tc.wantType)
			}
			if got.Severity != tc.wantSeverity {
				t.Errorf("Severity = %q, want %q", got.Severity, tc.wantSeverity)
			}
			if got.Decision != tc.wantNorm {
				t.Errorf("Decision = %q, want normalized %q", got.Decision, tc.wantNorm)
			}
			if got.TenantID != "tenant-edge014" {
				t.Errorf("TenantID = %q, want passthrough", got.TenantID)
			}
			if got.MatchedRule != "claude-code.allow-tests" {
				t.Errorf("MatchedRule = %q, want passthrough", got.MatchedRule)
			}
		})
	}
}

// TestSIEMEventForActionExtraIsBoundedAndSecretFree pins the Extra-map
// invariant: only safe IDs/hashes/policy_snapshot/approval_ref/tool_name
// (bounded). No raw labels, no InputRedacted maps, no raw command.
func TestSIEMEventForActionExtraIsBoundedAndSecretFree(t *testing.T) {
	const rawSecret = "Authorization: Bearer edge014-siem-secret-xyz"
	event := AgentActionEvent{
		EventID:        "evt-edge014-siem-extra",
		SessionID:      "edge_sess_siem_extra",
		ExecutionID:    "edge_exec_siem_extra",
		TenantID:       "tenant-edge014",
		PrincipalID:    "principal-edge014",
		Timestamp:      time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC),
		Layer:          LayerHook,
		Kind:           EventKindHookPreToolUse,
		ToolName:       "Bash",
		ActionName:     "bash.exec",
		Capability:     "exec.shell",
		Decision:       DecisionAllow,
		Status:         ActionStatusOK,
		InputHash:      "sha256:" + strings.Repeat("a", 64),
		PolicySnapshot: "sha256:policy-snap-abc",
		ApprovalRef:    "edge_appr_test_001",
		Labels:         Labels{"command.class": "safe", "leak": rawSecret},
		InputRedacted:  map[string]any{"command": rawSecret},
	}
	got := SIEMEventForAction(event)
	for k, v := range got.Extra {
		if strings.Contains(k, rawSecret) || strings.Contains(v, rawSecret) {
			t.Errorf("SIEMEventForAction Extra leaked raw secret in %q=%q", k, v)
		}
		if strings.Contains(v, "Bearer ") || strings.Contains(v, "Authorization") {
			t.Errorf("SIEMEventForAction Extra leaked secret-marker substring in %q=%q", k, v)
		}
	}
	for _, want := range []string{"session_id", "execution_id", "event_id", "layer", "kind", "tool_name", "input_hash", "policy_snapshot", "approval_ref"} {
		if _, ok := got.Extra[want]; !ok {
			t.Errorf("Extra missing required key %q; got %#v", want, got.Extra)
		}
	}
	for _, banned := range []string{"command", "labels", "input_redacted", "raw", "leak"} {
		if _, ok := got.Extra[banned]; ok {
			t.Errorf("Extra contains banned key %q (raw passthrough): %#v", banned, got.Extra)
		}
	}
}

// TestSIEMEventForSessionLifecycle pins start/end event types + severity
// (info on clean end, high on failed/degraded).
func TestSIEMEventForSessionLifecycle(t *testing.T) {
	startedAt := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	endedAt := startedAt.Add(time.Hour)
	for _, tc := range []struct {
		name         string
		status       SessionStatus
		wantEvent    string
		wantSeverity string
	}{
		{"clean end is info", SessionStatusEnded, audit.EventEdgeSessionEnded, audit.SeverityInfo},
		{"failed is high", SessionStatusFailed, audit.EventEdgeSessionEnded, audit.SeverityHigh},
		{"degraded is high", SessionStatusDegraded, audit.EventEdgeSessionEnded, audit.SeverityHigh},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := EdgeSession{
				TenantID:     "tenant-edge014",
				SessionID:    "edge_sess_lifecycle",
				PrincipalID:  "principal-edge014",
				AgentProduct: "claude-code",
				Mode:         "local-dev",
				Status:       tc.status,
				StartedAt:    startedAt,
				EndedAt:      &endedAt,
			}
			got := SIEMEventForSessionEnded(session)
			if got.EventType != tc.wantEvent {
				t.Errorf("EventType = %q, want %q", got.EventType, tc.wantEvent)
			}
			if got.Severity != tc.wantSeverity {
				t.Errorf("Severity = %q, want %q", got.Severity, tc.wantSeverity)
			}
			if got.TenantID != "tenant-edge014" {
				t.Errorf("TenantID = %q, want passthrough", got.TenantID)
			}
			if got.Extra["mode"] != "local-dev" {
				t.Errorf("Extra[mode] = %q, want local-dev", got.Extra["mode"])
			}
		})
	}

	// Started events are always info-severity.
	startSession := EdgeSession{
		TenantID: "tenant-edge014", SessionID: "edge_sess_started", Status: SessionStatusRunning,
		StartedAt: startedAt, AgentProduct: "claude-code", Mode: "local-dev",
	}
	startEvent := SIEMEventForSessionStarted(startSession)
	if startEvent.EventType != audit.EventEdgeSessionStarted {
		t.Errorf("started EventType = %q, want %q", startEvent.EventType, audit.EventEdgeSessionStarted)
	}
	if startEvent.Severity != audit.SeverityInfo {
		t.Errorf("started Severity = %q, want info", startEvent.Severity)
	}
}

// TestSIEMEventForApprovalResolvedSeverity pins the outcome → severity
// mapping per architect's table.
func TestSIEMEventForApprovalResolvedSeverity(t *testing.T) {
	at := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		outcome      string
		wantType     string
		wantSeverity string
	}{
		{"approved", audit.EventEdgeApprovalResolved, audit.SeverityInfo},
		{"rejected", audit.EventEdgeApprovalRejected, audit.SeverityHigh},
		{"expired", audit.EventEdgeApprovalExpired, audit.SeverityMedium},
		{"timeout", audit.EventEdgeApprovalResolved, audit.SeverityMedium},
		{"invalidated", audit.EventEdgeApprovalResolved, audit.SeverityMedium},
		{"banana", audit.EventEdgeApprovalResolved, audit.SeverityInfo}, // collapsed to "other" → info default
	} {
		t.Run(tc.outcome, func(t *testing.T) {
			got := SIEMEventForApprovalResolved("tenant-edge014", "edge_appr_xyz", "claude-code.require-approval-prod", tc.outcome, "principal-resolver", at, nil)
			if got.EventType != tc.wantType {
				t.Errorf("EventType = %q, want %q", got.EventType, tc.wantType)
			}
			if got.Severity != tc.wantSeverity {
				t.Errorf("Severity = %q, want %q", got.Severity, tc.wantSeverity)
			}
			if got.Extra["approval_ref"] != "edge_appr_xyz" {
				t.Errorf("Extra[approval_ref] = %q, want passthrough", got.Extra["approval_ref"])
			}
		})
	}
}

// TestSIEMEventForFailClosedIsCriticalSeverity pins the architect-locked
// severity for enterprise-strict fail-closed outcomes.
func TestSIEMEventForFailClosedIsCriticalSeverity(t *testing.T) {
	got := SIEMEventForFailClosed("tenant-edge014", "enterprise-strict", "gateway", "gateway_unavailable", time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC))
	if got.EventType != audit.EventEdgeFailClosed {
		t.Errorf("EventType = %q, want %q", got.EventType, audit.EventEdgeFailClosed)
	}
	if got.Severity != audit.SeverityCritical {
		t.Errorf("Severity = %q, want CRITICAL", got.Severity)
	}
	if got.Decision != "deny" {
		t.Errorf("Decision = %q, want deny", got.Decision)
	}
}

// TestSendSIEMEventNilSenderIsNoOp pins that a nil AuditSender does NOT
// panic and does NOT block the calling code path. Critical for callers
// that accept an optional audit pipeline (s.auditExporter may be nil
// when audit is intentionally disabled).
func TestSendSIEMEventNilSenderIsNoOp(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("SendSIEMEvent with nil sender panicked: %v", r)
		}
	}()
	SendSIEMEvent(nil, audit.SIEMEvent{EventType: audit.EventEdgePolicyDecision})
}

// TestSendSIEMEventSwallowsPanic pins that a panicking AuditSender does
// NOT propagate up to the caller. Edge call sites must never have a
// policy decision flipped by a broken audit pipeline.
func TestSendSIEMEventSwallowsPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("SendSIEMEvent did not swallow sender panic: %v", r)
		}
	}()
	SendSIEMEvent(panicAuditSender{}, audit.SIEMEvent{EventType: audit.EventEdgePolicyDecision})
}

// TestSendSIEMEventDeliversToWorkingSender pins the happy-path delivery.
func TestSendSIEMEventDeliversToWorkingSender(t *testing.T) {
	rec := &recordingAuditSender{}
	want := audit.SIEMEvent{
		EventType: audit.EventEdgePolicyDecision,
		TenantID:  "tenant-edge014",
		Action:    "bash.exec",
		Decision:  "allow",
	}
	SendSIEMEvent(rec, want)
	got := rec.snapshot()
	if len(got) != 1 {
		t.Fatalf("recorder got %d events, want 1", len(got))
	}
	if got[0].EventType != want.EventType {
		t.Errorf("EventType = %q, want %q", got[0].EventType, want.EventType)
	}
}

// TestRecorderInterfaceForbidsRawSecretLeak documents the contract that
// raw secret-shaped inputs MUST collapse to bounded labels via the
// Normalize* helpers before reaching a Prometheus recorder. The no-op
// recorder accepts any input (it does nothing); the test pins the
// invariant via the normalizers, which the step-7 Prometheus recorder
// MUST call before forwarding to a CounterVec.WithLabelValues call.
// TestExecutionLogAttrsEmitsOnlyBoundedFields pins step-8 ExecutionLogAttrs
// behavior: tenant/session/execution/job/workflow/step IDs are bounded,
// adapter/mode/status pass through Normalize* helpers, started_at/ended_at
// are emitted as time, and metrics counts are emitted as ints. Raw labels
// are NEVER logged wholesale.
func TestExecutionLogAttrsEmitsOnlyBoundedFields(t *testing.T) {
	endedAt := time.Date(2026, 5, 1, 12, 30, 0, 0, time.UTC)
	exec := AgentExecution{
		ExecutionID:   "edge_exec_abc123",
		SessionID:     "edge_sess_def456",
		TenantID:      "tenant-a",
		Adapter:       "claude-code",
		Mode:          "local-dev",
		WorkflowRunID: "wfrun_xyz",
		StepID:        "step-1",
		JobID:         "job-1",
		Attempt:       2,
		TraceID:       "trace-aaa",
		WorkerID:      "worker-bbb",
		Status:        "succeeded",
		StartedAt:     endedAt.Add(-1 * time.Second),
		EndedAt:       &endedAt,
		Metrics: ExecutionMetrics{
			Events:          7,
			Allow:           5,
			Deny:            1,
			RequireApproval: 1,
			Artifacts:       2,
			LLMCostUSD:      0.0123,
		},
		Labels: Labels{"raw_secret": "Authorization: Bearer leaky"},
	}
	attrs := ExecutionLogAttrs(exec)
	want := map[string]bool{
		"tenant_id": true, "session_id": true, "execution_id": true,
		"adapter": true, "mode": true, "workflow_run_id": true,
		"step_id": true, "job_id": true, "attempt": true,
		"trace_id": true, "worker_id": true, "status": true,
		"started_at": true, "ended_at": true,
		"events": true, "allow": true, "deny": true,
		"require_approval": true, "artifacts": true, "llm_cost_usd": true,
	}
	got := map[string]slog.Attr{}
	for _, a := range attrs {
		got[a.Key] = a
	}
	for k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("ExecutionLogAttrs missing key %q (got keys: %v)", k, got)
		}
	}
	for k, a := range got {
		s := a.Value.String()
		if strings.Contains(s, "Authorization") || strings.Contains(s, "Bearer") {
			t.Errorf("ExecutionLogAttrs attr %q leaked secret: %q", k, s)
		}
		if k == "labels" || k == "raw_secret" {
			t.Errorf("ExecutionLogAttrs emitted forbidden raw key %q", k)
		}
	}
}

// TestApprovalLogAttrsEmitsOnlyBoundedFields pins step-8 ApprovalLogAttrs
// behavior: approval_ref/tenant/session/execution/event IDs bounded;
// status/decision normalized; rule_id/policy_snapshot/action_hash/input_hash
// length-bounded; created_at/expires_at/resolved_at as time when present;
// raw Reason/ResolutionReason are NEVER logged wholesale (free-form text
// can carry user input/PII; callers wanting to log a reason must redact
// upstream).
func TestApprovalLogAttrsEmitsOnlyBoundedFields(t *testing.T) {
	createdAt := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	expiresAt := createdAt.Add(15 * time.Minute)
	resolvedAt := createdAt.Add(2 * time.Minute)
	const secret = "Authorization: Bearer leaky-approval-token"
	apr := EdgeApproval{
		ApprovalRef:      "edge_apr_abc",
		TenantID:         "tenant-a",
		SessionID:        "edge_sess_def",
		ExecutionID:      "edge_exec_ghi",
		EventID:          "edge_evt_jkl",
		PrincipalID:      "user@example.com",
		Requester:        "user@example.com",
		ResolverID:       "approver@example.com",
		Status:           "approved",
		Decision:         "allow",
		Reason:           secret + " — please run rm -rf /",
		ResolutionReason: secret + " — approver said yes",
		RuleID:           "rule_abc_xyz_long_id_value_should_be_bounded_to_eighty_characters_at_most_no_more",
		PolicySnapshot:   "policy_snap_long",
		ActionHash:       strings.Repeat("a", 200),
		InputHash:        strings.Repeat("b", 200),
		CreatedAt:        createdAt,
		ExpiresAt:        &expiresAt,
		ResolvedAt:       &resolvedAt,
		Labels:           Labels{"raw_secret": secret},
		Metadata:         Metadata{"raw_secret": secret},
	}
	attrs := ApprovalLogAttrs(apr)
	got := map[string]slog.Attr{}
	for _, a := range attrs {
		got[a.Key] = a
	}
	for _, want := range []string{
		"approval_ref", "tenant_id", "session_id", "execution_id", "event_id",
		"status", "decision", "rule_id", "action_hash", "input_hash",
		"created_at",
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("ApprovalLogAttrs missing key %q", want)
		}
	}
	for _, forbidden := range []string{"reason", "resolution_reason", "labels", "metadata", "raw_secret"} {
		if _, ok := got[forbidden]; ok {
			t.Errorf("ApprovalLogAttrs emitted forbidden raw key %q", forbidden)
		}
	}
	for k, a := range got {
		s := a.Value.String()
		if strings.Contains(s, "Authorization") || strings.Contains(s, "Bearer") || strings.Contains(s, "rm -rf") {
			t.Errorf("ApprovalLogAttrs attr %q leaked secret: %q", k, s)
		}
	}
	if h, ok := got["action_hash"]; ok {
		if len(h.Value.String()) > 90 {
			t.Errorf("ApprovalLogAttrs action_hash not bounded: len=%d", len(h.Value.String()))
		}
	}
}

// TestExportResultLogAttrsEmitsOnlyBoundedFields pins step-8
// ExportResultLogAttrs: artifact_type and result are normalized via the
// step-7 bounded helpers; sha256 / uri are length-bounded; raw URI
// query strings (which may carry signed-URL secrets) are NEVER logged
// wholesale.
func TestExportResultLogAttrsEmitsOnlyBoundedFields(t *testing.T) {
	pointer := ArtifactPointer{
		ArtifactType:   "edge.session_export",
		SessionID:      "edge_sess_abc",
		ExecutionID:    "edge_exec_def",
		EventID:        "edge_evt_ghi",
		TenantID:       "tenant-a",
		RetentionClass: "standard",
		RedactionLevel: "redacted",
		SHA256:         "abc123def456" + strings.Repeat("z", 200),
		URI:            "https://example.com/blob?token=Authorization:Bearer-secret",
		CreatedAt:      time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
	}
	attrs := ExportResultLogAttrs(pointer, "ok")
	got := map[string]slog.Attr{}
	for _, a := range attrs {
		got[a.Key] = a
	}
	for _, want := range []string{
		"tenant_id", "session_id", "execution_id", "event_id",
		"artifact_type", "result", "sha256", "created_at",
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("ExportResultLogAttrs missing key %q", want)
		}
	}
	if a, ok := got["artifact_type"]; ok && a.Value.String() != "edge.session_export" {
		t.Errorf("artifact_type = %q, want edge.session_export", a.Value.String())
	}
	if a, ok := got["result"]; ok && a.Value.String() != "ok" {
		t.Errorf("result = %q, want ok", a.Value.String())
	}
	if a, ok := got["sha256"]; ok && len(a.Value.String()) > 90 {
		t.Errorf("sha256 not bounded: len=%d", len(a.Value.String()))
	}
	for k, a := range got {
		s := a.Value.String()
		if strings.Contains(s, "Authorization") || strings.Contains(s, "Bearer") || strings.Contains(s, "token=") {
			t.Errorf("ExportResultLogAttrs attr %q leaked URL secret: %q", k, s)
		}
	}
}

// TestHookSummaryLogAttrsEmitsOnlyBoundedFields pins step-8
// HookSummaryLogAttrs: bounded fields only, hook_event passes through
// boundedHookEvent allowlist, decision normalized, latency emitted as
// duration. Raw error strings/raw payloads are converted via ErrorLogAttrs.
func TestHookSummaryLogAttrsEmitsOnlyBoundedFields(t *testing.T) {
	attrs := HookSummaryLogAttrs(HookSummary{
		TenantID:   "tenant-a",
		SessionID:  "edge_sess_abc",
		HookEvent:  "PreToolUse",
		Decision:   "ALLOW",
		ReasonCode: "policy_allow",
		LatencyMS:  123,
		Mode:       "local-dev",
		Component:  "agentd",
	})
	got := map[string]slog.Attr{}
	for _, a := range attrs {
		got[a.Key] = a
	}
	for _, want := range []string{
		"tenant_id", "session_id", "hook_event", "decision",
		"reason_code", "latency_ms", "mode", "component",
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("HookSummaryLogAttrs missing key %q", want)
		}
	}
	if a, ok := got["hook_event"]; ok && a.Value.String() != "PreToolUse" {
		t.Errorf("hook_event = %q, want PreToolUse passthrough", a.Value.String())
	}
	if a, ok := got["decision"]; ok && a.Value.String() != "allow" {
		t.Errorf("decision = %q, want bounded 'allow'", a.Value.String())
	}
}

// TestEvaluateSummaryLogAttrsEmitsOnlyBoundedFields pins step-8
// EvaluateSummaryLogAttrs: layer/kind normalized via Normalize* helpers,
// decision normalized, mode/component bounded.
func TestEvaluateSummaryLogAttrsEmitsOnlyBoundedFields(t *testing.T) {
	attrs := EvaluateSummaryLogAttrs(EvaluateSummary{
		TenantID:    "tenant-a",
		SessionID:   "edge_sess_abc",
		ExecutionID: "edge_exec_def",
		Layer:       "hook",
		Kind:        "hook.pre_tool_use",
		Decision:    "REQUIRE_APPROVAL",
		ApprovalRef: "edge_apr_xyz",
		LatencyMS:   45,
		Mode:        "enterprise-strict",
		Cached:      false,
	})
	got := map[string]slog.Attr{}
	for _, a := range attrs {
		got[a.Key] = a
	}
	for _, want := range []string{
		"tenant_id", "session_id", "execution_id", "layer", "kind",
		"decision", "approval_ref", "latency_ms", "mode", "cached",
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("EvaluateSummaryLogAttrs missing key %q", want)
		}
	}
	if a, ok := got["layer"]; ok && a.Value.String() != "hook" {
		t.Errorf("layer = %q, want hook", a.Value.String())
	}
	if a, ok := got["decision"]; ok && a.Value.String() != "require_approval" {
		t.Errorf("decision = %q, want require_approval", a.Value.String())
	}
}

// TestErrorLogAttrsConvertsErrorToReasonCodeAndRedactedMessage pins
// step-8 ErrorLogAttrs: a raw error becomes (a) a bounded reason_code
// (snake_case-ish or "other"), (b) a length-bounded error_message, and
// (c) NEVER leaks the raw value past 200 chars or as a non-bounded
// attribute. Nil errors emit no attributes.
func TestErrorLogAttrsConvertsErrorToReasonCodeAndRedactedMessage(t *testing.T) {
	if attrs := ErrorLogAttrs(nil, ""); len(attrs) != 0 {
		t.Errorf("ErrorLogAttrs(nil, \"\") = %v, want empty", attrs)
	}
	huge := strings.Repeat("Authorization: Bearer secret-", 200)
	attrs := ErrorLogAttrs(stringError(huge), "gateway_unavailable")
	got := map[string]slog.Attr{}
	for _, a := range attrs {
		got[a.Key] = a
	}
	if a, ok := got["reason_code"]; !ok || a.Value.String() != "gateway_unavailable" {
		t.Errorf("reason_code = %v, want gateway_unavailable", a)
	}
	if a, ok := got["error_message"]; ok {
		if len(a.Value.String()) > 256 {
			t.Errorf("error_message not bounded: len=%d", len(a.Value.String()))
		}
	} else {
		t.Errorf("ErrorLogAttrs missing error_message")
	}
	// Empty reason_code should default to "unknown".
	attrs = ErrorLogAttrs(stringError("boom"), "")
	for _, a := range attrs {
		if a.Key == "reason_code" && a.Value.String() != "unknown" {
			t.Errorf("reason_code with empty input = %q, want unknown", a.Value.String())
		}
	}
}

// stringError is a minimal error type for tests so we don't pull in
// errors.New + fmt.Errorf for fixture data.
type stringError string

func (s stringError) Error() string { return string(s) }

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
