package claude

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLaunchEdgeClaudeDryRunStartsAgentdAndOmitsNonceFromSettings(t *testing.T) {
	helperPath := os.Args[0]
	capture := filepath.Join(t.TempDir(), "agentd-env.json")

	result, err := LaunchEdgeClaude(context.Background(), LaunchOptions{
		Env:     envWith("CORDUM_TEST_HELPER_PROCESS", "1", "CORDUM_TEST_AGENTD_ENV_PATH", capture),
		Gateway: "http://gateway.local", APIKey: "secret-token", TenantID: "tenant-a",
		PrincipalID: "user-a", CWD: t.TempDir(), AgentdPath: helperPath, HookCommand: helperPath,
		ClaudePath: helperPath, PolicyMode: "enforce", DryRun: true,
	})
	if err != nil {
		t.Fatalf("LaunchEdgeClaude returned error: %v", err)
	}
	if result.SessionID != "sess-launch" || result.ExecutionID != "exec-launch" {
		t.Fatalf("unexpected session evidence: %#v", result)
	}
	env := readJSONMap(t, capture)
	nonce := env["CORDUM_AGENTD_NONCE"]
	assertLauncherNonce(t, nonce)
	if strings.Contains(env["CORDUM_AGENTD_SOCKET"], "nonce=") {
		t.Fatalf("agentd URL leaked nonce in argv/env URL: %s", env["CORDUM_AGENTD_SOCKET"])
	}
	assertSettingsOmitsRuntimeNonce(t, result.SettingsJSON, nonce)
	if _, err := os.Stat(result.SettingsPath); !os.IsNotExist(err) {
		t.Fatalf("temporary settings path should be cleaned up, stat err=%v", err)
	}
}

func TestLaunchEdgeClaudeRunsClaudeAndPropagatesExitCode(t *testing.T) {
	helperPath := os.Args[0]
	capture := filepath.Join(t.TempDir(), "claude.json")

	result, err := LaunchEdgeClaude(context.Background(), LaunchOptions{
		Env:     envWith("CORDUM_TEST_HELPER_PROCESS", "1", "CORDUM_TEST_CLAUDE_CAPTURE", capture, "CORDUM_TEST_CLAUDE_EXIT", "7"),
		Gateway: "http://gateway.local", APIKey: "secret-token", TenantID: "tenant-a",
		PrincipalID: "user-a", CWD: t.TempDir(), AgentdPath: helperPath, HookCommand: helperPath,
		ClaudePath: helperPath, PolicyMode: "enforce", ClaudeArgs: []string{"--print", "hello"},
	})
	if err != nil {
		t.Fatalf("LaunchEdgeClaude returned error: %v", err)
	}
	if result.ExitCode != 7 {
		t.Fatalf("exit code = %d, want 7", result.ExitCode)
	}
	got := readJSONMap(t, capture)
	if got["env_CORDUM_AGENTD_HOOK_NONCE"] == "" {
		t.Fatalf("claude env missing runtime hook nonce: %#v", got)
	}
	if strings.Contains(got["env_CORDUM_AGENTD_URL"], "nonce=") {
		t.Fatalf("claude agentd URL leaked nonce: %s", got["env_CORDUM_AGENTD_URL"])
	}
	if !strings.Contains(got["args"], "--settings") || !strings.Contains(got["args"], "--print") {
		t.Fatalf("claude args missing governed settings or passthrough: %#v", got)
	}
	assertSettingsOmitsRuntimeNonce(t, []byte(got["settings_json"]), got["env_CORDUM_AGENTD_HOOK_NONCE"])
}

func TestLaunchEdgeClaudeMissingClaudeBinaryReturnsClearError(t *testing.T) {
	helperPath := os.Args[0]
	capture := filepath.Join(t.TempDir(), "agentd-env.json")
	missingClaude := filepath.Join(t.TempDir(), "missing-claude")

	_, err := LaunchEdgeClaude(context.Background(), LaunchOptions{
		Env:     envWith("CORDUM_TEST_HELPER_PROCESS", "1", "CORDUM_TEST_AGENTD_ENV_PATH", capture),
		Gateway: "http://gateway.local", APIKey: "secret-token", TenantID: "tenant-a",
		PrincipalID: "user-a", CWD: t.TempDir(), AgentdPath: helperPath, HookCommand: helperPath,
		ClaudePath: missingClaude,
	})
	if err == nil || !strings.Contains(err.Error(), "claude binary not found") {
		t.Fatalf("error = %v, want clear missing claude error", err)
	}
	if _, statErr := os.Stat(capture); !os.IsNotExist(statErr) {
		t.Fatalf("agentd should not start when claude is missing, stat err=%v", statErr)
	}
}

func TestKillAgentdOnContextCancel(t *testing.T) {
	helperPath := os.Args[0]
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()

	result, _ := LaunchEdgeClaude(ctx, LaunchOptions{
		Env:     envWith("CORDUM_TEST_HELPER_PROCESS", "1", "CORDUM_TEST_CLAUDE_SLEEP_MS", "5000"),
		Gateway: "http://gateway.local", APIKey: "secret-token", TenantID: "tenant-a",
		PrincipalID: "user-a", CWD: t.TempDir(), AgentdPath: helperPath, HookCommand: helperPath,
		ClaudePath: helperPath, PolicyMode: "enforce",
	})
	if time.Since(start) > 3*time.Second {
		t.Fatalf("launcher did not return promptly after context cancellation")
	}
	if result.SettingsPath != "" {
		if _, err := os.Stat(result.SettingsPath); !os.IsNotExist(err) {
			t.Fatalf("temporary settings path should be cleaned on cancel, stat err=%v", err)
		}
	}
}

func TestLaunchEdgeClaudeAgentdEarlyExitDoesNotHang(t *testing.T) {
	helperPath := os.Args[0]
	cwd := t.TempDir()
	done := make(chan error, 1)
	go func() {
		_, err := LaunchEdgeClaude(context.Background(), LaunchOptions{
			Env:     envWith("CORDUM_TEST_HELPER_PROCESS", "1", "CORDUM_TEST_AGENTD_EXIT_EARLY", "1"),
			Gateway: "http://gateway.local", APIKey: "secret-token", TenantID: "tenant-a",
			PrincipalID: "user-a", CWD: cwd, AgentdPath: helperPath,
			HookCommand: helperPath,
			ClaudePath:  helperPath, PolicyMode: "enforce", DryRun: true,
		})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "cordum-agentd exited before becoming ready") {
			t.Fatalf("error = %v, want early agentd exit error", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("launcher hung after early agentd exit")
	}
}

func TestResolveLaunchMetadataFallbacks(t *testing.T) {
	cwd := t.TempDir()
	meta, err := ResolveLaunchMetadata(context.Background(), LaunchMetadataOptions{
		Env: []string{"USER=alice"}, CWD: cwd,
	})
	if err != nil {
		t.Fatalf("ResolveLaunchMetadata returned error: %v", err)
	}
	if meta.PrincipalID != "alice" || meta.Repo != filepath.Base(cwd) {
		t.Fatalf("unexpected fallback metadata: %#v", meta)
	}
	if meta.CWD == "" || meta.HostID == "" || meta.DeviceID == "" {
		t.Fatalf("missing cwd/host/device fallback: %#v", meta)
	}
}

func TestMain(m *testing.M) {
	if os.Getenv("CORDUM_TEST_HELPER_PROCESS") == "1" {
		if os.Getenv("CORDUM_AGENTD_NONCE") != "" {
			os.Exit(runLauncherAgentdHelper())
		}
		if os.Getenv("CORDUM_AGENTD_HOOK_NONCE") != "" {
			os.Exit(runLauncherClaudeHelper())
		}
	}
	os.Exit(m.Run())
}

func runLauncherAgentdHelper() int {
	captureEnv(os.Getenv("CORDUM_TEST_AGENTD_ENV_PATH"), map[string]string{
		"CORDUM_AGENTD_NONCE":  os.Getenv("CORDUM_AGENTD_NONCE"),
		"CORDUM_AGENTD_SOCKET": os.Getenv("CORDUM_AGENTD_SOCKET"),
		"CORDUM_API_KEY":       os.Getenv("CORDUM_API_KEY"),
	})
	if os.Getenv("CORDUM_TEST_AGENTD_EXIT_EARLY") == "1" {
		return 9
	}
	u, err := url.Parse(os.Getenv("CORDUM_AGENTD_SOCKET"))
	if err != nil {
		return 4
	}
	ln, err := net.Listen("tcp", u.Host)
	if err != nil {
		return 5
	}
	defer func() { _ = ln.Close() }()
	if err := writeLauncherState(); err != nil {
		return 6
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return 0
		}
		_ = conn.Close()
	}
}

func runLauncherClaudeHelper() int {
	settings := readSettingsArg(os.Args)
	captureEnv(os.Getenv("CORDUM_TEST_CLAUDE_CAPTURE"), map[string]string{
		"args":                         strings.Join(os.Args[1:], " "),
		"env_CORDUM_AGENTD_URL":        os.Getenv("CORDUM_AGENTD_URL"),
		"env_CORDUM_AGENTD_HOOK_NONCE": os.Getenv("CORDUM_AGENTD_HOOK_NONCE"),
		"settings_json":                settings,
	})
	if os.Getenv("CORDUM_TEST_CLAUDE_SLEEP_MS") != "" {
		sleepDuration, _ := time.ParseDuration(os.Getenv("CORDUM_TEST_CLAUDE_SLEEP_MS") + "ms")
		time.Sleep(sleepDuration)
		return 0
	}
	if os.Getenv("CORDUM_TEST_CLAUDE_EXIT") == "7" {
		return 7
	}
	return 0
}

func writeLauncherState() error {
	dir := filepath.Join(os.Getenv("CORDUM_AGENTD_STATE_DIR"), "sess-launch")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	body := `{"session_id":"sess-launch","execution_id":"exec-launch","dashboard_url":"http://dash.local/edge/sessions/sess-launch"}`
	return os.WriteFile(filepath.Join(dir, "state.json"), []byte(body), 0o600)
}

func readSettingsArg(args []string) string {
	for i, arg := range args {
		if arg == "--settings" && i+1 < len(args) {
			data, _ := os.ReadFile(args[i+1])
			return string(data)
		}
	}
	return ""
}

func captureEnv(path string, values map[string]string) {
	if path == "" {
		return
	}
	data, _ := json.Marshal(values)
	_ = os.WriteFile(path, data, 0o600)
}

func envWith(kv ...string) []string {
	env := append([]string(nil), os.Environ()...)
	for i := 0; i+1 < len(kv); i += 2 {
		env = append(env, kv[i]+"="+kv[i+1])
	}
	return env
}

func readJSONMap(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read capture %s: %v", path, err)
	}
	var out map[string]string
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("decode capture: %v", err)
	}
	return out
}

func assertLauncherNonce(t *testing.T, nonce string) {
	t.Helper()
	decoded, err := base64.StdEncoding.DecodeString(nonce)
	if err != nil || len(decoded) < 32 {
		t.Fatalf("launcher nonce is not 32-byte base64: len=%d err=%v", len(decoded), err)
	}
}

func assertSettingsOmitsRuntimeNonce(t *testing.T, settings []byte, nonce string) {
	t.Helper()
	text := string(settings)
	for _, forbidden := range []string{nonce, "CORDUM_AGENTD_HOOK_NONCE", "nonce="} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("settings leaked %q: %s", forbidden, text)
		}
	}
}
