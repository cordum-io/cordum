# MCP Integration

This document describes how Cordum integrates with the Model Context Protocol
(MCP), including the embedded server, gateway bridge, and dashboard settings.

Source code:

- `core/mcp/server.go` — JSON-RPC 2.0 MCP server implementation
- `core/mcp/resources.go` — Resource service and `DataBridge` interface
- `core/mcp/data_bridge.go` — HTTP and direct data bridge implementations
- `core/mcp/tools/` — Tool registry and built-in tool definitions
- `core/mcp/resources/` — Resource registry and built-in resource definitions
- `core/controlplane/gateway/handlers_mcp.go` — Gateway MCP bridge (SSE transport)
- `cmd/cordum-mcp/main.go` — Standalone MCP server binary
- `dashboard/src/pages/SettingsMcpPage.tsx` — MCP settings dashboard page

## 1. Overview

Cordum exposes an MCP server that allows AI agents and IDE tools to interact
with the platform via the standard Model Context Protocol. The server supports
tool execution and resource reads over a JSON-RPC 2.0 transport, with SSE
streaming through the gateway.

<!-- TODO: detailed architecture diagram showing agent → MCP client → gateway bridge → MCP server → tools/resources -->

## 2. Server Architecture

The MCP server (`core/mcp/server.go`) implements:

- **`ToolService`** — Lists available tools and executes tool calls
- **`ResourceService`** — Lists resources, lists resource templates, and reads resource contents
- **`MCPServer`** — JSON-RPC 2.0 request loop with configurable timeout (default 30s)

Server defaults:
- Name: `cordum`
- Protocol version: `DefaultProtocolVersion`
- Request timeout: 30 seconds

<!-- TODO: document supported JSON-RPC methods (initialize, tools/list, tools/call, resources/list, resources/read, etc.) -->

## 3. Gateway Bridge

The gateway embeds the MCP server and exposes it via HTTP/SSE:

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/mcp/sse` | GET | SSE event stream for MCP notifications |
| `/mcp/message` | POST | JSON-RPC message endpoint |
| `/mcp/status` | GET | MCP runtime status |

All MCP endpoints require authentication via the gateway's standard auth
middleware (`mcpAuth`).

The bridge is enabled via the config service (`cfg.Enabled`). When disabled,
routes are still registered but return explicit disabled/unavailable responses.

<!-- TODO: document MCP config keys stored in config service -->

## 4. Standalone MCP Server

The `cordum-mcp` binary (`cmd/cordum-mcp/main.go`) runs as a standalone MCP
server outside the gateway, useful for local development and direct IDE
integration.

<!-- TODO: document CLI flags, startup, and connection to gateway -->

## 5. Data Bridge

The `DataBridge` interface (`core/mcp/resources.go`) provides data access for
MCP resources. Two implementations exist:

- **`HTTPDataBridge`** — Fetches data via HTTP calls to the gateway API
- **`DirectDataBridge`** — Accesses stores directly (for in-process use)

Methods include listing jobs, workers, workflows, audit entries, and reading
job/workflow details.

<!-- TODO: document full DataBridge method set and resource URI patterns -->

## 6. Configuration

| Env Var | Description |
|---------|-------------|
| `CORDUM_MCP_GATEWAY_ALLOWLIST` | Comma-separated host/domain allowlist for outbound gateway calls |
| `CORDUM_MCP_ALLOW_PRIVATE_GATEWAY` | Allow private/loopback gateway hosts (default: `false`) |

MCP runtime is configured via the config service (stored in Redis). The
gateway reads MCP config on startup and when config is reloaded.

<!-- TODO: document config service keys for MCP enablement and transport selection -->

## 7. Dashboard Settings

The MCP settings page (`/settings/mcp`) provides a UI for managing MCP server
configuration.

<!-- TODO: document dashboard MCP settings capabilities -->

## See Also

- [mcp-server.md](mcp-server.md) — MCP server operational guide
- [mcp-tools-reference.md](mcp-tools-reference.md) — Complete tool definitions
- [mcp-resources-reference.md](mcp-resources-reference.md) — Complete resource definitions
- [configuration-reference.md](configuration-reference.md) — Full env var reference
