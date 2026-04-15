---
sidebar_position: 2
title: Configuration Reference
slug: /operations/configuration
---

# Configuration Reference

All Cordum services are configured via environment variables with sensible defaults.

## Gateway

| Variable | Default | Description |
|----------|---------|-------------|
| `CORDUM_HTTP_ADDR` | `:8081` | HTTP listen address |
| `CORDUM_GRPC_ADDR` | `:9090` | gRPC listen address |
| `CORDUM_METRICS_ADDR` | `:9092` | Prometheus metrics address |

## Safety Kernel

| Variable | Default | Description |
|----------|---------|-------------|
| `SAFETY_POLICY_PATH` | `config/safety.yaml` | Policy file path |
| `SAFETY_DECISION_CACHE_TTL` | — | Decision cache TTL |

## OpenTelemetry

| Variable | Default | Description |
|----------|---------|-------------|
| `OTEL_ENABLED` | `false` | Enable distributed tracing |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | OTLP gRPC endpoint |
| `OTEL_SERVICE_NAME` | service-specific | Service name in traces |
| `OTEL_TRACES_SAMPLER_ARG` | `0.1` | Sampling rate (0.0-1.0) |
