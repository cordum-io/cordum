// Command gen_docs_tables generates the doc enumeration tables that
// otherwise drift away from the code: the RBAC permission table, the REST
// route index, and the MCP tool catalog. Each table is written between a
// pair of stable HTML-comment markers in the target docs so the rest of
// the page is left untouched.
//
// The three sources of truth are read directly from code:
//   - RBAC permissions: auth.AllPermissions (compiled in).
//   - MCP tool catalog: mcp.RegisterAllTools into a fresh registry (compiled in).
//   - REST routes: the static registerRoute(mux, "METHOD /path", ...) string
//     literals in gateway.go, read via go/parser (no server construction).
//
// Usage:
//
//	go run ./tools/scripts/gen_docs_tables            # rewrite cordum/docs tables
//	go run ./tools/scripts/gen_docs_tables -check     # fail (exit 1) if out of date
//	go run ./tools/scripts/gen_docs_tables -site DIR  # also rewrite the Cordum-site tree at DIR
//
// The -check mode is wired to `make docs-tables-check` so CI fails when the
// committed tables no longer match the code.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/mcp"
)

// section identifies one generated region. Name is used in the marker pair
// (<!-- BEGIN:name --> ... <!-- END:name -->); source documents where the
// data comes from and is printed in the BEGIN marker for human readers.
type section struct {
	name   string
	render func() (string, error)
}

// target is a doc file plus the ordered list of sections it embeds.
type target struct {
	path     string
	sections []string
}

// routeSourceFiles holds the gateway source files whose registerRoute calls
// make up the registered-route surface, in the order their routes are listed.
// gateway.go carries the /api + /health REST surface; handlers_mcp.go adds the
// three /mcp/* transport routes (registered via registerMCPRoutes).
var routeSourceFiles = []string{
	"core/controlplane/gateway/gateway.go",
	"core/controlplane/gateway/handlers_mcp.go",
}

func main() {
	check := flag.Bool("check", false, "verify the committed tables match the code; exit 1 on drift")
	site := flag.String("site", "", "also (re)write the Cordum-site docs-site tree rooted at this directory")
	repoRoot := flag.String("root", ".", "cordum repo root (where docs/ and core/ live)")
	flag.Parse()

	sections := map[string]section{
		"rbac-permissions": {
			name:   "rbac-permissions",
			render: renderRBACTable,
		},
		"rest-routes": {
			name:   "rest-routes",
			render: func() (string, error) { return renderRouteTable(*repoRoot) },
		},
		"mcp-tools": {
			name:   "mcp-tools",
			render: renderMCPTable,
		},
	}

	// cordum/docs targets (in-repo, CI-gated).
	targets := []target{
		{path: filepath.Join(*repoRoot, "docs", "api-reference.md"), sections: []string{"rbac-permissions", "rest-routes"}},
		{path: filepath.Join(*repoRoot, "docs", "mcp-tools-reference.md"), sections: []string{"mcp-tools"}},
	}

	// Cordum-site mirror targets (manual, opt-in via -site; not present in cordum CI).
	if *site != "" {
		targets = append(targets,
			target{path: filepath.Join(*site, "docs", "api-reference", "api-overview.md"), sections: []string{"rbac-permissions", "rest-routes"}},
			target{path: filepath.Join(*site, "docs", "api-reference", "mcp-tools.md"), sections: []string{"mcp-tools"}},
		)
	}

	// Render every section once (the inputs are process-global).
	rendered := make(map[string]string, len(sections))
	for name, sec := range sections {
		body, err := sec.render()
		if err != nil {
			fatalf("render %s: %v", name, err)
		}
		rendered[name] = body
	}

	drift := false
	for _, t := range targets {
		changed, err := applyTarget(t, sections, rendered, *check)
		if err != nil {
			fatalf("%s: %v", t.path, err)
		}
		if changed {
			drift = true
			if *check {
				fmt.Fprintf(os.Stderr, "DRIFT: %s is out of date\n", t.path)
			} else {
				fmt.Printf("updated %s\n", t.path)
			}
		}
	}

	if *check && drift {
		fmt.Fprintln(os.Stderr, "docs tables are out of date — run `make docs-tables` and commit the result")
		os.Exit(1)
	}
	if *check {
		fmt.Println("docs tables are in sync with code")
	}
}

// applyTarget rewrites (or, in check mode, compares) every section region in
// one file. It returns true when the file's content would change.
func applyTarget(t target, sections map[string]section, rendered map[string]string, check bool) (bool, error) {
	raw, err := os.ReadFile(t.path)
	if err != nil {
		return false, err
	}
	content := string(raw)
	// Preserve the file's line-ending style: the generated region must use
	// the same EOL as the rest of the document or git sees spurious churn.
	nl := "\n"
	if strings.Contains(content, "\r\n") {
		nl = "\r\n"
	}
	updated := content
	for _, name := range t.sections {
		sec, ok := sections[name]
		if !ok {
			return false, fmt.Errorf("unknown section %q", name)
		}
		next, err := replaceSection(updated, sec, rendered[name], nl)
		if err != nil {
			return false, err
		}
		updated = next
	}
	if updated == content {
		return false, nil
	}
	if !check {
		if err := os.WriteFile(t.path, []byte(updated), 0o644); err != nil {
			return false, err
		}
	}
	return true, nil
}

// replaceSection swaps the text between the BEGIN/END markers for the freshly
// rendered table, leaving the markers (and everything outside them) intact.
// nl is the document's line ending; the rendered body (built with "\n") is
// re-encoded to nl, and content outside the region is left byte-for-byte.
func replaceSection(content string, sec section, body, nl string) (string, error) {
	begin := fmt.Sprintf("<!-- BEGIN:%s -->", sec.name)
	end := fmt.Sprintf("<!-- END:%s -->", sec.name)

	bi := strings.Index(content, begin)
	if bi < 0 {
		return "", fmt.Errorf("begin marker for section %q not found (expected exact line: %s)", sec.name, begin)
	}
	ei := strings.Index(content, end)
	if ei < 0 {
		return "", fmt.Errorf("end marker for section %q not found (expected: %s)", sec.name, end)
	}
	if ei < bi {
		return "", fmt.Errorf("end marker precedes begin marker for section %q", sec.name)
	}
	bodyNL := strings.ReplaceAll(body, "\n", nl)
	return content[:bi+len(begin)] + nl + nl + bodyNL + nl + nl + content[ei:], nil
}

// renderRBACTable emits one row per canonical permission, split into the
// resource (everything before the final dot) and action (after it).
func renderRBACTable() (string, error) {
	perms := append([]string(nil), auth.AllPermissions...)
	sort.Strings(perms)

	var b strings.Builder
	fmt.Fprintf(&b, "_Generated from `core/controlplane/gateway/auth/rbac.go` (`auth.AllPermissions`) — do not edit by hand; run `make docs-tables`. %d permissions._\n\n", len(perms))
	b.WriteString("| Permission | Resource | Action |\n")
	b.WriteString("|------------|----------|--------|\n")
	for _, p := range perms {
		resource, action := p, ""
		if i := strings.LastIndex(p, "."); i >= 0 {
			resource, action = p[:i], p[i+1:]
		}
		fmt.Fprintf(&b, "| `%s` | `%s` | `%s` |\n", p, resource, action)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// renderMCPTable enumerates the full tool catalog by registering every tool
// into a fresh registry and listing it back (names, descriptions, and the
// approval gate are all carried on the Tool definition).
func renderMCPTable() (string, error) {
	registry := mcp.NewToolRegistry()
	if err := mcp.RegisterAllTools(registry, stubBridge{}); err != nil {
		return "", fmt.Errorf("register tools: %w", err)
	}
	tools := registry.ListToolsUnfiltered()
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })

	var b strings.Builder
	fmt.Fprintf(&b, "_Generated from `core/mcp` via `RegisterAllTools` — do not edit by hand; run `make docs-tables`. %d tools._\n\n", len(tools))
	b.WriteString("| Tool | Approval | Scope | Description |\n")
	b.WriteString("|------|----------|-------|-------------|\n")
	for _, t := range tools {
		approval := "—"
		if t.RequiresApproval {
			approval = "required"
		}
		scope := t.ApprovalScope
		if scope == "" {
			scope = "—"
		}
		fmt.Fprintf(&b, "| `%s` | %s | `%s` | %s |\n", t.Name, approval, scope, escapePipes(t.Description))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

type route struct{ method, path string }

// renderRouteTable parses each gateway source file and lists every route
// registered through registerRoute(mux, "METHOD /path", ...). Routes are
// listed per file in source order, files in routeSourceFiles order, so the
// /api surface (gateway.go) comes first and the /mcp/* transport routes
// (handlers_mcp.go) come last.
func renderRouteTable(repoRoot string) (string, error) {
	var routes []route
	for _, rel := range routeSourceFiles {
		fileRoutes, err := parseRoutes(filepath.Join(repoRoot, rel))
		if err != nil {
			return "", err
		}
		routes = append(routes, fileRoutes...)
	}
	if len(routes) == 0 {
		return "", fmt.Errorf("no registerRoute calls found in %v", routeSourceFiles)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "_Generated from `%s` (registration order) — do not edit by hand; run `make docs-tables`. %d routes._\n\n", strings.Join(routeSourceFiles, "`, `"), len(routes))
	b.WriteString("| Method | Path |\n")
	b.WriteString("|--------|------|\n")
	for _, r := range routes {
		fmt.Fprintf(&b, "| %s | `%s` |\n", r.method, r.path)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// parseRoutes extracts the registerRoute(mux, "METHOD /path", ...) string
// literals from one Go source file, in source order.
func parseRoutes(path string) ([]route, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	var routes []route
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "registerRoute" || len(call.Args) < 2 {
			return true
		}
		lit, ok := call.Args[1].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		pattern, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		method, p, found := strings.Cut(pattern, " ")
		if !found {
			// A bare path with no method (rare) — record it verbatim.
			method, p = "", pattern
		}
		routes = append(routes, route{method: method, path: p})
		return true
	})
	return routes, nil
}

// escapePipes keeps a single-line description from breaking the table.
func escapePipes(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.ReplaceAll(s, "|", "\\|")
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
