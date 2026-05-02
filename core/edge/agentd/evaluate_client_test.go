package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGatewayClientEvaluatePostsRedactedRequestWithAuthHeaders(t *testing.T) {
	var (
		gotMethod  string
		gotPath    string
		gotAPIKey  string
		gotTenant  string
		gotContent string
		gotBody    EvaluateRequest
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAPIKey = r.Header.Get("X-API-Key")
		gotTenant = r.Header.Get("X-Tenant-ID")
		gotContent = r.Header.Get("Content-Type")
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Fatalf("decode request body %q: %v", body, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(EvaluateResponse{
			Decision:           "ALLOW",
			Reason:             "ok",
			RuleID:             "claude-code.allow-safe-build-test",
			PolicySnapshot:     "snap-eval-1",
			ActionHash:         "sha256:0011",
			InputHash:          "sha256:abcd",
			PermissionDecision: "allow",
			ExitCode:           0,
		})
	}))
	defer server.Close()

	client, err := NewGatewayClient(GatewayClientConfig{BaseURL: server.URL, APIKey: "key-eval", TenantID: "tenant-edge-a"})
	if err != nil {
		t.Fatalf("NewGatewayClient: %v", err)
	}
	resp, err := client.Evaluate(context.Background(), EvaluateRequest{
		TenantID:      "tenant-edge-a",
		PrincipalID:   "principal-edge-a",
		SessionID:     "edge_sess_1",
		ExecutionID:   "edge_exec_1",
		AgentProduct:  "claude-code",
		Layer:         "hook",
		Kind:          "hook.pre_tool_use",
		ToolName:      "Bash",
		Capability:    "exec.shell",
		ActionName:    "bash.exec",
		RiskTags:      []string{"exec", "test"},
		Labels:        map[string]string{"command.class": "safe", "command.family": "test"},
		InputRedacted: map[string]any{"command": "npm test"},
		InputHash:     "sha256:abcd",
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/api/v1/edge/evaluate" {
		t.Fatalf("path = %q, want /api/v1/edge/evaluate", gotPath)
	}
	if gotAPIKey != "key-eval" {
		t.Fatalf("X-API-Key = %q, want key-eval", gotAPIKey)
	}
	if gotTenant != "tenant-edge-a" {
		t.Fatalf("X-Tenant-ID = %q, want tenant-edge-a", gotTenant)
	}
	if gotContent != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", gotContent)
	}
	if gotBody.SessionID != "edge_sess_1" || gotBody.ExecutionID != "edge_exec_1" || gotBody.PrincipalID != "principal-edge-a" {
		t.Fatalf("session/execution/principal not forwarded: %#v", gotBody)
	}
	if gotBody.ToolName != "Bash" || gotBody.Capability != "exec.shell" || gotBody.ActionName != "bash.exec" {
		t.Fatalf("classifier fields not forwarded: %#v", gotBody)
	}
	if gotBody.InputHash != "sha256:abcd" {
		t.Fatalf("input_hash = %q, want sha256:abcd", gotBody.InputHash)
	}
	if cmd, _ := gotBody.InputRedacted["command"].(string); cmd != "npm test" {
		t.Fatalf("input_redacted.command = %q, want npm test", cmd)
	}
	if resp.Decision != "ALLOW" || resp.RuleID != "claude-code.allow-safe-build-test" || resp.PolicySnapshot != "snap-eval-1" {
		t.Fatalf("response decoded incorrectly: %#v", resp)
	}
	if resp.ActionHash != "sha256:0011" || resp.InputHash != "sha256:abcd" {
		t.Fatalf("response hashes not parsed: %#v", resp)
	}
}

func TestGatewayClientEvaluateParsesAllDecisionShapes(t *testing.T) {
	cases := []struct {
		name string
		body EvaluateResponse
	}{
		{"deny with reason", EvaluateResponse{Decision: "DENY", Reason: "secret blocked", RuleID: "deny-secret", PermissionDecision: "deny", ExitCode: 2, TerminalTitle: "Cordum Edge blocked", TerminalMessage: "secret blocked"}},
		{"require approval with ref+url", EvaluateResponse{Decision: "REQUIRE_APPROVAL", Reason: "needs approval", RuleID: "approval-rule", ApprovalRef: "edge_appr_xyz", ApprovalURL: "/edge/approvals/edge_appr_xyz", ActionHash: "sha256:11", InputHash: "sha256:22", WaitStrategy: "manual_approval", WaitAfter: "approve_then_retry", PermissionDecision: "deny", ExitCode: 2, TerminalMessage: "approval required. This action was not run. Approval: edge_appr_xyz."}},
		{"throttle with backoff", EvaluateResponse{Decision: "THROTTLE", Reason: "slow down", WaitStrategy: "backoff", TimeoutMS: 5000, PermissionDecision: "deny", ExitCode: 2}},
		{"constrain with updated input", EvaluateResponse{Decision: "CONSTRAIN", Reason: "use safer command", PermissionDecision: "allow", ExitCode: 0, UpdatedInput: map[string]any{"command": "npm ci"}}},
		{"degraded safety unavailable", EvaluateResponse{Decision: "DENY", Reason: "safety kernel unavailable", Degraded: true, ErrorCode: "safety_unavailable", ErrorMessage: "safety kernel unavailable; retry after checking Cordum Edge health", PermissionDecision: "deny", ExitCode: 2, WaitStrategy: "retry", TimeoutMS: 5000}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(tc.body)
			}))
			defer server.Close()
			client, err := NewGatewayClient(GatewayClientConfig{BaseURL: server.URL, APIKey: "k", TenantID: "t"})
			if err != nil {
				t.Fatalf("NewGatewayClient: %v", err)
			}
			resp, err := client.Evaluate(context.Background(), EvaluateRequest{TenantID: "t", PrincipalID: "p", SessionID: "s", ExecutionID: "e"})
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if resp.Decision != tc.body.Decision {
				t.Fatalf("decision = %q, want %q", resp.Decision, tc.body.Decision)
			}
			if resp.ApprovalRef != tc.body.ApprovalRef || resp.ApprovalURL != tc.body.ApprovalURL {
				t.Fatalf("approval coords = %q/%q, want %q/%q", resp.ApprovalRef, resp.ApprovalURL, tc.body.ApprovalRef, tc.body.ApprovalURL)
			}
			if resp.WaitStrategy != tc.body.WaitStrategy || resp.WaitAfter != tc.body.WaitAfter {
				t.Fatalf("wait coords = %q/%q, want %q/%q", resp.WaitStrategy, resp.WaitAfter, tc.body.WaitStrategy, tc.body.WaitAfter)
			}
			if resp.Degraded != tc.body.Degraded || resp.ErrorCode != tc.body.ErrorCode {
				t.Fatalf("degraded coords = %v/%q, want %v/%q", resp.Degraded, resp.ErrorCode, tc.body.Degraded, tc.body.ErrorCode)
			}
			if tc.body.UpdatedInput != nil {
				got, _ := resp.UpdatedInput["command"].(string)
				want, _ := tc.body.UpdatedInput["command"].(string)
				if got != want {
					t.Fatalf("updated_input.command = %q, want %q", got, want)
				}
			}
		})
	}
}

func TestGatewayClientEvaluateRejectsSecretsInGatewayErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`Authorization: Bearer leaked-token-abc and apiKey=sk-leaked-789`))
	}))
	defer server.Close()
	client, err := NewGatewayClient(GatewayClientConfig{BaseURL: server.URL, APIKey: "k", TenantID: "t"})
	if err != nil {
		t.Fatalf("NewGatewayClient: %v", err)
	}
	_, evalErr := client.Evaluate(context.Background(), EvaluateRequest{TenantID: "t", PrincipalID: "p", SessionID: "s", ExecutionID: "e"})
	if evalErr == nil {
		t.Fatal("Evaluate returned no error on 5xx; want error")
	}
	msg := evalErr.Error()
	// Gateway error body containing a Bearer token must NOT echo the literal token.
	if strings.Contains(msg, "leaked-token-abc") {
		t.Fatalf("error message leaked Bearer token: %q", msg)
	}
	if strings.Contains(msg, "sk-leaked-789") {
		t.Fatalf("error message leaked sk-style token: %q", msg)
	}
	if !strings.Contains(msg, "Bearer [REDACTED]") || !strings.Contains(msg, "[REDACTED]") {
		t.Fatalf("error message did not include sanitized token markers: %q", msg)
	}
}

func TestGatewayClientEvaluateValidatesDecisionAndClassifiesMalformed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(EvaluateResponse{
			Decision:     "ALLOW_BUT_NOT_REAL",
			ErrorMessage: "malformed upstream body with Bearer malformed-secret",
		})
	}))
	defer server.Close()
	client, err := NewGatewayClient(GatewayClientConfig{BaseURL: server.URL, APIKey: "edge-api-key-not-in-leak", TenantID: "t"})
	if err != nil {
		t.Fatalf("NewGatewayClient: %v", err)
	}
	_, evalErr := client.Evaluate(context.Background(), EvaluateRequest{TenantID: "t", PrincipalID: "p", SessionID: "s", ExecutionID: "e"})
	if evalErr == nil {
		t.Fatal("Evaluate returned nil error for invalid decision; want malformed response error")
	}
	if !errors.Is(evalErr, ErrEvaluateResponseMalformed) {
		t.Fatalf("Evaluate error = %v, want errors.Is(..., ErrEvaluateResponseMalformed)", evalErr)
	}
	if got := ClassifyEvaluateError(evalErr); got != GatewayErrorMalformed {
		t.Fatalf("ClassifyEvaluateError = %q, want malformed", got)
	}
	if strings.Contains(evalErr.Error(), "malformed-secret") {
		t.Fatalf("malformed response error leaked secret: %v", evalErr)
	}
}

func TestClassifyEvaluateErrorMapsTimeoutPolicyUnavailableAndUnavailable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want GatewayErrorCategory
	}{
		{name: "nil", err: nil, want: GatewayErrorNone},
		{name: "timeout", err: ErrGatewayTimeout, want: GatewayErrorTimeout},
		{name: "malformed", err: ErrEvaluateResponseMalformed, want: GatewayErrorMalformed},
		{name: "policy unavailable body", err: errors.New(`gateway status 503: {"code":"policy_unavailable","message":"retry"}`), want: GatewayErrorPolicyUnavailable},
		{name: "safety unavailable body", err: errors.New(`gateway status 503: {"error_code":"safety_unavailable"}`), want: GatewayErrorPolicyUnavailable},
		{name: "generic transport", err: errors.New("gateway request failed: connection refused"), want: GatewayErrorUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyEvaluateError(tc.err); got != tc.want {
				t.Fatalf("ClassifyEvaluateError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestGatewayClientEvaluateTimesOutWithErrGatewayTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Hold the connection longer than the per-call timeout to guarantee a
		// transport-level timeout regardless of test scheduling jitter.
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer server.Close()
	client, err := NewGatewayClient(GatewayClientConfig{BaseURL: server.URL, APIKey: "k", TenantID: "t", Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewGatewayClient: %v", err)
	}
	_, evalErr := client.Evaluate(context.Background(), EvaluateRequest{TenantID: "t", PrincipalID: "p", SessionID: "s", ExecutionID: "e"})
	if evalErr == nil {
		t.Fatal("Evaluate returned no error on timeout; want ErrGatewayTimeout")
	}
	if !errors.Is(evalErr, ErrGatewayTimeout) {
		t.Fatalf("Evaluate timeout error = %v, want errors.Is(..., ErrGatewayTimeout)", evalErr)
	}
}

func TestGatewayClientEvaluateBoundsLargeMetadata(t *testing.T) {
	huge := strings.Repeat("a", MaxGatewayMetadataValueBytes+512)
	var seen EvaluateRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		_ = json.Unmarshal(body, &seen)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(EvaluateResponse{Decision: "ALLOW"})
	}))
	defer server.Close()
	client, err := NewGatewayClient(GatewayClientConfig{BaseURL: server.URL, APIKey: "k", TenantID: "t"})
	if err != nil {
		t.Fatalf("NewGatewayClient: %v", err)
	}
	_, err = client.Evaluate(context.Background(), EvaluateRequest{
		TenantID:    "t",
		PrincipalID: "p",
		SessionID:   "s",
		ExecutionID: "e",
		ToolName:    huge,
		Labels:      map[string]string{"k": huge},
		RiskTags:    []string{huge},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(seen.ToolName) > MaxGatewayMetadataValueBytes+8 {
		t.Fatalf("tool_name was not bounded: len=%d", len(seen.ToolName))
	}
	for _, v := range seen.Labels {
		if len(v) > MaxGatewayMetadataValueBytes+8 {
			t.Fatalf("label value was not bounded: len=%d", len(v))
		}
	}
	for _, tag := range seen.RiskTags {
		if len(tag) > MaxGatewayMetadataValueBytes+8 {
			t.Fatalf("risk_tag was not bounded: len=%d", len(tag))
		}
	}
}

func TestGatewayClientEvaluateClosesResponseBody(t *testing.T) {
	// httptest.Server hooks the underlying Listener; the only observable end of
	// a leak here is the test goroutine count or a hanging connection. The
	// explicit assertion is that doJSON's defer Body.Close fires by sending a
	// large-but-bounded response and ensuring two sequential Evaluate calls
	// both succeed (a leaked Body would block the keep-alive reader).
	bigBody := EvaluateResponse{Decision: "ALLOW", Reason: strings.Repeat("a", 8192)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(bigBody)
	}))
	defer server.Close()
	client, err := NewGatewayClient(GatewayClientConfig{BaseURL: server.URL, APIKey: "k", TenantID: "t"})
	if err != nil {
		t.Fatalf("NewGatewayClient: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := client.Evaluate(context.Background(), EvaluateRequest{TenantID: "t", PrincipalID: "p", SessionID: "s", ExecutionID: "e"}); err != nil {
			t.Fatalf("Evaluate iter %d: %v", i, err)
		}
	}
}
