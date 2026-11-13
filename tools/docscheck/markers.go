// Package docscheck checks marked blocks in the cordum docs against the
// product: environment variable names and defaults against the Go source,
// and endpoint examples against the OpenAPI description. It reads the docs
// as text and never executes anything.
package docscheck

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Line is one source line and its 1-based number.
type Line struct {
	No   int
	Text string
}

// Row is one table body row. Cells have backticks stripped; Raw keeps them.
// Escaped pipes (\|) are unescaped in both.
type Row struct {
	Line  int
	Cells []string
	Raw   []string
}

// Block is the fenced block or table that follows a docscheck marker. Line
// is the first content line; Lines holds every content line.
type Block struct {
	File   string
	Line   int
	Kinds  []string
	Attrs  map[string]string
	Lines  []Line
	Header []string
	Rows   []Row
}

// Has reports whether the block's marker names kind.
func (b Block) Has(kind string) bool { return slices.Contains(b.Kinds, kind) }

const minReasonLen = 10

var (
	markerRe   = regexp.MustCompile(`^\s*<!--\s*docscheck:(.*?)-->\s*$`)
	tokenRe    = regexp.MustCompile(`([A-Za-z_]+)="([^"]*)"|(\S+)`)
	fenceRe    = regexp.MustCompile("^\\s{0,3}(`{3,}|~{3,})")
	sepRe      = regexp.MustCompile(`^\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?$`)
	knownKinds = map[string]bool{"env": true, "endpoints": true, "command": true, "skip": true}
)

// readLines reads a doc as text, treating CRLF exactly like LF.
func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSuffix(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	return strings.Split(text, "\n"), nil
}

// Parse returns every marked block in the doc at path. Markers inside code
// fences are ignored. Every grammar error is returned, each naming file:line.
func Parse(path string) ([]Block, error) {
	lines, err := readLines(path)
	if err != nil {
		return nil, err
	}
	var blocks []Block
	var errs []error
	for i := 0; i < len(lines); i++ {
		if fence := fenceRe.FindStringSubmatch(lines[i]); fence != nil {
			i, _ = fenceEnd(lines, i, fence[1])
			continue
		}
		m := markerRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		b, next, err := parseBlock(path, lines, i, m[1])
		if err != nil {
			errs = append(errs, err)
		} else {
			blocks = append(blocks, b)
		}
		i = next
	}
	return blocks, errors.Join(errs...)
}

// parseBlock parses the marker on lines[i] and the block after it. It
// returns the index of the block's last line so the scan resumes after it.
func parseBlock(path string, lines []string, i int, body string) (Block, int, error) {
	b := Block{File: path, Attrs: map[string]string{}}
	if err := parseMarker(body, &b); err != nil {
		return b, i, fmt.Errorf("%s:%d: %w", path, i+1, err)
	}
	j := i + 1
	for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
		j++
	}
	if j < len(lines) {
		if fence := fenceRe.FindStringSubmatch(lines[j]); fence != nil {
			end, ok := fenceEnd(lines, j, fence[1])
			if !ok {
				return b, end, fmt.Errorf("%s:%d: fenced block is not closed", path, j+1)
			}
			b.Line, b.Lines = j+2, numbered(lines, j+1, end)
			return b, end, nil
		}
		if isTableLine(lines[j]) {
			end := j
			for end < len(lines) && isTableLine(lines[end]) {
				end++
			}
			b.Line, b.Lines = j+1, numbered(lines, j, end)
			b.Header, b.Rows = splitTable(b.Lines)
			return b, end - 1, nil
		}
	}
	return b, i, fmt.Errorf("%s:%d: docscheck marker is not followed by a fenced block or a table", path, i+1)
}

func parseMarker(body string, b *Block) error {
	for _, tok := range tokenRe.FindAllStringSubmatch(body, -1) {
		switch {
		case tok[1] != "":
			if tok[1] != "reason" {
				return fmt.Errorf("unknown docscheck key %q", tok[1])
			}
			b.Attrs[tok[1]] = strings.TrimSpace(tok[2])
		case knownKinds[tok[3]]:
			b.Kinds = append(b.Kinds, tok[3])
		default:
			return fmt.Errorf("unknown docscheck kind %q", tok[3])
		}
	}
	if len(b.Kinds) == 0 {
		return errors.New("docscheck marker names no kind")
	}
	if b.Has("skip") && utf8.RuneCountInString(b.Attrs["reason"]) < minReasonLen {
		return errors.New(`skip needs reason="..." of at least 10 characters`)
	}
	return nil
}

// fenceEnd returns the index of the line closing the fence opened at start,
// or len(lines) and false when it is never closed.
func fenceEnd(lines []string, start int, fence string) (int, bool) {
	for k := start + 1; k < len(lines); k++ {
		s := strings.TrimSpace(lines[k])
		if len(s) >= len(fence) && strings.Trim(s, fence[:1]) == "" {
			return k, true
		}
	}
	return len(lines), false
}

func numbered(lines []string, from, to int) []Line {
	out := make([]Line, 0, to-from)
	for k := from; k < to; k++ {
		out = append(out, Line{No: k + 1, Text: lines[k]})
	}
	return out
}

func isTableLine(s string) bool { return strings.HasPrefix(strings.TrimSpace(s), "|") }

func splitTable(ls []Line) ([]string, []Row) {
	var header []string
	var rows []Row
	for k, l := range ls {
		raw := splitCells(l.Text)
		switch {
		case k == 0:
			header = stripTicks(raw)
		case k == 1 && sepRe.MatchString(strings.TrimSpace(l.Text)):
		default:
			rows = append(rows, Row{Line: l.No, Cells: stripTicks(raw), Raw: raw})
		}
	}
	return header, rows
}

// splitCells splits a table line on unescaped pipes and unescapes \|.
func splitCells(text string) []string {
	s := strings.TrimPrefix(strings.TrimSpace(text), "|")
	if strings.HasSuffix(s, "|") && !strings.HasSuffix(s, `\|`) {
		s = s[:len(s)-1]
	}
	var cells []string
	var cur strings.Builder
	for k := 0; k < len(s); k++ {
		switch {
		case s[k] == '\\' && k+1 < len(s) && s[k+1] == '|':
			cur.WriteByte('|')
			k++
		case s[k] == '|':
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(s[k])
		}
	}
	return append(cells, strings.TrimSpace(cur.String()))
}

func stripTicks(cells []string) []string {
	out := make([]string, len(cells))
	for k, c := range cells {
		out[k] = strings.TrimSpace(strings.ReplaceAll(c, "`", ""))
	}
	return out
}

// Endpoint is one endpoint example found in a doc.
type Endpoint struct {
	File   string
	Line   int
	Method string
	Path   string
}

var (
	methodRe = regexp.MustCompile(`\b(GET|POST|PUT|PATCH|DELETE)\s+(/api/[^\s` + "`" + `'")|\]]+)`)
	urlRe    = regexp.MustCompile(`https?://[^/\s]+(/api/[^\s` + "`" + `'")|\]]+)`)
	curlRe   = regexp.MustCompile(`(^|[\s(|;&])curl(\s|$)`)
	xFlagRe  = regexp.MustCompile(`(^|\s)(-X|--request)[\s=]*([A-Za-z]+)`)
	dataRe   = regexp.MustCompile(`(^|\s)(-d|--data[\w-]*|-F|--form)([\s=]|$)`)
)

// ScanContract returns every endpoint example in the whole text of a
// contract doc, marked or not.
func ScanContract(path string) ([]Endpoint, error) {
	lines, err := readLines(path)
	if err != nil {
		return nil, err
	}
	return endpointsIn(path, numbered(lines, 0, len(lines))), nil
}

// endpointsIn extracts "METHOD /api/..." examples and the URLs of curl
// commands. A curl command may span lines joined by a trailing backslash.
func endpointsIn(file string, ls []Line) []Endpoint {
	var out []Endpoint
	for k, l := range ls {
		for _, m := range methodRe.FindAllStringSubmatch(l.Text, -1) {
			out = append(out, Endpoint{File: file, Line: l.No, Method: m[1], Path: normalizeEndpoint(m[2])})
		}
		urls := urlRe.FindAllStringSubmatch(l.Text, -1)
		if len(urls) == 0 {
			continue
		}
		cmd := commandAround(ls, k)
		if !curlRe.MatchString(cmd) {
			continue
		}
		for _, m := range urls {
			out = append(out, Endpoint{File: file, Line: l.No, Method: curlMethod(cmd), Path: normalizeEndpoint(m[1])})
		}
	}
	return out
}

func commandAround(ls []Line, k int) string {
	cont := func(s string) bool { return strings.HasSuffix(strings.TrimRight(s, " \t"), `\`) }
	start, end := k, k
	for start > 0 && cont(ls[start-1].Text) {
		start--
	}
	for end < len(ls)-1 && cont(ls[end].Text) {
		end++
	}
	parts := make([]string, 0, end-start+1)
	for _, l := range ls[start : end+1] {
		parts = append(parts, l.Text)
	}
	return strings.Join(parts, " ")
}

// curlMethod is the -X value, else POST when data is sent, else GET.
func curlMethod(cmd string) string {
	if m := xFlagRe.FindStringSubmatch(cmd); m != nil {
		return strings.ToUpper(m[3])
	}
	if dataRe.MatchString(cmd) {
		return "POST"
	}
	return "GET"
}

// normalizeEndpoint strips the query, the fragment, trailing punctuation
// and any trailing slash.
func normalizeEndpoint(p string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	return strings.TrimRight(strings.TrimRight(p, ".,;:"), "/")
}
