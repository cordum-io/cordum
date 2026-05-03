package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// EDGE-047 precedence layer A: built-in defaults baseline.
// When no config file and no env, every value comes from a constant.
func TestEdgeClaudeConfigDefaultsBaseline(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()

	cfg, sources, err := LoadEdgeClaudeConfig(nil, cwd, home)
	if err != nil {
		t.Fatalf("LoadEdgeClaudeConfig returned error: %v", err)
	}
	if got, want := cfg.Gateway, "http://localhost:8081"; got != want {
		t.Errorf("Gateway = %q, want %q", got, want)
	}
	if got, want := cfg.Tenant, "default"; got != want {
		t.Errorf("Tenant = %q, want %q", got, want)
	}
	if got, want := cfg.PolicyMode, "enforce"; got != want {
		t.Errorf("PolicyMode = %q, want %q", got, want)
	}
	if got, want := cfg.HookCommand, "cordum-hook"; got != want {
		t.Errorf("HookCommand = %q, want %q", got, want)
	}
	if got, want := cfg.ApprovalWaitTimeout, 30*time.Second; got != want {
		t.Errorf("ApprovalWaitTimeout = %v, want %v", got, want)
	}
	if got, want := sources["gateway"], sourceDefault; got != want {
		t.Errorf("source[gateway] = %q, want %q", got, want)
	}
}

// Precedence layer B: ~/.cordum/config.yaml overrides built-in defaults.
func TestEdgeClaudeConfigUserYamlOverridesBuiltin(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	writeYAMLFixture(t, filepath.Join(home, ".cordum", "config.yaml"), `
gateway: https://gateway.example
tenant: tenant-from-user-yaml
policy_mode: observe
`)

	cfg, sources, err := LoadEdgeClaudeConfig(nil, cwd, home)
	if err != nil {
		t.Fatalf("LoadEdgeClaudeConfig: %v", err)
	}
	if cfg.Gateway != "https://gateway.example" {
		t.Errorf("Gateway = %q, want user-yaml value", cfg.Gateway)
	}
	if cfg.Tenant != "tenant-from-user-yaml" {
		t.Errorf("Tenant = %q, want user-yaml value", cfg.Tenant)
	}
	if cfg.PolicyMode != "observe" {
		t.Errorf("PolicyMode = %q, want user-yaml value", cfg.PolicyMode)
	}
	// Field not set in YAML stays at built-in default.
	if cfg.HookCommand != "cordum-hook" {
		t.Errorf("HookCommand = %q, want built-in default", cfg.HookCommand)
	}
	if sources["gateway"] != sourceUserYAML {
		t.Errorf("source[gateway] = %q, want %q", sources["gateway"], sourceUserYAML)
	}
	if sources["hook_command"] != sourceDefault {
		t.Errorf("source[hook_command] = %q, want %q", sources["hook_command"], sourceDefault)
	}
}

// Precedence layer C: ./cordum.yaml overrides ~/.cordum/config.yaml.
func TestEdgeClaudeConfigProjectYamlOverridesUserYaml(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	writeYAMLFixture(t, filepath.Join(home, ".cordum", "config.yaml"), `
gateway: https://user.example
tenant: tenant-user
policy_mode: observe
`)
	writeYAMLFixture(t, filepath.Join(cwd, "cordum.yaml"), `
gateway: https://project.example
policy_mode: enforce
`)

	cfg, sources, err := LoadEdgeClaudeConfig(nil, cwd, home)
	if err != nil {
		t.Fatalf("LoadEdgeClaudeConfig: %v", err)
	}
	if cfg.Gateway != "https://project.example" {
		t.Errorf("Gateway = %q, want project value", cfg.Gateway)
	}
	if cfg.PolicyMode != "enforce" {
		t.Errorf("PolicyMode = %q, want project value", cfg.PolicyMode)
	}
	// Field set only in user-yaml falls through.
	if cfg.Tenant != "tenant-user" {
		t.Errorf("Tenant = %q, want user-yaml value", cfg.Tenant)
	}
	if sources["gateway"] != sourceProjectYAML {
		t.Errorf("source[gateway] = %q, want %q", sources["gateway"], sourceProjectYAML)
	}
	if sources["tenant"] != sourceUserYAML {
		t.Errorf("source[tenant] = %q, want %q", sources["tenant"], sourceUserYAML)
	}
}

// Precedence layer D: env vars override project YAML.
func TestEdgeClaudeConfigEnvOverridesProjectYaml(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	writeYAMLFixture(t, filepath.Join(cwd, "cordum.yaml"), `
gateway: https://project.example
tenant: tenant-project
policy_mode: observe
`)
	env := []string{
		"CORDUM_GATEWAY=https://env.example",
		"CORDUM_TENANT_ID=tenant-env",
	}

	cfg, sources, err := LoadEdgeClaudeConfig(env, cwd, home)
	if err != nil {
		t.Fatalf("LoadEdgeClaudeConfig: %v", err)
	}
	if cfg.Gateway != "https://env.example" {
		t.Errorf("Gateway = %q, want env value", cfg.Gateway)
	}
	if cfg.Tenant != "tenant-env" {
		t.Errorf("Tenant = %q, want env value", cfg.Tenant)
	}
	// Field only in YAML falls through.
	if cfg.PolicyMode != "observe" {
		t.Errorf("PolicyMode = %q, want yaml value", cfg.PolicyMode)
	}
	if sources["gateway"] != sourceEnv {
		t.Errorf("source[gateway] = %q, want %q", sources["gateway"], sourceEnv)
	}
	if sources["policy_mode"] != sourceProjectYAML {
		t.Errorf("source[policy_mode] = %q, want %q", sources["policy_mode"], sourceProjectYAML)
	}
}

// api_key supplied as ${VAR} in YAML is expanded from the env at load time.
func TestEdgeClaudeConfigAPIKeyEnvReferenceExpands(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	writeYAMLFixture(t, filepath.Join(cwd, "cordum.yaml"), `
api_key: ${CORDUM_API_KEY_TEST}
`)
	env := []string{"CORDUM_API_KEY_TEST=resolved-secret-only-in-env"}

	cfg, _, err := LoadEdgeClaudeConfig(env, cwd, home)
	if err != nil {
		t.Fatalf("LoadEdgeClaudeConfig: %v", err)
	}
	if cfg.APIKey != "resolved-secret-only-in-env" {
		t.Errorf("APIKey = %q, want expanded env value", cfg.APIKey)
	}
}

// Plaintext api_key in YAML is rejected; the security rail prevents accidental
// commit of a real key. The error must NOT echo the offending value.
func TestEdgeClaudeConfigRejectsPlaintextAPIKey(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	writeYAMLFixture(t, filepath.Join(cwd, "cordum.yaml"), `
api_key: super-secret-plaintext-do-not-commit
`)

	_, _, err := LoadEdgeClaudeConfig(nil, cwd, home)
	if err == nil {
		t.Fatalf("LoadEdgeClaudeConfig: expected error for plaintext api_key, got nil")
	}
	if !strings.Contains(err.Error(), "api_key") {
		t.Errorf("error message = %v, want mention of api_key", err)
	}
	if strings.Contains(err.Error(), "super-secret-plaintext-do-not-commit") {
		t.Fatalf("error leaked the plaintext api_key: %v", err)
	}
}

// api_key referencing an unset env var is left empty, not literal "${VAR}".
// Caller can then fall through to flags or report missing-required.
func TestEdgeClaudeConfigAPIKeyEnvReferenceUnsetIsEmpty(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	writeYAMLFixture(t, filepath.Join(cwd, "cordum.yaml"), `
api_key: ${CORDUM_API_KEY_NEVER_DEFINED}
`)

	cfg, _, err := LoadEdgeClaudeConfig(nil, cwd, home)
	if err != nil {
		t.Fatalf("LoadEdgeClaudeConfig: %v", err)
	}
	if cfg.APIKey != "" {
		t.Errorf("APIKey = %q, want empty (env var unset)", cfg.APIKey)
	}
}

// Auto-cacert kicks in for local-dev gateway when ./certs/ca/ca.crt exists.
func TestEdgeClaudeConfigAutoDetectsCacertForLocalhost(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	certPath := filepath.Join(cwd, "certs", "ca", "ca.crt")
	if err := os.MkdirAll(filepath.Dir(certPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, []byte("test cert"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeYAMLFixture(t, filepath.Join(cwd, "cordum.yaml"), `
gateway: https://localhost:8081
`)

	cfg, sources, err := LoadEdgeClaudeConfig(nil, cwd, home)
	if err != nil {
		t.Fatalf("LoadEdgeClaudeConfig: %v", err)
	}
	if cfg.CACert != certPath {
		t.Errorf("CACert = %q, want %q", cfg.CACert, certPath)
	}
	if sources["cacert"] != sourceAutoDetected {
		t.Errorf("source[cacert] = %q, want %q", sources["cacert"], sourceAutoDetected)
	}
}

// Auto-cacert is NOT applied for remote gateways even if ./certs/ca/ca.crt exists.
func TestEdgeClaudeConfigSkipsAutoCacertForRemoteGateway(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	certPath := filepath.Join(cwd, "certs", "ca", "ca.crt")
	if err := os.MkdirAll(filepath.Dir(certPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeYAMLFixture(t, filepath.Join(cwd, "cordum.yaml"), `
gateway: https://gateway.production.example
`)

	cfg, _, err := LoadEdgeClaudeConfig(nil, cwd, home)
	if err != nil {
		t.Fatalf("LoadEdgeClaudeConfig: %v", err)
	}
	if cfg.CACert != "" {
		t.Errorf("CACert = %q, want empty (production gateway)", cfg.CACert)
	}
}

// Explicit cacert in YAML is preserved; auto-detect must not override it.
func TestEdgeClaudeConfigPreservesExplicitCacert(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	autoCert := filepath.Join(cwd, "certs", "ca", "ca.crt")
	if err := os.MkdirAll(filepath.Dir(autoCert), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(autoCert, []byte("auto"), 0o644); err != nil {
		t.Fatal(err)
	}
	explicitCert := filepath.Join(cwd, "operator-supplied.crt")
	if err := os.WriteFile(explicitCert, []byte("explicit"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeYAMLFixture(t, filepath.Join(cwd, "cordum.yaml"), `
gateway: https://localhost:8081
cacert: operator-supplied.crt
`)

	cfg, sources, err := LoadEdgeClaudeConfig(nil, cwd, home)
	if err != nil {
		t.Fatalf("LoadEdgeClaudeConfig: %v", err)
	}
	if cfg.CACert != "operator-supplied.crt" {
		t.Errorf("CACert = %q, want explicit yaml value", cfg.CACert)
	}
	if sources["cacert"] != sourceProjectYAML {
		t.Errorf("source[cacert] = %q, want %q (auto-detect must not override)", sources["cacert"], sourceProjectYAML)
	}
}

// RenderRedactedYAML masks api_key but preserves every other field, so
// `cordumctl edge claude --print-config` is safe to share.
func TestEdgeClaudeConfigRenderRedactedYAML(t *testing.T) {
	cfg := EdgeClaudeConfig{
		Gateway:    "https://localhost:8081",
		APIKey:     "super-secret-actual-key",
		Tenant:     "default",
		Principal:  "yaron",
		PolicyMode: "enforce",
	}
	out := cfg.RenderRedactedYAML()
	if !strings.Contains(out, "https://localhost:8081") {
		t.Errorf("output missing gateway: %q", out)
	}
	if !strings.Contains(out, "yaron") {
		t.Errorf("output missing principal: %q", out)
	}
	if strings.Contains(out, "super-secret-actual-key") {
		t.Fatalf("output leaked api_key value: %q", out)
	}
	if !strings.Contains(out, "<redacted>") {
		t.Errorf("output missing redaction marker: %q", out)
	}
}

// Malformed YAML returns a clear error and does NOT panic.
func TestEdgeClaudeConfigMalformedYAMLRejected(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "cordum.yaml"), []byte(":: not valid yaml ::\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := LoadEdgeClaudeConfig(nil, cwd, home)
	if err == nil {
		t.Fatalf("LoadEdgeClaudeConfig: expected error for malformed yaml, got nil")
	}
	if !strings.Contains(err.Error(), "cordum.yaml") {
		t.Errorf("error should mention cordum.yaml: %v", err)
	}
}

// Unknown YAML keys are rejected, so config typos surface immediately rather
// than silently falling back to defaults.
func TestEdgeClaudeConfigUnknownFieldsRejected(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	writeYAMLFixture(t, filepath.Join(cwd, "cordum.yaml"), `
gateway: https://localhost:8081
gatewey: oops-typo
`)

	_, _, err := LoadEdgeClaudeConfig(nil, cwd, home)
	if err == nil {
		t.Fatalf("LoadEdgeClaudeConfig: expected error for unknown field, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "gatewey") &&
		!strings.Contains(strings.ToLower(err.Error()), "unknown") &&
		!strings.Contains(strings.ToLower(err.Error()), "field") {
		t.Errorf("error should mention the unknown field: %v", err)
	}
}

func writeYAMLFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
