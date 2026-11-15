package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func scaffoldToDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "project")
	if err := scaffoldInit(target, false); err != nil {
		t.Fatalf("scaffoldInit: %v", err)
	}
	return target
}

func readCompose(t *testing.T, target string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(target, "docker-compose.yml"))
	if err != nil {
		t.Fatalf("read compose: %v", err)
	}
	return string(data)
}

func TestScaffoldInit_CreatesAllFiles(t *testing.T) {
	target := scaffoldToDir(t)

	expected := []string{
		"docker-compose.yml",
		filepath.Join("config", "pools.yaml"),
		filepath.Join("config", "timeouts.yaml"),
		filepath.Join("config", "safety.yaml"),
		filepath.Join("workflows", "hello.json"),
		"README.md",
	}
	for _, rel := range expected {
		path := filepath.Join(target, rel)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected file %s to exist", rel)
		}
	}
}

func TestScaffoldInit_ComposeRedisAuth(t *testing.T) {
	target := scaffoldToDir(t)
	content := readCompose(t, target)

	// Redis service should have --requirepass in its command.
	if !strings.Contains(content, "--requirepass") {
		t.Error("redis command should include --requirepass")
	}

	// Safety kernel must have REDIS_URL.
	// Find the safety-kernel section and verify REDIS_URL is present.
	skIdx := strings.Index(content, "cordum-safety-kernel:")
	if skIdx < 0 {
		t.Fatal("expected cordum-safety-kernel service in compose")
	}
	// Find the next service boundary to scope our search.
	skSection := content[skIdx:]
	nextService := strings.Index(skSection[1:], "\n  cordum-")
	if nextService > 0 {
		skSection = skSection[:nextService+1]
	}
	if !strings.Contains(skSection, "REDIS_URL=") {
		t.Error("cordum-safety-kernel should have REDIS_URL environment variable")
	}

	// All REDIS_URL values should use the same password variable.
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- REDIS_URL=") {
			if !strings.Contains(trimmed, "${REDIS_PASSWORD:-cordum-dev}") {
				t.Errorf("REDIS_URL should use ${REDIS_PASSWORD:-cordum-dev}, got: %s", trimmed)
			}
		}
	}
}

func TestScaffoldInit_ComposeAllServicesHaveHealthcheck(t *testing.T) {
	target := scaffoldToDir(t)
	content := readCompose(t, target)

	// All services in the template should have healthcheck blocks.
	services := []string{
		"nats:", "redis:",
		"cordum-context-engine:", "cordum-safety-kernel:",
		"cordum-scheduler:", "cordum-api-gateway:",
		"cordum-workflow-engine:", "cordum-dashboard:",
	}
	for _, svc := range services {
		idx := strings.Index(content, svc)
		if idx < 0 {
			t.Errorf("expected service %s in compose", svc)
			continue
		}
		// Find section until next top-level service or volumes.
		section := content[idx:]
		// Simple check: find "healthcheck:" after this service name.
		hcIdx := strings.Index(section, "healthcheck:")
		if hcIdx < 0 {
			t.Errorf("service %s should have a healthcheck", svc)
		}
	}
}

func TestScaffoldInit_NoForceSkipsExisting(t *testing.T) {
	target := scaffoldToDir(t)

	// Write a sentinel value to docker-compose.yml.
	composePath := filepath.Join(target, "docker-compose.yml")
	sentinel := "# sentinel-do-not-overwrite\n"
	if err := os.WriteFile(composePath, []byte(sentinel), 0o600); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	// Re-scaffold without force — should return an error (file exists).
	err := scaffoldInit(target, false)
	if err == nil {
		t.Fatal("expected error when files exist without --force")
	}
	if !strings.Contains(err.Error(), "file exists") {
		t.Fatalf("expected 'file exists' error, got: %v", err)
	}

	// Original file should be preserved.
	data, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatalf("read compose: %v", err)
	}
	if string(data) != sentinel {
		t.Error("scaffoldInit without --force should not overwrite existing files")
	}
}

func TestScaffoldInit_ComposeVolumes(t *testing.T) {
	target := scaffoldToDir(t)
	content := readCompose(t, target)

	for _, vol := range []string{"nats_data:", "redis_data:"} {
		if !strings.Contains(content, vol) {
			t.Errorf("expected volume %s to be defined in compose", vol)
		}
	}
}

func TestScaffoldInit_SafetyTemplateDenyDefault(t *testing.T) {
	target := scaffoldToDir(t)
	data, err := os.ReadFile(filepath.Join(target, "config", "safety.yaml"))
	if err != nil {
		t.Fatalf("read safety.yaml: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "default_decision: deny") {
		t.Error("safety.yaml should have default_decision: deny")
	}
	if !strings.Contains(content, "fail_mode: closed") {
		t.Error("safety.yaml should have fail_mode: closed")
	}
}

func TestScaffoldInit_TimeoutsTemplate900s(t *testing.T) {
	target := scaffoldToDir(t)
	data, err := os.ReadFile(filepath.Join(target, "config", "timeouts.yaml"))
	if err != nil {
		t.Fatalf("read timeouts.yaml: %v", err)
	}
	if !strings.Contains(string(data), "running_timeout_seconds: 900") {
		t.Error("timeouts.yaml should have running_timeout_seconds: 900")
	}
}

// Images must be published names, and every environment entry a plain string:
// an unquoted "KEY=${X:?msg: y}" parses as a YAML map and docker compose
// rejects the whole file.
func TestScaffoldInit_ComposeIsValid(t *testing.T) {
	var compose struct {
		Services map[string]struct {
			Image       string `yaml:"image"`
			Environment []any  `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(readCompose(t, scaffoldToDir(t))), &compose); err != nil {
		t.Fatalf("parse compose: %v", err)
	}
	for name, svc := range compose.Services {
		// Releases publish ghcr.io/cordum-io/cordum/<service>:<version>.
		if strings.Contains(svc.Image, "/control-plane:") {
			t.Errorf("%s uses unpublished image %s", name, svc.Image)
		}
		for i, entry := range svc.Environment {
			if _, ok := entry.(string); !ok {
				t.Errorf("%s environment[%d] is %T, want string: %v", name, i, entry, entry)
			}
		}
	}
}

// The scaffold's gateway serves HTTPS with the certs `cordumctl init` writes,
// so cordumctl's https://localhost:8081 default works from the project dir.
func TestScaffoldInit_GatewayServesHTTPS(t *testing.T) {
	target := scaffoldToDir(t)
	content := readCompose(t, target)

	section := func(name string) string {
		idx := strings.Index(content, "\n  "+name+":\n")
		if idx < 0 {
			t.Fatalf("expected service %s in compose", name)
		}
		s := content[idx+1:]
		if next := strings.Index(s[1:], "\n  cordum-"); next > 0 {
			s = s[:next+1]
		}
		return s
	}
	for _, want := range []string{
		"GATEWAY_HTTP_TLS_CERT=/etc/cordum/tls/server/tls.crt",
		"GATEWAY_HTTP_TLS_KEY=/etc/cordum/tls/server/tls.key",
		"./certs:/etc/cordum/tls:ro",
	} {
		if !strings.Contains(section("cordum-api-gateway"), want) {
			t.Errorf("cordum-api-gateway should contain %q", want)
		}
	}
	dashboard := section("cordum-dashboard")
	for _, want := range []string{
		"CORDUM_API_BASE_URL=${CORDUM_API_BASE_URL:-}\n",
		"CORDUM_API_UPSTREAM_SCHEME=https",
		"./certs:/etc/cordum/tls:ro",
	} {
		if !strings.Contains(dashboard, want) {
			t.Errorf("cordum-dashboard should contain %q", want)
		}
	}

	data, err := os.ReadFile(filepath.Join(target, "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	readme := string(data)
	if strings.Contains(readme, "http://localhost:8081") {
		t.Error("README should not point at a plain-HTTP gateway")
	}
	if !strings.Contains(readme, "--cacert certs/ca/ca.crt https://localhost:8081/") {
		t.Error("README curl should trust certs/ca/ca.crt over https")
	}
}

func TestScaffoldInit_ReadmeDocumentsSecrets(t *testing.T) {
	target := scaffoldToDir(t)
	data, err := os.ReadFile(filepath.Join(target, "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	content := string(data)
	for _, substr := range []string{
		"CORDUM_API_KEY",
		"REDIS_PASSWORD",
		"cordum-dev",
		"openssl rand -hex 32",
		"Production",
	} {
		if !strings.Contains(content, substr) {
			t.Errorf("README should contain %q", substr)
		}
	}
}
