package policy

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordum/cordum/core/infra/config"
)

// TestLegacyInputPolicyRuleJSON locks in the byte-level JSON output of
// `core/infra/config.InputPolicyRule` so any inadvertent shape change to
// the legacy struct fails this test loudly. Backwards-compat is DoD #3 of
// task-3bf37e32 (Policy Studio Backend 1).
//
// Set UPDATE_GOLDENS=1 to regenerate the golden file when the legacy shape
// is intentionally changed.
func TestLegacyInputPolicyRuleJSON(t *testing.T) {
	enabled := true
	fixture := config.InputPolicyRule{
		ID:       "legacy-input-secret-block",
		Tier:     config.PolicyTierGlobal,
		Selector: config.PolicySelector{WorkflowID: "wf-claims"},
		Enabled:  &enabled,
		Severity: "critical",
		Desc:     "Block secret leaks in inputs",
		Match: config.InputPolicyMatch{
			Tenants:         []string{"acme"},
			Topics:          []string{"job.acme.*"},
			Capabilities:    []string{"llm.request"},
			RiskTags:        []string{"untrusted_input"},
			Scanners:        []string{"secret_leak"},
			ContentPatterns: []string{`(?i)api[_-]?key`},
			Keywords:        []string{"secret", "token"},
			ContentTypes:    []string{"application/json"},
			Detectors:       []string{"secret_leak"},
			InputSizeGt:     1024,
			MaxInputBytes:   1048576,
		},
		Decision: "deny",
		Reason:   "secret_leak_detected",
	}

	got, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent err = %v", err)
	}
	got = append(got, '\n')

	goldenPath := filepath.Join("testdata", "legacy_input_rule.json")
	assertGoldenEqual(t, goldenPath, got)
}

// TestLegacyOutputPolicyRuleJSON does the same lock-in for the legacy
// `core/infra/config.OutputPolicyRule` shape.
func TestLegacyOutputPolicyRuleJSON(t *testing.T) {
	enabled := true
	hasError := false
	fixture := config.OutputPolicyRule{
		ID:       "legacy-output-pii-redact",
		Enabled:  &enabled,
		Severity: "high",
		Desc:     "Redact PII in outputs",
		Match: config.OutputPolicyMatch{
			Tenants:         []string{"acme"},
			Topics:          []string{"job.acme.*"},
			Capabilities:    []string{"llm.request"},
			RiskTags:        []string{"contains_pii"},
			Scanners:        []string{"pii"},
			ContentPatterns: []string{`(?i)\bSSN\b`},
			Keywords:        []string{"social", "ssn"},
			ContentTypes:    []string{"application/json"},
			Detectors:       []string{"pii"},
			OutputSizeGt:    0,
			MaxOutputBytes:  4194304,
			HasError:        &hasError,
		},
		Decision: "redact",
		Reason:   "pii_in_output",
	}

	got, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent err = %v", err)
	}
	got = append(got, '\n')

	goldenPath := filepath.Join("testdata", "legacy_output_rule.json")
	assertGoldenEqual(t, goldenPath, got)
}

// assertGoldenEqual reads the golden file and compares bytes against got.
// On UPDATE_GOLDENS=1 it overwrites the golden instead of comparing —
// always a deliberate operator action, never automatic.
func assertGoldenEqual(t *testing.T, path string, got []byte) {
	t.Helper()
	if os.Getenv("UPDATE_GOLDENS") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) err = %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("WriteFile(%s) err = %v", path, err)
		}
		t.Logf("UPDATE_GOLDENS=1 — wrote %d bytes to %s", len(got), path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) err = %v — generate via `UPDATE_GOLDENS=1 go test ./core/policy/...`", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("golden drift in %s\n--- want\n%s\n--- got\n%s", path, want, got)
	}
}
