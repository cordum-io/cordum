package scripts_test

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSDKBrowserSmokeUsesSupportedUbuntuRelease(t *testing.T) {
	path := filepath.Join(repositoryRoot(t), ".github", "workflows", "sdk-typescript.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read SDK workflow: %v", err)
	}
	var workflow struct {
		Jobs map[string]struct {
			RunsOn string `yaml:"runs-on"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatalf("parse SDK workflow: %v", err)
	}

	// Playwright 1.59.1 supports these Ubuntu hosts. A floating runner moved
	// to Ubuntu 26.04 and failed before any browser test could execute.
	// Revisit this compatibility list when upgrading Playwright or the host:
	// https://github.com/microsoft/playwright/blob/v1.59.1/packages/playwright-core/src/server/utils/hostPlatform.ts
	switch runner := workflow.Jobs["browser"].RunsOn; runner {
	case "ubuntu-22.04", "ubuntu-24.04":
	default:
		t.Fatalf("SDK browser runner %q is not pinned to a supported Playwright Ubuntu release", runner)
	}
}
