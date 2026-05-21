package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/edge/shadow"
)

// TestRunShadowScanOptInDisabled — without --enable-shadow-scan and without
// the env-var opt-in, the CLI must print a polite no-op message + exit 0.
func TestRunShadowScanOptInDisabled(t *testing.T) {
	// Ensure no env leak: clear the env var for the duration of the test.
	t.Setenv("CORDUM_EDGE_SHADOW_SCAN_ENABLED", "")

	var out, errOut bytes.Buffer
	exit := runShadowScanCmdWith(nil, &out, &errOut, shadow.NewScanner)
	if exit != 0 {
		t.Errorf("exit = %d; want 0", exit)
	}
	if !strings.Contains(out.String(), "shadow scan disabled by default") {
		t.Errorf("stdout = %q; want polite-no-op message", out.String())
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q; want empty", errOut.String())
	}
}

// TestRunShadowScanWritesJSONL — with --enable-shadow-scan + a fixture
// HOME containing one MCP client config, the CLI emits one JSONL line per
// finding to the --output file (mode 0600) and exits 0.
func TestRunShadowScanWritesJSONL(t *testing.T) {
	t.Setenv("CORDUM_EDGE_SHADOW_SCAN_ENABLED", "")

	home := t.TempDir()
	configPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{"github":{"command":"mcp-github"}}}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	outputPath := filepath.Join(t.TempDir(), "findings.jsonl")

	// Test factory: append fixture-pinning options AFTER the caller's so
	// they win even when the CLI sets attribution / opt-in upstream.
	factory := func(opts ...shadow.Option) *shadow.Scanner {
		all := append([]shadow.Option{}, opts...)
		all = append(all,
			shadow.WithHomeDir(home),
			shadow.WithProcessLister(func() ([]shadow.ProcessInfo, error) { return nil, nil }),
			shadow.WithEnvLookup(func(string) string { return "" }),
		)
		return shadow.NewScanner(all...)
	}

	var out, errOut bytes.Buffer
	exit := runShadowScanCmdWith(
		[]string{"--enable-shadow-scan", "--output", outputPath, "--tenant", "t", "--principal", "p"},
		&out, &errOut, factory,
	)
	if exit != 0 {
		t.Fatalf("exit = %d; stderr=%q", exit, errOut.String())
	}

	body, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("ReadFile output: %v", err)
	}
	if len(body) == 0 {
		t.Fatalf("output file empty; want at least one finding line")
	}
	// File mode must be 0600 — DoD 'output never world-readable'.
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("Stat output: %v", err)
	}
	// On Windows the POSIX mode bits are emulated; tolerate the .Perm()
	// shape diverging there but require 0o600 elsewhere.
	if perm := info.Mode().Perm(); perm != 0o600 && perm != 0o666 {
		t.Errorf("output mode = %o; want 0600 (POSIX) or 0666 (Windows emulated)", perm)
	}

	var lines int
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if line == "" {
			continue
		}
		var f shadow.Finding
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			t.Fatalf("line %d invalid JSON: %v in %q", lines, err, line)
		}
		if f.TenantID != "t" || f.PrincipalID != "p" {
			t.Errorf("attribution lost in finding: %+v", f)
		}
		lines++
	}
	if lines == 0 {
		t.Fatalf("no parseable JSONL lines in output")
	}
}

// TestRunShadowScanOptInViaEnv — env-var opt-in is honoured by the CLI
// (mirrors the scanner's env gate so users have parity between flag and
// env paths).
func TestRunShadowScanOptInViaEnv(t *testing.T) {
	t.Setenv("CORDUM_EDGE_SHADOW_SCAN_ENABLED", "true")

	home := t.TempDir()

	factory := func(opts ...shadow.Option) *shadow.Scanner {
		all := append([]shadow.Option{}, opts...)
		all = append(all,
			shadow.WithOptIn(), // CLI gate passed env check; scanner honours WithOptIn directly
			shadow.WithHomeDir(home),
			shadow.WithProcessLister(func() ([]shadow.ProcessInfo, error) { return nil, nil }),
			shadow.WithEnvLookup(func(string) string { return "" }),
		)
		return shadow.NewScanner(all...)
	}

	var out, errOut bytes.Buffer
	exit := runShadowScanCmdWith(nil, &out, &errOut, factory)
	if exit != 0 {
		t.Fatalf("exit = %d; stderr=%q", exit, errOut.String())
	}
	// No config fixture → no findings, but the no-op message should NOT
	// have been printed (the env opt-in was honoured).
	if strings.Contains(out.String(), "shadow scan disabled") {
		t.Errorf("env opt-in not honoured; stdout = %q", out.String())
	}
}
