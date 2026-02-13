package audit

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestDatadogExporter_Export(t *testing.T) {
	var mu sync.Mutex
	var capturedBody []byte
	var capturedHeaders http.Header

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		capturedHeaders = r.Header.Clone()
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	exp := NewDatadogExporter("test-api-key")
	exp.endpoint = srv.URL
	exp.hostname = "test-host"

	events := []SIEMEvent{
		{
			Timestamp: time.Date(2026, 2, 13, 12, 0, 0, 0, time.UTC),
			EventType: EventSafetyDecision,
			Severity:  SeverityInfo,
			TenantID:  "default",
			Action:    "lookup_balance",
			Decision:  "allow",
		},
		{
			Timestamp: time.Date(2026, 2, 13, 12, 0, 1, 0, time.UTC),
			EventType: EventSafetyViolation,
			Severity:  SeverityHigh,
			TenantID:  "default",
			Action:    "delete_account",
			Decision:  "deny",
		},
	}

	if err := exp.Export(t.Context(), events); err != nil {
		t.Fatalf("Export: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// Check headers.
	if ct := capturedHeaders.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if apiKey := capturedHeaders.Get("DD-API-KEY"); apiKey != "test-api-key" {
		t.Errorf("DD-API-KEY = %q, want test-api-key", apiKey)
	}

	// Parse and validate body.
	var entries []ddLogEntry
	if err := json.Unmarshal(capturedBody, &entries); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}

	// Verify first entry fields.
	if entries[0].DDSource != "cordum" {
		t.Errorf("ddsource = %q, want cordum", entries[0].DDSource)
	}
	if entries[0].Service != "cordum" {
		t.Errorf("service = %q, want cordum", entries[0].Service)
	}
	if entries[0].Hostname != "test-host" {
		t.Errorf("hostname = %q, want test-host", entries[0].Hostname)
	}
	if entries[0].DDTags != "service:cordum-gateway" {
		t.Errorf("ddtags = %q, want service:cordum-gateway", entries[0].DDTags)
	}

	// Verify message contains the original event JSON.
	var ev SIEMEvent
	if err := json.Unmarshal([]byte(entries[0].Message), &ev); err != nil {
		t.Fatalf("unmarshal message: %v", err)
	}
	if ev.Action != "lookup_balance" {
		t.Errorf("message.Action = %q, want lookup_balance", ev.Action)
	}
}

func TestDatadogExporter_SiteMapping(t *testing.T) {
	tests := []struct {
		site     string
		wantHost string
	}{
		{"us1", "http-intake.logs.datadoghq.com"},
		{"us3", "http-intake.logs.us3.datadoghq.com"},
		{"us5", "http-intake.logs.us5.datadoghq.com"},
		{"eu1", "http-intake.logs.datadoghq.eu"},
		{"ap1", "http-intake.logs.ap1.datadoghq.com"},
	}

	for _, tc := range tests {
		t.Run(tc.site, func(t *testing.T) {
			exp := NewDatadogExporter("key", WithDatadogSite(tc.site))
			want := "https://" + tc.wantHost + "/api/v2/logs"
			if exp.endpoint != want {
				t.Errorf("endpoint = %q, want %q", exp.endpoint, want)
			}
		})
	}
}

func TestDatadogExporter_DefaultSiteUS1(t *testing.T) {
	exp := NewDatadogExporter("key")
	want := "https://http-intake.logs.datadoghq.com/api/v2/logs"
	if exp.endpoint != want {
		t.Errorf("default endpoint = %q, want %q", exp.endpoint, want)
	}
}

func TestDatadogExporter_UnknownSiteKeepsDefault(t *testing.T) {
	exp := NewDatadogExporter("key", WithDatadogSite("unknown-site"))
	want := "https://http-intake.logs.datadoghq.com/api/v2/logs"
	if exp.endpoint != want {
		t.Errorf("endpoint = %q, want %q (should keep default for unknown site)", exp.endpoint, want)
	}
}

func TestDatadogExporter_CustomTags(t *testing.T) {
	exp := NewDatadogExporter("key", WithDatadogTags("env:staging,team:infra"))
	if exp.tags != "env:staging,team:infra" {
		t.Errorf("tags = %q, want env:staging,team:infra", exp.tags)
	}
}

func TestDatadogExporter_HTTPError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"rate_limited", http.StatusTooManyRequests},
		{"server_error", http.StatusInternalServerError},
		{"forbidden", http.StatusForbidden},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
			}))
			defer srv.Close()

			exp := NewDatadogExporter("key")
			exp.endpoint = srv.URL

			err := exp.Export(t.Context(), []SIEMEvent{{Action: "test"}})
			if err == nil {
				t.Fatalf("expected error for status %d", tc.statusCode)
			}
		})
	}
}

func TestDatadogExporter_BatchSerialization(t *testing.T) {
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	exp := NewDatadogExporter("key", WithDatadogTags("env:test"))
	exp.endpoint = srv.URL
	exp.hostname = "batch-host"

	events := make([]SIEMEvent, 5)
	for i := range events {
		events[i] = SIEMEvent{
			EventType: EventSafetyDecision,
			Action:    "batch-action",
			TenantID:  "tenant-1",
		}
	}

	if err := exp.Export(t.Context(), events); err != nil {
		t.Fatalf("Export: %v", err)
	}

	var entries []ddLogEntry
	if err := json.Unmarshal(captured, &entries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(entries) != 5 {
		t.Errorf("batch size = %d, want 5", len(entries))
	}
	for i, e := range entries {
		if e.DDTags != "env:test" {
			t.Errorf("entries[%d].DDTags = %q, want env:test", i, e.DDTags)
		}
	}
}

func TestDatadogExporter_Close(t *testing.T) {
	exp := NewDatadogExporter("key")
	if err := exp.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}
