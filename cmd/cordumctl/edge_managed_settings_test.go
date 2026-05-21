package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/edge/claude"
)

const (
	testManagedMCPGatewayURL = "https://mcp.cordum.example/mcp"
	testManagedLLMProxyURL   = "https://llm-proxy.cordum.example"
	testManagedAPIKeyHelper  = "/opt/cordum/bin/cordum-agentd claude api-key-helper"
)

func TestManagedSettingsExportWritesBothFiles(t *testing.T) {
	dir := t.TempDir()
	code, stdout, stderr := runManagedSettingsForTest(t, exportArgs(dir)...)
	if code != 0 {
		t.Fatalf("export exit=%d stderr=%s stdout=%s", code, stderr, stdout)
	}
	for _, name := range []string{"managed-settings.json", "managed-mcp.json"} {
		path := filepath.Join(dir, name)
		if !strings.Contains(stdout, "wrote "+filepath.ToSlash(path)) && !strings.Contains(stdout, "wrote "+path) {
			t.Fatalf("stdout missing wrote line for %s; got %q", name, stdout)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if info.Size() == 0 {
			t.Fatalf("%s is empty", path)
		}
	}
}

func TestManagedSettingsExportRefusesOverwriteWithoutForce(t *testing.T) {
	dir := t.TempDir()
	if code, _, stderr := runManagedSettingsForTest(t, exportArgs(dir)...); code != 0 {
		t.Fatalf("first export failed: code=%d stderr=%s", code, stderr)
	}
	code, _, stderr := runManagedSettingsForTest(t, exportArgs(dir)...)
	if code != 2 {
		t.Fatalf("second export expected exit 2 (refuse-overwrite); got %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "refusing to overwrite") {
		t.Fatalf("stderr missing refuse-overwrite message; got %q", stderr)
	}
}

func TestManagedSettingsExportAllowsOverwriteWithForce(t *testing.T) {
	dir := t.TempDir()
	if code, _, stderr := runManagedSettingsForTest(t, exportArgs(dir)...); code != 0 {
		t.Fatalf("first export failed: code=%d stderr=%s", code, stderr)
	}
	args := append(exportArgs(dir), "--force")
	code, _, stderr := runManagedSettingsForTest(t, args...)
	if code != 0 {
		t.Fatalf("force overwrite expected exit 0; got %d stderr=%s", code, stderr)
	}
}

func TestWriteManagedSettingsOutput_ForceUsesAtomicReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "managed-settings.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("write existing managed settings: %v", err)
	}

	if err := writeManagedSettingsOutput(path, []byte("new"), true); err != nil {
		t.Fatalf("force write managed settings: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rewritten managed settings: %v", err)
	}
	if string(data) != "new" {
		t.Fatalf("rewritten content = %q, want %q", data, "new")
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, ".managed-settings-*.tmp"))
	if err != nil {
		t.Fatalf("glob managed-settings temp files: %v", err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("force write left temp files: %v", leftovers)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat rewritten managed settings: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("rewritten mode = %#o, want 0600", got)
		}
	}
}

func TestManagedSettingsExportRejectsSensitiveHookCommand(t *testing.T) {
	dir := t.TempDir()
	args := exportArgs(dir)
	args = replaceFlag(args, "--hook-command", "/opt/cordum/bin/cordum-hook --token sk-test-not-real-secret")
	code, _, stderr := runManagedSettingsForTest(t, args...)
	if code != 2 {
		t.Fatalf("sensitive hook command expected exit 2; got %d stderr=%s", code, stderr)
	}
	if strings.Contains(stderr, "sk-test-not-real-secret") {
		t.Fatalf("stderr leaked the literal sensitive value: %q", stderr)
	}
	if !strings.Contains(stderr, "sensitive") {
		t.Fatalf("stderr missing sensitive-rejection message; got %q", stderr)
	}
}

func TestManagedSettingsExportRequiresMandatoryFlags(t *testing.T) {
	cases := []struct {
		name   string
		remove string
	}{
		{name: "missing_output", remove: "--output"},
		{name: "missing_mcp_gateway_url", remove: "--mcp-gateway-url"},
		{name: "missing_llm_proxy_base_url", remove: "--llm-proxy-base-url"},
		{name: "missing_api_key_helper_command", remove: "--api-key-helper-command"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			args := removeFlag(exportArgs(dir), tc.remove)
			code, _, stderr := runManagedSettingsForTest(t, args...)
			if code != 2 {
				t.Fatalf("expected exit 2; got %d stderr=%s", code, stderr)
			}
		})
	}
}

func TestManagedSettingsVerifyPassesGolden(t *testing.T) {
	dir := t.TempDir()
	if code, _, stderr := runManagedSettingsForTest(t, exportArgs(dir)...); code != 0 {
		t.Fatalf("export prep failed: code=%d stderr=%s", code, stderr)
	}
	path := filepath.Join(dir, "managed-settings.json")
	code, stdout, stderr := runManagedSettingsForTest(t, "verify", "--path", path)
	if code != 0 {
		t.Fatalf("verify exit=%d stderr=%s stdout=%s", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "ok:") {
		t.Fatalf("stdout missing ok marker; got %q", stdout)
	}
}

func TestManagedSettingsVerifyJSONFlagEmitsEnvelope(t *testing.T) {
	dir := t.TempDir()
	if code, _, stderr := runManagedSettingsForTest(t, exportArgs(dir)...); code != 0 {
		t.Fatalf("export prep failed: code=%d stderr=%s", code, stderr)
	}
	path := filepath.Join(dir, "managed-settings.json")
	code, stdout, _ := runManagedSettingsForTest(t, "verify", "--path", path, "--json")
	if code != 0 {
		t.Fatalf("verify --json exit=%d stdout=%s", code, stdout)
	}
	var env struct {
		OK     bool `json:"ok"`
		Drifts []struct {
			Field    string `json:"field"`
			Got      string `json:"got"`
			Want     string `json:"want"`
			Severity string `json:"severity"`
		} `json:"drifts"`
		Source string `json:"source"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("decode envelope: %v; stdout=%q", err, stdout)
	}
	if !env.OK {
		t.Fatalf("envelope OK should be true for golden; got drifts=%+v", env.Drifts)
	}
	if env.Source == "" {
		t.Fatalf("envelope Source must be populated; got empty")
	}
}

func TestManagedSettingsVerifyDetectsDrift(t *testing.T) {
	dir := t.TempDir()
	if code, _, stderr := runManagedSettingsForTest(t, exportArgs(dir)...); code != 0 {
		t.Fatalf("export prep failed: code=%d stderr=%s", code, stderr)
	}
	path := filepath.Join(dir, "managed-settings.json")
	if err := flipManagedSettingsBool(path, "allowManagedHooksOnly", false); err != nil {
		t.Fatalf("inject drift: %v", err)
	}
	code, stdout, _ := runManagedSettingsForTest(t, "verify", "--path", path)
	if code != 1 {
		t.Fatalf("verify drift expected exit 1; got %d stdout=%s", code, stdout)
	}
	if !strings.Contains(stdout, "allowManagedHooksOnly") {
		t.Fatalf("stdout missing drifted field name; got %q", stdout)
	}
}

func TestManagedSettingsVerifyMissingFileExits2(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.json")
	code, _, stderr := runManagedSettingsForTest(t, "verify", "--path", missing)
	if code != 2 {
		t.Fatalf("missing-file expected exit 2; got %d stderr=%s", code, stderr)
	}
}

func TestManagedSettingsVerifyMalformedFileExits2(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(path, []byte("{ not valid"), 0o600); err != nil {
		t.Fatalf("write broken fixture: %v", err)
	}
	code, _, stderr := runManagedSettingsForTest(t, "verify", "--path", path)
	if code != 2 {
		t.Fatalf("malformed expected exit 2; got %d stderr=%s", code, stderr)
	}
}

func TestManagedSettingsUnknownSubcommandExits2(t *testing.T) {
	code, _, stderr := runManagedSettingsForTest(t, "frobnicate")
	if code != 2 {
		t.Fatalf("unknown subcommand expected exit 2; got %d stderr=%s", code, stderr)
	}
}

// TestManagedSettingsExportHookTimeoutBelowClaudeDeadline guards the
// invariant in `core/edge/claude/hook_input.go:23-25`: the agentd-internal
// CORDUM_AGENTD_HOOK_TIMEOUT MUST stay strictly below Claude Code's 5s
// per-hook deadline so agentd's internal deadline fires before Claude
// SIGKILLs the hook. The QA reopen on task-ebed169a flagged a regression
// where `cordumctl edge managed-settings export` was emitting "5s" (equal
// to the deadline) instead of reusing `claude.DefaultHookTimeout` (4.5s).
//
// The per-hook JSON `timeout` integer is bounded separately: it is the
// kill-after-N-seconds value Claude Code itself enforces, so it may equal
// ClaudeHookDeadline but never exceed it.
func TestManagedSettingsExportHookTimeoutBelowClaudeDeadline(t *testing.T) {
	dir := t.TempDir()
	code, _, stderr := runManagedSettingsForTest(t, exportArgs(dir)...)
	if code != 0 {
		t.Fatalf("export exit=%d stderr=%s", code, stderr)
	}
	data, err := os.ReadFile(filepath.Join(dir, "managed-settings.json"))
	if err != nil {
		t.Fatalf("read managed-settings.json: %v", err)
	}
	var doc struct {
		Env   map[string]string `json:"env"`
		Hooks map[string][]struct {
			Hooks []struct {
				Timeout int `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal managed-settings.json: %v", err)
	}

	// Hard invariant: agentd's internal deadline must be STRICTLY below
	// Claude Code's per-hook deadline. The QA-flagged regression
	// emitted CORDUM_AGENTD_HOOK_TIMEOUT="5s" which equals
	// ClaudeHookDeadline and lets Claude SIGKILL before agentd's
	// graceful-shutdown path runs. The fix is to thread
	// claude.DefaultHookTimeout (4.5s) through the export path.
	envTimeout := strings.TrimSpace(doc.Env["CORDUM_AGENTD_HOOK_TIMEOUT"])
	if envTimeout == "" {
		t.Fatalf("env.CORDUM_AGENTD_HOOK_TIMEOUT missing from managed-settings.json")
	}
	parsed, err := time.ParseDuration(envTimeout)
	if err != nil {
		t.Fatalf("CORDUM_AGENTD_HOOK_TIMEOUT = %q not parseable as duration: %v", envTimeout, err)
	}
	if parsed >= claude.ClaudeHookDeadline {
		t.Fatalf("CORDUM_AGENTD_HOOK_TIMEOUT = %s; must stay strictly below claude.ClaudeHookDeadline (%s) per hook_input.go:23-25 — reuse claude.DefaultHookTimeout in cmd/cordumctl/edge_managed_settings.go",
			parsed, claude.ClaudeHookDeadline)
	}
	if parsed != claude.DefaultHookTimeout {
		t.Fatalf("CORDUM_AGENTD_HOOK_TIMEOUT = %s; expected reuse of claude.DefaultHookTimeout (%s) per EDGE-150 reuse-before-build rail",
			parsed, claude.DefaultHookTimeout)
	}

	// Soft invariant on the per-hook JSON timeout integer: must be
	// positive and not exceed ClaudeHookDeadline-in-seconds (Claude
	// Code rejects integer values above its own per-hook deadline).
	deadlineSeconds := int(claude.ClaudeHookDeadline / time.Second)
	for family, entries := range doc.Hooks {
		for i, entry := range entries {
			for j, h := range entry.Hooks {
				if h.Timeout <= 0 {
					t.Fatalf("hooks[%s][%d].hooks[%d].timeout = %d, want > 0", family, i, j, h.Timeout)
				}
				if h.Timeout > deadlineSeconds {
					t.Fatalf("hooks[%s][%d].hooks[%d].timeout = %d s; must not exceed claude.ClaudeHookDeadline (%d s)",
						family, i, j, h.Timeout, deadlineSeconds)
				}
			}
		}
	}
}

func runManagedSettingsForTest(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	t.Setenv("CORDUM_API_KEY", "")
	var stdout, stderr bytes.Buffer
	code := runEdgeManagedSettingsCmd(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func exportArgs(dir string) []string {
	return []string{
		"export",
		"--output", dir,
		"--mcp-gateway-url", testManagedMCPGatewayURL,
		"--llm-proxy-base-url", testManagedLLMProxyURL,
		"--api-key-helper-command", testManagedAPIKeyHelper,
		"--hook-command", "/opt/cordum/bin/cordum-hook",
	}
}

func flipManagedSettingsBool(path, key string, value bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return err
	}
	doc[key] = value
	patched, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(patched, '\n'), 0o600)
}
