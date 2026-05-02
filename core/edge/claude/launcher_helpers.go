package claude

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

func prepareLaunchTempRoot(parent string) (string, func(), error) {
	root, err := os.MkdirTemp(strings.TrimSpace(parent), "cordum-edge-claude-*")
	if err != nil {
		return "", nil, fmt.Errorf("create launcher temp dir: %w", err)
	}
	_ = os.Chmod(root, 0o700)
	return root, func() { _ = os.RemoveAll(root) }, nil
}

func reserveLoopbackHookURL() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("reserve loopback agentd port: %w", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return "http://" + addr + "/v1/edge/hooks/claude", nil
}

func resolveClaudePath(opts LaunchOptions) (string, error) {
	if strings.TrimSpace(opts.ClaudePath) == "" && (opts.DryRun || opts.NoLaunch) {
		if path, err := exec.LookPath(defaultClaudeExecutable); err == nil {
			return path, nil
		}
		return "", nil
	}
	return resolveExecutable(opts.ClaudePath, defaultClaudeExecutable)
}

func resolveExecutable(explicit, fallback string) (string, error) {
	if strings.TrimSpace(explicit) == "" {
		path, err := exec.LookPath(fallback)
		if err != nil {
			return "", fmt.Errorf("%s binary not found on PATH: %w", fallback, err)
		}
		return path, nil
	}
	info, err := os.Stat(explicit)
	if err != nil {
		return "", fmt.Errorf("%s binary not found at %s: %w", fallback, explicit, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s binary path is a directory: %s", fallback, explicit)
	}
	return explicit, nil
}

func endpointHost(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid agentd url: %w", err)
	}
	if strings.TrimSpace(u.Host) == "" {
		return "", fmt.Errorf("agentd url missing host: %s", raw)
	}
	return u.Host, nil
}

func dialLoopback(host string) error {
	conn, err := net.DialTimeout("tcp", host, 200*time.Millisecond)
	if err != nil {
		return err
	}
	return conn.Close()
}

func rejectSettingsOverride(args []string) error {
	for _, arg := range args {
		if arg == "--settings" || strings.HasPrefix(arg, "--settings=") {
			return fmt.Errorf("refusing claude --settings override; Cordum supplies temporary governed settings")
		}
	}
	return nil
}

func launchWriters(opts LaunchOptions) (io.Writer, io.Writer) {
	stdout, stderr := opts.Stdout, opts.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	return stdout, stderr
}

func verboseLaunchResult(w io.Writer, result LaunchResult, verbose bool) {
	if !verbose || w == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "cordum edge claude: agentd=%s settings=%s session=%s dashboard=%s\n",
		result.AgentdURL, result.SettingsPath, result.SessionID, result.DashboardURL)
}

func mergeEnv(base []string, overrides map[string]string) []string {
	env := envSliceMap(base)
	for key, value := range overrides {
		if strings.TrimSpace(value) != "" {
			env[key] = value
		}
	}
	out := make([]string, 0, len(env))
	for key, value := range env {
		out = append(out, key+"="+value)
	}
	return out
}

func envSliceMap(values []string) map[string]string {
	if len(values) == 0 {
		values = os.Environ()
	}
	out := make(map[string]string, len(values))
	for _, value := range values {
		key, val, ok := strings.Cut(value, "=")
		if ok {
			out[key] = val
		}
	}
	return out
}

func gitOutput(ctx context.Context, cwd string, args ...string) string {
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "git", append([]string{"-C", cwd}, args...)...)
	data, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" && trimmed != "HEAD" {
			return trimmed
		}
	}
	return ""
}

func derivedDashboardURL(gateway, sessionID string) string {
	if strings.TrimSpace(gateway) == "" || strings.TrimSpace(sessionID) == "" {
		return ""
	}
	return strings.TrimRight(gateway, "/") + "/edge/sessions/" + url.PathEscape(sessionID)
}
