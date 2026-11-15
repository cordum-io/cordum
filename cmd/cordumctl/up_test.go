package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureDevBootstrapEnvCreatesAndPreserves(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CORDUM_API_KEY", "")
	t.Setenv("API_KEY", "")
	t.Setenv("REDIS_PASSWORD", "")
	t.Setenv("CORDUM_ADMIN_USERNAME", "")
	t.Setenv("CORDUM_ADMIN_PASSWORD", "")
	t.Setenv("CORDUM_MCP_ENABLED", "")
	t.Setenv("CORDUM_MCP_POLICY_GATE_ENABLED", "")
	t.Setenv("CORDUM_TENANT_ID", "")

	res, err := ensureDevBootstrapEnv(dir)
	if err != nil {
		t.Fatalf("ensureDevBootstrapEnv() error: %v", err)
	}
	if !strings.HasPrefix(res.APIKey, "cordum_dev_") || len(res.APIKey) <= len("cordum_dev_") {
		t.Fatalf("expected generated cordum_dev_* key, got %q", res.APIKey)
	}
	if res.Tenant != "default" {
		t.Fatalf("Tenant = %q, want default", res.Tenant)
	}
	if len(res.AddedKeys) != 6 {
		t.Fatalf("AddedKeys = %v, want 6 keys added", res.AddedKeys)
	}

	envBytes, err := os.ReadFile(res.EnvPath)
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	content := string(envBytes)
	for _, want := range []string{
		"CORDUM_API_KEY=" + res.APIKey,
		"REDIS_PASSWORD=cordum_redis_",
		"CORDUM_ADMIN_USERNAME=admin",
		"CORDUM_ADMIN_PASSWORD=cordum_admin_",
		"CORDUM_MCP_ENABLED=true",
		"CORDUM_MCP_POLICY_GATE_ENABLED=true",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf(".env missing %q:\n%s", want, content)
		}
	}

	// Second run is idempotent and preserves generated credentials.
	res2, err := ensureDevBootstrapEnv(dir)
	if err != nil {
		t.Fatalf("second ensureDevBootstrapEnv() error: %v", err)
	}
	if len(res2.AddedKeys) != 0 {
		t.Fatalf("expected 0 added keys on second run, got %v", res2.AddedKeys)
	}
	if res2.APIKey != res.APIKey {
		t.Fatalf("APIKey mutated across runs: got %q want %q", res2.APIKey, res.APIKey)
	}
}

func TestEnsureDevBootstrapEnvPreservesExistingEnvFileAndProcessEnv(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	initial := "CORDUM_API_KEY=existing-key\nREDIS_PASSWORD=existing-redis\n"
	if err := os.WriteFile(envPath, []byte(initial), 0o600); err != nil {
		t.Fatalf("write seed .env: %v", err)
	}

	t.Setenv("CORDUM_API_KEY", "")
	t.Setenv("API_KEY", "")
	t.Setenv("REDIS_PASSWORD", "")
	t.Setenv("CORDUM_ADMIN_USERNAME", "custom-admin")
	t.Setenv("CORDUM_ADMIN_PASSWORD", "")
	t.Setenv("CORDUM_MCP_ENABLED", "")
	t.Setenv("CORDUM_MCP_POLICY_GATE_ENABLED", "")

	res, err := ensureDevBootstrapEnv(dir)
	if err != nil {
		t.Fatalf("ensureDevBootstrapEnv() error: %v", err)
	}
	if res.APIKey != "existing-key" {
		t.Fatalf("APIKey = %q, want existing-key", res.APIKey)
	}

	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	out := string(data)
	if !strings.Contains(out, "CORDUM_API_KEY=existing-key") {
		t.Fatalf("existing CORDUM_API_KEY overwritten:\n%s", out)
	}
	if !strings.Contains(out, "REDIS_PASSWORD=existing-redis") {
		t.Fatalf("existing REDIS_PASSWORD overwritten:\n%s", out)
	}
	if strings.Contains(out, "CORDUM_ADMIN_USERNAME=") {
		t.Fatalf("CORDUM_ADMIN_USERNAME from process env should not be written to .env:\n%s", out)
	}
	if !strings.Contains(out, "CORDUM_MCP_POLICY_GATE_ENABLED=true") {
		t.Fatalf("missing appended CORDUM_MCP_POLICY_GATE_ENABLED:\n%s", out)
	}
}
