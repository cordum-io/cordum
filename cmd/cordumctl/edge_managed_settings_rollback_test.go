package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/edge/claude"
)

// TestManagedSettingsFullRollbackCycle is the synthetic-test surface for DoD
// #4 of EDGE-150: it walks export -> drift -> verify-fails -> rollback ->
// verify-passes -> deterministic-regeneration end to end with no network
// calls. Production rollback is MDM-orchestrated; this test only exercises
// the CLI rollback-template safety net documented in
// docs/edge/managed-settings-deploy.md sections 8.1 and 8.2.
func TestManagedSettingsFullRollbackCycle(t *testing.T) {
	tmpdir := t.TempDir()
	settingsPath := filepath.Join(tmpdir, "managed-settings.json")
	mcpPath := filepath.Join(tmpdir, "managed-mcp.json")

	// (1) export — assert exit 0 + both files exist.
	exportCode, exportStdout, exportStderr := runManagedSettingsForTest(t, exportArgs(tmpdir)...)
	if exportCode != 0 {
		t.Fatalf("export: exit=%d stderr=%s stdout=%s", exportCode, exportStderr, exportStdout)
	}
	for _, p := range []string{settingsPath, mcpPath} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("post-export stat %s: %v", p, err)
		}
		if info.Size() == 0 {
			t.Fatalf("post-export %s is empty", p)
		}
	}

	// (2) drift — flip allowManagedHooksOnly to false in place.
	if err := flipManagedSettingsBool(settingsPath, "allowManagedHooksOnly", false); err != nil {
		t.Fatalf("inject drift via flipManagedSettingsBool: %v", err)
	}

	// (3) verify — assert exit 1 and stdout names the drifted field.
	driftCode, driftStdout, driftStderr := runManagedSettingsForTest(t, "verify", "--path", settingsPath)
	if driftCode != 1 {
		t.Fatalf("drift verify: expected exit 1; got %d stdout=%s stderr=%s", driftCode, driftStdout, driftStderr)
	}
	if !strings.Contains(driftStdout, "allowManagedHooksOnly") {
		t.Fatalf("drift verify stdout missing allowManagedHooksOnly drift line; got %q", driftStdout)
	}

	// (4) rollback-template — assert exit 0.
	rollbackArgs := []string{
		"rollback-template",
		"--path", settingsPath,
		"--mcp-gateway-url", testManagedMCPGatewayURL,
		"--llm-proxy-base-url", testManagedLLMProxyURL,
		"--api-key-helper-command", testManagedAPIKeyHelper,
		"--hook-command", "/opt/cordum/bin/cordum-hook",
	}
	rollbackCode, rollbackStdout, rollbackStderr := runManagedSettingsForTest(t, rollbackArgs...)
	if rollbackCode != 0 {
		t.Fatalf("rollback: exit=%d stderr=%s stdout=%s", rollbackCode, rollbackStderr, rollbackStdout)
	}
	if !strings.Contains(rollbackStdout, "ok:") {
		t.Fatalf("rollback stdout missing 'ok:' confirmation; got %q", rollbackStdout)
	}

	// (5) post-rollback verify — assert exit 0 and 'ok:' line.
	postCode, postStdout, postStderr := runManagedSettingsForTest(t, "verify", "--path", settingsPath)
	if postCode != 0 {
		t.Fatalf("post-rollback verify: exit=%d stdout=%s stderr=%s", postCode, postStdout, postStderr)
	}
	if !strings.Contains(postStdout, "ok:") {
		t.Fatalf("post-rollback verify stdout missing 'ok:' confirmation; got %q", postStdout)
	}

	// (6) deterministic regeneration — JSON-structural-equality vs a freshly
	// generated template with the same inputs. Two operators (or this test
	// at two different timestamps) must converge on identical content.
	reference, err := claude.GenerateManagedSettingsTemplate(claude.ManagedSettingsOptions{
		HookCommand:         "/opt/cordum/bin/cordum-hook",
		HookTimeout:         claude.DefaultHookTimeout,
		AgentdURL:           defaultManagedAgentdURL,
		MCPGatewayURL:       testManagedMCPGatewayURL,
		LLMProxyBaseURL:     testManagedLLMProxyURL,
		APIKeyHelperCommand: testManagedAPIKeyHelper,
		Platform:            runtime.GOOS,
	})
	if err != nil {
		t.Fatalf("regenerate reference template: %v", err)
	}
	onDisk, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read post-rollback file: %v", err)
	}
	var diskDoc, refDoc map[string]any
	if err := json.Unmarshal(onDisk, &diskDoc); err != nil {
		t.Fatalf("unmarshal post-rollback file: %v", err)
	}
	if err := json.Unmarshal(reference.ManagedSettingsJSON, &refDoc); err != nil {
		t.Fatalf("unmarshal reference template: %v", err)
	}
	if !reflect.DeepEqual(diskDoc, refDoc) {
		t.Fatalf("rollback-template not deterministic: disk=%s reference=%s", string(onDisk), string(reference.ManagedSettingsJSON))
	}

	// Defense-in-depth: managed-mcp.json from step (1) is left intact;
	// rollback-template only rewrites the managed-settings file. Keep the
	// stat assertion so a future change cannot silently extend the rollback
	// surface to include managed-mcp.json without updating this test.
	if info, err := os.Stat(mcpPath); err != nil {
		t.Fatalf("post-rollback managed-mcp.json missing: %v", err)
	} else if info.Size() == 0 {
		t.Fatalf("post-rollback managed-mcp.json unexpectedly empty")
	}
}
