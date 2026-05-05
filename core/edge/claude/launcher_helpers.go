package claude

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/cordum/cordum/core/edge/safeexec"
)

// siblingExecutable returns an absolute path to a binary named `name` that
// lives in the same directory as the running cordumctl (or whichever process
// the launcher is hosted in). Returns ok=false if the binary is not found.
// Honors Windows .exe convention. This lets `./bin/cordumctl edge claude`
// resolve `./bin/cordum-agentd(.exe)` and `./bin/cordum-hook(.exe)` without
// requiring the user to add `./bin` to PATH or set --agentd-path explicitly.
func siblingExecutable(name string) (string, bool) {
	self, err := os.Executable()
	if err != nil {
		return "", false
	}
	dir := filepath.Dir(self)
	candidates := []string{name}
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		candidates = []string{name + ".exe", name}
	}
	for _, candidate := range candidates {
		full := filepath.Join(dir, candidate)
		if info, statErr := os.Stat(full); statErr == nil && !info.IsDir() {
			return full, true
		}
	}
	return "", false
}

func prepareLaunchTempRoot(parent string) (string, func(), error) {
	parent = strings.TrimSpace(parent)
	if parent != "" {
		normalized, err := safeexec.NormalizeDir(parent, nil)
		if err != nil {
			return "", nil, fmt.Errorf("normalize launcher temp dir: %w", err)
		}
		parent = normalized
	}
	root, err := os.MkdirTemp(parent, "cordum-edge-claude-*")
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
		if path, err := exec.LookPath(fallback); err == nil {
			abs, absErr := filepath.Abs(path)
			if absErr != nil {
				return path, nil
			}
			return abs, nil
		}
		if sibling, ok := siblingExecutable(fallback); ok {
			return sibling, nil
		}
		return "", fmt.Errorf("%s binary not found on PATH or alongside cordumctl", fallback)
	}
	candidates := executableCandidates(explicit)
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", fmt.Errorf("%s binary not found at %s: %w", fallback, explicit, err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("%s binary path is a directory: %s", fallback, candidate)
		}
		// Always return absolute. Settings.json hook commands are exec'd by
		// Claude Code's bash sub-shell which has whatever PATH it inherited;
		// a relative candidate (e.g. "cordum-hook.exe" found in CWD) becomes
		// "command not found" once bash chdir's elsewhere. filepath.Abs is
		// idempotent on already-absolute paths.
		if abs, absErr := filepath.Abs(candidate); absErr == nil {
			return abs, nil
		}
		return candidate, nil
	}
	// Bare-name with no path separator: try PATH then sibling-of-self before
	// giving up. Lets cordum.yaml say `hook_command: cordum-hook` and have
	// the wrapper resolve it from ./bin/ next to cordumctl automatically.
	if !strings.ContainsAny(explicit, `/\`) {
		if path, err := exec.LookPath(explicit); err == nil {
			if abs, absErr := filepath.Abs(path); absErr == nil {
				return abs, nil
			}
			return path, nil
		}
		if sibling, ok := siblingExecutable(explicit); ok {
			return sibling, nil
		}
	}
	return "", fmt.Errorf("%s binary not found at %s (also tried %s extension)", fallback, explicit, runtime.GOOS+"-default")
}

// executableCandidates returns the explicit path plus, on Windows, the path
// with `.exe` appended if it is missing. Windows users often invoke
// `cordumctl edge claude --hook-command .\bin\cordum-hook`; the actual
// binary on disk is `.\bin\cordum-hook.exe`. Pre-EDGE-045, `os.Stat` failed
// on the no-extension form and the launcher errored out. Post-fix, we try
// the no-extension path first (in case the user really does have a
// no-extension binary), then fall back to `.exe`. Non-Windows hosts return
// just the explicit path.
func executableCandidates(explicit string) []string {
	out := []string{explicit}
	if runtime.GOOS != "windows" {
		return out
	}
	if strings.EqualFold(filepathExt(explicit), ".exe") {
		return out
	}
	return append(out, explicit+".exe")
}

// filepathExt returns the lowercase extension of path including the leading
// dot, or empty string if there is no extension. Avoids importing path/filepath
// just for Ext to keep the dep surface flat.
func filepathExt(path string) string {
	for i := len(path) - 1; i >= 0 && path[i] != '/' && path[i] != '\\'; i-- {
		if path[i] == '.' {
			return path[i:]
		}
	}
	return ""
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
	result, err := safeexec.RunCapture(runCtx, "git", append([]string{"-C", cwd}, args...), nil, safeexec.Options{
		MaxStdoutBytes: 64 << 10,
		MaxStderrBytes: 16 << 10,
	})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(result.Stdout))
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
