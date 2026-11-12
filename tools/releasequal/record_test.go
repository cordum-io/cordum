package releasequal

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	infraschema "github.com/cordum/cordum/core/infra/schema"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/valid-record.json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func encoded(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func changed(t *testing.T, path, key string, value any, remove bool) []byte {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(fixture(t), &v); err != nil {
		t.Fatal(err)
	}
	m := v
	if path != "" {
		if items, ok := v[path].([]any); ok {
			m = items[0].(map[string]any)
		} else {
			m = v[path].(map[string]any)
		}
	}
	if remove {
		delete(m, key)
	} else {
		m[key] = value
	}
	return encoded(t, v)
}

func wantError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v; want error containing %q", err, want)
	}
}

func TestSchemaAcceptsValidRecord(t *testing.T) {
	if err := infraschema.ValidateSchema(SchemaName, SchemaJSON(), fixture(t)); err != nil {
		t.Fatal(err)
	}
	data := SchemaJSON()
	data[0] = '!'
	if SchemaJSON()[0] != '{' {
		t.Fatal("SchemaJSON leaked mutable embedded bytes")
	}
}

func TestSchemaRejectsMutatedRecords(t *testing.T) {
	required := map[string]string{
		"": "schema_version release source artifacts gates exceptions", "release": "ref dry_run run_url",
		"source": "repository sha", "artifacts": "kind name ref digest", "gates": "id artifact digest result log",
		"exceptions": "id artifacts reason",
	}
	for path, keys := range required {
		for _, key := range strings.Fields(keys) {
			t.Run("missing/"+path+"/"+key, func(t *testing.T) {
				err := infraschema.ValidateSchema(SchemaName, SchemaJSON(), changed(t, path, key, nil, true))
				t.Log(err)
				wantError(t, err, key)
			})
		}
	}
	testSchemaMutations(t)
}

func testSchemaMutations(t *testing.T) {
	cases := []struct {
		path, key string
		value     any
		want      string
	}{
		{"", "unknown", true, "unknown"}, {"artifacts", "unknown", true, "unknown"},
		{"", "schema_version", 2, "/schema_version"},
		{"source", "sha", strings.Repeat("A", 40), "/source/sha"}, {"source", "sha", strings.Repeat("a", 39), "/source/sha"},
		{"source", "repository", "cordum", "/source/repository"},
		{"artifacts", "digest", "sha512:" + strings.Repeat("a", 64), "/artifacts/0/digest"},
		{"artifacts", "digest", "sha256:" + strings.Repeat("a", 63), "/artifacts/0/digest"},
		{"artifacts", "kind", "chart", "/artifacts/0/kind"}, {"gates", "result", "skipped", "/gates/0/result"},
		{"exceptions", "reason", "", "/exceptions/0/reason"}, {"exceptions", "artifacts", []string{}, "/exceptions/0/artifacts"},
		{"", "artifacts", []string{}, "/artifacts"}, {"release", "run_url", "http://example.com", "/release/run_url"},
		{"release", "ref", "main", "/release/ref"}, {"artifacts", "name", "API", "/artifacts/0/name"},
		{"release", "ref", "refs/tags/v1 x", "/release/ref"}, {"release", "run_url", "https://example.com/ x", "/release/run_url"},
	}
	for i, tc := range cases {
		t.Run(tc.path+"/"+tc.key+"/"+string(rune('a'+i)), func(t *testing.T) {
			err := infraschema.ValidateSchema(SchemaName, SchemaJSON(), changed(t, tc.path, tc.key, tc.value, false))
			t.Log(err)
			wantError(t, err, tc.want)
		})
	}
}

func parsed(t *testing.T) *Record {
	t.Helper()
	r, err := Parse(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestParseValidRecord(t *testing.T) {
	r := parsed(t)
	if r.Source.SHA != strings.Repeat("a", 40) || len(r.Artifacts) != 2 || len(r.Gates) != 2 || len(r.Exceptions) != 2 {
		t.Fatalf("unexpected record: %+v", r)
	}
}

func TestParseRunsTheSchema(t *testing.T) {
	for _, tc := range []struct {
		path, key string
		value     any
		want      string
	}{
		{"gates", "result", "skipped", "/gates/0/result"}, {"release", "unknown", true, "unknown"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			r, err := Parse(changed(t, tc.path, tc.key, tc.value, false))
			wantError(t, err, tc.want)
			if r != nil {
				t.Fatal("invalid record released")
			}
		})
	}
}

func TestParseRejectsTrailingData(t *testing.T) {
	r, err := Parse(append(fixture(t), []byte("{}")...))
	if err == nil || r != nil {
		t.Fatalf("trailing data accepted: %v", err)
	}
}

func TestParseRejectsCrossReferenceViolations(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Record)
		want   string
	}{
		{"gate-digest", func(r *Record) { r.Gates[0].Digest = r.Artifacts[1].Digest }, "gate \"image-smoke\" artifact \"api-gateway\": digest"},
		{"unknown-gate-artifact", func(r *Record) { r.Gates[0].Artifact = "missing" }, "gate \"image-smoke\": unknown artifact"},
		{"duplicate-artifact", func(r *Record) { r.Artifacts = append(r.Artifacts, r.Artifacts[0]) }, "duplicate artifact"},
		{"duplicate-gate", func(r *Record) { r.Gates = append(r.Gates, r.Gates[0]) }, "duplicate gate"},
		{"duplicate-exception", func(r *Record) { r.Exceptions = append(r.Exceptions, r.Exceptions[0]) }, "duplicate exception"},
		{"unknown-exception-artifact", func(r *Record) { r.Exceptions[0].Artifacts = []string{"missing"} }, "exception \"binary-no-release-gate\": unknown artifact"},
		{"uncovered-artifact", func(r *Record) { r.Gates = []Gate{}; r.Exceptions = []Exception{} }, "has neither a gate nor an exception"},
		{"non-tag-release", func(r *Record) { r.Release.Ref = "refs/heads/main" }, "non-dry-run ref"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := parsed(t)
			tc.mutate(r)
			got, err := Parse(encoded(t, r))
			wantError(t, err, tc.want)
			if got != nil {
				t.Fatal("invalid record released")
			}
		})
	}
}

func TestParseAcceptsDryRunBranchRecord(t *testing.T) {
	r := parsed(t)
	r.Release.DryRun = true
	r.Release.Ref = "refs/heads/release-dry-run/x"
	if _, err := Parse(encoded(t, r)); err != nil {
		t.Fatal(err)
	}
}
