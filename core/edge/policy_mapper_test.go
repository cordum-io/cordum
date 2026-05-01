package edge

import (
	"bytes"
	"reflect"
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

	if req.GetTopic() != EdgePolicyTopic || req.GetTopic() != "job.edge.action" {
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
