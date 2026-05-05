package edge

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestEdgeContractsJSONRoundTripUsePRDSnakeCaseFields(t *testing.T) {
	started := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	ended := started.Add(2 * time.Minute)
	resolved := started.Add(time.Minute)

	session := validEdgeSession(started)
	session.EndedAt = &ended
	execution := validAgentExecution(started)
	execution.EndedAt = &ended
	event := validAgentActionEvent(started)
	approval := validEdgeApproval(started)
	approval.ResolvedAt = &resolved

	assertJSONKeys(t, session, []string{
		"session_id", "tenant_id", "principal_id", "principal_type", "agent_product", "agent_version",
		"mode", "repo", "git_remote", "git_branch", "git_sha", "cwd", "host_id", "device_id",
		"trace_id", "workflow_run_id", "job_id", "policy_snapshot", "enforcement_layers", "policy_mode",
		"status", "risk_summary", "started_at", "ended_at", "labels",
	})
	assertJSONKeys(t, execution, []string{
		"execution_id", "session_id", "tenant_id", "adapter", "mode", "workflow_run_id", "step_id",
		"job_id", "attempt", "trace_id", "worker_id", "policy_snapshot", "status", "started_at", "ended_at", "metrics", "labels",
	})
	assertJSONKeys(t, event, []string{
		"event_id", "session_id", "execution_id", "tenant_id", "principal_id", "seq", "ts", "layer",
		"kind", "agent_product", "tool_name", "tool_use_id", "action_name", "capability", "risk_tags",
		"input_redacted", "input_hash", "decision", "decision_reason", "rule_id", "tier", "policy_snapshot",
		"approval_ref", "artifact_ptrs", "duration_ms", "status", "error_code", "error_message", "labels",
	})
	assertJSONKeys(t, approval, []string{
		"approval_ref", "tenant_id", "session_id", "execution_id", "event_id", "principal_id", "requester",
		"resolver_id", "resolved_by", "status", "decision", "reason", "resolution_reason", "rule_id",
		"policy_snapshot", "action_hash", "input_hash", "created_at", "expires_at", "resolved_at", "consumed_at", "labels", "metadata",
	})

	assertRoundTrip(t, session)
	assertRoundTrip(t, execution)
	assertRoundTrip(t, event)
	assertRoundTrip(t, approval)
}

func TestValidateRejectsRequiredIDsAndTimestamps(t *testing.T) {
	started := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)

	cases := []struct {
		name    string
		value   interface{ Validate() error }
		wantErr string
	}{
		{name: "session tenant", value: mutateSession(started, func(v *EdgeSession) { v.TenantID = "" }), wantErr: "tenant_id"},
		{name: "session id", value: mutateSession(started, func(v *EdgeSession) { v.SessionID = "" }), wantErr: "session_id"},
		{name: "session zero started", value: mutateSession(started, func(v *EdgeSession) { v.StartedAt = time.Time{} }), wantErr: "started_at"},
		{name: "session ended before started", value: mutateSession(started, func(v *EdgeSession) { t := started.Add(-time.Second); v.EndedAt = &t }), wantErr: "ended_at"},
		{name: "execution tenant", value: mutateExecution(started, func(v *AgentExecution) { v.TenantID = "" }), wantErr: "tenant_id"},
		{name: "execution session", value: mutateExecution(started, func(v *AgentExecution) { v.SessionID = "" }), wantErr: "session_id"},
		{name: "execution id", value: mutateExecution(started, func(v *AgentExecution) { v.ExecutionID = "" }), wantErr: "execution_id"},
		{name: "execution ended before started", value: mutateExecution(started, func(v *AgentExecution) { t := started.Add(-time.Second); v.EndedAt = &t }), wantErr: "ended_at"},
		{name: "event tenant", value: mutateEvent(started, func(v *AgentActionEvent) { v.TenantID = "" }), wantErr: "tenant_id"},
		{name: "event session", value: mutateEvent(started, func(v *AgentActionEvent) { v.SessionID = "" }), wantErr: "session_id"},
		{name: "event execution", value: mutateEvent(started, func(v *AgentActionEvent) { v.ExecutionID = "" }), wantErr: "execution_id"},
		{name: "event id", value: mutateEvent(started, func(v *AgentActionEvent) { v.EventID = "" }), wantErr: "event_id"},
		{name: "event zero ts", value: mutateEvent(started, func(v *AgentActionEvent) { v.Timestamp = time.Time{} }), wantErr: "ts"},
		{name: "approval tenant", value: mutateApproval(started, func(v *EdgeApproval) { v.TenantID = "" }), wantErr: "tenant_id"},
		{name: "approval session", value: mutateApproval(started, func(v *EdgeApproval) { v.SessionID = "" }), wantErr: "session_id"},
		{name: "approval execution", value: mutateApproval(started, func(v *EdgeApproval) { v.ExecutionID = "" }), wantErr: "execution_id"},
		{name: "approval event", value: mutateApproval(started, func(v *EdgeApproval) { v.EventID = "" }), wantErr: "event_id"},
		{name: "approval ref", value: mutateApproval(started, func(v *EdgeApproval) { v.ApprovalRef = "" }), wantErr: "approval_ref"},
		{name: "approval zero created", value: mutateApproval(started, func(v *EdgeApproval) { v.CreatedAt = time.Time{} }), wantErr: "created_at"},
		{name: "approval resolved before created", value: mutateApproval(started, func(v *EdgeApproval) { t := started.Add(-time.Second); v.ResolvedAt = &t }), wantErr: "resolved_at"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.value.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() error = %v, want field %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateRejectsUnsafeEnumsAndNegativeCounters(t *testing.T) {
	started := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)

	cases := []struct {
		name    string
		value   interface{ Validate() error }
		wantErr string
	}{
		{name: "policy mode", value: mutateSession(started, func(v *EdgeSession) { v.PolicyMode = PolicyMode("silent") }), wantErr: "policy_mode"},
		{name: "session status", value: mutateSession(started, func(v *EdgeSession) { v.Status = SessionStatus("lost") }), wantErr: "status"},
		{name: "risk level", value: mutateSession(started, func(v *EdgeSession) { v.RiskSummary.MaxRisk = RiskLevel("severe") }), wantErr: "max_risk"},
		{name: "negative risk", value: mutateSession(started, func(v *EdgeSession) { v.RiskSummary.DeniedCount = -1 }), wantErr: "denied_count"},
		{name: "execution status", value: mutateExecution(started, func(v *AgentExecution) { v.Status = ExecutionStatus("paused") }), wantErr: "status"},
		{name: "negative attempt", value: mutateExecution(started, func(v *AgentExecution) { v.Attempt = -1 }), wantErr: "attempt"},
		{name: "negative metric", value: mutateExecution(started, func(v *AgentExecution) { v.Metrics.Events = -1 }), wantErr: "events"},
		{name: "nan llm cost metric", value: mutateExecution(started, func(v *AgentExecution) { v.Metrics.LLMCostUSD = math.NaN() }), wantErr: "llm_cost_usd"},
		{name: "layer", value: mutateEvent(started, func(v *AgentActionEvent) { v.Layer = Layer("browser") }), wantErr: "layer"},
		{name: "decision", value: mutateEvent(started, func(v *AgentActionEvent) { v.Decision = EdgeDecision("ALLOW_WITH_CONSTRAINTS") }), wantErr: "decision"},
		{name: "action status", value: mutateEvent(started, func(v *AgentActionEvent) { v.Status = ActionStatus("retried") }), wantErr: "status"},
		{name: "negative seq", value: mutateEvent(started, func(v *AgentActionEvent) { v.Seq = -1 }), wantErr: "seq"},
		{name: "negative duration", value: mutateEvent(started, func(v *AgentActionEvent) { v.DurationMS = -1 }), wantErr: "duration_ms"},
		{name: "approval status", value: mutateApproval(started, func(v *EdgeApproval) { v.Status = ApprovalStatus("waiting") }), wantErr: "status"},
		{name: "approval decision", value: mutateApproval(started, func(v *EdgeApproval) { v.Decision = ApprovalDecision("maybe") }), wantErr: "decision"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.value.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() error = %v, want field %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateBoundsMapsRedactedInputAndArtifacts(t *testing.T) {
	started := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)

	cases := []struct {
		name    string
		value   interface{ Validate() error }
		wantErr string
	}{
		{name: "too many labels", value: mutateSession(started, func(v *EdgeSession) { v.Labels = tooManyLabels() }), wantErr: "labels"},
		{name: "too many enforcement layers", value: mutateSession(started, func(v *EdgeSession) { v.EnforcementLayers = tooManyLayers() }), wantErr: "enforcement_layers"},
		{name: "oversize input redacted", value: mutateEvent(started, func(v *AgentActionEvent) {
			v.InputRedacted = map[string]any{"blob": strings.Repeat("x", MaxInputRedactedBytes+1)}
		}), wantErr: "input_redacted"},
		{name: "too many artifact ptrs", value: mutateEvent(started, func(v *AgentActionEvent) { v.ArtifactPointers = tooManyArtifacts(started) }), wantErr: "artifact_ptrs"},
		{name: "too much metadata", value: mutateApproval(started, func(v *EdgeApproval) { v.Metadata = tooManyMetadata() }), wantErr: "metadata"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.value.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() error = %v, want field %q", err, tc.wantErr)
			}
		})
	}
}

func TestForwardCompatibleMapsSurviveRoundTripAndFutureEventKindsValidate(t *testing.T) {
	started := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	session := validEdgeSession(started)
	session.EnforcementLayers["future_bridge"] = true
	session.Labels["future_label"] = "kept"

	var roundTrip EdgeSession
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("marshal session: %v", err)
	}
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("unmarshal session: %v", err)
	}
	if !roundTrip.EnforcementLayers["future_bridge"] || roundTrip.Labels["future_label"] != "kept" {
		t.Fatalf("forward-compatible maps not preserved: %#v %#v", roundTrip.EnforcementLayers, roundTrip.Labels)
	}
	if err := roundTrip.Validate(); err != nil {
		t.Fatalf("Validate() with future maps = %v", err)
	}

	event := validAgentActionEvent(started)
	event.Kind = EventKind("future.roadmap.kind")
	if err := event.Validate(); err != nil {
		t.Fatalf("future event kind should remain valid, got %v", err)
	}
}

func TestEventKindConstantsCoverRoadmapPhases(t *testing.T) {
	want := []EventKind{
		EventKindSessionStarted, EventKindSessionHeartbeat, EventKindSessionDegraded, EventKindSessionEnded,
		EventKindExecutionStarted, EventKindExecutionEnded,
		EventKindHookUserPromptSubmit, EventKindHookPreToolUse, EventKindHookPolicyDecision, EventKindHookPermissionRequest,
		EventKindHookPostToolUse, EventKindHookPostToolUseFailure, EventKindHookConfigChange, EventKindHookFileChanged,
		EventKindApprovalRequested, EventKindApprovalGranted, EventKindApprovalRejected,
		EventKindArtifactCreated, EventKindPolicyDenied, EventKindPolicyDegraded, EventKindTerminalLine,
		EventKindMCPToolPre, EventKindMCPToolPost, EventKindMCPToolFailed, EventKindMCPServerConnected, EventKindMCPServerFailed,
		EventKindLLMRequestPre, EventKindLLMRequestPost, EventKindLLMStreamChunk, EventKindLLMCostRecorded, EventKindLLMDataRedacted, EventKindLLMPolicyDenied,
		EventKindRuntimeProcessExec, EventKindRuntimeFileRead, EventKindRuntimeFileWrite, EventKindRuntimeNetworkConnect, EventKindRuntimeDNSQuery,
		EventKindShadowAgentDetected, EventKindShadowAgentResolved,
	}
	got := make(map[EventKind]struct{}, len(want))
	for _, kind := range want {
		if kind == "" {
			t.Fatalf("empty event kind constant in roadmap set")
		}
		got[kind] = struct{}{}
	}
	if len(got) != len(want) {
		t.Fatalf("duplicate event kind constants: got unique %d want %d", len(got), len(want))
	}
}

func TestEdgeContractsDoNotIntroduceRawTranscriptOrToolPayloadFields(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeOf(EdgeSession{}),
		reflect.TypeOf(AgentExecution{}),
		reflect.TypeOf(AgentActionEvent{}),
		reflect.TypeOf(EdgeApproval{}),
	} {
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			jsonName := strings.Split(field.Tag.Get("json"), ",")[0]
			if jsonName == "" || jsonName == "-" {
				continue
			}
			if jsonName == "raw" || jsonName == "transcript" || jsonName == "tool_payload" || strings.Contains(jsonName, "raw_payload") {
				t.Fatalf("%s exposes disallowed sensitive field %q", typ.Name(), jsonName)
			}
		}
	}
}

func assertRoundTrip[T any](t *testing.T, value T) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %T: %v", value, err)
	}
	var got T
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal %T: %v", value, err)
	}
	if !reflect.DeepEqual(value, got) {
		t.Fatalf("round trip mismatch for %T:\nwant %#v\n got %#v", value, value, got)
	}
}

func assertJSONKeys(t *testing.T, value any, want []string) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %T: %v", value, err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal %T map: %v", value, err)
	}
	for _, key := range want {
		if _, ok := got[key]; !ok {
			t.Fatalf("%T JSON missing key %q in %s", value, key, string(data))
		}
	}
	for key := range got {
		if strings.Contains(key, "-") || strings.ContainsAny(key, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
			t.Fatalf("%T JSON key %q is not PRD snake_case", value, key)
		}
	}
}

func validEdgeSession(started time.Time) EdgeSession {
	return EdgeSession{
		SessionID:      "edge_sess_01J",
		TenantID:       "tenant-a",
		PrincipalID:    "user-a",
		PrincipalType:  PrincipalTypeHuman,
		AgentProduct:   "claude-code",
		AgentVersion:   "2.1.123",
		Mode:           SessionModeLocalDev,
		Repo:           "cordum-io/cordum",
		GitRemote:      "git@github.com:cordum-io/cordum.git",
		GitBranch:      "main",
		GitSHA:         "abc123",
		CWD:            "/workspace/cordum",
		HostID:         "host-a",
		DeviceID:       "device-a",
		TraceID:        "trace_01J",
		WorkflowRunID:  "run_123",
		JobID:          "run_123:generate_patch@1",
		PolicySnapshot: "sha256:policy",
		EnforcementLayers: EnforcementLayers{
			"claude_hooks": true,
			"mcp_gateway":  false,
			"llm_proxy":    false,
			"runtime":      false,
		},
		PolicyMode: PolicyModeEnforce,
		Status:     SessionStatusRunning,
		RiskSummary: RiskSummary{
			DeniedCount:   1,
			ApprovalCount: 1,
			ArtifactCount: 1,
			MaxRisk:       RiskLevelHigh,
		},
		StartedAt: started,
		Labels:    Labels{"source": "cordum-claude", "project": "cordum"},
	}
}

func validAgentExecution(started time.Time) AgentExecution {
	return AgentExecution{
		ExecutionID:    "exec_01J",
		SessionID:      "edge_sess_01J",
		TenantID:       "tenant-a",
		Adapter:        AdapterClaudeCodeHook,
		Mode:           ExecutionModeLocalDev,
		WorkflowRunID:  "run_123",
		StepID:         "generate_patch",
		JobID:          "run_123:generate_patch@1",
		Attempt:        1,
		TraceID:        "trace_01J",
		WorkerID:       "worker_123",
		PolicySnapshot: "sha256:policy",
		Status:         ExecutionStatusRunning,
		StartedAt:      started,
		Metrics: ExecutionMetrics{
			Events:          2,
			Allow:           1,
			Deny:            1,
			RequireApproval: 1,
			Artifacts:       1,
			LLMCostUSD:      0.12,
		},
		Labels: Labels{"workflow": "demo"},
	}
}

func validAgentActionEvent(started time.Time) AgentActionEvent {
	return AgentActionEvent{
		EventID:        "evt_01J",
		SessionID:      "edge_sess_01J",
		ExecutionID:    "exec_01J",
		TenantID:       "tenant-a",
		PrincipalID:    "user-a",
		Seq:            42,
		Timestamp:      started.Add(time.Second),
		Layer:          LayerHook,
		Kind:           EventKindHookPreToolUse,
		AgentProduct:   "claude-code",
		ToolName:       "Bash",
		ToolUseID:      "toolu_123",
		ActionName:     "bash.exec",
		Capability:     "exec.shell",
		RiskTags:       []string{"exec"},
		InputRedacted:  map[string]any{"command": "npm test"},
		InputHash:      "sha256:input",
		Decision:       DecisionAllow,
		DecisionReason: "test command allowed",
		RuleID:         "claude-code.allow-tests",
		RuleTier:       "global",
		PolicySnapshot: "sha256:policy",
		ApprovalRef:    "edge_appr_01J",
		ArtifactPointers: []ArtifactPointer{
			validArtifactPointer(started),
		},
		DurationMS: 12,
		Status:     ActionStatusOK,
		ErrorCode:  "",
		Labels:     Labels{"cwd": "/workspace/cordum"},
	}
}

func validArtifactPointer(started time.Time) ArtifactPointer {
	return ArtifactPointer{
		ArtifactType:   ArtifactTypeToolInput,
		SessionID:      "edge_sess_01J",
		ExecutionID:    "exec_01J",
		EventID:        "evt_01J",
		TenantID:       "tenant-a",
		RetentionClass: RetentionClassStandard,
		RedactionLevel: RedactionLevelStandard,
		SHA256:         "sha256:artifact",
		URI:            "artifact://edge/evt_01J/input",
		CreatedAt:      started,
	}
}

func validEdgeApproval(started time.Time) EdgeApproval {
	expires := started.Add(5 * time.Minute)
	return EdgeApproval{
		ApprovalRef:    "edge_appr_01J",
		TenantID:       "tenant-a",
		SessionID:      "edge_sess_01J",
		ExecutionID:    "exec_01J",
		EventID:        "evt_01J",
		PrincipalID:    "user-a",
		Requester:      "user-a",
		Status:         ApprovalStatusPending,
		Reason:         "Editing protected files requires approval",
		RuleID:         "edge.require-approval",
		PolicySnapshot: "sha256:policy",
		ActionHash:     "sha256:action",
		InputHash:      "sha256:input",
		CreatedAt:      started,
		ExpiresAt:      &expires,
		Labels:         Labels{"source": "dashboard"},
		Metadata:       Metadata{"ticket": "SEC-123"},
	}
}

func mutateSession(started time.Time, mutate func(*EdgeSession)) *EdgeSession {
	v := validEdgeSession(started)
	mutate(&v)
	return &v
}

func mutateExecution(started time.Time, mutate func(*AgentExecution)) *AgentExecution {
	v := validAgentExecution(started)
	mutate(&v)
	return &v
}

func mutateEvent(started time.Time, mutate func(*AgentActionEvent)) *AgentActionEvent {
	v := validAgentActionEvent(started)
	mutate(&v)
	return &v
}

func mutateApproval(started time.Time, mutate func(*EdgeApproval)) *EdgeApproval {
	v := validEdgeApproval(started)
	mutate(&v)
	return &v
}

func tooManyLabels() Labels {
	labels := make(Labels, MaxLabelEntries+1)
	for i := 0; i < MaxLabelEntries+1; i++ {
		labels[string(rune('a'+(i%26)))+string(rune('A'+(i%26)))+string(rune('0'+(i%10)))] = "v"
	}
	return labels
}

func tooManyLayers() EnforcementLayers {
	layers := make(EnforcementLayers, MaxEnforcementLayerEntries+1)
	for i := 0; i < MaxEnforcementLayerEntries+1; i++ {
		layers["layer_"+string(rune('a'+(i%26)))+string(rune('0'+(i%10)))] = true
	}
	return layers
}

func tooManyMetadata() Metadata {
	metadata := make(Metadata, MaxMetadataEntries+1)
	for i := 0; i < MaxMetadataEntries+1; i++ {
		metadata[string(rune('a'+(i%26)))+string(rune('0'+(i%10)))] = "v"
	}
	return metadata
}

func tooManyArtifacts(started time.Time) []ArtifactPointer {
	artifacts := make([]ArtifactPointer, MaxArtifactPointersPerEvent+1)
	for i := range artifacts {
		artifacts[i] = validArtifactPointer(started)
		artifacts[i].URI = artifacts[i].URI + string(rune('a'+(i%26)))
	}
	return artifacts
}

// TestArtifactTypeAcceptsAllP0EvidenceTypes pins the catalog of artifact types
// that EDGE-013 evidence export must support. New types added to
// AllArtifactTypes must validate; existing types must keep their stable wire
// values so older events stay loadable.
//
// Test-first design: written before extending validateArtifactType to accept
// edge.test_output, edge.mcp_request, edge.mcp_response,
// edge.llm_prompt_redacted, and edge.llm_response_redacted. On the prior
// 5-type switch, the new types fell through to the default branch and this
// test would have failed with "artifact_type has unsafe value ..." for each.
func TestArtifactTypeAcceptsAllP0EvidenceTypes(t *testing.T) {
	started := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	wantWireValues := map[ArtifactType]string{
		ArtifactTypeTranscript:          "edge.transcript",
		ArtifactTypeDiff:                "edge.diff",
		ArtifactTypeToolInput:           "edge.tool_input",
		ArtifactTypeToolResult:          "edge.tool_result",
		ArtifactTypeTestOutput:          "edge.test_output",
		ArtifactTypeMCPRequest:          "edge.mcp_request",
		ArtifactTypeMCPResponse:         "edge.mcp_response",
		ArtifactTypeLLMPromptRedacted:   "edge.llm_prompt_redacted",
		ArtifactTypeLLMResponseRedacted: "edge.llm_response_redacted",
		ArtifactTypeEvidenceBundle:      "edge.evidence_bundle",
	}
	if len(AllArtifactTypes) != len(wantWireValues) {
		t.Fatalf("AllArtifactTypes length = %d, want %d", len(AllArtifactTypes), len(wantWireValues))
	}
	for _, at := range AllArtifactTypes {
		want, ok := wantWireValues[at]
		if !ok {
			t.Errorf("AllArtifactTypes contains unmapped type %q — update wantWireValues", at)
			continue
		}
		if string(at) != want {
			t.Errorf("ArtifactType %q wire value = %q, want %q (PRD wire compat)", at, string(at), want)
		}
		ptr := validArtifactPointer(started)
		ptr.ArtifactType = at
		if err := ptr.Validate(); err != nil {
			t.Errorf("ArtifactType %q: validate returned %v, want nil", at, err)
		}
	}
}

// TestArtifactTypeRejectsUnknownValues ensures validateArtifactType is a
// closed allowlist. An attacker controlling event.artifact_ptrs cannot smuggle
// arbitrary or future "raw_payload"-style types past the validator.
func TestArtifactTypeRejectsUnknownValues(t *testing.T) {
	started := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	cases := []ArtifactType{
		"",
		"edge.unknown",
		"edge.raw_secret",
		"transcript",                        // missing "edge." prefix
		"edge.transcript ",                  // trailing whitespace, intentionally not normalized
		"edge.tool_input/extra",             // path-like injection
		ArtifactType("edge.\x00transcript"), // NUL byte injection
	}
	for _, at := range cases {
		ptr := validArtifactPointer(started)
		ptr.ArtifactType = at
		if err := ptr.Validate(); err == nil {
			t.Errorf("Validate accepted unsafe ArtifactType %q; want error", at)
		}
	}
}
