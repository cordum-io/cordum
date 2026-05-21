package main

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Current Cursor MCP config schema. Fetched 2026-05-16 from
// https://cursor.com/docs/context/mcp. Top-level `mcpServers` plus
// per-entry `type` (stdio / http / sse) selects the transport. Same
// shape as Claude Code's `.mcp.json`, so the merge logic reuses
// `marshalJSONStable` rather than duplicating a JSON writer.
const (
	cursorSchemaURL  = "https://cursor.com/docs/context/mcp"
	cursorSchemaDate = "2026-05-16"
)

// cursorAdapter implements AttachAdapter for Cursor's mcp.json. Same
// JSON-merge primitive as claudeCodeAdapter; only the default path
// and the stdio entry shape differ (Cursor requires `type: "stdio"`
// while Claude Code accepts a bare command/args without `type`).
type cursorAdapter struct {
	configPath string
}

func newCursorAdapter(configPath string) *cursorAdapter {
	return &cursorAdapter{configPath: configPath}
}

func (a *cursorAdapter) ClientName() string { return "cursor" }
func (a *cursorAdapter) ConfigPath() string { return a.configPath }

func (a *cursorAdapter) ReadAndMerge(existing []byte, gateway UpstreamServerRef) ([]byte, string, error) {
	root := map[string]any{}
	if len(bytes.TrimSpace(existing)) > 0 {
		if err := json.Unmarshal(existing, &root); err != nil {
			return nil, "", fmt.Errorf("cursor: invalid JSON: %w", err)
		}
	}
	servers, _ := root["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	gatewayName := gateway.Name
	if gatewayName == "" {
		gatewayName = "cordum-gateway"
	}
	entry := cursorEntryFor(gateway)
	priorCount := len(servers)
	_, replacing := servers[gatewayName]
	servers[gatewayName] = entry
	root["mcpServers"] = servers

	merged, err := marshalJSONStable(root)
	if err != nil {
		return nil, "", fmt.Errorf("cursor: serialize: %w", err)
	}
	preview := renderMergeSummary("cursor", a.configPath, priorCount, gatewayName, replacing, servers)
	return merged, preview, nil
}

// cursorEntryFor mirrors claudeCodeEntryFor but always stamps the
// `type` field even on stdio (Cursor docs document `type: "stdio"` as
// explicit). HTTP/SSE pass `url` + `headers`; the gateway flow does
// not currently set headers via attach so the entry stays minimal.
func cursorEntryFor(gateway UpstreamServerRef) map[string]any {
	entry := map[string]any{}
	transport := gateway.Transport
	if transport == "" {
		transport = "http"
	}
	switch transport {
	case "http", "sse":
		entry["url"] = gateway.Endpoint
	case "stdio":
		entry["type"] = "stdio"
		if len(gateway.Command) > 0 {
			entry["command"] = gateway.Command[0]
			if len(gateway.Command) > 1 {
				entry["args"] = gateway.Command[1:]
			}
		}
	default:
		entry["url"] = gateway.Endpoint
	}
	if gateway.AuthSecretRef != "" {
		entry["env"] = map[string]any{"CORDUM_AUTH_SECRET_REF": gateway.AuthSecretRef}
	}
	return entry
}
