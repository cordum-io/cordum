package gateway

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

var crossTenantGlobalRoutes = map[string]string{
	"GET /healthz": "health.ProbeServer liveness or readiness probe; reports process and dependency status only, no tenant data",
	"GET /readyz":  "health.ProbeServer liveness or readiness probe; reports process and dependency status only, no tenant data",
	"GET /livez":   "health.ProbeServer liveness or readiness probe; reports process and dependency status only, no tenant data",
}

var crossTenantExistingCoverage = map[string][]string{
	"ANY /api/v1/stream": {"TestEdgeEventStreamTenantFilteringAndBusPacketRegression"},
}

var crossTenantOpenFindings = map[string]string{}

var crossTenantPending = map[string][]string{
	"PR1-02-WF": {"/api/v1/workflows", "/api/v1/workflow-runs", "/api/v1/artifacts", "/api/v1/memory", "/api/v1/traces", "/api/v1/locks", "/api/v1/schemas", "/api/v1/topics"},
	// Slice separator: keep unchanged when removing a pending block.
	"PR1-02-FLEET": {"/api/v1/workers", "/api/v1/pools", "/api/v1/agents", "/api/v1/delegations"},
	// Slice separator: keep unchanged when removing a pending block.
	"PR1-02-POLICY": {"/api/v1/policy"},
	// Slice separator: keep unchanged when removing a pending block.
	"PR1-02-IDENTITY": {"/api/v1/auth", "/api/v1/users"},
	// Slice separator: keep unchanged when removing a pending block.
	"PR1-02-PLATFORM": {"/api/v1/packs", "/api/v1/marketplace", "/api/v1/config", "/api/v1/evals", "/api/v1/governance", "/api/v1/license", "/api/v1/telemetry", "/api/v1/status", "/api/v1/admin", "/api/v1/copilot", "/health", "/api/v1/health"},
	// Slice separator: keep unchanged when removing a pending block.
	"PR1-02-EDGE-MCP": {"/api/v1/edge", "/api/v1/mcp", "/mcp", "/api/v1/audit"},
}

var crossTenantRowKey = regexp.MustCompile(`^(?:M[0-9][A-Z]?|PR[0-9])-[A-Z0-9]+(?:-[A-Z0-9]+)*$`)

func TestCrossTenantSweepRouteManifest(t *testing.T) {
	sw := newCrossTenantSweep(t)
	live := crossTenantLiveRoutes(t, sw)
	tests, issues := crossTenantSourceInventory(t, live)
	classes, entryIssues := crossTenantExplicitClasses(tests)
	issues = append(issues, entryIssues...)
	pending, pendingIssues := crossTenantPendingClasses(live)
	issues = append(issues, pendingIssues...)
	for route := range classes {
		if !live[route] {
			issues = append(issues, "stale entry: "+route)
		}
	}
	counts := map[string]int{}
	for route := range live {
		owners := classes[route]
		if len(owners) == 0 {
			owners = pending[route]
		}
		switch len(owners) {
		case 0:
			issues = append(issues, "unclassified: "+route)
		case 1:
			counts[owners[0]]++
		default:
			sort.Strings(owners)
			issues = append(issues, fmt.Sprintf("ambiguous: %s (%s)", route, strings.Join(owners, ", ")))
		}
	}
	sort.Strings(issues)
	for _, issue := range issues {
		t.Error(issue)
	}
	t.Logf("route manifest: %d live routes; classification counts=%v", len(live), counts)
}

func crossTenantLiveRoutes(t *testing.T, sw *crossTenantSweep) map[string]bool {
	t.Helper()
	live := map[string]bool{}
	for _, route := range sw.server.Routes() {
		method := route.Method
		if method == "" {
			method = "ANY"
		}
		key := method + " " + route.Path
		if live[key] {
			t.Errorf("duplicate live route: %s", key)
		}
		live[key] = true
	}
	service := "GRPC " + pb.CordumApi_ServiceDesc.ServiceName + "/"
	for _, method := range pb.CordumApi_ServiceDesc.Methods {
		live[service+method.MethodName] = true
	}
	for _, stream := range pb.CordumApi_ServiceDesc.Streams {
		live[service+stream.StreamName] = true
	}
	for _, path := range []string{"/healthz", "/readyz", "/livez"} {
		key := "GET " + path
		_, pattern := sw.mux.Handler(httptest.NewRequest("GET", path, nil))
		if pattern != key {
			t.Errorf("probe route %s mounted as %q", key, pattern)
		}
		live[key] = true
	}
	return live
}

func crossTenantSourceInventory(t *testing.T, live map[string]bool) (map[string]bool, []string) {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("find gateway sources: %v", err)
	}
	tests, issues := map[string]bool{}, []string{}
	fset := token.NewFileSet()
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, path, data, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(path, "_test.go") {
			matches, err := build.Default.MatchFile(".", path)
			if err != nil {
				t.Fatal(err)
			}
			if !matches {
				continue
			}
			for _, name := range crossTenantTestNames(file) {
				tests[name] = true
			}
			continue
		}
		issues = append(issues, crossTenantStaticRoutes(fset, file, live)...)
	}
	return tests, issues
}

func crossTenantTestNames(file *ast.File) []string {
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if comment.Pos() < file.Package && (strings.HasPrefix(comment.Text, "//go:build ") || strings.HasPrefix(comment.Text, "// +build ")) {
				return nil
			}
		}
	}
	var names []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") {
			continue
		}
		suffix := strings.TrimPrefix(fn.Name.Name, "Test")
		if suffix == "" || !unicode.IsLower([]rune(suffix)[0]) {
			names = append(names, fn.Name.Name)
		}
	}
	return names
}

func crossTenantStaticRoutes(fset *token.FileSet, file *ast.File, live map[string]bool) []string {
	var issues []string
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "registerRoute" {
			return true
		}
		var literal *ast.BasicLit
		if len(call.Args) > 1 {
			literal, _ = call.Args[1].(*ast.BasicLit)
		}
		if literal == nil || literal.Kind != token.STRING {
			issues = append(issues, fmt.Sprintf("non-literal registerRoute at %s", fset.Position(call.Pos())))
			return true
		}
		pattern, err := strconv.Unquote(literal.Value)
		if err != nil {
			issues = append(issues, fmt.Sprintf("invalid route at %s: %v", fset.Position(call.Pos()), err))
			return true
		}
		method, path := parseRoutePattern(pattern)
		if !live[method+" "+path] {
			issues = append(issues, fmt.Sprintf("static route not live: %s %s at %s", method, path, fset.Position(call.Pos())))
		}
		return true
	})
	return issues
}

func crossTenantExplicitClasses(tests map[string]bool) (map[string][]string, []string) {
	classes, issues := map[string][]string{}, []string{}
	for key, cases := range crossTenantFamilies {
		if !crossTenantRowKey.MatchString(key) {
			issues = append(issues, "key format: "+key)
		}
		for _, c := range cases {
			classes[c.Route] = append(classes[c.Route], "covered "+key)
		}
	}
	for route, names := range crossTenantExistingCoverage {
		classes[route] = append(classes[route], "existing coverage")
		if len(names) == 0 {
			issues = append(issues, "empty existing coverage: "+route)
		}
		for _, name := range names {
			if !tests[name] {
				issues = append(issues, "unknown test: "+name+" for "+route)
			}
		}
	}
	for route, reason := range crossTenantGlobalRoutes {
		classes[route] = append(classes[route], "global")
		if strings.TrimSpace(reason) == "" {
			issues = append(issues, "empty global reason: "+route)
		}
	}
	for route, key := range crossTenantOpenFindings {
		classes[route] = append(classes[route], "open finding")
		if !crossTenantRowKey.MatchString(key) {
			issues = append(issues, "key format: "+key+" for "+route)
		}
	}
	return classes, issues
}

func crossTenantPendingClasses(live map[string]bool) (map[string][]string, []string) {
	pending, issues := map[string][]string{}, []string{}
	for key, prefixes := range crossTenantPending {
		if !crossTenantRowKey.MatchString(key) {
			issues = append(issues, "key format: "+key)
		}
		if len(prefixes) == 0 {
			issues = append(issues, "empty pending block: "+key)
		}
		for _, prefix := range prefixes {
			matches := 0
			for route := range live {
				_, path, _ := strings.Cut(route, " ")
				if !strings.HasPrefix(route, "GRPC ") && (path == prefix || strings.HasPrefix(path, prefix+"/")) {
					pending[route] = append(pending[route], "pending "+key)
					matches++
				}
			}
			if matches == 0 {
				issues = append(issues, "stale prefix: "+prefix+" in "+key)
			}
		}
	}
	return pending, issues
}
