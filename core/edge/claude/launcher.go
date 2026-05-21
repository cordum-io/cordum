package claude

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordum/cordum/core/edge/safeexec"
)

const (
	defaultAgentdExecutable       = "cordum-agentd"
	defaultClaudeExecutable       = "claude"
	defaultHookExecutable         = "cordum-hook"
	defaultLaunchAgentdReadyWait  = 10 * time.Second
	defaultLaunchSessionStateWait = 10 * time.Second
)

// LaunchOptions controls the local Cordum-governed Claude Code wrapper.
type LaunchOptions struct {
	Env                 []string
	Stdin               io.Reader
	Stdout              io.Writer
	Stderr              io.Writer
	Gateway             string
	APIKey              string
	TenantID            string
	PrincipalID         string
	CWD                 string
	Repo                string
	GitRemote           string
	GitBranch           string
	GitSHA              string
	HostID              string
	DeviceID            string
	DashboardURL        string
	PolicyMode          string
	ApprovalWaitTimeout time.Duration
	AgentdPath          string
	AgentdURL           string
	ClaudePath          string
	HookCommand         string
	StateDir            string
	TempDir             string
	ClaudeArgs          []string
	DryRun              bool
	NoLaunch            bool
	Verbose             bool
	// CACertPath, when non-empty, is forwarded to the cordum-agentd
	// subprocess as CORDUM_TLS_CA so it can validate Gateway TLS against
	// a locally-issued CA. Required on Windows when the Gateway uses a
	// self-signed cert (Go's HTTP client there ignores SSL_CERT_FILE).
	CACertPath string
}

// LaunchResult is safe to print in dry-run diagnostics. It intentionally omits
// API keys and hook nonces.
type LaunchResult struct {
	Gateway             string            `json:"gateway"`
	APIKeyConfigured    bool              `json:"api_key_configured"`
	TenantID            string            `json:"tenant_id"`
	PrincipalID         string            `json:"principal_id"`
	CWD                 string            `json:"cwd"`
	Repo                string            `json:"repo,omitempty"`
	GitRemote           string            `json:"git_remote,omitempty"`
	GitBranch           string            `json:"git_branch,omitempty"`
	GitSHA              string            `json:"git_sha,omitempty"`
	HostID              string            `json:"host_id,omitempty"`
	DeviceID            string            `json:"device_id,omitempty"`
	PolicyMode          string            `json:"policy_mode"`
	ApprovalWaitTimeout string            `json:"approval_wait_timeout"`
	AgentdPath          string            `json:"agentd_path"`
	AgentdURL           string            `json:"agentd_url"`
	ClaudePath          string            `json:"claude_path,omitempty"`
	SettingsPath        string            `json:"settings_path"`
	StateDir            string            `json:"state_dir"`
	SessionID           string            `json:"session_id,omitempty"`
	ExecutionID         string            `json:"execution_id,omitempty"`
	DashboardURL        string            `json:"dashboard_url,omitempty"`
	DryRun              bool              `json:"dry_run"`
	NoLaunch            bool              `json:"no_launch"`
	ExitCode            int               `json:"exit_code"`
	Metadata            map[string]string `json:"metadata,omitempty"`
	SettingsJSON        []byte            `json:"-"`
}

// LaunchEdgeClaude starts cordum-agentd, writes temporary Claude settings, and
// optionally launches Claude Code with those settings.
func LaunchEdgeClaude(ctx context.Context, opts LaunchOptions) (LaunchResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	stdout, stderr := launchWriters(opts)
	meta, err := ResolveLaunchMetadata(ctx, LaunchMetadataOptions{
		Env: opts.Env, CWD: opts.CWD, PrincipalID: opts.PrincipalID, Repo: opts.Repo,
		GitRemote: opts.GitRemote, GitBranch: opts.GitBranch, GitSHA: opts.GitSHA,
		HostID: opts.HostID, DeviceID: opts.DeviceID,
	})
	if err != nil {
		return LaunchResult{}, err
	}
	cfg, err := prepareLaunchConfig(opts, meta)
	if err != nil {
		return LaunchResult{}, err
	}
	if cfg.AgentdListener != nil {
		defer func() { _ = cfg.AgentdListener.Close() }()
	}
	claudePath, err := resolveClaudePath(opts)
	if err != nil && !opts.DryRun && !opts.NoLaunch {
		return LaunchResult{}, err
	}
	if err := rejectSettingsOverride(opts.ClaudeArgs); err != nil {
		return LaunchResult{}, err
	}
	tempRoot, cleanup, err := prepareLaunchTempRoot(opts.TempDir)
	if err != nil {
		return LaunchResult{}, err
	}
	defer cleanup()
	if cfg.StateDir == "" {
		cfg.StateDir = filepath.Join(tempRoot, "agentd-state")
	}
	cfg.StateDir, err = safeexec.NormalizeDir(cfg.StateDir, nil)
	if err != nil {
		return LaunchResult{}, fmt.Errorf("normalize agentd state dir: %w", err)
	}
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return LaunchResult{}, fmt.Errorf("create agentd state dir: %w", err)
	}
	_ = os.Chmod(cfg.StateDir, 0o700)
	agentd, err := startLaunchAgentd(ctx, cfg, opts, meta, stderr)
	if err != nil {
		return LaunchResult{}, err
	}
	defer agentd.stop()
	if err := waitForAgentdReady(ctx, cfg.AgentdURL, agentd.done); err != nil {
		return LaunchResult{}, err
	}
	state, err := waitForLaunchStateOrAgentdExit(ctx, cfg.StateDir, defaultLaunchSessionStateWait, agentd.done)
	if err != nil {
		return LaunchResult{}, err
	}
	settingsPath, settingsJSON, err := writeLaunchSettings(tempRoot, cfg, meta, state)
	if err != nil {
		return LaunchResult{}, err
	}
	result := cfg.result(meta, state, settingsPath, claudePath, opts)
	result.SettingsJSON = settingsJSON
	verboseLaunchResult(stderr, result, opts.Verbose)
	if opts.DryRun || opts.NoLaunch {
		_, _ = stdout.Write(nil)
		return result, nil
	}
	exitCode, err := runClaudeProcess(ctx, cfg, opts, meta, state, settingsPath, claudePath)
	result.ExitCode = exitCode
	return result, err
}

type launchConfig struct {
	Gateway             string
	APIKey              string
	TenantID            string
	PolicyMode          string
	ApprovalWaitTimeout time.Duration
	AgentdPath          string
	AgentdURL           string
	AgentdListener      net.Listener
	HookNonce           string
	HookCommand         string
	StateDir            string
	DashboardURL        string
	Env                 []string
	CACertPath          string
}

func prepareLaunchConfig(opts LaunchOptions, meta LaunchMetadata) (launchConfig, error) {
	if err := validateLaunchRequired(opts, meta); err != nil {
		return launchConfig{}, err
	}
	nonce, err := GenerateLauncherNonce()
	if err != nil {
		return launchConfig{}, err
	}
	agentdPath, err := resolveExecutable(opts.AgentdPath, defaultAgentdExecutable)
	if err != nil {
		return launchConfig{}, err
	}
	// Resolve hook command to an absolute path so Claude Code's bash hook
	// runner can exec it without consulting $PATH (Claude spawns hooks via
	// /usr/bin/bash -c "<command> claude pre-tool-use", and bash's PATH at
	// that point is whatever Claude inherited — typically does NOT include
	// our ./bin/. Bare-name hook command produces "command not found"). The
	// resolver checks PATH first, then siblings of cordumctl.
	hookCommand, err := resolveExecutable(opts.HookCommand, defaultHookExecutable)
	if err != nil {
		return launchConfig{}, fmt.Errorf("hook command: %w", err)
	}
	agentdURL := strings.TrimSpace(opts.AgentdURL)
	var agentdListener net.Listener
	if agentdURL == "" {
		if supportsAgentdListenerInheritance() {
			agentdURL, agentdListener, err = reserveLoopbackHookListener()
			if err != nil {
				return launchConfig{}, err
			}
		} else {
			agentdURL, err = reserveLoopbackHookURLLegacy()
			if err != nil {
				return launchConfig{}, err
			}
		}
	}
	policy := strings.TrimSpace(opts.PolicyMode)
	if policy == "" {
		policy = "enforce"
	}
	wait := opts.ApprovalWaitTimeout
	if wait <= 0 {
		wait = 30 * time.Second
	}
	stateDir := strings.TrimSpace(opts.StateDir)
	if stateDir != "" {
		stateDir, err = safeexec.NormalizeDir(stateDir, nil)
		if err != nil {
			if agentdListener != nil {
				_ = agentdListener.Close()
			}
			return launchConfig{}, fmt.Errorf("state dir: %w", err)
		}
	}
	return launchConfig{
		Gateway: strings.TrimRight(strings.TrimSpace(opts.Gateway), "/"), APIKey: strings.TrimSpace(opts.APIKey),
		TenantID: strings.TrimSpace(opts.TenantID), PolicyMode: policy, ApprovalWaitTimeout: wait,
		AgentdPath: agentdPath, AgentdURL: agentdURL, AgentdListener: agentdListener, HookNonce: nonce,
		HookCommand: hookCommand, StateDir: stateDir,
		DashboardURL: strings.TrimSpace(opts.DashboardURL), Env: opts.Env,
		CACertPath: strings.TrimSpace(opts.CACertPath),
	}, nil
}

func validateLaunchRequired(opts LaunchOptions, meta LaunchMetadata) error {
	var missing []string
	if strings.TrimSpace(opts.Gateway) == "" {
		missing = append(missing, "gateway")
	}
	if strings.TrimSpace(opts.APIKey) == "" {
		missing = append(missing, "api-key")
	}
	if strings.TrimSpace(opts.TenantID) == "" {
		missing = append(missing, "tenant")
	}
	if strings.TrimSpace(meta.PrincipalID) == "" {
		missing = append(missing, "principal")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required edge claude metadata: %s", strings.Join(missing, ", "))
	}
	return nil
}
