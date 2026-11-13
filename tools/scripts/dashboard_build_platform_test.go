package scripts_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dockerfileInstructions shares the release image check's logical-line splitter.
func dockerfileInstructions(dockerfile string) []string {
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
	return instructions
}

func checkDashboardBuildPlatform(dockerfile string) error {
	instructions := dockerfileInstructions(dockerfile)
	last, builder := -1, -1
	for i, instruction := range instructions {
		fields := strings.Fields(instruction)
		if !strings.EqualFold(fields[0], "FROM") {
			continue
		}
		last = i
		if len(fields) < 4 || !strings.EqualFold(fields[len(fields)-2], "AS") || !strings.EqualFold(fields[len(fields)-1], "builder") {
			continue
		}
		builder = i
		platform := ""
		for _, field := range fields[1:] {
			if strings.HasPrefix(field, "--platform") {
				platform += field
			}
		}
		if platform != "--platform=$BUILDPLATFORM" && platform != "--platform=${BUILDPLATFORM}" {
			return fmt.Errorf("builder not pinned to BUILDPLATFORM: %s", instruction)
		}
	}
	if last < 0 {
		return fmt.Errorf("no FROM instruction")
	}
	if builder < 0 {
		return fmt.Errorf("missing builder stage: %s", instructions[last])
	}
	for _, field := range strings.Fields(instructions[last])[1:] {
		if strings.HasPrefix(field, "--platform") {
			return fmt.Errorf("runtime platform must follow target: %s", instructions[last])
		}
	}
	return checkDashboardBuilderCopies(instructions[last+1:])
}

// Only the current Dockerfile's shell-form COPY is accepted, not native artifacts.
func checkDashboardBuilderCopies(instructions []string) error {
	copied := false
	for _, instruction := range instructions {
		fields := strings.Fields(instruction)
		if !strings.EqualFold(fields[0], "COPY") {
			continue
		}
		fromBuilder := false
		var args []string
		for _, field := range fields[1:] {
			if strings.EqualFold(field, "--from=builder") || field == "--from=0" {
				fromBuilder = true
			} else if !strings.HasPrefix(field, "--") {
				args = append(args, field)
			}
		}
		if !fromBuilder {
			continue
		}
		if len(args) != 2 || (args[0] != "/app/dist" && args[0] != "/app/dist/") {
			return fmt.Errorf("builder COPY must copy only /app/dist: %s", instruction)
		}
		copied = true
	}
	if !copied {
		return fmt.Errorf("runtime missing COPY --from=builder /app/dist")
	}
	return nil
}

func TestDashboardBuildPlatformFixtures(t *testing.T) {
	const builder = "FROM --platform=$BUILDPLATFORM node:22-alpine AS builder\n"
	const runtime = "FROM nginxinc/nginx-unprivileged:1.30-alpine3.24\n"
	const copyDist = "COPY --from=builder --chown=101:101 /app/dist /usr/share/nginx/html\n"
	fixed := builder + "RUN pnpm run build\n" + runtime + copyDist
	for _, tc := range []struct{ name, text, want string }{
		{"native builder", fixed, ""},
		{"braced variable", strings.ReplaceAll(fixed, "$BUILDPLATFORM", "${BUILDPLATFORM}"), ""},
		{"case and trailing slash", strings.ReplaceAll(strings.ReplaceAll(fixed, "AS builder", "as BUILDER"), "/app/dist ", "/app/dist/ "), ""},
		{"CRLF continuations", strings.ReplaceAll(strings.ReplaceAll(fixed, " node:", " \\\n# comment\n node:"), "\n", "\r\n"), ""},
		{"pre-fix", strings.ReplaceAll(fixed, "--platform=$BUILDPLATFORM ", ""), "builder not pinned"},
		{"hardcoded amd64", strings.ReplaceAll(fixed, "$BUILDPLATFORM", "linux/amd64"), "builder not pinned"},
		{"duplicate platform", strings.ReplaceAll(fixed, "--platform=$BUILDPLATFORM", "--platform=$BUILDPLATFORM --platform=linux/arm64"), "builder not pinned"},
		{"runtime pinned", builder + "FROM --platform=$BUILDPLATFORM nginx:alpine\n" + copyDist, "runtime platform"},
		{"node modules", fixed + "COPY --from=builder /app/node_modules /opt/nm\n", "copy only /app/dist"},
		{"numeric builder", fixed + "COPY --from=0 /app/node_modules /opt/nm\n", "copy only /app/dist"},
		{"whole app", strings.ReplaceAll(fixed, "/app/dist ", "/app "), "copy only /app/dist"},
		{"two sources", strings.ReplaceAll(fixed, "/app/dist ", "/app/dist /app/node_modules "), "copy only /app/dist"},
		{"missing builder", runtime + copyDist, "missing builder"},
		{"no FROM", "# FROM node AS builder\n", "no FROM"},
		{"missing copy", builder + runtime, "missing COPY"},
		{"unterminated continuation", fixed + "COPY --from=builder /app/node_modules /opt/nm \\", "copy only /app/dist"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkDashboardBuildPlatform(tc.text)
			if tc.want == "" && err != nil {
				t.Fatalf("expected valid layout: %v", err)
			}
			if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestDashboardImageBuildsOnBuildPlatform(t *testing.T) {
	root, wf := loadReleaseWorkflow(t)
	data, err := os.ReadFile(filepath.Join(root, "dashboard", "Dockerfile"))
	if err != nil {
		t.Fatalf("read dashboard Dockerfile: %v", err)
	}
	if err := checkDashboardBuildPlatform(string(data)); err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, step := range wf.Jobs["dashboard"].Steps {
		if !strings.HasPrefix(step.Uses, buildPushAction) || step.With["file"].Value != "./dashboard/Dockerfile" {
			continue
		}
		found++
		if got := step.With["platforms"].Value; got != "linux/amd64,linux/arm64" {
			t.Errorf("dashboard platforms = %q, want linux/amd64,linux/arm64", got)
		}
	}
	if found != 1 {
		t.Fatalf("want one dashboard build-push step, got %d", found)
	}
}
