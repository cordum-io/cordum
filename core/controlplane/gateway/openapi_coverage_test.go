package gateway

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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

func TestOpenAPIShadowAgentFindingManagedSkipContract(t *testing.T) {
	spec := loadGatewayOpenAPISpec(t)
	status := spec.Components.Schemas["ShadowAgentFinding"].Properties["status"]
	if !stringSliceContains(status.Enum, "managed_skip") {
		t.Fatalf("ShadowAgentFinding.status enum = %v, want managed_skip", status.Enum)
	}

	listOp := spec.Paths["/api/v1/edge/shadow-agents"].Get
	include := findOpenAPIParameter(t, listOp.Parameters, "include_managed_skip")
	if include.Schema.Type != "boolean" || include.Schema.Default.Value != "false" {
		t.Fatalf("include_managed_skip schema = type %q default %v, want boolean default false",
			include.Schema.Type, include.Schema.Default.Value)
	}

	statusParam := findOpenAPIParameter(t, listOp.Parameters, "status")
	if !strings.Contains(statusParam.Description, "include_managed_skip=true") ||
		!strings.Contains(statusParam.Description, "managed_skip") {
		t.Fatalf("status filter description must direct callers to include_managed_skip=true for managed_skip rows; got %q",
			statusParam.Description)
	}
}

func TestOpenAPICreateShadowAgentFindingEvidenceContract(t *testing.T) {
	schema := loadGatewayOpenAPISpec(t).Components.Schemas["CreateShadowAgentFindingRequest"]
	tests := []struct {
		name       string
		fields     []string
		wantAllows bool
	}{
		{name: "neither evidence field rejected"},
		{name: "summary only accepted", fields: []string{"evidence_summary"}, wantAllows: true},
		{name: "artifact pointer only accepted", fields: []string{"evidence_artifact_ptr"}, wantAllows: true},
		{name: "both evidence fields accepted", fields: []string{"evidence_summary", "evidence_artifact_ptr"}, wantAllows: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fields := createShadowFindingRequestFields(tc.fields...)

			got := openAPISchemaAllowsFields(schema, fields)

			if got != tc.wantAllows {
				t.Fatalf("schema allows fields = %v, want %v; anyOf=%v required=%v",
					got, tc.wantAllows, schema.AnyOf, schema.Required)
			}
		})
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

type gatewayOpenAPISpec struct {
	Components struct {
		Schemas map[string]gatewayOpenAPISchema `yaml:"schemas"`
	} `yaml:"components"`
	Paths map[string]gatewayOpenAPIPathItem `yaml:"paths"`
}

type gatewayOpenAPIPathItem struct {
	Get gatewayOpenAPIOperation `yaml:"get"`
}

type gatewayOpenAPISchema struct {
	Required   []string                          `yaml:"required"`
	AnyOf      []gatewayOpenAPIRequiredBranch    `yaml:"anyOf"`
	Properties map[string]gatewayOpenAPIProperty `yaml:"properties"`
}

type gatewayOpenAPIRequiredBranch struct {
	Required []string `yaml:"required"`
}

type gatewayOpenAPIProperty struct {
	Enum []string `yaml:"enum"`
}

type gatewayOpenAPIOperation struct {
	Parameters []gatewayOpenAPIParameter `yaml:"parameters"`
}

type gatewayOpenAPIParameter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Schema      struct {
		Type    string    `yaml:"type"`
		Default yaml.Node `yaml:"default"`
	} `yaml:"schema"`
}

func loadGatewayOpenAPISpec(t *testing.T) gatewayOpenAPISpec {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
	data, err := os.ReadFile(filepath.Join(repoRoot, "docs", "api", "openapi", "cordum-api.yaml"))
	if err != nil {
		t.Fatalf("read openapi spec: %v", err)
	}
	var spec gatewayOpenAPISpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatalf("parse openapi spec: %v", err)
	}
	return spec
}

func findOpenAPIParameter(t *testing.T, params []gatewayOpenAPIParameter, name string) gatewayOpenAPIParameter {
	t.Helper()
	for _, param := range params {
		if param.Name == name {
			return param
		}
	}
	t.Fatalf("OpenAPI parameter %q not found", name)
	return gatewayOpenAPIParameter{}
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func createShadowFindingRequestFields(extra ...string) map[string]struct{} {
	fields := map[string]struct{}{
		"owner_principal_id": {},
		"agent_product":      {},
		"risk":               {},
		"evidence_type":      {},
		"detected_at":        {},
	}
	for _, name := range extra {
		fields[name] = struct{}{}
	}
	return fields
}

func openAPISchemaAllowsFields(schema gatewayOpenAPISchema, fields map[string]struct{}) bool {
	if !openAPIRequiredFieldsPresent(schema.Required, fields) {
		return false
	}
	if len(schema.AnyOf) == 0 {
		return true
	}
	for _, branch := range schema.AnyOf {
		if openAPIRequiredFieldsPresent(branch.Required, fields) {
			return true
		}
	}
	return false
}

func openAPIRequiredFieldsPresent(required []string, fields map[string]struct{}) bool {
	for _, name := range required {
		if _, ok := fields[name]; !ok {
			return false
		}
	}
	return true
}
