package safetykernel

import (
	"encoding/json"
	"testing"

	"github.com/cordum/cordum/core/policy"
	"github.com/stretchr/testify/require"
)

func TestRuleToCompiledInput(t *testing.T) {
	rule := policy.Rule{
		ID:    "input-secret",
		Type:  policy.RuleTypeInput,
		Scope: policy.RuleScope{Kind: policy.RuleScopeTenant, Value: "tenant-acme"},
		Match: json.RawMessage(`{
			"tenants":["tenant-acme"],
			"topics":["job.acme.*"],
			"capabilities":["llm.request"],
			"risk_tags":["secret"],
			"content_types":["text/plain"],
			"keywords":["OPENAI_API_KEY"],
			"input_size_gt":128
		}`),
		Decide: json.RawMessage(`{
			"decision":"deny",
			"reason":"secret in prompt",
			"severity":"critical"
		}`),
	}

	compiled, err := RuleToCompiledInput(rule)
	require.NoError(t, err)
	require.Equal(t, "input-secret", compiled.id)
	require.Equal(t, "deny", compiled.decision)
	require.Equal(t, "secret in prompt", compiled.reason)
	require.Equal(t, "critical", compiled.severity)
	require.Equal(t, []string{"tenant-acme"}, compiled.tenants)
	require.Equal(t, []string{"job.acme.*"}, compiled.topics)
	require.Equal(t, []string{"llm.request"}, compiled.capabilities)
	require.Equal(t, []string{"secret"}, compiled.riskTags)
	require.Equal(t, []string{"text/plain"}, compiled.contentTypes)
	require.Equal(t, []string{"OPENAI_API_KEY"}, compiled.keywords)
	require.Equal(t, int64(128), compiled.maxBytes)
	require.True(t, RuleScopeMatchesJob(rule.Scope, JobContext{Tenant: "tenant-acme"}))
}

func TestRuleToCompiledOutput(t *testing.T) {
	hasError := false
	rule := policy.Rule{
		ID:    "output-pii",
		Type:  policy.RuleTypeOutput,
		Scope: policy.RuleScope{Kind: policy.RuleScopeGlobal},
		Match: json.RawMessage(`{
			"detectors":["pii"],
			"content_types":["application/json"],
			"output_size_gt":256,
			"has_error":false
		}`),
		Decide: json.RawMessage(`{
			"decision":"redact",
			"reason":"pii in output",
			"severity":"high"
		}`),
	}

	compiled, err := RuleToCompiledOutput(rule)
	require.NoError(t, err)
	require.Equal(t, "output-pii", compiled.id)
	require.Equal(t, "pii in output", compiled.reason)
	require.Equal(t, "high", compiled.severity)
	require.Equal(t, []string{"pii"}, compiled.scanners)
	require.Equal(t, []string{"application/json"}, compiled.contentTypes)
	require.Equal(t, int64(256), compiled.maxOutputBytes)
	require.NotNil(t, compiled.hasError)
	require.Equal(t, hasError, *compiled.hasError)
	require.Equal(t, "redact", outputDecisionString(compiled.decision))
}

func TestRuleToCompiledVelocity(t *testing.T) {
	rule := policy.Rule{
		ID:    "velocity-session",
		Type:  policy.RuleTypeVelocity,
		Scope: policy.RuleScope{Kind: policy.RuleScopeWorkflow, Value: "wf-claims"},
		Match: json.RawMessage(`{
			"tenants":["tenant-acme"],
			"topics":["job.claims.*"],
			"labels":{"actor_type":"service"}
		}`),
		Decide: json.RawMessage(`{
			"decision":"throttle",
			"reason":"session rate limit",
			"velocity":{"max_requests":3,"window_seconds":60,"key":"labels.session_id"}
		}`),
	}

	legacy, err := RuleToCompiledVelocity(rule)
	require.NoError(t, err)
	require.Equal(t, "velocity-session", legacy.ID)
	require.Equal(t, "throttle", legacy.Decision)
	require.Equal(t, "session rate limit", legacy.Reason)
	require.Equal(t, []string{"tenant-acme"}, legacy.Match.Tenants)
	require.Equal(t, []string{"job.claims.*"}, legacy.Match.Topics)
	require.Equal(t, map[string]string{"actor_type": "service"}, legacy.Match.Labels)
	require.NotNil(t, legacy.Velocity)
	require.Equal(t, 3, legacy.Velocity.MaxRequests)
	require.Equal(t, 60, legacy.Velocity.WindowSeconds)
	require.Equal(t, "labels.session_id", legacy.Velocity.Key)
}

func TestRuleToCompiledRejectsBadBoundaryInputs(t *testing.T) {
	cases := []struct {
		name    string
		fn      func(policy.Rule) error
		rule    policy.Rule
		wantErr string
	}{
		{
			name:    "input type mismatch",
			fn:      func(r policy.Rule) error { _, err := RuleToCompiledInput(r); return err },
			rule:    policy.Rule{ID: "r1", Type: policy.RuleTypeOutput, Match: json.RawMessage(`{}`), Decide: json.RawMessage(`{"decision":"deny"}`)},
			wantErr: "rule type mismatch",
		},
		{
			name:    "output missing match",
			fn:      func(r policy.Rule) error { _, err := RuleToCompiledOutput(r); return err },
			rule:    policy.Rule{ID: "r2", Type: policy.RuleTypeOutput, Decide: json.RawMessage(`{"decision":"redact"}`)},
			wantErr: "missing rule match",
		},
		{
			name:    "velocity corrupt match",
			fn:      func(r policy.Rule) error { _, err := RuleToCompiledVelocity(r); return err },
			rule:    policy.Rule{ID: "r3", Type: policy.RuleTypeVelocity, Match: json.RawMessage(`{"topics":`), Decide: json.RawMessage(`{"decision":"throttle"}`)},
			wantErr: "decode rule match",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.fn(tc.rule)
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestRuleScopeMatchesJobAcrossKinds(t *testing.T) {
	job := JobContext{Tenant: "tenant-acme", WorkflowID: "wf-acme", JobID: "job-1"}
	cases := []struct {
		name  string
		scope policy.RuleScope
		want  bool
	}{
		{"empty kind allows", policy.RuleScope{}, true},
		{"global allows", policy.RuleScope{Kind: policy.RuleScopeGlobal}, true},
		{"tenant match", policy.RuleScope{Kind: policy.RuleScopeTenant, Value: "tenant-acme"}, true},
		{"tenant mismatch", policy.RuleScope{Kind: policy.RuleScopeTenant, Value: "tenant-other"}, false},
		{"tenant empty value", policy.RuleScope{Kind: policy.RuleScopeTenant, Value: ""}, false},
		{"workflow match", policy.RuleScope{Kind: policy.RuleScopeWorkflow, Value: "wf-acme"}, true},
		{"workflow mismatch", policy.RuleScope{Kind: policy.RuleScopeWorkflow, Value: "wf-other"}, false},
		{"workflow empty value", policy.RuleScope{Kind: policy.RuleScopeWorkflow, Value: ""}, false},
		{"edge fleet rejected", policy.RuleScope{Kind: policy.RuleScopeEdgeFleet, Value: "fleet-1"}, false},
		{"edge user rejected", policy.RuleScope{Kind: policy.RuleScopeEdgeUser, Value: "user-1"}, false},
		{"unknown kind rejected", policy.RuleScope{Kind: policy.RuleScopeKind("unsupported"), Value: "x"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, RuleScopeMatchesJob(tc.scope, job))
		})
	}
}

func TestNormalizeInputDecisionTable(t *testing.T) {
	cases := []struct {
		raw     string
		want    string
		wantOK  bool
	}{
		{"deny", "deny", true},
		{" Deny ", "deny", true},
		{"require_approval", "require_approval", true},
		{"require-approval", "require_approval", true},
		{"require_human", "require_approval", true},
		{"throttle", "", false},
		{"", "", false},
		{"garbage", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			got, ok := normalizeInputDecision(tc.raw)
			require.Equal(t, tc.want, got)
			require.Equal(t, tc.wantOK, ok)
		})
	}
}

func TestNormalizeVelocityDecisionTable(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"throttle", "throttle"},
		{"deny", "deny"},
		{"block", "deny"},
		{"require_approval", "require_approval"},
		{"require-approval", "require_approval"},
		{"require_human", "require_approval"},
		{"allow_with_constraints", "allow_with_constraints"},
		{"allow-with-constraints", "allow_with_constraints"},
		{"", "throttle"},
		{"garbage", "throttle"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			require.Equal(t, tc.want, normalizeVelocityDecision(tc.raw))
		})
	}
}

func TestCompilePolicyPatternsRejectsRedos(t *testing.T) {
	_, err := compilePolicyPatterns("rule-redos", []string{"(.*)+"})
	require.ErrorContains(t, err, "rule-redos")
}

func TestCompilePolicyPatternsRejectsInvalidRegex(t *testing.T) {
	_, err := compilePolicyPatterns("rule-bad", []string{"["})
	require.ErrorContains(t, err, "rule-bad")
}

func TestRuleToCompiledInputRejectsCorruptDecideAndUnsupportedDecision(t *testing.T) {
	corrupt := policy.Rule{
		ID:     "input-corrupt-decide",
		Type:   policy.RuleTypeInput,
		Match:  json.RawMessage(`{}`),
		Decide: json.RawMessage(`{"decision":`),
	}
	_, err := RuleToCompiledInput(corrupt)
	require.ErrorContains(t, err, "decode rule decision")

	unsupported := policy.Rule{
		ID:     "input-unsupported",
		Type:   policy.RuleTypeInput,
		Match:  json.RawMessage(`{}`),
		Decide: json.RawMessage(`{"decision":"throttle"}`),
	}
	_, err = RuleToCompiledInput(unsupported)
	require.ErrorContains(t, err, "unsupported input decision")
}

func TestRuleToCompiledOutputRejectsCorruptDecideAndUnsupportedDecision(t *testing.T) {
	missingDecide := policy.Rule{
		ID:    "output-missing-decide",
		Type:  policy.RuleTypeOutput,
		Match: json.RawMessage(`{}`),
	}
	_, err := RuleToCompiledOutput(missingDecide)
	require.ErrorContains(t, err, "missing rule decision")

	unsupported := policy.Rule{
		ID:     "output-unsupported",
		Type:   policy.RuleTypeOutput,
		Match:  json.RawMessage(`{}`),
		Decide: json.RawMessage(`{"decision":"throttle"}`),
	}
	_, err = RuleToCompiledOutput(unsupported)
	require.ErrorContains(t, err, "unsupported output decision")
}

func TestRuleToCompiledVelocityRejectsMissingAndInvalidVelocity(t *testing.T) {
	missingVelocity := policy.Rule{
		ID:     "vel-missing",
		Type:   policy.RuleTypeVelocity,
		Match:  json.RawMessage(`{}`),
		Decide: json.RawMessage(`{"decision":"throttle"}`),
	}
	_, err := RuleToCompiledVelocity(missingVelocity)
	require.ErrorContains(t, err, "missing velocity config")

	invalidVelocity := policy.Rule{
		ID:     "vel-invalid",
		Type:   policy.RuleTypeVelocity,
		Match:  json.RawMessage(`{}`),
		Decide: json.RawMessage(`{"decision":"throttle","velocity":{"max_requests":0,"window_seconds":60,"key":"k"}}`),
	}
	_, err = RuleToCompiledVelocity(invalidVelocity)
	require.Error(t, err)

	corruptDecide := policy.Rule{
		ID:     "vel-corrupt-decide",
		Type:   policy.RuleTypeVelocity,
		Match:  json.RawMessage(`{}`),
		Decide: json.RawMessage(`{"decision":`),
	}
	_, err = RuleToCompiledVelocity(corruptDecide)
	require.ErrorContains(t, err, "decode rule decision")
}

func TestRuleToCompiledOutputRejectsBadPattern(t *testing.T) {
	rule := policy.Rule{
		ID:     "output-bad-pattern",
		Type:   policy.RuleTypeOutput,
		Match:  json.RawMessage(`{"content_patterns":["["]}`),
		Decide: json.RawMessage(`{"decision":"redact"}`),
	}
	_, err := RuleToCompiledOutput(rule)
	require.ErrorContains(t, err, "output-bad-pattern")
}

func TestCompilePolicyPatternsCompilesValid(t *testing.T) {
	got, err := compilePolicyPatterns("rule-ok", []string{"  ", "secret-\\d+"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "secret-\\d+", got[0].raw)
	require.True(t, got[0].re.MatchString("secret-12"))
}
