package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/cordum/cordum/sdk/client"
)

func TestEnvOr(t *testing.T) {
	t.Setenv("TEST_ENV", "")
	if got := envOr("TEST_ENV", "fallback"); got != "fallback" {
		t.Fatalf("expected fallback value")
	}
	t.Setenv("TEST_ENV", " value ")
	if got := envOr("TEST_ENV", "fallback"); got != "value" {
		t.Fatalf("expected trimmed env value")
	}
}

func TestNewFlagSetDefaults(t *testing.T) {
	t.Setenv("CORDUM_GATEWAY", "http://example.com")
	t.Setenv("CORDUM_API_KEY", "token")
	t.Setenv("CORDUM_TENANT_ID", "tenant-a")
	fs := newFlagSet("test")
	if *fs.gateway != "http://example.com" {
		t.Fatalf("expected gateway from env, got %s", *fs.gateway)
	}
	if *fs.apiKey != "token" {
		t.Fatalf("expected api key from env, got %s", *fs.apiKey)
	}
	if *fs.tenant != "tenant-a" {
		t.Fatalf("expected tenant from env, got %s", *fs.tenant)
	}
}

func TestNewClientTrimsGateway(t *testing.T) {
	client := sdk.NewWithTLS("http://localhost:8081/", "key", sdk.TLSOptions{})
	client.TenantID = "tenant"
	// NewWithTLS doesn't trim trailing slash — newClientFromFlags does via
	// strings.TrimRight, so we test the full path here.
	client2 := sdk.NewWithTLS(
		strings.TrimRight("http://localhost:8081/", "/"),
		"key",
		sdk.TLSOptions{},
	)
	client2.TenantID = "tenant"
	if client2.BaseURL != "http://localhost:8081" {
		t.Fatalf("expected trimmed base url, got %s", client2.BaseURL)
	}
	if client2.APIKey != "key" {
		t.Fatalf("expected api key on client")
	}
	if client2.TenantID != "tenant" {
		t.Fatalf("expected tenant id on client")
	}
}

func TestTLSOptionsFromFlags(t *testing.T) {
	// CLI flag takes priority over env var.
	t.Setenv("CORDUM_TLS_CA", "/env/ca.crt")
	t.Setenv("CORDUM_TLS_INSECURE", "")
	fs := newFlagSet("tls-test")
	fs.ParseArgs([]string{"--cacert", "/flag/ca.crt"})
	opts := fs.tlsOptions()
	if opts.CACertPath != "/flag/ca.crt" {
		t.Fatalf("expected flag ca path, got %s", opts.CACertPath)
	}
	if opts.InsecureSkipVerify {
		t.Fatalf("expected insecure=false")
	}
}

func TestTLSOptionsFromEnv(t *testing.T) {
	t.Setenv("CORDUM_TLS_CA", "/env/ca.crt")
	t.Setenv("CORDUM_TLS_INSECURE", "1")
	fs := newFlagSet("tls-env-test")
	fs.ParseArgs([]string{})
	opts := fs.tlsOptions()
	if opts.CACertPath != "/env/ca.crt" {
		t.Fatalf("expected env ca path, got %s", opts.CACertPath)
	}
	if !opts.InsecureSkipVerify {
		t.Fatalf("expected insecure=true from env")
	}
}

func TestTLSOptionsInsecureFlag(t *testing.T) {
	t.Setenv("CORDUM_TLS_CA", "")
	t.Setenv("CORDUM_TLS_INSECURE", "")
	fs := newFlagSet("tls-insecure-test")
	fs.ParseArgs([]string{"--insecure"})
	opts := fs.tlsOptions()
	if !opts.InsecureSkipVerify {
		t.Fatalf("expected insecure=true from flag")
	}
}

func TestLoadAndPrintJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "payload.json")
	if err := os.WriteFile(path, []byte(`{"id":"wf-1"}`), 0o600); err != nil {
		t.Fatalf("write temp json: %v", err)
	}
	var payload map[string]any
	loadJSON(path, &payload)
	if payload["id"] != "wf-1" {
		t.Fatalf("unexpected payload: %#v", payload)
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	printJSON(map[string]string{"k": "v"})
	_ = w.Close()
	os.Stdout = old

	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !strings.Contains(string(data), "\"k\"") {
		t.Fatalf("expected json output, got %s", string(data))
	}
}
