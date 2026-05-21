package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// Current Claude Code MCP config schema. Fetched 2026-05-16 from
// https://code.claude.com/docs/en/mcp. Top-level `mcpServers` is the
// canonical key; per-entry `type` selects transport (http /
// streamable-http / sse / stdio) and the remaining fields follow.
// Schema-drift detector compares the fetch-time hash against the
// live doc; a mismatch logs a warning but does not block apply.
const (
	claudeCodeSchemaURL  = "https://code.claude.com/docs/en/mcp"
	claudeCodeSchemaDate = "2026-05-16"
)

// claudeCodeAdapter implements AttachAdapter for the Claude Code CLI.
// Target path defaults to ~/.claude.json (user-scope per current
// docs); tests inject a temp-dir path via the constructor so the
// production discovery path stays decoupled from os.UserHomeDir.
type claudeCodeAdapter struct {
	configPath string
}

func newClaudeCodeAdapter(configPath string) *claudeCodeAdapter {
	return &claudeCodeAdapter{configPath: configPath}
}

func (a *claudeCodeAdapter) ClientName() string { return "claude_code" }

func (a *claudeCodeAdapter) ConfigPath() string { return a.configPath }

// ReadAndMerge parses the existing ~/.claude.json (or starts from an
// empty object when existing==nil), inserts or replaces the
// `mcpServers.cordum-gateway` entry per the gateway transport, and
// returns the re-serialized JSON. Preserves all sibling keys verbatim
// (project list, theme, history, etc.) so apply never loses user state.
func (a *claudeCodeAdapter) ReadAndMerge(existing []byte, gateway UpstreamServerRef) ([]byte, string, error) {
	root := map[string]any{}
	if len(bytes.TrimSpace(existing)) > 0 {
		if err := json.Unmarshal(existing, &root); err != nil {
			return nil, "", fmt.Errorf("claude_code: invalid JSON: %w", err)
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
	entry := claudeCodeEntryFor(gateway)
	priorCount := len(servers)
	_, replacing := servers[gatewayName]
	servers[gatewayName] = entry
	root["mcpServers"] = servers

	merged, err := marshalJSONStable(root)
	if err != nil {
		return nil, "", fmt.Errorf("claude_code: serialize: %w", err)
	}
	preview := renderMergeSummary("claude_code", a.configPath, priorCount, gatewayName, replacing, servers)
	return merged, preview, nil
}

// claudeCodeEntryFor returns the per-server map a Claude Code config
// expects under `mcpServers.<name>`. http/sse transports populate
// `type` + `url`; stdio populates `command` + `args`. Falls back to
// http when gateway.Transport is unset because the gateway's primary
// surface is HTTP.
func claudeCodeEntryFor(gateway UpstreamServerRef) map[string]any {
	entry := map[string]any{}
	transport := gateway.Transport
	if transport == "" {
		transport = "http"
	}
	switch transport {
	case "http", "streamable-http", "sse":
		entry["type"] = transport
		entry["url"] = gateway.Endpoint
	case "stdio":
		if len(gateway.Command) > 0 {
			entry["command"] = gateway.Command[0]
			if len(gateway.Command) > 1 {
				entry["args"] = gateway.Command[1:]
			}
		}
	default:
		entry["type"] = transport
		entry["url"] = gateway.Endpoint
	}
	if gateway.AuthSecretRef != "" {
		entry["env"] = map[string]any{"CORDUM_AUTH_SECRET_REF": gateway.AuthSecretRef}
	}
	return entry
}

// marshalJSONStable renders root with 2-space indent and sorted
// top-level keys so the on-disk representation is reproducible across
// runs. Stable output keeps git diffs minimal when operators re-attach.
func marshalJSONStable(root map[string]any) ([]byte, error) {
	keys := make([]string, 0, len(root))
	for k := range root {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf bytes.Buffer
	buf.WriteString("{\n")
	for i, k := range keys {
		keyJSON, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf.WriteString("  ")
		buf.Write(keyJSON)
		buf.WriteString(": ")
		valJSON, err := marshalIndentedValue(root[k], "  ")
		if err != nil {
			return nil, err
		}
		buf.Write(valJSON)
		if i < len(keys)-1 {
			buf.WriteByte(',')
		}
		buf.WriteByte('\n')
	}
	buf.WriteString("}\n")
	return buf.Bytes(), nil
}

// marshalIndentedValue serializes v with the given existing indent so
// the result slots into a parent object with `marshalJSONStable`'s 2-
// space indentation. Uses encoding/json for safe escaping then re-
// indents the result to keep the leading prefix consistent.
func marshalIndentedValue(v any, parentIndent string) ([]byte, error) {
	raw, err := json.MarshalIndent(v, parentIndent, "  ")
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// renderMergeSummary returns the operator-facing diff preview for a
// JSON-backed client. Lists prior server names + flags whether the
// cordum-gateway entry is new or replaced, so operators see the exact
// impact before re-running with `--apply`.
func renderMergeSummary(client, path string, priorCount int, gatewayName string, replacing bool, mergedServers map[string]any) string {
	priorNames := make([]string, 0, len(mergedServers))
	for k := range mergedServers {
		if k == gatewayName {
			continue
		}
		priorNames = append(priorNames, k)
	}
	sort.Strings(priorNames)
	action := "add"
	if replacing {
		action = "update"
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "  client: %s\n", client)
	fmt.Fprintf(&buf, "  path: %s\n", path)
	fmt.Fprintf(&buf, "  existing servers (%d): %v\n", priorCount, priorNames)
	fmt.Fprintf(&buf, "  planned change: %s mcpServers.%s\n", action, gatewayName)
	return buf.String()
}
