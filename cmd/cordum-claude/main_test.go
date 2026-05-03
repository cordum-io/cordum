package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// EDGE-047: cordum-claude is a thin alias for `cordumctl edge claude`.
// All argv after `cordum-claude` must be passed through unchanged, including
// the `--` boundary and post-`--` Claude args.
func TestCordumClaudePassesArgsThroughDoubleDash(t *testing.T) {
	capture := filepath.Join(t.TempDir(), "args.json")
	t.Setenv("CORDUM_CLAUDE_CORDUMCTL_BIN", os.Args[0])
	t.Setenv("CORDUM_CLAUDE_FAKE_CORDUMCTL", "1")
	t.Setenv("CORDUM_CLAUDE_CAPTURE", capture)

	var stdout, stderr bytes.Buffer
	code := run(
		[]string{"--policy-mode", "enforce", "--", "--my-claude-flag"},
		nil, &stdout, &stderr,
	)
	if code != 0 {
		t.Fatalf("exit code = %d stderr=%s", code, stderr.String())
	}

	var argv []string
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	if err := json.Unmarshal(data, &argv); err != nil {
		t.Fatalf("decode capture: %v", err)
	}
	got := argv[1:]
	want := []string{"edge", "claude", "--policy-mode", "enforce", "--", "--my-claude-flag"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("argv passed to cordumctl = %v, want %v", got, want)
	}
}

// Empty argv (zero flags) is still valid — relies entirely on env vars and
// ./cordum.yaml. The shim must not invent flags or refuse to run.
func TestCordumClaudeAcceptsZeroArgs(t *testing.T) {
	capture := filepath.Join(t.TempDir(), "args.json")
	t.Setenv("CORDUM_CLAUDE_CORDUMCTL_BIN", os.Args[0])
	t.Setenv("CORDUM_CLAUDE_FAKE_CORDUMCTL", "1")
	t.Setenv("CORDUM_CLAUDE_CAPTURE", capture)

	var stdout, stderr bytes.Buffer
	code := run(nil, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d stderr=%s", code, stderr.String())
	}
	var argv []string
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	if err := json.Unmarshal(data, &argv); err != nil {
		t.Fatalf("decode capture: %v", err)
	}
	got := argv[1:]
	want := []string{"edge", "claude"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("argv = %v, want %v", got, want)
	}
}

// Non-zero exit from cordumctl propagates back to cordum-claude's caller.
// Mirrors `set -e` semantics for users who pipe the wrapper into make/CI.
func TestCordumClaudePropagatesExitCode(t *testing.T) {
	t.Setenv("CORDUM_CLAUDE_CORDUMCTL_BIN", os.Args[0])
	t.Setenv("CORDUM_CLAUDE_FAKE_CORDUMCTL", "1")
	t.Setenv("CORDUM_CLAUDE_FAKE_EXIT", "7")
	t.Setenv("CORDUM_CLAUDE_CAPTURE", filepath.Join(t.TempDir(), "args.json"))

	var stdout, stderr bytes.Buffer
	code := run([]string{"--dry-run"}, nil, &stdout, &stderr)
	if code != 7 {
		t.Errorf("exit code = %d, want 7 (propagated from fake cordumctl)", code)
	}
}

// Missing cordumctl on PATH yields a clear error, not a nil-deref.
func TestCordumClaudeReportsMissingCordumctl(t *testing.T) {
	t.Setenv("CORDUM_CLAUDE_CORDUMCTL_BIN", filepath.Join(t.TempDir(), "definitely-does-not-exist-cordumctl"))
	// Ensure the fake-cordumctl env hooks are NOT set; we want a real exec failure.
	t.Setenv("CORDUM_CLAUDE_FAKE_CORDUMCTL", "")

	var stdout, stderr bytes.Buffer
	code := run([]string{"--dry-run"}, nil, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit; stderr=%s", stderr.String())
	}
	if stderr.Len() == 0 {
		t.Errorf("expected stderr message about missing cordumctl, got empty")
	}
}

func TestMain(m *testing.M) {
	if os.Getenv("CORDUM_CLAUDE_FAKE_CORDUMCTL") == "1" {
		runFakeCordumctl()
		return
	}
	os.Exit(m.Run())
}

// runFakeCordumctl writes os.Args to the capture path and exits with the
// requested code. Only invoked when the test re-execs the test binary with
// CORDUM_CLAUDE_FAKE_CORDUMCTL=1, simulating a real cordumctl on PATH.
func runFakeCordumctl() {
	capture := os.Getenv("CORDUM_CLAUDE_CAPTURE")
	if capture != "" {
		data, _ := json.Marshal(os.Args)
		_ = os.WriteFile(capture, data, 0o600)
	}
	exitCode := 0
	if v := os.Getenv("CORDUM_CLAUDE_FAKE_EXIT"); v != "" {
		switch v {
		case "1":
			exitCode = 1
		case "2":
			exitCode = 2
		case "7":
			exitCode = 7
		}
	}
	os.Exit(exitCode)
}
