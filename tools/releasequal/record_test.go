package releasequal

import (
	"bytes"
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

func fragments(t *testing.T) (*Record, []Fragment) {
	t.Helper()
	r := parsed(t)
	image, binary := r.Exceptions[1], r.Exceptions[1]
	image.Artifacts = []string{"api-gateway", "api-gateway"}
	binary.Artifacts = []string{"cordum-hook-linux-amd64"}
	return r, []Fragment{
		{Artifacts: r.Artifacts[:1], Gates: r.Gates, Exceptions: []Exception{image}},
		{Artifacts: r.Artifacts[1:], Exceptions: []Exception{binary, r.Exceptions[0]}},
	}
}

func TestAssembleOneRecordForImagesAndBinaries(t *testing.T) {
	header, parts := fragments(t)
	before := encoded(t, parts)
	r, err := Assemble(header.Release, header.Source, parts...)
	if err != nil {
		t.Fatal(err)
	}
	if r.Source != header.Source || r.SchemaVersion != SchemaVersion || len(r.Artifacts) != 2 || len(r.Gates) != 2 || len(r.Exceptions) != 2 {
		t.Fatalf("unexpected assembled record: %+v", r)
	}
	if r.Artifacts[0] != header.Artifacts[1] || r.Artifacts[1] != header.Artifacts[0] || r.Gates[0].ID != "image-smoke" || r.Gates[1].ID != "trivy-os-linux-amd64" {
		t.Fatal("wrong digests or artifact/gate order")
	}
	if r.Exceptions[0].ID != "binary-no-release-gate" || r.Exceptions[1].ID != "production-gate-not-digest-bound" || strings.Join(r.Exceptions[1].Artifacts, ",") != "api-gateway,cordum-hook-linux-amd64" {
		t.Fatalf("wrong merged exceptions: %+v", r.Exceptions)
	}
	if _, err := Parse(encoded(t, r)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, encoded(t, parts)) {
		t.Fatal("Assemble mutated input fragments")
	}
	reversed, err := Assemble(header.Release, header.Source, parts[1], parts[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded(t, r), encoded(t, reversed)) {
		t.Fatal("fragment order changed assembled JSON")
	}
}

func TestAssembleRejectsConflicts(t *testing.T) {
	cases := []struct {
		name   string
		mutate func([]Fragment) []Fragment
		want   string
	}{
		{"duplicate-artifact", func(f []Fragment) []Fragment { return append(f, Fragment{Artifacts: f[0].Artifacts}) }, "duplicate artifact"},
		{"exception-reason", func(f []Fragment) []Fragment { f[1].Exceptions[0].Reason = "different"; return f }, "conflicting reasons"},
		{"unknown-gate-artifact", func(f []Fragment) []Fragment { f[0].Gates[0].Artifact = "missing"; return f }, "unknown artifact"},
		{"zero-fragments", func([]Fragment) []Fragment { return nil }, "/artifacts"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, f := fragments(t)
			r, err := Assemble(h.Release, h.Source, tc.mutate(f)...)
			wantError(t, err, tc.want)
			if r != nil {
				t.Fatal("invalid assembled record released")
			}
		})
	}
}

func TestParseFragmentRejectsUnknownFields(t *testing.T) {
	for _, input := range []string{`{"unknown":true}`, `{"artifacts":[{"unknown":true}]}`, `{} {}`, `{}x`} {
		if _, err := ParseFragment([]byte(input)); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	h, f := fragments(t)
	part, err := ParseFragment(encoded(t, f[0]))
	if err != nil || !bytes.Equal(encoded(t, part), encoded(t, f[0])) {
		t.Fatalf("valid fragment changed: %v", err)
	}
	part, err = ParseFragment([]byte(`{"artifacts":[{"kind":"chart"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if r, err := Assemble(h.Release, h.Source, part); err == nil || r != nil {
		t.Fatal("invalid item escaped assembly")
	}
}

func TestCheck(t *testing.T) {
	cases := []struct{ name, artifact, digest, result, want string }{
		{"qualified", "api-gateway", "sha256:" + strings.Repeat("a", 64), ResultPass, ""},
		{"exception-only", "cordum-hook-linux-amd64", "sha256:" + strings.Repeat("b", 64), ResultPass, ""},
		{"altered-digest", "api-gateway", "sha256:b" + strings.Repeat("a", 63), ResultPass, "differs"},
		{"uppercase-digest", "api-gateway", "sha256:" + strings.Repeat("A", 64), ResultPass, "differs"},
		{"unknown-artifact", "missing", "sha256:" + strings.Repeat("a", 64), ResultPass, "not in the qualification record"},
		{"failing-gate-with-exception", "api-gateway", "sha256:" + strings.Repeat("a", 64), ResultFail, `result is "fail"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := parsed(t)
			r.Gates[1].Result = tc.result
			r, err := Parse(encoded(t, r))
			if err != nil {
				t.Fatal(err)
			}
			err = r.Check(tc.artifact, tc.digest)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				wantError(t, err, tc.want)
			}
		})
	}
}
