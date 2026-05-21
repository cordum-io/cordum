package edge

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestMapEventToPolicyCheckRequestUsesClassifierOutputAndTrustedMetadata(t *testing.T) {
	event := AgentActionEvent{
		EventID:      "evt-map-1",
		SessionID:    "sess-map-1",
		ExecutionID:  "exec-map-1",
		TenantID:     "tenant-map",
		PrincipalID:  "principal-map",
		Timestamp:    time.Date(2026, 5, 1, 18, 30, 0, 0, time.UTC),
		Layer:        LayerHook,
		Kind:         EventKindHookPreToolUse,
		AgentProduct: "claude-code",
		ToolName:     "Bash",
		ActionName:   "client.spoofed",
		Capability:   "client.spoofed",
		RiskTags:     []string{"safe"},
		InputRedacted: map[string]any{
			"command": "rm -rf /tmp/demo",
			"token":   "[REDACTED]",
		},
		Decision: DecisionRecorded,
		Status:   ActionStatusOK,
		Labels: Labels{
			"custom.team": "platform",
			"edge.layer":  "client-spoof",
		},
	}
	content := []byte(`{"command":"rm -rf /tmp/demo","token":"[REDACTED]"}`)
	classification := ActionClassification{
		ActionName:       "bash.exec",
		Capability:       "exec.shell",
		RiskTags:         []string{"destructive", "exec", "filesystem"},
		Labels:           Labels{"command.class": "destructive", "command.family": "filesystem_delete"},
		InputContent:     content,
		InputContentType: "application/json",
		InputSizeBytes:   int64(len(content)),
	}

	req, err := MapEventToPolicyCheckRequest(event, classification, PolicyMappingOptions{
		ActorID:   "actor-map",
		ActorType: pb.ActorType_ACTOR_TYPE_HUMAN,
	})
	if err != nil {
		t.Fatalf("MapEventToPolicyCheckRequest returned error: %v", err)
	}

	if req.GetTopic() != EdgePolicyTopic {
		t.Fatalf("Topic = %q, want %q", req.GetTopic(), EdgePolicyTopic)
	}
	if req.GetTenant() != "tenant-map" {
		t.Fatalf("Tenant = %q, want tenant-map", req.GetTenant())
	}
	if req.GetPrincipalId() != "principal-map" {
		t.Fatalf("PrincipalId = %q, want principal-map", req.GetPrincipalId())
	}
	if meta := req.GetMeta(); meta == nil {
		t.Fatal("Meta is nil")
	} else {
		if meta.GetTenantId() != "tenant-map" {
			t.Fatalf("Meta.TenantId = %q, want tenant-map", meta.GetTenantId())
		}
		if meta.GetActorId() != "actor-map" {
			t.Fatalf("Meta.ActorId = %q, want actor-map", meta.GetActorId())
		}
		if meta.GetActorType() != pb.ActorType_ACTOR_TYPE_HUMAN {
			t.Fatalf("Meta.ActorType = %v, want human", meta.GetActorType())
		}
		if meta.GetCapability() != "exec.shell" {
			t.Fatalf("Meta.Capability = %q, want classifier capability", meta.GetCapability())
		}
		if !reflect.DeepEqual(meta.GetRiskTags(), []string{"destructive", "exec", "filesystem"}) {
			t.Fatalf("Meta.RiskTags = %#v, want classifier tags", meta.GetRiskTags())
		}
	}

	wantLabels := map[string]string{
		"agent.product":     "claude-code",
		"command.class":     "destructive",
		"command.family":    "filesystem_delete",
		"custom.team":       "platform",
		"edge.action_name":  "bash.exec",
		"edge.event_id":     "evt-map-1",
		"edge.execution_id": "exec-map-1",
		"edge.kind":         "hook.pre_tool_use",
		"edge.layer":        "hook",
		"edge.session_id":   "sess-map-1",
		"hook.event":        "hook.pre_tool_use",
		"hook.tool_name":    "Bash",
	}
	for key, want := range wantLabels {
		if got := req.GetLabels()[key]; got != want {
			t.Fatalf("Labels[%q] = %q, want %q in %#v", key, got, want, req.GetLabels())
		}
	}
	if got := req.GetLabels()["edge.layer"]; got == "client-spoof" {
		t.Fatalf("reserved edge.layer label was trusted from client: %#v", req.GetLabels())
	}
	if req.GetMeta().GetCapability() == event.Capability || reflect.DeepEqual(req.GetMeta().GetRiskTags(), event.RiskTags) {
		t.Fatalf("mapper trusted client capability/risk_tags: meta=%#v event=%#v", req.GetMeta(), event)
	}
	if req.GetInputContentType() != "application/json" {
		t.Fatalf("InputContentType = %q, want application/json", req.GetInputContentType())
	}
	if !bytes.Equal(req.GetInputContent(), content) {
		t.Fatalf("InputContent = %s, want %s", req.GetInputContent(), content)
	}
	if req.GetInputSizeBytes() != int64(len(content)) {
		t.Fatalf("InputSizeBytes = %d, want %d", req.GetInputSizeBytes(), len(content))
	}
}

func TestPolicyMapperClassificationCompletenessCompatibility(t *testing.T) {
	event := policyMapperCompatibilityEvent()
	legacy := ActionClassification{
		ActionName:     "bash.exec",
		Capability:     "exec.shell",
		RiskTags:       []string{"exec"},
		Labels:         Labels{"command.class": "safe"},
		InputContent:   []byte(`{"command":"echo ok"}`),
		InputSizeBytes: 21,
	}

	req, err := MapEventToPolicyCheckRequest(event, legacy, PolicyMappingOptions{})
	if err != nil {
		t.Fatalf("legacy complete classification returned error: %v", err)
	}
	assertPolicyCompletenessLabels(t, req.GetLabels(), "true", "")

	partial := legacy
	partial.RiskTags = nil
	partial.Complete = false
	partial.MissingFields = []string{"risk_tags"}
	req, err = MapEventToPolicyCheckRequest(event, partial, PolicyMappingOptions{})
	if err != nil {
		t.Fatalf("partial risk_tags classification returned error: %v", err)
	}
	assertPolicyCompletenessLabels(t, req.GetLabels(), "false", "risk_tags")

	classifierProduced, err := ClassifyEvent(event)
	if err != nil {
		t.Fatalf("ClassifyEvent: %v", err)
	}
	req, err = MapEventToPolicyCheckRequest(event, classifierProduced, PolicyMappingOptions{})
	if err != nil {
		t.Fatalf("classifier-produced classification returned error: %v", err)
	}
	assertPolicyCompletenessLabels(t, req.GetLabels(), "true", "")
}

func TestPolicyMapperPartialManualRequiredFieldStillRejected(t *testing.T) {
	event := policyMapperCompatibilityEvent()
	partial := ActionClassification{
		ActionName:    "bash.exec",
		RiskTags:      []string{"exec"},
		Complete:      false,
		MissingFields: []string{"capability"},
	}

	if _, err := MapEventToPolicyCheckRequest(event, partial, PolicyMappingOptions{}); err == nil {
		t.Fatal("partial classification missing capability returned nil error")
	} else if !strings.Contains(err.Error(), "capability") {
		t.Fatalf("partial classification error = %q, want capability", err.Error())
	}
	assertPolicyCompletenessLabels(t, mapLabelsForPolicy(event, partial), "false", "capability")
}

func TestMapEventToPolicyCheckRequestValidationAndNormalization(t *testing.T) {
	baseEvent := AgentActionEvent{
		EventID:     "evt-map-validation",
		SessionID:   "sess-map-validation",
		ExecutionID: "exec-map-validation",
		TenantID:    "tenant-map",
		PrincipalID: "principal-map",
		Timestamp:   time.Date(2026, 5, 1, 18, 40, 0, 0, time.UTC),
		Layer:       LayerHook,
		Kind:        EventKindHookPreToolUse,
		ToolName:    "Bash",
		Decision:    DecisionRecorded,
		Status:      ActionStatusOK,
		InputRedacted: map[string]any{
			"command": "echo Bearer edge-mapper-validation-secret",
		},
		Labels: Labels{"custom.note": "Bearer edge-mapper-validation-secret"},
	}
	classification := ActionClassification{
		ActionName:       "bash.exec",
		Capability:       "exec.shell",
		RiskTags:         []string{"exec", "filesystem", "exec"},
		Labels:           Labels{"command.class": "destructive"},
		InputContent:     []byte(`{"command":"<redacted>"}`),
		InputContentType: "application/json",
		InputSizeBytes:   24,
	}

	for _, tc := range []struct {
		name      string
		mutate    func(*AgentActionEvent, *ActionClassification)
		wantField string
	}{
		{name: "missing tenant", mutate: func(event *AgentActionEvent, _ *ActionClassification) { event.TenantID = "" }, wantField: "tenant_id"},
		{name: "missing session", mutate: func(event *AgentActionEvent, _ *ActionClassification) { event.SessionID = "" }, wantField: "session_id"},
		{name: "missing execution", mutate: func(event *AgentActionEvent, _ *ActionClassification) { event.ExecutionID = "" }, wantField: "execution_id"},
		{name: "missing event", mutate: func(event *AgentActionEvent, _ *ActionClassification) { event.EventID = "" }, wantField: "event_id"},
		{name: "missing principal", mutate: func(event *AgentActionEvent, _ *ActionClassification) { event.PrincipalID = "" }, wantField: "principal_id"},
		{name: "missing action", mutate: func(_ *AgentActionEvent, classification *ActionClassification) { classification.ActionName = "" }, wantField: "action_name"},
		{name: "missing capability", mutate: func(_ *AgentActionEvent, classification *ActionClassification) { classification.Capability = "" }, wantField: "capability"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := baseEvent
			classified := classification
			tc.mutate(&event, &classified)

			_, err := MapEventToPolicyCheckRequest(event, classified, PolicyMappingOptions{})
			if err == nil {
				t.Fatal("MapEventToPolicyCheckRequest error = nil, want missing-field error")
			}
			if !strings.Contains(err.Error(), tc.wantField) {
				t.Fatalf("MapEventToPolicyCheckRequest error = %q, want field %q", err.Error(), tc.wantField)
			}
			if strings.Contains(err.Error(), "edge-mapper-validation-secret") {
				t.Fatalf("MapEventToPolicyCheckRequest error leaked raw secret-like value: %q", err.Error())
			}
		})
	}

	req, err := MapEventToPolicyCheckRequest(baseEvent, classification, PolicyMappingOptions{})
	if err != nil {
		t.Fatalf("MapEventToPolicyCheckRequest normalized request error: %v", err)
	}
	if got := req.GetMeta().GetActorId(); got != "principal-map" {
		t.Fatalf("default ActorId = %q, want principal-map", got)
	}
	if !reflect.DeepEqual(req.GetMeta().GetRiskTags(), []string{"exec", "filesystem"}) {
		t.Fatalf("deduped/sorted RiskTags = %#v, want exec/filesystem", req.GetMeta().GetRiskTags())
	}
	if got := req.GetLabels()["custom.note"]; got != defaultRedactionMarker {
		t.Fatalf("custom.note label = %q, want redaction marker in labels %#v", got, req.GetLabels())
	}
	for key, value := range req.GetLabels() {
		if strings.Contains(key, "edge-mapper-validation-secret") || strings.Contains(value, "edge-mapper-validation-secret") {
			t.Fatalf("policy label leaked secret-like value: %q=%q in %#v", key, value, req.GetLabels())
		}
	}
	req.GetInputContent()[0] = '!'
	if string(classification.InputContent) != `{"command":"<redacted>"}` {
		t.Fatalf("mapper did not clone input content; classification content mutated to %s", string(classification.InputContent))
	}
}

func policyMapperCompatibilityEvent() AgentActionEvent {
	return AgentActionEvent{
		EventID:     "evt-map-compat",
		SessionID:   "sess-map-compat",
		ExecutionID: "exec-map-compat",
		TenantID:    "tenant-map",
		PrincipalID: "principal-map",
		Timestamp:   time.Date(2026, 5, 1, 18, 45, 0, 0, time.UTC),
		Layer:       LayerHook,
		Kind:        EventKindHookPreToolUse,
		ToolName:    "Bash",
		Decision:    DecisionRecorded,
		Status:      ActionStatusOK,
		InputRedacted: map[string]any{
			"command": "echo ok",
		},
	}
}

func assertPolicyCompletenessLabels(t *testing.T, labels map[string]string, wantComplete, wantMissing string) {
	t.Helper()
	if got := labels["classifier.complete"]; got != wantComplete {
		t.Fatalf("classifier.complete = %q, want %q in %#v", got, wantComplete, labels)
	}
	gotMissing, hasMissing := labels["classifier.missing_fields"]
	if wantMissing == "" {
		if hasMissing {
			t.Fatalf("classifier.missing_fields = %q, want absent in %#v", gotMissing, labels)
		}
		return
	}
	if gotMissing != wantMissing {
		t.Fatalf("classifier.missing_fields = %q, want %q in %#v", gotMissing, wantMissing, labels)
	}
}
