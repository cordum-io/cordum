package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// EDGE-047: --print-config dumps the resolved config as YAML with api_key
// replaced by `<redacted>`, then exits 0 without launching anything.
func TestEdgeClaudePrintConfigRedactsAPIKeyAndExitsCleanly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("CORDUM_GATEWAY", "https://gateway.example")
	t.Setenv("CORDUM_API_KEY", "super-secret-print-config-token")
	t.Setenv("CORDUM_TENANT_ID", "tenant-print")
	t.Setenv("CORDUM_PRINCIPAL_ID", "principal-print")

	var stdout, stderr bytes.Buffer
	code := runEdgeClaudeCmd(
		[]string{"--print-config"},
		nil, &stdout, &stderr,
	)
	if code != 0 {
		t.Fatalf("exit = %d stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "<redacted>") {
		t.Errorf("output missing <redacted>: %s", out)
	}
	if strings.Contains(out, "super-secret-print-config-token") {
		t.Fatalf("output leaked api_key: %s", out)
	}
	if !strings.Contains(out, "https://gateway.example") {
		t.Errorf("output missing gateway: %s", out)
	}
	if !strings.Contains(out, "tenant-print") {
		t.Errorf("output missing tenant: %s", out)
	}
	if !strings.Contains(out, "api_key source: env") {
		t.Errorf("output missing source attribution for api_key: %s", out)
	}
}

// --print-config picks up project-yaml values in the cwd and identifies them
// in the source comments.
func TestEdgeClaudePrintConfigShowsProjectYamlSource(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)
	t.Setenv("CORDUM_API_KEY", "supplied-via-env")
	t.Setenv("CORDUM_PRINCIPAL_ID", "yaron")
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "cordum.yaml"), []byte("gateway: https://yaml.example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	var stdout, stderr bytes.Buffer
	code := runEdgeClaudeCmd([]string{"--print-config"}, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "https://yaml.example") {
		t.Errorf("output missing project-yaml gateway: %s", out)
	}
	if !strings.Contains(out, "gateway source: project_yaml") {
		t.Errorf("output missing project_yaml source attribution: %s", out)
	}
}

// --print-config attributes explicitly-set CLI flags as `flag` source, even
// when env or yaml supplied a value at the lower precedence layer. This is
// the integration assertion for layer E of the precedence chain
// (flag > env > project_yaml > user_yaml > default).
func TestEdgeClaudePrintConfigShowsFlagSourceForExplicitOverride(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("CORDUM_GATEWAY", "https://env.example") // env-level value
	t.Setenv("CORDUM_API_KEY", "key")
	t.Setenv("CORDUM_PRINCIPAL_ID", "yaron")

	var stdout, stderr bytes.Buffer
	code := runEdgeClaudeCmd(
		[]string{"--gateway", "https://flag.example", "--print-config"},
		nil, &stdout, &stderr,
	)
	if code != 0 {
		t.Fatalf("exit = %d stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "https://flag.example") {
		t.Errorf("output should contain flag-supplied gateway: %s", out)
	}
	if strings.Contains(out, "https://env.example") {
		t.Errorf("output should NOT contain env-supplied gateway (flag wins): %s", out)
	}
	if !strings.Contains(out, "gateway source: flag") {
		t.Errorf("output should attribute gateway to flag source: %s", out)
	}
	// Fields not on the command line stay at their pre-flag source.
	if !strings.Contains(out, "principal source: env") {
		t.Errorf("principal should still attribute to env source: %s", out)
	}
}

// --print-config does not start cordum-agentd or write any settings file —
// proving early-exit semantics. We assert this by passing an absurd
// --agentd-path that would crash if the launcher actually fired.
func TestEdgeClaudePrintConfigDoesNotLaunchAgentd(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("CORDUM_GATEWAY", "https://localhost:8081")
	t.Setenv("CORDUM_API_KEY", "key")
	t.Setenv("CORDUM_PRINCIPAL_ID", "yaron")

	var stdout, stderr bytes.Buffer
	code := runEdgeClaudeCmd([]string{
		"--print-config",
		"--agentd-path", "/path/that/definitely/does/not/exist/cordum-agentd",
	}, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d stderr=%s -- launcher must not run when --print-config is set", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "agentd") || strings.Contains(stderr.String(), "binary not found") {
		t.Errorf("stderr suggests launcher ran: %s", stderr.String())
	}
}
