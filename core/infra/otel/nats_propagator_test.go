package otel

import (
	"context"
	"testing"

	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestInjectExtractRoundTrip(t *testing.T) {
	// Set up a real tracer provider so we get valid span contexts.
	tp := sdktrace.NewTracerProvider()
	defer func() { _ = tp.Shutdown(context.Background()) }()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	tracer := tp.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test-op")
	defer span.End()

	originalSC := span.SpanContext()
	if !originalSC.IsValid() {
		t.Fatal("expected valid span context")
	}

	// Inject into NATS headers.
	var headers nats.Header
	InjectTraceContext(ctx, &headers)

	if headers.Get("traceparent") == "" {
		t.Fatal("expected traceparent header after inject")
	}

	// Extract from NATS headers into a fresh context.
	extractedCtx := ExtractTraceContext(context.Background(), headers)
	extractedSC := trace.SpanContextFromContext(extractedCtx)

	if !extractedSC.IsValid() {
		t.Fatal("expected valid span context after extract")
	}
	if extractedSC.TraceID() != originalSC.TraceID() {
		t.Fatalf("trace ID mismatch: got %s, want %s", extractedSC.TraceID(), originalSC.TraceID())
	}
	if extractedSC.SpanID() != originalSC.SpanID() {
		t.Fatalf("span ID mismatch: got %s, want %s", extractedSC.SpanID(), originalSC.SpanID())
	}
}

func TestExtractEmptyHeaders(t *testing.T) {
	ctx := context.Background()

	// Nil headers — should return original context.
	result := ExtractTraceContext(ctx, nil)
	sc := trace.SpanContextFromContext(result)
	if sc.IsValid() {
		t.Fatal("expected invalid span context from nil headers")
	}

	// Empty headers — should return original context.
	result = ExtractTraceContext(ctx, nats.Header{})
	sc = trace.SpanContextFromContext(result)
	if sc.IsValid() {
		t.Fatal("expected invalid span context from empty headers")
	}
}

func TestInjectNilHeaders(t *testing.T) {
	// Should not panic.
	InjectTraceContext(context.Background(), nil)
}

func TestNatsHeaderCarrierKeys(t *testing.T) {
	h := nats.Header{}
	h.Set("traceparent", "00-abc-def-01")
	h.Set("tracestate", "vendor=value")

	carrier := natsHeaderCarrier{headers: h}
	keys := carrier.Keys()
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}
}
