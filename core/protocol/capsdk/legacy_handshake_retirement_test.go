package capsdk

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLegacyUnsignedHandshakeContractIsRetired(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob capsdk sources: %v", err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		assertNoLegacyHandshakeSymbols(t, path, parsed)
	}
}

func assertNoLegacyHandshakeSymbols(t *testing.T, path string, file *ast.File) {
	t.Helper()
	legacy := map[string]struct{}{
		"HandshakeRequest": {}, "HandshakeResponse": {},
		"WorkerHandshakeSubject": {}, "WorkerHandshakeRenewSubject": {},
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.TypeSpec:
			if _, found := legacy[value.Name.Name]; found {
				t.Errorf("%s exports retired unsigned handshake type %s", path, value.Name.Name)
			}
		case *ast.ValueSpec:
			assertNoLegacyHandshakeValues(t, path, value, legacy)
		}
		return true
	})
}

func assertNoLegacyHandshakeValues(t *testing.T, path string, spec *ast.ValueSpec, legacy map[string]struct{}) {
	t.Helper()
	for _, name := range spec.Names {
		if _, found := legacy[name.Name]; found {
			t.Errorf("%s exports retired unsigned handshake value %s", path, name.Name)
		}
	}
	for _, expression := range spec.Values {
		literal, ok := expression.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			continue
		}
		value, err := strconv.Unquote(literal.Value)
		if err == nil && (value == "sys.worker.handshake" || value == "sys.worker.handshake.renew") {
			t.Errorf("%s retains retired unsigned handshake subject %q", path, value)
		}
	}
}
