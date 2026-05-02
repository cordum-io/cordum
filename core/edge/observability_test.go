package edge

import (
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
