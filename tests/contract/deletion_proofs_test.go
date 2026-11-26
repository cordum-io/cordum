package contract

import (
	"fmt"
	"go/ast"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const retentionHeader = "| Entry | Category | Cleanup path | Proof |"
const holdHeader = "| Entry | Proof |"

func retentionFixture(t *testing.T) (string, *DataInventory, string) {
	t.Helper()
	root, err := repoRoot(".")
	require.NoError(t, err)
	inventory, err := LoadDataInventory(filepath.Join(root, "docs/contract/data-inventory.yaml"))
	require.NoError(t, err)
	raw, err := os.ReadFile(filepath.Join(root, "docs/contract/deletion-proofs.md"))
	require.NoError(t, err)
	return root, inventory, strings.ReplaceAll(string(raw), "\r\n", "\n")
}

func TestRetentionProofMapContract(t *testing.T) {
	root, inventory, doc := retentionFixture(t)
	tests := retentionTestNames(t, root)
	require.NoError(t, checkRetentionMap(doc, inventory, tests))
	for _, proof := range []string{"`TestRetentionTraceScanFindsEveryType`", "`TestRetentionBackupWindow`", "open finding PR2-08-F1", "`TestRetentionBackupWindow`, pending PR2-08-DATA"} {
		require.NoError(t, checkRetentionMap(strings.Replace(doc, "pending PR2-08-EDGE", proof, 1), inventory, tests))
	}
}

func TestRetentionProofMapRejects(t *testing.T) {
	// Independent fixture: filling pending cells in the real map must not change
	// these negative controls or cause them to fail for an unrelated reference.
	held := true
	inventory := &DataInventory{EndpointOnly: []DataEntry{{ID: "DI-E01", Deletion: &DataDeletion{None: "local"}}},
		ControlPlane: []DataEntry{{ID: "DI-C01", Deletion: &DataDeletion{}, LegalHold: &DataLegalHold{Applies: &held}}}}
	doc := retentionHeader + "\n| --- | --- | --- | --- |\n| DI-E01 | no cleanup path | none | none: local |\n" +
		"| DI-C01 | retention cleanup | sweep | pending PR2-08-EDGE |\n\n" +
		holdHeader + "\n| --- | --- |\n| DI-C01 | pending PR2-08-EDGE |\n"
	require.NoError(t, checkRetentionMap(doc, inventory, nil))
	line := strings.Split(strings.Split(doc, "| DI-E01 |")[1], "\n")[0]
	for _, tc := range []struct{ name, old, replacement, errorText string }{
		{"missing", "| DI-E01 |" + line + "\n", "", "missing entry DI-E01"},
		{"category", "| DI-E01 | no cleanup path |", "| DI-E01 | retention cleanup |", "category"},
		{"unknown_test", "pending PR2-08-EDGE", "`TestRetentionDoesNotExist`", "reference"},
		{"duplicate", "| DI-E01 |" + line, "| DI-E01 |" + line + "\n| DI-E01 |" + line, "duplicate"},
		{"unknown_id", "| DI-E01 |" + line, "| DI-E01 |" + line + "\n| DI-E99 |" + line, "unknown entry"},
		{"missing_hold", "| DI-C01 | pending PR2-08-EDGE |\n", "", "legal hold"},
		{"extra_hold", "| DI-C01 | pending PR2-08-EDGE |", "| DI-C02 | pending PR2-08-EDGE |", "legal hold"},
		{"owner", "pending PR2-08-EDGE", "pending PR2-08-OTHER", "reference"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := strings.Replace(doc, tc.old, tc.replacement, 1)
			require.NotEqual(t, doc, changed)
			file := filepath.Join(t.TempDir(), "deletion-proofs.md")
			write(t, file, changed)
			raw, err := os.ReadFile(file)
			require.NoError(t, err)
			require.ErrorContains(t, checkRetentionMap(string(raw), inventory, nil), tc.errorText)
		})
	}
}

func checkRetentionMap(doc string, inventory *DataInventory, tests map[string]bool) error {
	rows, err := retentionRows(doc, retentionHeader)
	if err != nil {
		return err
	}
	holds, err := retentionRows(doc, holdHeader)
	if err != nil {
		return err
	}
	for _, e := range append(append([]DataEntry{}, inventory.EndpointOnly...), inventory.ControlPlane...) {
		row, ok := rows[e.ID]
		if !ok {
			return fmt.Errorf("missing entry %s", e.ID)
		}
		delete(rows, e.ID)
		if err := retentionCategory(e, row, tests); err != nil {
			return err
		}
		if e.LegalHold != nil && e.LegalHold.Applies != nil && *e.LegalHold.Applies {
			hold, ok := holds[e.ID]
			if !ok {
				return fmt.Errorf("missing legal hold %s", e.ID)
			}
			if err := retentionReferences(hold[1], tests); err != nil {
				return err
			}
			delete(holds, e.ID)
		}
	}
	if len(rows) != 0 {
		return fmt.Errorf("unknown entry: %v", rows)
	}
	if len(holds) != 0 {
		return fmt.Errorf("unexpected legal hold: %v", holds)
	}
	return nil
}

func retentionCategory(e DataEntry, row []string, tests map[string]bool) error {
	category := row[1]
	if e.Deletion == nil || (category == "no cleanup path") != (e.Deletion.None != "") {
		return fmt.Errorf("%s: category disagrees with inventory", e.ID)
	}
	switch category {
	case "retention cleanup":
		return retentionReferences(row[3], tests)
	case "explicit delete only", "no cleanup path":
		if !strings.HasPrefix(row[3], "none: ") || strings.TrimSpace(strings.TrimPrefix(row[3], "none: ")) == "" {
			return fmt.Errorf("%s: missing none reason", e.ID)
		}
	default:
		return fmt.Errorf("%s: unknown category %q", e.ID, category)
	}
	return nil
}

func retentionRows(doc, header string) (map[string][]string, error) {
	if strings.Count("\n"+doc, "\n"+header+"\n") != 1 {
		return nil, fmt.Errorf("missing or duplicate table %s", header)
	}
	_, tail, _ := strings.Cut(doc, header+"\n")
	lines := strings.Split(tail, "\n")
	width := strings.Count(header, "|") - 1
	if lines[0] != "|"+strings.Repeat(" --- |", width) {
		return nil, fmt.Errorf("invalid table separator %s", header)
	}
	rows := map[string][]string{}
	for _, line := range lines[1:] {
		if !strings.HasPrefix(line, "|") {
			break
		}
		if !strings.HasSuffix(line, "|") {
			return nil, fmt.Errorf("unterminated row %s", line)
		}
		cells := strings.Split(line[1:len(line)-1], "|")
		if len(cells) != width {
			return nil, fmt.Errorf("invalid row width %s", line)
		}
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
			if cells[i] == "" {
				return nil, fmt.Errorf("empty cell %s", line)
			}
		}
		if _, exists := rows[cells[0]]; exists {
			return nil, fmt.Errorf("duplicate entry %s", cells[0])
		}
		rows[cells[0]] = cells
	}
	return rows, nil
}

func retentionReferences(cell string, tests map[string]bool) error {
	name := regexp.MustCompile("^`(Test[A-Za-z0-9_]+)(/[^`,[:space:]]+)?`$")
	pending := regexp.MustCompile(`^pending PR2-08-(EDGE|JOBS|DATA|SERVICES)$`)
	finding := regexp.MustCompile(`^open finding PR2-08-F[0-9]+$`)
	for _, raw := range strings.Split(cell, ",") {
		ref := strings.TrimSpace(raw)
		if match := name.FindStringSubmatch(ref); match != nil {
			if tests[match[1]] {
				continue
			}
		} else if pending.MatchString(ref) || finding.MatchString(ref) {
			continue
		}
		return fmt.Errorf("invalid proof reference %q", ref)
	}
	return nil
}

func retentionTestNames(t *testing.T, root string) map[string]bool {
	t.Helper()
	tests := map[string]bool{}
	for _, tree := range []string{"core", "cmd", "tests"} {
		err := filepath.WalkDir(filepath.Join(root, tree), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := goFile(path) // go/parser, shared with the inventory checker.
			if err != nil {
				return err
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if ok && fn.Recv == nil && fn.Body != nil && types.ExprString(fn.Type) == "func(t *testing.T)" {
					tests[fn.Name.Name] = true
				}
			}
			return nil
		})
		require.NoError(t, err)
	}
	return tests
}

func TestRetentionBackupWindow(t *testing.T) {
	root, _, doc := retentionFixture(t)
	raw, err := os.ReadFile(filepath.Join(root, "deploy/k8s/production/backup.yaml"))
	require.NoError(t, err)
	manifest := strings.ReplaceAll(string(raw), "\r\n", "\n")
	require.NoError(t, checkRetentionBackup(doc, manifest))
	for _, change := range []struct{ old, replacement string }{
		{"0 * * * *", "0 0 * * *"}, {"30 3 * * *", "30 3 * * 1"},
		{"-mtime +7", "-mtime +8"}, {"within 9 days", "within 8 days"},
	} {
		require.Error(t, checkRetentionBackup(strings.ReplaceAll(doc, change.old, change.replacement), strings.ReplaceAll(manifest, change.old, change.replacement)))
	}
}

func checkRetentionBackup(doc, manifest string) error {
	var retention string
	for name, schedule := range map[string]string{"cordum-redis-backup": "0 * * * *", "cordum-nats-backup": "0 * * * *", "cordum-backup-retention": "30 3 * * *"} {
		found := 0
		for _, part := range strings.Split(manifest, "---\n") {
			if strings.Contains(part, "\n  name: "+name+"\n") && strings.Contains(part, "kind: CronJob\n") && strings.Contains(part, "\n  schedule: \""+schedule+"\"\n") {
				found++
				if name == "cordum-backup-retention" {
					retention = part
				}
			}
		}
		if found != 1 {
			return fmt.Errorf("backup schedule %s", name)
		}
	}
	for _, command := range []string{
		"find /backup -name '*.rdb' -mtime +7 -delete -print",
		"find /backup -name 'redis-*.topology' -mtime +7 -delete -print",
		"find /backup -name 'redis-*.sha256*' -mtime +7 -delete -print",
		"find /backup -maxdepth 1 -name 'nats-*' -type d -mtime +7 -exec rm -rf {} + -print",
	} {
		if !strings.Contains(retention, "\n              "+command+"\n") {
			return fmt.Errorf("backup cleanup changed: %s", command)
		}
	}
	if !strings.Contains(doc, "For `deploy/k8s/production`, data deleted live leaves the newest backup that holds it within 9 days") {
		return fmt.Errorf("backup bound must state 9 days")
	}
	return nil
}
