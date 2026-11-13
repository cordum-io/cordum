// Package contract checks the repository's contract documents against the code
// they name, using static, repo-relative references.
package contract

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// repoRoot walks up from dir to the directory that holds go.mod.
func repoRoot(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve repo directory: %w", err)
	}
	for d := abs; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d, nil
		}
		if filepath.Dir(d) == d {
			return "", fmt.Errorf("no go.mod above %s", dir)
		}
	}
}

// repoFile joins a repo-relative, slash-separated path onto root. It rejects
// empty, absolute, drive-lettered, backslashed and non-canonical paths and any
// ".." segment, so the result cannot leave root.
func repoFile(root, rel string) (string, error) {
	if rel == "" || strings.ContainsAny(rel, `\:`) || path.Clean(rel) != rel || !filepath.IsLocal(filepath.FromSlash(rel)) {
		return "", fmt.Errorf("invalid repo path %q", rel)
	}
	return filepath.Join(root, filepath.FromSlash(rel)), nil
}

func goFile(file string) (*ast.File, error) {
	return parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
}

// goDecls lists a file's top-level funcs, types, vars and consts, and its
// methods as Type.Method (pointer mark and type parameters stripped).
func goDecls(file string) (map[string]bool, error) {
	f, err := goFile(file)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			name := d.Name.Name
			if d.Recv != nil {
				recv, _, _ := strings.Cut(strings.TrimPrefix(types.ExprString(d.Recv.List[0].Type), "*"), "[")
				name = recv + "." + name
			}
			set[name] = true
		case *ast.GenDecl:
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.TypeSpec:
					set[s.Name.Name] = true
				case *ast.ValueSpec:
					for _, n := range s.Names {
						set[n.Name] = true
					}
				}
			}
		}
	}
	return set, nil
}

// hasSymbol reports whether symbol resolves in the file at rel: a top-level
// declaration or Type.Method in a non-test .go file, else a literal anchor.
func hasSymbol(root, rel, symbol string) bool {
	full, err := repoFile(root, rel)
	if err != nil || symbol == "" || strings.HasSuffix(rel, "_test.go") {
		return false
	}
	if strings.HasSuffix(rel, ".go") {
		set, err := goDecls(full)
		return err == nil && set[symbol]
	}
	b, err := os.ReadFile(full)
	return err == nil && strings.Contains(string(b), symbol)
}

// hasTest reports whether a *_test.go file directly in the package directory
// declares top-level `func Name(*testing.T)` with the Test prefix. The check is
// static and ignores build tags.
func hasTest(root, pkg, name string) bool {
	dir, err := repoFile(root, pkg)
	if err != nil || !strings.HasPrefix(name, "Test") {
		return false
	}
	entries, _ := os.ReadDir(dir) // a missing directory has no entries
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := goFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == name && isTestSig(fd) {
				return true
			}
		}
	}
	return false
}

func isTestSig(fd *ast.FuncDecl) bool {
	ps := fd.Type.Params.List
	return len(ps) == 1 && len(ps[0].Names) <= 1 && fd.Type.TypeParams == nil &&
		fd.Type.Results == nil && types.ExprString(ps[0].Type) == "*testing.T"
}
