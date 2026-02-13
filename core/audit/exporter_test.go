package audit

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// Verify all exporters satisfy the Exporter interface at compile time.
var (
	_ Exporter = (*mockExporter)(nil)
	_ Exporter = (*WebhookExporter)(nil)
	_ Exporter = (*DatadogExporter)(nil)
	_ Exporter = (*SyslogExporter)(nil)
	_ Exporter = (*CloudWatchExporter)(nil)
)

func TestSIEMEvent_JSONRoundTrip(t *testing.T) {
	ev := SIEMEvent{
		Timestamp:     time.Date(2026, 2, 13, 10, 0, 0, 0, time.UTC),
		EventType:     EventSafetyDecision,
		Severity:      SeverityHigh,
		TenantID:      "tenant-1",
		AgentID:       "agent-1",
		JobID:         "job-1",
		Action:        "delete_account",
		Decision:      "deny",
		MatchedRule:   "rule-1",
		Reason:        "Destructive action",
		RiskTags:      []string{"destructive"},
		Capabilities:  []string{"db.write"},
		PolicyVersion: "v3",
		Identity:      "user@example.com",
		Extra:         map[string]string{"key": "value"},
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got SIEMEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.EventType != EventSafetyDecision {
		t.Errorf("EventType = %q, want %q", got.EventType, EventSafetyDecision)
	}
	if got.Severity != SeverityHigh {
		t.Errorf("Severity = %q, want %q", got.Severity, SeverityHigh)
	}
	if got.Action != "delete_account" {
		t.Errorf("Action = %q, want delete_account", got.Action)
	}
	if got.Decision != "deny" {
		t.Errorf("Decision = %q, want deny", got.Decision)
	}
	if got.TenantID != "tenant-1" {
		t.Errorf("TenantID = %q, want tenant-1", got.TenantID)
	}
	if len(got.RiskTags) != 1 || got.RiskTags[0] != "destructive" {
		t.Errorf("RiskTags = %v, want [destructive]", got.RiskTags)
	}
	if got.Extra["key"] != "value" {
		t.Errorf("Extra[key] = %q, want value", got.Extra["key"])
	}
}

func TestSIEMEvent_OmitsEmptyFields(t *testing.T) {
	ev := SIEMEvent{
		EventType: EventSystemAuth,
		Severity:  SeverityInfo,
		Action:    "login",
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Fields with omitempty should be absent when empty.
	for _, field := range []string{"agent_id", "job_id", "decision", "matched_rule", "reason", "risk_tags", "capabilities", "policy_version", "identity", "extra"} {
		if _, ok := raw[field]; ok {
			t.Errorf("expected %q to be omitted from JSON", field)
		}
	}
}

func TestExporter_ExportAndClose(t *testing.T) {
	mock := &mockExporter{}
	events := []SIEMEvent{
		{EventType: EventSafetyDecision, Action: "test-1"},
		{EventType: EventSafetyViolation, Action: "test-2"},
	}

	if err := mock.Export(context.Background(), events); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if got := mock.totalEvents(); got != 2 {
		t.Errorf("totalEvents = %d, want 2", got)
	}
	if err := mock.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestExporter_FailThenSucceed(t *testing.T) {
	mock := &mockExporter{failNext: 1}

	err := mock.Export(context.Background(), []SIEMEvent{{Action: "first"}})
	if err == nil {
		t.Fatal("expected error on first export")
	}

	err = mock.Export(context.Background(), []SIEMEvent{{Action: "second"}})
	if err != nil {
		t.Fatalf("expected success on second export: %v", err)
	}
	if got := mock.totalEvents(); got != 1 {
		t.Errorf("totalEvents = %d, want 1", got)
	}
}

func TestEventConstants(t *testing.T) {
	events := map[string]string{
		"safety.decision":    EventSafetyDecision,
		"safety.approval":    EventSafetyApproval,
		"safety.policy_change": EventPolicyChange,
		"safety.violation":   EventSafetyViolation,
		"system.auth":        EventSystemAuth,
	}
	for want, got := range events {
		if got != want {
			t.Errorf("event constant = %q, want %q", got, want)
		}
	}

	severities := map[string]string{
		"CRITICAL": SeverityCritical,
		"HIGH":     SeverityHigh,
		"MEDIUM":   SeverityMedium,
		"LOW":      SeverityLow,
		"INFO":     SeverityInfo,
	}
	for want, got := range severities {
		if got != want {
			t.Errorf("severity constant = %q, want %q", got, want)
		}
	}
}
