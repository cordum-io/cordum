package safetykernel

import (
	"context"
	"regexp"
	"testing"

	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func newTruncationTestServer(t *testing.T) *server {
	t.Helper()
	srv := &server{scanners: defaultOutputScanners()}
	err := srv.setPolicy(context.Background(), &config.SafetyPolicy{
		OutputPolicy: config.OutputPolicyConfig{Enabled: true, FailMode: "open"},
		OutputRules: []config.OutputPolicyRule{{
			ID: "sensitive-output", Decision: "quarantine",
			Match: config.OutputPolicyMatch{Topics: []string{"job.*"}, Scanners: []string{"secret"}},
		}},
	}, "truncation-snapshot")
	if err != nil {
		t.Fatalf("set policy: %v", err)
	}
	return srv
}

func TestCheckOutputInfersUpstreamTruncationFromDeclaredSize(t *testing.T) {
	content := []byte("verified clean prefix")
	resp, err := newTruncationTestServer(t).CheckOutput(context.Background(), &pb.OutputCheckRequest{
		JobId: "job-a", Topic: "job.default", Tenant: "tenant-a",
		OutputContent: content, OutputSizeBytes: int64(len(content) + 128),
	})
	if err != nil {
		t.Fatalf("CheckOutput: %v", err)
	}
	if resp.GetDecision() != pb.OutputDecision_OUTPUT_DECISION_QUARANTINE {
		t.Fatalf("decision = %v, want quarantine", resp.GetDecision())
	}
	if !hasProtoOutputFinding(resp.GetFindings(), "content_truncated", "high") {
		t.Fatalf("missing fail-closed truncation finding: %#v", resp.GetFindings())
	}
}

func TestEvaluateOutputInfersUpstreamTruncationFromDeclaredSize(t *testing.T) {
	content := []byte("verified clean prefix")
	resp, err := newTruncationTestServer(t).EvaluateOutput(context.Background(), &OutputEvaluateRequest{
		JobID: "job-a", Topic: "job.default", Tenant: "tenant-a",
		OutputContent: content, OutputSizeBytes: int64(len(content) + 128),
	})
	if err != nil {
		t.Fatalf("EvaluateOutput: %v", err)
	}
	if resp.Decision != "quarantine" {
		t.Fatalf("decision = %q, want quarantine: %#v", resp.Decision, resp)
	}
	if !hasOutputFinding(resp.Findings, "content_truncated", "high") {
		t.Fatalf("missing fail-closed truncation finding: %#v", resp.Findings)
	}
}

func TestEvaluateInputRuleFailsClosedForAnyTruncatedContentCriterion(t *testing.T) {
	content := []byte("verified clean prefix")
	declaredSize := int64(len(content) + 128)
	tests := map[string]compiledInputRule{
		"scanner": {scanners: []string{"secret"}},
		"pattern": {patterns: []compiledOutputPattern{{raw: "secret", re: regexp.MustCompile("secret")}}},
		"keyword": {keywords: []string{"secret"}},
		"scope":   {scope: &config.ScopeConfig{}},
	}
	for name, rule := range tests {
		t.Run(name, func(t *testing.T) {
			rule.maxBytes = declaredSize + 1024
			matched, findings := evaluateInputRule(rule, inputEvaluateRequest{
				content: content, inputSize: declaredSize,
			}, nil)
			if !matched {
				t.Fatal("truncated content-sensitive rule did not fail closed")
			}
			if len(findings) != 1 || findings[0].Type != "content_truncated" {
				t.Fatalf("findings = %#v, want content_truncated", findings)
			}
		})
	}
}

func TestEvaluateInputRuleKeepsPureMetadataAndSizeSemantics(t *testing.T) {
	req := inputEvaluateRequest{tenant: "tenant-a", content: []byte("clean"), inputSize: 64}
	matched, findings := evaluateInputRule(compiledInputRule{tenants: []string{"tenant-a"}}, req, nil)
	if !matched || len(findings) != 0 {
		t.Fatalf("pure metadata result = %v, %#v", matched, findings)
	}
	matched, findings = evaluateInputRule(compiledInputRule{maxBytes: 128}, req, nil)
	if matched || len(findings) != 0 {
		t.Fatalf("below-limit pure size result = %v, %#v", matched, findings)
	}
	req.inputSize = 256
	matched, findings = evaluateInputRule(compiledInputRule{maxBytes: 128}, req, nil)
	if !matched || len(findings) != 1 || findings[0].Type != "input_size_exceeded" {
		t.Fatalf("over-limit pure size result = %v, %#v", matched, findings)
	}
}
