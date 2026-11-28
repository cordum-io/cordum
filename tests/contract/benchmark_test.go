package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const benchmarkIDs = `protection.unauthorized_effects protection.mutation_rejection
protection.replay_rejection protection.cross_tenant_rejection protection.revocation_rejection
protection.constraint_enforcement protection.bypass_coverage protection.evidence_integrity
protection.recovery_correctness utility.legitimate_completion utility.false_blocks
utility.unnecessary_approvals utility.reviewer_time utility.installation_effort
utility.upgrade_effort utility.sustained_enforced_usage operations.latency
operations.throughput operations.outage_behavior operations.recovery_time
operations.unknown_outcome_reconciliation operations.support_burden operations.deployment_cost`

func benchmarkSpec(t *testing.T) (string, string) {
	t.Helper()
	root, err := repoRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs/contract/benchmark-spec.md"))
	if err != nil {
		t.Fatal(err)
	}
	return root, string(data)
}

func benchmarkExample(t *testing.T) (string, []byte) {
	t.Helper()
	root, spec := benchmarkSpec(t)
	_, rest, ok := strings.Cut(spec, "```json\n")
	if !ok {
		t.Fatal("benchmark spec has no JSON example")
	}
	example, _, ok := strings.Cut(rest, "\n```")
	if !ok || !json.Valid([]byte(example)) {
		t.Fatal("benchmark spec JSON example is invalid or unclosed")
	}
	return root, []byte(example)
}

func TestBenchmarkMeasuresContract(t *testing.T) {
	root, _ := benchmarkSpec(t)
	rows, err := BenchmarkMeasures(filepath.Join(root, "docs/contract/benchmark-spec.md"))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, row := range rows {
		if row.ID == "" || row.Dimension == "" || row.Measure == "" || row.Definition == "" || row.Unit == "" || row.Method == "" {
			t.Errorf("incomplete measure: %+v", row)
		}
		if !strings.HasPrefix(row.ID, strings.ToLower(row.Dimension)+".") {
			t.Errorf("dimension does not match ID: %+v", row)
		}
		ids = append(ids, row.ID)
	}
	want := strings.Fields(benchmarkIDs)
	slices.Sort(ids)
	slices.Sort(want)
	if !slices.Equal(ids, want) {
		t.Fatalf("measure IDs (including duplicates): got %v, want %v", ids, want)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs/contract/benchmark-result.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties struct {
			Measures struct{ Required []string }
		}
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	slices.Sort(schema.Properties.Measures.Required)
	if !slices.Equal(schema.Properties.Measures.Required, ids) {
		t.Fatalf("schema required IDs differ from table: %v", schema.Properties.Measures.Required)
	}
}

func TestBenchmarkMeasuresParser(t *testing.T) {
	header := "| ID | Dimension | Measure | Definition | Unit | Method |\n| --- | --- | --- | --- | --- | --- |\n"
	row := "| x | D | M | Definition | units | Method |\n"
	for _, tc := range []struct {
		name, text string
		valid      bool
	}{
		{"valid", header + row + "\nOutside table", true},
		{"CRLF", strings.ReplaceAll(header+row, "\n", "\r\n"), true},
		{"missing table", "# No measures", false},
		{"empty table", header, false},
		{"wrong column count", header + "| x | D |\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "spec.md")
			write(t, p, tc.text)
			rows, err := BenchmarkMeasures(p)
			if !tc.valid {
				if err == nil || len(rows) != 0 {
					t.Fatalf("invalid table returned rows=%v err=%v", rows, err)
				}
				return
			}
			want := []BenchmarkMeasure{{ID: "x", Dimension: "D", Measure: "M", Definition: "Definition", Unit: "units", Method: "Method"}}
			if err != nil || !slices.Equal(rows, want) {
				t.Fatalf("got rows=%v err=%v, want %v", rows, err, want)
			}
		})
	}
	if rows, err := BenchmarkMeasures(filepath.Join(t.TempDir(), "missing.md")); err == nil || len(rows) != 0 {
		t.Fatalf("missing spec returned rows=%v err=%v", rows, err)
	}
}

func TestBenchmarkResultExample(t *testing.T) {
	root, example := benchmarkExample(t)
	if err := ValidateBenchmarkResult(root, example); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`0`, `1.5`, `"unsupported"`, `"not measured"`} {
		t.Run(value, func(t *testing.T) {
			data := editBenchmarkJSON(t, example, []string{"measures", "operations.latency", "value"}, json.RawMessage(value))
			if err := ValidateBenchmarkResult(root, data); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// editBenchmarkJSON changes or deletes one field without untyped JSON values.
func editBenchmarkJSON(t *testing.T, data []byte, path []string, value json.RawMessage) []byte {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if len(path) > 1 {
		fields[path[0]] = editBenchmarkJSON(t, fields[path[0]], path[1:], value)
	} else if len(value) == 0 {
		delete(fields, path[0])
	} else {
		fields[path[0]] = value
	}
	out, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestBenchmarkResultRejectsInvalid(t *testing.T) {
	root, example := benchmarkExample(t)
	for _, tc := range []struct{ path, value string }{
		{"measures/operations.latency/value", `""`},
		{"measures/operations.latency/value", `"n/a"`},
		{"measures/operations.latency/value", `null`},
		{"measures/operations.latency/value", `true`},
		{"measures/operations.latency/value", `{}`},
		{"measures/operations.latency/value", ""},
		{"measures/operations.latency/note", `1`},
		{"measures/operations.latency/extra", `1`},
		{"measures/unknown", `{"value": 1}`},
		{"deployment_boundary", ""},
		{"deployment_boundary", `" \t\r\n"`},
		{"deployment_boundary", `1`},
		{"schema_version", `2`},
		{"schema_version", ""},
		{"system", ""},
		{"system/name", `""`},
		{"system/name", ""},
		{"system/version", ""},
		{"system/extra", `1`},
		{"measures", ""},
		{"extra", `1`},
	} {
		t.Run(tc.path+"="+tc.value, func(t *testing.T) {
			data := editBenchmarkJSON(t, example, strings.Split(tc.path, "/"), json.RawMessage(tc.value))
			if err := ValidateBenchmarkResult(root, data); err == nil {
				t.Fatal("invalid result accepted")
			}
		})
	}
}

func TestBenchmarkResultRequiresEveryMeasure(t *testing.T) {
	root, example := benchmarkExample(t)
	for _, id := range strings.Fields(benchmarkIDs) {
		t.Run(id, func(t *testing.T) {
			data := editBenchmarkJSON(t, example, []string{"measures", id}, nil)
			if err := ValidateBenchmarkResult(root, data); err == nil {
				t.Fatal("result with missing measure accepted")
			}
		})
	}
}

func TestBenchmarkResultLoadErrors(t *testing.T) {
	root, example := benchmarkExample(t)
	if err := ValidateBenchmarkResult(root, []byte(`{`)); err == nil {
		t.Fatal("malformed JSON accepted")
	}
	if err := ValidateBenchmarkResult(t.TempDir(), example); err == nil {
		t.Fatal("missing schema accepted")
	}
}

func TestBenchmarkWorkloads(t *testing.T) {
	root, spec := benchmarkSpec(t)
	links := regexp.MustCompile(`\]\(([^)]+/workloads/[^)]+\.yaml)\)`).FindAllStringSubmatch(spec, -1)
	var names []string
	for _, link := range links {
		p := filepath.Join(root, "docs/contract", filepath.FromSlash(link[1]))
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() {
			t.Errorf("workload is not a regular file: %s", p)
		}
		names = append(names, filepath.Base(p))
	}
	slices.Sort(names)
	want := []string{"edge-hook-evaluate.yaml", "job-submit-approval.yaml", "job-submit.yaml", "mcp-tool-call.yaml"}
	if !slices.Equal(names, want) {
		t.Fatalf("workload links: got %v, want %v", names, want)
	}
	if !strings.Contains(spec, "M6-02 (planned)") || !strings.Contains(spec, "not measured") {
		t.Fatal("planned adversarial corpus and measurement gap must be explicit")
	}
}
