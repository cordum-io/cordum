package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/cordum/cordum/core/edge/claude"
)

func runEdgeCmd(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: cordumctl edge <claude|doctor>")
		return 2
	}
	switch args[0] {
	case "claude":
		return runEdgeClaudeCmd(args[1:], os.Stdin, os.Stdout, os.Stderr)
	case "doctor":
		return runEdgeDoctorCmd(args[1:], os.Stdout, os.Stderr)
	default:
		fmt.Fprintf(os.Stderr, "unknown edge subcommand %q\n", args[0])
		return 2
	}
}

func runEdgeClaudeCmd(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flagArgs, claudeArgs := splitClaudePassthrough(args)
	fs := newFlagSet("edge claude")
	principal := fs.String("principal", firstEnv("CORDUM_PRINCIPAL_ID", "CORDUM_EDGE_PRINCIPAL_ID"), "principal id for Edge session evidence")
	cwd := fs.String("cwd", firstEnv("CORDUM_EDGE_CWD"), "working directory for Claude and repository detection")
	repo := fs.String("repo", firstEnv("CORDUM_EDGE_REPO"), "repository label override")
	gitRemote := fs.String("git-remote", firstEnv("CORDUM_EDGE_GIT_REMOTE"), "git remote override")
	gitBranch := fs.String("git-branch", firstEnv("CORDUM_EDGE_GIT_BRANCH"), "git branch override")
	gitSHA := fs.String("git-sha", firstEnv("CORDUM_EDGE_GIT_SHA"), "git sha override")
	hostID := fs.String("host-id", firstEnv("CORDUM_EDGE_HOST_ID"), "host label override")
	deviceID := fs.String("device-id", firstEnv("CORDUM_EDGE_DEVICE_ID"), "device label override")
	dashboardURL := fs.String("dashboard-url", firstEnv("CORDUM_EDGE_DASHBOARD_URL", "CORDUM_DASHBOARD_URL"), "dashboard URL override")
	policyMode := fs.String("policy-mode", firstEnvDefault("enforce", "CORDUM_EDGE_POLICY_MODE"), "policy mode: observe, enforce, or enterprise-strict")
	approvalWait := fs.Duration("approval-wait-timeout", 30*time.Second, "inline approval wait timeout")
	agentdPath := fs.String("agentd-path", firstEnv("CORDUM_AGENTD_PATH"), "cordum-agentd binary path")
	agentdURL := fs.String("agentd-url", "", "local agentd hook URL override")
	claudePath := fs.String("claude-path", firstEnv("CLAUDE_PATH"), "Claude Code binary path")
	hookCommand := fs.String("hook-command", firstEnvDefault("cordum-hook", "CORDUM_HOOK_COMMAND"), "cordum-hook command path for generated settings")
	stateDir := fs.String("state-dir", "", "agentd state directory override")
	settingsOutput := fs.String("settings-output", "", "write generated settings.json to path or - without overwriting")
	dryRun := fs.Bool("dry-run", false, "start agentd and render settings, but do not launch Claude; print JSON summary")
	noLaunch := fs.Bool("no-launch", false, "start agentd and render settings, but do not launch Claude")
	verbose := fs.Bool("verbose", false, "print non-secret diagnostics to stderr")
	fs.ParseArgs(flagArgs)
	claudeArgs = append(fs.Args(), claudeArgs...)
	effectiveNoLaunch := *noLaunch || *settingsOutput != ""
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
	})
	if err != nil {
		fmt.Fprintf(stderr, "cordumctl edge claude: %s\n", redactEdgeClaudeError(err.Error(), *fs.apiKey))
		return 1
	}
	if *settingsOutput != "" {
		if err := writeEdgeSettingsOutput(stdout, *settingsOutput, result.SettingsJSON); err != nil {
			fmt.Fprintf(stderr, "cordumctl edge claude: %s\n", redactEdgeClaudeError(err.Error(), *fs.apiKey))
			return 1
		}
	}
	if *dryRun && *settingsOutput != "-" {
		if err := writeEdgeClaudeJSON(stdout, result); err != nil {
			fmt.Fprintf(stderr, "cordumctl edge claude: %s\n", err)
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
