package tenancy

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cordum/cordum/tests/contract"
)

type storeKeyEntry struct{ Pattern, Package, Binding, Coverage string }
type notTenantKey struct{ Pattern, Package, Reason string }

func TestTenantIsolationManifestGuard(t *testing.T) {
	root := filepath.Join("..", "..")
	if info, err := os.Stat(filepath.Join(root, "go.mod")); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("module go.mod required at %s: %v", root, err)
	}
	inventory, err := contract.LoadDataInventory(filepath.Join(root, "docs", "contract", "data-inventory.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(storeKeyManifest) == 0 {
		t.Fatal("manifest must not be empty")
	}
	tests, err := testFuncDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	literals, err := packageLiterals(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range checkManifest(inventoryKeys(inventory), storeKeyManifest, notTenantKeys, tests, literals) {
		t.Error(message)
	}
}

func inventoryKeys(inventory *contract.DataInventory) []contract.DataKeyRef {
	var keys []contract.DataKeyRef
	for _, section := range [][]contract.DataEntry{inventory.EndpointOnly, inventory.ControlPlane} {
		for _, entry := range section {
			if entry.Storage != nil {
				keys = append(keys, entry.Storage.RedisKeys...)
			}
		}
	}
	return keys
}

func checkManifest(keys []contract.DataKeyRef, manifest []storeKeyEntry, exclusions []notTenantKey, tests, literals map[string][]string) []string {
	var messages []string
	covered := make([]bool, len(keys))
	mark := func(pattern, pkg string) bool {
		found := false
		for i, key := range keys {
			if path.Dir(key.Path) == pkg && patternCovers(key.Pattern, pattern) {
				covered[i], found = true, true
			}
		}
		return found
	}
	for _, entry := range manifest {
		label := entry.Package + " " + entry.Pattern
		prefix, _, _ := strings.Cut(entry.Pattern, "{")
		if !mark(entry.Pattern, entry.Package) && !hasPrefixLiteral(literals[entry.Package], prefix) && !hasComposedPrefix(literals[entry.Package], prefix) {
			messages = append(messages, "stale manifest "+label)
		}
		messages = append(messages, entryMessages(entry, tests)...)
	}
	for _, entry := range exclusions {
		label := entry.Package + " " + entry.Pattern
		if !mark(entry.Pattern, entry.Package) {
			messages = append(messages, "stale classification "+label)
		}
		if strings.TrimSpace(entry.Reason) == "" {
			messages = append(messages, "empty reason "+label)
		}
	}
	for i, key := range keys {
		if !covered[i] {
			messages = append(messages, "unlisted "+path.Dir(key.Path)+" "+key.Pattern)
		}
	}
	return messages
}

var bindingRE = regexp.MustCompile(`^(tenant-in-key|tenant-in-value|global-id)(, enforced at \S.*)?$`)
var coverageRE = regexp.MustCompile(`^(Test[A-Za-z0-9_]+|pending:PR1-03[B-H]|open-finding:[A-Z][A-Z0-9]*-[0-9A-Z]+(-[0-9A-Z]+)*)$`)
var testFuncRE = regexp.MustCompile(`^Test[A-Za-z0-9_]*$`)
var placeholderRE = regexp.MustCompile(`\{[^{}]+\}`)

func entryMessages(entry storeKeyEntry, tests map[string][]string) []string {
	var messages []string
	label := entry.Package + " " + entry.Pattern
	if !bindingRE.MatchString(entry.Binding) {
		messages = append(messages, "invalid binding "+label)
	}
	if !coverageRE.MatchString(entry.Coverage) {
		messages = append(messages, "invalid coverage "+label)
	} else if strings.HasPrefix(entry.Coverage, "Test") && len(tests[entry.Coverage]) != 1 {
		messages = append(messages, fmt.Sprintf("coverage %s: %s declared in %d directories", label, entry.Coverage, len(tests[entry.Coverage])))
	}
	return messages
}

func patternCovers(pattern, entry string) bool {
	var expr strings.Builder
	expr.WriteString("^")
	end := 0
	for _, loc := range placeholderRE.FindAllStringIndex(pattern, -1) {
		expr.WriteString(regexp.QuoteMeta(pattern[end:loc[0]]))
		if pattern[loc[0]:loc[1]] == "{suffix}" {
			expr.WriteString(".+")
		} else {
			expr.WriteString("[^:]+")
		}
		end = loc[1]
	}
	expr.WriteString(regexp.QuoteMeta(pattern[end:]))
	expr.WriteString("$")
	return regexp.MustCompile(expr.String()).MatchString(entry)
}

func hasPrefixLiteral(literals []string, prefix string) bool {
	return prefix != "" && slices.ContainsFunc(literals, func(literal string) bool {
		return strings.HasPrefix(literal, prefix)
	})
}

func hasComposedPrefix(literals []string, prefix string) bool {
	segments := strings.Split(strings.TrimSuffix(prefix, ":"), ":")
	if slices.Contains(segments, "") {
		return false
	}
	reachable := make([]bool, len(segments)+1)
	reachable[0] = true
	for start := range segments {
		if !reachable[start] {
			continue
		}
		for end := start + 1; end <= len(segments); end++ {
			run := strings.Join(segments[start:end], ":")
			if slices.ContainsFunc(literals, func(literal string) bool {
				return literal == run || strings.HasPrefix(literal, run+":")
			}) {
				reachable[end] = true
			}
		}
	}
	return reachable[len(segments)]
}

func walkModule(root string, visit func(rel string) error) error {
	root = filepath.Clean(root)
	return filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name == root {
				return nil
			}
			base := entry.Name()
			if base == "testdata" || base == "vendor" || base == "node_modules" || strings.HasPrefix(base, ".") || strings.HasPrefix(base, "_") {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(name, "go.mod")); err == nil {
				return filepath.SkipDir
			} else if !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		return visit(filepath.ToSlash(rel))
	})
}

func testFuncDirs(root string) (map[string][]string, error) {
	dirs := map[string][]string{}
	err := walkModule(root, func(rel string) error {
		if !strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, filepath.FromSlash(rel)), nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Recv == nil && testFuncRE.MatchString(fn.Name.Name) {
				dirs[fn.Name.Name] = append(dirs[fn.Name.Name], path.Dir(rel))
			}
		}
		return nil
	})
	for name, values := range dirs {
		slices.Sort(values)
		dirs[name] = slices.Compact(values)
	}
	return dirs, err
}

func packageLiterals(root string) (map[string][]string, error) {
	literals := map[string][]string{}
	err := walkModule(root, func(rel string) error {
		if strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, filepath.FromSlash(rel)), nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if lit, ok := node.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				value, unquoteErr := strconv.Unquote(lit.Value)
				if unquoteErr != nil {
					err = unquoteErr
					return false
				}
				literals[path.Dir(rel)] = append(literals[path.Dir(rel)], value)
			}
			return true
		})
		return err
	})
	return literals, err
}
