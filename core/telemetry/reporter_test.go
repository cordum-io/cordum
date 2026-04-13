package telemetry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReporterRetriesUntilSuccess(t *testing.T) {
	attempts := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			http.Error(w, "retry", http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	reporter := NewReporter(server.URL, server.Client())
	reporter.baseBackoff = 1

	if err := reporter.Report(context.Background(), TelemetryPayload{SchemaVersion: payloadSchemaVersion}); err != nil {
		t.Fatalf("Report() error = %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestReporterRequiresHTTPS(t *testing.T) {
	reporter := NewReporter("http://example.com", &http.Client{})
	err := reporter.Report(context.Background(), TelemetryPayload{SchemaVersion: payloadSchemaVersion})
	if err == nil || err.Error() != "telemetry endpoint must use https" {
		t.Fatalf("Report() error = %v, want https validation", err)
	}
}

func TestReporterReturnsLastErrorAfterRetries(t *testing.T) {
	attempts := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		http.Error(w, "still failing", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	reporter := NewReporter(server.URL, server.Client())
	reporter.baseBackoff = 1

	err := reporter.Report(context.Background(), TelemetryPayload{SchemaVersion: payloadSchemaVersion})
	if err == nil {
		t.Fatal("expected retry failure")
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected terminal HTTP error, got %v", err)
	}
}
