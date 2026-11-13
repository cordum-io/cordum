package contract

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// EnforcementMatrix is docs/contract/enforcement-matrix.yaml: for each entry
// point (row) and control (column), a status and a code reference.
type EnforcementMatrix struct {
	SchemaVersion int    `yaml:"schema_version"`
	ReviewedAt    string `yaml:"reviewed_at"`
	Columns       []string
	Rows          []MatrixRow
}

// MatrixRow is one entry point, located in the code by its anchors.
type MatrixRow struct {
	ID      string
	Anchors []EnforcementPoint
	Cells   []MatrixCellGroup
}

// MatrixCellGroup gives each column in Cols the same status, ref and finding.
type MatrixCellGroup struct {
	Status, Ref string
	Finding     string `yaml:"finding,omitempty"`
	Cols        []string
}

// MatrixIssue is one rule violation; Row is empty for matrix-level rules.
type MatrixIssue struct{ Rule, Row, Detail string }

const matrixNotVerified = "not verified"

var (
	matrixControls     = []string{"deny", "require_approval", "throttle", "output_scanning"}
	matrixRequiredRows = []string{"edge-hook-observe", "edge-hook-enforce", "edge-hook-enterprise-strict", "mcp-tool-call",
		"mcp-approval-gate", "job-submit-http", "job-submit-grpc", "scheduler-dispatch", "scheduler-approval-fast-path",
		"workflow-step", "dlq-retry", "workflow-rerun", "pending-replay", "orphan-replay", "saga-compensation"}
	matrixCodeReadRows = []string{"edge-hook-observe", "edge-hook-enforce", "edge-hook-enterprise-strict", "mcp-tool-call",
		"scheduler-dispatch", "scheduler-approval-fast-path"}
	matrixStatuses = []string{"enforced", "advisory", "ignored", "not_applicable"}
	matrixRefRE    = regexp.MustCompile(`^([A-Za-z0-9_./-]+):([1-9][0-9]*)$`)
)

// constraintColumns names every leaf field of t by its dotted yaml path. An
// untagged field uses its Go name, so a new field always demands a column.
func constraintColumns(t reflect.Type) []string {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return nil
	}
	var out []string
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if !f.IsExported() || name == "-" {
			continue
		}
		name = cmp.Or(name, f.Name)
		subs := constraintColumns(f.Type)
		if len(subs) == 0 {
			out = append(out, name)
		}
		for _, s := range subs {
			out = append(out, name+"."+s)
		}
	}
	return out
}

// LoadEnforcementMatrix reads one YAML document, rejecting unknown keys and
// any further document.
func LoadEnforcementMatrix(file string) (*EnforcementMatrix, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var m EnforcementMatrix
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if err := dec.Decode(&yaml.Node{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: more than one YAML document", file)
	}
	return &m, nil
}

// Validate checks the matrix against constraints and the tree at root, with
// issues in declared row and column order.
func (m *EnforcementMatrix) Validate(root string, constraints reflect.Type) []MatrixIssue {
	var is []MatrixIssue
	add := func(rule, row, detail string) { is = append(is, MatrixIssue{rule, row, detail}) }
	if m.SchemaVersion != 1 {
		add("schema-version", "", fmt.Sprintf("schema_version is %d, want 1", m.SchemaVersion))
	}
	if !shaRE.MatchString(m.ReviewedAt) {
		add("reviewed-at", "", "reviewed_at must be 40 lowercase hex characters")
	}
	want := append(slices.Clone(matrixControls), constraintColumns(constraints)...)
	for _, c := range want {
		if !slices.Contains(m.Columns, c) {
			add("column-missing", "", c)
		}
	}
	for i, c := range m.Columns {
		if !slices.Contains(want, c) {
			add("column-unknown", "", c)
		} else if slices.Index(m.Columns, c) < i {
			add("column-duplicate", "", c)
		}
	}
	ids, lines := map[string]bool{}, map[string]int{}
	for _, r := range m.Rows {
		if ids[r.ID] {
			add("row-duplicate", r.ID, "id is used by an earlier row")
		}
		ids[r.ID] = true
		m.validateRow(root, r, lines, add)
	}
	for _, id := range matrixRequiredRows {
		if !ids[id] {
			add("row-missing", id, "required row is absent")
		}
	}
	return is
}

func (m *EnforcementMatrix) validateRow(root string, r MatrixRow, lines map[string]int, add func(rule, row, detail string)) {
	if len(r.Anchors) == 0 {
		add("row-anchor", r.ID, "a row needs at least one anchor")
	}
	for _, a := range r.Anchors {
		if !hasSymbol(root, a.Path, a.Symbol) {
			add("row-anchor", r.ID, fmt.Sprintf("%q not found in %s", a.Symbol, a.Path))
		}
	}
	seen := map[string]bool{}
	for _, g := range r.Cells {
		for _, c := range g.Cols {
			if !slices.Contains(m.Columns, c) {
				add("cell-column", r.ID, c+" is not a declared column")
			} else if seen[c] {
				add("cell-duplicate", r.ID, c+" is in more than one group")
			}
			seen[c] = true
		}
		nv, detail := g.Ref == matrixNotVerified, fmt.Sprintf("status %q, ref %q, finding %q for %v", g.Status, g.Ref, g.Finding, g.Cols)
		for _, c := range []struct {
			rule string
			bad  bool
		}{
			{"cell-status", !slices.Contains(matrixStatuses, g.Status)},
			{"finding-id", g.Finding != "" && !findRE.MatchString(g.Finding)},
			{"cell-ref-required", nv && slices.Contains(matrixCodeReadRows, r.ID)},
			{"cell-ref", !nv && !matrixRefOK(root, g.Ref, lines)},
		} {
			if c.bad {
				add(c.rule, r.ID, detail)
			}
		}
	}
	for _, c := range m.Columns {
		if !seen[c] {
			add("cell-missing", r.ID, c+" has no cell")
		}
	}
}

// matrixRefOK accepts path:line where path is a repo file and line is inside
// it. lines caches each file's line count (-1: not readable).
func matrixRefOK(root, ref string, lines map[string]int) bool {
	sm := matrixRefRE.FindStringSubmatch(ref)
	if sm == nil {
		return false
	}
	n, ok := lines[sm[1]]
	if !ok {
		n = -1
		if full, err := repoFile(root, sm[1]); err == nil {
			if b, err := os.ReadFile(full); err == nil {
				n = bytes.Count(b, []byte("\n"))
				if len(b) > 0 && b[len(b)-1] != '\n' {
					n++
				}
			}
		}
		lines[sm[1]] = n
	}
	line, err := strconv.Atoi(sm[2])
	return err == nil && line <= n
}
