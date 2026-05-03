package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	"github.com/cordum/cordum/core/edge/claude"
)

func runEdgeCmd(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: cordumctl edge <claude|doctor|init>")
		return 2
	}
	switch args[0] {
	case "claude":
		return runEdgeClaudeCmd(args[1:], os.Stdin, os.Stdout, os.Stderr)
	case "doctor":
		return runEdgeDoctorCmd(args[1:], os.Stdout, os.Stderr)
	case "init":
		return runEdgeInitCmd(args[1:], os.Stdin, os.Stdout, os.Stderr)
	default:
		fmt.Fprintf(os.Stderr, "unknown edge subcommand %q\n", args[0])
		return 2
	}
}

func runEdgeClaudeCmd(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flagArgs, claudeArgs := splitClaudePassthrough(args)

	// Layered config (default → ~/.cordum/config.yaml → ./cordum.yaml → env).
	// Flags layer on top below via FlagSet defaults that fall back to cfg.X.
	cfg, sources, err := loadEdgeClaudeConfigForRun()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "cordumctl edge claude: %s\n", err)
		return 1
	}

	fs := newFlagSet("edge claude")
	// Replace env-only defaults from newFlagSet() with the YAML+env-resolved
	// values so cordum.yaml fields actually flow through to the launcher.
	*fs.gateway = cfg.Gateway
	*fs.apiKey = cfg.APIKey
	*fs.tenant = cfg.Tenant
	if cfg.CACert != "" {
		*fs.cacert = cfg.CACert
	}

	principal := fs.String("principal", cfg.Principal, "principal id for Edge session evidence")
	cwd := fs.String("cwd", firstEnv("CORDUM_EDGE_CWD"), "working directory for Claude and repository detection")
	repo := fs.String("repo", firstEnv("CORDUM_EDGE_REPO"), "repository label override")
	gitRemote := fs.String("git-remote", firstEnv("CORDUM_EDGE_GIT_REMOTE"), "git remote override")
	gitBranch := fs.String("git-branch", firstEnv("CORDUM_EDGE_GIT_BRANCH"), "git branch override")
	gitSHA := fs.String("git-sha", firstEnv("CORDUM_EDGE_GIT_SHA"), "git sha override")
	hostID := fs.String("host-id", firstEnv("CORDUM_EDGE_HOST_ID"), "host label override")
	deviceID := fs.String("device-id", firstEnv("CORDUM_EDGE_DEVICE_ID"), "device label override")
	dashboardURL := fs.String("dashboard-url", cfg.DashboardURL, "dashboard URL override")
	policyMode := fs.String("policy-mode", cfg.PolicyMode, "policy mode: observe, enforce, or enterprise-strict")
	approvalWait := fs.Duration("approval-wait-timeout", cfg.ApprovalWaitTimeout, "inline approval wait timeout")
	agentdPath := fs.String("agentd-path", cfg.AgentdPath, "cordum-agentd binary path")
	agentdURL := fs.String("agentd-url", "", "local agentd hook URL override")
	claudePath := fs.String("claude-path", firstEnv("CLAUDE_PATH"), "Claude Code binary path")
	hookCommand := fs.String("hook-command", cfg.HookCommand, "cordum-hook command path for generated settings")
	stateDir := fs.String("state-dir", "", "agentd state directory override")
	settingsOutput := fs.String("settings-output", "", "write generated settings.json to path or - without overwriting")
	dryRun := fs.Bool("dry-run", false, "start agentd and render settings, but do not launch Claude; print JSON summary")
	noLaunch := fs.Bool("no-launch", false, "start agentd and render settings, but do not launch Claude")
	verbose := fs.Bool("verbose", false, "print non-secret diagnostics to stderr")
	printConfig := fs.Bool("print-config", false, "render resolved config as YAML with api_key redacted, then exit (no agentd, no Claude)")
	fs.ParseArgs(flagArgs)
	claudeArgs = append(fs.Args(), claudeArgs...)
	effectiveNoLaunch := *noLaunch || *settingsOutput != ""

	// Track which fields were set by user-supplied flags so --print-config
	// reports the actual provenance, not the YAML default.
	fs.Visit(func(f *flag.Flag) {
		if field := edgeClaudeFlagToConfigField(f.Name); field != "" {
			sources[field] = sourceFlag
		}
	})

	if *printConfig {
		resolved := EdgeClaudeConfig{
			Gateway: *fs.gateway, APIKey: *fs.apiKey, Tenant: *fs.tenant,
			Principal: *principal, PolicyMode: *policyMode, CACert: *fs.cacert,
			DashboardURL: *dashboardURL, AgentdPath: *agentdPath,
			HookCommand: *hookCommand, ApprovalWaitTimeout: *approvalWait,
		}
		emitPrintConfig(stdout, resolved, sources)
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	result, err := claude.LaunchEdgeClaude(ctx, claude.LaunchOptions{
		Env: os.Environ(), Stdin: stdin, Stdout: stdout, Stderr: stderr,
		Gateway: *fs.gateway, APIKey: *fs.apiKey, TenantID: *fs.tenant, PrincipalID: *principal,
		CWD: *cwd, Repo: *repo, GitRemote: *gitRemote, GitBranch: *gitBranch, GitSHA: *gitSHA,
		HostID: *hostID, DeviceID: *deviceID, DashboardURL: *dashboardURL, PolicyMode: *policyMode,
		ApprovalWaitTimeout: *approvalWait, AgentdPath: *agentdPath, AgentdURL: *agentdURL,
		ClaudePath: *claudePath, HookCommand: *hookCommand, StateDir: *stateDir,
		ClaudeArgs: claudeArgs, DryRun: *dryRun, NoLaunch: effectiveNoLaunch, Verbose: *verbose,
		CACertPath: *fs.cacert,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "cordumctl edge claude: %s\n", redactEdgeClaudeError(err.Error(), *fs.apiKey))
		return 1
	}
	if *settingsOutput != "" {
		if err := writeEdgeSettingsOutput(stdout, *settingsOutput, result.SettingsJSON); err != nil {
			_, _ = fmt.Fprintf(stderr, "cordumctl edge claude: %s\n", redactEdgeClaudeError(err.Error(), *fs.apiKey))
			return 1
		}
	}
	if *dryRun && *settingsOutput != "-" {
		if err := writeEdgeClaudeJSON(stdout, result); err != nil {
			_, _ = fmt.Fprintf(stderr, "cordumctl edge claude: %s\n", err)
			return 1
		}
	}
	return result.ExitCode
}

func splitClaudePassthrough(args []string) ([]string, []string) {
	for i, arg := range args {
		if arg == "--" {
			return append([]string(nil), args[:i]...), append([]string(nil), args[i+1:]...)
		}
	}
	return args, nil
}

func writeEdgeClaudeJSON(w io.Writer, result claude.LaunchResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(result); err != nil {
		return fmt.Errorf("write dry-run json: %w", err)
	}
	return nil
}

// loadEdgeClaudeConfigForRun resolves the layered config using the real
// process environment, current working directory, and home dir. Failures
// are non-fatal at the loader level (missing file = empty layer); only
// genuine YAML errors / security violations bubble up.
func loadEdgeClaudeConfigForRun() (EdgeClaudeConfig, map[string]configSource, error) {
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	return LoadEdgeClaudeConfig(os.Environ(), cwd, home)
}

// edgeClaudeFlagToConfigField maps a CLI flag name to the matching
// EdgeClaudeConfig YAML key, or returns "" for flags that have no YAML
// counterpart (cwd, repo, git-*, host-id, device-id, etc.).
func edgeClaudeFlagToConfigField(name string) string {
	switch name {
	case "gateway":
		return "gateway"
	case "api-key":
		return "api_key"
	case "tenant":
		return "tenant"
	case "cacert":
		return "cacert"
	case "principal":
		return "principal"
	case "policy-mode":
		return "policy_mode"
	case "dashboard-url":
		return "dashboard_url"
	case "agentd-path":
		return "agentd_path"
	case "hook-command":
		return "hook_command"
	case "approval-wait-timeout":
		return "approval_wait_timeout"
	}
	return ""
}

// emitPrintConfig writes the resolved config as YAML (api_key redacted) plus
// a comment block that names the precedence layer responsible for each
// field. Output is stable and ordered so it is greppable from doctor scripts.
func emitPrintConfig(stdout io.Writer, cfg EdgeClaudeConfig, sources map[string]configSource) {
	_, _ = fmt.Fprintln(stdout, "# Cordum Edge Claude — resolved config (api_key redacted).")
	_, _ = fmt.Fprintln(stdout, "# Source comments below record which precedence layer produced each field.")
	_, _ = fmt.Fprintln(stdout, "#")
	_, _ = fmt.Fprint(stdout, cfg.RenderRedactedYAML())
	_, _ = fmt.Fprintln(stdout, "")
	keys := make([]string, 0, len(sources))
	for k := range sources {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	_, _ = fmt.Fprintln(stdout, "# sources:")
	for _, k := range keys {
		_, _ = fmt.Fprintf(stdout, "#   %s source: %s\n", k, sources[k])
	}
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func firstEnvDefault(fallback string, keys ...string) string {
	if value := firstEnv(keys...); value != "" {
		return value
	}
	return fallback
}

func redactEdgeClaudeError(message, apiKey string) string {
	out := message
	if strings.TrimSpace(apiKey) != "" {
		out = strings.ReplaceAll(out, apiKey, "[REDACTED]")
	}
	return out
}
