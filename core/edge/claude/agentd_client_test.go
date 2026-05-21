package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPAgentdClientPostsBoundedRequestToLoopback(t *testing.T) {
	seen := make(chan AgentdRequest, 1)
	handlerErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			reportAgentdHandlerErr(handlerErr, fmt.Errorf("method=%s, want POST", r.Method))
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if got := r.Header.Get("Content-Type"); !strings.Contains(got, "application/json") {
			reportAgentdHandlerErr(handlerErr, fmt.Errorf("Content-Type=%q, want application/json", got))
			http.Error(w, "bad content type", http.StatusUnsupportedMediaType)
			return
		}
		var req AgentdRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			reportAgentdHandlerErr(handlerErr, fmt.Errorf("decode agentd request: %w", err))
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		seen <- req
		reportAgentdHandlerErr(handlerErr, nil)
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
	assertAgentdHandlerErr(t, handlerErr)
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

func TestRunAuthenticatesViaHeaderFromEnv(t *testing.T) {
	handlerErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Cordum-Agentd-Nonce"); got != syntheticAgentdHexNonce {
			reportAgentdHandlerErr(handlerErr, fmt.Errorf("X-Cordum-Agentd-Nonce=%q, want synthetic env nonce", got))
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if strings.Contains(r.URL.RawQuery, "nonce=") {
			reportAgentdHandlerErr(handlerErr, fmt.Errorf("request URL leaked nonce query: %s", r.URL.String()))
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		reportAgentdHandlerErr(handlerErr, nil)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"decision":"allow","reason":"header nonce ok"}`))
	}))
	defer server.Close()

	code, stdout, stderr := runHook(t, RunOptions{
		Args:  []string{"claude", "pre-tool-use"},
		Stdin: hookInput(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"npm test"}}`),
		Env: map[string]string{
			"CORDUM_AGENTD_URL":        server.URL,
			"CORDUM_AGENTD_HOOK_NONCE": syntheticAgentdHexNonce,
		},
	})
	assertAgentdHandlerErr(t, handlerErr)
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr should be empty for header-auth allow, got %q", stderr)
	}
	assertCompactJSON(t, stdout, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"header nonce ok"}}`)
	if strings.Contains(stdout+stderr, syntheticAgentdHexNonce) {
		t.Fatalf("hook output leaked nonce: stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestRunLegacyNonceURLWithoutEnvFailsClosedClearly(t *testing.T) {
	code, stdout, stderr := runHook(t, RunOptions{
		Args:  []string{"claude", "pre-tool-use"},
		Stdin: hookInput(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"npm test"}}`),
		Env: map[string]string{
			"CORDUM_AGENTD_URL":         "http://127.0.0.1:8765/v1/edge/hooks/claude?nonce=" + syntheticAgentdHexNonce,
			"CORDUM_AGENTD_FAIL_CLOSED": "true",
		},
	})
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr)
	}
	assertCompactJSON(t, stdout, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Cordum Edge unavailable; blocking by fail-closed policy"}}`)
	if !strings.Contains(stderr, "CORDUM_AGENTD_HOOK_NONCE") || !strings.Contains(stderr, "not embedded in CORDUM_AGENTD_URL") {
		t.Fatalf("stderr missing clear nonce migration error: %q", stderr)
	}
	if strings.Contains(stdout+stderr, syntheticAgentdHexNonce) {
		t.Fatalf("fail-closed output leaked nonce: stdout=%q stderr=%q", stdout, stderr)
	}
}

func reportAgentdHandlerErr(ch chan<- error, err error) {
	select {
	case ch <- err:
	default:
	}
}

func assertAgentdHandlerErr(t *testing.T, ch <-chan error) {
	t.Helper()
	select {
	case err := <-ch:
		if err != nil {
			t.Fatalf("agentd test handler failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("agentd test handler did not report its assertion result")
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

func TestNewHTTPAgentdClientStrictLoopbackContract(t *testing.T) {
	for _, rawURL := range []string{
		"http://127.0.0.1:8765/v1/edge/hooks/claude",
		"http://[::1]:8765/v1/edge/hooks/claude",
		"http://localhost:8765/v1/edge/hooks/claude",
	} {
		t.Run("accept_"+rawURL, func(t *testing.T) {
			if _, err := NewHTTPAgentdClient(rawURL, time.Second); err != nil {
				t.Fatalf("NewHTTPAgentdClient(%q) returned error: %v", rawURL, err)
			}
		})
	}
	t.Run("reject_non_canonical_127_alias", func(t *testing.T) {
		rawURL := "http://127.0.0.5:8765/v1/edge/hooks/claude"
		_, err := NewHTTPAgentdClient(rawURL, time.Second)
		if err == nil {
			t.Fatalf("NewHTTPAgentdClient(%q) returned nil error", rawURL)
		}
		if !strings.Contains(err.Error(), "loopback/local") {
			t.Fatalf("NewHTTPAgentdClient(%q) err = %v, want loopback/local", rawURL, err)
		}
	})
}

func TestIsLoopbackHost_StrictAllowlist(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{host: "127.0.0.1", want: true},
		{host: "::1", want: true},
		{host: "localhost", want: true},
		{host: "127.0.0.5", want: false},
		{host: "127.1.2.3", want: false},
		{host: "10.0.0.1", want: false},
		{host: "example.com", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			if got := isLoopbackHost(tc.host); got != tc.want {
				t.Fatalf("isLoopbackHost(%q) = %v, want %v", tc.host, got, tc.want)
			}
		})
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
