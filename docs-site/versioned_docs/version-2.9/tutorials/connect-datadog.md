---
title: Connect Cordum to Datadog
sidebar_position: 6
---
# Connect Cordum to Datadog

Export distributed traces and metrics from Cordum to Datadog using the OpenTelemetry Collector.

## Prerequisites

- Cordum running (Docker Compose or Kubernetes)
- Datadog account with an API key
- Datadog site URL (e.g., `datadoghq.com`, `datadoghq.eu`)

## Step 1: Enable OTEL in Cordum

Set the following environment variables on all Cordum services:

```bash
# Enable OTEL tracing and metrics
OTEL_ENABLED=true
OTEL_METRICS_ENABLED=true

# Point to the OTEL collector sidecar (or standalone collector)
OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4317

# Sampling rate (0.1 = 10% of traces, adjust for your volume)
OTEL_TRACES_SAMPLER_ARG=0.1
```

For Docker Compose, add these to your `.env` file. For Kubernetes, set them in the deployment env vars or ConfigMap.

## Step 2: Configure the OTEL Collector

Create or update the collector configuration to export to Datadog.

### Docker Compose

Add an OTEL collector service to your `docker-compose.yml`:

```yaml
  otel-collector:
    image: otel/opentelemetry-collector-contrib:0.115.0
    command: ["--config=/etc/otel/config.yaml"]
    ports:
      - "4317:4317"   # OTLP gRPC
    volumes:
      - ./config/otel-collector.yaml:/etc/otel/config.yaml:ro
    environment:
      - DD_API_KEY=${DD_API_KEY}
      - DD_SITE=${DD_SITE:-datadoghq.com}
```

Create `config/otel-collector.yaml`:

```yaml
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317

processors:
  batch:
    timeout: 5s
    send_batch_size: 1024

exporters:
  datadog:
    api:
      key: ${DD_API_KEY}
      site: ${DD_SITE}
    traces:
      span_name_as_resource_name: true
    metrics:
      resource_attributes_as_tags: true

service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [batch]
      exporters: [datadog]
    metrics:
      receivers: [otlp]
      processors: [batch]
      exporters: [datadog]
```

### Kubernetes (Kustomize)

Uncomment the OTEL resources in `deploy/k8s/production/kustomization.yaml`:

```yaml
resources:
  # ... existing resources ...
  - otel-collector-config.yaml  # uncomment this

patches:
  # ... existing patches ...
  - path: patches/otel-sidecar.yaml  # uncomment this
```

Update the collector ConfigMap to use the Datadog exporter (replace the default OTLP exporter section).

## Step 3: Set Datadog Credentials

```bash
# Docker Compose
export DD_API_KEY="your-datadog-api-key"
export DD_SITE="datadoghq.com"  # or datadoghq.eu

# Kubernetes
kubectl create secret generic datadog-api-key \
  --from-literal=api-key="your-datadog-api-key" \
  -n cordum
```

## Step 4: Restart and Verify

```bash
# Docker Compose
docker compose up -d

# Kubernetes
kubectl rollout restart deployment -n cordum
```

### Verify traces in Datadog APM

1. Open Datadog APM > Traces
2. Filter by service: `cordum-api-gateway`, `cordum-scheduler`, `cordum-safety-kernel`
3. You should see traces for:
   - HTTP requests to the API gateway
   - Safety kernel policy evaluations
   - Job dispatch and result processing

### Verify metrics in Datadog Metrics Explorer

1. Open Datadog Metrics > Explorer
2. Search for metrics with prefix `cordum_`:
   - `cordum_scheduler_jobs_received_total` — jobs received by topic
   - `cordum_scheduler_jobs_completed_total` — jobs completed by status
   - `cordum_api_gateway_http_requests_total` — API request rate
   - `cordum_api_gateway_http_request_duration_seconds` — API latency

## Expected Datadog Views

**APM Service Map**: Cordum services appear as nodes with request/error/latency metrics. The gateway connects to the safety kernel and scheduler, which connect to NATS and Redis.

**Trace Waterfall**: A single job submission shows: gateway HTTP handler > safety kernel policy check > scheduler dispatch > worker execution > result processing.

**Metrics Dashboard**: Import the pre-built Grafana dashboard (`deploy/grafana/cordum-overview.json`) into Datadog using the Prometheus integration, or build a Datadog dashboard using the same metric names.

## Troubleshooting

| Issue | Solution |
|-------|----------|
| No traces in Datadog | Check `OTEL_ENABLED=true` is set on all services. Verify collector logs: `docker compose logs otel-collector` |
| Missing metrics | Verify `OTEL_METRICS_ENABLED=true`. Check collector config has a `metrics` pipeline |
| High cardinality | Lower sampling rate: `OTEL_TRACES_SAMPLER_ARG=0.01` (1%) |
| Collector OOM | Increase memory limit or lower batch size in collector config |
| Existing Prometheus broken | OTEL metrics are additive. Prometheus on `:9092/metrics` should be unchanged. If missing, check the Prometheus endpoint independently |
