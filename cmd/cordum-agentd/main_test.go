package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	edgecore "github.com/cordum/cordum/core/edge"
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
	if !strings.Contains(stderr.String(), "CORDUM_GATEWAY") ||
		!strings.Contains(stderr.String(), "CORDUM_AGENTD_SOCKET") ||
		!strings.Contains(stderr.String(), "CORDUM_AGENTD_NONCE") {
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

func TestRunCLIRedactsNonceFromStartupErrors(t *testing.T) {
	t.Parallel()

	const nonce = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
	var stderr bytes.Buffer
	code := runCLI(context.Background(), cliOptions{
		Args:   []string{"--gateway", "http://127.0.0.1:8081", "--tenant", "tenant-a"},
		Env:    map[string]string{"CORDUM_AGENTD_NONCE": nonce},
		Stderr: &stderr,
		Run: func(context.Context, runConfig) error {
			return errors.New("startup rejected nonce " + nonce)
		},
	})
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if strings.Contains(stderr.String(), nonce) {
		t.Fatalf("stderr leaked nonce: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "[REDACTED]") {
		t.Fatalf("stderr = %q, want redaction marker", stderr.String())
	}
}

func TestDefaultRunReadsCordumAgentdNonceEnv(t *testing.T) {
	const nonce = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
	setDefaultRunRequiredEnv(t)
	t.Setenv("CORDUM_AGENTD_NONCE", nonce)

	opts, err := defaultRunOptionsWithRecorder(context.Background(), runConfig{}, edgecore.NewNoopRecorder())
	if err != nil {
		t.Fatalf("defaultRunOptionsWithRecorder returned error: %v", err)
	}
	if opts.Nonce != nonce {
		t.Fatalf("RunOptions.Nonce = %q, want launcher env nonce", opts.Nonce)
	}
	if opts.Config.TenantID != "tenant-default-run-test" {
		t.Fatalf("Config.TenantID = %q, want env tenant", opts.Config.TenantID)
	}
}

func TestDefaultRunRejectsInvalidCordumAgentdNonceEnv(t *testing.T) {
	for name, nonce := range map[string]string{
		"too_short": "dG9vc2hvcnQ=",
		"malformed": "not-base64-!!@@",
	} {
		t.Run(name, func(t *testing.T) {
			setDefaultRunRequiredEnv(t)
			t.Setenv("CORDUM_AGENTD_NONCE", nonce)

			_, err := defaultRunOptionsWithRecorder(context.Background(), runConfig{}, edgecore.NewNoopRecorder())
			if err == nil {
				t.Fatal("defaultRunOptionsWithRecorder error = nil, want nonce validation error")
			}
			if !strings.Contains(err.Error(), "agentd: CORDUM_AGENTD_NONCE invalid: must be base64 encoding of >= 32 bytes") {
				t.Fatalf("error = %q, want sanitized nonce validation error", err.Error())
			}
			if strings.Contains(err.Error(), nonce) {
				t.Fatalf("error leaked nonce value: %q", err.Error())
			}
		})
	}
}

func TestDefaultRunLeavesNonceEmptyWhenCordumAgentdNonceUnset(t *testing.T) {
	setDefaultRunRequiredEnv(t)
	t.Setenv("CORDUM_AGENTD_NONCE", "")

	opts, err := defaultRunOptionsWithRecorder(context.Background(), runConfig{}, edgecore.NewNoopRecorder())
	if err != nil {
		t.Fatalf("defaultRunOptionsWithRecorder returned error: %v", err)
	}
	if opts.Nonce != "" {
		t.Fatalf("RunOptions.Nonce = %q, want empty for auto-generation", opts.Nonce)
	}
}

func setDefaultRunRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("CORDUM_GATEWAY", "http://127.0.0.1:8081")
	t.Setenv("CORDUM_API_KEY", "test-default-run-api-key")
	t.Setenv("CORDUM_TENANT_ID", "tenant-default-run-test")
	t.Setenv("CORDUM_AGENTD_STATE_DIR", t.TempDir())
}
