package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestResolveUpComposeFilesIncludesDevOverrideWhenPresent(t *testing.T) {
	dir := t.TempDir()
	mainFile := filepath.Join(dir, "docker-compose.yml")
	devFile := filepath.Join(dir, "docker-compose.dev.yml")
	if err := os.WriteFile(mainFile, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(devFile, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := resolveUpComposeFiles(mainFile, true, false)
	if len(got) != 2 || got[0] != mainFile || got[1] != devFile {
		t.Fatalf("resolveUpComposeFiles(dev=true, explicit=false) = %v, want [%s %s]", got, mainFile, devFile)
	}
	gotExplicit := resolveUpComposeFiles(mainFile, true, true)
	if len(gotExplicit) != 1 || gotExplicit[0] != mainFile {
		t.Fatalf("resolveUpComposeFiles(dev=true, explicit=true) = %v, want [%s]", gotExplicit, mainFile)
	}
	gotNonDev := resolveUpComposeFiles(mainFile, false, false)
	if len(gotNonDev) != 1 || gotNonDev[0] != mainFile {
		t.Fatalf("resolveUpComposeFiles(dev=false) = %v, want [%s]", gotNonDev, mainFile)
	}
}

func TestSeedDevMCPConfigSeedsCleanInstallAndPreservesExistingTools(t *testing.T) {
	var posted map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/config":
			// Mimics clean install returning empty object from gateway.
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/config":
			if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
				t.Fatalf("decode posted config: %v", err)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := newRestClient(srv.URL, "dev-key", "default", nil)
	if err := seedDevMCPConfig(context.Background(), client, true); err != nil {
		t.Fatalf("seedDevMCPConfig clean install error: %v", err)
	}
	if posted["scope"] != "system" || posted["scope_id"] != "default" {
		t.Fatalf("unexpected scope in posted config: %#v", posted)
	}
	data, _ := posted["data"].(map[string]any)
	mcpCfg, _ := data["mcp"].(map[string]any)
	if mcpCfg["enabled"] != true || mcpCfg["transport"] != "http" || mcpCfg["policy_gate_enabled"] != true {
		t.Fatalf("unexpected mcp config seeded: %#v", mcpCfg)
	}

	// Existing tools and explicit policy_gate_enabled:false are preserved.
	posted = nil
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/config":
			_, _ = w.Write([]byte(`{"scope":"system","scope_id":"default","data":{"safety":{"pii_detection_enabled":true},"mcp":{"policy_gate_enabled":false,"tools":{"demo.tool":{"enabled":true}}}}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/config":
			if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
				t.Fatalf("decode posted config: %v", err)
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv2.Close()

	client2 := newRestClient(srv2.URL, "dev-key", "default", nil)
	if err := seedDevMCPConfig(context.Background(), client2, true); err != nil {
		t.Fatalf("seedDevMCPConfig existing error: %v", err)
	}
	data2, _ := posted["data"].(map[string]any)
	if _, ok := data2["safety"]; !ok {
		t.Fatalf("expected unrelated system/default keys to be preserved: %#v", data2)
	}
	mcpCfg2, _ := data2["mcp"].(map[string]any)
	if mcpCfg2["enabled"] != true || mcpCfg2["transport"] != "http" {
		t.Fatalf("expected missing enabled/transport filled: %#v", mcpCfg2)
	}
	if mcpCfg2["policy_gate_enabled"] != false {
		t.Fatalf("expected explicit policy_gate_enabled=false to be preserved, got %#v", mcpCfg2["policy_gate_enabled"])
	}
	if _, ok := mcpCfg2["tools"]; !ok {
		t.Fatalf("expected existing mcp.tools to be preserved: %#v", mcpCfg2)
	}
}
