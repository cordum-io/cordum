package otel

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

const (
	envOTELMetricsEnabled  = "OTEL_METRICS_ENABLED"
	envOTELMetricsEndpoint = "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"
	metricsExportInterval  = 15 * time.Second
	metricsShutdownTimeout = 5 * time.Second
)

var (
	globalMeterProvider otelmetric.MeterProvider = noop.NewMeterProvider()
	metricsShutdown                              = func(context.Context) error { return nil }
	metricsInitOnce     sync.Once
)

// MetricsEnabled returns true when OTEL metrics export is enabled.
func MetricsEnabled() bool {
	v := strings.TrimSpace(os.Getenv(envOTELMetricsEnabled))
	if v == "" {
		return false
	}
	return v == "1" || strings.EqualFold(v, "true")
}

// InitMetrics initializes the global OTEL MeterProvider with OTLP gRPC export.
// When disabled (default), a noop provider is used with zero overhead.
// Existing Prometheus metrics on :9092/metrics are unaffected — OTEL metrics
// are an additive, separate pipeline.
func InitMetrics(serviceName string) error {
	if !MetricsEnabled() {
		slog.Debug("otel metrics disabled", "component", "otel")
		return nil
	}

	var initErr error
	metricsInitOnce.Do(func() {
		ctx := context.Background()

		endpoint := strings.TrimSpace(os.Getenv(envOTELMetricsEndpoint))
		if endpoint == "" {
			endpoint = strings.TrimSpace(os.Getenv(envOTELEndpoint))
		}
		if endpoint == "" {
			endpoint = "localhost:4317"
		}

		opts := []otlpmetricgrpc.Option{
			otlpmetricgrpc.WithEndpoint(endpoint),
		}
		if !strings.HasPrefix(endpoint, "https://") {
			opts = append(opts, otlpmetricgrpc.WithInsecure())
		}

		exporter, err := otlpmetricgrpc.New(ctx, opts...)
		if err != nil {
			initErr = fmt.Errorf("otel metrics exporter: %w", err)
			return
		}

		res, err := resource.New(ctx,
			resource.WithAttributes(
				semconv.ServiceNameKey.String(serviceName),
			),
		)
		if err != nil {
			initErr = fmt.Errorf("otel metrics resource: %w", err)
			return
		}

		provider := sdkmetric.NewMeterProvider(
			sdkmetric.WithResource(res),
			sdkmetric.WithReader(
				sdkmetric.NewPeriodicReader(exporter,
					sdkmetric.WithInterval(metricsExportInterval),
				),
			),
		)

		globalMeterProvider = provider
		otel.SetMeterProvider(provider)
		metricsShutdown = func(ctx context.Context) error {
			return provider.Shutdown(ctx)
		}

		slog.Info("otel metrics initialized",
			"component", "otel",
			"endpoint", endpoint,
			"service", serviceName,
			"interval", metricsExportInterval,
		)
	})
	return initErr
}

// Meter returns the global OTEL meter for creating instruments.
// Returns a noop meter when OTEL metrics are disabled.
func Meter(name string) otelmetric.Meter {
	return globalMeterProvider.Meter(name)
}

// ShutdownMetrics flushes pending metric data and releases resources.
func ShutdownMetrics() error {
	ctx, cancel := context.WithTimeout(context.Background(), metricsShutdownTimeout)
	defer cancel()
	return metricsShutdown(ctx)
}
