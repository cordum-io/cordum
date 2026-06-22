package gateway

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/audit"
)

func TestMatchesFilters_AgentAndSearchAndCategory(t *testing.T) {
	ev := &audit.SIEMEvent{
		EventType: audit.EventEdgeActionDenied,
		AgentID:   "agent-7",
		AgentName: "Billing Bot",
		Reason:    "blocked path",
		Extra:     map[string]string{"session_id": "sess-9", "execution_id": "exec-3", "resource_id": "res-1"},
	}

	// agent_id exact match.
	if !matchesFilters(ev, auditEventsFilters{agentID: "agent-7"}) {
		t.Error("agent_id exact should match")
	}
	if matchesFilters(ev, auditEventsFilters{agentID: "agent-X"}) {
		t.Error("agent_id mismatch should be filtered out")
	}
	// agent substring on the human name.
	if !matchesFilters(ev, auditEventsFilters{agent: "billing"}) {
		t.Error("agent substring on name should match")
	}
	if matchesFilters(ev, auditEventsFilters{agent: "nope"}) {
		t.Error("agent substring miss should be filtered out")
	}
	// search now matches agent name + session/execution/resource IDs.
	for _, term := range []string{"billing bot", "sess-9", "exec-3", "res-1", "blocked path"} {
		if !matchesFilters(ev, auditEventsFilters{search: term}) {
			t.Errorf("search %q should match", term)
		}
	}
	// governance/routine category (Hide system audit is backed by category=governance).
	if !matchesFilters(ev, auditEventsFilters{category: audit.CategoryGovernance}) {
		t.Error("edge.action_denied must be governance")
	}
	if matchesFilters(ev, auditEventsFilters{category: audit.CategoryRoutine}) {
		t.Error("edge.action_denied must not be routine")
	}
}

func TestToAuditEventResponseItem_HumanFieldsAndRedaction(t *testing.T) {
	const secret = "sk-test-SECRETCANARY0123456789ABCDEF"
	ev := &audit.SIEMEvent{
		Seq:         5,
		EventType:   audit.EventEdgeActionDenied,
		Severity:    "high",
		AgentID:     "agent-7",
		AgentName:   "Billing Bot",
		Decision:    "deny",
		MatchedRule: "no-prod-writes",
		Reason:      "blocked path",
		Identity:    "user:alice",
		EventHash:   "h2",
		PrevHash:    "h1",
		Extra: map[string]string{
			"session_id":    "sess-9",
			"execution_id":  "exec-3",
			"resource_id":   "res-1",
			"agent_product": "claude-code",
			"trace_id":      "tr-1",
			"artifact_id":   "sha256:abc",
			"api_key":       secret, // non-allowlisted → must never reach human fields
		},
	}
	item := toAuditEventResponseItem("id-1", ev)

	if item.HumanSummary == "" {
		t.Fatal("HumanSummary should be populated")
	}
	if item.Category != audit.CategoryGovernance {
		t.Errorf("Category = %q, want governance", item.Category)
	}
	if item.SessionID != "sess-9" || item.ExecutionID != "exec-3" || item.ResourceID != "res-1" {
		t.Errorf("pivots: session=%q exec=%q res=%q", item.SessionID, item.ExecutionID, item.ResourceID)
	}
	if item.AgentProduct != "claude-code" || item.TraceID != "tr-1" || item.ArtifactID != "sha256:abc" {
		t.Errorf("product/trace/artifact: %q/%q/%q", item.AgentProduct, item.TraceID, item.ArtifactID)
	}
	if item.AgentLabel != "Billing Bot" {
		t.Errorf("AgentLabel = %q", item.AgentLabel)
	}
	if !strings.Contains(item.ActorLabel, "alice") {
		t.Errorf("ActorLabel = %q, want to contain alice", item.ActorLabel)
	}
	// Existing fields must be preserved unchanged.
	if item.Seq != 5 || item.EventHash != "h2" || item.PrevHash != "h1" || item.AgentName != "Billing Bot" {
		t.Errorf("existing fields changed: %+v", item)
	}
	// The non-allowlisted secret must not leak into any humanized field.
	blob := strings.Join([]string{
		item.HumanSummary, item.ActorLabel, item.AgentLabel, item.ResourceLabel,
		item.InputPreview, item.OutputPreview, item.AgentProduct, item.TraceID, item.ArtifactID,
	}, " ")
	if strings.Contains(blob, secret) || strings.Contains(blob, "SECRETCANARY") {
		t.Errorf("humanized fields leaked secret: %q", blob)
	}
}

func TestParseAuditEventsQuery_AgentParams(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/audit/events?agent_id=agent-7&agent=Billing+Bot&search=Foo", nil)
	_, _, filters, err := parseAuditEventsQuery(r)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if filters.agentID != "agent-7" {
		t.Errorf("agentID = %q", filters.agentID)
	}
	if filters.agent != "billing bot" {
		t.Errorf("agent = %q, want lowercased", filters.agent)
	}
	if filters.search != "foo" {
		t.Errorf("search = %q, want lowercased", filters.search)
	}
}
