package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	fs := newFlagSet("test")
	if *fs.gateway != "http://example.com" {
		t.Fatalf("expected gateway from env, got %s", *fs.gateway)
	}
	if *fs.apiKey != "token" {
		t.Fatalf("expected api key from env, got %s", *fs.apiKey)
	}
}

func TestNewClientTrimsGateway(t *testing.T) {
	client := newClient("http://localhost:8081/", "key")
	if client.BaseURL != "http://localhost:8081" {
		t.Fatalf("expected trimmed base url, got %s", client.BaseURL)
	}
	if client.APIKey != "key" {
		t.Fatalf("expected api key on client")
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
