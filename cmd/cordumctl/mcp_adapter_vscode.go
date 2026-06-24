package main

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// VS Code Copilot agent-mode MCP config schema (verified 2026-06-23 from
// https://code.visualstudio.com/docs/copilot/chat/mcp-servers). Unlike
// Cursor/Claude (top-level `mcpServers`), VS Code uses top-level `servers`
// plus a sibling `inputs` array for prompted secrets referenced as
// `${input:<id>}`. HTTP servers use `type: "http"` with `url` + a `headers`
// map; the API key flows through `inputs` so it is never written to disk.
const (
	vscodeSchemaURL  = "https://code.visualstudio.com/docs/copilot/chat/mcp-servers"
	vscodeSchemaDate = "2026-06-23"

	// vscodeAPIKeyInputID is the `inputs` entry id the headers reference. VS
	// Code prompts for it once and stores it in the OS secret store.
	vscodeAPIKeyInputID = "cordum-api-key" // #nosec G101 -- VS Code inputs[] id (a label), not a credential
)

// vscodeAdapter implements AttachAdapter for VS Code Copilot's mcp.json. It
// emits the Cordum auth headers (X-API-Key via a prompted input, plus literal
// X-Tenant-ID / X-Agent-Id) so Copilot agent-mode tool calls are authenticated
// and policy-gated by Cordum's MCP server.
type vscodeAdapter struct {
	configPath string
}

func newVSCodeAdapter(configPath string) *vscodeAdapter { return &vscodeAdapter{configPath: configPath} }

func (a *vscodeAdapter) ClientName() string { return "vscode" }
func (a *vscodeAdapter) ConfigPath() string { return a.configPath }

func (a *vscodeAdapter) ReadAndMerge(existing []byte, gateway UpstreamServerRef) ([]byte, string, error) {
	root := map[string]any{}
	if len(bytes.TrimSpace(existing)) > 0 {
		if err := json.Unmarshal(existing, &root); err != nil {
			return nil, "", fmt.Errorf("vscode: invalid JSON: %w", err)
		}
	}
	servers, _ := root["servers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	name := gateway.Name
	if name == "" {
		name = "cordum"
	}
	priorCount := len(servers)
	_, replacing := servers[name]
	servers[name] = vscodeServerEntry(gateway)
	root["servers"] = servers
	root["inputs"] = mergeVSCodeInputs(root["inputs"])

	merged, err := marshalJSONStable(root)
	if err != nil {
		return nil, "", fmt.Errorf("vscode: serialize: %w", err)
	}
	preview := renderMergeSummary("vscode", a.configPath, priorCount, name, replacing, servers)
	return merged, preview, nil
}

// vscodeServerEntry builds the per-server block. stdio is supported as a
// fallback; the recommended path is HTTP so the call exercises Cordum's
// identity + policy gate exactly as in production.
func vscodeServerEntry(gateway UpstreamServerRef) map[string]any {
	transport := gateway.Transport
	if transport == "" {
		transport = "http"
	}
	switch transport {
	case "stdio":
		entry := map[string]any{"type": "stdio"}
		if len(gateway.Command) > 0 {
			entry["command"] = gateway.Command[0]
			if len(gateway.Command) > 1 {
				entry["args"] = gateway.Command[1:]
			}
		}
		return entry
	default: // http / sse
		t := transport
		if t == "sse" {
			t = "sse"
		} else {
			t = "http"
		}
		headers := map[string]any{
			"X-API-Key": "${input:" + vscodeAPIKeyInputID + "}",
		}
		if gateway.Tenant != "" {
			headers["X-Tenant-ID"] = gateway.Tenant
		}
		if gateway.AgentID != "" {
			headers["X-Agent-Id"] = gateway.AgentID
		}
		return map[string]any{
			"type":    t,
			"url":     gateway.Endpoint,
			"headers": headers,
		}
	}
}

// mergeVSCodeInputs ensures the cordum-api-key prompted input exists exactly
// once, preserving any other inputs the user already configured.
func mergeVSCodeInputs(existing any) []any {
	out := []any{}
	have := false
	if arr, ok := existing.([]any); ok {
		for _, it := range arr {
			if m, ok := it.(map[string]any); ok {
				if id, _ := m["id"].(string); id == vscodeAPIKeyInputID {
					have = true
				}
			}
			out = append(out, it)
		}
	}
	if !have {
		out = append(out, map[string]any{
			"id":          vscodeAPIKeyInputID,
			"type":        "promptString",
			"description": "Cordum MCP API key (Copilot agent identity)",
			"password":    true,
		})
	}
	return out
}
