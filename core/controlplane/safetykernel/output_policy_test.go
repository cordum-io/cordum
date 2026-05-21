package safetykernel

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/redisutil"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
)

func boolPtr(v bool) *bool { return &v }

func TestCompileOutputRulesNormalizesScannersAndEnable(t *testing.T) {
	policy := &config.SafetyPolicy{
		OutputRules: []config.OutputPolicyRule{
			{
				ID:       "disabled-rule",
				Enabled:  boolPtr(false),
				Decision: "quarantine",
				Match: config.OutputPolicyMatch{
					Scanners: []string{"secret"},
				},
			},
			{
				ID:       "enabled-rule",
				Decision: "quarantine",
				Severity: "critical",
				Match: config.OutputPolicyMatch{
					Detectors:    []string{"secret_leak"},
					OutputSizeGt: 4096,
				},
			},
		},
	}

	rules := compileOutputRules(policy)
	if len(rules) != 1 {
		t.Fatalf("expected one compiled rule, got %d", len(rules))
	}
	if rules[0].id != "enabled-rule" {
		t.Fatalf("unexpected rule id: %q", rules[0].id)
	}
	if len(rules[0].scanners) != 1 || rules[0].scanners[0] != "secret" {
		t.Fatalf("expected normalized scanner alias, got %#v", rules[0].scanners)
	}
	if rules[0].maxOutputBytes != 4096 {
		t.Fatalf("expected output_size_gt to map to max bytes, got %d", rules[0].maxOutputBytes)
	}
}

func TestOutputEvaluateRequestFromProtoIncludesContext(t *testing.T) {
	req := &pb.OutputCheckRequest{
		JobId:        "job-ctx",
		Topic:        "job.demo",
		Tenant:       "tenant-a",
		ResultPtr:    "redis://res:job-ctx",
		ErrorMessage: "none",
		ErrorCode:    "ok",
		WorkerId:     "worker-1",
		ExecutionMs:  55,
		WorkflowId:   "wf-1",
		StepId:       "step-1",
		Capabilities: []string{"code.execute"},
		RiskTags:     []string{"secrets"},
		PrincipalId:  "principal-a",
		PackId:       "pack-a",
		ContentType:  "text/plain",
	}
	got := outputEvaluateRequestFromProto(req, []byte("output data"))
	if got.JobID != "job-ctx" || got.Topic != "job.demo" || got.Tenant != "tenant-a" {
		t.Fatalf("unexpected base context: %#v", got)
	}
	if got.ResultPtr != "redis://res:job-ctx" || got.StepID != "step-1" || got.WorkflowID != "wf-1" {
		t.Fatalf("unexpected workflow/pointer context: %#v", got)
	}
	if got.PrincipalID != "principal-a" || got.PackID != "pack-a" || got.ContentType != "text/plain" {
		t.Fatalf("unexpected actor/content context: %#v", got)
	}
	if len(got.OutputContent) == 0 || got.OutputSizeBytes != int64(len("output data")) {
		t.Fatalf("expected dereferenced output content to be captured, got %#v", got)
	}
}

func TestCheckOutputScansSecretContent(t *testing.T) {
	srv := &server{
		scanners: defaultOutputScanners(),
	}
	_ = srv.setPolicy(context.Background(), &config.SafetyPolicy{
		OutputPolicy: config.OutputPolicyConfig{Enabled: true, FailMode: "open"},
		OutputRules: []config.OutputPolicyRule{
			{
				ID:       "out-secret",
				Decision: "quarantine",
				Reason:   "secret detected",
				Match: config.OutputPolicyMatch{
					Topics:   []string{"job.*"},
					Scanners: []string{"secret"},
				},
			},
		},
	}, "snap-1")

	resp, err := srv.CheckOutput(context.Background(), &pb.OutputCheckRequest{
		JobId:         "job-1",
		Topic:         "job.default",
		Tenant:        "default",
		OutputContent: []byte("leak AKIA1234567890ABCDEF in text"),
	})
	if err != nil {
		t.Fatalf("check output: %v", err)
	}
	if resp.GetDecision() != pb.OutputDecision_OUTPUT_DECISION_QUARANTINE {
		t.Fatalf("expected quarantine, got %v", resp.GetDecision())
	}
	if resp.GetRuleId() != "out-secret" {
		t.Fatalf("unexpected rule id: %q", resp.GetRuleId())
	}
	if len(resp.GetFindings()) == 0 {
		t.Fatalf("expected findings for secret content")
	}
}

func TestCheckOutputMatchesOutputSizeLimit(t *testing.T) {
	srv := &server{
		scanners: defaultOutputScanners(),
	}
	_ = srv.setPolicy(context.Background(), &config.SafetyPolicy{
		OutputPolicy: config.OutputPolicyConfig{Enabled: true, FailMode: "open"},
		OutputRules: []config.OutputPolicyRule{
			{
				ID:       "out-size",
				Decision: "quarantine",
				Match: config.OutputPolicyMatch{
					Topics:       []string{"job.*"},
					OutputSizeGt: 1024,
				},
			},
		},
	}, "snap-2")

	resp, err := srv.CheckOutput(context.Background(), &pb.OutputCheckRequest{
		JobId:           "job-2",
		Topic:           "job.default",
		OutputSizeBytes: 8192,
	})
	if err != nil {
		t.Fatalf("check output: %v", err)
	}
	if resp.GetDecision() != pb.OutputDecision_OUTPUT_DECISION_QUARANTINE {
		t.Fatalf("expected quarantine from size rule, got %v", resp.GetDecision())
	}
}

func TestCheckOutputDisabledPolicyReturnsAllow(t *testing.T) {
	srv := &server{
		scanners: defaultOutputScanners(),
	}
	_ = srv.setPolicy(context.Background(), &config.SafetyPolicy{
		OutputPolicy: config.OutputPolicyConfig{Enabled: false, FailMode: "open"},
		OutputRules: []config.OutputPolicyRule{
			{
				ID:       "out-secret",
				Decision: "quarantine",
				Match: config.OutputPolicyMatch{
					Topics:   []string{"job.*"},
					Scanners: []string{"secret"},
				},
			},
		},
	}, "snap-3")

	resp, err := srv.CheckOutput(context.Background(), &pb.OutputCheckRequest{
		JobId:         "job-3",
		Topic:         "job.default",
		OutputContent: []byte("AKIA1234567890ABCDEF"),
	})
	if err != nil {
		t.Fatalf("check output: %v", err)
	}
	if resp.GetDecision() != pb.OutputDecision_OUTPUT_DECISION_ALLOW {
		t.Fatalf("expected allow when output policy disabled, got %v", resp.GetDecision())
	}
}

func TestLoadOutputScannersFromConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "output_scanners.yaml")
	content := `
version: v1
scanners:
  secret:
    finding_type: secret_leak
    patterns:
      - name: custom_secret
        regex: "CUSTOMSECRET_[A-Z0-9]{8}"
        severity: critical
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write scanner file: %v", err)
	}

	t.Setenv(envOutputScannersPath, path)
	scanners := loadOutputScanners()
	secret, ok := scanners["secret"]
	if !ok || secret == nil {
		t.Fatalf("expected custom secret scanner to load")
	}
	findings := secret.Scan([]byte("prefix CUSTOMSECRET_ABC12345 suffix"))
	if len(findings) == 0 {
		t.Fatalf("expected custom scanner finding")
	}
}

func TestLoadOutputScannersDetectsIsraeliPII(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "output_scanners.yaml")
	content := `
version: v1
scanners:
  pii:
    finding_type: pii
    patterns:
      - name: email_address
        regex: "\\b[A-Za-z0-9._%+\\-]+@[A-Za-z0-9.\\-]+\\.[A-Za-z]{2,}\\b"
        severity: high
      - name: israeli_national_id
        regex: "\\b\\d{9}\\b"
        severity: high
        context_required: true
      - name: israeli_mobile
        regex: "\\b05\\d-?\\d{3}-?\\d{4}\\b"
        severity: high
      - name: israeli_bank_account
        regex: "\\b\\d{2}-\\d{3}-\\d{5,6}\\b"
        severity: high
        context_required: true
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write scanner file: %v", err)
	}

	t.Setenv(envOutputScannersPath, path)
	scanners := loadOutputScanners()
	pii, ok := scanners["pii"]
	if !ok || pii == nil {
		t.Fatalf("expected pii scanner to load")
	}

	// Simulate the vendor_email output with Israeli PII
	vendorOutput := `
PRIMARY DELIVERY CONTACT:
  Name: Yael Cohen
  Employee ID (Teudat Zehut): 312456789
  Mobile: 052-345-6789
  Email: yael.cohen@example.com
  Home Address: 45 Herzl St, Apt 3, Raanana 4321001

BILLING RECONCILIATION:
  Account Holder: Yael Cohen
  Bank Account: Leumi 12-345-678901
`

	findings := pii.Scan([]byte(vendorOutput))
	if len(findings) == 0 {
		t.Fatalf("expected PII findings in vendor_email-style output, got 0")
	}

	// Verify we detect at least 3 types: email, phone, national ID
	foundTypes := map[string]bool{}
	for _, f := range findings {
		foundTypes[f.Detail] = true
	}

	for _, expected := range []string{"email_address", "israeli_mobile"} {
		found := false
		for _, f := range findings {
			if f.Detail == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected finding with detail %q, found types: %v", expected, findings)
		}
	}

	if len(findings) < 3 {
		t.Errorf("expected at least 3 PII findings (email, phone, ID/bank), got %d", len(findings))
	}
}

func TestCheckOutputDereferencesResultPointer(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer mr.Close()

	resultClient, err := redisutil.NewClient("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("new redis client: %v", err)
	}
	defer func() { _ = resultClient.Close() }()

	if err := resultClient.Set(context.Background(), "res:job-pointer", []byte("leak AKIA1234567890ABCDEF in text"), 0).Err(); err != nil {
		t.Fatalf("seed result pointer content: %v", err)
	}

	srv := &server{
		scanners:     defaultOutputScanners(),
		resultClient: resultClient,
	}
	_ = srv.setPolicy(context.Background(), &config.SafetyPolicy{
		OutputPolicy: config.OutputPolicyConfig{Enabled: true, FailMode: "open"},
		OutputRules: []config.OutputPolicyRule{
			{
				ID:       "out-secret",
				Decision: "quarantine",
				Match: config.OutputPolicyMatch{
					Topics:   []string{"job.*"},
					Scanners: []string{"secret"},
				},
			},
		},
	}, "snap-pointer")

	resp, err := srv.CheckOutput(context.Background(), &pb.OutputCheckRequest{
		JobId:     "job-pointer",
		Topic:     "job.default",
		ResultPtr: "redis://res:job-pointer",
	})
	if err != nil {
		t.Fatalf("check output: %v", err)
	}
	if resp.GetDecision() != pb.OutputDecision_OUTPUT_DECISION_QUARANTINE {
		t.Fatalf("expected quarantine from pointer content, got %v", resp.GetDecision())
	}
}

func TestCheckOutputKeywordMatching(t *testing.T) {
	srv := &server{
		scanners: defaultOutputScanners(),
	}
	_ = srv.setPolicy(context.Background(), &config.SafetyPolicy{
		OutputPolicy: config.OutputPolicyConfig{Enabled: true, FailMode: "open"},
		OutputRules: []config.OutputPolicyRule{
			{
				ID:       "out-keyword",
				Decision: "quarantine",
				Reason:   "sensitive keyword detected",
				Match: config.OutputPolicyMatch{
					Topics:   []string{"job.*"},
					Keywords: []string{"CONFIDENTIAL", "TOP SECRET"},
				},
			},
		},
	}, "snap-kw")

	// Should quarantine when keyword found
	resp, err := srv.CheckOutput(context.Background(), &pb.OutputCheckRequest{
		JobId:         "job-kw-1",
		Topic:         "job.default",
		OutputContent: []byte("This document is CONFIDENTIAL and must not be shared."),
	})
	if err != nil {
		t.Fatalf("check output: %v", err)
	}
	if resp.GetDecision() != pb.OutputDecision_OUTPUT_DECISION_QUARANTINE {
		t.Fatalf("expected quarantine from keyword match, got %v", resp.GetDecision())
	}

	// Should allow when no keyword found
	resp, err = srv.CheckOutput(context.Background(), &pb.OutputCheckRequest{
		JobId:         "job-kw-2",
		Topic:         "job.default",
		OutputContent: []byte("This is normal public output with no issues."),
	})
	if err != nil {
		t.Fatalf("check output: %v", err)
	}
	if resp.GetDecision() != pb.OutputDecision_OUTPUT_DECISION_ALLOW {
		t.Fatalf("expected allow when no keyword matched, got %v", resp.GetDecision())
	}
}

func TestCheckOutputContentTypeFilter(t *testing.T) {
	srv := &server{
		scanners: defaultOutputScanners(),
	}
	_ = srv.setPolicy(context.Background(), &config.SafetyPolicy{
		OutputPolicy: config.OutputPolicyConfig{Enabled: true, FailMode: "open"},
		OutputRules: []config.OutputPolicyRule{
			{
				ID:       "out-binary",
				Decision: "quarantine",
				Reason:   "binary output not allowed",
				Match: config.OutputPolicyMatch{
					Topics:       []string{"job.*"},
					ContentTypes: []string{"application/octet-stream"},
					// No scanners/patterns: metadata-only rule
				},
			},
		},
	}, "snap-ct")

	// Should quarantine when content type matches
	resp, err := srv.CheckOutput(context.Background(), &pb.OutputCheckRequest{
		JobId:       "job-ct-1",
		Topic:       "job.default",
		ContentType: "application/octet-stream",
	})
	if err != nil {
		t.Fatalf("check output: %v", err)
	}
	if resp.GetDecision() != pb.OutputDecision_OUTPUT_DECISION_QUARANTINE {
		t.Fatalf("expected quarantine for binary content type, got %v", resp.GetDecision())
	}

	// Should allow when content type doesn't match
	resp, err = srv.CheckOutput(context.Background(), &pb.OutputCheckRequest{
		JobId:       "job-ct-2",
		Topic:       "job.default",
		ContentType: "text/plain",
	})
	if err != nil {
		t.Fatalf("check output: %v", err)
	}
	if resp.GetDecision() != pb.OutputDecision_OUTPUT_DECISION_ALLOW {
		t.Fatalf("expected allow for text content type, got %v", resp.GetDecision())
	}
}

func TestEvaluateOutputDirect(t *testing.T) {
	srv := &server{
		scanners: defaultOutputScanners(),
	}
	_ = srv.setPolicy(context.Background(), &config.SafetyPolicy{
		OutputPolicy: config.OutputPolicyConfig{Enabled: true, FailMode: "open"},
		OutputRules: []config.OutputPolicyRule{
			{
				ID:       "out-secret-eval",
				Decision: "quarantine",
				Reason:   "secret in output",
				Match: config.OutputPolicyMatch{
					Topics:   []string{"job.*"},
					Scanners: []string{"secret"},
				},
			},
		},
	}, "snap-eval")

	// EvaluateOutput should detect secrets
	resp, err := srv.EvaluateOutput(context.Background(), &OutputEvaluateRequest{
		JobID:         "job-eval-1",
		Topic:         "job.default",
		Tenant:        "default",
		OutputContent: []byte("leak AKIA1234567890ABCDEF in text"),
	})
	if err != nil {
		t.Fatalf("evaluate output: %v", err)
	}
	if resp.Decision != "quarantine" {
		t.Fatalf("expected quarantine, got %q", resp.Decision)
	}
	if resp.RuleID != "out-secret-eval" {
		t.Fatalf("expected rule id out-secret-eval, got %q", resp.RuleID)
	}
	if len(resp.Findings) == 0 {
		t.Fatalf("expected findings in evaluate output response")
	}
	if resp.PolicySnapshot != "snap-eval" {
		t.Fatalf("expected policy snapshot snap-eval, got %q", resp.PolicySnapshot)
	}

	// EvaluateOutput should allow clean content
	resp, err = srv.EvaluateOutput(context.Background(), &OutputEvaluateRequest{
		JobID:         "job-eval-2",
		Topic:         "job.default",
		Tenant:        "default",
		OutputContent: []byte("safe output with no secrets"),
	})
	if err != nil {
		t.Fatalf("evaluate output: %v", err)
	}
	if resp.Decision != "allow" {
		t.Fatalf("expected allow, got %q", resp.Decision)
	}
}

func TestEvaluateOutput_FailClosedOnResultGetError(t *testing.T) {
	resultClient := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		MaxRetries:   0,
		DialTimeout:  20 * time.Millisecond,
		ReadTimeout:  20 * time.Millisecond,
		WriteTimeout: 20 * time.Millisecond,
	})
	t.Cleanup(func() { _ = resultClient.Close() })

	srv := &server{
		scanners: defaultOutputScanners(),
	}
	_ = srv.setPolicy(context.Background(), &config.SafetyPolicy{
		OutputPolicy: config.OutputPolicyConfig{Enabled: true, FailMode: "open"},
		OutputRules: []config.OutputPolicyRule{
			{
				ID:       "out-secret-pointer-fail",
				Decision: "quarantine",
				Match: config.OutputPolicyMatch{
					Topics:   []string{"job.*"},
					Scanners: []string{"secret"},
				},
			},
		},
	}, "snap-pointer-fail")
	srv.resultClient = resultClient

	resp, err := srv.EvaluateOutput(context.Background(), &OutputEvaluateRequest{
		JobID:     "job-pointer-fail",
		Topic:     "job.default",
		ResultPtr: "redis://res:job-pointer-fail",
	})
	if err != nil {
		return
	}
	if resp == nil {
		t.Fatal("expected fail-closed response or error, got nil response and nil error")
	}
	if resp.Decision == "allow" {
		t.Fatalf("expected result pointer read error to fail closed, got allow: %#v", resp)
	}
	if resp.Decision == "quarantine" && !hasOutputFindingType(resp.Findings, "pointer_unreadable") {
		t.Fatalf("expected pointer_unreadable finding on quarantine response, got %#v", resp.Findings)
	}
}

func hasOutputFindingType(findings []outputFinding, want string) bool {
	for _, finding := range findings {
		if finding.Type == want {
			return true
		}
	}
	return false
}

func TestEvaluateOutputKeywordAndContentType(t *testing.T) {
	srv := &server{
		scanners: defaultOutputScanners(),
	}
	_ = srv.setPolicy(context.Background(), &config.SafetyPolicy{
		OutputPolicy: config.OutputPolicyConfig{Enabled: true, FailMode: "open"},
		OutputRules: []config.OutputPolicyRule{
			{
				ID:       "out-kw-ct",
				Decision: "redact",
				Reason:   "sensitive keyword in JSON output",
				Match: config.OutputPolicyMatch{
					Topics:       []string{"job.*"},
					Keywords:     []string{"password"},
					ContentTypes: []string{"application/json"},
				},
			},
		},
	}, "snap-kw-ct")

	// Should redact when both keyword and content type match
	resp, err := srv.EvaluateOutput(context.Background(), &OutputEvaluateRequest{
		JobID:         "job-kw-ct-1",
		Topic:         "job.default",
		ContentType:   "application/json",
		OutputContent: []byte(`{"user":"admin","password":"hunter2"}`),
	})
	if err != nil {
		t.Fatalf("evaluate output: %v", err)
	}
	if resp.Decision != "redact" {
		t.Fatalf("expected redact, got %q", resp.Decision)
	}

	// Should NOT match when content type differs
	resp, err = srv.EvaluateOutput(context.Background(), &OutputEvaluateRequest{
		JobID:         "job-kw-ct-2",
		Topic:         "job.default",
		ContentType:   "text/plain",
		OutputContent: []byte(`{"user":"admin","password":"hunter2"}`),
	})
	if err != nil {
		t.Fatalf("evaluate output: %v", err)
	}
	if resp.Decision != "allow" {
		t.Fatalf("expected allow when content type mismatch, got %q", resp.Decision)
	}
}

func TestValidateRegexComplexity(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		wantErr bool
	}{
		{"simple pattern accepted", `AKIA[0-9A-Z]{16}`, false},
		{"email pattern accepted", `[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`, false},
		{"nested quantifier (.*)*", `(.*)*`, true},
		{"nested quantifier (a+)+", `(a+)+`, true},
		{"nested quantifier (.+)?", `(.+)?`, true},
		{"nested quantifier with brace (.+){2,}", `(.+){2,}`, true},
		{"too many alternations", `a|b|c|d|e|f|g`, true},
		{"five alternations ok", `a|b|c|d|e`, false},
		{"pattern too long", string(make([]byte, 300)), true},
		{"normal length ok", `[A-Z]{5,10}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRegexComplexity(tt.pattern)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateRegexComplexity(%q) error=%v, wantErr=%v", tt.pattern, err, tt.wantErr)
			}
		})
	}
}

func TestCompileOutputRulesRejectsComplexPatterns(t *testing.T) {
	policy := &config.SafetyPolicy{
		OutputRules: []config.OutputPolicyRule{
			{
				ID:       "regex-rule",
				Decision: "quarantine",
				Match: config.OutputPolicyMatch{
					Topics:          []string{"job.*"},
					ContentPatterns: []string{`(.*)*`}, // nested quantifier
				},
			},
		},
	}
	rules := compileOutputRules(policy)
	// Rule should be skipped because all patterns are rejected
	if len(rules) != 0 {
		t.Fatalf("expected nested quantifier pattern to be rejected, got %d rules", len(rules))
	}
}

func TestCompileOutputRulesAcceptsSimplePattern(t *testing.T) {
	policy := &config.SafetyPolicy{
		OutputRules: []config.OutputPolicyRule{
			{
				ID:       "simple-rule",
				Decision: "quarantine",
				Match: config.OutputPolicyMatch{
					Topics:          []string{"job.*"},
					ContentPatterns: []string{`AKIA[0-9A-Z]{16}`},
				},
			},
		},
	}
	rules := compileOutputRules(policy)
	if len(rules) != 1 {
		t.Fatalf("expected simple pattern to be accepted, got %d rules", len(rules))
	}
	if len(rules[0].patterns) != 1 {
		t.Fatalf("expected 1 compiled pattern, got %d", len(rules[0].patterns))
	}
}

func TestContentTruncationFinding(t *testing.T) {
	srv := &server{
		scanners: defaultOutputScanners(),
	}
	_ = srv.setPolicy(context.Background(), &config.SafetyPolicy{
		OutputPolicy: config.OutputPolicyConfig{Enabled: true, FailMode: "open"},
		OutputRules: []config.OutputPolicyRule{
			{
				ID:       "out-secret-trunc",
				Decision: "quarantine",
				Reason:   "secret detected",
				Match: config.OutputPolicyMatch{
					Topics:   []string{"job.*"},
					Scanners: []string{"secret"},
				},
			},
		},
	}, "snap-trunc")

	// Create content larger than maxOutputScanBytes with a secret at the start
	bigContent := make([]byte, maxOutputScanBytes+1024)
	copy(bigContent, []byte("AKIA1234567890ABCDEF "))
	for i := len("AKIA1234567890ABCDEF "); i < len(bigContent); i++ {
		bigContent[i] = 'A'
	}

	resp, err := srv.CheckOutput(context.Background(), &pb.OutputCheckRequest{
		JobId:         "job-trunc",
		Topic:         "job.default",
		Tenant:        "default",
		OutputContent: bigContent,
	})
	if err != nil {
		t.Fatalf("check output: %v", err)
	}
	if resp.GetDecision() != pb.OutputDecision_OUTPUT_DECISION_QUARANTINE {
		t.Fatalf("expected quarantine, got %v", resp.GetDecision())
	}
	// Should include a content_truncated finding
	found := false
	for _, f := range resp.GetFindings() {
		if f.GetType() == "content_truncated" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected content_truncated finding in response")
	}
}

func TestTruncateOutputContentBelowLimit(t *testing.T) {
	small := []byte("hello")
	out, truncated := truncateOutputContent(small)
	if truncated {
		t.Fatalf("expected no truncation for small content")
	}
	if string(out) != "hello" {
		t.Fatalf("content should be unchanged")
	}
}

func TestTruncateOutputContentAboveLimit(t *testing.T) {
	big := make([]byte, maxOutputScanBytes+100)
	out, truncated := truncateOutputContent(big)
	if !truncated {
		t.Fatalf("expected truncation for oversized content")
	}
	if len(out) != maxOutputScanBytes {
		t.Fatalf("expected %d bytes, got %d", maxOutputScanBytes, len(out))
	}
}

func BenchmarkCheckOutputFastPath(b *testing.B) {
	srv := &server{
		scanners: defaultOutputScanners(),
	}
	_ = srv.setPolicy(context.Background(), &config.SafetyPolicy{
		OutputPolicy: config.OutputPolicyConfig{Enabled: true, FailMode: "open"},
		OutputRules: []config.OutputPolicyRule{
			{
				ID:       "out-safe",
				Decision: "quarantine",
				Match: config.OutputPolicyMatch{
					Topics:   []string{"job.*"},
					Scanners: []string{"secret", "pii", "injection"},
				},
			},
		},
	}, "snap-bench")

	req := &pb.OutputCheckRequest{
		JobId:         "job-bench",
		Topic:         "job.default",
		OutputContent: []byte("safe content that should not trigger scanners"),
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := srv.CheckOutput(context.Background(), req)
		if err != nil {
			b.Fatalf("check output: %v", err)
		}
		if resp.GetDecision() != pb.OutputDecision_OUTPUT_DECISION_ALLOW {
			b.Fatalf("expected allow, got %v", resp.GetDecision())
		}
	}
}

func TestContentForScanNilResultClient(t *testing.T) {
	// Server with nil resultClient — simulates deployment without Redis result store.
	srv := &server{
		resultClient: nil,
	}

	// Request with a result pointer but no inline content.
	req := &pb.OutputCheckRequest{
		ResultPtr:    "redis://result:job-123",
		ErrorMessage: "fallback error msg",
	}

	content, truncated := srv.contentForScan(context.Background(), req)
	if truncated {
		t.Fatal("expected no truncation for short error message")
	}
	// Should fall back to error message when resultClient is nil.
	if string(content) != "fallback error msg" {
		t.Fatalf("expected fallback to error message, got %q", string(content))
	}

	// Request with neither content nor error message.
	req2 := &pb.OutputCheckRequest{
		ResultPtr: "redis://result:job-456",
	}
	content2, _ := srv.contentForScan(context.Background(), req2)
	if content2 != nil {
		t.Fatalf("expected nil content when no fallback available, got %q", string(content2))
	}
}
