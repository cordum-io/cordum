package contract

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

const dataStorage = `storage: {kind: file, location: local, writers: [{path: pkg/data.go, symbol: Store.Write}]}`
const dataContent = `content: {description: input, personal_data: possible}`
const dataRetention = `retention: {settings: [{name: KEEP_TTL, path: pkg/data.go}], default: 24h}`
const dataDeletion = `deletion: {paths: [{path: pkg/data.go, symbol: Remove}]}`
const dataHold = `legal_hold: {applies: false, reason: No hold in this store}`
const dataKeys = `redis_keys: [{pattern: 'data:{id}', path: pkg/data.go}, {pattern: 'formatted:{id}', path: pkg/data.go}]`

func dataDocument() string {
	// Independent of requiredDataClasses: removing a validator class must not
	// silently remove the positive control or its expected coverage.
	classes := []string{"raw-local-inputs", "redacted-events", "evidence-audit", "hashes-commitments", "artifacts", "telemetry", "backups", "exports"}
	doc := "schema_version: 1\nreviewed_at: 0123456789abcdef0123456789abcdef01234567\nclasses:\n"
	for _, c := range classes {
		doc += fmt.Sprintf("  - {id: %s, title: %s}\n", c, c)
	}
	doc += "endpoint_only:\n  - id: DI-E01\n    class: raw-local-inputs\n"
	fields := []string{dataStorage, dataContent, dataRetention, dataDeletion, dataHold}
	for _, f := range fields {
		doc += "    " + f + "\n"
	}
	doc += "control_plane:\n"
	for i, c := range classes[1:] {
		doc += fmt.Sprintf("  - id: DI-C%02d\n    class: %s\n", i+1, c)
		for j, f := range fields {
			if j == 0 {
				f = `storage: {kind: redis_key, location: Redis, ` + dataKeys + `, streams: [{name: EVENTS, path: pkg/data.go}]}`
			}
			doc += "    " + f + "\n"
		}
	}
	return doc
}

func dataFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "pkg/data.go"), "package pkg\nimport \"fmt\"\nconst TTL = \"KEEP_TTL\"\n"+
		"type Store struct{}\nfunc (s *Store) Write() {}\nfunc Remove() {}\n"+
		"var key = \"data:\"\nvar stream = `EVENTS`\nvar formatted = fmt.Sprintf(\"formatted:%s\", \"id\")\n// COMMENT_ONLY\n")
	write(t, filepath.Join(root, "pkg/data_test.go"), "package pkg\nconst TTL = \"KEEP_TTL\"\nfunc Remove() {}\n")
	write(t, filepath.Join(root, "pkg/data.txt"), "KEEP_TTL")
	write(t, filepath.Join(root, "pkg/broken.go"), "package [")
	return root
}

func loadData(t *testing.T, text string) (*DataInventory, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "inventory.yaml")
	write(t, p, text)
	return LoadDataInventory(p)
}

func TestDataInventoryValidatorAcceptsValid(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		doc, err := loadData(t, strings.ReplaceAll(dataDocument(), "\n", newline))
		if err != nil {
			t.Fatal(err)
		}
		if got := doc.Validate(dataFixture(t)); len(got) != 0 {
			t.Fatalf("positive control: %+v", got)
		}
	}
}

type dataReject struct{ name, rule, id, old, replacement string }

func dataFieldRejects() []dataReject {
	return []dataReject{
		{"missing_storage", "entry-fields", "DI-E01", dataStorage, ""},
		{"missing_content", "entry-fields", "DI-E01", dataContent, ""},
		{"missing_retention", "retention", "DI-E01", dataRetention, ""},
		{"missing_deletion", "deletion", "DI-E01", dataDeletion, ""},
		{"missing_legal_hold", "legal-hold", "DI-E01", dataHold, ""},
		{"retention_both", "retention", "DI-E01", "default: 24h", "default: 24h, none: No automatic retention setting"},
		{"retention_neither", "retention", "DI-E01", dataRetention, "retention: {}"},
		{"retention_short_reason", "retention", "DI-E01", dataRetention, "retention: {none: absent}"},
		{"deletion_both", "deletion", "DI-E01", dataDeletion, strings.TrimSuffix(dataDeletion, "}") + ", none: No automatic deletion path}"},
		{"deletion_neither", "deletion", "DI-E01", dataDeletion, "deletion: {}"},
		{"deletion_short_reason", "deletion", "DI-E01", dataDeletion, "deletion: {none: absent}"},
		{"hold_applies_missing", "legal-hold", "DI-E01", "applies: false, ", ""},
		{"hold_without_refs", "legal-hold", "DI-E01", "applies: false", "applies: true"},
		{"hold_reason_missing", "legal-hold", "DI-E01", "reason: No hold in this store", "reason: ''"},
		{"location_missing", "entry-fields", "DI-E01", "location: local", "location: ' '"},
		{"description_missing", "entry-fields", "DI-E01", "description: input", "description: ''"},
		{"personal_data_invalid", "entry-fields", "DI-E01", "personal_data: possible", "personal_data: maybe"},
		{"id_format", "entry-fields", "DI-E1", "DI-E01", "DI-E1"},
		{"duplicate_id", "entry-fields", "DI-C01", "DI-C02", "DI-C01"},
		{"unknown_class", "entry-fields", "DI-E01", "class: raw-local-inputs", "class: unknown"},
		{"storage_kind", "entry-fields", "DI-C01", "kind: redis_key", "kind: unknown"},
	}
}

func dataReferenceRejects() []dataReject {
	return []dataReject{
		{"setting_absent", "reference", "DI-E01", "name: KEEP_TTL", "name: MISSING_TTL"},
		{"setting_test_file", "reference", "DI-E01", "name: KEEP_TTL, path: pkg/data.go", "name: KEEP_TTL, path: pkg/data_test.go"},
		{"setting_non_go", "reference", "DI-E01", "name: KEEP_TTL, path: pkg/data.go", "name: KEEP_TTL, path: pkg/data.txt"},
		{"symbol_absent", "reference", "DI-E01", "symbol: Remove", "symbol: Missing"},
		{"writer_absent", "reference", "DI-E01", "symbol: Store.Write", "symbol: Missing"},
		{"symbol_test_file", "reference", "DI-E01", "path: pkg/data.go, symbol: Remove", "path: pkg/data_test.go, symbol: Remove"},
		{"path_escape", "reference", "DI-E01", "pkg/data.go", "../data.go"},
		{"key_absent", "reference", "DI-C01", "data:{id}", "missing:{id}"},
		{"key_empty_prefix", "reference", "DI-C01", "data:{id}", "{id}"},
		{"key_no_colon", "reference", "DI-C01", "data:{id}", "data{id}"},
		{"stream_absent", "reference", "DI-C01", "name: EVENTS", "name: MISSING"},
		{"endpoint_key", "endpoint", "DI-E01", "location: local", "location: local, " + dataKeys},
		{"endpoint_stream", "endpoint", "DI-E01", "location: local", "location: local, streams: [{name: EVENTS, path: pkg/data.go}]"},
		{"endpoint_kind", "endpoint", "DI-E01", "kind: file", "kind: external"},
		{"class_missing", "classes", "", "  - {id: artifacts, title: artifacts}\n", ""},
		{"class_without_entry", "classes", "", "classes:\n", "classes:\n  - {id: extra, title: Extra}\n"},
		{"class_title_missing", "classes", "", "title: artifacts", "title: ''"},
		{"class_duplicate", "classes", "", "classes:\n", "classes:\n  - {id: artifacts, title: Artifacts}\n"},
		{"reviewed_sha", "schema", "", "0123456789abcdef0123456789abcdef01234567", "bad"},
		{"hold_ref_absent", "reference", "DI-E01", dataHold, "legal_hold: {applies: true, reason: Held, refs: [{path: pkg/data.go, symbol: Missing}]}"},
	}
}

func TestDataInventoryValidatorRejects(t *testing.T) {
	root := dataFixture(t)
	for _, tc := range append(dataFieldRejects(), dataReferenceRejects()...) {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := loadData(t, mutate(t, dataDocument(), tc.old, tc.replacement))
			if err != nil {
				t.Fatal(err)
			}
			got := doc.Validate(root)
			for _, issue := range got {
				if issue.Rule == tc.rule && issue.ClaimID == tc.id && issue.Detail != "" {
					return
				}
			}
			t.Fatalf("want %s issue for %q; got %+v", tc.rule, tc.id, got)
		})
	}
}

func TestDataInventoryLoadRejects(t *testing.T) {
	for name, text := range map[string]string{
		"malformed": "classes: [", "empty": "", "null": "null", "unknown": dataDocument() + "unexpected: true\n",
		"nested_unknown":  mutate(t, dataDocument(), "location: local", "locaton: local"),
		"version":         mutate(t, dataDocument(), "schema_version: 1", "schema_version: 2"),
		"second_document": dataDocument() + "---\nschema_version: 1\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadData(t, text); err == nil {
				t.Fatal("invalid document loaded without error")
			}
		})
	}
	if _, err := LoadDataInventory(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing file loaded")
	}
}

func TestDataInventoryLiteralHelper(t *testing.T) {
	root := dataFixture(t)
	for _, tc := range []struct {
		path, value string
		want        bool
	}{
		{"pkg/data.go", "KEEP_TTL", true}, {"pkg/data.go", "EVENTS", true},
		{"pkg/data.go", "KEEP", false}, {"pkg/data.go", "COMMENT_ONLY", false},
		{"pkg/data_test.go", "KEEP_TTL", false}, {"pkg/data.txt", "KEEP_TTL", false},
		{"../data.go", "KEEP_TTL", false}, {"/data.go", "KEEP_TTL", false},
		{"pkg/broken.go", "KEEP_TTL", false}, {"pkg/missing.go", "KEEP_TTL", false},
	} {
		if got := hasStringLiteral(root, tc.path, tc.value); got != tc.want {
			t.Errorf("hasStringLiteral(%q, %q) = %v, want %v", tc.path, tc.value, got, tc.want)
		}
	}
}

func TestDataInventoryExplicitAbsence(t *testing.T) {
	doc, err := loadData(t, mutate(t, dataDocument(),
		dataRetention, "retention: {none: No automatic retention setting exists}",
		dataDeletion, "deletion: {none: No automatic deletion path exists}",
		dataHold, "legal_hold: {applies: true, reason: Consulted here, refs: [{path: pkg/data.go, symbol: Remove}]}",
		"kind: file", "kind: process_memory"))
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.Validate(dataFixture(t)); len(got) != 0 {
		t.Fatalf("explicit absence/hold reference: %+v", got)
	}
	doc.SchemaVersion = 2
	if got := doc.Validate(dataFixture(t)); len(got) != 1 || got[0].Rule != "schema" {
		t.Fatalf("mutated version accepted: %+v", got)
	}
	if got := (*DataInventory)(nil).Validate(dataFixture(t)); len(got) != 1 || got[0].Rule != "schema" {
		t.Fatalf("nil inventory accepted: %+v", got)
	}
}

func TestDataInventoryFile(t *testing.T) {
	root, err := repoRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := LoadDataInventory(filepath.Join(root, "docs/contract/data-inventory.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range doc.Validate(root) {
		t.Errorf("%s %s: %s", issue.Rule, issue.ClaimID, issue.Detail)
	}
	counts := map[string]int{}
	for _, entries := range [][]DataEntry{doc.EndpointOnly, doc.ControlPlane} {
		for _, e := range entries {
			counts[e.Class]++
		}
	}
	t.Logf("endpoint_only=%d control_plane=%d classes=%v", len(doc.EndpointOnly), len(doc.ControlPlane), counts)
}
