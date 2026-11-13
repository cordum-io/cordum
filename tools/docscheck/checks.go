package docscheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config names every input relative to Root, so fixtures need no real repo.
type Config struct {
	Root         string
	Docs         []string // docs whose marked blocks are checked
	ContractGlob string   // contract docs, scanned whole for endpoint examples
	OpenAPI      string
	CodeRoots    []string // Go trees whose non-test string literals name the product
	TextSources  []string // globs; directories are walked; names match whole words
	Baseline     string   // known-mismatch list; empty means none
}

// Issue is one mismatch at File:Line, with File relative to Root.
type Issue struct {
	File, Kind, Key, Detail string
	Line                    int
}

// Report is the outcome of Check. Checked counts passed plus failed plus
// known checks per kind; Unchecked counts what could not be checked, by reason.
type Report struct {
	Blocks, Passed  int
	Failures, Known []Issue
	Checked         map[string]int
	Unchecked       map[string]int
}

const reasonCommand = "executed by the CI command runner (PR2-15-COMMANDS)"

var (
	envNameRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	// A second env-shaped literal, as in firstEnv("A_X", "B_X"), is another
	// name, not a default.
	secondNameRe = regexp.MustCompile(`^[A-Z][A-Z0-9]*(_[A-Z0-9]+)+$`)
	wordRe       = regexp.MustCompile(`[A-Za-z0-9_]+`)
	tickRe       = regexp.MustCompile("^`([^`]*)`$")
)

// path resolves p against Root unless it is absolute.
func (c Config) path(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.Root, p)
}

type codeIndex struct {
	names    map[string]bool
	defaults map[string]map[string]bool
}

// Check verifies the marked blocks of cfg.Docs and the endpoint examples of
// the contract docs, then applies the baseline.
func Check(cfg Config) (Report, error) {
	r := Report{Checked: map[string]int{}, Unchecked: map[string]int{}}
	idx, err := indexProduct(cfg)
	if err != nil {
		return r, err
	}
	ops, err := loadOpenAPI(cfg.path(cfg.OpenAPI))
	if err != nil {
		return r, err
	}
	for _, doc := range cfg.Docs {
		blocks, err := Parse(cfg.path(doc))
		if err != nil {
			return r, err
		}
		for _, b := range blocks {
			b.File = filepath.ToSlash(doc)
			r.checkBlock(b, idx, ops)
		}
	}
	contracts, err := filepath.Glob(cfg.path(cfg.ContractGlob))
	if err != nil {
		return r, err
	}
	for _, c := range contracts {
		eps, err := ScanContract(c)
		if err != nil {
			return r, err
		}
		rel, err := filepath.Rel(cfg.Root, c)
		if err != nil {
			return r, err
		}
		for _, ep := range eps {
			ep.File = filepath.ToSlash(rel)
			r.checkEndpoint(ep, ops)
		}
	}
	return r, r.applyBaseline(cfg)
}

func (r *Report) pass(kind string) { r.Passed++; r.Checked[kind]++ }

func (r *Report) fail(i Issue) { r.Failures = append(r.Failures, i); r.Checked[i.Kind]++ }

func (r *Report) checkBlock(b Block, idx codeIndex, ops map[string]map[string]bool) {
	r.Blocks++
	if b.Has("skip") {
		r.Unchecked["skip: "+b.Attrs["reason"]]++
		return
	}
	if b.Has("command") {
		r.Unchecked[reasonCommand]++
	}
	if b.Has("endpoints") {
		for _, ep := range endpointsIn(b.File, b.Lines) {
			r.checkEndpoint(ep, ops)
		}
	}
	if b.Has("env") {
		r.checkEnv(b, idx)
	}
}

func (r *Report) checkEnv(b Block, idx codeIndex) {
	col := -1
	for k, h := range b.Header {
		if strings.EqualFold(h, "Default") {
			col = k
		}
	}
	for _, row := range b.Rows {
		name := row.Cells[0]
		if !envNameRe.MatchString(name) {
			r.Unchecked["variable cell is not a single name"]++
			continue
		}
		at := Issue{File: b.File, Line: row.Line, Key: name}
		if idx.names[name] {
			r.pass("env-name")
		} else {
			at.Kind, at.Detail = "env-name", name+" is not named anywhere in the product source"
			r.fail(at)
		}
		if col < 0 || col >= len(row.Raw) {
			r.Unchecked["table has no Default column"]++
			continue
		}
		r.checkDefault(at, row.Raw[col], idx.defaults[name])
	}
}

// checkDefault compares a Default cell that is one backticked token, or a
// dash for none, with the single recognized code default.
func (r *Report) checkDefault(at Issue, cell string, code map[string]bool) {
	doc, dash := "", cell == "—" || cell == "-" || cell == ""
	if m := tickRe.FindStringSubmatch(cell); m != nil {
		doc = m[1]
	} else if !dash {
		r.Unchecked["default cell is prose"]++
		return
	}
	switch {
	case len(code) == 0:
		r.Unchecked["code default not in a recognized form"]++
		return
	case len(code) > 1:
		r.Unchecked["ambiguous code default"]++
		return
	}
	var want string
	for v := range code {
		want = v
	}
	if (dash && want == "") || (!dash && doc == want) {
		r.pass("env-default")
		return
	}
	at.Kind = "env-default"
	at.Detail = fmt.Sprintf("%s: documented default %s, code default %q", at.Key, cell, want)
	r.fail(at)
}

func (r *Report) checkEndpoint(ep Endpoint, ops map[string]map[string]bool) {
	for tmpl, methods := range ops {
		if (methods[ep.Method] || methods["*"]) && pathMatches(ep.Path, tmpl) {
			r.pass("endpoint")
			return
		}
	}
	key := ep.Method + " " + ep.Path
	r.fail(Issue{File: ep.File, Line: ep.Line, Kind: "endpoint", Key: key, Detail: key + " not in the OpenAPI description"})
}

// pathMatches reports whether a doc path matches an OpenAPI template. A
// template parameter matches any non-empty segment; any other segment must
// be equal, so a doc placeholder (<x>, {x}, :x, $X) matches only a parameter.
func pathMatches(doc, tmpl string) bool {
	ds, ts := strings.Split(doc, "/"), strings.Split(tmpl, "/")
	if len(ds) != len(ts) {
		return false
	}
	for k := range ds {
		param := strings.HasPrefix(ts[k], "{") && strings.HasSuffix(ts[k], "}")
		if (param && ds[k] == "") || (!param && ds[k] != ts[k]) {
			return false
		}
	}
	return true
}

// indexProduct collects the names in non-test Go string literals and in the
// text sources, and the defaults passed as Call("NAME", "default").
func indexProduct(cfg Config) (codeIndex, error) {
	idx := codeIndex{names: map[string]bool{}, defaults: map[string]map[string]bool{}}
	fset := token.NewFileSet()
	for _, root := range cfg.CodeRoots {
		err := walkFiles(cfg.path(root), func(path string) error {
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err == nil {
				ast.Inspect(f, idx.visit)
			}
			return err
		})
		if err != nil {
			return idx, err
		}
	}
	for _, pattern := range cfg.TextSources {
		matches, err := filepath.Glob(cfg.path(pattern))
		if err != nil {
			return idx, err
		}
		for _, m := range matches {
			if err := walkFiles(m, idx.addText); err != nil {
				return idx, err
			}
		}
	}
	return idx, nil
}

// walkFiles calls fn for every file under root, skipping testdata
// directories and _test.go files.
func walkFiles(root string, fn func(string) error) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && d.Name() == "testdata":
			return filepath.SkipDir
		case d.IsDir() || strings.HasSuffix(path, "_test.go"):
			return nil
		}
		return fn(path)
	})
}

func (idx codeIndex) addText(path string) error {
	data, err := os.ReadFile(path)
	for _, w := range wordRe.FindAll(data, -1) {
		idx.names[string(w)] = true
	}
	return err
}

func (idx codeIndex) visit(n ast.Node) bool {
	switch n := n.(type) {
	case *ast.BasicLit:
		if s, ok := strLit(n); ok {
			idx.names[s] = true
		}
	case *ast.CallExpr:
		for k := 0; k+1 < len(n.Args); k++ {
			name, ok := strLit(n.Args[k])
			def, okDef := strLit(n.Args[k+1])
			if ok && okDef && envNameRe.MatchString(name) && !secondNameRe.MatchString(def) {
				if idx.defaults[name] == nil {
					idx.defaults[name] = map[string]bool{}
				}
				idx.defaults[name][def] = true
			}
		}
	}
	return true
}

func strLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

// loadOpenAPI returns path template -> set of upper-case methods ("*" for
// an x-any-method path).
func loadOpenAPI(path string) (map[string]map[string]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(doc.Paths) == 0 {
		return nil, fmt.Errorf("%s: no OpenAPI paths", path)
	}
	ops := map[string]map[string]bool{}
	for p, item := range doc.Paths {
		methods := map[string]bool{}
		for k, v := range item {
			switch k = strings.ToLower(k); k {
			case "get", "post", "put", "patch", "delete", "head", "options":
				methods[strings.ToUpper(k)] = true
			case "x-any-method":
				methods["*"] = v == true
			}
		}
		ops[strings.TrimRight(p, "/")] = methods
	}
	return ops, nil
}

type baselineEntry struct {
	File, Kind, Key, Reason, Owner string
	line                           int
}

// applyBaseline moves failures listed in the baseline to Known, and turns
// every entry that matched no failure into a STALE failure.
func (r *Report) applyBaseline(cfg Config) error {
	entries, err := loadBaseline(cfg)
	if err != nil {
		return err
	}
	used := make([]bool, len(entries))
	var failures []Issue
	for _, f := range r.Failures {
		matched := false
		for k, e := range entries {
			if e.File == f.File && e.Kind == f.Kind && e.Key == f.Key {
				used[k], matched = true, true
			}
		}
		if matched {
			r.Known = append(r.Known, f)
		} else {
			failures = append(failures, f)
		}
	}
	for k, e := range entries {
		if !used[k] {
			failures = append(failures, Issue{File: filepath.ToSlash(cfg.Baseline), Line: e.line, Kind: "baseline-stale",
				Key: e.Kind + " " + e.Key, Detail: fmt.Sprintf("STALE baseline entry: no %s failure for %q in %s", e.Kind, e.Key, e.File)})
		}
	}
	r.Failures = failures
	return nil
}

func loadBaseline(cfg Config) ([]baselineEntry, error) {
	if cfg.Baseline == "" {
		return nil, nil
	}
	path := cfg.path(cfg.Baseline)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	if doc.Content[0].Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("%s: the baseline must be a list", path)
	}
	var entries []baselineEntry
	for _, n := range doc.Content[0].Content {
		var e baselineEntry
		if err := n.Decode(&e); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n.Line, err)
		}
		if e.File == "" || e.Kind == "" || e.Key == "" || e.Reason == "" || e.Owner == "" {
			return nil, fmt.Errorf("%s:%d: a baseline entry needs file, kind, key, reason and owner", path, n.Line)
		}
		e.line = n.Line
		entries = append(entries, e)
	}
	return entries, nil
}

// Summary is the one-line "docscheck:" report of the counts.
func (r Report) Summary() string {
	parts := func(m map[string]int) (string, int) {
		var out []string
		total := 0
		for k, n := range m {
			out, total = append(out, fmt.Sprintf("%s=%d", k, n)), total+n
		}
		sort.Strings(out)
		return strings.Join(out, "; "), total
	}
	checked, _ := parts(r.Checked)
	unchecked, total := parts(r.Unchecked)
	return fmt.Sprintf("docscheck: blocks=%d passed=%d failed=%d known=%d checked=[%s] unchecked=%d [%s]",
		r.Blocks, r.Passed, len(r.Failures), len(r.Known), checked, total, unchecked)
}
