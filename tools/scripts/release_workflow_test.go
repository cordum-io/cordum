package scripts_test

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type releaseWorkflowStep struct {
	Name            string               `yaml:"name"`
	ID              string               `yaml:"id"`
	If              yaml.Node            `yaml:"if"`
	Uses            string               `yaml:"uses"`
	With            map[string]yaml.Node `yaml:"with"`
	ContinueOnError yaml.Node            `yaml:"continue-on-error"`
}

type releaseWorkflowJob struct {
	Needs           yaml.Node             `yaml:"needs"`
	If              string                `yaml:"if"`
	ContinueOnError yaml.Node             `yaml:"continue-on-error"`
	Steps           []releaseWorkflowStep `yaml:"steps"`
}

const (
	buildPushAction = "docker/build-push-action@"
	trivyAction     = "aquasecurity/trivy-action@"
)

// loadReleaseWorkflow returns the cordum repo root and the parsed jobs of
// .github/workflows/docker.yml.
func loadReleaseWorkflow(t *testing.T) (string, map[string]releaseWorkflowJob) {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	root := filepath.Join(filepath.Dir(current), "..", "..")
	data, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "docker.yml"))
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	var workflow struct {
		Jobs map[string]releaseWorkflowJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatalf("parse release workflow: %v", err)
	}
	return root, workflow.Jobs
}

func TestReleaseChartWaitsForPublishedImages(t *testing.T) {
	_, jobs := loadReleaseWorkflow(t)

	for job, required := range map[string][]string{
		"helm-chart":             {"smoke-published-images"},
		"smoke-published-images": {"control-plane", "dashboard", "demo-quickstart-worker"},
	} {
		t.Run(job, func(t *testing.T) {
			config, ok := jobs[job]
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

// checkFinalStageUpgradesAlpine reports an error when the final stage of a
// Dockerfile is based on an Alpine image but never runs
// `apk upgrade --no-cache`. Without it the image ships the base layer's
// packages as they were when the base was published (task-278cbdd7: the
// demo image shipped alpine:3.21's zlib 1.3.2-r1, CVE-2026-4738).
func checkFinalStageUpgradesAlpine(dockerfile string) error {
	text := strings.ReplaceAll(dockerfile, "\r\n", "\n")
	var instructions []string
	var pending strings.Builder
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasSuffix(trimmed, "\\") {
			pending.WriteString(strings.TrimSuffix(trimmed, "\\"))
			pending.WriteString(" ")
			continue
		}
		pending.WriteString(trimmed)
		if instruction := strings.Join(strings.Fields(pending.String()), " "); instruction != "" {
			instructions = append(instructions, instruction)
		}
		pending.Reset()
	}
	if rest := strings.Join(strings.Fields(pending.String()), " "); rest != "" {
		instructions = append(instructions, rest)
	}

	last := -1
	for i, instruction := range instructions {
		if strings.HasPrefix(strings.ToUpper(instruction), "FROM ") {
			last = i
		}
	}
	if last < 0 {
		return errors.New("no FROM instruction")
	}
	base := ""
	for _, token := range strings.Fields(instructions[last])[1:] {
		if !strings.HasPrefix(token, "--") {
			base = token
			break
		}
	}
	if !strings.Contains(strings.ToLower(base), "alpine") {
		return nil
	}
	for _, instruction := range instructions[last+1:] {
		if strings.HasPrefix(strings.ToUpper(instruction), "RUN ") && strings.Contains(instruction, "apk upgrade --no-cache") {
			return nil
		}
	}
	return fmt.Errorf("final stage %s does not run `apk upgrade --no-cache`", base)
}

func TestReleaseFinalStageUpgradeFixtures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		dockerfile string
		wantErr    bool
	}{
		{
			name: "pre-#531 demo final stage",
			dockerfile: "FROM golang:1.26-alpine AS builder\nRUN go build -o /out/worker ./demo\n" +
				"FROM alpine:3.21\nRUN apk add --no-cache ca-certificates wget\nUSER cordum\n",
			wantErr: true,
		},
		{
			name: "upgrade only in builder stage",
			dockerfile: "FROM golang:1.26-alpine AS builder\nRUN apk upgrade --no-cache\n" +
				"FROM alpine:3.24\nRUN apk add --no-cache ca-certificates\n",
			wantErr: true,
		},
		{
			name: "upgrade line commented out",
			dockerfile: "FROM alpine:3.24\n# RUN apk upgrade --no-cache && apk add --no-cache ca-certificates\n" +
				"RUN apk add --no-cache ca-certificates\n",
			wantErr: true,
		},
		{
			name:       "root Dockerfile style",
			dockerfile: "FROM golang:1.26-alpine AS builder\r\nFROM alpine:3.24\r\nRUN apk upgrade --no-cache \\\r\n    && apk add --no-cache ca-certificates\r\nUSER cordum\r\n",
		},
		{
			name:       "non-alpine final stage",
			dockerfile: "FROM golang:1.26-alpine AS builder\nFROM gcr.io/distroless/static-debian12:nonroot\nCOPY --from=builder /out/app /app\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkFinalStageUpgradesAlpine(tc.dockerfile)
			if tc.wantErr && err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected nil, got %v", err)
			}
		})
	}
}

func TestReleaseImagesUpgradeAlpinePackages(t *testing.T) {
	root, jobs := loadReleaseWorkflow(t)
	files := map[string]bool{}
	for _, job := range jobs {
		scanned := false
		for _, step := range job.Steps {
			if strings.HasPrefix(step.Uses, trivyAction) {
				scanned = true
			}
		}
		if !scanned {
			continue
		}
		for _, step := range job.Steps {
			if file, ok := step.With["file"]; ok && strings.HasPrefix(step.Uses, buildPushAction) {
				files[path.Clean(file.Value)] = true
			}
		}
	}
	for _, want := range []string{"Dockerfile", "dashboard/Dockerfile", "demo/quickstart/Dockerfile"} {
		if !files[want] {
			t.Fatalf("scanned image jobs do not build %s; found %v", want, files)
		}
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			if err := checkFinalStageUpgradesAlpine(string(data)); err != nil {
				t.Errorf("%s: %v", name, err)
			}
		})
	}
}

// TestReleaseTrivyGatesBlockImagePush keeps each image job's Trivy step in
// front of its push. With format: sarif, trivy-action v0.36.0 (ed142fd0)
// entrypoint.sh:75-82 unsets TRIVY_SEVERITY unless limit-severities-for-sarif
// is "true", so the effective gate is every fixed OS-package finding at any
// severity (ignore-unfixed: true, vuln-type: os), not only the
// `severity: CRITICAL` the step names (verified in task-278cbdd7). Setting
// that input, ignoring findings, or letting the job continue past the scan
// would weaken the gate.
func TestReleaseTrivyGatesBlockImagePush(t *testing.T) {
	_, jobs := loadReleaseWorkflow(t)
	for _, name := range []string{"control-plane", "dashboard", "demo-quickstart-worker"} {
		t.Run(name, func(t *testing.T) {
			job, ok := jobs[name]
			if !ok {
				t.Fatalf("required job %q missing", name)
			}
			if job.If != "" {
				t.Errorf("job must keep its default condition, got if: %q", job.If)
			}
			assertReleaseFailureBlocks(t, job.ContinueOnError, "job")
			scan := -1
			for i, step := range job.Steps {
				if !strings.HasPrefix(step.Uses, trivyAction) {
					continue
				}
				if scan >= 0 {
					t.Fatalf("want exactly one Trivy step, found another at step %d (%s)", i, step.Name)
				}
				scan = i
			}
			if scan < 0 {
				t.Fatal("no Trivy scan step")
			}
			step := job.Steps[scan]
			if got := step.With["exit-code"].Value; got != "1" {
				t.Errorf("Trivy exit-code = %q, want \"1\"", got)
			}
			if limit, ok := step.With["limit-severities-for-sarif"]; ok && strings.EqualFold(limit.Value, "true") {
				t.Error("limit-severities-for-sarif: true would narrow the gate to the named severity")
			}
			if _, ok := step.With["trivyignores"]; ok {
				t.Error("Trivy step must not ignore findings through trivyignores")
			}
			if step.If.Kind != 0 {
				t.Errorf("Trivy step must not carry an if: condition, got %q", step.If.Value)
			}
			assertReleaseFailureBlocks(t, step.ContinueOnError, "Trivy step")
			pushes := 0
			for i, candidate := range job.Steps {
				if !strings.HasPrefix(candidate.Uses, buildPushAction) || candidate.With["push"].Value != "true" {
					continue
				}
				pushes++
				if i < scan {
					t.Errorf("push step %q runs before the Trivy scan", candidate.Name)
				}
				if candidate.If.Kind != 0 {
					t.Errorf("push step %q must not carry an if: condition, got %q", candidate.Name, candidate.If.Value)
				}
				assertReleaseFailureBlocks(t, candidate.ContinueOnError, "push step "+candidate.Name)
			}
			if pushes == 0 {
				t.Fatal("no push step (docker/build-push-action with push: true)")
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
