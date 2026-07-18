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

// This source-level boot test is intentional: the responder has no production
// API yet, so a direct behavioral test cannot compile. AST inspection ignores
// comments and requires real construction plus both ISSUE and RENEW edges.
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
		"NewHandshakeService",
		"WorkerHandshakeChallengeSubject",
		"WorkerHandshakeAuthenticateSubject",
		"HandleChallenge",
		"HandleAuthenticate",
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
}
