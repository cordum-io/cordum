package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/edge/claude"
)

func TestEdgeDoctorManagedSettingsCheckSkipsWithoutFlag(t *testing.T) {
	env := &edgeDoctorEnv{
		readFile:            os.ReadFile,
		managedSettingsPath: "",
	}
	res := edgeCheckManagedSettings(context.Background(), env)
	if res.State != stateSkip {
		t.Fatalf("empty path expected skip; got state=%q detail=%q", res.State, res.Detail)
	}
}

func TestEdgeDoctorManagedSettingsCheckPasses(t *testing.T) {
	path := writeManagedSettingsFixture(t)
	env := &edgeDoctorEnv{
		readFile:            os.ReadFile,
		managedSettingsPath: path,
	}
	res := edgeCheckManagedSettings(context.Background(), env)
	if res.State != stateOK {
		t.Fatalf("golden expected OK; got state=%q detail=%q", res.State, res.Detail)
	}
}

func TestEdgeDoctorManagedSettingsCheckFailsOnDrift(t *testing.T) {
	path := writeManagedSettingsFixture(t)
	flipBoolField(t, path, "allowManagedHooksOnly", false)
	env := &edgeDoctorEnv{
		readFile:            os.ReadFile,
		managedSettingsPath: path,
	}
	res := edgeCheckManagedSettings(context.Background(), env)
	if res.State != stateFail {
		t.Fatalf("drift expected fail; got state=%q detail=%q", res.State, res.Detail)
	}
	if !strings.Contains(res.Detail, "allowManagedHooksOnly") {
		t.Fatalf("detail must cite drifted field; got %q", res.Detail)
	}
}

func TestEdgeDoctorManagedSettingsCheckMissingFileFails(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.json")
	env := &edgeDoctorEnv{
		readFile:            os.ReadFile,
		managedSettingsPath: missing,
	}
	res := edgeCheckManagedSettings(context.Background(), env)
	if res.State != stateFail {
		t.Fatalf("missing file expected fail; got state=%q detail=%q", res.State, res.Detail)
	}
	if !strings.Contains(res.Fix, "managed-settings export") {
		t.Fatalf("fix should point at export subcommand; got %q", res.Fix)
	}
}

func TestEdgeDoctorManagedSettingsCheckRegisteredInDefault(t *testing.T) {
	checks := defaultEdgeDoctorChecks()
	for _, c := range checks {
		if c.id == "managed_settings_compliance" {
			return
		}
	}
	t.Fatalf("managed_settings_compliance not registered in defaultEdgeDoctorChecks; got %d checks", len(checks))
}

func writeManagedSettingsFixture(t *testing.T) string {
	t.Helper()
	bundle, err := claude.GenerateManagedSettingsTemplate(claude.ManagedSettingsOptions{
		HookCommand:         "/opt/cordum/bin/cordum-hook",
		AgentdURL:           "http://127.0.0.1:8765/v1/edge/hooks/claude",
		MCPGatewayURL:       "https://mcp.cordum.example/mcp",
		LLMProxyBaseURL:     "https://llm-proxy.cordum.example",
		APIKeyHelperCommand: "/opt/cordum/bin/cordum-agentd claude api-key-helper",
		Platform:            "linux",
	})
	if err != nil {
		t.Fatalf("generate managed settings: %v", err)
	}
	path := filepath.Join(t.TempDir(), "managed-settings.json")
	if err := os.WriteFile(path, bundle.ManagedSettingsJSON, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func flipBoolField(t *testing.T, path, key string, value bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	doc[key] = value
	patched, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}
	if err := os.WriteFile(path, append(patched, '\n'), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
