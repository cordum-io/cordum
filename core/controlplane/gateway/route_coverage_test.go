package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type openAPIOperation struct {
	Method      string
	Path        string
	OperationID string
}

type openAPIRouteCoverage struct {
	operations     []openAPIOperation
	operationByKey map[string]openAPIOperation
	methodsByPath  map[string]map[string]bool
	coveragePaths  map[string]bool
}

var routeCoveragePathParam = regexp.MustCompile(`\{[^/}]+\}`)

func TestRouteCoverage_OpenAPIRoutesAreRegistered(t *testing.T) {
	if testing.Short() {
		t.Skip("skip runtime mux coverage in -short mode")
	}

	spec := loadOpenAPIRouteCoverage(t)
	_, mux := newRouteCoverageMux(t)

	var missing []string
	for _, op := range spec.operations {
		req := httptest.NewRequest(strings.ToUpper(op.Method), substituteRouteParams(op.Path), nil)
		_, pattern := mux.Handler(req)
		if pattern != "" {
			continue
		}
		missing = append(missing, fmt.Sprintf("- %s: %s %s", routeCoverageOpName(op), strings.ToUpper(op.Method), op.Path))
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("openapi operations missing from runtime mux (%d):\n%s", len(missing), strings.Join(missing, "\n"))
	}
}

func TestRouteCoverage_AllRegisteredRoutesAppearInOpenAPI(t *testing.T) {
	spec := loadOpenAPIRouteCoverage(t)
	s, _ := newRouteCoverageMux(t)

	var missing []string
	for _, route := range s.Routes() {
		if route.Auth == "public" || route.Path == "/healthz" || isRelaxedRouteCoveragePath(route.Path) {
			continue
		}

		normalizedPath := normalizeRouteCoveragePath(route.Path)
		if strings.EqualFold(route.Method, "ANY") {
			if spec.pathSupportsAnyMethod(normalizedPath) {
				continue
			}
			missing = append(missing, fmt.Sprintf("- %s %s (auth=%s)", route.Method, route.Path, route.Auth))
			continue
		}

		if _, ok := spec.operationByKey[routeCoverageKey(route.Method, route.Path)]; ok {
			continue
		}
		missing = append(missing, fmt.Sprintf("- %s %s (auth=%s)", route.Method, route.Path, route.Auth))
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("registered routes missing from OpenAPI (%d):\n%s", len(missing), strings.Join(missing, "\n"))
	}
}

func newRouteCoverageMux(t *testing.T) (*server, *http.ServeMux) {
	t.Helper()

	s, _, _ := newTestGateway(t)
	mux := http.NewServeMux()
	if err := s.registerRoutes(mux); err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}
	return s, mux
}

func loadOpenAPIRouteCoverage(t *testing.T) openAPIRouteCoverage {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(routeCoverageRepoRoot(t), "docs", "api", "openapi", "cordum-api.yaml"))
	if err != nil {
		t.Fatalf("read openapi spec: %v", err)
	}

	var raw struct {
		Paths map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatalf("parse openapi spec: %v", err)
	}

	coverage := openAPIRouteCoverage{
		operationByKey: make(map[string]openAPIOperation),
		methodsByPath:  make(map[string]map[string]bool),
		coveragePaths:  make(map[string]bool),
	}
	for path, node := range raw.Paths {
		if node.Kind != yaml.MappingNode {
			t.Fatalf("openapi path %s is not a mapping", path)
		}

		normalizedPath := normalizeRouteCoveragePath(path)
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := strings.ToLower(node.Content[i].Value)
			value := node.Content[i+1]

			switch key {
			case "x-any-method":
				if strings.EqualFold(value.Value, "true") {
					coverage.coveragePaths[normalizedPath] = true
				}
			case "x-subroute-dispatch":
				if strings.EqualFold(value.Value, "true") {
					dispatchPath := normalizedPath
					if !strings.HasSuffix(dispatchPath, "/") {
						dispatchPath += "/"
					}
					coverage.coveragePaths[dispatchPath] = true
				}
			case "get", "post", "put", "patch", "delete", "head", "options", "trace":
				if isDeprecatedRouteCoverageOp(value) {
					continue
				}
				op := openAPIOperation{
					Method:      key,
					Path:        path,
					OperationID: findRouteCoverageOperationID(value),
				}
				coverage.operations = append(coverage.operations, op)
				coverage.operationByKey[routeCoverageKey(op.Method, op.Path)] = op
				if coverage.methodsByPath[normalizedPath] == nil {
					coverage.methodsByPath[normalizedPath] = make(map[string]bool)
				}
				coverage.methodsByPath[normalizedPath][op.Method] = true
			}
		}
	}

	sort.Slice(coverage.operations, func(i, j int) bool {
		if coverage.operations[i].Path == coverage.operations[j].Path {
			return coverage.operations[i].Method < coverage.operations[j].Method
		}
		return coverage.operations[i].Path < coverage.operations[j].Path
	})

	return coverage
}

func (c openAPIRouteCoverage) pathSupportsAnyMethod(normalizedPath string) bool {
	if c.coveragePaths[normalizedPath] {
		return true
	}
	methods := c.methodsByPath[normalizedPath]
	if len(methods) == 0 {
		return false
	}
	for _, method := range []string{"get", "post", "put", "patch", "delete"} {
		if !methods[method] {
			return false
		}
	}
	return true
}

func routeCoverageRepoRoot(t *testing.T) string {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
}

func routeCoverageKey(method, path string) string {
	return strings.ToLower(strings.TrimSpace(method)) + " " + normalizeRouteCoveragePath(path)
}

func normalizeRouteCoveragePath(path string) string {
	return routeCoveragePathParam.ReplaceAllString(path, "{}")
}

func substituteRouteParams(path string) string {
	return routeCoveragePathParam.ReplaceAllString(path, "test")
}

func routeCoverageOpName(op openAPIOperation) string {
	if strings.TrimSpace(op.OperationID) != "" {
		return op.OperationID
	}
	return strings.ToUpper(op.Method) + " " + op.Path
}

func isDeprecatedRouteCoverageOp(node *yaml.Node) bool {
	if node == nil || node.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if strings.EqualFold(node.Content[i].Value, "deprecated") && strings.EqualFold(node.Content[i+1].Value, "true") {
			return true
		}
	}
	return false
}

func findRouteCoverageOperationID(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if strings.EqualFold(node.Content[i].Value, "operationId") {
			return strings.TrimSpace(node.Content[i+1].Value)
		}
	}
	return ""
}

func isRelaxedRouteCoveragePath(path string) bool {
	switch {
	case path == "/api/v1/stream":
		return true
	case strings.HasPrefix(path, "/mcp/"):
		return true
	case strings.HasPrefix(path, "/api/v1/mcp/"):
		return true
	default:
		return false
	}
}
