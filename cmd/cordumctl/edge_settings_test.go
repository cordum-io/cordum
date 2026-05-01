package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteEdgeSettingsOutputPrintsToStdout(t *testing.T) {
	var stdout bytes.Buffer
	payload := []byte(`{"hooks":{"PreToolUse":[]}}`)

	if err := writeEdgeSettingsOutput(&stdout, "-", payload); err != nil {
		t.Fatalf("writeEdgeSettingsOutput returned error: %v", err)
	}
	if stdout.String() != string(payload)+"\n" {
		t.Fatalf("stdout = %q, want payload plus newline", stdout.String())
	}
}

func TestWriteEdgeSettingsOutputRefusesExistingFile(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(outPath, []byte("keep-me"), 0o600); err != nil {
		t.Fatalf("seed existing file: %v", err)
	}

	var stdout bytes.Buffer
	err := writeEdgeSettingsOutput(&stdout, outPath, []byte(`{"new":true}`))
	if err == nil {
		t.Fatalf("writeEdgeSettingsOutput overwrote existing file")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout should stay empty on overwrite refusal, got %q", stdout.String())
	}
	got, readErr := os.ReadFile(outPath)
	if readErr != nil {
		t.Fatalf("read output path: %v", readErr)
	}
	if string(got) != "keep-me" {
		t.Fatalf("existing file was modified: %q", got)
	}
}

func TestRenderEdgeSettingsPreviewRedactsSecretsAndExplainsTradeoff(t *testing.T) {
	preview := renderEdgeSettingsPreview([]byte(`{
		"env": {
			"ANTHROPIC_API_KEY": "sk-test-secret",
			"CORDUM_API_KEY": "ghp_testtoken",
			"CORDUM_EDGE_SESSION_ID": "sess-123"
		}
	}`))
	for _, secret := range []string{"sk-test-secret", "ghp_testtoken"} {
		if strings.Contains(preview, secret) {
			t.Fatalf("preview leaked %q: %s", secret, preview)
		}
	}
	for _, want := range []string{"[REDACTED]", "settings-output preview", "enterprise uses agentd memory/keychain/service bootstrap"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("preview missing %q: %s", want, preview)
		}
	}
}
