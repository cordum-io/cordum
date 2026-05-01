package edge

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	infraschema "github.com/cordum/cordum/core/infra/schema"
)

func TestEmbeddedEdgeSchemasValidateGoodSamples(t *testing.T) {
	started := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	ended := started.Add(2 * time.Minute)
	resolved := started.Add(time.Minute)

	session := validEdgeSession(started)
	session.EndedAt = &ended
	execution := validAgentExecution(started)
	execution.EndedAt = &ended
	event := validAgentActionEvent(started)
	approval := validEdgeApproval(started)
	approval.Status = ApprovalStatusApproved
	approval.Decision = ApprovalDecisionApprove
	approval.ResolverID = "approver-a"
	approval.ResolvedBy = "approver-a"
	approval.ResolutionReason = "Approved for schema test"
	approval.ResolvedAt = &resolved

	cases := []struct {
		name  string
		value any
	}{
		{name: "edge_session.schema.json", value: session},
		{name: "agent_execution.schema.json", value: execution},
		{name: "agent_action_event.schema.json", value: event},
		{name: "edge_approval.schema.json", value: approval},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schemaJSON, err := Schema(tc.name)
			if err != nil {
				t.Fatalf("Schema(%q): %v", tc.name, err)
			}
			if err := infraschema.ValidateSchema(tc.name, schemaJSON, schemaPayload(t, tc.value)); err != nil {
				t.Fatalf("ValidateSchema(%q) good sample: %v", tc.name, err)
			}
		})
	}
}

func TestEmbeddedEdgeSchemasRejectUnexpectedTopLevelFields(t *testing.T) {
	started := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	payload := schemaPayload(t, validEdgeSession(started))
	payload["tool_payload"] = map[string]any{"raw": "must not be accepted"}

	schemaJSON, err := Schema("edge_session.schema.json")
	if err != nil {
		t.Fatalf("Schema(edge_session.schema.json): %v", err)
	}
	err = infraschema.ValidateSchema("edge_session.schema.json", schemaJSON, payload)
	if err == nil || !strings.Contains(err.Error(), "additionalProperties") {
		t.Fatalf("expected additionalProperties rejection, got %v", err)
	}
}

func TestEmbeddedEdgeSchemasPreserveForwardCompatibleMaps(t *testing.T) {
	started := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	sessionPayload := schemaPayload(t, validEdgeSession(started))
	sessionPayload["enforcement_layers"].(map[string]any)["future_bridge"] = true
	sessionPayload["labels"].(map[string]any)["future_label"] = "kept"

	sessionSchema, err := Schema("edge_session.schema.json")
	if err != nil {
		t.Fatalf("Schema(edge_session.schema.json): %v", err)
	}
	if err := infraschema.ValidateSchema("edge_session.schema.json", sessionSchema, sessionPayload); err != nil {
		t.Fatalf("future session maps should validate: %v", err)
	}

	eventPayload := schemaPayload(t, validAgentActionEvent(started))
	eventPayload["kind"] = "future.roadmap.kind"
	eventPayload["input_redacted"].(map[string]any)["future_shape"] = map[string]any{"kept": true}
	eventSchema, err := Schema("agent_action_event.schema.json")
	if err != nil {
		t.Fatalf("Schema(agent_action_event.schema.json): %v", err)
	}
	if err := infraschema.ValidateSchema("agent_action_event.schema.json", eventSchema, eventPayload); err != nil {
		t.Fatalf("future event maps/kind should validate: %v", err)
	}
}

func TestSchemaAccessorRejectsUnknownName(t *testing.T) {
	if _, err := Schema("missing.schema.json"); err == nil {
		t.Fatalf("expected unknown schema name to fail")
	}
	if _, err := Schema("../edge_session.schema.json"); err == nil {
		t.Fatalf("expected path traversal schema name to fail")
	}
}

func schemaPayload(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %T: %v", value, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal %T to schema payload: %v", value, err)
	}
	return payload
}
