---
sidebar_position: 1
title: Architecture
slug: /concepts/architecture
---

# Architecture

Cordum is a safety-first agent orchestration platform with 7 core services.

## Services

| Service | Port | Purpose |
|---------|------|---------|
| API Gateway | :8081 | HTTP/gRPC API, WebSocket streaming, dashboard backend |
| Scheduler | — | Job dispatch, safety checks, worker routing |
| Safety Kernel | :50051 | Policy evaluation, input/output scanning |
| Workflow Engine | — | DAG execution, step orchestration |
| Context Engine | :50070 | Conversation memory, context windows |
| MCP Server | :8082 | Model Context Protocol endpoints |
| cordumctl | CLI | Management CLI |

## Infrastructure

- **NATS** — Message bus for job submission, dispatch, and results
- **Redis** — State store for jobs, workflows, configuration, and credentials
- **gRPC** — Safety Kernel and Context Engine inter-service communication
- **Protobuf** — Wire format via Cordum Agent Protocol (CAP)

## Data Flow

```
Client → Gateway → NATS (submit) → Scheduler → Safety Kernel (check)
                                         ↓
                                   NATS (dispatch) → Worker → NATS (result)
                                         ↓
                                   Workflow Engine (if workflow job)
```
