package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func baseEntry() EvalEntry {
	return EvalEntry{
		ID:               "entry-1",
		Input:            json.RawMessage(`{"tenant":"acme","topic":"support","agent_id":"agent-a","capabilities":["read"],"risk_tags":["pii"],"metadata":{"origin":"ticket-42"}}`),
		ExpectedDecision: SafetyDeny,
		RuleID:           "rule-pii-leak-01",
		Metadata:         map[string]string{"scenario": "denied-in-prod"},
		Source:           EvalEntrySourceAuditImport,
		SourceRef:        "audit-xyz",
		Notes:            "Reproduces the leak detected on 2026-03-12",
	}
}

func baseDataset() EvalDataset {
	return EvalDataset{
		Name:        "pii-leaks-q1",
		Version:     1,
		Tenant:      "acme",
		Description: "Q1 regression cases for PII leak rule",
		CreatedAt:   "2026-04-20T09:17:00Z",
		UpdatedAt:   "2026-04-20T09:17:00Z",
		CreatedBy:   "alice@example.com",
		Entries:     []EvalEntry{baseEntry()},
	}
}

func TestEvalDatasetValidateHappyPath(t *testing.T) {
	d := baseDataset()
	d.Normalize()
	if err := d.Validate(); err != nil {
		t.Fatalf("Validate: unexpected error: %v", err)
	}
}

func TestEvalDatasetNameValidation(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"simple", "basic", false},
		{"hyphen", "pii-leaks-q1", false},
		{"underscore", "audit_import_v1", false},
		{"alphanum", "abc123", false},
		{"minlen_3", "abc", false},
		{"too-short_2", "ab", true},
		{"too-long_65", "a" + strings.Repeat("b", 64), true},
		{"leading-dash", "-bad", true},
		{"leading-underscore", "_bad", true},
		{"uppercase", "BadCase", true},
		{"space", "bad name", true},
		{"dot", "bad.name", true},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := baseDataset()
			d.Name = tt.in
			d.Normalize()
			if tt.name == "uppercase" {
				// Normalize lowercases names; to actually test the uppercase
				// failure mode we re-assert post-normalize.
				d.Name = tt.in
			}
			err := d.Validate()
			if tt.wantErr && err == nil {
				t.Fatalf("expected validation error for %q", tt.in)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected validation error for %q: %v", tt.in, err)
			}
		})
	}
}

func TestEvalDatasetVersionMustBePositive(t *testing.T) {
	for _, v := range []int{-1, 0} {
		d := baseDataset()
		d.Version = v
		d.Normalize()
		err := d.Validate()
		if err == nil {
			t.Fatalf("expected error for version %d", v)
		}
		if !strings.Contains(err.Error(), "version must be >= 1") {
			t.Fatalf("unexpected error for version %d: %v", v, err)
		}
	}
}

func TestEvalDatasetRequiresTenant(t *testing.T) {
	d := baseDataset()
	d.Tenant = "   "
	d.Normalize()
	if err := d.Validate(); err == nil || !strings.Contains(err.Error(), "tenant is required") {
		t.Fatalf("expected tenant required error, got %v", err)
	}
}

func TestEvalDatasetRequiresEntries(t *testing.T) {
	d := baseDataset()
	d.Entries = nil
	d.Normalize()
	if err := d.Validate(); err == nil || !strings.Contains(err.Error(), "at least one entry") {
		t.Fatalf("expected missing-entries error, got %v", err)
	}
}

func TestEvalDatasetEntryCap(t *testing.T) {
	d := baseDataset()
	base := baseEntry()
	d.Entries = make([]EvalEntry, MaxEvalDatasetEntries+1)
	for i := range d.Entries {
		d.Entries[i] = EvalEntry{
			ID:               strings.Repeat("a", 1) + itoa(i),
			Input:            base.Input,
			ExpectedDecision: SafetyAllow,
		}
	}
	d.Normalize()
	if err := d.Validate(); err == nil || !strings.Contains(err.Error(), "cap") {
		t.Fatalf("expected entries cap error, got %v", err)
	}
}

func TestEvalDatasetRejectsDuplicateEntryIDs(t *testing.T) {
	d := baseDataset()
	dup := baseEntry()
	dup.Notes = "second"
	d.Entries = append(d.Entries, dup)
	d.Normalize()
	err := d.Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicate entry id") {
		t.Fatalf("expected duplicate-entry-id error, got %v", err)
	}
}

func TestEvalDatasetEntryValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*EvalEntry)
		errSub string
	}{
		{
			name:   "missing id",
			mutate: func(e *EvalEntry) { e.ID = "" },
			errSub: "entry id is required",
		},
		{
			name:   "missing input",
			mutate: func(e *EvalEntry) { e.Input = nil },
			errSub: "entry input is required",
		},
		{
			name:   "invalid json input",
			mutate: func(e *EvalEntry) { e.Input = json.RawMessage(`{broken`) },
			errSub: "must be valid JSON",
		},
		{
			name:   "unknown expected decision",
			mutate: func(e *EvalEntry) { e.ExpectedDecision = SafetyDecision("MAYBE") },
			errSub: "not a recognized SafetyDecision",
		},
		{
			name:   "unavailable rejected",
			mutate: func(e *EvalEntry) { e.ExpectedDecision = SafetyUnavailable },
			errSub: "not a recognized SafetyDecision",
		},
		{
			name:   "unknown source",
			mutate: func(e *EvalEntry) { e.Source = "guess" },
			errSub: "entry source",
		},
		{
			name:   "notes too long",
			mutate: func(e *EvalEntry) { e.Notes = strings.Repeat("x", MaxEvalEntryNotesBytes+1) },
			errSub: "entry notes exceeds",
		},
		{
			name: "metadata too many keys",
			mutate: func(e *EvalEntry) {
				e.Metadata = make(map[string]string, MaxEvalEntryMetadataKeys+1)
				for i := 0; i <= MaxEvalEntryMetadataKeys; i++ {
					e.Metadata["k"+itoa(i)] = "v"
				}
			},
			errSub: "entry metadata has",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := baseDataset()
			d.Entries[0] = baseEntry()
			tt.mutate(&d.Entries[0])
			d.Normalize()
			err := d.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q", tt.errSub)
			}
			if !strings.Contains(err.Error(), tt.errSub) {
				t.Fatalf("expected error containing %q, got %v", tt.errSub, err)
			}
		})
	}
}

func TestEvalDatasetAcceptsAllExpectedDecisions(t *testing.T) {
	decisions := []SafetyDecision{
		SafetyAllow,
		SafetyDeny,
		SafetyRequireApproval,
		SafetyThrottle,
		SafetyAllowWithConstraints,
	}
	for _, decision := range decisions {
		t.Run(string(decision), func(t *testing.T) {
			d := baseDataset()
			d.Entries[0].ExpectedDecision = decision
			d.Normalize()
			if err := d.Validate(); err != nil {
				t.Fatalf("unexpected error for %s: %v", decision, err)
			}
		})
	}
}

func TestEvalDatasetContentHashIsDeterministic(t *testing.T) {
	d1 := baseDataset()
	d2 := baseDataset()
	// Re-order metadata by supplying a map with multiple keys; Go's map
	// iteration is randomized so without canonicalization this would be
	// flaky.
	d1.Entries[0].Metadata = map[string]string{"a": "1", "b": "2", "c": "3"}
	d2.Entries[0].Metadata = map[string]string{"c": "3", "b": "2", "a": "1"}

	h1, err := d1.ComputeContentHash()
	if err != nil {
		t.Fatalf("ComputeContentHash d1: %v", err)
	}
	h2, err := d2.ComputeContentHash()
	if err != nil {
		t.Fatalf("ComputeContentHash d2: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("content hash not canonical: %s vs %s", h1, h2)
	}
	if len(h1) != 64 {
		t.Fatalf("expected 64-hex sha256, got %d chars: %s", len(h1), h1)
	}
}

func TestEvalDatasetContentHashChangesWithContent(t *testing.T) {
	d := baseDataset()
	h1, err := d.ComputeContentHash()
	if err != nil {
		t.Fatalf("hash 1: %v", err)
	}

	modified := baseDataset()
	modified.Entries[0].Notes = "different"
	h2, err := modified.ComputeContentHash()
	if err != nil {
		t.Fatalf("hash 2: %v", err)
	}

	if h1 == h2 {
		t.Fatal("expected content hash to change when an entry's notes change")
	}
}

func TestEvalDatasetCreatedAtMilli(t *testing.T) {
	d := baseDataset()
	d.CreatedAt = "2026-04-20T09:17:00.000Z"
	ms, err := d.CreatedAtMilli()
	if err != nil {
		t.Fatalf("CreatedAtMilli: %v", err)
	}
	// 2026-04-20T09:17:00Z UTC
	want := int64(1776676620000)
	if ms != want {
		t.Fatalf("expected %d, got %d", want, ms)
	}

	d.CreatedAt = "not-a-date"
	if _, err := d.CreatedAtMilli(); err == nil {
		t.Fatal("expected error for malformed timestamp")
	}
}

func TestEvalDatasetNormalizeLowercasesName(t *testing.T) {
	d := baseDataset()
	d.Name = "PII-Leaks-Q1"
	d.Normalize()
	if d.Name != "pii-leaks-q1" {
		t.Fatalf("expected lowercased name, got %q", d.Name)
	}
}

// itoa is a tiny allocation-free int-to-string for table driven tests.
// It keeps the test file dependency-free (no strconv import just for
// this).
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
