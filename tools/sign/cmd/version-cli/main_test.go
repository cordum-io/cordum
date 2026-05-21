package main

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

// EDGE-151-DOWNGRADE CI gate scenario (d). version-cli wraps
// tools/sign.SemverCompare so the CI monotonicity job and the install
// path agree on ordering. These tests cover the workflow's two
// exit-status contracts: monotonic-or-fail must exit 0 for a strictly-
// greater tag and exit 1 (with a useful stderr line) when the pushed
// tag would silently sibling-downgrade a published release.

func runArgs(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	stdout, _ := os.CreateTemp(t.TempDir(), "stdout.*")
	defer func() { _ = stdout.Close() }()
	err := run(args, stdout)
	outBytes, _ := os.ReadFile(stdout.Name())
	return string(outBytes), "", err
}

func TestMonotonicOrFail_Greater(t *testing.T) {
	stdout, _, err := runArgs(t, "monotonic-or-fail", "v1.3.0", "v1.2.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Contains([]byte(stdout), []byte("ok: v1.3.0 > v1.2.0")) {
		t.Errorf("stdout missing ok message: %q", stdout)
	}
}

func TestMonotonicOrFail_Equal(t *testing.T) {
	_, _, err := runArgs(t, "monotonic-or-fail", "v1.2.0", "v1.2.0")
	if err == nil {
		t.Fatalf("expected error for equal tags")
	}
	if !strings.Contains(err.Error(), "not strictly greater") {
		t.Errorf("error text missing 'not strictly greater': %v", err)
	}
}

func TestMonotonicOrFail_Lower(t *testing.T) {
	_, _, err := runArgs(t, "monotonic-or-fail", "v1.0.0", "v1.2.0")
	if err == nil {
		t.Fatalf("expected error for downgrade tag")
	}
}

func TestMonotonicOrFail_FirstRelease(t *testing.T) {
	stdout, _, err := runArgs(t, "monotonic-or-fail", "v1.0.0", "v0.0.0")
	if err != nil {
		t.Fatalf("unexpected error for first release: %v", err)
	}
	if !bytes.Contains([]byte(stdout), []byte("first release")) {
		t.Errorf("stdout missing first-release message: %q", stdout)
	}
}

func TestMonotonicOrFail_InvalidNewer(t *testing.T) {
	_, _, err := runArgs(t, "monotonic-or-fail", "garbage", "v1.0.0")
	if err == nil {
		t.Fatalf("expected error for invalid new tag")
	}
	if !errors.Is(err, err) || !strings.Contains(err.Error(), "invalid new tag") {
		t.Errorf("error text missing 'invalid new tag': %v", err)
	}
}

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b, want string
	}{
		{"v1.0.0", "v1.0.0", "0"},
		{"v1.0.0", "v1.0.1", "-1"},
		{"v2.0.0", "v1.99.99", "1"},
		{"v1.0.0-rc1", "v1.0.0", "-1"},
	}
	for _, c := range cases {
		stdout, _, err := runArgs(t, "compare", c.a, c.b)
		if err != nil {
			t.Fatalf("compare %s %s: %v", c.a, c.b, err)
		}
		if got := strings.TrimSpace(stdout); got != c.want {
			t.Errorf("compare %s %s = %q, want %q", c.a, c.b, got, c.want)
		}
	}
}

func TestUnknownSubcommand(t *testing.T) {
	_, _, err := runArgs(t, "nope")
	if err == nil {
		t.Fatalf("expected error for unknown subcommand")
	}
}

func TestMissingArgs(t *testing.T) {
	_, _, err := runArgs(t)
	if err == nil {
		t.Fatalf("expected error for missing subcommand")
	}
}
