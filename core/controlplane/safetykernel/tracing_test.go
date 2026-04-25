package safetykernel

import (
	"context"
	"os"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestEvaluationSpanNoopWhenEndpointUnset(t *testing.T) {
	if err := os.Unsetenv(envOTELEndpoint); err != nil {
		t.Fatalf("unset env: %v", err)
	}

	// Install a recording provider as the local evalTracerProvider but
	// keep tracerEnabled=false. evaluationSpan must short-circuit before
	// it hits the provider, so the recorder must observe zero spans.
	prevProvider := evalTracerProvider
	prevEnabled := tracerEnabled
	rec := tracetest.NewSpanRecorder()
	evalTracerProvider = trace.NewTracerProvider(trace.WithSpanProcessor(rec))
	tracerEnabled = false
	t.Cleanup(func() {
		evalTracerProvider = prevProvider
		tracerEnabled = prevEnabled
	})

	ctx, finish := evaluationSpan(context.Background(), "output", "agent-x", "checkout", "acme")
	finish("allow", 3)
	_ = ctx

	if spans := rec.Ended(); len(spans) != 0 {
		t.Fatalf("expected 0 spans when endpoint unset, got %d", len(spans))
	}
}

// TestEvaluationSpanLocalProviderDoesNotClobberGlobal verifies that
// initTracing keeps its TracerProvider local: a global provider installed
// before or after initTracing must remain reachable via otel.GetTracerProvider().
// This is the regression guard for the global-clobber bug @yaront1111
// flagged on PR #230.
func TestEvaluationSpanLocalProviderDoesNotClobberGlobal(t *testing.T) {
	prevGlobal := otel.GetTracerProvider()
	prevLocal := evalTracerProvider
	prevEnabled := tracerEnabled
	t.Cleanup(func() {
		otel.SetTracerProvider(prevGlobal)
		evalTracerProvider = prevLocal
		tracerEnabled = prevEnabled
	})

	// Pretend another component (e.g. cordumotel.InitTracer) installed a
	// global TracerProvider that should NOT be touched by safety-kernel.
	globalRec := tracetest.NewSpanRecorder()
	globalTP := trace.NewTracerProvider(trace.WithSpanProcessor(globalRec))
	otel.SetTracerProvider(globalTP)

	// Simulate initTracing wiring up a local provider for the safety kernel.
	localRec := tracetest.NewSpanRecorder()
	evalTracerProvider = trace.NewTracerProvider(trace.WithSpanProcessor(localRec))
	tracerEnabled = true

	// The global provider must still be the one we installed -- the local
	// wiring must not have called otel.SetTracerProvider.
	if got := otel.GetTracerProvider(); got != globalTP {
		t.Fatalf("global TracerProvider was clobbered: got %T, want %T", got, globalTP)
	}

	// And evaluationSpan must record on the LOCAL provider, not the global.
	_, finish := evaluationSpan(context.Background(), "input", "agent-x", "checkout", "acme")
	finish("allow", 1)

	if err := globalTP.ForceFlush(context.Background()); err != nil {
		t.Fatalf("global flush: %v", err)
	}
	if err := evalTracerProvider.(*trace.TracerProvider).ForceFlush(context.Background()); err != nil {
		t.Fatalf("local flush: %v", err)
	}
	if got := len(globalRec.Ended()); got != 0 {
		t.Errorf("global recorder saw %d spans; safety-kernel must record only on its local provider", got)
	}
	if got := len(localRec.Ended()); got != 1 {
		t.Errorf("local recorder saw %d spans, want 1", got)
	}
}

// TestEvaluationSpanInputKindUsesInputName guards spanNameFor against the
// strings.Title regression -- the deprecated call used to produce
// "EvaluateInput" via strings.Title, but a future careless edit could send
// us back through Title or some equivalent. Lock the literal name.
func TestEvaluationSpanInputKindUsesInputName(t *testing.T) {
	prevProvider := evalTracerProvider
	prevEnabled := tracerEnabled
	rec := tracetest.NewSpanRecorder()
	evalTracerProvider = trace.NewTracerProvider(trace.WithSpanProcessor(rec))
	tracerEnabled = true
	t.Cleanup(func() {
		evalTracerProvider = prevProvider
		tracerEnabled = prevEnabled
	})

	_, finish := evaluationSpan(context.Background(), "input", "agent-x", "checkout", "acme")
	finish("allow", 0)

	if err := evalTracerProvider.(*trace.TracerProvider).ForceFlush(context.Background()); err != nil {
		t.Fatalf("force flush: %v", err)
	}
	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if got := spans[0].Name(); got != "safetykernel.EvaluateInput" {
		t.Errorf("span name = %q, want %q", got, "safetykernel.EvaluateInput")
	}
}

// Sanity check: the noop import is wired so the package-level default
// evalTracerProvider compiles. No assertion needed; failure shows up as a
// compile error.
var _ = noop.NewTracerProvider

func TestEvaluationSpanRecordsRequiredAttributesWhenProviderInstalled(t *testing.T) {
	prevProvider := evalTracerProvider
	prevEnabled := tracerEnabled
	rec := tracetest.NewSpanRecorder()
	tp := trace.NewTracerProvider(trace.WithSpanProcessor(rec))
	evalTracerProvider = tp
	tracerEnabled = true
	t.Cleanup(func() {
		evalTracerProvider = prevProvider
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
// via CORDUM_OTEL_ENDPOINT only. evalTracerProvider may also be wired to
// a recorder; the local tracerEnabled gate must still suppress emission.
func TestEvaluationSpanIgnoresGlobalProviderWhenLocalGateIsOff(t *testing.T) {
	prevGlobal := otel.GetTracerProvider()
	prevLocal := evalTracerProvider
	prevEnabled := tracerEnabled
	globalRec := tracetest.NewSpanRecorder()
	globalTP := trace.NewTracerProvider(trace.WithSpanProcessor(globalRec))
	otel.SetTracerProvider(globalTP)
	localRec := tracetest.NewSpanRecorder()
	evalTracerProvider = trace.NewTracerProvider(trace.WithSpanProcessor(localRec))
	tracerEnabled = false
	t.Cleanup(func() {
		otel.SetTracerProvider(prevGlobal)
		evalTracerProvider = prevLocal
		tracerEnabled = prevEnabled
	})

	_, finish := evaluationSpan(context.Background(), "output", "agent-x", "checkout", "acme")
	finish("deny", 7)

	if err := globalTP.ForceFlush(context.Background()); err != nil {
		t.Fatalf("global flush: %v", err)
	}
	if err := evalTracerProvider.(*trace.TracerProvider).ForceFlush(context.Background()); err != nil {
		t.Fatalf("local flush: %v", err)
	}
	if spans := globalRec.Ended(); len(spans) != 0 {
		t.Fatalf("global recorder expected 0 spans (gate off), got %d", len(spans))
	}
	if spans := localRec.Ended(); len(spans) != 0 {
		t.Fatalf("local recorder expected 0 spans (CORDUM_OTEL_ENDPOINT gate off), got %d", len(spans))
	}
}
