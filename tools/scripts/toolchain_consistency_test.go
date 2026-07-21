package scripts_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestRootDockerBuildersMatchGoDirective(t *testing.T) {
	root := repositoryRoot(t)
	version := rootGoVersion(t, root)
	argLine := fmt.Sprintf("ARG GO_VERSION=%s", version)
	fromLine := fmt.Sprintf("FROM golang:%s-alpine AS builder", version)

	assertFilesContain(t, root, argLine, []string{
		"Dockerfile",
		"Dockerfile.localcap",
		"demo/mock-bank/Dockerfile",
		"demo/quickstart/Dockerfile",
	})
	assertFilesContain(t, root, fromLine, []string{
		"examples/demo-guardrails/worker/Dockerfile",
		"examples/hello-worker-go/Dockerfile",
		"examples/multi-topic-worker-go/Dockerfile",
	})
}

func TestActiveToolchainDocsMatchGoDirective(t *testing.T) {
	root := repositoryRoot(t)
	version := rootGoVersion(t, root)
	assertFilesContain(t, root, fmt.Sprintf("Go toolchain: `go %s`", version), []string{
		"CONTRIBUTING.md",
	})
	assertFileCount(t, root, "TESTING.md", fmt.Sprintf("go-version: '%s'", version), 3)
}

func rootGoVersion(t *testing.T, root string) string {
	t.Helper()
	goMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	match := regexp.MustCompile(`(?m)^go ([0-9]+\.[0-9]+\.[0-9]+)$`).FindSubmatch(goMod)
	if len(match) != 2 {
		t.Fatal("go.mod must contain a three-part Go directive")
	}
	return string(match[1])
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
}

func assertFilesContain(t *testing.T, root, want string, paths []string) {
	t.Helper()
	for _, path := range paths {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !strings.Contains(string(data), want) {
			t.Errorf("%s does not contain %q", path, want)
		}
	}
}

func assertFileCount(t *testing.T, root, path, want string, count int) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if got := strings.Count(string(data), want); got != count {
		t.Errorf("%s contains %q %d times, want %d", path, want, got, count)
	}
}
