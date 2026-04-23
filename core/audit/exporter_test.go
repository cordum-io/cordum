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
	_ Exporter = (*DiscardExporter)(nil)
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
	for _, field := range []string{"agent_id", "job_id", "decision", "matched_rule", "reason", "risk_tags", "capabilities", "policy_version", "identity", "extra", "seq", "event_hash", "prev_hash"} {
		if _, ok := raw[field]; ok {
			t.Errorf("expected %q to be omitted from JSON", field)
		}
	}
}

func TestSIEMEvent_ChainFieldsRoundTrip(t *testing.T) {
	ev := SIEMEvent{
		EventType: EventSafetyDecision,
		Severity:  SeverityInfo,
		Action:    "test",
		TenantID:  "tenant-1",
		Seq:       42,
		EventHash: "abc123",
		PrevHash:  "def456",
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got SIEMEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Seq != 42 {
		t.Errorf("Seq = %d, want 42", got.Seq)
	}
	if got.EventHash != "abc123" {
		t.Errorf("EventHash = %q, want abc123", got.EventHash)
	}
	if got.PrevHash != "def456" {
		t.Errorf("PrevHash = %q, want def456", got.PrevHash)
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
		"safety.decision":                    EventSafetyDecision,
		"delegation.lineage":                 EventDelegationLineage,
		"delegation.rejected":                EventDelegationRejected,
		"delegation.revoked_before_dispatch": EventDelegationRevokedBeforeDispatch,
		"safety.approval":                    EventSafetyApproval,
		"safety.policy_change":               EventPolicyChange,
		"safety.violation":                   EventSafetyViolation,
		"system.auth":                        EventSystemAuth,
		"mcp.tool_approval":                  EventMCPToolApproval,
		"mcp.tool_denied":                    EventMCPToolDenied,
		// shadow_eval is the Phase-2 dual-evaluation signal — pinned
		// here because SIEM correlation rules and the results API both
		// filter on the literal string; an accidental rename would
		// silently drop events from downstream dashboards.
		"shadow_eval": EventShadowEval,
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

func TestNewExporterFromEnvWithEntitlements_NullBackendAliases(t *testing.T) {
	for _, typ := range []string{"null", "discard", "chain-only", "none", ""} {
		t.Run(typ, func(t *testing.T) {
			t.Setenv("CORDUM_AUDIT_EXPORT_TYPE", typ)

			exp, err := NewExporterFromEnvWithEntitlements(nil)
			if err != nil {
				t.Fatalf("NewExporterFromEnvWithEntitlements(%q) error = %v", typ, err)
			}
			if exp == nil {
				t.Fatalf("NewExporterFromEnvWithEntitlements(%q) returned nil exporter", typ)
			}
			if _, ok := exp.Backend().(*DiscardExporter); !ok {
				t.Fatalf("NewExporterFromEnvWithEntitlements(%q) backend = %T, want *DiscardExporter", typ, exp.Backend())
			}
			if err := exp.Close(); err != nil {
				t.Fatalf("close %q exporter: %v", typ, err)
			}
		})
	}
}

func TestDiscardExporter_ExportClose(t *testing.T) {
	t.Parallel()

	exp := NewDiscardExporter()
	if err := exp.Export(context.Background(), []SIEMEvent{{Action: "test"}}); err != nil {
		t.Fatalf("Export error = %v", err)
	}
	if err := exp.Close(); err != nil {
		t.Fatalf("first Close error = %v", err)
	}
	if err := exp.Close(); err != nil {
		t.Fatalf("second Close error = %v", err)
	}
}
