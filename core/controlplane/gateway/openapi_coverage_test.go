package gateway

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestOpenAPICoverage runs the openapi-audit tool against the committed
// spec and the gateway's handler source. It passes iff every registered
// mux.HandleFunc maps to a live spec operation (and vice-versa). The
// tool is invoked via `go run` so we don't need to maintain a separate
// library surface; the tool's exit code is the test verdict.
//
// Locating the repo root via runtime.Caller keeps the test hermetic
// across `go test ./...` runs from any working directory.
func TestOpenAPICoverage(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))

	spec := filepath.Join(repoRoot, "docs", "api", "openapi", "cordum-api.yaml")
	gwDir := filepath.Join(repoRoot, "core", "controlplane", "gateway")
	toolPkg := filepath.Join(repoRoot, "tools", "openapi-audit")

	cmd := exec.Command("go", "run", toolPkg, "--spec", spec, "--gateway-dir", gwDir)
	cmd.Dir = repoRoot
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err != nil {
		t.Fatalf("openapi-audit failed:\n%s", out.String())
	}
}

// TestOpenAPIRedoclyLint runs redocly lint on the spec. Gated behind
// OPENAPI_FULL=1 because it shells out to npx and pulls redocly on
// first run — fine in CI, noisy for a plain `go test`.
func TestOpenAPIRedoclyLint(t *testing.T) {
	if os.Getenv("OPENAPI_FULL") != "1" {
		t.Skip("set OPENAPI_FULL=1 to run redocly lint (CI does)")
	}
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
	spec := filepath.Join(repoRoot, "docs", "api", "openapi", "cordum-api.yaml")

	cmd := exec.Command("npx", "--yes", "@redocly/cli@latest", "lint", spec)
	cmd.Dir = repoRoot
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("redocly lint failed:\n%s", out.String())
	}
}
