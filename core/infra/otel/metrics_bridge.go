package otel

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
)

// GatewayMetricsBridge creates OTEL instruments that mirror the gateway's
// key Prometheus metrics. When OTEL is disabled, all instruments are noop.
type GatewayMetricsBridge struct {
	requestCounter  otelmetric.Int64Counter
	latencyRecorder otelmetric.Float64Histogram
}

// NewGatewayMetricsBridge creates OTEL instruments for gateway metrics.
// Safe to call when OTEL is disabled — instruments will be noop.
func NewGatewayMetricsBridge() *GatewayMetricsBridge {
	meter := Meter("cordum-api-gateway")
	reqCounter, _ := meter.Int64Counter("cordum.api_gateway.http_requests",
		otelmetric.WithDescription("HTTP requests by method/route/status"),
		otelmetric.WithUnit("{request}"),
	)
	latencyHist, _ := meter.Float64Histogram("cordum.api_gateway.http_request_duration",
		otelmetric.WithDescription("HTTP request latency by method/route"),
		otelmetric.WithUnit("s"),
	)
	return &GatewayMetricsBridge{
		requestCounter:  reqCounter,
		latencyRecorder: latencyHist,
	}
}

// RecordRequest records an HTTP request to OTEL metrics alongside Prometheus.
func (b *GatewayMetricsBridge) RecordRequest(ctx context.Context, method, route, status string, durationSeconds float64) {
	if b == nil {
		return
	}
	if b.requestCounter != nil {
		b.requestCounter.Add(ctx, 1,
			otelmetric.WithAttributes(
				attribute.String("http.method", method),
				attribute.String("http.route", route),
				attribute.String("http.status", status),
			),
		)
	}
	if b.latencyRecorder != nil {
		b.latencyRecorder.Record(ctx, durationSeconds,
			otelmetric.WithAttributes(
				attribute.String("http.method", method),
				attribute.String("http.route", route),
			),
		)
	}
}

// SchedulerMetricsBridge creates OTEL instruments for scheduler metrics.
type SchedulerMetricsBridge struct {
	jobsReceived  otelmetric.Int64Counter
	jobsCompleted otelmetric.Int64Counter
	safetyDenied  otelmetric.Int64Counter
}

// NewSchedulerMetricsBridge creates OTEL instruments for scheduler metrics.
func NewSchedulerMetricsBridge() *SchedulerMetricsBridge {
	meter := Meter("cordum-scheduler")
	recv, _ := meter.Int64Counter("cordum.scheduler.jobs_received",
		otelmetric.WithDescription("Jobs received by topic"),
		otelmetric.WithUnit("{job}"),
	)
	comp, _ := meter.Int64Counter("cordum.scheduler.jobs_completed",
		otelmetric.WithDescription("Jobs completed by topic and status"),
		otelmetric.WithUnit("{job}"),
	)
	denied, _ := meter.Int64Counter("cordum.scheduler.safety_denied",
		otelmetric.WithDescription("Jobs denied by safety policy"),
		otelmetric.WithUnit("{job}"),
	)
	return &SchedulerMetricsBridge{
		jobsReceived:  recv,
		jobsCompleted: comp,
		safetyDenied:  denied,
	}
}

// RecordJobReceived records a job received event.
func (b *SchedulerMetricsBridge) RecordJobReceived(ctx context.Context, topic string) {
	if b == nil || b.jobsReceived == nil {
		return
	}
	b.jobsReceived.Add(ctx, 1, otelmetric.WithAttributes(attribute.String("topic", topic)))
}

// RecordJobCompleted records a job completion event.
func (b *SchedulerMetricsBridge) RecordJobCompleted(ctx context.Context, topic, status string) {
	if b == nil || b.jobsCompleted == nil {
		return
	}
	b.jobsCompleted.Add(ctx, 1, otelmetric.WithAttributes(
		attribute.String("topic", topic),
		attribute.String("status", status),
	))
}

// RecordSafetyDenied records a safety denial event.
func (b *SchedulerMetricsBridge) RecordSafetyDenied(ctx context.Context, topic string) {
	if b == nil || b.safetyDenied == nil {
		return
	}
	b.safetyDenied.Add(ctx, 1, otelmetric.WithAttributes(attribute.String("topic", topic)))
}
