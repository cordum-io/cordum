package docscheck

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeDoc writes content to a fresh temp file and returns its path.
func writeDoc(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const envDoc = "# Env\n" + // 1
	"\n" + // 2
	"<!-- docscheck: env -->\n" + // 3
	"\n" + // 4
	"| Variable | Default | Description |\n" + // 5
	"|----------|---------|-------------|\n" + // 6
	"| `FOO_A` | `x` | one |\n" + // 7
	"| `FOO_B` | `a\\|b` (dev) | two \\| three |\n" + // 8
	"\n" + // 9
	"after\n" // 10

func TestParseMarkers(t *testing.T) {
	t.Run("env table rows and cells", func(t *testing.T) {
		path := writeDoc(t, "env.md", envDoc)
		blocks, err := Parse(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(blocks) != 1 {
			t.Fatalf("blocks = %d, want 1", len(blocks))
		}
		b := blocks[0]
		if b.File != path || b.Line != 5 || !reflect.DeepEqual(b.Kinds, []string{"env"}) {
			t.Fatalf("block = %s:%d %v, want %s:5 [env]", b.File, b.Line, b.Kinds, path)
		}
		if want := []string{"Variable", "Default", "Description"}; !reflect.DeepEqual(b.Header, want) {
			t.Fatalf("header = %q, want %q", b.Header, want)
		}
		want := []Row{
			{Line: 7, Cells: []string{"FOO_A", "x", "one"}, Raw: []string{"`FOO_A`", "`x`", "one"}},
			{Line: 8, Cells: []string{"FOO_B", "a|b (dev)", "two | three"}, Raw: []string{"`FOO_B`", "`a|b` (dev)", "two | three"}},
		}
		if !reflect.DeepEqual(b.Rows, want) {
			t.Fatalf("rows = %#v\nwant %#v", b.Rows, want)
		}
		if len(b.Lines) != 4 || b.Lines[0].No != 5 || b.Lines[3].No != 8 {
			t.Fatalf("lines = %#v, want lines 5..8", b.Lines)
		}
	})

	t.Run("fenced block with info string and multi-kind", func(t *testing.T) {
		doc := "<!-- docscheck: endpoints command -->\n```bash\ncurl http://h/api/v1/status\necho hi\n```\n"
		blocks, err := Parse(writeDoc(t, "f.md", doc))
		if err != nil {
			t.Fatal(err)
		}
		want := []Line{{No: 3, Text: "curl http://h/api/v1/status"}, {No: 4, Text: "echo hi"}}
		if len(blocks) != 1 || blocks[0].Line != 3 || !reflect.DeepEqual(blocks[0].Lines, want) ||
			!reflect.DeepEqual(blocks[0].Kinds, []string{"endpoints", "command"}) {
			t.Fatalf("blocks = %#v", blocks)
		}
	})

	t.Run("tilde fence and skip reason", func(t *testing.T) {
		doc := "<!-- docscheck: skip reason=\"needs a live cluster\" -->\n~~~ yaml\nk: v\n~~~\n"
		blocks, err := Parse(writeDoc(t, "s.md", doc))
		if err != nil {
			t.Fatal(err)
		}
		if len(blocks) != 1 || blocks[0].Attrs["reason"] != "needs a live cluster" || blocks[0].Lines[0].Text != "k: v" {
			t.Fatalf("blocks = %#v", blocks)
		}
	})

	t.Run("marker inside an unmarked fence is ignored", func(t *testing.T) {
		doc := "```md\n<!-- docscheck: bogus -->\n```\ntext\n"
		blocks, err := Parse(writeDoc(t, "i.md", doc))
		if err != nil || len(blocks) != 0 {
			t.Fatalf("blocks = %#v, err = %v; want none", blocks, err)
		}
	})

	t.Run("CRLF parses like LF", func(t *testing.T) {
		lf, err := Parse(writeDoc(t, "lf.md", envDoc))
		if err != nil {
			t.Fatal(err)
		}
		crlf, err := Parse(writeDoc(t, "lf.md", strings.ReplaceAll(envDoc, "\n", "\r\n")))
		if err != nil {
			t.Fatal(err)
		}
		lf[0].File, crlf[0].File = "", ""
		if !reflect.DeepEqual(lf, crlf) {
			t.Fatalf("CRLF blocks differ:\n%#v\n%#v", crlf, lf)
		}
	})

	errCases := []struct{ name, doc, want string }{
		{"unknown kind", "x\n\n<!-- docscheck: bogus -->\n| a |\n", `:3: unknown docscheck kind "bogus"`},
		{"unknown key", "<!-- docscheck: env owner=\"me\" -->\n| a |\n", `:1: unknown docscheck key "owner"`},
		{"skip without reason", "\n<!-- docscheck: skip -->\n```\nx\n```\n", `:2: skip needs reason="..." of at least 10 characters`},
		{"skip with short reason", "<!-- docscheck: skip reason=\"later\" -->\n```\nx\n```\n", `:1: skip needs reason="..." of at least 10 characters`},
		{"no kind", "<!-- docscheck: -->\n```\nx\n```\n", `:1: docscheck marker names no kind`},
		{"not followed by block", "<!-- docscheck: env -->\n\nplain prose\n", `:1: docscheck marker is not followed by a fenced block or a table`},
		{"marker at end of file", "a\n<!-- docscheck: env -->\n", `:2: docscheck marker is not followed by a fenced block or a table`},
		{"unclosed fence", "<!-- docscheck: command -->\n```bash\nls\n", `:2: fenced block is not closed`},
	}
	for _, tc := range errCases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeDoc(t, "e.md", tc.doc)
			_, err := Parse(path)
			if err == nil || err.Error() != path+tc.want {
				t.Fatalf("err = %v, want %q", err, path+tc.want)
			}
		})
	}
}

func TestScanContract(t *testing.T) {
	doc := "# Contract\n" + // 1
		"Call `GET /api/v1/things/{id}` or POST /api/v1/things/.\n" + // 2
		"Polls `https://h:8081/api/v1/status` without curl.\n" + // 3
		"```bash\n" + // 4
		"curl -sS -X DELETE https://h/api/v1/things/abc?force=1 | jq\n" + // 5
		"curl -sS \\\n" + // 6
		"  -d '{}' \\\n" + // 7
		"  http://h/api/v1/things#frag\n" + // 8
		"curl http://h/api/v1/status\n" + // 9
		"```\n" // 10
	path := writeDoc(t, "c.md", strings.ReplaceAll(doc, "\n", "\r\n"))
	got, err := ScanContract(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []Endpoint{
		{File: path, Line: 2, Method: "GET", Path: "/api/v1/things/{id}"},
		{File: path, Line: 2, Method: "POST", Path: "/api/v1/things"},
		{File: path, Line: 5, Method: "DELETE", Path: "/api/v1/things/abc"},
		{File: path, Line: 8, Method: "POST", Path: "/api/v1/things"},
		{File: path, Line: 9, Method: "GET", Path: "/api/v1/status"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("endpoints =\n%#v\nwant\n%#v", got, want)
	}
}

func TestPathMatches(t *testing.T) {
	const tmpl = "/api/v1/things/{id}"
	cases := []struct {
		doc, tmpl string
		want      bool
	}{
		{"/api/v1/things/abc", tmpl, true},
		{"/api/v1/things/{thing_id}", tmpl, true},
		{"/api/v1/things/<id>", tmpl, true},
		{"/api/v1/things/:id", tmpl, true},
		{"/api/v1/things/$ID", tmpl, true},
		{"/api/v1/things/{id}", "/api/v1/things/list", false},
		{"/api/v1/things/<id>", "/api/v1/things/list", false},
		{"/api/v1/things", tmpl, false},
		{"/api/v1/things/a/b", tmpl, false},
		{"/api/v1/other/abc", tmpl, false},
		{"/api/v1/things//", tmpl, false},
	}
	for _, tc := range cases {
		if got := pathMatches(tc.doc, tc.tmpl); got != tc.want {
			t.Errorf("pathMatches(%q, %q) = %v, want %v", tc.doc, tc.tmpl, got, tc.want)
		}
	}
}

const fixtureDoc = "docs/contract/fixture.md"

func fixtureConfig() Config {
	return Config{
		Root:         filepath.Join("testdata", "fixture"),
		Docs:         []string{fixtureDoc},
		ContractGlob: "docs/contract/*.md",
		OpenAPI:      "openapi.yaml",
		CodeRoots:    []string{"core"},
	}
}

func TestCheckFixture(t *testing.T) {
	r, err := Check(fixtureConfig())
	if err != nil {
		t.Fatal(err)
	}
	want := []Issue{
		{File: fixtureDoc, Line: 6, Kind: "env-default", Key: "FIX_VAR", Detail: "FIX_VAR: documented default `b`, code default \"a\""},
		{File: fixtureDoc, Line: 15, Kind: "endpoint", Key: "GET /api/v1/missing", Detail: "GET /api/v1/missing not in the OpenAPI description"},
	}
	if !reflect.DeepEqual(r.Failures, want) {
		t.Fatalf("failures =\n%#v\nwant\n%#v", r.Failures, want)
	}
	// Exact counts pin the passes: FIX_OK and FIX_NONE are the two passing
	// defaults (FIX_OK stays single-valued although firstEnv names it), and
	// GET /api/v1/things/{id} is the passing endpoint.
	checked := map[string]int{"env-name": 6, "env-default": 3, "endpoint": 2}
	unchecked := map[string]int{
		"default cell is prose":                       1,
		"code default not in a recognized form":       1,
		"ambiguous code default":                      1,
		reasonCommand:                                 1,
		"skip: needs a live cluster to run":           1,
		"endpoints block holds no recognized example": 1,
	}
	if r.Blocks != 4 || r.Passed != 9 || len(r.Known) != 0 ||
		!reflect.DeepEqual(r.Checked, checked) || !reflect.DeepEqual(r.Unchecked, unchecked) {
		t.Fatalf("report = %s", r.Summary())
	}

	t.Run("names come from code roots or text sources", func(t *testing.T) {
		cfg := fixtureConfig()
		cfg.CodeRoots = nil
		cfg.TextSources = []string{"docs"} // markdown is never product source
		r, err := Check(cfg)
		if err != nil {
			t.Fatal(err)
		}
		miss := Issue{File: fixtureDoc, Line: 6, Kind: "env-name", Key: "FIX_VAR", Detail: "FIX_VAR is not named anywhere in the product source"}
		if len(r.Failures) == 0 || r.Failures[0] != miss {
			t.Fatalf("failures = %#v, want first %#v", r.Failures, miss)
		}
		cfg.TextSources = []string{"co*"}
		if r, err = Check(cfg); err != nil || r.Checked["env-name"] != 6 || hasKind(r.Failures, "env-name") {
			t.Fatalf("with text sources: err = %v, %s", err, r.Summary())
		}
	})

	t.Run("baseline entry makes the wrong default KNOWN", func(t *testing.T) {
		cfg := fixtureConfig()
		cfg.Baseline = writeDoc(t, "b.yaml", "- {file: docs/contract/fixture.md, kind: env-default, key: FIX_VAR, reason: doc says b, owner: PR2-15-DOCS}\n"+
			"- {file: docs/contract/fixture.md, kind: endpoint, key: GET /api/v1/missing, reason: removed route, owner: PR2-15-DOCS}\n")
		r, err := Check(cfg)
		if err != nil || len(r.Failures) != 0 || len(r.Known) != 2 || r.Known[0].Key != "FIX_VAR" {
			t.Fatalf("err = %v, failures = %#v, known = %#v", err, r.Failures, r.Known)
		}
	})

	t.Run("baseline entry with no failure is STALE", func(t *testing.T) {
		cfg := fixtureConfig()
		cfg.Baseline = writeDoc(t, "b.yaml", "# header\n- {file: docs/contract/fixture.md, kind: env-default, key: FIX_OK, reason: was wrong, owner: PR2-15-DOCS}\n")
		r, err := Check(cfg)
		if err != nil {
			t.Fatal(err)
		}
		stale := Issue{File: filepath.ToSlash(cfg.Baseline), Line: 2, Kind: "baseline-stale", Key: "env-default FIX_OK",
			Detail: "STALE baseline entry: no env-default failure for \"FIX_OK\" in docs/contract/fixture.md"}
		if len(r.Failures) != 3 || r.Failures[2] != stale {
			t.Fatalf("failures = %#v, want third %#v", r.Failures, stale)
		}
	})

	t.Run("baseline entry without a reason is an error", func(t *testing.T) {
		cfg := fixtureConfig()
		cfg.Baseline = writeDoc(t, "b.yaml", "- {file: x.md, kind: endpoint, key: GET /x, owner: PR2-15-DOCS}\n")
		if _, err := Check(cfg); err == nil || err.Error() != cfg.Baseline+":1: a baseline entry needs file, kind, key, reason and owner" {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("skip marker without a reason fails", func(t *testing.T) {
		cfg := fixtureConfig()
		doc := writeDoc(t, "skip.md", "<!-- docscheck: skip -->\n```\nx\n```\n")
		cfg.Docs = []string{doc}
		if _, err := Check(cfg); err == nil || err.Error() != doc+`:1: skip needs reason="..." of at least 10 characters` {
			t.Fatalf("err = %v", err)
		}
	})
}

func hasKind(issues []Issue, kind string) bool {
	for _, i := range issues {
		if i.Kind == kind {
			return true
		}
	}
	return false
}

// repoRoot walks up from the test's directory to the directory holding go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

// TestDocsMatchProduct is the CI check: the marked blocks of the real docs
// and the whole contract docs against the product source and OpenAPI file.
func TestDocsMatchProduct(t *testing.T) {
	r, err := Check(Config{
		Root:         repoRoot(t),
		Docs:         []string{"docs/configuration-reference.md", "docs/quickstart.md"},
		ContractGlob: "docs/contract/*.md",
		OpenAPI:      "docs/api/openapi/cordum-api.yaml",
		CodeRoots:    []string{"cmd", "core", "sdk", "tools"},
		TextSources:  []string{"docker-compose*.yml", "deploy", "tools/scripts"},
		Baseline:     "tools/docscheck/known-mismatches.yaml",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Log(r.Summary())
	for _, f := range r.Failures {
		t.Errorf("%s:%d: %s", f.File, f.Line, f.Detail)
	}
	if r.Checked["env-name"] == 0 || r.Checked["endpoint"] == 0 {
		t.Errorf("no marked env or endpoint checks ran; are the docscheck markers missing?")
	}
}
