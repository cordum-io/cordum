package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEdgeClaudeDryRunSettingsOutputRedactsSecrets(t *testing.T) {
	// Isolate config-file lookup from any local ~/.cordum/config.yaml or
	// ./cordum.yaml so the test reflects only the env vars set below.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("CORDUM_GATEWAY", "http://gateway.local")
	t.Setenv("CORDUM_API_KEY", "super-secret-token")
	t.Setenv("CORDUM_TENANT_ID", "tenant-cli")
	t.Setenv("CORDUM_PRINCIPAL_ID", "principal-cli")
	t.Setenv("CORDUMCTL_EDGE_HELPER", "1")
	capture := filepath.Join(t.TempDir(), "agentd-env.json")
	t.Setenv("CORDUMCTL_TEST_AGENTD_ENV_PATH", capture)

	var stdout, stderr bytes.Buffer
	code := runEdgeClaudeCmd([]string{
		"--dry-run",
		"--settings-output", "-",
		"--agentd-path", os.Args[0],
		"--hook-command", os.Args[0],
		"--claude-path", os.Args[0],
		"--cwd", t.TempDir(),
	}, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d stderr=%s", code, stderr.String())
	}
	for _, stream := range []string{stdout.String(), stderr.String()} {
		if strings.Contains(stream, "super-secret-token") {
			t.Fatalf("output leaked API key: stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
	}
	assertCLISettingsOutput(t, stdout.String())
	env := readCLIJSONMap(t, capture)
	assertCLINonce(t, env["CORDUM_AGENTD_NONCE"])
	if env["CORDUM_API_KEY"] != "super-secret-token" {
		t.Fatalf("agentd did not receive API key through env")
	}
	if strings.Contains(env["CORDUM_AGENTD_SOCKET"], "nonce=") {
		t.Fatalf("agentd socket URL leaked nonce: %s", env["CORDUM_AGENTD_SOCKET"])
	}
}

func TestMain(m *testing.M) {
	if os.Getenv("CORDUMCTL_EDGE_HELPER") == "1" && os.Getenv("CORDUM_AGENTD_NONCE") != "" {
		os.Exit(runCordumctlAgentdHelper())
	}
	os.Exit(m.Run())
}

func runCordumctlAgentdHelper() int {
	captureCLIEnv(os.Getenv("CORDUMCTL_TEST_AGENTD_ENV_PATH"), map[string]string{
		"CORDUM_AGENTD_NONCE":  os.Getenv("CORDUM_AGENTD_NONCE"),
		"CORDUM_AGENTD_SOCKET": os.Getenv("CORDUM_AGENTD_SOCKET"),
		"CORDUM_API_KEY":       os.Getenv("CORDUM_API_KEY"),
	})
	u, err := url.Parse(os.Getenv("CORDUM_AGENTD_SOCKET"))
	if err != nil {
		return 4
	}
	ln, err := net.Listen("tcp", u.Host)
	if err != nil {
		return 5
	}
	defer func() { _ = ln.Close() }()
	if err := writeCordumctlState(); err != nil {
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

func writeCordumctlState() error {
	dir := filepath.Join(os.Getenv("CORDUM_AGENTD_STATE_DIR"), "sess-cli")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	body := `{"session_id":"sess-cli","execution_id":"exec-cli","dashboard_url":"http://dash.local/edge/sessions/sess-cli"}`
	return os.WriteFile(filepath.Join(dir, "state.json"), []byte(body), 0o600)
}

func captureCLIEnv(path string, values map[string]string) {
	if path == "" {
		return
	}
	data, _ := json.Marshal(values)
	_ = os.WriteFile(path, data, 0o600)
}

func assertCLISettingsOutput(t *testing.T, settings string) {
	t.Helper()
	if !strings.Contains(settings, `"CORDUM_AGENTD_URL"`) {
		t.Fatalf("settings output missing agentd URL: %s", settings)
	}
	for _, forbidden := range []string{"super-secret-token", "CORDUM_AGENTD_HOOK_NONCE", "nonce="} {
		if strings.Contains(settings, forbidden) {
			t.Fatalf("settings output leaked %q: %s", forbidden, settings)
		}
	}
	if !json.Valid([]byte(settings)) {
		t.Fatalf("settings output is not JSON: %s", settings)
	}
}

func assertCLINonce(t *testing.T, nonce string) {
	t.Helper()
	decoded, err := base64.StdEncoding.DecodeString(nonce)
	if err != nil || len(decoded) < 32 {
		t.Fatalf("nonce is not 32-byte base64: len=%d err=%v", len(decoded), err)
	}
}

func readCLIJSONMap(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	var out map[string]string
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("decode capture: %v", err)
	}
	return out
}
