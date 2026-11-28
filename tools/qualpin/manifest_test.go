package qualpin

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func validJSON(t *testing.T) []byte {
	t.Helper()
	record, err := os.ReadFile("../releasequal/testdata/valid-record.json")
	if err != nil {
		t.Fatal(err)
	}
	return []byte(`{"schema_version":1,"qualification":{"id":"q1","combination":"linux-kind"},
	"record":` + string(record) + `,"policy":{"bundle":"deploy","version":"v1","authority_version":"unsupported"},
	"executor":{"kind":"broker","version":"v1","platform":{"os":"linux","arch":"amd64","version":"6.8"}},
	"deployment":{"profile":"kind","config_digest":"sha256:` + strings.Repeat("a", 64) + `"},
	"corpus":{"name":"deploy","version":"v1","digest":"sha256:` + strings.Repeat("b", 64) + `"}}`)
}

// An empty value removes the key; all mutations preserve well-formed JSON.
func change(t *testing.T, data []byte, path string, value json.RawMessage) []byte {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	key, rest, nested := strings.Cut(path, ".")
	if nested {
		fields[key] = change(t, fields[key], rest, value)
	} else if len(value) == 0 {
		delete(fields, key)
	} else {
		fields[key] = value
	}
	result, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestParseValid(t *testing.T) {
	m, err := Parse(validJSON(t))
	if err != nil || m == nil || m.Qualification.ID != "q1" {
		t.Fatalf("manifest=%+v error=%v", m, err)
	}
}

func TestMissingLink(t *testing.T) {
	for _, path := range []string{"record.source", "record.artifacts", "policy", "executor", "deployment", "corpus", "record.gates", "record.exceptions"} {
		t.Run(path, func(t *testing.T) {
			m, err := Parse(change(t, validJSON(t), path, nil))
			if err == nil || m != nil || !strings.Contains(err.Error(), strings.TrimPrefix(path, "record.")) {
				t.Fatalf("released manifest=%+v error=%v", m, err)
			}
		})
	}
}

func TestSchema(t *testing.T) {
	for _, path := range []string{
		"qualification.id", "qualification.combination", "policy.bundle", "policy.version", "policy.authority_version",
		"executor.kind", "executor.version", "executor.platform.os", "executor.platform.arch", "executor.platform.version",
		"deployment.profile", "corpus.name", "corpus.version",
	} {
		for _, value := range []string{"", `" \t\n"`, `null`} {
			t.Run(path+"/"+value, func(t *testing.T) {
				if m, err := Parse(change(t, validJSON(t), path, json.RawMessage(value))); err == nil || m != nil {
					t.Fatalf("released manifest=%+v error=%v", m, err)
				}
			})
		}
	}
	for _, tc := range []struct{ path, value string }{
		{"schema_version", "2"}, {"qualification", "null"}, {"executor.platform", "null"},
		{"extra", "1"}, {"qualification.extra", "1"}, {"policy.extra", "1"}, {"executor.extra", "1"},
		{"executor.platform.extra", "1"}, {"deployment.extra", "1"}, {"corpus.extra", "1"},
		{"deployment.config_digest", `"sha256:abc"`}, {"corpus.digest", `"sha256:abc"`},
		{"record.source.sha", `"bad"`}, {"record.gates", `[{"id":"test","artifact":"absent","digest":"sha256:` + strings.Repeat("a", 64) + `","result":"pass","log":"test"}]`},
	} {
		t.Run(tc.path+"/invalid", func(t *testing.T) {
			if m, err := Parse(change(t, validJSON(t), tc.path, json.RawMessage(tc.value))); err == nil || m != nil {
				t.Fatalf("released manifest=%+v error=%v", m, err)
			}
		})
	}
	if m, err := Parse(append(validJSON(t), []byte(` {}`)...)); err == nil || m != nil {
		t.Fatalf("trailing JSON released manifest=%+v error=%v", m, err)
	}
}

func TestVerify(t *testing.T) {
	m, err := Parse(validJSON(t))
	if err != nil {
		t.Fatal(err)
	}
	a := m.Record.Artifacts[0]
	for _, digest := range []string{a.Digest, "sha256:" + strings.Repeat("c", 64), ""} {
		t.Run(digest, func(t *testing.T) {
			running := map[string]string{"extra": "ignored"}
			var want []Mismatch
			if digest != "" {
				running[a.Name] = digest
			}
			if digest != a.Digest {
				want = []Mismatch{{Name: a.Name, Expected: a.Digest, Running: digest}}
			}
			if got := Verify(*m, running); !reflect.DeepEqual(got, want) {
				t.Fatalf("got %+v want %+v", got, want)
			}
		})
	}
}
