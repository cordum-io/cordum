package policy

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

// TestRule_PerTypeRoundTrip exercises one realistic fixture per RuleType.
// Match/Decide are json.RawMessage carriers that MUST be preserved
// bit-for-bit through marshal/unmarshal so the orval-generated TS layer
// and the proto google.protobuf.Struct carriers can round-trip lossless.
func TestRule_PerTypeRoundTrip(t *testing.T) {
	now := time.Date(2026, 5, 9, 8, 30, 0, 0, time.UTC)

	cases := []struct {
		name   string
		rule   Rule
		wantID string
	}{
		{
			name: "input",
			rule: Rule{
				ID:      "rule-input-secrets",
				Name:    "Block secret leaks in input",
				Type:    RuleTypeInput,
				Scope:   RuleScope{Kind: RuleScopeTenant, Value: "tenant-acme"},
				Status:  RuleStatusPublished,
				Version: "v1",
				Audit:   AuditMetadata{CreatedAt: now, CreatedBy: "yaron@cordum.io"},
				Match: json.RawMessage(`{
                  "tenants": ["acme"],
                  "topics": ["job.acme.*"],
                  "scanners": ["secret_leak"],
                  "input_size_gt": 1024
                }`),
				Decide: json.RawMessage(`{"decision":"deny","reason":"secret_leak","severity":"critical"}`),
			},
			wantID: "rule-input-secrets",
		},
		{
			name: "output",
			rule: Rule{
				ID:      "rule-output-pii-redact",
				Name:    "Redact PII in outputs",
				Type:    RuleTypeOutput,
				Scope:   RuleScope{Kind: RuleScopeGlobal},
				Status:  RuleStatusPublished,
				Version: "v3",
				Audit:   AuditMetadata{CreatedAt: now, CreatedBy: "policyteam@cordum.io"},
				Match: json.RawMessage(`{
                  "detectors": ["pii"],
                  "output_size_gt": 0,
                  "has_error": false
                }`),
				Decide: json.RawMessage(`{"decision":"redact","reason":"pii_in_output","severity":"high"}`),
			},
			wantID: "rule-output-pii-redact",
		},
		{
			name: "velocity",
			rule: Rule{
				ID:          "rule-velocity-llm-throttle",
				Name:        "LLM request rate limit per session",
				Type:        RuleTypeVelocity,
				Scope:       RuleScope{Kind: RuleScopeWorkflow, Value: "wf-claims-triage"},
				Status:      RuleStatusPublished,
				Version:     "v2",
				Audit:       AuditMetadata{CreatedAt: now, CreatedBy: "ops@cordum.io"},
				Description: "throttle to 60 req/min per session",
				Match: json.RawMessage(`{
                  "tenants": ["acme"],
                  "capabilities": ["llm.request"],
                  "labels": {"actor_type": "service"}
                }`),
				Decide: json.RawMessage(`{
                  "decision": "throttle",
                  "reason": "rate_limit_exceeded",
                  "velocity": {"max_requests": 60, "window_seconds": 60, "key": "labels.session_id"},
                  "constraints": {"budgets": {"max_runtime_ms": 30000}}
                }`),
			},
			wantID: "rule-velocity-llm-throttle",
		},
		{
			name: "edge",
			rule: Rule{
				ID:      "rule-edge-fs-write-deny",
				Name:    "Block writes to /etc",
				Type:    RuleTypeEdge,
				Scope:   RuleScope{Kind: RuleScopeEdgeFleet, Value: "fleet-prod"},
				Status:  RuleStatusPublished,
				Version: "v1",
				Audit:   AuditMetadata{CreatedAt: now, CreatedBy: "secops@cordum.io"},
				Match: json.RawMessage(`{
                  "capability": "file.write",
                  "labels": {"path_prefix": "/etc"},
                  "complete": true
                }`),
				Decide: json.RawMessage(`{"decision":"DENY","reason":"system_path_protected"}`),
			},
			wantID: "rule-edge-fs-write-deny",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			origMatch := append([]byte(nil), tc.rule.Match...)
			origDecide := append([]byte(nil), tc.rule.Decide...)

			raw, err := json.Marshal(tc.rule)
			if err != nil {
				t.Fatalf("Marshal err = %v", err)
			}
			var back Rule
			if err := json.Unmarshal(raw, &back); err != nil {
				t.Fatalf("Unmarshal err = %v", err)
			}
			if back.ID != tc.wantID {
				t.Fatalf("ID drift: %s -> %s", tc.wantID, back.ID)
			}
			if back.Type != tc.rule.Type {
				t.Fatalf("Type drift: %v -> %v", tc.rule.Type, back.Type)
			}
			if back.Scope != tc.rule.Scope {
				t.Fatalf("Scope drift: %+v -> %+v", tc.rule.Scope, back.Scope)
			}
			if back.Status != tc.rule.Status {
				t.Fatalf("Status drift: %v -> %v", tc.rule.Status, back.Status)
			}
			if !back.Audit.CreatedAt.Equal(tc.rule.Audit.CreatedAt) || back.Audit.CreatedBy != tc.rule.Audit.CreatedBy {
				t.Fatalf("Audit drift: %+v -> %+v", tc.rule.Audit, back.Audit)
			}

			// Match/Decide json.RawMessage round-trip: the bytes must be
			// semantically equal (json.Compact normalises whitespace which
			// json.Marshal applies on serialise — bit-for-bit equality after
			// re-canonicalisation is the contract we hold).
			origCompact := mustCompact(t, origMatch)
			backCompact := mustCompact(t, back.Match)
			if !bytes.Equal(origCompact, backCompact) {
				t.Fatalf("Match drift: %s vs %s", origCompact, backCompact)
			}
			origCompact = mustCompact(t, origDecide)
			backCompact = mustCompact(t, back.Decide)
			if !bytes.Equal(origCompact, backCompact) {
				t.Fatalf("Decide drift: %s vs %s", origCompact, backCompact)
			}
		})
	}
}

// TestDecision_RoundTrip exercises a Decision with multi-step Trace, all
// optional fields populated, and the new wire-format DecisionType values
// (allow_with_constraints, quarantine, redact) per amendment #2.
func TestDecision_RoundTrip(t *testing.T) {
	now := time.Date(2026, 5, 9, 8, 30, 0, 0, time.UTC)

	cases := []struct {
		name string
		dec  Decision
	}{
		{
			name: "job_allow_with_constraints",
			dec: Decision{
				Source:        DecisionSourceJob,
				RuleID:        "rule-velocity-llm-throttle",
				BundleID:      "bundle-acme-default",
				BundleVersion: "v12",
				Type:          DecisionAllowWithConstraints,
				Trace: []TraceStep{{
					RuleID:       "rule-velocity-llm-throttle",
					BundleID:     "bundle-acme-default",
					DecisionType: DecisionAllowWithConstraints,
					Reason:       "within_throttle_budget",
					Timestamp:    now,
					Constraints:  json.RawMessage(`{"budgets":{"max_runtime_ms":30000}}`),
				}},
				InputRef:  "blob://acme/input/r1",
				OutputRef: "blob://acme/output/r1",
				AuditHash: "0x" + "ab" + "cd" + "ef",
				Timestamp: now,
			},
		},
		{
			name: "edge_deny",
			dec: Decision{
				Source:    DecisionSourceEdge,
				RuleID:    "rule-edge-fs-write-deny",
				Type:      DecisionDeny,
				Trace:     nil,
				Timestamp: now,
			},
		},
		{
			name: "output_quarantine",
			dec: Decision{
				Source:    DecisionSourceJob,
				RuleID:    "rule-output-pii-quarantine",
				Type:      DecisionQuarantine,
				Timestamp: now,
			},
		},
		{
			name: "output_redact",
			dec: Decision{
				Source:    DecisionSourceJob,
				RuleID:    "rule-output-pii-redact",
				Type:      DecisionRedact,
				Timestamp: now,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.dec)
			if err != nil {
				t.Fatalf("Marshal err = %v", err)
			}
			var back Decision
			if err := json.Unmarshal(raw, &back); err != nil {
				t.Fatalf("Unmarshal err = %v", err)
			}
			if back.Source != tc.dec.Source || back.Type != tc.dec.Type || back.RuleID != tc.dec.RuleID {
				t.Fatalf("Decision drift: %+v vs %+v", tc.dec, back)
			}
			if !back.Timestamp.Equal(tc.dec.Timestamp) {
				t.Fatalf("Timestamp drift: %v vs %v", tc.dec.Timestamp, back.Timestamp)
			}
			if len(back.Trace) != len(tc.dec.Trace) {
				t.Fatalf("Trace length drift: %d vs %d", len(tc.dec.Trace), len(back.Trace))
			}
			for i := range tc.dec.Trace {
				if back.Trace[i].RuleID != tc.dec.Trace[i].RuleID ||
					back.Trace[i].DecisionType != tc.dec.Trace[i].DecisionType {
					t.Fatalf("TraceStep[%d] drift: %+v vs %+v", i, tc.dec.Trace[i], back.Trace[i])
				}
			}
		})
	}
}

// TestBundle_RoundTrip covers Bundle with EdgeMode metadata + a versioned
// rule snapshot. Verifies BundleMetadata.EdgeMode survives marshal cycles.
func TestBundle_RoundTrip(t *testing.T) {
	now := time.Date(2026, 5, 9, 8, 30, 0, 0, time.UTC)

	bundle := Bundle{
		ID:           "bundle-edge-prod",
		Name:         "Production edge bundle",
		RuleIDs:      []string{"rule-edge-fs-write-deny", "rule-edge-shell-throttle"},
		ScopeBinding: RuleScope{Kind: RuleScopeEdgeFleet, Value: "fleet-prod"},
		Versions: []BundleVersion{{
			Version:    "v3",
			DeployedAt: now,
			AuditHash:  "0xdeadbeef",
			RuleSnapshot: []Rule{{
				ID:      "rule-edge-fs-write-deny",
				Name:    "Block writes to /etc",
				Type:    RuleTypeEdge,
				Scope:   RuleScope{Kind: RuleScopeEdgeFleet, Value: "fleet-prod"},
				Status:  RuleStatusPublished,
				Version: "v1",
				Audit:   AuditMetadata{CreatedAt: now, CreatedBy: "secops@cordum.io"},
				Match:   json.RawMessage(`{"capability":"file.write"}`),
				Decide:  json.RawMessage(`{"decision":"DENY"}`),
			}},
		}},
		Metadata: BundleMetadata{EdgeMode: EdgeModeEnterpriseStrict},
	}

	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("Marshal err = %v", err)
	}
	var back Bundle
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("Unmarshal err = %v", err)
	}
	if back.ID != bundle.ID || back.Name != bundle.Name {
		t.Fatalf("Bundle envelope drift")
	}
	if back.Metadata.EdgeMode != EdgeModeEnterpriseStrict {
		t.Fatalf("EdgeMode drift: %v vs enterprise-strict", back.Metadata.EdgeMode)
	}
	if len(back.Versions) != 1 || len(back.Versions[0].RuleSnapshot) != 1 {
		t.Fatalf("Versions/RuleSnapshot drift: %+v", back.Versions)
	}
	if back.Versions[0].RuleSnapshot[0].ID != "rule-edge-fs-write-deny" {
		t.Fatalf("RuleSnapshot[0].ID drift")
	}
}

// TestBundle_OmitsZeroMetadata confirms BundleMetadata's zero value uses
// `omitzero` so an unset metadata block doesn't pollute the JSON output.
func TestBundle_OmitsZeroMetadata(t *testing.T) {
	b := Bundle{
		ID:           "bundle-no-meta",
		Name:         "Bundle without edge metadata",
		ScopeBinding: RuleScope{Kind: RuleScopeGlobal},
	}
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("Marshal err = %v", err)
	}
	if bytes.Contains(raw, []byte(`"metadata"`)) {
		t.Fatalf("zero BundleMetadata should be omitted; got %s", raw)
	}
}

func mustCompact(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		t.Fatalf("Compact err = %v on %s", err, raw)
	}
	return buf.Bytes()
}
