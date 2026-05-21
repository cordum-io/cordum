package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

const defaultMCPGatewayEndpoint = "https://localhost:8081/api/v1/mcp/gateway/upstream"

// runMCPAttachCmd dispatches the `cordumctl mcp <preview|attach|rollback>`
// subcommands. Returns the process exit code so the caller (main.go)
// can propagate non-zero. Stdout writes go to the supplied writer so
// tests can capture output; stderr captures usage errors.
//
// The first arg is the verb the parent mcp dispatcher already pulled
// off (preview / attach / rollback). Remaining args carry the
// `--client` selector and the optional `--apply` / `--config-path` /
// gateway flags.
func runMCPAttachCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: cordumctl mcp <preview|attach|rollback> --client <claude_code|codex|cursor>")
		return 2
	}
	verb := args[0]
	rest := args[1:]
	fs := flag.NewFlagSet("mcp "+verb, flag.ContinueOnError)
	fs.SetOutput(stderr)
	client := fs.String("client", "", "client to target: claude_code | codex | cursor")
	configPath := fs.String("config-path", "", "override the default per-client config path (test/CI use)")
	apply := fs.Bool("apply", false, "for `attach` only: write changes (without this flag, attach refuses to write)")
	gatewayName := fs.String("gateway-name", "cordum-gateway", "MCP server entry name to add/update")
	gatewayTransport := fs.String("gateway-transport", "http", "transport: http | sse | stdio")
	gatewayEndpoint := fs.String("gateway-endpoint", defaultMCPGatewayEndpoint, "HTTP/SSE endpoint URL")
	gatewaySecretRef := fs.String("gateway-secret-ref", "", "optional secret:// reference for the gateway auth token")
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if *client == "" {
		_, _ = fmt.Fprintln(stderr, "missing --client (claude_code | codex | cursor)")
		return 2
	}
	adapter, err := buildAttachAdapter(*client, *configPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err.Error())
		return 2
	}
	gateway := UpstreamServerRef{
		Name:          *gatewayName,
		Transport:     *gatewayTransport,
		Endpoint:      *gatewayEndpoint,
		AuthSecretRef: *gatewaySecretRef,
	}
	switch verb {
	case "preview":
		return PreviewAttach(adapter, gateway, stdout)
	case "attach":
		if !*apply {
			// Task rail #1: do not overwrite user configs without explicit
			// --apply. Fall through to a preview rather than erroring so
			// operators see what would happen, then re-run with --apply.
			_, _ = fmt.Fprintln(stdout, "attach: --apply not set; running preview only (re-run with --apply to write)")
			return PreviewAttach(adapter, gateway, stdout)
		}
		return ApplyAttach(adapter, gateway, stdout)
	case "rollback":
		return RollbackAttach(adapter, stdout)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown mcp attach verb %q (want preview | attach | rollback)\n", verb)
		return 2
	}
}

// buildAttachAdapter resolves a per-client adapter from the (client,
// optional configPath) pair. When configPath is empty we read
// os.UserHomeDir + DefaultConfigPath; tests pass a non-empty path
// pointing into t.TempDir() to bypass the home-dir resolution.
func buildAttachAdapter(client, configPath string) (AttachAdapter, error) {
	if configPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home dir: %w", err)
		}
		configPath = DefaultConfigPath(client, home)
		if configPath == "" {
			return nil, fmt.Errorf("unknown client %q (want claude_code | codex | cursor)", client)
		}
	}
	switch client {
	case "claude_code":
		return newClaudeCodeAdapter(configPath), nil
	case "codex":
		return newCodexAdapter(configPath), nil
	case "cursor":
		return newCursorAdapter(configPath), nil
	default:
		return nil, fmt.Errorf("unknown client %q (want claude_code | codex | cursor)", client)
	}
}
