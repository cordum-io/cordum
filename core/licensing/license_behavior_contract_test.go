package licensing

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLicenseBehaviorContract(t *testing.T) {
	root := licenseContractRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "docs/contract/license-behavior.md"))
	require.NoError(t, err)
	doc := strings.ReplaceAll(string(raw), "\r\n", "\n")
	tests := licenseContractTests(t, root)
	states := strings.Fields("valid warning grace degraded invalid missing")
	var pairs []string
	for _, class := range []string{"policy evaluation", "approvals", "audit", "protected operations", "runner", "administration"} {
		for _, state := range states {
			pairs = append(pairs, class+"/"+state)
		}
	}
	for index, table := range []struct {
		header string
		keys   []string
		keyLen int
	}{
		{"| State | How it arises | Recorded status | Plan and entitlements | Break-glass state | Tests |", states, 1},
		{"| Class | State | Required behavior | Tests |", pairs, 2},
		{"| Entitlement | Required behavior after falling back to Community | Owner |", licenseContractInventory(), 1},
	} {
		var found []string
		for _, row := range licenseContractRows(t, doc, table.header) {
			found = append(found, strings.Join(row[:table.keyLen], "/"))
			if index == 2 {
				require.Contains(t, []string{"PR2-06", "PR2-06-GATEWAY", "PR2-06-KERNEL", "PR2-06-RUNNER"}, row[len(row)-1])
			} else {
				licenseContractReferences(t, row[len(row)-1], tests, index == 0)
			}
		}
		require.ElementsMatch(t, table.keys, found, "table %s", table.header)
	}
}

func licenseContractRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		raw, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		require.True(t, err == nil || os.IsNotExist(err), "read go.mod: %v", err)
		if strings.Contains("\n"+strings.ReplaceAll(string(raw), "\r\n", "\n"), "\nmodule github.com/cordum/cordum\n") {
			return dir
		}
		require.NotEqual(t, dir, filepath.Dir(dir), "cordum go.mod not found")
		dir = filepath.Dir(dir)
	}
}

func licenseContractRows(t *testing.T, doc, header string) [][]string {
	t.Helper()
	require.Equal(t, 1, strings.Count("\n"+doc, "\n"+header+"\n"), "missing or duplicate header: %s", header)
	_, tail, ok := strings.Cut(doc, header+"\n")
	require.True(t, ok)
	lines := strings.Split(tail, "\n")
	width := strings.Count(header, "|") - 1
	require.Equal(t, "|"+strings.Repeat(" --- |", width), lines[0], "table separator")
	var rows [][]string
	for _, line := range lines[1:] {
		if !strings.HasPrefix(line, "|") {
			break
		}
		require.True(t, strings.HasSuffix(line, "|"), "unterminated row: %s", line)
		cells := strings.Split(line[1:len(line)-1], "|")
		require.Len(t, cells, width, "row: %s", line)
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
			require.NotEmpty(t, cells[i], "empty cell: %s", line)
		}
		rows = append(rows, cells)
	}
	return rows
}

func licenseContractReferences(t *testing.T, cell string, tests map[string]bool, testsOnly bool) {
	t.Helper()
	allowedPending := map[string]bool{"pending PR2-06-GATEWAY": true, "pending PR2-06-KERNEL": true, "pending PR2-06-RUNNER": true}
	name := regexp.MustCompile("^`(Test[A-Za-z0-9_]+)(/[^`,[:space:]]+)?`$")
	finding := regexp.MustCompile(`^open finding PR2-06-F[0-9]+$`)
	for _, raw := range strings.Split(cell, ",") {
		ref := strings.TrimSpace(raw)
		if match := name.FindStringSubmatch(ref); match != nil {
			require.True(t, tests[match[1]], "test declaration not found: %s", ref)
		} else {
			require.True(t, !testsOnly && (allowedPending[ref] || finding.MatchString(ref)), "invalid reference: %s", ref)
		}
	}
}

func licenseContractTests(t *testing.T, root string) map[string]bool {
	t.Helper()
	tests := make(map[string]bool)
	for _, tree := range []string{"core", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, tree), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "vendor" || entry.Name() == "node_modules") {
				return filepath.SkipDir
			}
			if entry.IsDir() || !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil || fn.Body == nil {
					continue
				}
				var signature bytes.Buffer
				if err := format.Node(&signature, token.NewFileSet(), fn.Type); err != nil {
					return err
				}
				if signature.String() == "func(t *testing.T)" {
					tests[fn.Name.Name] = true
				}
			}
			return nil
		})
		require.NoError(t, err, "scan %s tests", tree)
	}
	return tests
}

func licenseContractInventory() []string {
	enterprise, community := reflect.ValueOf(DefaultEntitlements(PlanEnterprise)), reflect.ValueOf(DefaultEntitlements(PlanCommunity))
	keys := make(map[string]bool)
	for i := 0; i < enterprise.NumField(); i++ {
		field, a, b := enterprise.Type().Field(i), enterprise.Field(i), community.Field(i)
		key := strings.Split(field.Tag.Get("json"), ",")[0]
		if field.PkgPath != "" || key == "-" || key == "" {
			continue
		}
		if !reflect.DeepEqual(a.Interface(), b.Interface()) {
			keys[key] = true
		}
		if a.Kind() == reflect.Map {
			for _, k := range append(a.MapKeys(), b.MapKeys()...) {
				x, y := a.MapIndex(k), b.MapIndex(k)
				if x.IsValid() != y.IsValid() || (x.IsValid() && !reflect.DeepEqual(x.Interface(), y.Interface())) {
					keys[k.String()] = true
				}
			}
		}
	}
	var result []string
	for key := range keys {
		result = append(result, "`"+key+"`")
	}
	return result
}
