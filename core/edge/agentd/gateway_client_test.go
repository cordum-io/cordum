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
)

func TestGatewayClientCreateSessionSendsAuthHeadersAndMetadataAndParsesResponse(t *testing.T) {
	t.Parallel()

	const apiKey = "super-secret-api-key-1234"
	var captured struct {
		Method string
		Path   string
		APIKey string
		Tenant string
		Body   createSessionRequestJSON
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.Method = r.Method
		captured.Path = r.URL.Path
		captured.APIKey = r.Header.Get("X-API-Key")
		captured.Tenant = r.Header.Get("X-Tenant-ID")
		if err := json.NewDecoder(r.Body).Decode(&captured.Body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if len(captured.Body.CWD) > MaxGatewayMetadataValueBytes {
			t.Fatalf("cwd length = %d, want <= %d", len(captured.Body.CWD), MaxGatewayMetadataValueBytes)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"session_id":      "sess-123",
			"execution_id":    "exec-456",
			"trace_id":        "trace-789",
			"policy_snapshot": "policy-snap-001",
			"dashboard_url":   "/edge/sessions/sess-123",
			"session": map[string]any{
				"session_id":         "sess-123",
				"tenant_id":          "tenant-a",
				"principal_id":       "principal-a",
				"principal_type":     "human",
				"mode":               "local-dev",
				"policy_mode":        "enforce",
				"status":             "running",
				"started_at":         "2026-05-02T07:00:00Z",
				"risk_summary":       map[string]any{"max_risk": "low"},
				"enforcement_layers": map[string]bool{"hook": true, "agentd": true},
			},
			"execution": map[string]any{
				"execution_id": "exec-456",
				"session_id":   "sess-123",
				"tenant_id":    "tenant-a",
				"adapter":      "claude-code-hook",
				"mode":         "local-dev",
				"status":       "running",
				"started_at":   "2026-05-02T07:00:00Z",
			},
		})
	}))
	t.Cleanup(server.Close)

	client, err := NewGatewayClient(GatewayClientConfig{
		BaseURL:    server.URL,
		APIKey:     apiKey,
		TenantID:   "tenant-a",
		HTTPClient: server.Client(),
		Timeout:    time.Second,
	})
	if err != nil {
		t.Fatalf("NewGatewayClient: %v", err)
	}

	longCWD := "D:/Cordum/cordum/" + strings.Repeat("x", MaxGatewayMetadataValueBytes+64)
	got, err := client.CreateSession(context.Background(), CreateSessionRequest{
		TenantID:       "tenant-a",
		PrincipalID:    "principal-a",
		PrincipalType:  edgecore.PrincipalTypeHuman,
		AgentProduct:   "claude-code",
		AgentVersion:   "1.2.3",
		Mode:           edgecore.SessionModeLocalDev,
		Repo:           "cordum",
		GitRemote:      "git@github.com:cordum-io/cordum.git",
		GitBranch:      "feature/cordum-edge-p0",
		GitSHA:         "abcdef123456",
		CWD:            longCWD,
		HostID:         "host-a",
		DeviceID:       "device-a",
		PolicySnapshot: "policy-snap-request",
		PolicyMode:     edgecore.PolicyModeEnforce,
		Labels:         edgecore.Labels{"source": "agentd", "local": "true"},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if captured.Method != http.MethodPost {
		t.Fatalf("method = %s, want POST", captured.Method)
	}
	if captured.Path != "/api/v1/edge/sessions" {
		t.Fatalf("path = %s, want /api/v1/edge/sessions", captured.Path)
	}
	if captured.APIKey != apiKey {
		t.Fatalf("X-API-Key = %q, want configured API key", captured.APIKey)
	}
	if captured.Tenant != "tenant-a" {
		t.Fatalf("X-Tenant-ID = %q, want tenant-a", captured.Tenant)
	}
	if captured.Body.TenantID != "tenant-a" || captured.Body.PrincipalID != "principal-a" {
		t.Fatalf("body tenant/principal = %q/%q", captured.Body.TenantID, captured.Body.PrincipalID)
	}
	if captured.Body.AgentProduct != "claude-code" || captured.Body.Mode != edgecore.SessionModeLocalDev {
		t.Fatalf("body agent/mode = %q/%q", captured.Body.AgentProduct, captured.Body.Mode)
	}
	if captured.Body.PolicyMode != edgecore.PolicyModeEnforce {
		t.Fatalf("body policy_mode = %q, want enforce", captured.Body.PolicyMode)
	}
	if captured.Body.EnforcementLayers["hook"] != true || captured.Body.EnforcementLayers["agentd"] != true {
		t.Fatalf("enforcement layers = %#v, want hook+agentd", captured.Body.EnforcementLayers)
	}
	if got.SessionID != "sess-123" || got.ExecutionID != "exec-456" || got.TraceID != "trace-789" {
		t.Fatalf("ids = %#v", got)
	}
	if got.PolicySnapshot != "policy-snap-001" || got.DashboardURL != "/edge/sessions/sess-123" {
		t.Fatalf("policy/dashboard = %q/%q", got.PolicySnapshot, got.DashboardURL)
	}
	if got.Session.SessionID != "sess-123" || got.Execution.ExecutionID != "exec-456" {
		t.Fatalf("nested session/execution not parsed: %#v", got)
	}
}

func TestGatewayClientCreateSessionRedactsAPIKeyFromStatusErrors(t *testing.T) {
	t.Parallel()

	const apiKey = "super-secret-api-key-1234"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid api key "+apiKey, http.StatusForbidden)
	}))
	t.Cleanup(server.Close)

	client, err := NewGatewayClient(GatewayClientConfig{
		BaseURL:    server.URL,
		APIKey:     apiKey,
		TenantID:   "tenant-a",
		HTTPClient: server.Client(),
		Timeout:    time.Second,
	})
	if err != nil {
		t.Fatalf("NewGatewayClient: %v", err)
	}

	_, err = client.CreateSession(context.Background(), CreateSessionRequest{
		TenantID:      "tenant-a",
		PrincipalID:   "principal-a",
		PrincipalType: edgecore.PrincipalTypeHuman,
	})
	if err == nil {
		t.Fatal("CreateSession returned nil error for 403")
	}
	if strings.Contains(err.Error(), apiKey) {
		t.Fatalf("error leaked API key: %v", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("error = %q, want redaction marker", err.Error())
	}
}

func TestGatewayClientCreateSessionTimesOutWithPerCallDeadline(t *testing.T) {
	t.Parallel()

	client, err := NewGatewayClient(GatewayClientConfig{
		BaseURL:  "http://127.0.0.1:65534",
		APIKey:   "secret-key",
		TenantID: "tenant-a",
		Timeout:  10 * time.Millisecond,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			<-req.Context().Done()
			return nil, req.Context().Err()
		})},
	})
	if err != nil {
		t.Fatalf("NewGatewayClient: %v", err)
	}

	start := time.Now()
	_, err = client.CreateSession(context.Background(), CreateSessionRequest{
		TenantID:      "tenant-a",
		PrincipalID:   "principal-a",
		PrincipalType: edgecore.PrincipalTypeHuman,
	})
	if err == nil {
		t.Fatal("CreateSession returned nil error for timeout")
	}
	if !errors.Is(err, ErrGatewayTimeout) {
		t.Fatalf("error = %v, want ErrGatewayTimeout", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("timeout took %s, want bounded by per-call timeout", elapsed)
	}
}

func TestGatewayClientLifecycleEndpointsUseEdgeRoutesOnly(t *testing.T) {
	t.Parallel()

	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/edge/sessions/sess-1/heartbeat":
			_ = json.NewEncoder(w).Encode(HeartbeatResponse{SessionID: "sess-1", HeartbeatAlive: true})
		case "/api/v1/edge/executions/exec-1/end":
			var body EndExecutionRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode execution end: %v", err)
			}
			if body.Status != edgecore.ExecutionStatusSucceeded {
				t.Fatalf("execution status = %q, want succeeded", body.Status)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"execution_id": "exec-1"})
		case "/api/v1/edge/sessions/sess-1/end":
			var body EndSessionRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode session end: %v", err)
			}
			if body.Status != edgecore.SessionStatusEnded {
				t.Fatalf("session status = %q, want ended", body.Status)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"session_id": "sess-1"})
		default:
			t.Fatalf("unexpected route %s %s; agentd must not use Job APIs", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewGatewayClient(GatewayClientConfig{BaseURL: server.URL, APIKey: "key", TenantID: "tenant-a", HTTPClient: server.Client(), Timeout: time.Second})
	if err != nil {
		t.Fatalf("NewGatewayClient: %v", err)
	}
	if _, err := client.Heartbeat(context.Background(), "sess-1"); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if err := client.EndExecution(context.Background(), "exec-1", EndExecutionRequest{Status: edgecore.ExecutionStatusSucceeded}); err != nil {
		t.Fatalf("EndExecution: %v", err)
	}
	if err := client.EndSession(context.Background(), "sess-1", EndSessionRequest{Status: edgecore.SessionStatusEnded}); err != nil {
		t.Fatalf("EndSession: %v", err)
	}
	got := strings.Join(paths, "\n")
	if strings.Contains(got, "/api/v1/jobs") {
		t.Fatalf("client used Job API route: %s", got)
	}
}

func TestGatewayClientMarkSessionDegradedWritesBoundedEdgeEventNoSecretEcho(t *testing.T) {
	t.Parallel()

	const secret = "sk-test-secret-1234"
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/edge/events" {
			t.Fatalf("path = %s, want /api/v1/edge/events", r.URL.Path)
		}
		if strings.Contains(r.URL.Path, "/jobs") {
			t.Fatalf("used Job API route: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode event: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(captured)
	}))
	t.Cleanup(server.Close)

	client, err := NewGatewayClient(GatewayClientConfig{BaseURL: server.URL, APIKey: "key", TenantID: "tenant-a", HTTPClient: server.Client(), Timeout: time.Second})
	if err != nil {
		t.Fatalf("NewGatewayClient: %v", err)
	}
	reason := "gateway timeout with bearer " + secret + " " + strings.Repeat("x", MaxGatewayMetadataValueBytes+100)
	_, err = client.MarkSessionDegraded(context.Background(), SessionState{
		SessionID:      "sess-1",
		ExecutionID:    "exec-1",
		TenantID:       "tenant-a",
		PrincipalID:    "principal-a",
		PolicySnapshot: "snap-1",
	}, reason)
	if err != nil {
		t.Fatalf("MarkSessionDegraded: %v", err)
	}
	if captured["tenant_id"] != "tenant-a" || captured["session_id"] != "sess-1" || captured["execution_id"] != "exec-1" {
		t.Fatalf("event identity = %#v", captured)
	}
	if captured["kind"] != string(edgecore.EventKindSessionDegraded) || captured["decision"] != string(edgecore.DecisionRecorded) || captured["status"] != string(edgecore.ActionStatusDegraded) {
		t.Fatalf("event kind/decision/status = %#v", captured)
	}
	body, _ := json.Marshal(captured)
	if strings.Contains(string(body), secret) {
		t.Fatalf("degraded event leaked secret: %s", string(body))
	}
	if len(captured["decision_reason"].(string)) > MaxGatewayMetadataValueBytes {
		t.Fatalf("decision_reason len = %d, want <= %d", len(captured["decision_reason"].(string)), MaxGatewayMetadataValueBytes)
	}
}

func TestGatewayClientWriteEventsUsesAtomicBatchEndpoint(t *testing.T) {
	t.Parallel()

	var captured struct {
		Method         string
		Path           string
		IdempotencyKey string
		Body           struct {
			Events []edgecore.AgentActionEvent `json:"events"`
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.Method = r.Method
		captured.Path = r.URL.Path
		captured.IdempotencyKey = r.Header.Get("Idempotency-Key")
		if strings.Contains(r.URL.Path, "/jobs") {
			t.Fatalf("used Job API route: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured.Body); err != nil {
			t.Fatalf("decode batch request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"items": captured.Body.Events})
	}))
	t.Cleanup(server.Close)

	client, err := NewGatewayClient(GatewayClientConfig{BaseURL: server.URL, APIKey: "key", TenantID: "tenant-a", HTTPClient: server.Client(), Timeout: time.Second})
	if err != nil {
		t.Fatalf("NewGatewayClient: %v", err)
	}
	events := []edgecore.AgentActionEvent{
		{
			EventID:        "evt-receipt",
			SessionID:      "sess-1",
			ExecutionID:    "exec-1",
			TenantID:       "tenant-a",
			PrincipalID:    "principal-a",
			Timestamp:      time.Unix(10, 0).UTC(),
			Layer:          edgecore.LayerHook,
			Kind:           edgecore.EventKindHookPreToolUse,
			Decision:       edgecore.DecisionRecorded,
			PolicySnapshot: "snap-1",
		},
		{
			EventID:        "evt-decision",
			SessionID:      "sess-1",
			ExecutionID:    "exec-1",
			TenantID:       "tenant-a",
			PrincipalID:    "principal-a",
			Timestamp:      time.Unix(11, 0).UTC(),
			Layer:          edgecore.LayerHook,
			Kind:           edgecore.EventKindHookPolicyDecision,
			Decision:       edgecore.DecisionAllow,
			PolicySnapshot: "snap-1",
		},
	}
	got, err := client.WriteEventsWithIdempotency(context.Background(), events, "agentd-hook-idem-test")
	if err != nil {
		t.Fatalf("WriteEventsWithIdempotency: %v", err)
	}
	if captured.Method != http.MethodPost || captured.Path != "/api/v1/edge/events/batch" {
		t.Fatalf("route = %s %s, want POST /api/v1/edge/events/batch", captured.Method, captured.Path)
	}
	if captured.IdempotencyKey != "agentd-hook-idem-test" {
		t.Fatalf("Idempotency-Key = %q, want agentd-hook-idem-test", captured.IdempotencyKey)
	}
	if len(captured.Body.Events) != 2 || captured.Body.Events[0].EventID != "evt-receipt" || captured.Body.Events[1].EventID != "evt-decision" {
		t.Fatalf("captured batch events = %#v, want receipt+decision", captured.Body.Events)
	}
	if len(got) != 2 || got[0].EventID != "evt-receipt" || got[1].Decision != edgecore.DecisionAllow {
		t.Fatalf("WriteEvents response = %#v, want echoed batch", got)
	}
}

type createSessionRequestJSON struct {
	TenantID          string                     `json:"tenant_id"`
	PrincipalID       string                     `json:"principal_id"`
	PrincipalType     edgecore.PrincipalType     `json:"principal_type"`
	AgentProduct      string                     `json:"agent_product"`
	AgentVersion      string                     `json:"agent_version"`
	Mode              edgecore.SessionMode       `json:"mode"`
	Repo              string                     `json:"repo"`
	GitRemote         string                     `json:"git_remote"`
	GitBranch         string                     `json:"git_branch"`
	GitSHA            string                     `json:"git_sha"`
	CWD               string                     `json:"cwd"`
	HostID            string                     `json:"host_id"`
	DeviceID          string                     `json:"device_id"`
	PolicySnapshot    string                     `json:"policy_snapshot"`
	EnforcementLayers edgecore.EnforcementLayers `json:"enforcement_layers"`
	PolicyMode        edgecore.PolicyMode        `json:"policy_mode"`
	Labels            edgecore.Labels            `json:"labels"`
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
