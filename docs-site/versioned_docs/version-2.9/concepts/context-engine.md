---
sidebar_position: 9
title: "Context Engine"
slug: /concepts/context-engine
---

# Context Engine

This document describes the Context Engine gRPC service, which assembles
model-ready context windows from stored conversation history and RAG chunks.

Source code:

- `core/contextwindow/engine/service.go` — gRPC service implementation (`BuildWindow`)
- `core/contextwindow/engine/client.go` — gRPC client with TLS support
- `cmd/cordum-context-engine/main.go` — Service binary entry point
- `dashboard/src/pages/ContextInspectorPage.tsx` — Context inspector dashboard page

## 1. Overview

The Context Engine is a standalone gRPC service that manages conversational
memory for agents. It stores interaction history in Redis and assembles
context windows on demand, supporting raw passthrough, chat history, and
RAG-augmented modes.

<!-- TODO: detailed architecture diagram showing agent → scheduler → context engine → Redis -->

## 2. Context Modes

The `BuildWindow` RPC accepts a mode that controls how context is assembled:

| Mode | Behavior |
|------|----------|
| `CONTEXT_MODE_RAW` | Passes the logical payload through without history |
| `CONTEXT_MODE_CHAT` | Prepends recent conversation history from the memory store |
| `CONTEXT_MODE_RAG` | Prepends conversation history and attaches matching RAG chunks |

Default mode when unspecified: `CONTEXT_MODE_RAW`.

## 3. BuildWindow API

The primary RPC method:

```
rpc BuildWindow(BuildWindowRequest) returns (BuildWindowResponse)
```

Request fields:
- `memory_id` — Identifies the conversation/memory to retrieve history for
- `mode` — Context assembly mode (RAW, CHAT, RAG)
- `logical_payload` — The current request payload to build context around

The service retrieves up to 20 recent history events from Redis (configurable),
extracts the user message, and assembles a sequence of `ModelMessage` entries
(role + content) suitable for LLM consumption.

<!-- TODO: document full protobuf message definitions from proto/cordum/agent/v1/ -->

## 4. Memory Storage

History events are stored in Redis lists keyed by memory ID. Each event
records a role, content, and timestamp.

Defaults:
- Max history entries per window: 20
- Max entry size: 64 KB (`CONTEXT_ENGINE_MAX_ENTRY_BYTES`)
- Max chunk scan depth: 1000 (`CONTEXT_ENGINE_MAX_CHUNK_SCAN`)
- Redis operation timeout: 2 seconds

<!-- TODO: document Redis key format, TTL behavior, and cleanup -->

## 5. Configuration

### Service

| Env Var | Default | Description |
|---------|---------|-------------|
| `CONTEXT_ENGINE_ADDR` | `:50070` | gRPC listen address |
| `CONTEXT_ENGINE_METRICS_ADDR` | — | Metrics endpoint address |
| `CONTEXT_ENGINE_METRICS_PUBLIC` | — | Set to `1` for non-loopback metrics in production |
| `CONTEXT_ENGINE_MAX_ENTRY_BYTES` | `65536` (64 KB) | Max size of a single history entry |
| `CONTEXT_ENGINE_MAX_CHUNK_SCAN` | `1000` | Max RAG chunks to scan per request |

### TLS (Server)

| Env Var | Description |
|---------|-------------|
| `CONTEXT_ENGINE_TLS_CERT` | Server TLS certificate path |
| `CONTEXT_ENGINE_TLS_KEY` | Server TLS private key path |

Both must be set together. Certificates are hot-reloaded every 30 seconds
(see [production.md — TLS Certificate Hot-Reload](/operations/production#tls-certificate-hot-reload)).

### TLS (Client)

| Env Var | Description |
|---------|-------------|
| `CONTEXT_ENGINE_TLS_CA` | CA certificate for verifying the context engine server |
| `CONTEXT_ENGINE_TLS_REQUIRED` | Require TLS for context engine connections |
| `CONTEXT_ENGINE_INSECURE` | Allow insecure connections (non-production only) |

In production mode (`CORDUM_ENV=production`), TLS is required unless
`CONTEXT_ENGINE_INSECURE` is explicitly set.

## 6. Dashboard — Context Inspector

The Context Inspector page (`/context-inspector` or linked from job details)
provides a visual interface for examining context windows built for specific
jobs and memory IDs.

<!-- TODO: document inspector UI capabilities and usage -->

## See Also

- [grpc-services.md](/api-reference/grpc-services) — gRPC service overview
- [production.md](/operations/production) — TLS hot-reload and production hardening
- [configuration-reference.md](/operations/config-reference) — Full env var reference
