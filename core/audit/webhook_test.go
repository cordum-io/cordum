package audit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
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
