package safetykernel

import (
	"testing"

	"github.com/cordum/cordum/core/infra/config"
)

// TestShouldDowngradeDenyToRequireHuman_FalsePositiveScenarios exercises
// the 5 false-positive scenarios from task-96f931fe DoD #4 + architect
// amendment comment-79a9e609 §(3): each prompt-only rule that authored
// a "deny" verdict must downgrade to REQUIRE_HUMAN when the threshold
// declares "truly ambiguous" routing.
//
// The 5 scenarios from the amendment (verbatim):
//   - defensive `/etc/passwd` mention with `act == nil` (prompt-only)
//   - `rm -rf` mention with `act == nil`
//   - API-key rotation procedure with no key-value in content
//   - approval-token logging in docs (no token value)
//   - 169.254.169.254 mention with `act == nil` (no outbound URL action)
//
// All 5 share the structural property: the matched finding has medium
// severity, low-to-medium confidence, and no ActionDescriptor — meeting
// the architect's "medium-severity OR low-confidence OR prompt-only"
// downgrade trigger.
func TestShouldDowngradeDenyToRequireHuman_FalsePositiveScenarios(t *testing.T) {
	t.Parallel()

	// Threshold posture an operator would set after adopting REQUIRE_HUMAN
	// routing: high-severity high-confidence denies stay DENY; everything
	// else routes to human review. Matches architect amendment §(2).
	threshold := config.RequireHumanThreshold{
		MinSeverityForDeny:      "high",
		MinConfidenceForDeny:    0.8,
		DowngradeWhenPromptOnly: true,
	}

	cases := []struct {
		name     string
		rule     compiledInputRule
		findings []outputFinding
		// action == nil represents prompt-only (no ActionDescriptor).
		// Per amendment §(2), all 5 FP scenarios are prompt-only.
		want bool
	}{
		{
			name: "defensive /etc/passwd mention — prompt-only education content",
			rule: compiledInputRule{
				id:       "scanner_secret_path_leak",
				decision: "deny",
				severity: "medium",
			},
			findings: []outputFinding{{
				Type:       "secret_path_mention",
				Severity:   "medium",
				Detail:     "matched defensive /etc/passwd reference in security runbook",
				Scanner:    "regex",
				Confidence: 0.6,
			}},
			want: true,
		},
		{
			name: "rm -rf mention in defensive runbook — prompt-only",
			rule: compiledInputRule{
				id:       "scanner_destructive_mention",
				decision: "deny",
				severity: "medium",
			},
			findings: []outputFinding{{
				Type:       "destructive_command_mention",
				Severity:   "medium",
				Detail:     "matched 'rm -rf' in non-executed context",
				Scanner:    "regex",
				Confidence: 0.55,
			}},
			want: true,
		},
		{
			name: "API-key rotation procedure with no key value",
			rule: compiledInputRule{
				id:       "scanner_api_key_rotation_docs",
				decision: "deny",
				severity: "medium",
			},
			findings: []outputFinding{{
				Type:       "api_key_rotation_mention",
				Severity:   "medium",
				Detail:     "matched API-key-rotation prose without a key value",
				Scanner:    "regex",
				Confidence: 0.7,
			}},
			want: true,
		},
		{
			name: "approval-token logging in compliance docs — no token value",
			rule: compiledInputRule{
				id:       "scanner_approval_token_log",
				decision: "deny",
				severity: "medium",
			},
			findings: []outputFinding{{
				Type:       "approval_token_logging_mention",
				Severity:   "medium",
				Detail:     "matched approval-token-logging prose in compliance doc",
				Scanner:    "regex",
				Confidence: 0.65,
			}},
			want: true,
		},
		{
			name: "metadata-service 169.254.169.254 education — no outbound URL action",
			rule: compiledInputRule{
				id:       "url_metadata_education",
				decision: "deny",
				severity: "medium",
			},
			findings: []outputFinding{{
				Type:       "metadata_service_mention",
				Severity:   "medium",
				Detail:     "matched 169.254.169.254 in security-architecture context",
				Scanner:    "regex",
				Confidence: 0.7,
			}},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := shouldDowngradeDenyToRequireHuman(tc.rule, tc.findings, nil /* prompt-only */, threshold)
			if got != tc.want {
				// Per Yaron senior-QA directive msg-82043ff6 + governor msg-c42abf9c §4:
				// mutation-resistant assertEquals on the explicit boolean
				// outcome — never a bare assertNotEquals.
				t.Fatalf("shouldDowngradeDenyToRequireHuman(rule=%q, findings=%d, action=nil, threshold=%+v) = %v, want %v",
					tc.rule.id, len(tc.findings), threshold, got, tc.want)
			}
		})
	}
}

// TestShouldDowngradeDenyToRequireHuman_HighSeverityActionBoundStaysDeny
// guards the contract that high-severity + high-confidence + action-bound
// denials are NOT downgraded — per architect amendment §(2) those stay DENY
// (the "unchanged from today" branch). DoD #6 requires that action-layer
// misses are not masked.
func TestShouldDowngradeDenyToRequireHuman_HighSeverityActionBoundStaysDeny(t *testing.T) {
	t.Parallel()

	threshold := config.RequireHumanThreshold{
		MinSeverityForDeny:      "high",
		MinConfidenceForDeny:    0.8,
		DowngradeWhenPromptOnly: true,
	}

	action := &config.ActionDescriptor{
		Kind: "file_write",
		TargetResource: &config.ActionTargetResource{
			Type: "file",
		},
	}
	rule := compiledInputRule{id: "scanner_secret_path_leak", decision: "deny", severity: "high"}
	findings := []outputFinding{{
		Type:       "secret_path_leak",
		Severity:   "high",
		Detail:     "matched a credential path with high-confidence regex on an action-bound write",
		Scanner:    "regex",
		Confidence: 0.95,
	}}

	got := shouldDowngradeDenyToRequireHuman(rule, findings, action, threshold)
	if got {
		t.Fatalf("high-severity (0.95) action-bound deny was downgraded; want stay-DENY (got=%v)", got)
	}
}

// TestShouldDowngradeDenyToRequireHuman_ZeroThresholdPreservesLegacy
// guards backward compatibility: zero-value threshold (operator hasn't
// opted in) must not downgrade ANY rule. This preserves existing DENY
// behavior for callers that have not migrated to the REQUIRE_HUMAN
// posture.
func TestShouldDowngradeDenyToRequireHuman_ZeroThresholdPreservesLegacy(t *testing.T) {
	t.Parallel()

	zero := config.RequireHumanThreshold{}
	rule := compiledInputRule{id: "scanner_secret_path_leak", decision: "deny", severity: "low"}
	findings := []outputFinding{{
		Type:       "secret_path_mention",
		Severity:   "low",
		Detail:     "would be ambiguous if threshold were configured",
		Scanner:    "regex",
		Confidence: 0.1,
	}}

	got := shouldDowngradeDenyToRequireHuman(rule, findings, nil, zero)
	if got {
		t.Fatalf("zero-value threshold downgraded a deny; want legacy DENY preservation (got=%v)", got)
	}
}

// TestShouldDowngradeDenyToRequireHuman_RuleSeverityFloor exercises the
// rule.severity check independent of finding severity. A rule that the
// operator authored as "low" tier should downgrade even when individual
// findings synthesize higher severities — operator intent dominates.
func TestShouldDowngradeDenyToRequireHuman_RuleSeverityFloor(t *testing.T) {
	t.Parallel()

	threshold := config.RequireHumanThreshold{MinSeverityForDeny: "high"}
	rule := compiledInputRule{id: "scanner_x", decision: "deny", severity: "low"}
	findings := []outputFinding{{
		Type:       "x",
		Severity:   "high", // finding alone would not trigger downgrade
		Detail:     "synthesized higher severity",
		Scanner:    "regex",
		Confidence: 0.9,
	}}

	got := shouldDowngradeDenyToRequireHuman(rule, findings, nil, threshold)
	if !got {
		t.Fatalf("low-tier rule with high-severity finding was not downgraded; rule.severity authored intent should dominate (got=%v)", got)
	}
}

// TestSeverityRank validates the severity-string → ordinal mapping. The
// function is consulted by shouldDowngradeDenyToRequireHuman and any drift
// in the mapping would silently break threshold comparisons.
func TestSeverityRank(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want int
	}{
		{"low", 1},
		{"medium", 2},
		{"high", 3},
		{"critical", 4},
		{"LOW", 1},  // case-insensitive
		{"High", 3}, // case-insensitive
		{"  medium  ", 2},
		{"", 0},
		{"unknown", 0},
	}

	for _, tc := range cases {
		got := severityRank(tc.in)
		if got != tc.want {
			t.Errorf("severityRank(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
