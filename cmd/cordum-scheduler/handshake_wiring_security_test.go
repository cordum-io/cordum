package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// This source-level boot test complements the subscriber's behavioral tests:
// boot must assemble the complete authority, install both admission and
// request/reply paths, and start them before Engine.Start.
func TestSchedulerBootWiresWorkerHandshakeResponder(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list scheduler sources: %v", err)
	}
	seen := map[string]bool{}
	fset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.Ident:
				seen[value.Name] = true
			case *ast.SelectorExpr:
				seen[value.Sel.Name] = true
			}
			return true
		})
	}
	required := []string{
		"loadHandshakeSecurityConfig",
		"initializeHandshakeSecurity",
		"NewHandshakeService",
		"WithSessionMiddleware",
		"NewHandshakeSubscriber",
		"Start",
		"Close",
	}
	missing := make([]string, 0, len(required))
	for _, name := range required {
		if !seen[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) != 0 {
		sort.Strings(missing)
		t.Fatalf("scheduler boot has no complete worker-handshake responder wiring; missing AST identifiers: %v", missing)
	}
	assertHandshakeBootOrder(t)
}

func assertHandshakeBootOrder(t *testing.T) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	positions := map[string]token.Pos{}
	ast.Inspect(mainFunction(file), func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if name := bootCallName(call.Fun); name != "" {
			positions[name] = call.Pos()
		}
		return true
	})
	ordered := []string{
		"loadHandshakeSecurityConfig",
		"initializeHandshakeSecurity",
		"handshakeSubscriber.Start",
		"engine.Start",
	}
	for i, name := range ordered {
		if positions[name] == token.NoPos {
			t.Fatalf("scheduler boot call %s missing", name)
		}
		if i > 0 && positions[ordered[i-1]] >= positions[name] {
			t.Fatalf("scheduler boot order must be %v", ordered)
		}
	}
}

func mainFunction(file *ast.File) ast.Node {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == "main" {
			return function.Body
		}
	}
	return nil
}

func bootCallName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		if receiver, ok := value.X.(*ast.Ident); ok {
			return receiver.Name + "." + value.Sel.Name
		}
	}
	return ""
}
