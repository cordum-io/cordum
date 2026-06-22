package audit

import (
	"encoding/json"
	"strings"
	"testing"
)

// legacyComplianceCSVHeaders is the frozen pre-task-c8d4b056 column order. The
// humanized columns must be APPENDED after these without renaming/reordering.
var legacyComplianceCSVHeaders = []string{
	"timestamp", "event_type", "severity", "tenant_id", "agent_id", "agent_name",
	"agent_risk_tier", "job_id", "action", "decision", "matched_rule", "reason",
	"risk_tags", "capabilities", "policy_version", "identity", "seq", "event_hash",
	"prev_hash", "soc2_controls", "extra_json",
}

var humanizedCSVColumns = []string{
	"human_summary", "actor_label", "agent_label", "resource_label", "session_id",
	"execution_id", "resource_id", "input_preview", "output_preview", "trace_id", "artifact_id",
}

func TestComplianceCSVHeaders_AppendedNotReordered(t *testing.T) {
	if len(complianceCSVHeaders) != len(legacyComplianceCSVHeaders)+len(humanizedCSVColumns) {
		t.Fatalf("header count = %d, want %d", len(complianceCSVHeaders), len(legacyComplianceCSVHeaders)+len(humanizedCSVColumns))
	}
	for i, h := range legacyComplianceCSVHeaders {
		if complianceCSVHeaders[i] != h {
			t.Fatalf("existing column %d reordered: got %q want %q", i, complianceCSVHeaders[i], h)
		}
	}
	for i, h := range humanizedCSVColumns {
		if complianceCSVHeaders[len(legacyComplianceCSVHeaders)+i] != h {
			t.Fatalf("appended column %d = %q, want %q", i, complianceCSVHeaders[len(legacyComplianceCSVHeaders)+i], h)
		}
	}
}

func sampleHumanizeEvent() SIEMEvent {
	return SIEMEvent{
		EventType:   EventEdgeActionDenied,
		Severity:    "high",
		TenantID:    "t",
		AgentID:     "agent-7",
		AgentName:   "Billing Bot",
		Decision:    "deny",
		MatchedRule: "no-prod-writes",
		Reason:      "blocked path",
		Extra: map[string]string{
			"session_id":    "sess-9",
			"execution_id":  "exec-3",
			"resource_id":   "res-1",
			"trace_id":      "tr-1",
			"artifact_id":   "sha256:abc",
			"input_preview": "command read file",
			"api_key":       "sk-test-SECRETCANARY0123456789ABCDEF", // non-allowlisted
		},
	}
}

func TestBuildCSVRow_HumanizedCellsAndRedaction(t *testing.T) {
	ev := sampleHumanizeEvent()
	row := buildCSVRow(ev, []string{"CC7.3"})
	if len(row) != len(complianceCSVHeaders) {
		t.Fatalf("row width %d != header width %d", len(row), len(complianceCSVHeaders))
	}
	cell := func(name string) string {
		for i, h := range complianceCSVHeaders {
			if h == name {
				return row[i]
			}
		}
		t.Fatalf("unknown column %q", name)
		return ""
	}
	// Existing columns unchanged.
	if cell("agent_name") != "Billing Bot" || cell("decision") != "deny" || cell("reason") != "blocked path" {
		t.Errorf("existing cells changed: name=%q decision=%q reason=%q", cell("agent_name"), cell("decision"), cell("reason"))
	}
	// Appended humanized cells populated.
	if cell("human_summary") == "" {
		t.Error("human_summary empty")
	}
	if cell("agent_label") != "Billing Bot" {
		t.Errorf("agent_label = %q", cell("agent_label"))
	}
	if cell("session_id") != "sess-9" || cell("execution_id") != "exec-3" || cell("resource_id") != "res-1" {
		t.Errorf("pivot cells: %q/%q/%q", cell("session_id"), cell("execution_id"), cell("resource_id"))
	}
	if cell("trace_id") != "tr-1" || cell("artifact_id") != "sha256:abc" || cell("input_preview") != "command read file" {
		t.Errorf("trace/artifact/preview: %q/%q/%q", cell("trace_id"), cell("artifact_id"), cell("input_preview"))
	}
	// The non-allowlisted secret must not appear in ANY humanized column.
	for _, name := range humanizedCSVColumns {
		if strings.Contains(cell(name), "SECRETCANARY") {
			t.Errorf("humanized column %q leaked secret: %q", name, cell(name))
		}
	}
}

func TestBuildCSVRow_FormulaInjectionNeutralised(t *testing.T) {
	ev := SIEMEvent{
		EventType: EventSafetyDecision,
		Action:    "job.submit",
		// A summary that, after composition, starts with a spreadsheet formula
		// trigger must be neutralised by csvSafe.
		Identity: "=cmd|' /c calc'!A1",
	}
	row := buildCSVRow(ev, nil)
	for i, h := range complianceCSVHeaders {
		if h == "human_summary" || h == "actor_label" {
			if c := row[i]; c != "" && strings.ContainsAny(string(c[0]), "=+-@\t\r") {
				t.Errorf("column %q not formula-guarded: %q", h, c)
			}
		}
	}
}

func TestBuildNDJSONEventLine_AdditivePropsAndPayloadRetained(t *testing.T) {
	ev := sampleHumanizeEvent()
	line, err := buildNDJSONEventLine(ev, []string{"CC7.3"})
	if err != nil {
		t.Fatalf("buildNDJSONEventLine: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(line, &obj); err != nil {
		t.Fatalf("ndjson not valid JSON: %v (%s)", err, line)
	}
	// Original event payload + chain/control metadata retained.
	if obj["event_type"] != string(EventEdgeActionDenied) {
		t.Errorf("lost original event_type: %v", obj["event_type"])
	}
	if obj["type"] != "event" || obj["soc2_controls"] == nil {
		t.Errorf("lost type/soc2_controls: %#v", obj)
	}
	// Additive humanized top-level properties present.
	if s, _ := obj["human_summary"].(string); s == "" {
		t.Error("ndjson human_summary missing")
	}
	if obj["session_id"] != "sess-9" || obj["agent_label"] != "Billing Bot" {
		t.Errorf("ndjson humanized props: session=%v agent=%v", obj["session_id"], obj["agent_label"])
	}
	// Humanized props never carry the non-allowlisted secret.
	for _, k := range humanizedCSVColumns {
		if v, _ := obj[k].(string); strings.Contains(v, "SECRETCANARY") {
			t.Errorf("ndjson humanized prop %q leaked secret: %q", k, v)
		}
	}
}
