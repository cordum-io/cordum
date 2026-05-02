package agentd

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/claude"
)

func TestLocalServerRejectsRemoteAndBroadBindAddresses(t *testing.T) {
	t.Parallel()

	for _, rawURL := range []string{
		"http://0.0.0.0:8765/v1/edge/hooks/claude",
		"http://192.168.1.20:8765/v1/edge/hooks/claude",
		"http://[::]:8765/v1/edge/hooks/claude",
	} {
		t.Run(rawURL, func(t *testing.T) {
			t.Parallel()
			_, err := NewLocalServer(LocalServerConfig{BindURL: rawURL, Nonce: "nonce-123"})
			if err == nil {
				t.Fatalf("NewLocalServer(%q) returned nil error, want local-only rejection", rawURL)
			}
		})
	}
}

func TestLocalServerLoopbackRequiresNonceAndBoundsRoutesMethodsAndBody(t *testing.T) {
	t.Parallel()

	server, err := NewLocalServer(LocalServerConfig{
		BindURL:      "http://127.0.0.1:8765/v1/edge/hooks/claude",
		Nonce:        "nonce-123",
		MaxBodyBytes: 128,
	})
	if err != nil {
		t.Fatalf("NewLocalServer: %v", err)
	}
	handler := server.Handler()

	validBody := `{"event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"npm test"}}`
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		nonce  string
		want   int
	}{
		{name: "missing nonce", method: http.MethodPost, path: "/v1/edge/hooks/claude", body: validBody, want: http.StatusUnauthorized},
		{name: "bad nonce", method: http.MethodPost, path: "/v1/edge/hooks/claude", body: validBody, nonce: "wrong", want: http.StatusUnauthorized},
		{name: "unknown route", method: http.MethodPost, path: "/v1/edge/admin", body: validBody, nonce: "nonce-123", want: http.StatusNotFound},
		{name: "wrong method", method: http.MethodGet, path: "/v1/edge/hooks/claude", nonce: "nonce-123", want: http.StatusMethodNotAllowed},
		{name: "oversize body", method: http.MethodPost, path: "/v1/edge/hooks/claude", body: `{"event_name":"` + strings.Repeat("x", 256) + `"}`, nonce: "nonce-123", want: http.StatusRequestEntityTooLarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if tc.nonce != "" {
				req.Header.Set("X-Cordum-Agentd-Nonce", tc.nonce)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != tc.want {
				t.Fatalf("status = %d body=%q, want %d", rr.Code, rr.Body.String(), tc.want)
			}
			if strings.Contains(rr.Body.String(), "nonce-123") {
				t.Fatalf("response leaked nonce: %q", rr.Body.String())
			}
		})
	}
}

func TestLocalServerAcceptsNonceQueryForCordumHookURLCompatibility(t *testing.T) {
	t.Parallel()

	server, err := NewLocalServer(LocalServerConfig{
		BindURL:      "http://127.0.0.1:8765/v1/edge/hooks/claude",
		Nonce:        "nonce-123",
		MaxBodyBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("NewLocalServer: %v", err)
	}
	if got := server.HookURLWithNonce(); got != "http://127.0.0.1:8765/v1/edge/hooks/claude?nonce=nonce-123" {
		t.Fatalf("HookURLWithNonce = %q", got)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/edge/hooks/claude?nonce=nonce-123", strings.NewReader(`{"event_name":"PreToolUse"}`))
	rr := httptest.NewRecorder()
	server.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "nonce-123") {
		t.Fatalf("response leaked nonce: %q", rr.Body.String())
	}
}

func TestLocalServerDeprecatedNonceQueryLogsWarningWithoutValue(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	server, err := NewLocalServer(LocalServerConfig{
		BindURL:      "http://127.0.0.1:8765/v1/edge/hooks/claude",
		Nonce:        "nonce-123",
		MaxBodyBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("NewLocalServer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/edge/hooks/claude?nonce=nonce-123", strings.NewReader(`{"event_name":"PreToolUse"}`))
	rr := httptest.NewRecorder()
	server.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rr.Code, rr.Body.String())
	}
	logText := logs.String()
	if !strings.Contains(logText, "deprecated agentd hook nonce query parameter used") {
		t.Fatalf("query nonce warning missing: %q", logText)
	}
	if strings.Contains(logText, "nonce-123") {
		t.Fatalf("query nonce warning leaked nonce value: %q", logText)
	}
}

func TestSameUserImpersonationCannotForgeHookFromSettingsOnly(t *testing.T) {
	const syntheticNonce = "f00ddeadbeefcafe0123456789abcdef"
	server, err := NewLocalServer(LocalServerConfig{
		BindURL:      "http://127.0.0.1:8765/v1/edge/hooks/claude",
		Nonce:        syntheticNonce,
		MaxBodyBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("NewLocalServer: %v", err)
	}
	settingsJSON, err := claude.GenerateDevSettingsJSON(claude.DevSettingsOptions{
		SessionID:           "sess-impersonation",
		ExecutionID:         "exec-impersonation",
		AgentdURL:           server.HookURLWithNonce(),
		AgentdHookNonce:     syntheticNonce,
		HookCommand:         "cordum-hook",
		HookTimeout:         claude.DefaultHookTimeout,
		PolicyMode:          "local-dev-enforce",
		ApprovalWaitTimeout: 30 * time.Second,
		Platform:            "linux",
	})
	if err != nil {
		t.Fatalf("GenerateDevSettingsJSON: %v", err)
	}
	settingsText := string(settingsJSON)
	if strings.Contains(settingsText, syntheticNonce) {
		t.Fatalf("settings leaked synthetic nonce: %s", settingsText)
	}
	if match := regexp.MustCompile(`(?i)nonce=[0-9a-f]{32}`).FindString(settingsText); match != "" {
		t.Fatalf("settings reader could extract nonce query %q from %s", match, settingsText)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/edge/hooks/claude", strings.NewReader(`{"event_name":"PreToolUse"}`))
	rr := httptest.NewRecorder()
	server.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("settings-only impersonation status = %d body=%q, want 401", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), syntheticNonce) {
		t.Fatalf("unauthorized response leaked nonce: %q", rr.Body.String())
	}
}

func TestLocalServerValidHookReturnsSafeNotReadyDecisionWithoutSecretEcho(t *testing.T) {
	t.Parallel()

	const secret = "sk-test-secret-123"
	server, err := NewLocalServer(LocalServerConfig{
		BindURL:      "http://127.0.0.1:8765/v1/edge/hooks/claude",
		Nonce:        "nonce-123",
		MaxBodyBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("NewLocalServer: %v", err)
	}

	body, err := json.Marshal(claude.AgentdRequest{
		EventName:  "PreToolUse",
		SessionID:  "sess-1",
		ToolName:   "Bash",
		ToolInput:  map[string]any{"command": "echo " + secret},
		RawPayload: []byte(`{"authorization":"Bearer ` + secret + `"}`),
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/edge/hooks/claude", bytes.NewReader(body))
	req.Header.Set("X-Cordum-Agentd-Nonce", "nonce-123")
	rr := httptest.NewRecorder()
	server.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), secret) || strings.Contains(rr.Body.String(), "nonce-123") {
		t.Fatalf("response leaked secret/nonce: %q", rr.Body.String())
	}
	var decision claude.AgentdDecision
	if err := json.Unmarshal(rr.Body.Bytes(), &decision); err != nil {
		t.Fatalf("decode decision: %v", err)
	}
	if decision.Decision != claude.DecisionDeny {
		t.Fatalf("decision = %q, want fail-closed deny until EDGE-018 evaluate wiring", decision.Decision)
	}
	if !strings.Contains(strings.ToLower(decision.Reason), "not ready") {
		t.Fatalf("reason = %q, want explicit not-ready guidance", decision.Reason)
	}
}

func TestLocalServerAcceptedHookWritesBoundedEvidenceEvent(t *testing.T) {
	t.Parallel()

	const secret = "sk-test-secret-123"
	writer := &stubEventWriter{}
	server, err := NewLocalServer(LocalServerConfig{
		BindURL:      "http://127.0.0.1:8765/v1/edge/hooks/claude",
		Nonce:        "nonce-123",
		MaxBodyBytes: 1 << 20,
		State: SessionState{
			SessionID:      "sess-1",
			ExecutionID:    "exec-1",
			TenantID:       "tenant-a",
			PrincipalID:    "principal-a",
			TraceID:        "trace-1",
			PolicySnapshot: "snap-1",
		},
		EventWriter: writer,
	})
	if err != nil {
		t.Fatalf("NewLocalServer: %v", err)
	}
	body, err := json.Marshal(claude.AgentdRequest{
		EventName:     "PreToolUse",
		SessionID:     "sess-1",
		ExecutionID:   "exec-1",
		ToolName:      "Bash",
		ToolUseID:     "toolu-1",
		Capability:    "exec.shell",
		RiskTags:      []string{"shell"},
		InputHash:     "sha256:abc",
		ActionHash:    "sha256:def",
		InputRedacted: map[string]any{"command": "[REDACTED]"},
		ToolInput:     map[string]any{"command": "echo " + secret},
		RawPayload:    []byte(`{"authorization":"Bearer ` + secret + `"}`),
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/edge/hooks/claude", bytes.NewReader(body))
	req.Header.Set("X-Cordum-Agentd-Nonce", "nonce-123")
	rr := httptest.NewRecorder()
	server.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q, want 200", rr.Code, rr.Body.String())
	}
	if len(writer.events) != 1 {
		t.Fatalf("events written = %d, want 1", len(writer.events))
	}
	event := writer.events[0]
	if event.TenantID != "tenant-a" || event.SessionID != "sess-1" || event.ExecutionID != "exec-1" || event.PolicySnapshot != "snap-1" {
		t.Fatalf("event identity/policy = %#v", event)
	}
	if event.Kind != edgecore.EventKindHookPreToolUse || event.Layer != edgecore.LayerHook {
		t.Fatalf("event kind/layer = %q/%q", event.Kind, event.Layer)
	}
	if event.InputHash != "sha256:abc" || event.Labels["action_hash"] != "sha256:def" {
		t.Fatalf("event hashes = input:%q labels:%#v", event.InputHash, event.Labels)
	}
	eventJSON, _ := json.Marshal(event)
	if strings.Contains(string(eventJSON), secret) || strings.Contains(string(eventJSON), "RawPayload") {
		t.Fatalf("event leaked raw secret/payload: %s", string(eventJSON))
	}
	if got := event.InputRedacted["command"]; got != "[REDACTED]" {
		t.Fatalf("event input_redacted command = %#v", got)
	}
}

func TestLocalServerRejectsMismatchedSessionIDsWithoutWritingEvent(t *testing.T) {
	t.Parallel()

	writer := &stubEventWriter{}
	server, err := NewLocalServer(LocalServerConfig{
		BindURL:      "http://127.0.0.1:8765/v1/edge/hooks/claude",
		Nonce:        "nonce-123",
		MaxBodyBytes: 1 << 20,
		State:        SessionState{SessionID: "sess-1", ExecutionID: "exec-1", TenantID: "tenant-a"},
		EventWriter:  writer,
	})
	if err != nil {
		t.Fatalf("NewLocalServer: %v", err)
	}
	body := `{"event_name":"PreToolUse","session_id":"other","execution_id":"exec-1"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/edge/hooks/claude", strings.NewReader(body))
	req.Header.Set("X-Cordum-Agentd-Nonce", "nonce-123")
	rr := httptest.NewRecorder()
	server.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%q, want 409", rr.Code, rr.Body.String())
	}
	if len(writer.events) != 0 {
		t.Fatalf("events written on mismatch = %d, want 0", len(writer.events))
	}
}

type stubEventWriter struct {
	events []edgecore.AgentActionEvent
}

func (w *stubEventWriter) WriteEvent(_ context.Context, event edgecore.AgentActionEvent) (edgecore.AgentActionEvent, error) {
	w.events = append(w.events, event)
	return event, nil
}

func TestPrepareUnixSocketPathUsesUserOnlyDirectoryPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket chmod semantics are not available on Windows")
	}
	t.Parallel()

	socketPath := t.TempDir() + "/nested/agentd.sock"
	if err := PrepareUnixSocketPath(context.Background(), socketPath); err != nil {
		t.Fatalf("PrepareUnixSocketPath: %v", err)
	}
	info, err := statPathMode(socketPath[:strings.LastIndex(socketPath, "/")])
	if err != nil {
		t.Fatalf("stat socket directory: %v", err)
	}
	if got := info.Perm(); got != 0o700 {
		t.Fatalf("socket directory perm = %o, want 0700", got)
	}
}
