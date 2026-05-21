package audit

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWebhookExporter_Export(t *testing.T) {
	var mu sync.Mutex
	var captured []byte
	var capturedHeaders http.Header

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		capturedHeaders = r.Header.Clone()
		captured, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	exp := NewWebhookExporter(srv.URL)
	events := []SIEMEvent{
		{
			Timestamp: time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC),
			EventType: EventSafetyDecision,
			Severity:  SeverityInfo,
			TenantID:  "default",
			Action:    "lookup_balance",
			Decision:  "allow",
		},
		{
			Timestamp: time.Date(2026, 2, 10, 12, 0, 1, 0, time.UTC),
			EventType: EventSafetyViolation,
			Severity:  SeverityHigh,
			TenantID:  "default",
			Action:    "delete_account",
			Decision:  "deny",
			Reason:    "Destructive operations blocked",
		},
	}

	if err := exp.Export(t.Context(), events); err != nil {
		t.Fatalf("Export: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if ct := capturedHeaders.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var got []SIEMEvent
	if err := json.Unmarshal(captured, &got); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	if got[0].Action != "lookup_balance" {
		t.Errorf("event[0].Action = %q, want lookup_balance", got[0].Action)
	}
	if got[1].Decision != "deny" {
		t.Errorf("event[1].Decision = %q, want deny", got[1].Decision)
	}
}

func TestWebhookExporter_HMACSignature(t *testing.T) {
	secret := "test-webhook-secret"
	var capturedSig string
	var capturedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedSig = r.Header.Get("X-Cordum-Signature")
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	exp := NewWebhookExporter(srv.URL, WithWebhookSecret(secret))
	events := []SIEMEvent{{
		EventType: EventSafetyDecision,
		Action:    "test",
	}}

	if err := exp.Export(t.Context(), events); err != nil {
		t.Fatalf("Export: %v", err)
	}

	if !strings.HasPrefix(capturedSig, "sha256=") {
		t.Fatalf("signature prefix = %q, want sha256=...", capturedSig)
	}
	sigHex := strings.TrimPrefix(capturedSig, "sha256=")

	// Verify HMAC
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(capturedBody)
	expected := hex.EncodeToString(mac.Sum(nil))
	if sigHex != expected {
		t.Errorf("HMAC mismatch: got %s, want %s", sigHex, expected)
	}
}

func TestWebhookExporter_AgentFieldsSerialized(t *testing.T) {
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	exp := NewWebhookExporter(srv.URL)
	events := []SIEMEvent{{
		Timestamp:     time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC),
		EventType:     EventSafetyDecision,
		Severity:      SeverityInfo,
		TenantID:      "default",
		AgentID:       "agent-abc123",
		AgentName:     "research-bot",
		AgentRiskTier: "high",
		Action:        "submit",
		Decision:      "allow",
	}}

	if err := exp.Export(t.Context(), events); err != nil {
		t.Fatalf("Export: %v", err)
	}

	body := string(captured)
	if !strings.Contains(body, `"agent_id":"agent-abc123"`) {
		t.Errorf("webhook payload missing agent_id: %s", body)
	}
	if !strings.Contains(body, `"agent_name":"research-bot"`) {
		t.Errorf("webhook payload missing agent_name: %s", body)
	}
	if !strings.Contains(body, `"agent_risk_tier":"high"`) {
		t.Errorf("webhook payload missing agent_risk_tier: %s", body)
	}
}

func TestWebhookExporter_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	exp := NewWebhookExporter(srv.URL)
	err := exp.Export(t.Context(), []SIEMEvent{{Action: "test"}})
	if err == nil {
		t.Fatal("expected error on 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %q, want mention of 500", err)
	}
}

func TestWebhookExporter_CustomHeaders(t *testing.T) {
	var capturedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	exp := NewWebhookExporter(srv.URL, WithWebhookHeaders(map[string]string{
		"Authorization": "Bearer test-token",
	}))
	if err := exp.Export(t.Context(), []SIEMEvent{{Action: "test"}}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if capturedAuth != "Bearer test-token" {
		t.Errorf("Authorization = %q, want Bearer test-token", capturedAuth)
	}
}

type webhookSecretEnvCase struct {
	name      string
	typ       string
	secret    string
	wantErr   string
	wantWarn  string
	wantClean bool
}

func TestWithWebhookSecret_RejectsEmptyAndShortSecrets(t *testing.T) {
	tests := []webhookSecretEnvCase{
		{
			name:    "webhook_empty_rejected",
			typ:     "webhook",
			secret:  "",
			wantErr: "CORDUM_AUDIT_EXPORT_WEBHOOK_SECRET required",
		},
		{
			name:    "webhook_short_rejected",
			typ:     "webhook",
			secret:  "short",
			wantErr: "webhook secret must be >=32 chars",
		},
		{
			name:      "webhook_min_length_ok",
			typ:       "webhook",
			secret:    "12345678901234567890123456789012",
			wantClean: true,
		},
		{
			name:      "datadog_empty_irrelevant",
			typ:       "datadog",
			secret:    "",
			wantClean: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runWebhookSecretEnvCase(t, tc)
		})
	}
}

func runWebhookSecretEnvCase(t *testing.T, tc webhookSecretEnvCase) {
	t.Helper()
	t.Setenv("CORDUM_AUDIT_EXPORT_TYPE", tc.typ)
	t.Setenv("CORDUM_AUDIT_EXPORT_WEBHOOK_URL", "https://example.com/hook")
	t.Setenv("CORDUM_AUDIT_EXPORT_WEBHOOK_SECRET", tc.secret)
	t.Setenv("CORDUM_AUDIT_EXPORT_DD_API_KEY", "dd-api-key")

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	exp, err := NewExporterFromEnv()
	if exp != nil {
		t.Cleanup(func() { _ = exp.Close() })
	}
	if tc.wantErr != "" {
		assertWebhookSecretEnvError(t, err, tc.wantErr)
		return
	}
	assertWebhookSecretEnvOK(t, exp, err, buf.String(), tc)
}

func assertWebhookSecretEnvError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("NewExporterFromEnv() err = %v, want containing %q", err, want)
	}
}

func assertWebhookSecretEnvOK(t *testing.T, exp *BufferedExporter, err error, logs string, tc webhookSecretEnvCase) {
	t.Helper()
	if err != nil {
		t.Fatalf("NewExporterFromEnv() unexpected err: %v", err)
	}
	if tc.wantClean && exp == nil {
		t.Fatal("NewExporterFromEnv() returned nil exporter")
	}
	if tc.wantWarn != "" && !strings.Contains(logs, tc.wantWarn) {
		t.Fatalf("expected warning %q, got logs:\n%s", tc.wantWarn, logs)
	}
	if tc.wantWarn == "" && strings.Contains(logs, "payloads will be UNSIGNED") {
		t.Fatalf("unexpected unsigned webhook warning for type %q logs:\n%s", tc.typ, logs)
	}
}
