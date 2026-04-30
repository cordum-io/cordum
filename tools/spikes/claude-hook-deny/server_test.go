package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const denialReason = "Cordum policy blocked this Bash command: destructive recursive deletion is not allowed."

func TestPreToolUseDeniesBashRecursiveDelete(t *testing.T) {
	body := `{
		"hook_event_name":"PreToolUse",
		"session_id":"sess-test",
		"cwd":"/tmp/project",
		"tool_name":"Bash",
		"tool_use_id":"toolu-test",
		"tool_input":{"command":"rm -rf /tmp/cordum-hook-spike-test"}
	}`

	res := postHook(t, http.MethodPost, body)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%q", res.Code, http.StatusOK, res.Body.String())
	}
	if ct := res.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}

	var out struct {
		HookSpecificOutput struct {
			HookEventName            string `json:"hookEventName"`
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("decode response JSON: %v; body=%q", err, res.Body.String())
	}

	if out.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Fatalf("hookEventName = %q, want PreToolUse", out.HookSpecificOutput.HookEventName)
	}
	if out.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("permissionDecision = %q, want deny", out.HookSpecificOutput.PermissionDecision)
	}
	if out.HookSpecificOutput.PermissionDecisionReason != denialReason {
		t.Fatalf("permissionDecisionReason = %q, want %q", out.HookSpecificOutput.PermissionDecisionReason, denialReason)
	}
}

func TestPreToolUseLeavesSafeBashUndecided(t *testing.T) {
	body := `{
		"hook_event_name":"PreToolUse",
		"session_id":"sess-test",
		"cwd":"/tmp/project",
		"tool_name":"Bash",
		"tool_input":{"command":"printf hello"}
	}`

	res := postHook(t, http.MethodPost, body)

	assertEmptyOK(t, res)
}

func TestPreToolUseLeavesNonBashUndecided(t *testing.T) {
	body := `{
		"hook_event_name":"PreToolUse",
		"session_id":"sess-test",
		"cwd":"/tmp/project",
		"tool_name":"Read",
		"tool_input":{"file_path":"README.md"}
	}`

	res := postHook(t, http.MethodPost, body)

	assertEmptyOK(t, res)
}

func TestPreToolUseRejectsNonPOST(t *testing.T) {
	res := postHook(t, http.MethodGet, ``)

	if res.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d; body=%q", res.Code, http.StatusMethodNotAllowed, res.Body.String())
	}
}

func TestPreToolUseRejectsMalformedJSON(t *testing.T) {
	res := postHook(t, http.MethodPost, `{not-json`)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%q", res.Code, http.StatusBadRequest, res.Body.String())
	}
}

func TestPreToolUseRejectsOversizedJSON(t *testing.T) {
	oversizedCommand := strings.Repeat("x", maxHookRequestBytes+1)
	body := `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"` + oversizedCommand + `"}}`

	res := postHook(t, http.MethodPost, body)

	if res.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body=%q", res.Code, http.StatusRequestEntityTooLarge, res.Body.String())
	}
}

func TestPreToolUseHandlesMissingOrNonStringCommandSafely(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "missing tool_input",
			body: `{"hook_event_name":"PreToolUse","tool_name":"Bash"}`,
		},
		{
			name: "missing command",
			body: `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{}}`,
		},
		{
			name: "non-string command",
			body: `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":["rm","-rf","/tmp/x"]}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := postHook(t, http.MethodPost, tt.body)
			assertEmptyOK(t, res)
		})
	}
}

func TestRecursiveDeleteDetectionMatchesOnlyCommandPositions(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{name: "rm dash rf", command: "rm -rf /tmp/cordum-hook-spike-test", want: true},
		{name: "rm split flags", command: "rm -r -f /tmp/cordum-hook-spike-test", want: true},
		{name: "sudo rm uppercase flags", command: "sudo rm -Rf /tmp/cordum-hook-spike-test", want: true},
		{name: "after shell separator", command: "cd /tmp && rm -fr cordum-hook-spike-test", want: true},
		{name: "nested bash command string", command: "bash -lc 'rm -rf /tmp/cordum-hook-spike-test'", want: true},
		{name: "safe rm without recursive force", command: "rm /tmp/cordum-hook-spike-test", want: false},
		{name: "text mention not command", command: "echo rm -rf /tmp/cordum-hook-spike-test", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDestructiveRecursiveDelete(tt.command); got != tt.want {
				t.Fatalf("isDestructiveRecursiveDelete(%q) = %t, want %t", tt.command, got, tt.want)
			}
		})
	}
}

func TestBoundedLogFieldsSanitizeRedactAndTruncate(t *testing.T) {
	if got := boundedLogField("  event\nwith\tspaces  "); got != "event with spaces" {
		t.Fatalf("boundedLogField sanitized value = %q, want %q", got, "event with spaces")
	}
	if got := boundedLogField("sk-12345678901234567890"); got != redactedSensitiveLogField {
		t.Fatalf("boundedLogField token-like value = %q, want %q", got, redactedSensitiveLogField)
	}
	if got := boundedSessionID("session-12345678901234567890"); got != "session-12345678..." {
		t.Fatalf("boundedSessionID = %q, want %q", got, "session-12345678...")
	}
	if got := boundedLogField(""); got != redactedEmptyLogField {
		t.Fatalf("boundedLogField empty value = %q, want %q", got, redactedEmptyLogField)
	}
}

func postHook(t *testing.T, method, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, "/hook/pre-tool-use", bytes.NewBufferString(body))
	res := httptest.NewRecorder()

	newHookMux().ServeHTTP(res, req)

	return res
}

func assertEmptyOK(t *testing.T, res *httptest.ResponseRecorder) {
	t.Helper()

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%q", res.Code, http.StatusOK, res.Body.String())
	}
	if body := res.Body.String(); body != "" {
		t.Fatalf("body = %q, want empty body with no structured allow override", body)
	}
}
