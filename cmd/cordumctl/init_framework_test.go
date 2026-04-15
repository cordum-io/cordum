package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func scaffoldFrameworkToDir(t *testing.T, framework string) string {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "project")
	if err := scaffoldInit(target, false); err != nil {
		t.Fatalf("scaffoldInit: %v", err)
	}
	if err := scaffoldFramework(target, framework, true); err != nil {
		t.Fatalf("scaffoldFramework(%s): %v", framework, err)
	}
	return target
}

func TestValidateFramework(t *testing.T) {
	for _, valid := range []string{"", "langchain", "crewai", "autogen"} {
		if err := validateFramework(valid); err != nil {
			t.Errorf("validateFramework(%q) = %v, want nil", valid, err)
		}
	}
	for _, invalid := range []string{"pytorch", "tensorflow", "openai"} {
		if err := validateFramework(invalid); err == nil {
			t.Errorf("validateFramework(%q) = nil, want error", invalid)
		}
	}
}

func TestScaffoldLangchain_CreatesWorkerFiles(t *testing.T) {
	target := scaffoldFrameworkToDir(t, "langchain")

	expected := []string{
		filepath.Join("worker", "agent.py"),
		filepath.Join("worker", "requirements.txt"),
		filepath.Join("worker", "Dockerfile"),
		filepath.Join("config", "safety.yaml"),
		"docker-compose.yml",
	}
	for _, rel := range expected {
		path := filepath.Join(target, rel)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected file %s to exist", rel)
		}
	}

	// Verify worker code references cap.runtime.Agent
	agentPy, err := os.ReadFile(filepath.Join(target, "worker", "agent.py"))
	if err != nil {
		t.Fatalf("read agent.py: %v", err)
	}
	if !strings.Contains(string(agentPy), "cap.runtime") {
		t.Error("agent.py should import cap.runtime")
	}
	if !strings.Contains(string(agentPy), "@agent.job") {
		t.Error("agent.py should use @agent.job decorator")
	}
}

func TestScaffoldCrewAI_CreatesWorkerFiles(t *testing.T) {
	target := scaffoldFrameworkToDir(t, "crewai")

	expected := []string{
		filepath.Join("worker", "crew.py"),
		filepath.Join("worker", "requirements.txt"),
		filepath.Join("worker", "Dockerfile"),
	}
	for _, rel := range expected {
		path := filepath.Join(target, rel)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected file %s to exist", rel)
		}
	}

	crewPy, err := os.ReadFile(filepath.Join(target, "worker", "crew.py"))
	if err != nil {
		t.Fatalf("read crew.py: %v", err)
	}
	if !strings.Contains(string(crewPy), "cap.runtime") {
		t.Error("crew.py should import cap.runtime")
	}
}

func TestScaffoldAutoGen_CreatesWorkerFiles(t *testing.T) {
	target := scaffoldFrameworkToDir(t, "autogen")

	expected := []string{
		filepath.Join("worker", "agents.py"),
		filepath.Join("worker", "requirements.txt"),
		filepath.Join("worker", "Dockerfile"),
	}
	for _, rel := range expected {
		path := filepath.Join(target, rel)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected file %s to exist", rel)
		}
	}

	agentsPy, err := os.ReadFile(filepath.Join(target, "worker", "agents.py"))
	if err != nil {
		t.Fatalf("read agents.py: %v", err)
	}
	if !strings.Contains(string(agentsPy), "cap.runtime") {
		t.Error("agents.py should import cap.runtime")
	}
}

func TestScaffoldFramework_ComposeIncludesWorkerService(t *testing.T) {
	tests := []struct {
		framework string
		service   string
	}{
		{"langchain", "cordum-langchain-worker"},
		{"crewai", "cordum-crewai-worker"},
		{"autogen", "cordum-autogen-worker"},
	}
	for _, tt := range tests {
		t.Run(tt.framework, func(t *testing.T) {
			target := scaffoldFrameworkToDir(t, tt.framework)
			data, err := os.ReadFile(filepath.Join(target, "docker-compose.yml"))
			if err != nil {
				t.Fatalf("read compose: %v", err)
			}
			content := string(data)
			if !strings.Contains(content, tt.service+":") {
				t.Errorf("docker-compose.yml should contain %s service", tt.service)
			}
			// Worker service should depend on NATS and connect to it.
			svcIdx := strings.Index(content, tt.service+":")
			if svcIdx < 0 {
				t.Fatalf("service %s not found", tt.service)
			}
			section := content[svcIdx:]
			if !strings.Contains(section, "NATS_URL=nats://nats:4222") {
				t.Errorf("%s should have NATS_URL environment variable", tt.service)
			}
		})
	}
}

func TestScaffoldFramework_SafetyPolicyHasDenyDefault(t *testing.T) {
	for _, framework := range []string{"langchain", "crewai", "autogen"} {
		t.Run(framework, func(t *testing.T) {
			target := scaffoldFrameworkToDir(t, framework)
			data, err := os.ReadFile(filepath.Join(target, "config", "safety.yaml"))
			if err != nil {
				t.Fatalf("read safety.yaml: %v", err)
			}
			content := string(data)
			if !strings.Contains(content, "default_decision: deny") {
				t.Error("safety.yaml should have default_decision: deny")
			}
			if !strings.Contains(content, "rules:") {
				t.Error("safety.yaml should have rules section")
			}
		})
	}
}

func TestScaffoldFramework_RequirementsIncludeCAP(t *testing.T) {
	for _, framework := range []string{"langchain", "crewai", "autogen"} {
		t.Run(framework, func(t *testing.T) {
			target := scaffoldFrameworkToDir(t, framework)
			data, err := os.ReadFile(filepath.Join(target, "worker", "requirements.txt"))
			if err != nil {
				t.Fatalf("read requirements.txt: %v", err)
			}
			content := string(data)
			if !strings.Contains(content, "cap-sdk") {
				t.Error("requirements.txt should include cap-sdk")
			}
			if !strings.Contains(content, "nats-py") {
				t.Error("requirements.txt should include nats-py")
			}
		})
	}
}
