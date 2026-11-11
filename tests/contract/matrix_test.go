package contract

import (
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/infra/config"
)

var constraintsType = reflect.TypeOf(config.PolicyConstraints{})

type fixtureExtraTop struct {
	Budgets        config.BudgetConstraints    `yaml:"budgets"`
	Sandbox        config.SandboxProfile       `yaml:"sandbox"`
	Toolchain      config.ToolchainConstraints `yaml:"toolchain"`
	Diff           config.DiffConstraints      `yaml:"diff"`
	RedactionLevel string                      `yaml:"redaction_level"`
	Quota          int                         `yaml:"quota"`
}

type fixtureExtraNested struct {
	Budgets struct {
		MaxRuntimeMs      int64 `yaml:"max_runtime_ms"`
		MaxRetries        int32 `yaml:"max_retries"`
		MaxArtifactBytes  int64 `yaml:"max_artifact_bytes"`
		MaxConcurrentJobs int32 `yaml:"max_concurrent_jobs"`
		MaxGPU            int   `yaml:"max_gpu"`
	} `yaml:"budgets"`
	Sandbox        config.SandboxProfile       `yaml:"sandbox"`
	Toolchain      config.ToolchainConstraints `yaml:"toolchain"`
	Diff           config.DiffConstraints      `yaml:"diff"`
	RedactionLevel string                      `yaml:"redaction_level"`
}

// realMatrix loads a fresh in-memory copy of the repository's matrix.
func realMatrix(t *testing.T) (*EnforcementMatrix, string) {
	t.Helper()
	root, err := repoRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	m, err := LoadEnforcementMatrix(filepath.Join(root, "docs", "contract", "enforcement-matrix.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return m, root
}

// addGroup appends to the first row a copy of its first group that covers col.
func addGroup(m *EnforcementMatrix, col string) {
	g := m.Rows[0].Cells[0]
	m.Rows[0].Cells = append(m.Rows[0].Cells, MatrixCellGroup{Status: g.Status, Ref: g.Ref, Cols: []string{col}})
}

func hasMatrixIssue(is []MatrixIssue, rule, detail string) bool {
	return slices.ContainsFunc(is, func(i MatrixIssue) bool { return i.Rule == rule && strings.Contains(i.Detail, detail) })
}

func TestMatrixColumnsTrackPolicyConstraints(t *testing.T) {
	want := []string{"budgets.max_runtime_ms", "budgets.max_retries", "budgets.max_artifact_bytes", "budgets.max_concurrent_jobs",
		"sandbox.isolated", "sandbox.network_allowlist", "sandbox.fs_read_only", "sandbox.fs_read_write",
		"toolchain.allowed_tools", "toolchain.allowed_commands", "diff.max_files", "diff.max_lines", "diff.deny_path_globs", "redaction_level"}
	if got := constraintColumns(constraintsType); !slices.Equal(got, want) {
		t.Fatalf("constraintColumns = %v, want %v", got, want)
	}
	type inner struct{ D int }
	odd := reflect.TypeOf(struct {
		A    struct{ B int }
		C    *inner `yaml:"c,omitempty"`
		E    int    `yaml:"-"`
		priv int
	}{})
	if got := constraintColumns(odd); !slices.Equal(got, []string{"A.B", "c.D"}) {
		t.Errorf("untagged, pointer, skipped and unexported fields: got %v", got)
	}
	m, root := realMatrix(t)
	for _, tc := range []struct {
		name string
		typ  reflect.Type
		want string
	}{{"extra top-level field", reflect.TypeOf(fixtureExtraTop{}), "quota"}, {"extra nested field", reflect.TypeOf(fixtureExtraNested{}), "budgets.max_gpu"}} {
		if is := m.Validate(root, tc.typ); !hasMatrixIssue(is, "column-missing", tc.want) {
			t.Errorf("%s: want column-missing naming %s, got %+v", tc.name, tc.want, is)
		}
	}
	for _, i := range m.Validate(root, constraintsType) {
		if strings.HasPrefix(i.Rule, "column-") {
			t.Errorf("real PolicyConstraints: unexpected %s %s", i.Rule, i.Detail)
		}
	}
}

func TestMatrixRepoFileIsValid(t *testing.T) {
	m, root := realMatrix(t)
	for _, i := range m.Validate(root, constraintsType) {
		t.Errorf("%+v", i)
	}
	if len(matrixRequiredRows) != 15 || len(m.Columns) != 18 {
		t.Fatalf("want 15 required rows and 18 columns, got %d and %d", len(matrixRequiredRows), len(m.Columns))
	}
	want, counts := slices.Sorted(slices.Values(m.Columns)), map[string]int{}
	for _, r := range m.Rows {
		var cols []string
		for _, g := range r.Cells {
			cols, counts[g.Status] = append(cols, g.Cols...), counts[g.Status]+len(g.Cols)
			if slices.Contains(matrixCodeReadRows, r.ID) && g.Ref == "not verified" {
				t.Errorf("%s: code-read row has a not verified cell %v", r.ID, g.Cols)
			}
		}
		if slices.Sort(cols); !slices.Equal(cols, want) {
			t.Errorf("%s: covers %v, want each declared column once", r.ID, cols)
		}
	}
	for _, id := range matrixRequiredRows {
		if !slices.ContainsFunc(m.Rows, func(r MatrixRow) bool { return r.ID == id }) {
			t.Errorf("required row %s missing", id)
		}
	}
	t.Logf("rows=%d cells by status: %v", len(m.Rows), counts)
}

func TestMatrixRejects(t *testing.T) {
	row := func(m *EnforcementMatrix, id string) *MatrixRow {
		return &m.Rows[slices.IndexFunc(m.Rows, func(r MatrixRow) bool { return r.ID == id })]
	}
	cases := []struct {
		rule   string
		mutate func(m *EnforcementMatrix)
	}{
		{"schema-version", func(m *EnforcementMatrix) { m.SchemaVersion = 2 }},
		{"reviewed-at", func(m *EnforcementMatrix) { m.ReviewedAt = strings.ToUpper(m.ReviewedAt) + "0" }},
		{"row-missing", func(m *EnforcementMatrix) { m.Rows = slices.Delete(m.Rows, 0, 1) }},
		{"row-duplicate", func(m *EnforcementMatrix) { m.Rows = append(m.Rows, m.Rows[0]) }},
		{"row-anchor", func(m *EnforcementMatrix) { m.Rows[0].Anchors[0].Symbol = "Nope.Missing" }},
		{"column-missing", func(m *EnforcementMatrix) { m.Columns = m.Columns[1:] }},
		{"column-unknown", func(m *EnforcementMatrix) { m.Columns = append(m.Columns, "foo") }},
		{"cell-missing", func(m *EnforcementMatrix) { m.Rows[0].Cells[0].Cols = m.Rows[0].Cells[0].Cols[1:] }},
		{"cell-duplicate", func(m *EnforcementMatrix) { addGroup(m, m.Rows[0].Cells[0].Cols[0]) }},
		{"cell-column", func(m *EnforcementMatrix) { addGroup(m, "foo") }},
		{"cell-status", func(m *EnforcementMatrix) { m.Rows[0].Cells[0].Status = "partial" }},
		{"cell-ref", func(m *EnforcementMatrix) { m.Rows[0].Cells[0].Ref = "core/nope.go:1" }},
		{"cell-ref", func(m *EnforcementMatrix) { m.Rows[0].Cells[0].Ref = m.Rows[0].Anchors[0].Path + ":999999" }},
		{"cell-ref", func(m *EnforcementMatrix) { m.Rows[0].Cells[0].Ref = "foo" }},
		{"cell-ref-required", func(m *EnforcementMatrix) { row(m, "scheduler-dispatch").Cells[0].Ref = "not verified" }},
		{"finding-id", func(m *EnforcementMatrix) { m.Rows[0].Cells[0].Finding = "R16" }},
		{"row-missing", func(m *EnforcementMatrix) { *m = EnforcementMatrix{} }}, // nil slices must not panic
	}
	for i, tc := range cases {
		t.Run(tc.rule, func(t *testing.T) {
			m, root := realMatrix(t)
			tc.mutate(m)
			if is := m.Validate(root, constraintsType); !hasMatrixIssue(is, tc.rule, "") {
				t.Errorf("case %d: want %s, got %+v", i, tc.rule, is)
			}
		})
	}
}

func TestMatrixLoadRejects(t *testing.T) {
	for name, text := range map[string]string{
		"unknown key":     "schema_version: 1\nbogus: 1\n",
		"malformed":       "rows: [\n",
		"second document": "schema_version: 1\n---\nschema_version: 1\n",
	} {
		p := filepath.Join(t.TempDir(), "matrix.yaml")
		write(t, p, text)
		if _, err := LoadEnforcementMatrix(p); err == nil {
			t.Errorf("%s: want a load error", name)
		}
	}
}
