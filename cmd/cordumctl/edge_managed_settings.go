package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/cordum/cordum/core/edge/claude"
)

const (
	defaultManagedHookCommand = "/opt/cordum/bin/cordum-hook"
	defaultManagedAgentdURL   = "http://127.0.0.1:8765/v1/edge/hooks/claude"
)

// runEdgeManagedSettingsCmd dispatches the managed-settings CLI subcommands
// (export, verify, rollback-template). All paths return an exit code rather
// than calling os.Exit so the function is safely usable from tests.
func runEdgeManagedSettingsCmd(args []string, stdout, stderr io.Writer) int {
	stdout, stderr = edgeManagedSettingsWriters(stdout, stderr)
	if len(args) < 1 {
		_, _ = fmt.Fprintln(stderr, "usage: cordumctl edge managed-settings <export|verify|rollback-template>")
		return 2
	}
	switch args[0] {
	case "export":
		return runEdgeManagedSettingsExportCmd(args[1:], stdout, stderr)
	case "verify":
		return runEdgeManagedSettingsVerifyCmd(args[1:], stdout, stderr)
	case "rollback-template":
		return runEdgeManagedSettingsRollbackCmd(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown managed-settings subcommand %q\n", args[0])
		_, _ = fmt.Fprintln(stderr, "usage: cordumctl edge managed-settings <export|verify|rollback-template>")
		return 2
	}
}

func edgeManagedSettingsWriters(stdout, stderr io.Writer) (io.Writer, io.Writer) {
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	return stdout, stderr
}

// runEdgeManagedSettingsExportCmd writes managed-settings.json + managed-mcp.json
// into the requested directory. Refuses to overwrite existing files unless
// --force is set; rejects sensitive --hook-command values via the existing
// claude.GenerateManagedSettingsTemplate validator.
func runEdgeManagedSettingsExportCmd(args []string, stdout, stderr io.Writer) int {
	fs := newManagedFlagSet("edge managed-settings export", stderr)
	output := fs.String("output", "", "directory to write managed-settings.json and managed-mcp.json")
	mcpURL := fs.String("mcp-gateway-url", "", "MCP gateway URL for managed-mcp.json")
	llmProxy := fs.String("llm-proxy-base-url", "", "Anthropic LLM proxy base URL")
	apiKeyHelper := fs.String("api-key-helper-command", "", "command that emits the API key for Claude Code's apiKeyHelper")
	hookCommand := fs.String("hook-command", defaultManagedHookCommand, "cordum-hook binary path baked into the managed hooks")
	agentdURL := fs.String("agentd-url", defaultManagedAgentdURL, "loopback agentd hook URL baked into env.CORDUM_AGENTD_URL")
	platform := fs.String("platform", runtime.GOOS, "target platform: linux, darwin, or windows")
	force := fs.Bool("force", false, "overwrite existing files in --output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	dir := strings.TrimSpace(*output)
	if dir == "" {
		_, _ = fmt.Fprintln(stderr, "edge managed-settings export: --output required")
		return 2
	}
	if strings.TrimSpace(*mcpURL) == "" || strings.TrimSpace(*llmProxy) == "" || strings.TrimSpace(*apiKeyHelper) == "" {
		_, _ = fmt.Fprintln(stderr, "edge managed-settings export: --mcp-gateway-url, --llm-proxy-base-url, and --api-key-helper-command are required")
		return 2
	}
	bundle, err := claude.GenerateManagedSettingsTemplate(claude.ManagedSettingsOptions{
		HookCommand:         *hookCommand,
		HookTimeout:         claude.DefaultHookTimeout,
		AgentdURL:           *agentdURL,
		MCPGatewayURL:       *mcpURL,
		LLMProxyBaseURL:     *llmProxy,
		APIKeyHelperCommand: *apiKeyHelper,
		Platform:            strings.TrimSpace(*platform),
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "edge managed-settings export: %s\n", redactManagedSettingsError(err))
		return 2
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		_, _ = fmt.Fprintf(stderr, "edge managed-settings export: %s\n", err)
		return 1
	}
	settingsPath := filepath.Join(dir, "managed-settings.json")
	mcpPath := filepath.Join(dir, "managed-mcp.json")
	for _, item := range []struct {
		path string
		data []byte
	}{
		{path: settingsPath, data: bundle.ManagedSettingsJSON},
		{path: mcpPath, data: bundle.ManagedMCPJSON},
	} {
		if err := writeManagedSettingsOutput(item.path, item.data, *force); err != nil {
			_, _ = fmt.Fprintf(stderr, "edge managed-settings export: %s\n", err)
			if errors.Is(err, errRefusingToOverwrite) {
				return 2
			}
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "wrote %s\n", item.path)
	}
	return 0
}

// runEdgeManagedSettingsVerifyCmd reports drift in a managed-settings.json
// file. Exit codes: 0 = clean, 1 = drift detected, 2 = file/parse problem.
func runEdgeManagedSettingsVerifyCmd(args []string, stdout, stderr io.Writer) int {
	fs := newManagedFlagSet("edge managed-settings verify", stderr)
	path := fs.String("path", "", "path to managed-settings.json to verify")
	emitJSON := fs.Bool("json", false, "emit machine-readable JSON envelope")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*path) == "" {
		_, _ = fmt.Fprintln(stderr, "edge managed-settings verify: --path required")
		return 2
	}
	res, err := claude.VerifyManagedSettingsFromPath(*path)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "edge managed-settings verify: %s\n", redactManagedSettingsError(err))
		return 2
	}
	if *emitJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			_, _ = fmt.Fprintf(stderr, "edge managed-settings verify: %s\n", err)
			return 2
		}
		if !res.OK {
			return 1
		}
		return 0
	}
	if res.OK {
		_, _ = fmt.Fprintln(stdout, "ok: managed-settings.json matches Cordum Edge invariants")
		return 0
	}
	for _, d := range res.Drifts {
		_, _ = fmt.Fprintf(stdout, "drift: %s got=%s want=%s severity=%s\n", d.Field, d.Got, d.Want, d.Severity)
	}
	return 1
}

// runEdgeManagedSettingsRollbackCmd regenerates a synthetic managed-settings.json
// at --path and verifies the result. Production rollback is MDM-orchestrated;
// this subcommand exists for synthetic test fixtures only (DoD #4).
func runEdgeManagedSettingsRollbackCmd(args []string, stdout, stderr io.Writer) int {
	fs := newManagedFlagSet("edge managed-settings rollback-template", stderr)
	path := fs.String("path", "", "managed-settings.json path to overwrite with the freshly-generated template")
	mcpURL := fs.String("mcp-gateway-url", "", "MCP gateway URL")
	llmProxy := fs.String("llm-proxy-base-url", "", "Anthropic LLM proxy base URL")
	apiKeyHelper := fs.String("api-key-helper-command", "", "apiKeyHelper command")
	hookCommand := fs.String("hook-command", defaultManagedHookCommand, "cordum-hook binary path")
	agentdURL := fs.String("agentd-url", defaultManagedAgentdURL, "loopback agentd hook URL")
	platform := fs.String("platform", runtime.GOOS, "target platform")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*path) == "" {
		_, _ = fmt.Fprintln(stderr, "edge managed-settings rollback-template: --path required")
		return 2
	}
	if strings.TrimSpace(*mcpURL) == "" || strings.TrimSpace(*llmProxy) == "" || strings.TrimSpace(*apiKeyHelper) == "" {
		_, _ = fmt.Fprintln(stderr, "edge managed-settings rollback-template: --mcp-gateway-url, --llm-proxy-base-url, and --api-key-helper-command are required")
		return 2
	}
	bundle, err := claude.GenerateManagedSettingsTemplate(claude.ManagedSettingsOptions{
		HookCommand:         *hookCommand,
		HookTimeout:         claude.DefaultHookTimeout,
		AgentdURL:           *agentdURL,
		MCPGatewayURL:       *mcpURL,
		LLMProxyBaseURL:     *llmProxy,
		APIKeyHelperCommand: *apiKeyHelper,
		Platform:            strings.TrimSpace(*platform),
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "edge managed-settings rollback-template: %s\n", redactManagedSettingsError(err))
		return 2
	}
	if err := atomicWriteManagedSettings(*path, bundle.ManagedSettingsJSON); err != nil {
		_, _ = fmt.Fprintf(stderr, "edge managed-settings rollback-template: %s\n", err)
		return 1
	}
	res, err := claude.VerifyManagedSettingsFromPath(*path)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "edge managed-settings rollback-template: %s\n", redactManagedSettingsError(err))
		return 1
	}
	if !res.OK {
		for _, d := range res.Drifts {
			_, _ = fmt.Fprintf(stderr, "post-rollback drift: %s got=%s want=%s severity=%s\n", d.Field, d.Got, d.Want, d.Severity)
		}
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "ok: rewrote %s with the freshly generated managed-settings template\n", *path)
	return 0
}

var errRefusingToOverwrite = errors.New("refusing to overwrite existing managed settings file")

// writeManagedSettingsOutput writes payload to path. Without force, it uses
// O_EXCL so a concurrent operator export wins exactly one writer; with
// force it delegates to atomicWriteManagedSettings (temp-file + rename) so
// a crashed process never leaves a half-written file on disk that a
// downstream reader on macOS/Windows could pick up as the active managed
// configuration. Mode 0600 prevents fleet workstation readers other than
// the operator from harvesting the file.
func writeManagedSettingsOutput(path string, payload []byte, force bool) error {
	clean := filepath.Clean(path)
	if force {
		return atomicWriteManagedSettings(clean, payload)
	}
	f, err := os.OpenFile(clean, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%w: %s (re-run with --force to overwrite)", errRefusingToOverwrite, clean)
		}
		return fmt.Errorf("create %s: %w", clean, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(payload); err != nil {
		return fmt.Errorf("write %s: %w", clean, err)
	}
	return nil
}

// atomicWriteManagedSettings replaces the file at path with payload using a
// temp-file + os.Rename swap so a crashed process never leaves a half-written
// managed-settings.json on disk.
func atomicWriteManagedSettings(path string, payload []byte) error {
	clean := filepath.Clean(path)
	dir := filepath.Dir(clean)
	tmp, err := os.CreateTemp(dir, ".managed-settings-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write temp file %s: %w", tmpName, err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("chmod temp file %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("sync temp file %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close temp file %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, clean); err != nil {
		cleanup()
		return fmt.Errorf("rename %s -> %s: %w", tmpName, clean, err)
	}
	return nil
}

// newManagedFlagSet returns a flag.FlagSet wired up with ContinueOnError so
// callers translate parse failures into exit codes rather than os.Exit.
func newManagedFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

// redactManagedSettingsError strips newlines and trims known leak vectors
// from error strings before they reach stderr. The underlying value-level
// secret check happens in claude.GenerateManagedSettingsTemplate so this
// shim only normalises whitespace and clamps overly long messages.
func redactManagedSettingsError(err error) string {
	if err == nil {
		return ""
	}
	out := err.Error()
	out = strings.ReplaceAll(out, "\n", " ")
	out = strings.ReplaceAll(out, "\r", " ")
	if len(out) > 512 {
		out = out[:512] + "..."
	}
	return out
}
