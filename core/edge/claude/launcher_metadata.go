package claude

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/cordum/cordum/core/edge/safeexec"
)

// LaunchMetadata is the local machine/repository identity sent to agentd.
type LaunchMetadata struct {
	PrincipalID string
	CWD         string
	Repo        string
	GitRemote   string
	GitBranch   string
	GitSHA      string
	HostID      string
	DeviceID    string
}

type LaunchMetadataOptions struct {
	Env         []string
	CWD         string
	PrincipalID string
	Repo        string
	GitRemote   string
	GitBranch   string
	GitSHA      string
	HostID      string
	DeviceID    string
}

type launchSessionState struct {
	SessionID    string `json:"session_id"`
	ExecutionID  string `json:"execution_id"`
	DashboardURL string `json:"dashboard_url"`
}

// GenerateLauncherNonce returns a base64-encoded 32-byte nonce for agentd.
func GenerateLauncherNonce() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate launcher nonce: %w", err)
	}
	return base64.StdEncoding.EncodeToString(buf[:]), nil
}

func ResolveLaunchMetadata(ctx context.Context, opts LaunchMetadataOptions) (LaunchMetadata, error) {
	env := envSliceMap(opts.Env)
	cwd := firstNonEmpty(opts.CWD, env["CORDUM_EDGE_CWD"])
	if cwd == "" {
		got, err := os.Getwd()
		if err != nil {
			return LaunchMetadata{}, fmt.Errorf("resolve cwd: %w", err)
		}
		cwd = got
	}
	abs, err := filepath.Abs(cwd)
	if err == nil {
		cwd = abs
	}
	host, _ := os.Hostname()
	return LaunchMetadata{
		PrincipalID: firstNonEmpty(opts.PrincipalID, env["CORDUM_PRINCIPAL_ID"], env["CORDUM_EDGE_PRINCIPAL_ID"], env["USER"], env["USERNAME"], "unknown"),
		CWD:         cwd,
		Repo:        firstNonEmpty(opts.Repo, env["CORDUM_EDGE_REPO"], gitOutput(ctx, cwd, "rev-parse", "--show-toplevel"), filepath.Base(cwd)),
		GitRemote:   firstNonEmpty(opts.GitRemote, env["CORDUM_EDGE_GIT_REMOTE"], gitOutput(ctx, cwd, "config", "--get", "remote.origin.url")),
		GitBranch:   firstNonEmpty(opts.GitBranch, env["CORDUM_EDGE_GIT_BRANCH"], gitOutput(ctx, cwd, "rev-parse", "--abbrev-ref", "HEAD")),
		GitSHA:      firstNonEmpty(opts.GitSHA, env["CORDUM_EDGE_GIT_SHA"], gitOutput(ctx, cwd, "rev-parse", "HEAD")),
		HostID:      firstNonEmpty(opts.HostID, env["CORDUM_EDGE_HOST_ID"], host),
		DeviceID:    firstNonEmpty(opts.DeviceID, env["CORDUM_EDGE_DEVICE_ID"], host),
	}, nil
}

func (c launchConfig) agentdEnv(meta LaunchMetadata) []string {
	return mergeEnv(c.Env, map[string]string{
		"CORDUM_GATEWAY":                             c.Gateway,
		"CORDUM_API_KEY":                             c.APIKey,
		"CORDUM_TENANT_ID":                           c.TenantID,
		"CORDUM_PRINCIPAL_ID":                        meta.PrincipalID,
		"CORDUM_EDGE_PRINCIPAL_ID":                   meta.PrincipalID,
		"CORDUM_EDGE_REPO":                           meta.Repo,
		"CORDUM_EDGE_GIT_REMOTE":                     meta.GitRemote,
		"CORDUM_EDGE_GIT_BRANCH":                     meta.GitBranch,
		"CORDUM_EDGE_GIT_SHA":                        meta.GitSHA,
		"CORDUM_EDGE_HOST_ID":                        meta.HostID,
		"CORDUM_EDGE_DEVICE_ID":                      meta.DeviceID,
		"CORDUM_EDGE_POLICY_MODE":                    c.PolicyMode,
		"CORDUM_AGENTD_SOCKET":                       c.AgentdURL,
		"CORDUM_AGENTD_NONCE":                        c.HookNonce,
		"CORDUM_AGENTD_STATE_DIR":                    c.StateDir,
		"CORDUM_AGENTD_INLINE_APPROVAL_WAIT":         "true",
		"CORDUM_AGENTD_INLINE_APPROVAL_WAIT_TIMEOUT": durationForEnv(c.ApprovalWaitTimeout),
		"CORDUM_TLS_CA":                              c.CACertPath,
	})
}

func (c launchConfig) claudeEnv(meta LaunchMetadata, state launchSessionState) []string {
	return mergeEnv(c.Env, map[string]string{
		"CORDUM_AGENTD_URL":                 c.AgentdURL,
		"CORDUM_AGENTD_HOOK_NONCE":          c.HookNonce,
		"CORDUM_TENANT_ID":                  c.TenantID,
		"CORDUM_EDGE_PRINCIPAL_ID":          meta.PrincipalID,
		"CORDUM_EDGE_SESSION_ID":            state.SessionID,
		"CORDUM_EDGE_EXECUTION_ID":          state.ExecutionID,
		"CORDUM_EDGE_MODE":                  c.PolicyMode,
		"CORDUM_EDGE_APPROVAL_WAIT_TIMEOUT": durationForEnv(c.ApprovalWaitTimeout),
		"CORDUM_AGENTD_HOOK_TIMEOUT":        durationForEnv(DefaultHookTimeout),
	})
}

func (c launchConfig) result(meta LaunchMetadata, state launchSessionState, settingsPath, claudePath string, opts LaunchOptions) LaunchResult {
	dashboard := firstNonEmpty(state.DashboardURL, c.DashboardURL, derivedDashboardURL(c.Gateway, state.SessionID))
	return LaunchResult{
		Gateway: c.Gateway, APIKeyConfigured: c.APIKey != "", TenantID: c.TenantID,
		PrincipalID: meta.PrincipalID, CWD: meta.CWD, Repo: meta.Repo, GitRemote: meta.GitRemote,
		GitBranch: meta.GitBranch, GitSHA: meta.GitSHA, HostID: meta.HostID, DeviceID: meta.DeviceID,
		PolicyMode: c.PolicyMode, ApprovalWaitTimeout: durationForEnv(c.ApprovalWaitTimeout),
		AgentdPath: c.AgentdPath, AgentdURL: c.AgentdURL, ClaudePath: claudePath,
		SettingsPath: settingsPath, StateDir: c.StateDir, SessionID: state.SessionID,
		ExecutionID: state.ExecutionID, DashboardURL: dashboard, DryRun: opts.DryRun,
		NoLaunch: opts.NoLaunch, Metadata: map[string]string{"platform": runtime.GOOS},
	}
}

func writeLaunchSettings(root string, cfg launchConfig, meta LaunchMetadata, state launchSessionState) (string, []byte, error) {
	root, err := safeexec.NormalizeDir(root, nil)
	if err != nil {
		return "", nil, fmt.Errorf("normalize temporary Claude settings dir: %w", err)
	}
	settings, err := GenerateDevSettingsJSON(DevSettingsOptions{
		SessionID: state.SessionID, ExecutionID: state.ExecutionID, AgentdURL: cfg.AgentdURL,
		AgentdHookNonce: cfg.HookNonce, HookCommand: cfg.HookCommand, HookTimeout: DefaultHookTimeout,
		PolicyMode: cfg.PolicyMode, ApprovalWaitTimeout: cfg.ApprovalWaitTimeout, Platform: runtime.GOOS,
		ExtraEnv: map[string]string{"CORDUM_TENANT_ID": cfg.TenantID, "CORDUM_EDGE_PRINCIPAL_ID": meta.PrincipalID},
	})
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(root, "settings.json")
	if err := safeexec.ValidateArgPaths([]string{path}, "", []string{root}); err != nil {
		return "", nil, fmt.Errorf("validate temporary Claude settings path: %w", err)
	}
	if err := os.WriteFile(path, settings, 0o600); err != nil {
		return "", nil, fmt.Errorf("write temporary Claude settings: %w", err)
	}
	return path, settings, nil
}

func waitForLaunchState(ctx context.Context, dir string, timeout time.Duration) (launchSessionState, error) {
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if state, ok := readFirstLaunchState(dir); ok {
			return state, nil
		}
		select {
		case <-deadline.Done():
			return launchSessionState{}, fmt.Errorf("timed out waiting for agentd session state in %s", dir)
		case <-ticker.C:
		}
	}
}

func readFirstLaunchState(dir string) (launchSessionState, bool) {
	matches, _ := filepath.Glob(filepath.Join(dir, "*", "state.json"))
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var state launchSessionState
		if json.Unmarshal(data, &state) == nil && strings.TrimSpace(state.SessionID) != "" {
			return state, true
		}
	}
	return launchSessionState{}, false
}
