package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const openAPISpecRelativePath = "../../../docs/api/openapi/cordum-api.yaml"

func TestWorkflows_OpenAPIDocumentsAll4xx5xxResponses(t *testing.T) {
	spec := readOpenAPISpec(t)

	assertOpenAPIResponses(t, spec, "/api/v1/workflows", "post",
		"400", "401", "403", "500", "503")
	assertOpenAPIResponses(t, spec, "/api/v1/workflows/{id}", "delete",
		"401", "403", "404", "500", "503")
}

func assertOpenAPIEdgeErrorEnumContains(t *testing.T, codes ...string) {
	t.Helper()
	block := openAPISchemaBlock(t, readOpenAPISpec(t), "EdgeError")
	for _, code := range codes {
		if !strings.Contains(block, "- "+code) {
			t.Fatalf("EdgeError.code enum missing %q", code)
		}
	}
}

func assertOpenAPIResponses(t *testing.T, spec, path, method string, statuses ...string) {
	t.Helper()
	block := openAPIMethodBlock(t, spec, path, method)
	for _, status := range statuses {
		if !strings.Contains(block, "        '"+status+"':") {
			t.Fatalf("%s %s missing OpenAPI response %s", method, path, status)
		}
	}
}

func readOpenAPISpec(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(openAPISpecRelativePath))
	if err != nil {
		t.Fatalf("read OpenAPI spec: %v", err)
	}
	return strings.ReplaceAll(string(raw), "\r\n", "\n")
}

func openAPISchemaBlock(t *testing.T, spec, schema string) string {
	t.Helper()
	start := strings.Index(spec, "    "+schema+":\n")
	if start < 0 {
		t.Fatalf("OpenAPI schema %q not found", schema)
	}
	rest := spec[start+len("    "+schema+":\n"):]
	if end := strings.Index(rest, "\n    Error:\n"); end >= 0 {
		return rest[:end]
	}
	return rest
}

func openAPIMethodBlock(t *testing.T, spec, path, method string) string {
	t.Helper()
	pathBlock := openAPIPathBlock(t, spec, path)
	key := "    " + method + ":\n"
	start := strings.Index(pathBlock, key)
	if start < 0 {
		t.Fatalf("OpenAPI method %s for %s not found", method, path)
	}
	rest := pathBlock[start+len(key):]
	end := len(rest)
	for _, candidate := range []string{"get", "post", "put", "patch", "delete"} {
		if idx := strings.Index(rest, "\n    "+candidate+":\n"); idx >= 0 && idx < end {
			end = idx
		}
	}
	return rest[:end]
}

func openAPIPathBlock(t *testing.T, spec, path string) string {
	t.Helper()
	key := "  " + path + ":\n"
	start := strings.Index(spec, key)
	if start < 0 {
		t.Fatalf("OpenAPI path %s not found", path)
	}
	rest := spec[start+len(key):]
	if end := strings.Index(rest, "\n  /"); end >= 0 {
		return rest[:end]
	}
	return rest
}
