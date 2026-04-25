package safetykernel

import (
	"context"
	"os"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestEvaluationSpanNoopWhenEndpointUnset(t *testing.T) {
	if err := os.Unsetenv(envOTELEndpoint); err != nil {
		t.Fatalf("unset env: %v", err)
	}

	ctx, finish := evaluationSpan(context.Background(), "output", "agent-x", "checkout", "acme")
	finish("allow", 3)
	_ = ctx
}

func TestEvaluationSpanRecordsRequiredAttributesWhenProviderInstalled(t *testing.T) {
	prev := otel.GetTracerProvider()
	rec := tracetest.NewSpanRecorder()
	tp := trace.NewTracerProvider(trace.WithSpanProcessor(rec))
	otel.SetTracerProvider(tp)
	prevEnabled := tracerEnabled
	tracerEnabled = true
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		tracerEnabled = prevEnabled
	})

	_, finish := evaluationSpan(context.Background(), "output", "agent-x", "checkout", "acme")
	finish("deny", 7)

	if err := tp.ForceFlush(context.Background()); err != nil {
		t.Fatalf("force flush: %v", err)
	}

	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	got := map[string]string{}
	for _, kv := range spans[0].Attributes() {
		got[string(kv.Key)] = kv.Value.Emit()
	}
	for _, want := range []string{"agent.id", "job.topic", "tenant", "policy.kind", "policy.decision", "policy.rule_count", "policy.duration_ms"} {
		if _, ok := got[want]; !ok {
			t.Errorf("span missing required attribute: %s", want)
		}
	}
	if got["policy.decision"] != "deny" {
		t.Errorf("policy.decision = %q, want %q", got["policy.decision"], "deny")
	}
	if got["agent.id"] != "agent-x" {
		t.Errorf("agent.id = %q, want %q", got["agent.id"], "agent-x")
	}
}

// TestEvaluationSpanIgnoresGlobalProviderWhenLocalGateIsOff verifies that
// turning on a global TracerProvider via the existing OTEL_ENABLED path
// does NOT activate safety-kernel evaluation spans -- those stay opt-in
// via CORDUM_OTEL_ENDPOINT only.
func TestEvaluationSpanIgnoresGlobalProviderWhenLocalGateIsOff(t *testing.T) {
	prev := otel.GetTracerProvider()
	rec := tracetest.NewSpanRecorder()
	tp := trace.NewTracerProvider(trace.WithSpanProcessor(rec))
	otel.SetTracerProvider(tp)
	prevEnabled := tracerEnabled
	tracerEnabled = false
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		tracerEnabled = prevEnabled
	})

	_, finish := evaluationSpan(context.Background(), "output", "agent-x", "checkout", "acme")
	finish("deny", 7)

	if err := tp.ForceFlush(context.Background()); err != nil {
		t.Fatalf("force flush: %v", err)
	}
	if spans := rec.Ended(); len(spans) != 0 {
		t.Fatalf("expected 0 spans (CORDUM_OTEL_ENDPOINT gate off), got %d", len(spans))
	}
}
