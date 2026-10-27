package scripts_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"gopkg.in/yaml.v3"
)

type releaseWorkflowJob struct {
	Needs           yaml.Node `yaml:"needs"`
	If              string    `yaml:"if"`
	ContinueOnError yaml.Node `yaml:"continue-on-error"`
	Steps           []struct {
		Name            string    `yaml:"name"`
		ContinueOnError yaml.Node `yaml:"continue-on-error"`
	} `yaml:"steps"`
}

func TestReleaseChartWaitsForPublishedImages(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	path := filepath.Join(filepath.Dir(current), "..", "..", ".github", "workflows", "docker.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	var workflow struct {
		Jobs map[string]releaseWorkflowJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatalf("parse release workflow: %v", err)
	}

	for job, required := range map[string][]string{
		"helm-chart":             {"smoke-published-images"},
		"smoke-published-images": {"control-plane", "dashboard"},
	} {
		t.Run(job, func(t *testing.T) {
			config, ok := workflow.Jobs[job]
			if !ok {
				t.Fatalf("required job %q missing", job)
			}
			var needs []string
			switch config.Needs.Kind {
			case yaml.ScalarNode:
				needs = []string{config.Needs.Value}
			case yaml.SequenceNode:
				if err := config.Needs.Decode(&needs); err != nil {
					t.Fatalf("decode needs: %v", err)
				}
			}
			for _, dependency := range required {
				assertContains(t, needs, dependency)
			}
			// Keep GitHub's default success condition: always() or an explicit
			// result override could publish a chart after failed image smoke.
			if config.If != "" {
				t.Errorf("job must retain its default success condition, got if: %q", config.If)
			}
			assertReleaseFailureBlocks(t, config.ContinueOnError, "job")
			for _, step := range config.Steps {
				assertReleaseFailureBlocks(t, step.ContinueOnError, "step "+step.Name)
			}
		})
	}
}

func assertReleaseFailureBlocks(t *testing.T, value yaml.Node, location string) {
	t.Helper()
	if value.Kind != 0 && (value.Tag != "!!bool" || value.Value != "false") {
		t.Errorf("%s must not ignore failures with continue-on-error", location)
	}
}
