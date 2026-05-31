package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// EDGE-047: `cordumctl edge init` writes ./cordum.yaml populated with the
// supplied tenant, principal, and policy_mode. api_key is written as a
// ${VAR} reference, never plaintext.
func TestEdgeInitWritesYAML(t *testing.T) {
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := runEdgeInitCmd([]string{
		"--cwd", cwd,
		"--gateway", "https://localhost:8081",
		"--tenant", "default",
		"--principal", "yaron",
		"--policy-mode", "enforce",
		"--api-key-env", "CORDUM_API_KEY",
		"--no-wrapper",
		"--non-interactive",
	}, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d stderr=%s", code, stderr.String())
	}
	body := readFile(t, filepath.Join(cwd, "cordum.yaml"))
	for _, want := range []string{
		"gateway: https://localhost:8081",
		"tenant: default",
		"principal: yaron",
		"policy_mode: enforce",
		"api_key: ${CORDUM_API_KEY}",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("cordum.yaml missing %q. body:\n%s", want, body)
		}
	}
	if strings.Contains(body, "<redacted>") {
		t.Errorf("cordum.yaml should not contain redaction marker: %s", body)
	}
}

// Re-running init must NOT overwrite an existing cordum.yaml without --force.
func TestEdgeInitRefusesOverwriteWithoutForce(t *testing.T) {
	cwd := t.TempDir()
	existing := []byte("gateway: https://prior.example\n")
	if err := os.WriteFile(filepath.Join(cwd, "cordum.yaml"), existing, 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runEdgeInitCmd([]string{
		"--cwd", cwd,
		"--gateway", "https://localhost:8081",
		"--tenant", "default",
		"--principal", "yaron",
		"--no-wrapper",
		"--non-interactive",
	}, nil, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit, got 0; stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	body := readFile(t, filepath.Join(cwd, "cordum.yaml"))
	if !strings.Contains(body, "https://prior.example") {
		t.Errorf("existing cordum.yaml was clobbered: %s", body)
	}
}

// --force overwrites an existing cordum.yaml.
func TestEdgeInitForceOverwrites(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "cordum.yaml"), []byte("gateway: https://prior.example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runEdgeInitCmd([]string{
		"--cwd", cwd,
		"--force",
		"--gateway", "https://localhost:8081",
		"--tenant", "tenant-new",
		"--principal", "yaron",
		"--no-wrapper",
		"--non-interactive",
	}, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d stderr=%s", code, stderr.String())
	}
	body := readFile(t, filepath.Join(cwd, "cordum.yaml"))
	if strings.Contains(body, "https://prior.example") {
		t.Errorf("existing prior config should have been overwritten: %s", body)
	}
	if !strings.Contains(body, "tenant: tenant-new") {
		t.Errorf("new config not written: %s", body)
	}
}

// init is idempotent: re-running with --force on the same scaffold inputs
// produces the same byte content (deterministic output).
func TestEdgeInitIsIdempotent(t *testing.T) {
	cwd := t.TempDir()
	args := []string{
		"--cwd", cwd,
		"--gateway", "https://localhost:8081",
		"--tenant", "default",
		"--principal", "yaron",
		"--policy-mode", "enforce",
		"--api-key-env", "CORDUM_API_KEY",
		"--no-wrapper",
		"--non-interactive",
	}
	var firstStdout, firstStderr bytes.Buffer
	if code := runEdgeInitCmd(args, nil, &firstStdout, &firstStderr); code != 0 {
		t.Fatalf("first run exit = %d stderr=%s", code, firstStderr.String())
	}
	first := readFile(t, filepath.Join(cwd, "cordum.yaml"))

	var secondStdout, secondStderr bytes.Buffer
	code := runEdgeInitCmd(append(args, "--force"), nil, &secondStdout, &secondStderr)
	if code != 0 {
		t.Fatalf("second run exit = %d stderr=%s", code, secondStderr.String())
	}
	second := readFile(t, filepath.Join(cwd, "cordum.yaml"))

	if first != second {
		t.Errorf("re-run produced different output\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// Generated cordum.yaml must round-trip through LoadEdgeClaudeConfig
// without errors — the security rail (no plaintext api_key) is satisfied.
func TestEdgeInitOutputLoadsCleanly(t *testing.T) {
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := runEdgeInitCmd([]string{
		"--cwd", cwd,
		"--gateway", "https://localhost:8081",
		"--tenant", "default",
		"--principal", "yaron",
		"--policy-mode", "enforce",
		"--api-key-env", "CORDUM_API_KEY",
		"--no-wrapper",
		"--non-interactive",
	}, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d stderr=%s", code, stderr.String())
	}
	cfg, sources, err := LoadEdgeClaudeConfig(
		[]string{"CORDUM_API_KEY=test-key-from-env"},
		cwd,
		t.TempDir(),
	)
	if err != nil {
		t.Fatalf("LoadEdgeClaudeConfig on init output failed: %v", err)
	}
	if cfg.Gateway != "https://localhost:8081" {
		t.Errorf("Gateway = %q, want init-supplied value", cfg.Gateway)
	}
	if cfg.APIKey != "test-key-from-env" {
		t.Errorf("APIKey = %q, want env-resolved value (init wrote ${CORDUM_API_KEY})", cfg.APIKey)
	}
	if sources["gateway"] != sourceProjectYAML {
		t.Errorf("source[gateway] = %q, want %q", sources["gateway"], sourceProjectYAML)
	}
}

// Wrapper script generation is opt-in (default on, --no-wrapper disables).
// The script delegates to `cordumctl edge claude`, passing through args.
func TestEdgeInitWritesWrapperScript(t *testing.T) {
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := runEdgeInitCmd([]string{
		"--cwd", cwd,
		"--gateway", "https://localhost:8081",
		"--tenant", "default",
		"--principal", "yaron",
		"--non-interactive",
	}, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d stderr=%s", code, stderr.String())
	}
	if runtime.GOOS == "windows" {
		body := readFile(t, filepath.Join(cwd, "cordum-claude.ps1"))
		if !strings.Contains(body, "cordumctl") {
			t.Errorf("ps1 wrapper missing cordumctl call: %s", body)
		}
	} else {
		body := readFile(t, filepath.Join(cwd, "cordum-claude.sh"))
		if !strings.Contains(body, "cordumctl") {
			t.Errorf("sh wrapper missing cordumctl call: %s", body)
		}
		info, err := os.Stat(filepath.Join(cwd, "cordum-claude.sh"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Errorf("cordum-claude.sh is not executable: mode=%v", info.Mode())
		}
	}
}

// Plaintext api_key supplied to init is a hard error — refuses to even
// write the file, so a typo'd flag never produces a leaked secret.
func TestEdgeInitRejectsPlaintextAPIKey(t *testing.T) {
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := runEdgeInitCmd([]string{
		"--cwd", cwd,
		"--gateway", "https://localhost:8081",
		"--tenant", "default",
		"--principal", "yaron",
		"--api-key", "literal-secret-token-do-not-store",
		"--no-wrapper",
		"--non-interactive",
	}, nil, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit; stderr=%s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(cwd, "cordum.yaml")); err == nil {
		t.Errorf("cordum.yaml was written despite plaintext api_key flag")
	}
	if strings.Contains(stderr.String(), "literal-secret-token-do-not-store") {
		t.Fatalf("stderr leaked the plaintext api_key: %s", stderr.String())
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// BUG-015 regression lock: the written cordum.yaml must be 0o600. The file
// can carry secrets via env-var references; if perms relax on a force re-run,
// any local user could read it.
func TestEdgeInitCordumYAMLIs0600(t *testing.T) {
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := runEdgeInitCmd([]string{
		"--cwd", cwd,
		"--gateway", "https://localhost:8081",
		"--tenant", "default",
		"--principal", "yaron",
		"--policy-mode", "enforce",
		"--api-key-env", "CORDUM_API_KEY",
		"--no-wrapper",
		"--non-interactive",
	}, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d stderr=%s", code, stderr.String())
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows permission model differs")
	}
	info, err := os.Stat(filepath.Join(cwd, "cordum.yaml"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("cordum.yaml mode = %#o, want 0600", got)
	}

	stdout.Reset()
	stderr.Reset()
	code = runEdgeInitCmd([]string{
		"--cwd", cwd,
		"--gateway", "https://localhost:8081",
		"--tenant", "default",
		"--principal", "yaron",
		"--policy-mode", "enforce",
		"--api-key-env", "CORDUM_API_KEY",
		"--no-wrapper",
		"--non-interactive",
		"--force",
	}, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("force exit code = %d stderr=%s", code, stderr.String())
	}
	info, err = os.Stat(filepath.Join(cwd, "cordum.yaml"))
	if err != nil {
		t.Fatalf("stat after force: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("cordum.yaml mode after force = %#o, want 0600", got)
	}
}
