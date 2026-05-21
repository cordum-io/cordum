package main

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
)

// Current Codex MCP config schema. Fetched 2026-05-16 from
// https://developers.openai.com/codex/config-reference. Codex stores
// MCP servers as `[mcp_servers.<id>]` TOML sections with fields
// command / args / cwd / env / enabled / required / startup_timeout_sec
// / tool_timeout_sec. Stdio-only per current docs; HTTP/SSE gateways
// are rejected until a local proxy command is implemented.
const (
	codexSchemaURL  = "https://developers.openai.com/codex/config-reference"
	codexSchemaDate = "2026-05-16"
)

// codexAdapter implements AttachAdapter for OpenAI Codex's
// config.toml. The merge is line-aware: existing `[mcp_servers.*]`
// blocks for other servers are preserved verbatim (whitespace +
// comments stay put) and the cordum-gateway block is inserted or
// replaced in-place so operator-authored comments above other
// sections are not reshuffled.
type codexAdapter struct {
	configPath string
}

func newCodexAdapter(configPath string) *codexAdapter {
	return &codexAdapter{configPath: configPath}
}

func (a *codexAdapter) ClientName() string { return "codex" }
func (a *codexAdapter) ConfigPath() string { return a.configPath }

func (a *codexAdapter) ReadAndMerge(existing []byte, gateway UpstreamServerRef) ([]byte, string, error) {
	priorSections, priorErr := parseCodexMCPSections(existing)
	if priorErr != nil {
		return nil, "", fmt.Errorf("codex: invalid TOML: %w", priorErr)
	}
	gatewayID := codexIDOf(gateway.Name)
	if gatewayID == "" {
		gatewayID = "cordum_gateway"
	}
	replacing := false
	priorIDs := make([]string, 0, len(priorSections))
	for _, sec := range priorSections {
		if sec.id == gatewayID {
			replacing = true
			continue
		}
		priorIDs = append(priorIDs, sec.id)
	}
	sort.Strings(priorIDs)

	merged, err := renderCodexMerged(existing, gatewayID, gateway, replacing)
	if err != nil {
		return nil, "", fmt.Errorf("codex: render: %w", err)
	}
	preview := codexMergePreview(a.configPath, priorIDs, gatewayID, replacing)
	return merged, preview, nil
}

// codexMCPSection captures a single `[mcp_servers.<id>]` block we
// found in the existing config. We carry the raw byte slice so the
// re-rendered output preserves comments + whitespace verbatim for
// every block we are NOT replacing.
type codexMCPSection struct {
	id    string
	start int // inclusive byte offset of the header line
	end   int // exclusive byte offset (first byte of the next section or EOF)
}

// parseCodexMCPSections returns the offsets of every
// `[mcp_servers.<id>]` block in the existing config. Non-mcp_servers
// sections and top-level keys are NOT enumerated — we only need to
// know where the mcp_servers blocks live so we can replace one of
// them in place. Returns an error when a section header is malformed
// (unterminated bracket) so parse failures abort apply.
func parseCodexMCPSections(data []byte) ([]codexMCPSection, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var out []codexMCPSection
	lines := bytes.Split(data, []byte("\n"))
	offset := 0
	current := codexMCPSection{start: -1}
	for _, line := range lines {
		trimmed := bytes.TrimSpace(line)
		// Detect a section header.
		if len(trimmed) > 0 && trimmed[0] == '[' {
			if !bytes.Contains(trimmed, []byte("]")) {
				return nil, fmt.Errorf("unterminated section header: %q", string(trimmed))
			}
			if current.start >= 0 {
				current.end = offset
				out = append(out, current)
				current = codexMCPSection{start: -1}
			}
			id := codexExtractMCPID(trimmed)
			if id != "" {
				current = codexMCPSection{id: id, start: offset}
			}
		}
		offset += len(line) + 1 // +1 for the newline byte
	}
	if current.start >= 0 {
		current.end = len(data)
		out = append(out, current)
	}
	return out, nil
}

// codexExtractMCPID returns the bare server id for an
// `[mcp_servers.<id>]` header line or "" for any other section.
// Whitespace inside the brackets is tolerated.
func codexExtractMCPID(header []byte) string {
	end := bytes.IndexByte(header, ']')
	if end < 0 {
		return ""
	}
	inner := strings.TrimSpace(string(header[1:end]))
	const prefix = "mcp_servers."
	if !strings.HasPrefix(inner, prefix) {
		return ""
	}
	return strings.TrimSpace(inner[len(prefix):])
}

// codexIDOf normalises a server name into a TOML-safe bare key. TOML
// bare keys allow A-Za-z0-9_- so the canonical gateway name
// "cordum-gateway" stays intact; non-bare characters (dots, slashes,
// spaces) are dropped so the resulting section never needs quoting.
func codexIDOf(name string) string {
	id := strings.TrimSpace(name)
	if id == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(id))
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			// Drop anything else (dots, slashes, spaces) — safer than
			// emitting a quoted key the user's tooling may choke on.
		}
	}
	return b.String()
}

// renderCodexMerged returns the new config bytes. If we are replacing
// an existing `[mcp_servers.<gatewayID>]` block we splice the new
// rendering into that location; otherwise we append the new block at
// the end of the file with a leading newline.
func renderCodexMerged(existing []byte, gatewayID string, gateway UpstreamServerRef, replacing bool) ([]byte, error) {
	block, err := renderCodexGatewayBlock(gatewayID, gateway)
	if err != nil {
		return nil, err
	}
	if !replacing {
		return appendCodexGatewayBlock(existing, block), nil
	}
	sections, err := parseCodexMCPSections(existing)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	written := false
	cursor := 0
	for _, sec := range sections {
		if sec.id != gatewayID {
			continue
		}
		out.Write(existing[cursor:sec.start])
		out.Write(block)
		// Skip any blank lines immediately following the replaced block
		// so we don't accumulate gaps on repeated apply.
		end := sec.end
		for end < len(existing) && existing[end] == '\n' {
			end++
		}
		// Only emit a trailing separator when content follows; appending
		// a newline at EOF would defeat idempotency (each apply would
		// grow the file by one byte).
		if end < len(existing) {
			out.WriteByte('\n')
		}
		cursor = end
		written = true
		break
	}
	if cursor < len(existing) {
		out.Write(existing[cursor:])
	}
	if !written {
		// Defensive: replacing flag said yes but parser disagreed.
		// Fall back to append so we never silently drop the new block.
		return appendCodexGatewayBlock(existing, block), nil
	}
	return out.Bytes(), nil
}

func appendCodexGatewayBlock(existing, block []byte) []byte {
	var out bytes.Buffer
	out.Write(bytes.TrimRight(existing, "\n"))
	if out.Len() > 0 {
		out.WriteString("\n\n")
	}
	out.Write(block)
	return out.Bytes()
}

// renderCodexGatewayBlock returns the canonical
// `[mcp_servers.<gatewayID>]` block for the supplied gateway ref.
// Codex's MCP transport is stdio-only per current docs, so HTTP/SSE
// gateways fail fast rather than writing a non-existent proxy command
// into a user config.
func renderCodexGatewayBlock(gatewayID string, gateway UpstreamServerRef) ([]byte, error) {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "[mcp_servers.%s]\n", gatewayID)

	transport := strings.ToLower(strings.TrimSpace(gateway.Transport))
	switch transport {
	case "":
		if len(gateway.Command) == 0 {
			return nil, fmt.Errorf("codex: empty transport requires explicit --gateway-transport stdio or a local --gateway-command")
		}
		writeCodexCommand(&buf, gateway.Command)
	case "stdio":
		if len(gateway.Command) == 0 {
			return nil, fmt.Errorf("codex: stdio transport requires non-empty Command")
		}
		writeCodexCommand(&buf, gateway.Command)
	case "http", "sse", "streamable-http":
		return nil, fmt.Errorf("codex: transport %s unsupported: Codex config is stdio-only and cordumctl mcp proxy not implemented; use --gateway-transport stdio with a local --gateway-command, or use --client claude_code/cursor for HTTP", transport)
	default:
		return nil, fmt.Errorf("codex: unknown transport %q", gateway.Transport)
	}
	if gateway.AuthSecretRef != "" {
		fmt.Fprintf(&buf, "env = { CORDUM_AUTH_SECRET_REF = %q }\n", gateway.AuthSecretRef)
	}
	buf.WriteString("enabled = true\n")
	return buf.Bytes(), nil
}

func writeCodexCommand(buf *bytes.Buffer, command []string) {
	fmt.Fprintf(buf, "command = %q\n", command[0])
	if len(command) <= 1 {
		return
	}
	buf.WriteString("args = [")
	for i, arg := range command[1:] {
		if i > 0 {
			buf.WriteString(", ")
		}
		fmt.Fprintf(buf, "%q", arg)
	}
	buf.WriteString("]\n")
}

// codexMergePreview renders the operator-facing summary for a TOML-
// backed Codex merge. Mirrors renderMergeSummary's shape so preview
// output is consistent across JSON and TOML clients.
func codexMergePreview(path string, priorIDs []string, gatewayID string, replacing bool) string {
	action := "add"
	if replacing {
		action = "update"
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "  client: codex\n")
	fmt.Fprintf(&buf, "  path: %s\n", path)
	fmt.Fprintf(&buf, "  existing servers (%d): %v\n", len(priorIDs), priorIDs)
	fmt.Fprintf(&buf, "  planned change: %s [mcp_servers.%s]\n", action, gatewayID)
	return buf.String()
}
