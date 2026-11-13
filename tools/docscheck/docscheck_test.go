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
