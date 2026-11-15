package main

import (
	"os"
	"path/filepath"
	"runtime"
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

// .env.example ships CORDUM_API_KEY= (empty). readEnvFile reads a key's first
// definition, so the generated key must replace that line, not follow it.
func TestEnsureDevBootstrapEnvFillsEmptyDefinitionInPlace(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte("CORDUM_API_KEY=\nREDIS_PASSWORD=existing-redis\n"), 0o600); err != nil {
		t.Fatalf("write seed .env: %v", err)
	}
	t.Setenv("CORDUM_API_KEY", "")
	t.Setenv("API_KEY", "")

	res, err := ensureDevBootstrapEnv(dir)
	if err != nil {
		t.Fatalf("ensureDevBootstrapEnv() error: %v", err)
	}
	if got := readEnvFile(envPath, "CORDUM_API_KEY", "API_KEY"); got != res.APIKey {
		t.Fatalf("readEnvFile(CORDUM_API_KEY) = %q, want the generated %q", got, res.APIKey)
	}
	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	if n := strings.Count(string(data), "CORDUM_API_KEY="); n != 1 {
		t.Fatalf(".env defines CORDUM_API_KEY %d times, want once:\n%s", n, data)
	}
	if !strings.Contains(string(data), "REDIS_PASSWORD=existing-redis\n") {
		t.Fatalf("existing REDIS_PASSWORD changed:\n%s", data)
	}
}

// os.WriteFile keeps an existing file's mode, so .env must be restricted to
// 0600 before the credentials go in. A read-only .env shows the order: writing
// it fails unless the chmod comes first.
func TestEnsureDevBootstrapEnvRestrictsExistingEnvBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte("REDIS_PASSWORD=existing-redis\n"), 0o444); err != nil {
		t.Fatalf("write seed .env: %v", err)
	}
	t.Setenv("CORDUM_API_KEY", "")
	t.Setenv("API_KEY", "")

	res, err := ensureDevBootstrapEnv(dir)
	if err != nil {
		t.Fatalf("ensureDevBootstrapEnv() on a read-only .env: %v", err)
	}
	if got := readEnvFile(envPath, "CORDUM_API_KEY"); got != res.APIKey {
		t.Fatalf("readEnvFile(CORDUM_API_KEY) = %q, want %q", got, res.APIKey)
	}
	info, err := os.Stat(envPath)
	if err != nil {
		t.Fatalf("stat .env: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf(".env mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestEnsureDevCLIContextWritesValidYAMLAndDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	caDir := filepath.Join(dir, "certs", "ca")
	if err := os.MkdirAll(caDir, 0o755); err != nil {
		t.Fatalf("mkdir certs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(caDir, "ca.crt"), []byte("dummy-ca"), 0o600); err != nil {
		t.Fatalf("write ca.crt: %v", err)
	}

	cfgPath, created, err := ensureDevCLIContext(dir, "tenant-alpha")
	if err != nil {
		t.Fatalf("ensureDevCLIContext() error: %v", err)
	}
	if !created {
		t.Fatal("expected created = true")
	}

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read cordum.yaml: %v", err)
	}
	if !strings.Contains(string(raw), `api_key: "${CORDUM_API_KEY}"`) && !strings.Contains(string(raw), `api_key: ${CORDUM_API_KEY}`) {
		t.Fatalf("expected ${CORDUM_API_KEY} env reference in cordum.yaml:\n%s", string(raw))
	}

	cfg, _, err := LoadEdgeClaudeConfig([]string{"CORDUM_API_KEY=secret-from-env"}, dir, t.TempDir())
	if err != nil {
		t.Fatalf("LoadEdgeClaudeConfig() failed on generated cordum.yaml: %v", err)
	}
	if cfg.Gateway != defaultDevGatewayURL {
		t.Fatalf("Gateway = %q, want %q", cfg.Gateway, defaultDevGatewayURL)
	}
	if cfg.Tenant != "tenant-alpha" {
		t.Fatalf("Tenant = %q, want tenant-alpha", cfg.Tenant)
	}
	if cfg.APIKey != "secret-from-env" {
		t.Fatalf("APIKey = %q, want secret-from-env", cfg.APIKey)
	}
	if cfg.CACert != "./certs/ca/ca.crt" {
		t.Fatalf("CACert = %q, want ./certs/ca/ca.crt", cfg.CACert)
	}

	custom := []byte("gateway: https://custom.example.com\n")
	if err := os.WriteFile(cfgPath, custom, 0o600); err != nil {
		t.Fatalf("overwrite cordum.yaml: %v", err)
	}
	_, created2, err := ensureDevCLIContext(dir, "tenant-alpha")
	if err != nil {
		t.Fatalf("second ensureDevCLIContext() error: %v", err)
	}
	if created2 {
		t.Fatal("expected created = false when cordum.yaml already exists")
	}
	after, _ := os.ReadFile(cfgPath)
	if string(after) != string(custom) {
		t.Fatalf("cordum.yaml was modified unexpectedly: %q", string(after))
	}
}

// TestAPIKeyDefaultsReadDotEnvOnlyWhenEnvUnset pins #591's .env readers: the
// --api-key default and loadEdgeClaudeConfigForRun take CORDUM_API_KEY from
// ./.env only when the environment does not set it.
func TestAPIKeyDefaultsReadDotEnvOnlyWhenEnvUnset(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("CORDUM_API_KEY=from-dotenv"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CORDUM_API_KEY", "")
	if err := os.Unsetenv("CORDUM_API_KEY"); err != nil {
		t.Fatal(err)
	}
	checkAPIKeyDefaults(t, "unset", "from-dotenv")

	t.Setenv("CORDUM_API_KEY", "from-env")
	checkAPIKeyDefaults(t, "set", "from-env")
}

func checkAPIKeyDefaults(t *testing.T, env, want string) {
	t.Helper()
	if got := *newFlagSet("test").apiKey; got != want {
		t.Errorf("CORDUM_API_KEY %s: --api-key default = %q, want %q", env, got, want)
	}
	cfg, _, err := loadEdgeClaudeConfigForRun()
	if err != nil {
		t.Fatalf("CORDUM_API_KEY %s: loadEdgeClaudeConfigForRun: %v", env, err)
	}
	if cfg.APIKey != want {
		t.Errorf("CORDUM_API_KEY %s: loadEdgeClaudeConfigForRun APIKey = %q, want %q", env, cfg.APIKey, want)
	}
}
