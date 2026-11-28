package safetykernel

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// joinLines joins n generated values, one per line.
func joinLines(n int, gen func(int) string) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = gen(i)
	}
	return strings.Join(parts, "\n")
}

func satKeyID(i int) string { return fmt.Sprintf("AKIA%016d", i) }

func satEmail(i int) string { return fmt.Sprintf("user%d@example.com", i) }

func satSecret(i int) string { return fmt.Sprintf("synthetic-saturation-%03d", i) }

func satCredential(i int) string { return fmt.Sprintf("password=%q", satSecret(i)) }

// satCard returns a distinct 16-digit number that passes the Luhn check when
// valid is set and fails it otherwise.
func satCard(i int, valid bool) string {
	base := fmt.Sprintf("4%014d", i)
	for d := 0; d <= 9; d++ {
		if n := base + strconv.Itoa(d); luhnValid(n) == valid {
			return n
		}
	}
	return ""
}

func hasFindingType(findings []outputFinding, want string) bool {
	for _, f := range findings {
		if f.Type == want {
			return true
		}
	}
	return false
}

func TestScanSaturation(t *testing.T) {
	keyword := newKeywordScanner([]string{"needle"})
	invalidCards := joinLines(11, func(i int) string { return satCard(i, false) })
	cases := []struct {
		name      string
		scanner   OutputScanner
		content   string
		capped    int
		saturated bool
		uncapped  int
	}{
		{"regex at pattern cap", sharedSecretScanner(), joinLines(10, satKeyID), 10, false, 10},
		{"regex above pattern cap", sharedSecretScanner(), joinLines(11, satKeyID), 10, true, 11},
		{"pii at pattern cap", sharedPIIScanner(), joinLines(10, satEmail), 10, false, 10},
		{"pii above pattern cap", sharedPIIScanner(), joinLines(11, satEmail), 10, true, 11},
		{"pii card past invalid candidates", sharedPIIScanner(), invalidCards + "\n" + satCard(99, true), 1, false, 1},
		{"keyword at scanner cap", keyword, strings.Repeat("needle ", 32), 32, false, 32},
		{"keyword above scanner cap", keyword, strings.Repeat("needle ", 33), 32, true, 33},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := []byte(tc.content)
			counter := scanSaturatedTotal.WithLabelValues(tc.scanner.Name())
			before := testutil.ToFloat64(counter)
			res := runScan(tc.scanner, content, true)
			if len(res.Findings) != tc.capped || res.Saturated != tc.saturated {
				t.Fatalf("capped scan = %d findings, saturated=%v; want %d, %v",
					len(res.Findings), res.Saturated, tc.capped, tc.saturated)
			}
			wantDelta := 0.0
			if tc.saturated {
				wantDelta = 1
			}
			if delta := testutil.ToFloat64(counter) - before; delta != wantDelta {
				t.Fatalf("saturation counter delta = %v, want %v", delta, wantDelta)
			}
			if got := len(tc.scanner.Scan(content)); got != tc.capped {
				t.Fatalf("Scan = %d findings, want the capped %d", got, tc.capped)
			}
			uncapped := runScan(tc.scanner, content, false)
			if len(uncapped.Findings) != tc.uncapped || uncapped.Saturated {
				t.Fatalf("uncapped scan = %d findings, saturated=%v; want %d, false",
					len(uncapped.Findings), uncapped.Saturated, tc.uncapped)
			}
		})
	}
}

func TestScanWithScannersCombinedCap(t *testing.T) {
	scanners := map[string]OutputScanner{
		"a": newKeywordScanner([]string{"alpha"}),
		"b": newKeywordScanner([]string{"beta"}),
	}
	for _, tc := range []struct {
		each      int
		saturated bool
	}{{16, false}, {17, true}} {
		t.Run(fmt.Sprintf("%d_each", tc.each), func(t *testing.T) {
			counter := scanSaturatedTotal.WithLabelValues("combined")
			before := testutil.ToFloat64(counter)
			content := strings.Repeat("alpha ", tc.each) + strings.Repeat("beta ", tc.each)
			findings := scanWithScanners([]byte(content), []string{"a", "b"}, scanners)
			if got := hasFindingType(findings, scanSaturatedType); got != tc.saturated {
				t.Fatalf("scan_saturated finding = %v, want %v (findings %d)", got, tc.saturated, len(findings))
			}
			wantDelta := 0.0
			if tc.saturated {
				wantDelta = 1
			}
			if delta := testutil.ToFloat64(counter) - before; delta != wantDelta {
				t.Fatalf("combined counter delta = %v, want %v", delta, wantDelta)
			}
		})
	}
}

// releasedOutput mirrors the scheduler: QUARANTINE releases nothing, REDACT
// masks the finding spans, ALLOW releases the content as is.
func releasedOutput(content []byte, resp *pb.OutputCheckResponse) string {
	switch resp.GetDecision() {
	case pb.OutputDecision_OUTPUT_DECISION_QUARANTINE:
		return ""
	case pb.OutputDecision_OUTPUT_DECISION_REDACT:
		out := append([]byte(nil), content...)
		for _, f := range resp.GetFindings() {
			for i := f.GetOffset(); i < f.GetOffset()+f.GetLength() && i < int64(len(out)); i++ {
				out[i] = '*'
			}
		}
		return string(out)
	}
	return string(content)
}

func TestOutputSaturationEscalatesRedact(t *testing.T) {
	cases := []struct {
		name     string
		decision string
		match    config.OutputPolicyMatch
		secrets  int
		want     pb.OutputDecision
	}{
		{"scanner saturated", "redact", config.OutputPolicyMatch{Scanners: []string{"secret"}}, 100, pb.OutputDecision_OUTPUT_DECISION_QUARANTINE},
		{"scanner under cap", "redact", config.OutputPolicyMatch{Scanners: []string{"secret"}}, 5, pb.OutputDecision_OUTPUT_DECISION_REDACT},
		{"content pattern saturated", "redact", config.OutputPolicyMatch{ContentPatterns: []string{`synthetic-saturation-\d+`}}, 100, pb.OutputDecision_OUTPUT_DECISION_QUARANTINE},
		{"keyword saturated", "redact", config.OutputPolicyMatch{Keywords: []string{"password"}}, 100, pb.OutputDecision_OUTPUT_DECISION_QUARANTINE},
		{"allow rule unchanged", "allow", config.OutputPolicyMatch{Scanners: []string{"secret"}}, 100, pb.OutputDecision_OUTPUT_DECISION_ALLOW},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.match.Topics = []string{"job.*"}
			srv := &server{scanners: defaultOutputScanners()}
			if err := srv.setPolicy(context.Background(), &config.SafetyPolicy{
				OutputPolicy: config.OutputPolicyConfig{Enabled: true, FailMode: "open"},
				OutputRules:  []config.OutputPolicyRule{{ID: "out-sat", Decision: tc.decision, Match: tc.match}},
			}, "snap-sat"); err != nil {
				t.Fatalf("set policy: %v", err)
			}
			content := []byte("begin output\n" + joinLines(tc.secrets, satCredential) + "\nend output")

			resp, err := srv.CheckOutput(context.Background(), &pb.OutputCheckRequest{
				JobId: "job-sat", Topic: "job.default", Tenant: "default", OutputContent: content,
			})
			if err != nil {
				t.Fatalf("check output: %v", err)
			}
			if resp.GetDecision() != tc.want {
				t.Fatalf("CheckOutput decision = %v, want %v (reason %q)", resp.GetDecision(), tc.want, resp.GetReason())
			}
			if tc.decision == "redact" {
				released := releasedOutput(content, resp)
				for i := 0; i < tc.secrets; i++ {
					if strings.Contains(released, satSecret(i)) {
						t.Fatalf("released output contains seeded secret %d", i)
					}
				}
			}

			eval, err := srv.EvaluateOutput(context.Background(), &OutputEvaluateRequest{
				JobID: "job-sat", Topic: "job.default", Tenant: "default", OutputContent: content,
			})
			if err != nil {
				t.Fatalf("evaluate output: %v", err)
			}
			if eval.Decision != outputDecisionString(tc.want) {
				t.Fatalf("EvaluateOutput decision = %q, want %q", eval.Decision, outputDecisionString(tc.want))
			}
		})
	}
}

func TestInputKeywordSaturationFailsClosed(t *testing.T) {
	rule := compiledInputRule{id: "in-sat", decision: "deny", severity: "low", keywords: []string{"needle"}}
	matched, findings := evaluateInputRule(rule, inputEvaluateRequest{
		topic: "job.default", content: []byte(strings.Repeat("needle ", 33)),
	}, nil)
	if !matched {
		t.Fatal("deny input rule with a saturated keyword scan must fire")
	}
	for _, f := range findings {
		if f.Type == scanSaturatedType && f.Severity == "high" {
			return
		}
	}
	t.Fatalf("want a high-severity scan_saturated finding, got %#v", findings)
}

func TestRedactContentMasksEveryMatch(t *testing.T) {
	for _, tc := range []struct {
		name string
		line func(int) string
		seed func(int) string
		opts ContentScanOptions
	}{
		{"secret", satCredential, satSecret, ContentScanOptions{IncludeSecrets: true, IncludePII: true}},
		{"pii", satEmail, satEmail, ContentScanOptions{IncludePII: true}},
		{"keyword", satSecret, satSecret, ContentScanOptions{Keywords: []string{"synthetic-saturation"}}},
		{"custom", satSecret, satSecret, ContentScanOptions{ExtraPatterns: []NamedContentPattern{
			{Name: "synthetic", Pattern: regexp.MustCompile(`synthetic-saturation-\d+`)},
		}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := "begin fixture\n" + joinLines(100, tc.line) + "\nend fixture"
			redacted, findings := RedactContent([]byte(content), tc.opts)
			if len(findings) != 100 {
				t.Fatalf("findings = %d, want 100", len(findings))
			}
			for i := 0; i < 100; i++ {
				if strings.Contains(redacted, tc.seed(i)) {
					t.Fatalf("redacted content contains seeded match %d", i)
				}
			}
			if !strings.HasPrefix(redacted, "begin fixture\n") || !strings.HasSuffix(redacted, "\nend fixture") {
				t.Fatal("redaction must keep the surrounding text")
			}
		})
	}
}

func TestScanSaturationAcrossPatterns(t *testing.T) {
	var patterns []regexPattern
	for _, pattern := range []string{"alpha", "beta", "gamma", "delta"} {
		patterns = append(patterns, regexPattern{Expression: regexp.MustCompile(pattern)})
	}
	for _, tc := range []struct {
		name    string
		scanner OutputScanner
		content string
		extra   string
	}{
		{"regex", newRegexScanner("synthetic", "custom", patterns), strings.Repeat("alpha beta gamma delta\n", 8), "delta"},
		{"pii", newPIIScanner(), strings.Repeat("user@example.com\n123-45-6789\n202-555-0123\n"+satCard(99, true)+"\n", 8), satCard(99, true)},
	} {
		for _, extra := range []int{0, 1} {
			t.Run(fmt.Sprintf("%s_%d", tc.name, 32+extra), func(t *testing.T) {
				content := []byte(tc.content + strings.Repeat(tc.extra, extra))
				result := runScan(tc.scanner, content, true)
				if len(result.Findings) != 32 || result.Saturated != (extra == 1) {
					t.Fatalf("findings=%d saturated=%v, want 32 and %v", len(result.Findings), result.Saturated, extra == 1)
				}
				if uncapped := runScan(tc.scanner, content, false); len(uncapped.Findings) != 32+extra || uncapped.Saturated {
					t.Fatalf("uncapped findings=%d saturated=%v, want %d and false", len(uncapped.Findings), uncapped.Saturated, 32+extra)
				}
			})
		}
	}
}
