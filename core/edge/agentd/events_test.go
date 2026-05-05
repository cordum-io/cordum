package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/claude"
)

func TestRecordDecisionEvidenceWritesSanitizedDecisionEvent(t *testing.T) {
	t.Parallel()

	writer := &captureEventWriter{}
	decision := claude.AgentdDecision{Decision: claude.DecisionAllow, Reason: "allowed"}
	gotDecision, err := RecordDecisionEvidence(context.Background(), writer, decision, DecisionEvidence{
		State: evidenceTestState(edgecore.PolicyModeEnforce),
		Request: claude.AgentdRequest{
			EventName:      "PreToolUse",
			SessionID:      "edge_sess_ev",
			ExecutionID:    "edge_exec_ev",
			TenantID:       "tenant-ev",
			PrincipalID:    "principal-ev",
			ToolName:       "Bash",
			ToolUseID:      "toolu-ev",
			TranscriptPath: `C:\Users\yaron\secret-transcript.jsonl`,
			Prompt:         "raw prompt with sk-rawpromptsecret",
			ToolInput:      map[string]any{"token": "Bearer raw-tool-secret"},
			InputRedacted:  map[string]any{"command": "echo Bearer raw-tool-secret"},
			InputHash:      "sha256:input-ev",
			ActionHash:     "sha256:action-ev",
			Capability:     "exec.shell",
			RiskTags:       []string{"exec", "test"},
			Labels:         map[string]string{"command.class": "safe", "unsafe_path": `C:\Users\yaron\secret`},
		},
		Response: EvaluateResponse{
			EventID:        "evt-decision-ev",
			Decision:       string(edgecore.DecisionAllow),
			Reason:         "safe allow",
			RuleID:         "edge.safe.allow",
			PolicySnapshot: "snap-ev",
			ActionHash:     "sha256:action-ev",
			InputHash:      "sha256:input-ev",
		},
		CacheHit:   true,
		DurationMS: 42,
	})
	if err != nil {
		t.Fatalf("RecordDecisionEvidence: %v", err)
	}
	if gotDecision.Decision != decision.Decision {
		t.Fatalf("decision changed after evidence write: got %#v want %#v", gotDecision, decision)
	}
	if len(writer.events) != 1 {
		t.Fatalf("writer event count = %d, want 1", len(writer.events))
	}
	event := writer.events[0]
	if event.EventID != "evt-decision-ev" || event.Kind != edgecore.EventKindHookPolicyDecision {
		t.Fatalf("event identity/kind = %q/%q, want evt-decision-ev/policy_decision", event.EventID, event.Kind)
	}
	if event.Decision != edgecore.DecisionAllow || event.Status != edgecore.ActionStatusOK {
		t.Fatalf("event decision/status = %q/%q, want allow/ok", event.Decision, event.Status)
	}
	if event.Labels["cache"] != "hit" || event.Labels["source"] != "cordum-agentd" {
		t.Fatalf("event labels = %#v, want cache hit + source", event.Labels)
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	text := string(payload)
	for _, forbidden := range []string{"raw-tool-secret", "rawpromptsecret", "secret-transcript", `C:\\Users\\yaron`, "unsafe_path"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("decision evidence leaked forbidden content %q: %s", forbidden, text)
		}
	}
	if !strings.Contains(text, "Bearer [REDACTED]") {
		t.Fatalf("expected redacted command evidence, got %s", text)
	}
}

func TestRecordDecisionEvidenceFailureDoesNotFlipFreshGatewayDecision(t *testing.T) {
	t.Parallel()

	writer := &captureEventWriter{err: errors.New("redis unavailable: Bearer evidence-secret")}
	decision := claude.AgentdDecision{Decision: claude.DecisionAllow}
	gotDecision, err := RecordDecisionEvidence(context.Background(), writer, decision, DecisionEvidence{
		State: evidenceTestState(edgecore.PolicyModeEnterpriseStrict),
		Request: claude.AgentdRequest{
			EventName:     "PreToolUse",
			SessionID:     "edge_sess_ev",
			ExecutionID:   "edge_exec_ev",
			TenantID:      "tenant-ev",
			PrincipalID:   "principal-ev",
			ToolName:      "Bash",
			InputRedacted: map[string]any{"command": "npm test"},
			InputHash:     "sha256:input-ev",
			ActionHash:    "sha256:action-ev",
			Labels:        map[string]string{"command.class": "safe"},
		},
		Response: EvaluateResponse{Decision: string(edgecore.DecisionAllow), EventID: "evt-evidence-fail", PolicySnapshot: "snap-ev"},
	})
	if err == nil {
		t.Fatal("RecordDecisionEvidence returned nil error for failing writer; want observable evidence failure")
	}
	if gotDecision.Decision != claude.DecisionAllow {
		t.Fatalf("fresh Gateway allow flipped after evidence write failure: %#v", gotDecision)
	}
	if strings.Contains(err.Error(), "evidence-secret") {
		t.Fatalf("evidence write error leaked secret: %v", err)
	}
}

func TestAuditEvidence_TierFieldRecorded(t *testing.T) {
	t.Parallel()

	event, err := BuildDecisionEvidenceEvent(DecisionEvidence{
		State: evidenceTestState(edgecore.PolicyModeEnforce),
		Request: claude.AgentdRequest{
			EventName:     "PreToolUse",
			SessionID:     "edge_sess_ev",
			ExecutionID:   "edge_exec_ev",
			TenantID:      "tenant-ev",
			PrincipalID:   "principal-ev",
			ToolName:      "Bash",
			InputRedacted: map[string]any{"command": "npm test"},
			InputHash:     "sha256:input-tier",
			ActionHash:    "sha256:action-tier",
			Labels:        map[string]string{"command.class": "safe"},
		},
		Response: EvaluateResponse{
			Decision:            string(edgecore.DecisionAllow),
			EventID:             "evt-tier-evidence",
			RuleID:              "job.allow-tests",
			RuleTier:            "job",
			PolicySnapshot:      "snap-tier-global",
			JobOverrideSnapshot: "snap-tier-job",
			PermissionDecision:  "allow",
		},
		CacheMiss: true,
	})
	if err != nil {
		t.Fatalf("BuildDecisionEvidenceEvent: %v", err)
	}
	if event.RuleTier != "job" {
		t.Fatalf("RuleTier = %q, want job", event.RuleTier)
	}
	if event.Labels["tier"] != "job" {
		t.Fatalf("labels tier = %q, want job; labels=%#v", event.Labels["tier"], event.Labels)
	}
	siem := edgecore.SIEMEventForAction(event)
	if siem.Extra["tier"] != "job" {
		t.Fatalf("SIEM extra tier = %q, want job; extra=%#v", siem.Extra["tier"], siem.Extra)
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	if !strings.Contains(string(payload), `"tier":"job"`) {
		t.Fatalf("event JSON missing tier=job: %s", payload)
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("event with rule tier did not validate: %v", err)
	}
}

func TestBuildDecisionEvidenceEventAttachesArtifactPointers(t *testing.T) {
	t.Parallel()

	state := evidenceTestState(edgecore.PolicyModeEnforce)
	ptr := edgecore.ArtifactPointer{
		TenantID:       state.TenantID,
		SessionID:      state.SessionID,
		ExecutionID:    state.ExecutionID,
		EventID:        "evt-artifact-evidence",
		ArtifactType:   edgecore.ArtifactTypeTestOutput,
		RetentionClass: edgecore.RetentionClassShort,
		RedactionLevel: edgecore.RedactionLevelStandard,
		SHA256:         strings.Repeat("a", 64),
		URI:            "artifact://tenant-ev/edge_sess_ev/edge_exec_ev/evt-artifact-evidence/test-output",
		CreatedAt:      time.Date(2026, 5, 2, 15, 0, 0, 0, time.UTC),
	}
	event, err := BuildDecisionEvidenceEvent(DecisionEvidence{
		State: state,
		Request: claude.AgentdRequest{
			EventName:     "PreToolUse",
			SessionID:     state.SessionID,
			ExecutionID:   state.ExecutionID,
			TenantID:      state.TenantID,
			PrincipalID:   state.PrincipalID,
			ToolName:      "Bash",
			InputRedacted: map[string]any{"command": "npm test"},
			InputHash:     "sha256:input-artifact",
			ActionHash:    "sha256:action-artifact",
		},
		Response:         EvaluateResponse{Decision: string(edgecore.DecisionAllow), EventID: "evt-artifact-evidence", PolicySnapshot: "snap-ev"},
		ArtifactPointers: []edgecore.ArtifactPointer{ptr},
	})
	if err != nil {
		t.Fatalf("BuildDecisionEvidenceEvent: %v", err)
	}
	if len(event.ArtifactPointers) != 1 || event.ArtifactPointers[0].URI != ptr.URI {
		t.Fatalf("artifact pointers = %#v, want attached pointer", event.ArtifactPointers)
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("event with attached artifact pointer did not validate: %v", err)
	}
}

func TestBuildDecisionEvidenceEventRecordsDegradedFailClosedWithoutRawError(t *testing.T) {
	t.Parallel()

	event, err := BuildDecisionEvidenceEvent(DecisionEvidence{
		State: evidenceTestState(edgecore.PolicyModeEnterpriseStrict),
		Request: claude.AgentdRequest{
			EventName:     "PreToolUse",
			SessionID:     "edge_sess_ev",
			ExecutionID:   "edge_exec_ev",
			TenantID:      "tenant-ev",
			PrincipalID:   "principal-ev",
			ToolName:      "Bash",
			InputRedacted: map[string]any{"command": "rm -rf /tmp/project"},
			InputHash:     "sha256:input-rm",
			ActionHash:    "sha256:action-rm",
			RiskTags:      []string{"destructive", "filesystem"},
		},
		Response:     EvaluateResponse{Decision: string(edgecore.DecisionDeny), EventID: "evt-degraded", PolicySnapshot: "snap-ev"},
		Degraded:     true,
		FailClosed:   true,
		ErrorCode:    "gateway_timeout",
		ErrorMessage: "Gateway timeout: Bearer raw-error-secret",
	})
	if err != nil {
		t.Fatalf("BuildDecisionEvidenceEvent: %v", err)
	}
	if event.Status != edgecore.ActionStatusDegraded || event.Decision != edgecore.DecisionDeny {
		t.Fatalf("degraded event status/decision = %q/%q, want degraded/deny", event.Status, event.Decision)
	}
	if event.Labels["fail_closed"] != "true" || event.Labels["degraded"] != "true" {
		t.Fatalf("degraded labels = %#v, want fail_closed/degraded true", event.Labels)
	}
	payload, _ := json.Marshal(event)
	if strings.Contains(string(payload), "raw-error-secret") {
		t.Fatalf("degraded event leaked raw error secret: %s", payload)
	}
}

type captureEventWriter struct {
	events []edgecore.AgentActionEvent
	err    error
}

func (w *captureEventWriter) WriteEvent(_ context.Context, event edgecore.AgentActionEvent) (edgecore.AgentActionEvent, error) {
	if w.err != nil {
		return edgecore.AgentActionEvent{}, w.err
	}
	w.events = append(w.events, event)
	return event, nil
}

func evidenceTestState(mode edgecore.PolicyMode) SessionState {
	return SessionState{
		SessionID:      "edge_sess_ev",
		ExecutionID:    "edge_exec_ev",
		TenantID:       "tenant-ev",
		PrincipalID:    "principal-ev",
		PolicySnapshot: "snap-ev",
		PolicyMode:     mode,
	}
}
