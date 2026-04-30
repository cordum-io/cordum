package claude

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPAgentdClientPostsBoundedRequestToLoopback(t *testing.T) {
	seen := make(chan AgentdRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method=%s, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); !strings.Contains(got, "application/json") {
			t.Fatalf("Content-Type=%q, want application/json", got)
		}
		var req AgentdRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			t.Fatalf("decode agentd request: %v", err)
		}
		seen <- req
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"decision":"allow","reason":"loopback ok"}`))
	}))
	defer server.Close()

	client, err := NewHTTPAgentdClient(server.URL, time.Second)
	if err != nil {
		t.Fatalf("NewHTTPAgentdClient returned error: %v", err)
	}
	decision, err := client.EvaluateHook(context.Background(), AgentdRequest{
		EventName:   "PreToolUse",
		SessionID:   "sess-123",
		ExecutionID: "exec-456",
		ToolName:    "Bash",
		ToolUseID:   "toolu-789",
		RawPayload:  []byte(`{"hook_event_name":"PreToolUse"}`),
	})
	if err != nil {
		t.Fatalf("EvaluateHook returned error: %v", err)
	}
	if decision.Decision != DecisionAllow || decision.Reason != "loopback ok" {
		t.Fatalf("decision=%#v", decision)
	}
	got := <-seen
	if got.EventName != "PreToolUse" || got.SessionID != "sess-123" || got.ExecutionID != "exec-456" || got.ToolName != "Bash" || got.ToolUseID != "toolu-789" {
		t.Fatalf("unexpected request fields: %#v", got)
	}
	if string(got.RawPayload) != `{"hook_event_name":"PreToolUse"}` {
		t.Fatalf("raw payload mismatch: %q", got.RawPayload)
	}
}

func TestHTTPAgentdClientRejectsRemoteGatewayURLs(t *testing.T) {
	for _, rawURL := range []string{
		"https://api.cordum.example/v1/edge/hooks/claude",
		"http://10.0.0.5:8765/v1/edge/hooks/claude",
	} {
		if _, err := NewHTTPAgentdClient(rawURL, time.Second); err == nil {
			t.Fatalf("NewHTTPAgentdClient(%q) returned nil error; remote agentd URLs must be rejected", rawURL)
		}
	}
}

func TestRunUsesLoopbackAgentdURLFromEnvironment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"decision":"allow","reason":"env loopback"}`))
	}))
	defer server.Close()
	t.Setenv("CORDUM_AGENTD_URL", server.URL)

	code, stdout, stderr := runHook(t, RunOptions{
		Args:  []string{"claude", "pre-tool-use"},
		Stdin: hookInput(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"npm test"}}`),
	})
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr)
	}
	assertCompactJSON(t, stdout, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"env loopback"}}`)
}
