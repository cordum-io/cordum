package scripts_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"gopkg.in/yaml.v3"
)

type dependabotConfig struct {
	Updates []dependabotUpdate `yaml:"updates"`
}

type dependabotUpdate struct {
	Ecosystem string             `yaml:"package-ecosystem"`
	Directory string             `yaml:"directory"`
	Ignore    []dependabotIgnore `yaml:"ignore"`
}

type dependabotIgnore struct {
	Dependency  string   `yaml:"dependency-name"`
	Versions    []string `yaml:"versions"`
	UpdateTypes []string `yaml:"update-types"`
}

func TestDependabotDefersKnownIncompatibleMigrations(t *testing.T) {
	config := loadDependabotConfig(t)
	root := findUpdate(t, config, "gomod", "/")
	for _, dependency := range []string{"k8s.io/api", "k8s.io/apimachinery", "k8s.io/client-go"} {
		entry := findIgnore(t, root, dependency)
		assertContains(t, entry.Versions, ">= 0.36.0")
	}

	dashboard := findUpdate(t, config, "npm", "/dashboard")
	for _, dependency := range []string{"orval", "zod", "recharts", "typescript"} {
		entry := findIgnore(t, dashboard, dependency)
		assertContains(t, entry.UpdateTypes, "version-update:semver-major")
	}
}

func loadDependabotConfig(t *testing.T) dependabotConfig {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	path := filepath.Join(filepath.Dir(current), "..", "..", ".github", "dependabot.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read dependabot config: %v", err)
	}
	var config dependabotConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatalf("parse dependabot config: %v", err)
	}
	return config
}

func findUpdate(t *testing.T, config dependabotConfig, ecosystem, directory string) dependabotUpdate {
	t.Helper()
	for _, update := range config.Updates {
		if update.Ecosystem == ecosystem && update.Directory == directory {
			return update
		}
	}
	t.Fatalf("dependabot update %s directory %q not found", ecosystem, directory)
	return dependabotUpdate{}
}

func findIgnore(t *testing.T, update dependabotUpdate, dependency string) dependabotIgnore {
	t.Helper()
	for _, entry := range update.Ignore {
		if entry.Dependency == dependency {
			return entry
		}
	}
	t.Fatalf("dependabot ignore for %q not found", dependency)
	return dependabotIgnore{}
}

func assertContains(t *testing.T, values []string, want string) {
	t.Helper()
	for _, value := range values {
		if value == want {
			return
		}
	}
	t.Fatalf("%q not found in %v", want, values)
}
