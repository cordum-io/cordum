package scheduler

import (
	"encoding/json"
	"testing"
)

func TestOutputSafetyRecordJSON(t *testing.T) {
	record := OutputSafetyRecord{
		Decision:        OutputQuarantine,
		Reason:          "secret detected",
		RuleID:          "out-001",
		Findings:        []OutputFinding{{Type: "secret_leak", Severity: "critical", Detail: "aws_access_key_id", Scanner: "regex", Confidence: 0.97, MatchedPattern: "AKIA[0-9A-Z]{16}"}},
		CheckedAt:       1700000000,
		CheckDurationMs: 12,
		Phase:           "sync",
	}

	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal output safety record: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw json: %v", err)
	}
	if got := raw["decision"]; got != string(OutputQuarantine) {
		t.Fatalf("expected decision %q, got %v", OutputQuarantine, got)
	}
	if got := raw["rule_id"]; got != "out-001" {
		t.Fatalf("expected rule_id out-001, got %v", got)
	}
	if got := raw["phase"]; got != "sync" {
		t.Fatalf("expected phase sync, got %v", got)
	}

	var roundTrip OutputSafetyRecord
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("round-trip unmarshal: %v", err)
	}
	if roundTrip.Decision != OutputQuarantine {
		t.Fatalf("expected round-trip decision %q, got %q", OutputQuarantine, roundTrip.Decision)
	}
	if len(roundTrip.Findings) != 1 || roundTrip.Findings[0].Type != "secret_leak" {
		t.Fatalf("unexpected findings after round-trip: %+v", roundTrip.Findings)
	}
	if roundTrip.Findings[0].Confidence != 0.97 || roundTrip.Findings[0].Scanner != "regex" {
		t.Fatalf("expected scanner/confidence fields to round-trip, got %+v", roundTrip.Findings[0])
	}
}

func TestOutputDecisionConstants(t *testing.T) {
	if OutputAllow != "ALLOW" || OutputDeny != "DENY" || OutputQuarantine != "QUARANTINE" || OutputRedact != "REDACT" {
		t.Fatalf("unexpected output decisions: %q %q %q %q", OutputAllow, OutputDeny, OutputQuarantine, OutputRedact)
	}
}

func TestTerminalStatesIncludeQuarantined(t *testing.T) {
	if !terminalStates[JobStateQuarantined] {
		t.Fatalf("expected %q to be terminal", JobStateQuarantined)
	}
}
