// Command openapi-audit diffs the gateway's registered HTTP routes against
// the OpenAPI spec and reports gaps in either direction.
//
// It parses every *.go file under the gateway package with go/parser, collects
// calls of the form mux.HandleFunc("METHOD /path", ...),
// mux.HandleFunc("/path", ...), s.registerRoute(mux, "METHOD /path", ...), or
// s.registerRoute(mux, "/path", ...), and loads the spec via gopkg.in/yaml.v3.
// A route registered without a method prefix is treated as any-method: the
// corresponding spec path must either list all standard HTTP methods or
// declare x-any-method: true.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// standardMethods is the set of HTTP methods the audit recognises. An
// any-method route is considered covered if the spec documents every method
// in this set, or if the spec path declares x-any-method: true.
var standardMethods = []string{"get", "post", "put", "patch", "delete"}

// Route is a parsed HTTP registration. Method is lower-case or "" for
// any-method registrations.
type Route struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	File   string `json:"file"`
	Line   int    `json:"line"`
}

// SpecOp is a single operation parsed out of the OpenAPI paths block.
type SpecOp struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// Report is the structured output emitted with --json.
type Report struct {
	MissingFromSpec []Route  `json:"missing_from_spec"`
	UnroutedInSpec  []SpecOp `json:"unrouted_in_spec"`
	Routes          int      `json:"routes_seen"`
	SpecOps         int      `json:"spec_ops_seen"`
}

// pathParam normalises template parameters so {id} and {user_id} in the spec
// compare equal to their mux equivalents.
var pathParam = regexp.MustCompile(`\{[^/}]+\}`)

func normalisePath(p string) string {
	return pathParam.ReplaceAllString(p, "{}")
}

// parseHandlerPattern splits a mux pattern into (method, path). Go 1.22
// accepts patterns like "GET /foo" (method-prefixed) and "/foo" (any-method).
// Patterns with a host prefix are rejected because the gateway does not use
// them; a future change may need to extend this.
func parseHandlerPattern(pattern string) (method, path string, err error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return "", "", errors.New("empty pattern")
	}
	if !strings.HasPrefix(pattern, "/") {
		// Expect "METHOD /path"; split on the first space.
		i := strings.IndexByte(pattern, ' ')
		if i <= 0 || i == len(pattern)-1 {
			return "", "", fmt.Errorf("malformed pattern %q", pattern)
		}
		method = strings.ToLower(pattern[:i])
		path = strings.TrimSpace(pattern[i+1:])
		if !strings.HasPrefix(path, "/") {
			return "", "", fmt.Errorf("pattern path missing leading slash: %q", pattern)
		}
		return method, path, nil
	}
	return "", pattern, nil
}

// collectRoutes walks a directory, parses every non-test .go file, and
// extracts string-literal mux.HandleFunc/registerRoute patterns. Test files are
// skipped so synthetic routes in _test.go fixtures do not pollute production
// coverage.
func collectRoutes(dir string) ([]Route, error) {
	fset := token.NewFileSet()
	var routes []Route
	walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if perr != nil {
			return fmt.Errorf("parse %s: %w", path, perr)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel == nil {
				return true
			}
			var patternArgIndex int
			switch sel.Sel.Name {
			case "HandleFunc":
				patternArgIndex = 0
			case "registerRoute":
				patternArgIndex = 1
			default:
				return true
			}
			if len(call.Args) <= patternArgIndex {
				return true
			}
			lit, ok := call.Args[patternArgIndex].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			raw, uerr := unquoteGoString(lit.Value)
			if uerr != nil {
				return true
			}
			method, rpath, perr := parseHandlerPattern(raw)
			if perr != nil {
				return true
			}
			pos := fset.Position(lit.Pos())
			routes = append(routes, Route{
				Method: method,
				Path:   rpath,
				File:   pos.Filename,
				Line:   pos.Line,
			})
			return true
		})
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return routes, nil
}

// isDeprecatedOp returns true when an operation node declares
// `deprecated: true`. The node is the mapping value under e.g. `get:`.
func isDeprecatedOp(n *yaml.Node) bool {
	if n == nil || n.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if strings.ToLower(n.Content[i].Value) == "deprecated" &&
			strings.ToLower(n.Content[i+1].Value) == "true" {
			return true
		}
	}
	return false
}

// unquoteGoString handles both interpreted ("foo") and raw (`foo`) string
// literals. strconv.Unquote would work too, but we keep the dependency surface
// minimal and explicit about the two cases we actually parse.
func unquoteGoString(s string) (string, error) {
	if len(s) < 2 {
		return "", fmt.Errorf("bad literal %q", s)
	}
	switch s[0] {
	case '"':
		// Interpreted literal — defer escape handling to fmt.Sscanf via %q.
		var out string
		if _, err := fmt.Sscanf(s, "%q", &out); err != nil {
			return "", err
		}
		return out, nil
	case '`':
		return s[1 : len(s)-1], nil
	}
	return "", fmt.Errorf("bad literal %q", s)
}

// specDoc mirrors the subset of the OpenAPI document we care about. Using
// yaml.Node rather than plain maps lets us preserve the distinction between
// "x-any-method: true" (a boolean extension) and "x-any-method: \"true\"".
type specDoc struct {
	Paths map[string]yaml.Node `yaml:"paths"`
}

// loadSpecOps returns a slice of SpecOp and a map of path -> anyMethod flag.
// An op appearing under an x-any-method path is still enumerated as
// "<method> <path>" for each declared method so the diff can assert both
// directions accurately. `x-subroute-dispatch: true` is a companion
// extension for Go 1.22 sub-mux dispatch: it lets the canonical collection
// path (for example `/api/v1/evals/datasets`) declare that an internal
// catch-all route exists at `/api/v1/evals/datasets/` without forcing the
// OpenAPI document to model an invalid trailing-slash path.
func loadSpecOps(specPath string) ([]SpecOp, map[string]bool, error) {
	data, err := os.ReadFile(specPath)
	if err != nil {
		return nil, nil, err
	}
	var doc specDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, fmt.Errorf("parse spec: %w", err)
	}
	var ops []SpecOp
	anyMethod := make(map[string]bool)
	for path, node := range doc.Paths {
		if node.Kind != yaml.MappingNode {
			return nil, nil, fmt.Errorf("spec path %s is not a mapping", path)
		}
		var opsHere int
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := strings.ToLower(node.Content[i].Value)
			switch key {
			case "x-any-method":
				if node.Content[i+1].Value == "true" {
					anyMethod[path] = true
				}
				continue
			case "x-subroute-dispatch":
				if node.Content[i+1].Value == "true" {
					dispatchPath := path
					if !strings.HasSuffix(dispatchPath, "/") {
						dispatchPath += "/"
					}
					anyMethod[dispatchPath] = true
				}
				continue
			case "get", "post", "put", "patch", "delete", "head", "options", "trace":
				// Skip deprecated ops — they document a renamed/removed
				// path that clients may still call but the gateway no
				// longer registers. The audit only cares about LIVE ops.
				if isDeprecatedOp(node.Content[i+1]) {
					opsHere++
					continue
				}
				ops = append(ops, SpecOp{Method: key, Path: path})
				opsHere++
			default:
				// Parameters, summary, description, and x-* extensions fall here;
				// they are not operations and therefore do not contribute to coverage.
			}
		}
		if opsHere == 0 && !anyMethod[path] {
			return nil, nil, fmt.Errorf("spec path %s has no operations", path)
		}
	}
	sort.Slice(ops, func(i, j int) bool {
		if ops[i].Path != ops[j].Path {
			return ops[i].Path < ops[j].Path
		}
		return ops[i].Method < ops[j].Method
	})
	return ops, anyMethod, nil
}

// diff computes the two gap lists. A route with no method (any-method) is
// satisfied when the spec either declares x-any-method on that path or lists
// every standard method. A spec op is satisfied when a matching route exists,
// or when an any-method route covers the same path.
func diff(routes []Route, ops []SpecOp, anyMethod map[string]bool) ([]Route, []SpecOp) {
	routeMethods := make(map[string]map[string]Route) // normPath -> method -> route
	routeAnyMethod := make(map[string]Route)          // normPath -> route
	for _, r := range routes {
		np := normalisePath(r.Path)
		if r.Method == "" {
			routeAnyMethod[np] = r
			continue
		}
		if routeMethods[np] == nil {
			routeMethods[np] = make(map[string]Route)
		}
		routeMethods[np][r.Method] = r
	}
	specByPath := make(map[string]map[string]bool) // normPath -> method -> present
	for _, op := range ops {
		np := normalisePath(op.Path)
		if specByPath[np] == nil {
			specByPath[np] = make(map[string]bool)
		}
		specByPath[np][op.Method] = true
	}
	specAnyMethod := make(map[string]bool) // normPath -> x-any-method set
	for path := range anyMethod {
		specAnyMethod[normalisePath(path)] = true
	}

	var missing []Route
	for np, methods := range routeMethods {
		for m, r := range methods {
			if specAnyMethod[np] {
				continue
			}
			if specByPath[np][m] {
				continue
			}
			missing = append(missing, r)
		}
	}
	for np, r := range routeAnyMethod {
		if specAnyMethod[np] {
			continue
		}
		// Any-method registered but spec doesn't declare x-any-method; require
		// every standard method to be listed, else flag as missing.
		covered := true
		for _, m := range standardMethods {
			if !specByPath[np][m] {
				covered = false
				break
			}
		}
		if !covered {
			missing = append(missing, r)
		}
	}
	sort.Slice(missing, func(i, j int) bool {
		if missing[i].Path != missing[j].Path {
			return missing[i].Path < missing[j].Path
		}
		return missing[i].Method < missing[j].Method
	})

	// Collect any-method prefix roots so sub-path ops can be considered
	// covered when they share a prefix with an x-any-method route. This is
	// the gateway's "sub-mux dispatch" pattern: a single catch-all
	// HandleFunc at `/api/v1/foo/` dispatches internally to concrete
	// `/api/v1/foo/{id}`, `/api/v1/foo/by-name/{name}` ops because Go's
	// strict mux refuses to register ambiguous sibling patterns. Each
	// dispatched op is still documented separately; the prefix only
	// signals "coverage is delegated".
	var anyPrefixes []string
	for np := range specAnyMethod {
		if strings.HasSuffix(np, "/") {
			anyPrefixes = append(anyPrefixes, np)
		}
	}

	var unrouted []SpecOp
	for _, op := range ops {
		np := normalisePath(op.Path)
		if _, ok := routeAnyMethod[np]; ok {
			continue
		}
		if routeMethods[np][op.Method] != (Route{}) {
			continue
		}
		// Op is covered by a prefix-level any-method route when its path
		// starts with the prefix (the prefix itself counts as routed via
		// its own any-method declaration earlier in this loop).
		covered := false
		for _, prefix := range anyPrefixes {
			if strings.HasPrefix(np, prefix) {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		unrouted = append(unrouted, op)
	}
	return missing, unrouted
}

func writeText(w *os.File, report Report) {
	_, _ = fmt.Fprintf(w, "Routes seen:   %d\n", report.Routes)
	_, _ = fmt.Fprintf(w, "Spec ops seen: %d\n", report.SpecOps)
	_, _ = fmt.Fprintln(w)
	if len(report.MissingFromSpec) == 0 {
		_, _ = fmt.Fprintln(w, "Routes missing from spec: (none)")
	} else {
		_, _ = fmt.Fprintln(w, "Routes missing from spec:")
		for _, r := range report.MissingFromSpec {
			m := r.Method
			if m == "" {
				m = "ANY"
			}
			_, _ = fmt.Fprintf(w, "  %-6s %s  (%s:%d)\n", strings.ToUpper(m), r.Path, r.File, r.Line)
		}
	}
	_, _ = fmt.Fprintln(w)
	if len(report.UnroutedInSpec) == 0 {
		_, _ = fmt.Fprintln(w, "Spec ops without a route: (none)")
	} else {
		_, _ = fmt.Fprintln(w, "Spec ops without a route:")
		for _, op := range report.UnroutedInSpec {
			_, _ = fmt.Fprintf(w, "  %-6s %s\n", strings.ToUpper(op.Method), op.Path)
		}
	}
}

// run is factored out of main so tests can exercise the full tool end-to-end
// without calling os.Exit.
func run(specPath, gatewayDir string, asJSON bool, stdout, stderr *os.File) int {
	routes, err := collectRoutes(gatewayDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	ops, anyMethod, err := loadSpecOps(specPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	missing, unrouted := diff(routes, ops, anyMethod)
	report := Report{
		MissingFromSpec: missing,
		UnroutedInSpec:  unrouted,
		Routes:          len(routes),
		SpecOps:         len(ops),
	}
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 2
		}
	} else {
		writeText(stdout, report)
	}
	if len(missing) == 0 && len(unrouted) == 0 {
		return 0
	}
	return 1
}

func main() {
	spec := flag.String("spec", "docs/api/openapi/cordum-api.yaml", "path to the OpenAPI spec")
	gwDir := flag.String("gateway-dir", "core/controlplane/gateway", "gateway handler directory")
	asJSON := flag.Bool("json", false, "emit report as JSON")
	flag.Parse()
	os.Exit(run(*spec, *gwDir, *asJSON, os.Stdout, os.Stderr))
}
