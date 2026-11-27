package identity

// The coverage guard reads the module's source, not its build, so it also
// sees runners in packages this test does not import. It checks that the two
// lists match the declared consts and every Entry has one runner (see package comment).

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	identityImport = "github.com/cordum/cordum/tests/identity"
	runnerPrefix   = "TestAudienceMatrix"
)

// goFile is one parsed source file. Positions name it by its slash path from
// the walked root.
type goFile struct {
	fset *token.FileSet
	file *ast.File
	src  []byte
}

func (g goFile) at(p token.Pos) string {
	pos := g.fset.Position(p)
	return fmt.Sprintf("%s:%d", pos.Filename, pos.Line)
}

// runCall is one Run call: the Entry const it names and where it is.
type runCall struct {
	entry string
	at    string
}

// moduleRoot walks up from the working directory to the cordum module root.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if b, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil {
			for _, line := range strings.Split(string(b), "\n") {
				if f := strings.Fields(line); len(f) == 2 && f[0] == "module" && f[1] == "github.com/cordum/cordum" {
					return dir
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod for github.com/cordum/cordum above the working directory")
		}
		dir = parent
	}
}

// parseTree parses, in lexical order, every .go file under root whose slash
// path keep accepts.
func parseTree(t *testing.T, root string, keep func(rel string) bool) []goFile {
	t.Helper()
	fset := token.NewFileSet()
	var files []goFile
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && skipDir(path, rel, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || !keep(rel) {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		files = append(files, goFile{fset, f, src})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// skipDir skips what the go tool skips (testdata, and names starting with .
// or _), other modules, vendored and Node trees, and the dashboard.
func skipDir(path, rel, name string) bool {
	if name == "testdata" || name == "vendor" || name == "node_modules" || rel == "dashboard" ||
		strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
		return true
	}
	_, err := os.Stat(filepath.Join(path, "go.mod"))
	return err == nil
}

// typedConsts maps each const of type typ declared in f to its string value.
// A value that is not a string literal maps to "".
func typedConsts(f *ast.File, typ string) map[string]string {
	out := map[string]string{}
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.CONST {
			continue
		}
		for _, s := range g.Specs {
			v := s.(*ast.ValueSpec)
			if id, ok := v.Type.(*ast.Ident); !ok || id.Name != typ {
				continue
			}
			for i, n := range v.Names {
				out[n.Name] = ""
				if i < len(v.Values) {
					if lit, ok := v.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						out[n.Name], _ = strconv.Unquote(lit.Value)
					}
				}
			}
		}
	}
	return out
}

// listProblems reports each const missing from list and each list value that
// no const declares.
func listProblems[T ~string](name string, consts map[string]string, list []T) []string {
	var probs []string
	declared := map[string]bool{}
	for c, v := range consts {
		declared[v] = true
		if !slices.Contains(list, T(v)) {
			probs = append(probs, fmt.Sprintf("const %s (%q) is not in %s", c, v, name))
		}
	}
	for _, v := range list {
		if !declared[string(v)] {
			probs = append(probs, fmt.Sprintf("%s holds %q, which no const declares", name, v))
		}
	}
	return probs
}

// importName returns the name f gives its import of path, "" when f does not
// import it, and the import's position.
func importName(f *ast.File, path string) (string, token.Pos) {
	for _, imp := range f.Imports {
		if p, err := strconv.Unquote(imp.Path.Value); err != nil || p != path {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name, imp.Pos()
		}
		return path[strings.LastIndex(path, "/")+1:], imp.Pos()
	}
	return "", token.NoPos
}

// pkgSel returns X when e is the selector pkg.X.
func pkgSel(e ast.Expr, pkg string) (string, bool) {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || id.Name != pkg {
		return "", false
	}
	return sel.Sel.Name, true
}

// scanRuns collects every Run call made through the identity import. It
// reports each use the coverage count cannot trust.
func scanRuns(files []goFile) ([]runCall, []string) {
	var calls []runCall
	var probs []string
	for _, g := range files {
		switch name, pos := importName(g.file, identityImport); name {
		case "", "_":
		case ".":
			probs = append(probs, g.at(pos)+": dot-import of tests/identity hides its Run calls from this guard")
		default:
			c, p := fileRuns(g, name)
			calls, probs = append(calls, c...), append(probs, p...)
		}
	}
	return calls, probs
}

// fileRuns collects the Run calls in one file that imports identity as pkg.
func fileRuns(g goFile, pkg string) (calls []runCall, probs []string) {
	for _, d := range g.file.Decls {
		fn, _ := d.(*ast.FuncDecl)
		runner := fn != nil && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, runnerPrefix)
		called := map[ast.Expr]bool{}
		ast.Inspect(d, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := pkgSel(call.Fun, pkg); ok && sel == "Run" {
					called[call.Fun] = true
					switch entry, ok := entryArg(call, pkg); {
					case !runner:
						probs = append(probs, g.at(call.Pos())+": Run is called outside a top-level "+runnerPrefix+" test")
					case !ok:
						probs = append(probs, g.at(call.Pos())+": Run's entry argument is not a constant selector "+pkg+".<Entry>")
					default:
						calls = append(calls, runCall{entry, g.at(call.Pos())})
					}
				}
			}
			if e, ok := n.(*ast.SelectorExpr); ok && !called[e] {
				if sel, ok := pkgSel(e, pkg); ok && sel == "Run" {
					probs = append(probs, g.at(e.Pos())+": Run is used as a value, not called")
				}
			}
			return true
		})
	}
	return calls, probs
}

// entryArg returns the const name when Run's entry argument is pkg.<Const>.
func entryArg(call *ast.CallExpr, pkg string) (string, bool) {
	if len(call.Args) < 2 {
		return "", false
	}
	return pkgSel(call.Args[1], pkg)
}

// coverageProblems checks that every entry has exactly one Run call and that
// every call names an Entry const that is in the list.
func coverageProblems(calls []runCall, consts map[string]string, entries []Entry) []string {
	var probs []string
	at := map[Entry][]string{}
	for _, c := range calls {
		v, ok := consts[c.entry]
		if !ok || !slices.Contains(entries, Entry(v)) {
			probs = append(probs, fmt.Sprintf("%s: Run names %s, which is not an Entry in Entries", c.at, c.entry))
			continue
		}
		at[Entry(v)] = append(at[Entry(v)], c.at)
	}
	for _, e := range entries {
		switch n := len(at[e]); {
		case n == 0:
			probs = append(probs, fmt.Sprintf("entry %q has no runner", e))
		case n > 1:
			probs = append(probs, fmt.Sprintf("entry %q has %d runners, want 1: %s", e, n, strings.Join(at[e], ", ")))
		}
	}
	return probs
}

// report fails t with every problem, sorted, and one hint on how to fix them.
func report(t *testing.T, hint string, probs []string) {
	t.Helper()
	if len(probs) == 0 {
		return
	}
	slices.Sort(probs)
	t.Errorf("%d problem(s):\n  %s\n%s", len(probs), strings.Join(probs, "\n  "), hint)
}

func TestGuardRunnerCoverage(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "matrix.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	creds, entries := typedConsts(f, "Credential"), typedConsts(f, "Entry")
	probs := matrix{Credentials, Entries, grants}.problems()
	probs = append(probs, listProblems("Credentials", creds, Credentials)...)
	probs = append(probs, listProblems("Entries", entries, Entries)...)
	calls, scan := scanRuns(parseTree(t, moduleRoot(t), func(rel string) bool { return strings.HasSuffix(rel, "_test.go") }))
	probs = append(probs, scan...)
	probs = append(probs, coverageProblems(calls, entries, Entries)...)
	report(t, "Every Credential and Entry const belongs in its list, and every Entry needs exactly one runner: "+
		"a top-level "+runnerPrefix+"<Name>(t *testing.T) that calls Run(t, identity.<Entry>, ...) once, "+
		"as the tests/identity package comment says.", probs)
}
