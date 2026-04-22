# Cordum MCP Server

Cordum exposes MCP using JSON-RPC 2.0 so Claude clients can discover tools and
resources through a standard protocol.

Current implementation status:

- Transport and protocol handlers are implemented.
- Tool/resource registries are implemented and config-toggle aware.
- Gateway MCP auth + tenant checks are implemented.
- Built-in tool/resource handlers are registered by default in both stdio and
  gateway HTTP modes (`core/mcp/tools/register.go`,
  `core/mcp/resources/register.go`).

## Overview

Cordum MCP supports these JSON-RPC methods:

- `initialize`
- `ping`
- `tools/list`
- `tools/call`
- `resources/list`
- `resources/templates/list`
- `resources/read`

Primary code paths:

- `cmd/cordum-mcp/main.go`
- `core/mcp/server.go`
- `core/mcp/transport_stdio.go`
- `core/mcp/transport_http.go`
- `core/controlplane/gateway/gateway_mcp.go`

## Transport Modes

### 1) `stdio` (local client integration)

Run:

```bash
go run ./cmd/cordum-mcp --addr http://localhost:8081 --api-key "$CORDUM_API_KEY"
```

Flags:

- `--addr` (default `http://localhost:8081`)
- `--api-key` (or `CORDUM_API_KEY`)
- `--request-timeout` (default `30s`)

### 2) Gateway HTTP + SSE (remote clients)

Enable MCP in config:

- `mcp.enabled=true`
- `mcp.transport=http`
- `mcp.port=<optional metadata>`

Routes:

- `GET /mcp/sse`
- `POST /mcp/message`
- `GET /mcp/status`

`/mcp/sse` returns `X-MCP-Session-ID`. Clients can send `X-MCP-Session-ID` on
`/mcp/message` to correlate session responses.

## Available Tools

Current runtime behavior:

- `tools/list` returns the built-in registered tool set (subject to config
  toggles).
- Tool IDs implemented in `core/mcp/tools.go`:
  - `cordum_submit_job`
  - `cordum_cancel_job`
  - `cordum_trigger_workflow`
  - `cordum_approve_job`
  - `cordum_reject_job`
  - `cordum_query_policy`

### `cordum_submit_job`

JSON schema:

```json
{
  "type": "object",
  "required": ["prompt"],
  "properties": {
    "topic": { "type": "string" },
    "prompt": { "type": "string" },
    "priority": { "type": "string", "enum": ["low", "normal", "high", "critical"] },
    "capability": { "type": "string" },
    "risk_tags": { "type": "array", "items": { "type": "string" } },
    "labels": { "type": "object", "additionalProperties": { "type": "string" } },
    "memory_id": { "type": "string" },
    "pack_id": { "type": "string" }
  }
}
```

Request example:

```json
{
  "jsonrpc": "2.0",
  "id": 11,
  "method": "tools/call",
  "params": {
    "name": "cordum_submit_job",
    "arguments": {
      "topic": "job.default",
      "prompt": "Summarize release notes",
      "priority": "normal",
      "risk_tags": ["external_api"]
    }
  }
}
```

Response example:

```json
{
  "jsonrpc": "2.0",
  "id": 11,
  "result": {
    "content": [{ "type": "text", "text": "job submitted" }],
    "structuredContent": {
      "job_id": "2f6f4a22-8f3f-4f59-a78a-8c5fe2f14ce1",
      "trace_id": "6e0f1c62-12fd-45c8-95fb-f4cbaf955312",
      "status": "pending"
    }
  }
}
```

### `cordum_trigger_workflow`

JSON schema:

```json
{
  "type": "object",
  "required": ["workflow_id"],
  "properties": {
    "workflow_id": { "type": "string" },
    "input": { "type": "object", "additionalProperties": true },
    "dry_run": { "type": "boolean" },
    "idempotency_key": { "type": "string" }
  }
}
```

Request example:

```json
{
  "jsonrpc": "2.0",
  "id": 12,
  "method": "tools/call",
  "params": {
    "name": "cordum_trigger_workflow",
    "arguments": {
      "workflow_id": "wf-demo",
      "input": { "message": "run guardrails check" },
      "dry_run": false
    }
  }
}
```

Response example:

```json
{
  "jsonrpc": "2.0",
  "id": 12,
  "result": {
    "content": [{ "type": "text", "text": "workflow triggered" }],
    "structuredContent": {
      "run_id": "7e96f7dd-d6e8-4f1f-b7f5-84ad1bbd7aa3",
      "workflow_id": "wf-demo",
      "status": "pending"
    }
  }
}
```

### `cordum_approve_job`

JSON schema:

```json
{
  "type": "object",
  "required": ["job_id"],
  "properties": {
    "job_id": { "type": "string" },
    "note": { "type": "string" }
  }
}
```

Request example:

```json
{
  "jsonrpc": "2.0",
  "id": 13,
  "method": "tools/call",
  "params": {
    "name": "cordum_approve_job",
    "arguments": {
      "job_id": "cebdd333-3e00-4fda-a7d9-1ac1d395ca81:write@1",
      "note": "Reviewed and accepted."
    }
  }
}
```

Response example:

```json
{
  "jsonrpc": "2.0",
  "id": 13,
  "result": {
    "content": [{ "type": "text", "text": "job approved" }],
    "structuredContent": {
      "approved": true,
      "job_id": "cebdd333-3e00-4fda-a7d9-1ac1d395ca81:write@1"
    }
  }
}
```

### `cordum_reject_job`

JSON schema:

```json
{
  "type": "object",
  "required": ["job_id", "reason"],
  "properties": {
    "job_id": { "type": "string" },
    "reason": { "type": "string" }
  }
}
```

Request example:

```json
{
  "jsonrpc": "2.0",
  "id": 14,
  "method": "tools/call",
  "params": {
    "name": "cordum_reject_job",
    "arguments": {
      "job_id": "cebdd333-3e00-4fda-a7d9-1ac1d395ca81:write@1",
      "reason": "Policy exception not approved."
    }
  }
}
```

Response example:

```json
{
  "jsonrpc": "2.0",
  "id": 14,
  "result": {
    "content": [{ "type": "text", "text": "job rejected" }],
    "structuredContent": {
      "rejected": true,
      "job_id": "cebdd333-3e00-4fda-a7d9-1ac1d395ca81:write@1"
    }
  }
}
```

### `cordum_query_policy`

JSON schema:

```json
{
  "type": "object",
  "required": ["topic"],
  "properties": {
    "topic": { "type": "string" },
    "priority": { "type": "string", "enum": ["low", "normal", "high", "critical"] },
    "capability": { "type": "string" },
    "risk_tags": { "type": "array", "items": { "type": "string" } },
    "labels": { "type": "object", "additionalProperties": { "type": "string" } }
  }
}
```

Request example:

```json
{
  "jsonrpc": "2.0",
  "id": 15,
  "method": "tools/call",
  "params": {
    "name": "cordum_query_policy",
    "arguments": {
      "topic": "job.bank-executors.process",
      "priority": "normal",
      "capability": "shell.exec",
      "risk_tags": ["dangerous"]
    }
  }
}
```

Response example:

```json
{
  "jsonrpc": "2.0",
  "id": 15,
  "result": {
    "content": [{ "type": "text", "text": "policy simulated" }],
    "structuredContent": {
      "decision": "require_approval",
      "reason": "Financial execution actions require explicit human authorization.",
      "rule_id": "policy-finance-approval",
      "constraints": {},
      "remediations": []
    }
  }
}
```

### `cordum_cancel_job`

JSON schema:

```json
{
  "type": "object",
  "required": ["job_id"],
  "properties": {
    "job_id": { "type": "string" },
    "reason": { "type": "string" }
  }
}
```

Request example:

```json
{
  "jsonrpc": "2.0",
  "id": 16,
  "method": "tools/call",
  "params": {
    "name": "cordum_cancel_job",
    "arguments": {
      "job_id": "2f6f4a22-8f3f-4f59-a78a-8c5fe2f14ce1",
      "reason": "Operator cancelled from MCP client"
    }
  }
}
```

Response example:

```json
{
  "jsonrpc": "2.0",
  "id": 16,
  "result": {
    "content": [{ "type": "text", "text": "job cancelled" }],
    "structuredContent": {
      "cancelled": true,
      "job_id": "2f6f4a22-8f3f-4f59-a78a-8c5fe2f14ce1"
    }
  }
}
```

## Available Resources

Current runtime behavior:

- `resources/list` and `resources/templates/list` return built-in registered
  resources (subject to config toggles).

Registered resource catalog (`core/mcp/resources.go`):

- `cordum://jobs/{id}`
- `cordum://jobs?status={status}&limit={n}`
- `cordum://workflows/{id}/runs`
- `cordum://workflows/{id}/runs/{runId}`
- `cordum://audit?limit={n}`
- `cordum://health`
- `cordum://policies`

### `cordum://jobs/{id}`

Read request example:

```json
{
  "jsonrpc": "2.0",
  "id": 21,
  "method": "resources/read",
  "params": {
    "uri": "cordum://jobs/2f6f4a22-8f3f-4f59-a78a-8c5fe2f14ce1"
  }
}
```

Response example:

```json
{
  "jsonrpc": "2.0",
  "id": 21,
  "result": {
    "contents": [
      {
        "uri": "cordum://jobs/2f6f4a22-8f3f-4f59-a78a-8c5fe2f14ce1",
        "mimeType": "application/json",
        "text": "{\"id\":\"2f6f4a22-8f3f-4f59-a78a-8c5fe2f14ce1\",\"state\":\"SUCCEEDED\",\"topic\":\"job.default\",\"result_ptr\":\"redis://res:2f6f4a22-8f3f-4f59-a78a-8c5fe2f14ce1\"}"
      }
    ]
  }
}
```

### `cordum://jobs?status={status}&limit={n}`

Read request example:

```json
{
  "jsonrpc": "2.0",
  "id": 22,
  "method": "resources/read",
  "params": {
    "uri": "cordum://jobs?status=running&limit=25"
  }
}
```

Response example:

```json
{
  "jsonrpc": "2.0",
  "id": 22,
  "result": {
    "contents": [
      {
        "uri": "cordum://jobs?status=running&limit=25",
        "mimeType": "application/json",
        "text": "{\"items\":[{\"id\":\"job-1\",\"state\":\"RUNNING\"}],\"next_cursor\":1739433600000000}"
      }
    ]
  }
}
```

### `cordum://workflows/{id}/runs`

Read request example:

```json
{
  "jsonrpc": "2.0",
  "id": 23,
  "method": "resources/read",
  "params": {
    "uri": "cordum://workflows/wf-demo/runs"
  }
}
```

Response example:

```json
{
  "jsonrpc": "2.0",
  "id": 23,
  "result": {
    "contents": [
      {
        "uri": "cordum://workflows/wf-demo/runs",
        "mimeType": "application/json",
        "text": "{\"workflow_id\":\"wf-demo\",\"items\":[{\"id\":\"run-1\",\"status\":\"running\",\"started_at\":\"2026-02-13T15:00:00Z\"}]}"
      }
    ]
  }
}
```

### `cordum://workflows/{id}/runs/{runId}`

Read request example:

```json
{
  "jsonrpc": "2.0",
  "id": 24,
  "method": "resources/read",
  "params": {
    "uri": "cordum://workflows/wf-demo/runs/run-1"
  }
}
```

Response example:

```json
{
  "jsonrpc": "2.0",
  "id": 24,
  "result": {
    "contents": [
      {
        "uri": "cordum://workflows/wf-demo/runs/run-1",
        "mimeType": "application/json",
        "text": "{\"id\":\"run-1\",\"workflow_id\":\"wf-demo\",\"status\":\"running\",\"steps\":[{\"id\":\"step-1\",\"status\":\"succeeded\"}]}"
      }
    ]
  }
}
```

### `cordum://audit?limit={n}`

Read request example:

```json
{
  "jsonrpc": "2.0",
  "id": 25,
  "method": "resources/read",
  "params": {
    "uri": "cordum://audit?limit=50"
  }
}
```

Response example:

```json
{
  "jsonrpc": "2.0",
  "id": 25,
  "result": {
    "contents": [
      {
        "uri": "cordum://audit?limit=50",
        "mimeType": "application/json",
        "text": "{\"items\":[{\"timestamp\":\"2026-02-13T15:01:00Z\",\"actor\":\"admin\",\"action\":\"approve\",\"target\":\"job-1\",\"decision\":\"allow\"}]}"
      }
    ]
  }
}
```

### `cordum://health`

Read request example:

```json
{
  "jsonrpc": "2.0",
  "id": 26,
  "method": "resources/read",
  "params": {
    "uri": "cordum://health"
  }
}
```

Response example:

```json
{
  "jsonrpc": "2.0",
  "id": 26,
  "result": {
    "contents": [
      {
        "uri": "cordum://health",
        "mimeType": "application/json",
        "text": "{\"worker_count\":8,\"connected_pools\":3,\"redis_status\":\"ok\",\"nats_status\":\"ok\",\"uptime\":7200}"
      }
    ]
  }
}
```

### `cordum://policies`

Read request example:

```json
{
  "jsonrpc": "2.0",
  "id": 27,
  "method": "resources/read",
  "params": {
    "uri": "cordum://policies"
  }
}
```

Response example:

```json
{
  "jsonrpc": "2.0",
  "id": 27,
  "result": {
    "contents": [
      {
        "uri": "cordum://policies",
        "mimeType": "application/json",
        "text": "{\"active_bundles\":[{\"id\":\"baseline\",\"enabled\":true,\"rule_count\":8}],\"current_snapshot_id\":\"v2026-02-13\",\"safety_stance\":\"balanced\"}"
      }
    ]
  }
}
```

## Authentication and Tenant Isolation

### `stdio` mode

- Provide gateway credentials via `--api-key` or `CORDUM_API_KEY`.
- The binary forwards calls through gateway-backed clients.

### HTTP/SSE mode

- Gateway MCP auth wrapper applies `AuthenticateHTTP`.
- Use:
  - `X-API-Key: <key>`
  - `X-Tenant-ID: <tenant>`
- Cross-tenant requests are denied unless key context explicitly allows
  cross-tenant access.

Quick status check:

```bash
curl -sS http://localhost:8081/mcp/status \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: default"
```

## Integration Examples

### Claude Desktop (`claude_desktop_config.json`)

```json
{
  "mcpServers": {
    "cordum": {
      "command": "cordum-mcp",
      "args": ["--addr", "http://localhost:8081"],
      "env": {
        "CORDUM_API_KEY": "replace-with-api-key"
      }
    }
  }
}
```

### Claude Code CLI

```bash
claude mcp add cordum -- cordum-mcp --addr http://localhost:8081
```

### HTTP/SSE test call

```bash
curl -sS -X POST http://localhost:8081/mcp/message \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: default" \
  -d '{"jsonrpc":"2.0","id":1,"method":"ping"}'
```

### TypeScript custom client (HTTP mode)

```ts
const res = await fetch("http://localhost:8081/mcp/message", {
  method: "POST",
  headers: {
    "Content-Type": "application/json",
    "X-API-Key": process.env.CORDUM_API_KEY ?? "",
    "X-Tenant-ID": "default"
  },
  body: JSON.stringify({
    jsonrpc: "2.0",
    id: 1,
    method: "initialize",
    params: { protocolVersion: "2024-11-05" }
  })
});
const payload = await res.json();
console.log(payload);
```

## Dashboard Configuration

The dashboard provides a full MCP management UI at **Settings > MCP Server** (`/settings/mcp`).

### Setup Flow

1. **Enable MCP**: Navigate to `/settings/mcp` and toggle **Enable MCP Server** on.
2. **Configure transport**: Expand the Transport Configuration card and choose a mode:
   - **HTTP + SSE** (recommended) — for remote Claude Desktop / Claude Code connections
   - **stdio** — for local process integration
   - **Both** — enable both simultaneously
   Set the HTTP port (default 3001) and allowed CORS origins as needed, then click **Save Transport Settings**.
3. **Set up authentication**: In the Authentication card, ensure **Require API Key** is on, then click **Generate MCP API Key**. Copy the generated secret immediately — it is only shown once.
4. **Copy config snippet**: The Quick Start card provides ready-to-paste snippets:
   - **Claude Desktop**: JSON for `claude_desktop_config.json`
   - **Claude Code**: CLI command `claude mcp add cordum -- cordum-mcp --addr http://localhost:3001`
5. **Verify connection**: The status indicator in the page header turns green when the MCP server is running. It shows the connected client count and uptime.
6. **Manage tools and resources**: The Tools and Resources tables let you enable or disable individual MCP tools (e.g., `cordum_submit_job`) and resources (e.g., `cordum://jobs/{id}`) with per-item toggles. Expand any tool row to preview its input schema.

### API-based Configuration

You can also configure MCP through the config API:

- `GET /api/v1/config` — read current MCP configuration
- `POST /api/v1/config` — set `mcp.enabled`, `mcp.transport`, `mcp.port`, and per-item toggles (`mcp.tools.<name>.enabled`, `mcp.resources.<name>.enabled`)
- `GET /mcp/status` — runtime status (running, connected clients, uptime)

See [Dashboard Guide](dashboard-guide.md#how-to-configure-mcp-server) for the full walkthrough.

## Troubleshooting

- `401 unauthorized` on `/mcp/*`: missing/invalid API key.
- `403 tenant access denied`: tenant header mismatch with auth context.
- `404` on `/mcp/*`: MCP routes disabled (`mcp.enabled=false`) or non-HTTP
  transport.
- `tools/list` or `resources/list` empty: entries are disabled by config
  (`mcp.tools.<tool_id>.enabled=false`,
  `mcp.resources.<resource_name>.enabled=false`).

## Cross References

- [API Reference](./api-reference.md)
- [Configuration](./configuration.md)
- [System Overview](./system_overview.md)
