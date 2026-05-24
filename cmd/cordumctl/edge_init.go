package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// runEdgeInitCmd scaffolds ./cordum.yaml + an optional `cordum-claude` wrapper
// script for the `cordumctl edge claude` workflow. It is idempotent — running
// it twice with the same flag inputs produces byte-identical output, so a
// re-run after editing flags only changes what was actually edited.
//
// API-key handling: the YAML always stores `${ENV_VAR_NAME}` references
// (default ENV_VAR_NAME=CORDUM_API_KEY). A literal `--api-key` value is
// rejected to prevent secrets being written to a checked-in file. Operators
// who want a different env-var name pass `--api-key-env`.
func runEdgeInitCmd(args []string, _ io.Reader, stdout, stderr io.Writer) int {
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	fs := flag.NewFlagSet("edge init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cwd := fs.String("cwd", "", "directory to write cordum.yaml in (default: current dir)")
	force := fs.Bool("force", false, "overwrite an existing cordum.yaml")
	gateway := fs.String("gateway", firstEnvDefault("https://localhost:8081", "CORDUM_GATEWAY"), "gateway base URL")
	tenant := fs.String("tenant", firstEnvDefault("default", "CORDUM_TENANT_ID"), "tenant id")
	principal := fs.String("principal", firstEnv("CORDUM_PRINCIPAL_ID", "CORDUM_EDGE_PRINCIPAL_ID"), "principal id")
	policyMode := fs.String("policy-mode", firstEnvDefault("enforce", "CORDUM_EDGE_POLICY_MODE"), "policy mode: observe, enforce, or enterprise-strict")
	cacertFlag := fs.String("cacert", "", "CA certificate path (auto-detected from ./certs/ca/ca.crt for localhost gateway if empty)")
	dashboardURL := fs.String("dashboard-url", firstEnv("CORDUM_EDGE_DASHBOARD_URL", "CORDUM_DASHBOARD_URL"), "dashboard URL")
	agentdPathFlag := fs.String("agentd-path", firstEnv("CORDUM_AGENTD_PATH"), "cordum-agentd binary path (auto-detected if empty)")
	hookCommandFlag := fs.String("hook-command", firstEnv("CORDUM_HOOK_COMMAND"), "cordum-hook command path (auto-detected if empty)")
	approvalWait := fs.String("approval-wait-timeout", "30s", "inline approval wait timeout (Go duration)")
	apiKeyEnv := fs.String("api-key-env", "CORDUM_API_KEY", "env var name to reference for api_key (written to YAML as ${VAR})")
	apiKeyPlaintext := fs.String("api-key", "", "rejected — see --api-key-env (this flag exists only to surface a clear error if a user tries plaintext)")
	noWrapper := fs.Bool("no-wrapper", false, "skip generating the cordum-claude wrapper script")
	nonInteractive := fs.Bool("non-interactive", false, "fail instead of prompting if a required field is missing")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if strings.TrimSpace(*apiKeyPlaintext) != "" {
		_, _ = fmt.Fprintln(stderr, "edge init: --api-key plaintext is rejected; use --api-key-env <NAME> so the YAML stores ${NAME} and resolves at runtime")
		return 2
	}

	resolvedCwd, err := resolveInitCwd(*cwd)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "edge init: %s\n", err)
		return 1
	}
	yamlPath := filepath.Join(resolvedCwd, "cordum.yaml")
	if !*force {
		if _, err := os.Stat(yamlPath); err == nil {
			_, _ = fmt.Fprintf(stderr, "edge init: %s already exists — re-run with --force to overwrite\n", yamlPath)
			return 1
		} else if !os.IsNotExist(err) {
			_, _ = fmt.Fprintf(stderr, "edge init: stat %s: %s\n", yamlPath, err)
			return 1
		}
	}

	if strings.TrimSpace(*principal) == "" && *nonInteractive {
		_, _ = fmt.Fprintln(stderr, "edge init: --principal required in non-interactive mode")
		return 1
	}

	scaffold := edgeInitScaffold{
		Gateway:             *gateway,
		Tenant:              *tenant,
		Principal:           *principal,
		PolicyMode:          *policyMode,
		CACert:              detectInitCACert(*cacertFlag, *gateway, resolvedCwd),
		DashboardURL:        *dashboardURL,
		AgentdPath:          detectInitBinary(*agentdPathFlag, "cordum-agentd"),
		HookCommand:         detectInitBinary(*hookCommandFlag, "cordum-hook"),
		ApprovalWaitTimeout: strings.TrimSpace(*approvalWait),
		APIKeyEnvVar:        strings.TrimSpace(*apiKeyEnv),
	}
	if scaffold.APIKeyEnvVar == "" {
		scaffold.APIKeyEnvVar = "CORDUM_API_KEY"
	}

	if err := writeInitYAML(yamlPath, scaffold); err != nil {
		_, _ = fmt.Fprintf(stderr, "edge init: %s\n", err)
		return 1
	}

	if !*noWrapper {
		wrapperPath, err := writeInitWrapper(resolvedCwd, scaffold)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "edge init: wrapper script: %s\n", err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "wrote %s\n", wrapperPath)
	}
	_, _ = fmt.Fprintf(stdout, "wrote %s\n", yamlPath)
	emitInitNextSteps(stdout, scaffold)
	return 0
}

func resolveInitCwd(flagValue string) (string, error) {
	if strings.TrimSpace(flagValue) != "" {
		return flagValue, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve cwd: %w", err)
	}
	return cwd, nil
}

type edgeInitScaffold struct {
	Gateway             string
	Tenant              string
	Principal           string
	PolicyMode          string
	CACert              string
	DashboardURL        string
	AgentdPath          string
	HookCommand         string
	ApprovalWaitTimeout string
	APIKeyEnvVar        string
}

// detectInitCACert returns the explicit cacert flag if set; otherwise, for a
// localhost-https gateway, looks for ./certs/ca/ca.crt in the target cwd.
func detectInitCACert(explicit, gateway, cwd string) string {
	if strings.TrimSpace(explicit) != "" {
		return explicit
	}
	if !isLocalhostHTTPSGateway(gateway) {
		return ""
	}
	candidate := filepath.Join(cwd, "certs", "ca", "ca.crt")
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return "./" + filepath.ToSlash(filepath.Join("certs", "ca", "ca.crt"))
	}
	return ""
}

// detectInitBinary returns explicit if set; otherwise tries to find the
// binary on PATH. On Windows, looks for `<name>.exe` first per EDGE-045's
// runtime fallback. Returns empty string when the binary is nowhere visible
// — letting `cordumctl edge claude` later auto-resolve via PATH at launch
// time, which works just as well.
func detectInitBinary(explicit, name string) string {
	if strings.TrimSpace(explicit) != "" {
		return explicit
	}
	candidates := []string{name}
	if runtime.GOOS == "windows" {
		candidates = []string{name + ".exe", name}
	}
	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate); err == nil && path != "" {
			return path
		}
	}
	return ""
}

// writeInitYAML emits cordum.yaml in a fixed key order so the output is
// deterministic across re-runs (idempotency contract). Comments explain the
// security rail to anyone reviewing the checked-in file.
func writeInitYAML(path string, s edgeInitScaffold) error {
	var b strings.Builder
	b.WriteString("# Cordum Edge — managed by `cordumctl edge init`.\n")
	b.WriteString("# api_key MUST be empty or a ${ENV_VAR} reference; plaintext is rejected at load time.\n")
	b.WriteString("# Override any field via the matching env var (CORDUM_GATEWAY, CORDUM_TENANT_ID, …) or CLI flag.\n")
	b.WriteString("\n")
	writeYAMLField(&b, "gateway", s.Gateway)
	writeYAMLField(&b, "api_key", "${"+s.APIKeyEnvVar+"}")
	writeYAMLField(&b, "tenant", s.Tenant)
	writeYAMLField(&b, "principal", s.Principal)
	writeYAMLField(&b, "policy_mode", s.PolicyMode)
	writeYAMLField(&b, "cacert", s.CACert)
	writeYAMLField(&b, "dashboard_url", s.DashboardURL)
	writeYAMLField(&b, "agentd_path", s.AgentdPath)
	writeYAMLField(&b, "hook_command", s.HookCommand)
	writeYAMLField(&b, "approval_wait_timeout", s.ApprovalWaitTimeout)
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	// WriteFile only applies the mode on create; enforce owner-only perms on
	// `--force` re-runs over a pre-existing, looser-permissioned file too.
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}

func writeYAMLField(b *strings.Builder, key, value string) {
	v := strings.TrimSpace(value)
	if v == "" {
		fmt.Fprintf(b, "# %s: # not set; falls back to env or built-in default\n", key)
		return
	}
	fmt.Fprintf(b, "%s: %s\n", key, v)
}

// writeInitWrapper writes a tiny `cordum-claude` script that delegates to
// `cordumctl edge claude` so users can invoke `./cordum-claude -- <claude args>`
// without typing the cordumctl prefix.
func writeInitWrapper(cwd string, _ edgeInitScaffold) (string, error) {
	if runtime.GOOS == "windows" {
		path := filepath.Join(cwd, "cordum-claude.ps1")
		body := wrapperPS1Body()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			return "", fmt.Errorf("write %s: %w", path, err)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return "", fmt.Errorf("chmod %s: %w", path, err)
		}
		return path, nil
	}
	path := filepath.Join(cwd, "cordum-claude.sh")
	body := wrapperShBody()
	// The wrapper is invoked directly by the user, so it must keep the owner
	// execute bit; 0700 is owner-only (no group/world) and as tight as an
	// executable file can be.
	err := os.WriteFile(path, []byte(body), 0o700) // #nosec G306 -- wrapper script must be owner-executable; 0700 is owner-only
	if err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	// Enforce the mode on re-runs over a pre-existing, looser-permissioned file.
	chmodErr := os.Chmod(path, 0o700) // #nosec G302 -- wrapper script must be owner-executable; 0700 is owner-only
	if chmodErr != nil {
		return "", fmt.Errorf("chmod %s: %w", path, chmodErr)
	}
	return path, nil
}

func wrapperShBody() string {
	return `#!/usr/bin/env bash
# Cordum Edge Claude wrapper — generated by ` + "`cordumctl edge init`" + `.
# Loads ./cordum.yaml + env vars then launches the governed Claude session.
# Pass extra Claude args after the literal '--'.
set -euo pipefail
exec cordumctl edge claude "$@"
`
}

func wrapperPS1Body() string {
	return `# Cordum Edge Claude wrapper — generated by ` + "`cordumctl edge init`" + `.
# Loads .\cordum.yaml + env vars then launches the governed Claude session.
# Pass extra Claude args after the literal '--'.
$ErrorActionPreference = 'Stop'
& cordumctl edge claude @args
exit $LASTEXITCODE
`
}

func emitInitNextSteps(stdout io.Writer, s edgeInitScaffold) {
	_, _ = fmt.Fprintln(stdout, "")
	_, _ = fmt.Fprintln(stdout, "Next:")
	_, _ = fmt.Fprintf(stdout, "  1. Export your API key:    export %s=<your-key>\n", s.APIKeyEnvVar)
	_, _ = fmt.Fprintln(stdout, "  2. Verify resolved config: cordumctl edge claude --print-config")
	_, _ = fmt.Fprintln(stdout, "  3. Launch governed Claude: cordumctl edge claude")
	if runtime.GOOS == "windows" {
		_, _ = fmt.Fprintln(stdout, "     or via wrapper:        .\\cordum-claude.ps1")
	} else {
		_, _ = fmt.Fprintln(stdout, "     or via wrapper:        ./cordum-claude.sh")
	}
}
