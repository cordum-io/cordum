package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestTracingMiddleware_NoSpoofableAgentID(t *testing.T) {
	// Set up an in-memory span exporter to inspect recorded spans.
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
	)
	defer func() { _ = tp.Shutdown(t.Context()) }()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := tracingMiddlewareWithProvider(tp, inner)

	// Request with a spoofed X-Agent-ID header.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	req.Header.Set("X-Agent-ID", "spoofed-agent")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}

	// Verify the span does NOT contain the spoofed agent_id.
	for _, span := range spans {
		for _, attr := range span.Attributes {
			if attr.Key == "cordum.agent_id" && attr.Value.AsString() == "spoofed-agent" {
				t.Fatal("span should NOT contain spoofed X-Agent-ID header value")
			}
		}
	}
}

func TestTracingMiddleware_SpanAttributes(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
	)
	defer func() { _ = tp.Shutdown(t.Context()) }()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := tracingMiddlewareWithProvider(tp, inner)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}

	span := spans[0]
	hasMethod := false
	hasURL := false
	hasStatus := false
	for _, attr := range span.Attributes {
		switch attr.Key {
		case attribute.Key("http.method"):
			hasMethod = true
			if attr.Value.AsString() != "POST" {
				t.Errorf("http.method = %q, want POST", attr.Value.AsString())
			}
		case attribute.Key("http.url"):
			hasURL = true
		case attribute.Key("http.status_code"):
			hasStatus = true
		}
	}
	if !hasMethod {
		t.Error("span missing http.method attribute")
	}
	if !hasURL {
		t.Error("span missing http.url attribute")
	}
	if !hasStatus {
		t.Error("span missing http.status_code attribute")
	}
}
