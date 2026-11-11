package otel

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const (
	envOTELEnabled      = "OTEL_ENABLED"
	envOTELEndpoint     = "OTEL_EXPORTER_OTLP_ENDPOINT"
	envOTELServiceName  = "OTEL_SERVICE_NAME"
	envOTELSamplerArg   = "OTEL_TRACES_SAMPLER_ARG"
	defaultSamplingRate = 0.1
	shutdownTimeout     = 5 * time.Second
)

var (
	globalProvider trace.TracerProvider = noop.NewTracerProvider()
	globalShutdown                      = func(context.Context) error { return nil }
	initOnce       sync.Once
)

// Enabled returns true if OTEL tracing is enabled via environment.
func Enabled() bool {
	v := strings.TrimSpace(os.Getenv(envOTELEnabled))
	if v == "" {
		return false
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false
	}
	return b
}

// InitTracer initializes the global TracerProvider. When OTEL_ENABLED is false
// (the default), a noop provider is used with zero overhead. When enabled, an
// OTLP gRPC exporter is created with batch span processing and configurable
// sampling rate.
//
// Call Shutdown before process exit to flush pending spans.
func InitTracer(serviceName string) (trace.TracerProvider, error) {
	if !Enabled() {
		slog.Debug("otel tracing disabled", "component", "otel")
		return noop.NewTracerProvider(), nil
	}

	if sn := strings.TrimSpace(os.Getenv(envOTELServiceName)); sn != "" {
		serviceName = sn
	}
	if serviceName == "" {
		serviceName = "cordum"
	}

	endpoint := strings.TrimSpace(os.Getenv(envOTELEndpoint))
	if endpoint == "" {
		endpoint = "localhost:4317"
	}

	samplingRate := defaultSamplingRate
	if v := strings.TrimSpace(os.Getenv(envOTELSamplerArg)); v != "" {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil && parsed >= 0 && parsed <= 1 {
			samplingRate = parsed
		} else {
			slog.Warn("invalid OTEL_TRACES_SAMPLER_ARG, using default", "value", v, "default", defaultSamplingRate)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("otel: create OTLP exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(serviceName)),
		resource.WithProcessRuntimeDescription(),
		resource.WithHost(),
	)
	if err != nil {
		return nil, fmt.Errorf("otel: create resource: %w", err)
	}

	sampler := sdktrace.ParentBased(sdktrace.TraceIDRatioBased(samplingRate))

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	initOnce.Do(func() {
		globalProvider = tp
		globalShutdown = func(ctx context.Context) error {
			return tp.Shutdown(ctx)
		}
	})

	slog.Info("otel tracing enabled",
		"component", "otel",
		"service", serviceName,
		"endpoint", endpoint,
		"sampling_rate", samplingRate,
	)

	return tp, nil
}

// Shutdown flushes pending spans and shuts down the tracer provider.
func Shutdown(ctx context.Context) error {
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
	}
	return globalShutdown(ctx)
}

// Provider returns the global TracerProvider. Useful for creating tracing
// middleware with a specific provider (e.g., in tests with in-memory exporters).
func Provider() trace.TracerProvider {
	return globalProvider
}

// Tracer returns a named tracer from the global provider. When OTEL is
// disabled, this returns a noop tracer with zero allocation overhead.
func Tracer(name string) trace.Tracer {
	return globalProvider.Tracer(name)
}
