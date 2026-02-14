package v1

import (
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestOutputCheckRequestRoundTripAllFields(t *testing.T) {
	req := &OutputCheckRequest{
		JobId:           "job-1",
		Topic:           "job.demo.run",
		Tenant:          "tenant-a",
		Labels:          map[string]string{"env": "prod", "source": "scheduler"},
		ResultPtr:       "redis://res:job-1",
		ArtifactPtrs:    []string{"redis://art:1", "redis://art:2"},
		ErrorMessage:    "",
		ErrorCode:       "",
		WorkerId:        "worker-1",
		ExecutionMs:     321,
		OutputSizeBytes: 2048,
		ContentHash:     "sha256:abc",
		WorkflowId:      "wf-1",
		StepId:          "step-1",
		OutputContent:   []byte(`{"ok":true}`),
		Capabilities:    []string{"code.write", "net.read"},
		RiskTags:        []string{"secrets", "external_io"},
		PrincipalId:     "principal-1",
		PackId:          "pack-1",
		ContentType:     "application/json",
		OriginalLabels:  map[string]string{"mcp.server": "github"},
	}

	data, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var got OutputCheckRequest
	if err := proto.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}

	if got.GetJobId() != req.GetJobId() || got.GetTopic() != req.GetTopic() || got.GetTenant() != req.GetTenant() {
		t.Fatalf("unexpected identity fields: %#v", &got)
	}
	if got.GetResultPtr() != req.GetResultPtr() || got.GetWorkerId() != req.GetWorkerId() {
		t.Fatalf("unexpected pointer/worker fields: %#v", &got)
	}
	if got.GetExecutionMs() != req.GetExecutionMs() || got.GetOutputSizeBytes() != req.GetOutputSizeBytes() {
		t.Fatalf("unexpected timing/size fields: %#v", &got)
	}
	if got.GetPrincipalId() != req.GetPrincipalId() || got.GetPackId() != req.GetPackId() || got.GetContentType() != req.GetContentType() {
		t.Fatalf("unexpected context fields: %#v", &got)
	}
	if len(got.GetCapabilities()) != 2 || len(got.GetRiskTags()) != 2 {
		t.Fatalf("expected capabilities and risk tags to round-trip: %#v", &got)
	}
	if len(got.GetOriginalLabels()) != 1 || got.GetOriginalLabels()["mcp.server"] != "github" {
		t.Fatalf("expected original_labels to round-trip: %#v", got.GetOriginalLabels())
	}
}

func TestOutputCheckResponseFindingsAndEnums(t *testing.T) {
	resp := &OutputCheckResponse{
		Decision:       OutputDecision_OUTPUT_DECISION_QUARANTINE,
		Reason:         "secret detected",
		RuleId:         "out-1",
		PolicySnapshot: "snap-1",
		Findings: []*OutputFinding{
			{
				Type:           "secret_leak",
				Severity:       "critical",
				Detail:         "aws access key",
				Offset:         12,
				Length:         20,
				Scanner:        "regex",
				Confidence:     0.99,
				MatchedPattern: "AKIA[0-9A-Z]{16}",
			},
		},
		RedactedPtr: "redis://res:job-1:redacted",
	}

	data, err := proto.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var got OutputCheckResponse
	if err := proto.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if got.GetDecision() != OutputDecision_OUTPUT_DECISION_QUARANTINE {
		t.Fatalf("expected quarantine decision, got %v", got.GetDecision())
	}
	if got.GetRedactedPtr() != "redis://res:job-1:redacted" {
		t.Fatalf("unexpected redacted ptr: %q", got.GetRedactedPtr())
	}
	if len(got.GetFindings()) != 1 {
		t.Fatalf("expected one finding, got %d", len(got.GetFindings()))
	}
	f := got.GetFindings()[0]
	if f.GetScanner() != "regex" || f.GetMatchedPattern() != "AKIA[0-9A-Z]{16}" {
		t.Fatalf("expected scanner fields to round-trip, got %#v", f)
	}
	if f.GetConfidence() <= 0 {
		t.Fatalf("expected confidence > 0, got %f", f.GetConfidence())
	}

	if OutputDecision_OUTPUT_DECISION_ALLOW.String() != "OUTPUT_DECISION_ALLOW" {
		t.Fatalf("unexpected allow enum string: %s", OutputDecision_OUTPUT_DECISION_ALLOW.String())
	}
	if OutputDecision_value["OUTPUT_DECISION_REDACT"] != int32(OutputDecision_OUTPUT_DECISION_REDACT) {
		t.Fatalf("enum map missing redact value")
	}
}
