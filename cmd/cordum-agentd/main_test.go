package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRunCLIHelpDoesNotStartAgentd(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	called := false
	code := runCLI(context.Background(), cliOptions{
		Args:   []string{"--help"},
		Stderr: &stderr,
		Run: func(context.Context, runConfig) error {
			called = true
			return nil
		},
	})
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if called {
		t.Fatal("runner was called for --help")
	}
	if !strings.Contains(stderr.String(), "CORDUM_GATEWAY") || !strings.Contains(stderr.String(), "CORDUM_AGENTD_SOCKET") {
		t.Fatalf("help output missing key env vars: %q", stderr.String())
	}
}

func TestRunCLIPassesEnvAndArgsToRunner(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	var got runConfig
	code := runCLI(context.Background(), cliOptions{
		Args:   []string{"--gateway", "http://127.0.0.1:8081", "--tenant", "tenant-a"},
		Env:    map[string]string{"CORDUM_API_KEY": "secret-key", "CORDUM_AGENTD_FAIL_CLOSED": "true"},
		Stderr: &stderr,
		Run: func(ctx context.Context, cfg runConfig) error {
			got = cfg
			return nil
		},
	})
	if code != 0 {
		t.Fatalf("code = %d stderr=%q, want 0", code, stderr.String())
	}
	if got.Gateway != "http://127.0.0.1:8081" || got.TenantID != "tenant-a" {
		t.Fatalf("gateway/tenant = %q/%q", got.Gateway, got.TenantID)
	}
	if got.Env["CORDUM_API_KEY"] != "secret-key" {
		t.Fatalf("env not passed to runner: %#v", got.Env)
	}
	if !got.FailClosed {
		t.Fatal("fail_closed flag/env not parsed")
	}
}

func TestRunCLIRedactsSecretsFromStartupErrors(t *testing.T) {
	t.Parallel()

	const apiKey = "super-secret-api-key-1234"
	var stderr bytes.Buffer
	code := runCLI(context.Background(), cliOptions{
		Args:   []string{"--gateway", "http://127.0.0.1:8081", "--tenant", "tenant-a"},
		Env:    map[string]string{"CORDUM_API_KEY": apiKey},
		Stderr: &stderr,
		Run: func(context.Context, runConfig) error {
			return errors.New("gateway rejected api key " + apiKey)
		},
	})
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if strings.Contains(stderr.String(), apiKey) {
		t.Fatalf("stderr leaked API key: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "[REDACTED]") {
		t.Fatalf("stderr = %q, want redaction marker", stderr.String())
	}
}
