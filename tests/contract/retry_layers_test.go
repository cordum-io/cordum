package contract

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	retryLayersHeader = "| Layer | Path | Trigger | Re-runs handler | Limit | Setting | Code |"
	retryCapHeader    = "| Path | Factors | Cap |"
)

// retryQuotedDefault matches a Go default quoted in a Limit cell: `name = value`.
var retryQuotedDefault = regexp.MustCompile("`([A-Za-z_][A-Za-z0-9_]*) = ([^`]+)`")

// loadRetryLayers returns the repo root, the pinned CAP module's directory
// and the contract document.
func loadRetryLayers(t *testing.T) (root, capRoot, doc string) {
	t.Helper()
	root, err := repoRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/cordum-io/cap/v2")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("locate the pinned CAP module: %v", err)
	}
	capRoot = strings.TrimSpace(string(out))
	if capRoot == "" {
		t.Fatal("the pinned CAP module is not in the module cache; run go mod download")
	}
	raw, err := os.ReadFile(filepath.Join(root, "docs", "contract", "retry-layers.md"))
	if err != nil {
		t.Fatal(err)
	}
	return root, capRoot, string(raw)
}

func TestRetryLayersContract(t *testing.T) {
	root, capRoot, doc := loadRetryLayers(t)
	if problems := checkRetryLayers(root, capRoot, doc); len(problems) > 0 {
		t.Fatalf("retry-layers.md disagrees with the code:\n%s", strings.Join(problems, "\n"))
	}

	jobCap := "| job | client 1 × bus 1 × scheduler 50 × worker handler 1 × manual DLQ retry 1 | 50 |"
	layersHead := retryLayersHeader + "\n|" + strings.Repeat(" --- |", 7) + "\n"
	capsHead := retryCapHeader + "\n|" + strings.Repeat(" --- |", 3) + "\n"
	noCodeRow := "| client | job | t | No. | l | s |  |"
	cases := []struct {
		name string
		doc  string
		want []string
	}{
		{"missing symbol", strings.Replace(doc, "nats.go#isDurableSubject", "nats.go#isDurableTopic", 1),
			[]string{"core/infra/bus/nats.go#isDurableTopic does not resolve"}},
		{"missing CAP symbol", strings.Replace(doc, "runtime.go#WithRetries", "runtime.go#WithRetry", 1),
			[]string{"cap:sdk/go/runtime/runtime.go#WithRetry does not resolve"}},
		{"changed default", strings.Replace(doc, "`maxJSRedeliveries = 100`", "`maxJSRedeliveries = 99`", 1),
			[]string{"bus: maxJSRedeliveries = 99, code has 100"}},
		{"undeclared default", strings.Replace(doc, "`maxJSRedeliveries = 100`", "`maxJSRedelivery = 100`", 1),
			[]string{"bus: maxJSRedelivery is not declared in the row's Go files",
				"bus: Code cites maxJSRedeliveries, but Limit does not quote it"}},
		{"unquoted default", strings.Replace(doc, "At most `maxSchedulingRetries = 50` dispatches", "At most 50 dispatches", 1),
			[]string{"scheduler: Code cites maxSchedulingRetries, but Limit does not quote it"}},
		{"wrong product", strings.Replace(doc, jobCap, strings.TrimSuffix(jobCap, " 50 |")+" 51 |", 1),
			[]string{"cap job: 51, product 50"}},
		{"cap misses a layer", strings.Replace(doc, jobCap, strings.Replace(jobCap, " × bus 1", "", 1), 1),
			[]string{`cap job: factors name ["client" "scheduler" "worker handler" "manual DLQ retry"], ` +
				`layers on the path are ["client" "bus" "scheduler" "worker handler" "manual DLQ retry"]`}},
		{"empty tables", layersHead + "\n" + capsHead,
			[]string{"no rows under " + retryLayersHeader, "no rows under " + retryCapHeader,
				"no cap row for job", "no cap row for workflow step"}},
		{"no code ref", layersHead + noCodeRow + "\n\n" + capsHead + "| job | client 1 | 1 |\n| workflow step | client 1 | 1 |\n",
			[]string{"malformed row: " + noCodeRow, "no rows under " + retryLayersHeader,
				`cap job: factors name ["client"], layers on the path are []`,
				`cap workflow step: factors name ["client"], layers on the path are []`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := checkRetryLayers(root, capRoot, c.doc); !slices.Equal(got, c.want) {
				t.Errorf("problems = %q, want %q", got, c.want)
			}
		})
	}
}

// checkRetryLayers lists every way doc disagrees with the code under root and
// the CAP module at capRoot.
func checkRetryLayers(root, capRoot, doc string) []string {
	layers, problems := retryTableRows(doc, retryLayersHeader)
	caps, capProblems := retryTableRows(doc, retryCapHeader)
	problems = append(problems, capProblems...)
	onPath := map[string][]string{}
	for _, row := range layers {
		for _, path := range strings.Split(row[1], ", ") {
			onPath[path] = append(onPath[path], row[0])
		}
		problems = append(problems, checkRetryLayer(root, capRoot, row)...)
	}
	seen := map[string]bool{}
	for _, row := range caps {
		seen[row[0]] = true
		problems = append(problems, checkRetryCap(row, onPath[row[0]])...)
	}
	for _, path := range []string{"job", "workflow step"} {
		if !seen[path] {
			problems = append(problems, "no cap row for "+path)
		}
	}
	return problems
}

// retryTableRows returns the trimmed cells of the table under header.
func retryTableRows(doc, header string) ([][]string, []string) {
	if n := strings.Count("\n"+doc, "\n"+header+"\n"); n != 1 {
		return nil, []string{fmt.Sprintf("header appears %d times: %s", n, header)}
	}
	_, tail, _ := strings.Cut(doc, header+"\n")
	lines := strings.Split(tail, "\n")
	width := strings.Count(header, "|") - 1
	if lines[0] != "|"+strings.Repeat(" --- |", width) {
		return nil, []string{"bad separator under " + header}
	}
	var rows [][]string
	var problems []string
	for _, line := range lines[1:] {
		if !strings.HasPrefix(line, "|") {
			break
		}
		cells := strings.Split(strings.TrimSuffix(line[1:], "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if !strings.HasSuffix(line, "|") || len(cells) != width || slices.Contains(cells, "") {
			problems = append(problems, "malformed row: "+line)
			continue
		}
		rows = append(rows, cells)
	}
	if len(rows) == 0 {
		problems = append(problems, "no rows under "+header)
	}
	return rows, problems
}

// checkRetryLayer resolves a layer row's Code refs, compares each Go default
// its Limit quotes with the declaration in the row's Go files, and requires
// Limit to quote every const or var the row's Code cites.
func checkRetryLayer(root, capRoot string, row []string) []string {
	var problems, goFiles, cited []string
	for _, ref := range strings.Split(row[6], ",") {
		ref = strings.TrimSpace(ref)
		base, rel := root, ref
		if capRel, ok := strings.CutPrefix(ref, "cap:"); ok {
			base, rel = capRoot, capRel
		}
		file, symbol, _ := strings.Cut(rel, "#")
		if !hasSymbol(base, file, symbol) {
			problems = append(problems, ref+" does not resolve")
			continue
		}
		if full, err := repoFile(base, file); err == nil && strings.HasSuffix(file, ".go") {
			goFiles = append(goFiles, full)
			if _, ok := goDeclValue([]string{full}, symbol); ok {
				cited = append(cited, symbol)
			}
		}
	}
	quoted := map[string]bool{}
	for _, m := range retryQuotedDefault.FindAllStringSubmatch(row[4], -1) {
		quoted[m[1]] = true
		got, ok := goDeclValue(goFiles, m[1])
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("%s: %s is not declared in the row's Go files", row[0], m[1]))
		case got != m[2]:
			problems = append(problems, fmt.Sprintf("%s: %s = %s, code has %s", row[0], m[1], m[2], got))
		}
	}
	for _, name := range cited {
		if !quoted[name] {
			problems = append(problems, fmt.Sprintf("%s: Code cites %s, but Limit does not quote it", row[0], name))
		}
	}
	return problems
}

// goDeclValue prints the value of the first top-level const or var name
// declared in files.
func goDeclValue(files []string, name string) (string, bool) {
	for _, file := range files {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		for _, d := range f.Decls {
			g, ok := d.(*ast.GenDecl)
			if !ok || (g.Tok != token.CONST && g.Tok != token.VAR) {
				continue
			}
			for _, s := range g.Specs {
				v := s.(*ast.ValueSpec)
				for i, n := range v.Names {
					var b bytes.Buffer
					if n.Name == name && i < len(v.Values) && printer.Fprint(&b, fset, v.Values[i]) == nil {
						return b.String(), true
					}
				}
			}
		}
	}
	return "", false
}

// checkRetryCap requires a cap row's factors, each written `<layer> <n>` and
// joined by " × ", to name exactly the layers on its path, and its Cap to
// equal their product.
func checkRetryCap(row, layers []string) []string {
	product, names := 1, []string{}
	for _, factor := range strings.Split(row[1], " × ") {
		i := strings.LastIndex(factor, " ")
		n, err := strconv.Atoi(factor[i+1:])
		if i < 1 || err != nil || n < 1 {
			return []string{fmt.Sprintf("cap %s: bad factor %q", row[0], factor)}
		}
		product *= n
		names = append(names, factor[:i])
	}
	var problems []string
	if !slices.Equal(slices.Sorted(slices.Values(names)), slices.Sorted(slices.Values(layers))) {
		problems = append(problems, fmt.Sprintf("cap %s: factors name %q, layers on the path are %q", row[0], names, layers))
	}
	if row[2] != strconv.Itoa(product) {
		problems = append(problems, fmt.Sprintf("cap %s: %s, product %d", row[0], row[2], product))
	}
	return problems
}
