package shadow_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/edge/shadow"
)

// writeHomeConfig is a small fixture builder that writes a config file under
// fakeHome's per-client subdirectory and returns the full path. It centralises
// the platform-specific layout so the test cases stay readable.
func writeHomeConfig(t *testing.T, fakeHome, rel, content string) string {
	t.Helper()
	p := filepath.Join(fakeHome, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

func newOptedInScanner(t *testing.T, fakeHome string, opts ...shadow.Option) *shadow.Scanner {
	t.Helper()
	base := []shadow.Option{
		shadow.WithOptIn(),
		shadow.WithHomeDir(fakeHome),
		shadow.WithTenant("tenant-shadow-test"),
		shadow.WithPrincipal("principal-shadow-test"),
		shadow.WithHostname("test-host"),
		shadow.WithNowFn(func() time.Time { return time.Unix(1700000000, 0).UTC() }),
		// No processes / no env vars by default — individual cases override.
		shadow.WithProcessLister(func() ([]shadow.ProcessInfo, error) { return nil, nil }),
		shadow.WithEnvLookup(func(string) string { return "" }),
	}
	return shadow.NewScanner(append(base, opts...)...)
}

const sampleClaudeConfig = `{
  "mcpServers": {
    "github": {"command": "mcp-github", "args": ["--token", "ghp_redacted"]},
    "anthropic-tools": {"command": "mcp-anthropic", "transport": "http"}
  },
  "model": "claude-opus"
}`

const sampleCodexTOML = `[mcp_servers.local]
command = "mcp-local"
transport = "stdio"

[mcp_servers.remote]
command = "mcp-remote"
endpoint = "https://mcp.example/api"
`

const sampleCursorConfig = `{
  "mcpServers": {
    "filesystem": {"command": "mcp-fs"}
  }
}`

const sampleManagedConfig = `{
  "mcpServers": {"managed-fleet": {"command": "mcp-managed"}},
  "env": {"CORDUM_EDGE_MANAGED_POLICY_MODE": "enterprise-strict"}
}`

// TestScannerDetectsClaudeCodeConfig (A) — Claude Code config produces a
// product=claude-code, evidence_type=config_file finding with the expected
// summary shape and status=observed.
func TestScannerDetectsClaudeCodeConfig(t *testing.T) {
	home := t.TempDir()
	writeHomeConfig(t, home, ".claude/settings.json", sampleClaudeConfig)

	findings, err := newOptedInScanner(t, home).Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	var got *shadow.Finding
	for i := range findings {
		if findings[i].Product == "claude-code" && findings[i].EvidenceType == shadow.EvidenceConfigFile {
			got = &findings[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("no claude-code config_file finding; got %d findings: %+v", len(findings), findings)
	}
	if got.Status != shadow.StatusObserved {
		t.Errorf("Status = %q; want %q", got.Status, shadow.StatusObserved)
	}
	if got.TenantID != "tenant-shadow-test" || got.PrincipalID != "principal-shadow-test" || got.Hostname != "test-host" {
		t.Errorf("tenant/principal/host attribution lost: %+v", got)
	}
	if !strings.Contains(got.RedactedConfigSummary, "2 mcp servers") {
		t.Errorf("RedactedConfigSummary = %q; want a '2 mcp servers' fragment", got.RedactedConfigSummary)
	}
}

// TestScannerDetectsCodexConfig (B) — Codex TOML config.
func TestScannerDetectsCodexConfig(t *testing.T) {
	home := t.TempDir()
	writeHomeConfig(t, home, ".codex/config.toml", sampleCodexTOML)

	findings, err := newOptedInScanner(t, home).Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for _, f := range findings {
		if f.Product == "codex" && f.EvidenceType == shadow.EvidenceConfigFile {
			return
		}
	}
	t.Fatalf("no codex config_file finding; got %+v", findings)
}

// TestScannerDetectsCursorConfig (C) — Cursor JSON config.
func TestScannerDetectsCursorConfig(t *testing.T) {
	home := t.TempDir()
	writeHomeConfig(t, home, ".cursor/mcp.json", sampleCursorConfig)

	findings, err := newOptedInScanner(t, home).Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for _, f := range findings {
		if f.Product == "cursor" && f.EvidenceType == shadow.EvidenceConfigFile {
			return
		}
	}
	t.Fatalf("no cursor config_file finding; got %+v", findings)
}

// TestScannerSkipsManagedConfig (D) — config carrying the managed-policy
// invariant marker emits Status=managed_skip and is NOT flagged as a shadow
// observation. DoD #4 'managed config not flagged'.
func TestScannerSkipsManagedConfig(t *testing.T) {
	home := t.TempDir()
	writeHomeConfig(t, home, ".claude/settings.json", sampleManagedConfig)

	findings, err := newOptedInScanner(t, home).Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	var managed *shadow.Finding
	for i := range findings {
		if findings[i].Product == "claude-code" && findings[i].EvidenceType == shadow.EvidenceConfigFile {
			managed = &findings[i]
			break
		}
	}
	if managed == nil {
		t.Fatalf("expected a managed-skip finding for claude-code; got %+v", findings)
	}
	if managed.Status != shadow.StatusManagedSkip {
		t.Errorf("Status = %q; want %q (managed config must not be flagged as shadow)",
			managed.Status, shadow.StatusManagedSkip)
	}
}

// TestScannerDoesNotReadPrompt (E) — DoD #1 'no private content'. Any bytes
// that look like a user prompt must NOT appear in the redacted summary.
func TestScannerDoesNotReadPrompt(t *testing.T) {
	home := t.TempDir()
	const prompt = "BEGIN_PRIVATE_PROMPT_LEAK my secret diary entry END_PRIVATE_PROMPT_LEAK"
	configWithPrompt := `{"mcpServers":{"x":{"command":"foo"}},"prompt":"` + prompt + `"}`
	writeHomeConfig(t, home, ".claude/settings.json", configWithPrompt)

	findings, err := newOptedInScanner(t, home).Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for _, f := range findings {
		if strings.Contains(f.RedactedConfigSummary, "BEGIN_PRIVATE_PROMPT_LEAK") {
			t.Fatalf("finding leaked private prompt content: %q", f.RedactedConfigSummary)
		}
		if strings.Contains(f.RedactedConfigSummary, "secret diary") {
			t.Fatalf("finding leaked private prompt content: %q", f.RedactedConfigSummary)
		}
	}
}

// TestScannerDetectsClaudeProcess (F) — gopsutil seam: injected process list
// containing a 'claude' entry produces a process_name finding.
func TestScannerDetectsClaudeProcess(t *testing.T) {
	home := t.TempDir()
	scanner := newOptedInScanner(t, home,
		shadow.WithProcessLister(func() ([]shadow.ProcessInfo, error) {
			return []shadow.ProcessInfo{
				{Name: "claude", PID: 4242},
				{Name: "bash", PID: 1},
				{Name: "cursor", PID: 9999},
			}, nil
		}),
	)
	findings, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	var sawClaude, sawCursor bool
	for _, f := range findings {
		if f.EvidenceType != shadow.EvidenceProcessName {
			continue
		}
		if f.Product == "claude-code" {
			sawClaude = true
			// PID should appear in the redacted path but not as the only
			// identifying signal; verify the path contains the process name.
			if !strings.Contains(f.RedactedPath, "claude") {
				t.Errorf("claude process finding RedactedPath = %q; should mention process name", f.RedactedPath)
			}
		}
		if f.Product == "cursor" {
			sawCursor = true
		}
	}
	if !sawClaude || !sawCursor {
		t.Fatalf("expected claude+cursor process findings; got %+v", findings)
	}
}

// TestScannerOptInDisabledByDefault (G) — without opt-in, Scan returns
// ErrOptInRequired and zero findings, regardless of fixture state.
func TestScannerOptInDisabledByDefault(t *testing.T) {
	home := t.TempDir()
	writeHomeConfig(t, home, ".claude/settings.json", sampleClaudeConfig)

	scanner := shadow.NewScanner(
		shadow.WithHomeDir(home),
		shadow.WithEnvLookup(func(string) string { return "" }),
	)
	findings, err := scanner.Scan(context.Background())
	if !errors.Is(err, shadow.ErrOptInRequired) {
		t.Fatalf("expected ErrOptInRequired, got err=%v findings=%+v", err, findings)
	}
	if len(findings) != 0 {
		t.Fatalf("expected zero findings when opt-in disabled, got %d", len(findings))
	}
}

// TestScannerOptInExplicitFlag (H) — env-var opt-in is honoured just like
// the constructor option.
func TestScannerOptInExplicitFlag(t *testing.T) {
	home := t.TempDir()
	writeHomeConfig(t, home, ".claude/settings.json", sampleClaudeConfig)

	scanner := shadow.NewScanner(
		shadow.WithHomeDir(home),
		shadow.WithTenant("t"), shadow.WithPrincipal("p"), shadow.WithHostname("h"),
		shadow.WithNowFn(func() time.Time { return time.Unix(1700000000, 0).UTC() }),
		shadow.WithProcessLister(func() ([]shadow.ProcessInfo, error) { return nil, nil }),
		shadow.WithEnvLookup(func(k string) string {
			if k == "CORDUM_EDGE_SHADOW_SCAN_ENABLED" {
				return "true"
			}
			return ""
		}),
	)
	findings, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) == 0 {
		t.Fatalf("env-var opt-in did not enable scan; got 0 findings")
	}
}

// TestScannerPermissionDenied (I) — POSIX-only because the chmod 0000 model
// does not apply identically on Windows. Asserts the scanner reports the
// path as unreadable rather than crashing.
func TestScannerPermissionDenied(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission semantics not reproducible on Windows")
	}
	home := t.TempDir()
	p := writeHomeConfig(t, home, ".claude/settings.json", sampleClaudeConfig)
	if err := os.Chmod(p, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	defer func() { _ = os.Chmod(p, 0o644) }()

	findings, err := newOptedInScanner(t, home).Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan unexpectedly returned error: %v", err)
	}
	var unreadable *shadow.Finding
	for i := range findings {
		if findings[i].Product == "claude-code" && findings[i].Status == shadow.StatusUnreadable {
			unreadable = &findings[i]
			break
		}
	}
	if unreadable == nil {
		t.Fatalf("expected an unreadable finding; got %+v", findings)
	}
	if !strings.Contains(unreadable.RemediationHint, "permission") &&
		!strings.Contains(unreadable.RemediationHint, "privilege") {
		t.Errorf("RemediationHint = %q; want a permission/privilege phrase", unreadable.RemediationHint)
	}
}

// TestScannerRefusesEnforcement (J) — static-source assertion: the scanner
// package must not import os/exec or call os.Remove / os.WriteFile (apart
// from the JSONL output path, which lives in cmd/cordumctl/shadow_scan.go).
// We grep the package source for forbidden symbols.
func TestScannerRefusesEnforcement(t *testing.T) {
	pkgDir := "."
	// Walk the package directory and grep for forbidden tokens in non-_test
	// files only — tests are allowed to do anything (they don't ship).
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	forbidden := []string{
		`"os/exec"`,
		`exec.Command`,
		`os.Remove`,
		`os.WriteFile`,
		`os.Rename`,
		`os.RemoveAll`,
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(pkgDir, name))
		if err != nil {
			t.Fatalf("ReadFile %s: %v", name, err)
		}
		for _, f := range forbidden {
			if strings.Contains(string(body), f) {
				t.Errorf("%s contains forbidden enforcement symbol %q (task rail #2: no enforcement)", name, f)
			}
		}
	}
}
