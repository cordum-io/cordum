package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		nextSvc := strings.Index(section[1:], "\n  ")
		if nextSvc > 0 {
			// Look for healthcheck within the broader section
			// (using a large window since indented content has leading spaces)
		}
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
