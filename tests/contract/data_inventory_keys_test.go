package contract

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// dataKeyShapeRE is the lexical shape of a Redis key or key part: a lowercase
// first segment and a colon, then only key characters and format verbs.
var dataKeyShapeRE = regexp.MustCompile(`^[a-z][a-z0-9_-]*:[A-Za-z0-9_.:%{}*-]*$`)
var dataLuaXAddRE = regexp.MustCompile(`(?i)redis\.p?call\(\s*['"]xadd['"]`)

// dataFound is one discovered item: a key prefix (a key-shaped literal up to
// its first '%', '{' or '*'), a JetStream stream name, or a Redis stream writer.
type dataFound struct {
	Kind, Value, Path string
	Line              int
}

func (f dataFound) String() string {
	return fmt.Sprintf("%s %q at %s:%d", f.Kind, f.Value, f.Path, f.Line)
}

// discoverDataKeys parses the non-test Go files under dirs (relative to root,
// skipping testdata and vendor). A file that does not parse fails the scan.
func discoverDataKeys(root string, dirs ...string) ([]dataFound, error) {
	var found []dataFound
	for _, dir := range dirs {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() && (d.Name() == "testdata" || d.Name() == "vendor") {
				if err == nil {
					err = filepath.SkipDir
				}
				return err
			}
			if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			found = append(found, scanDataFile(fset, f, filepath.ToSlash(rel))...)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return found, nil
}

func scanDataFile(fset *token.FileSet, f *ast.File, rel string) []dataFound {
	var found []dataFound
	at := func(kind, value string, n ast.Node) {
		found = append(found, dataFound{kind, value, rel, fset.Position(n.Pos()).Line})
	}
	values := map[string]string{} // file-level string consts and vars
	funcs := map[*ast.FuncLit]string{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.ValueSpec:
			for i, name := range x.Names {
				if i < len(x.Values) {
					if s, ok := dataString(x.Values[i]); ok {
						values[name.Name] = s
					}
				}
			}
		case *ast.AssignStmt:
			for i, rhs := range x.Rhs {
				if fl, ok := rhs.(*ast.FuncLit); ok && i < len(x.Lhs) {
					if id, ok := x.Lhs[i].(*ast.Ident); ok {
						funcs[fl] = id.Name
					}
				}
			}
		}
		return true
	})
	var stack []ast.Node
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		stack = append(stack, n)
		switch x := n.(type) {
		case *ast.BasicLit:
			if s, ok := dataString(x); ok && dataKeyShapeRE.MatchString(s) {
				prefix, _, _ := strings.Cut(s, "%")
				prefix, _, _ = strings.Cut(prefix, "{")
				prefix, _, _ = strings.Cut(prefix, "*")
				at("key", prefix, x)
			} else if ok && dataLuaXAddRE.MatchString(s) {
				at("redis-stream", "XADD", x)
			}
		case *ast.CallExpr:
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "XAdd" {
				at("redis-stream", "XAdd", x)
			}
		case *ast.CompositeLit:
			if sel, ok := x.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "StreamConfig" {
				for _, name := range dataStreamNames(f, x, stack, values, funcs) {
					at("jetstream", name, x)
				}
			}
		}
		return true
	})
	return found
}

func dataString(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

// dataStreamNames resolves a StreamConfig Name: a literal, a file-level const
// or var, or a parameter of the enclosing function literal, resolved at the
// call sites of the variable it is assigned to. Anything else is "<unresolved>".
func dataStreamNames(f *ast.File, cfg *ast.CompositeLit, stack []ast.Node, values map[string]string, funcs map[*ast.FuncLit]string) []string {
	var expr ast.Expr
	for _, el := range cfg.Elts {
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Name" {
				expr = kv.Value
			}
		}
	}
	resolve := func(e ast.Expr) (string, bool) {
		if s, ok := dataString(e); ok {
			return s, true
		}
		if id, ok := e.(*ast.Ident); ok {
			s, ok := values[id.Name]
			return s, ok
		}
		return "", false
	}
	if s, ok := resolve(expr); ok || expr == nil {
		return []string{s}
	}
	id, _ := expr.(*ast.Ident)
	for i := len(stack) - 1; i >= 0 && id != nil; i-- {
		fl, ok := stack[i].(*ast.FuncLit)
		if !ok || funcs[fl] == "" {
			continue
		}
		index, n := -1, 0
		for _, field := range fl.Type.Params.List {
			for _, name := range field.Names {
				if name.Name == id.Name {
					index = n
				}
				n++
			}
		}
		if index < 0 {
			break
		}
		var names []string
		ast.Inspect(f, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok && len(call.Args) > index {
				if fn, ok := call.Fun.(*ast.Ident); ok && fn.Name == funcs[fl] {
					s, ok := resolve(call.Args[index])
					if !ok {
						s = "<unresolved>"
					}
					names = append(names, s)
				}
			}
			return true
		})
		if len(names) > 0 {
			return names
		}
	}
	return []string{"<unresolved>"}
}

// uncoveredDataKeys lists each discovered item no entry or not_tenant_data
// classification accounts for, and each classification pattern that matches
// no discovered item in its file.
func uncoveredDataKeys(inv *DataInventory, found []dataFound) []string {
	var out []string
	entries := slices.Concat(inv.EndpointOnly, inv.ControlPlane)
	for _, f := range found {
		if !slices.ContainsFunc(entries, func(e DataEntry) bool { return dataEntryCovers(e, f) }) &&
			!slices.ContainsFunc(inv.NotTenantData, func(c DataKeyClass) bool {
				return slices.ContainsFunc(c.Patterns, func(p string) bool { return dataClassMatches(c, p, f) })
			}) {
			out = append(out, "unclassified "+f.String())
		}
	}
	for _, c := range inv.NotTenantData {
		for _, p := range c.Patterns {
			if !slices.ContainsFunc(found, func(f dataFound) bool { return dataClassMatches(c, p, f) }) {
				out = append(out, fmt.Sprintf("stale not_tenant_data %q in %s", p, c.Path))
			}
		}
	}
	return out
}

// dataEntryCovers: a key-pattern prefix covers longer prefixes anywhere, and a
// pattern covers the key parts it contains in its own file; a jetstream name
// must be listed; a Redis stream writer file must be a redis_stream entry ref.
func dataEntryCovers(e DataEntry, f dataFound) bool {
	s := e.Storage
	if s == nil {
		return false
	}
	switch f.Kind {
	case "key":
		return slices.ContainsFunc(s.RedisKeys, func(k DataKeyRef) bool {
			prefix, _, _ := strings.Cut(k.Pattern, "{")
			return strings.Contains(prefix, ":") && strings.HasPrefix(f.Value, prefix) || k.Path == f.Path && strings.Contains(k.Pattern, f.Value)
		})
	case "jetstream":
		return slices.ContainsFunc(s.Streams, func(r DataLiteralRef) bool { return r.Name == f.Value })
	case "redis-stream":
		return s.Kind == "redis_stream" && (slices.ContainsFunc(s.RedisKeys, func(k DataKeyRef) bool { return k.Path == f.Path }) ||
			slices.ContainsFunc(s.Writers, func(w CodeRef) bool { return w.Path == f.Path }))
	}
	return false
}

// dataClassMatches: a classification covers keys and jetstream names in its own
// file only, by the pattern's text before its first '{'; never stream writers.
func dataClassMatches(c DataKeyClass, pattern string, f dataFound) bool {
	prefix, _, _ := strings.Cut(pattern, "{")
	return c.Path == f.Path && f.Kind != "redis-stream" && prefix != "" && strings.HasPrefix(f.Value, prefix)
}

func TestDataInventoryKeyCoverage(t *testing.T) {
	root, err := repoRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	inv, err := LoadDataInventory(filepath.Join(root, "docs", "contract", "data-inventory.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	found, err := discoverDataKeys(root, "core", "cmd")
	if err != nil {
		t.Fatal(err)
	}
	// Non-vacuity: the scan must see the families the inventory already names.
	for _, want := range []dataFound{{Kind: "key", Value: "audit:chain:", Path: "core/audit/chain.go"},
		{Kind: "jetstream", Value: "CORDUM_SYS", Path: "core/infra/bus/nats.go"}, {Kind: "jetstream", Value: "CORDUM_JOBS", Path: "core/infra/bus/nats.go"},
		{Kind: "redis-stream", Value: "XADD", Path: "core/audit/chain.go"}} {
		if !slices.ContainsFunc(found, func(f dataFound) bool { return f.Kind == want.Kind && f.Value == want.Value && f.Path == want.Path }) {
			t.Errorf("scan missed %s %q in %s", want.Kind, want.Value, want.Path)
		}
	}
	for _, msg := range uncoveredDataKeys(inv, found) {
		t.Error(msg)
	}
}

func TestDataInventoryKeyCoverageFixture(t *testing.T) {
	const path = "core/fixture/store.go"
	found, err := discoverDataKeys(filepath.Join("testdata", "data_inventory_keys"), "core")
	if err != nil {
		t.Fatal(err)
	}
	complete := func() *DataInventory {
		return &DataInventory{ControlPlane: []DataEntry{
			{ID: "DI-C01", Storage: &DataStorage{Kind: "redis_key", RedisKeys: []DataKeyRef{{Pattern: "fix:covered:{id}", Path: path}, {Pattern: "fix:unknown:{id}", Path: path}}}},
			{ID: "DI-C02", Storage: &DataStorage{Kind: "redis_stream", Writers: []CodeRef{{Path: path, Symbol: "appendScript"}}}},
			{ID: "DI-C03", Storage: &DataStorage{Kind: "jetstream", Streams: []DataLiteralRef{{Name: "FIX_EVENTS", Path: path}, {Name: "FIX_DIRECT", Path: path}, {Name: "FIX_SHADOW", Path: path}}}},
		}, NotTenantData: []DataKeyClass{{Path: path, Patterns: []string{"fix:label:{name}"}, Reason: "fixture label names, not Redis keys"}}}
	}
	if got := uncoveredDataKeys(complete(), found); len(got) != 0 {
		t.Fatalf("complete fixture inventory reported %q", got)
	}
	broken := t.TempDir()
	write(t, filepath.Join(broken, "core", "pkg", "broken.go"), "package [")
	if _, err := discoverDataKeys(broken, "core"); err == nil {
		t.Fatal("a file that does not parse must fail the scan, not hide its keys")
	}
	shadowed := t.TempDir()
	write(t, filepath.Join(shadowed, "core", "pkg", "shadow.go"), `package pkg

const streamName = "TMP_CONST"

func ensure(js X, streamName string) {
	js.AddStream(&nats.StreamConfig{Name: streamName})
	add := func(name string) { js.AddStream(&nats.StreamConfig{Name: name}) }
	add(streamName)
}
`)
	shadowFound, err := discoverDataKeys(shadowed, "core")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(shadowFound, []dataFound{{"jetstream", "<unresolved>", "core/pkg/shadow.go", 6}, {"jetstream", "<unresolved>", "core/pkg/shadow.go", 7}}) {
		t.Errorf("a function parameter that shadows a file-level const must stay unresolved, got %q", shadowFound)
	}
	cases := map[string]struct {
		edit func(*DataInventory)
		want string
	}{
		"unclassified key prefix":      {func(d *DataInventory) { d.ControlPlane[0].Storage.RedisKeys = d.ControlPlane[0].Storage.RedisKeys[:1] }, `unclassified key "fix:unknown:" at core/fixture/store.go:`},
		"unclassified jetstream":       {func(d *DataInventory) { d.ControlPlane[2].Storage.Streams = d.ControlPlane[2].Storage.Streams[:1] }, `unclassified jetstream "FIX_DIRECT" at core/fixture/store.go:`},
		"shadowed jetstream":           {func(d *DataInventory) { d.ControlPlane[2].Storage.Streams = d.ControlPlane[2].Storage.Streams[:2] }, `unclassified jetstream "FIX_SHADOW" at core/fixture/store.go:`},
		"unclassified redis stream":    {func(d *DataInventory) { d.ControlPlane[1].Storage.Kind = "redis_key" }, `unclassified redis-stream "XADD" at core/fixture/store.go:`},
		"label without classification": {func(d *DataInventory) { d.NotTenantData = nil }, `unclassified key "fix:label:" at core/fixture/store.go:`},
		"stale classification": {func(d *DataInventory) {
			d.NotTenantData[0].Patterns = append(d.NotTenantData[0].Patterns, "fix:gone:{name}")
		}, `stale not_tenant_data "fix:gone:{name}" in core/fixture/store.go`},
		"classification in another file": {func(d *DataInventory) { d.NotTenantData[0].Path = "core/fixture/other.go" }, `unclassified key "fix:label:" at core/fixture/store.go:`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			inv := complete()
			tc.edit(inv)
			got := uncoveredDataKeys(inv, found)
			if !slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, tc.want) }) {
				t.Fatalf("got %q, want an item starting %q", got, tc.want)
			}
		})
	}
}

func TestDataInventoryNotTenantDataRejectsIdentityPlaceholders(t *testing.T) {
	root, err := repoRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	inv, err := LoadDataInventory(filepath.Join(root, "docs", "contract", "data-inventory.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if is := inv.Validate(root); len(is) != 0 {
		t.Fatalf("real inventory: %+v", is)
	}
	classify := func(pattern string) []Issue {
		d := *inv
		d.NotTenantData = append(slices.Clone(inv.NotTenantData), DataKeyClass{Path: "core/edge/store_redis.go", Patterns: []string{pattern}, Reason: "fixture classification under test"})
		return d.Validate(root)
	}
	if is := classify("edge:session:{shard}"); len(is) != 0 {
		t.Fatalf("control pattern refused: %+v", is)
	}
	for _, ph := range []string{"{tenant}", "{user_id}", "{email}", "{session}", "{agentID}", "{client_ip}", "{IPAddress}", "{job}", "{jobs}"} {
		is := classify("edge:session:" + ph)
		if len(is) != 1 || is[0].Rule != "not-tenant-data" || !strings.Contains(is[0].Detail, ph) {
			t.Errorf("%s: got %+v, want one not-tenant-data issue naming it", ph, is)
		}
	}
	for _, ok := range []string{"{tip}", "{zip}", "{recipient}", "{ids}", "{name}", "{shard}"} {
		if got := identityPlaceholders("x:" + ok); len(got) != 0 {
			t.Errorf("identityPlaceholders(%q) = %q, want none", ok, got)
		}
	}
}
