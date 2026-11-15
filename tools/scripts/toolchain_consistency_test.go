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
	assertGoVersionPinsMatch(t, root, "TESTING.md", version)
}

// goVersionPin matches a setup-go `go-version:` pin with a literal version,
// quoted or bare, alone or as the first entry of a matrix list.
var goVersionPin = regexp.MustCompile(`go-version:\s*\[?\s*['"]?([0-9][0-9A-Za-z.-]*)`)

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

// assertGoVersionPinsMatch fails for every `go-version:` pin in path that is
// not go.mod's version. A file with no pins passes.
func assertGoVersionPinsMatch(t *testing.T, root, path, version string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for _, match := range goVersionPin.FindAllSubmatch(data, -1) {
		if got := string(match[1]); got != version {
			t.Errorf("%s pins go-version %q, want go.mod's %q", path, got, version)
		}
	}
}
