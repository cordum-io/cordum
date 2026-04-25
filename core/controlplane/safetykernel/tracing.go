package safetykernel

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const envOTELEndpoint = "CORDUM_OTEL_ENDPOINT"

const tracerName = "github.com/cordum/cordum/core/controlplane/safetykernel"

var (
	tracerInitOnce sync.Once
	tracerShutdown = func(context.Context) error { return nil }
	// evalTracerProvider holds the local TracerProvider used by
	// evaluationSpan. We keep it package-local instead of calling
	// otel.SetTracerProvider so we don't clobber any global TracerProvider
	// that another component (e.g. cordumotel.InitTracer in kernel.Run)
	// has already installed. tracerEnabled still gates whether spans are
	// emitted -- the new feature is opt-in via CORDUM_OTEL_ENDPOINT only.
	evalTracerProvider trace.TracerProvider = noop.NewTracerProvider()
	tracerEnabled      bool
)

func initTracing(ctx context.Context) error {
	var initErr error
	tracerInitOnce.Do(func() {
		endpoint := strings.TrimSpace(os.Getenv(envOTELEndpoint))
		if endpoint == "" {
			return
		}

		exp, err := otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpoint(endpoint),
			otlptracegrpc.WithInsecure(),
		)
		if err != nil {
			initErr = err
			slog.Warn("safetykernel: OTLP tracer init failed", "endpoint", endpoint, "error", err)
			return
		}

		tp := sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(exp),
		)
		evalTracerProvider = tp
		tracerShutdown = tp.Shutdown
		tracerEnabled = true
		slog.Info("safetykernel: OTLP tracer enabled", "endpoint", endpoint)
	})
	return initErr
}

func shutdownTracing(ctx context.Context) error {
	return tracerShutdown(ctx)
}

// spanNameFor returns the Evaluate-prefixed span name for a known policy kind.
// Avoids strings.Title (deprecated since Go 1.18) and avoids pulling in
// golang.org/x/text/cases for what is a closed set of values.
func spanNameFor(kind string) string {
	switch kind {
	case "input":
		return "safetykernel.EvaluateInput"
	case "output":
		return "safetykernel.EvaluateOutput"
	default:
		return "safetykernel.Evaluate"
	}
}

func evaluationSpan(
	ctx context.Context,
	kind string,
	agentID string,
	jobTopic string,
	tenant string,
) (context.Context, func(decision string, ruleCount int)) {
	// Gate on the local tracerEnabled flag rather than the local
	// TracerProvider so deployments that enable a different OTEL surface
	// (e.g. OTEL_ENABLED=true) don't accidentally start emitting
	// safety-kernel evaluation spans. The new feature is opt-in via
	// CORDUM_OTEL_ENDPOINT only -- consistent with the comments in
	// EvaluateOutput / CheckOutput.
	if !tracerEnabled {
		return ctx, func(string, int) {}
	}
	tr := evalTracerProvider.Tracer(tracerName)
	start := time.Now()

	ctx, span := tr.Start(ctx, spanNameFor(kind),
		trace.WithAttributes(
			attribute.String("agent.id", agentID),
			attribute.String("job.topic", jobTopic),
			attribute.String("tenant", tenant),
			attribute.String("policy.kind", kind),
		),
	)

	finish := func(decision string, ruleCount int) {
		span.SetAttributes(
			attribute.String("policy.decision", decision),
			attribute.Int("policy.rule_count", ruleCount),
			attribute.Int64("policy.duration_ms", time.Since(start).Milliseconds()),
		)
		span.End()
	}
	return ctx, finish
}

func init() {
	_ = initTracingSilently()
}

func initTracingSilently() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := initTracing(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Warn("safetykernel: tracer init returned error", "error", err)
		return err
	}
	return nil
}
