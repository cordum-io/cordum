package safetykernel

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/policy"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestEvaluateRuleUnifiedInputPreservesLegacyDecisionAndEmitsUnifiedDecision(t *testing.T) {
	srv := &server{
		scanners: map[string]OutputScanner{},
	}
	rule := policy.Rule{
		ID:    "unified-input-deny",
		Type:  policy.RuleTypeInput,
		Scope: policy.RuleScope{Kind: policy.RuleScopeTenant, Value: "tenant-acme"},
		Match: json.RawMessage(`{
			"tenants":["tenant-acme"],
			"topics":["job.acme.evaluate"],
			"keywords":["blocked-token"],
			"content_types":["text/plain"]
		}`),
		Decide: json.RawMessage(`{
			"decision":"deny",
			"reason":"blocked input token",
			"severity":"high"
		}`),
	}
	req := &pb.PolicyCheckRequest{
		JobId:            "job-123",
		Tenant:           "tenant-acme",
		Topic:            "job.acme.evaluate",
		InputContent:     []byte("contains blocked-token"),
		InputContentType: "text/plain",
	}

	legacy, unified, err := srv.EvaluateRule(context.Background(), rule, req)

	require.NoError(t, err)
	require.Equal(t, pb.DecisionType_DECISION_TYPE_DENY, legacy.GetDecision())
	require.Equal(t, "blocked input token", legacy.GetReason())
	require.Equal(t, "unified-input-deny", legacy.GetRuleId())
	require.Equal(t, policy.DecisionSourceJob, unified.Source)
	require.Equal(t, policy.DecisionDeny, unified.Type)
	require.Equal(t, "unified-input-deny", unified.RuleID)
	require.False(t, unified.Timestamp.IsZero())
	require.NotEmpty(t, unified.Trace)
	require.Equal(t, policy.DecisionDeny, unified.Trace[0].DecisionType)
}

func TestEvaluateRuleScopeMismatchEmitsAllow(t *testing.T) {
	srv := &server{scanners: map[string]OutputScanner{}}
	rule := policy.Rule{
		ID:     "unified-input-other-tenant",
		Type:   policy.RuleTypeInput,
		Scope:  policy.RuleScope{Kind: policy.RuleScopeTenant, Value: "tenant-other"},
		Match:  json.RawMessage(`{"keywords":["blocked-token"]}`),
		Decide: json.RawMessage(`{"decision":"deny","reason":"would deny"}`),
	}
	req := &pb.PolicyCheckRequest{
		Tenant:           "tenant-acme",
		Topic:            "job.acme.evaluate",
		InputContent:     []byte("contains blocked-token"),
		InputContentType: "text/plain",
	}

	legacy, unified, err := srv.EvaluateRule(context.Background(), rule, req)

	require.NoError(t, err)
	require.Equal(t, pb.DecisionType_DECISION_TYPE_ALLOW, legacy.GetDecision())
	require.Equal(t, "rule scope did not match job", legacy.GetReason())
	require.Equal(t, policy.DecisionAllow, unified.Type)
	require.NotEmpty(t, unified.Trace)
}

func TestEvaluateRuleNilRequestReturnsError(t *testing.T) {
	srv := &server{scanners: map[string]OutputScanner{}}
	rule := policy.Rule{ID: "r", Type: policy.RuleTypeInput}

	_, _, err := srv.EvaluateRule(context.Background(), rule, nil)

	require.ErrorContains(t, err, "missing policy check request")
}

func TestEvaluateRuleNilContextDefaults(t *testing.T) {
	srv := &server{scanners: map[string]OutputScanner{}}
	rule := policy.Rule{
		ID:     "r-nil-ctx",
		Type:   policy.RuleTypeInput,
		Match:  json.RawMessage(`{"topics":["nope"]}`),
		Decide: json.RawMessage(`{"decision":"deny","reason":"would deny"}`),
	}
	req := &pb.PolicyCheckRequest{Topic: "different"}

	//nolint:staticcheck // SA1012: deliberate nil context to exercise the default branch.
	legacy, _, err := srv.EvaluateRule(nil, rule, req)

	require.NoError(t, err)
	require.Equal(t, pb.DecisionType_DECISION_TYPE_ALLOW, legacy.GetDecision())
}

func TestEvaluateRuleUnsupportedRuleTypeReturnsError(t *testing.T) {
	srv := &server{scanners: map[string]OutputScanner{}}
	rule := policy.Rule{
		ID:     "r-unsup",
		Type:   policy.RuleType("unknown"),
		Match:  json.RawMessage(`{}`),
		Decide: json.RawMessage(`{"decision":"deny"}`),
	}
	req := &pb.PolicyCheckRequest{Topic: "t"}

	_, _, err := srv.EvaluateRule(context.Background(), rule, req)

	require.ErrorContains(t, err, "unsupported unified rule type")
}

func TestEvaluateRuleUnifiedOutputRedactsAndEmitsUnifiedDecision(t *testing.T) {
	srv := &server{scanners: map[string]OutputScanner{}}
	rule := policy.Rule{
		ID:     "unified-output-redact",
		Type:   policy.RuleTypeOutput,
		Scope:  policy.RuleScope{Kind: policy.RuleScopeGlobal},
		Match:  json.RawMessage(`{"topics":["job.acme.evaluate"],"keywords":["secret"]}`),
		Decide: json.RawMessage(`{"decision":"redact","reason":"redact secret","severity":"high"}`),
	}
	req := &pb.PolicyCheckRequest{
		JobId:            "job-out-1",
		Tenant:           "tenant-acme",
		Topic:            "job.acme.evaluate",
		InputContent:     []byte("contains a secret"),
		InputContentType: "text/plain",
	}

	legacy, unified, err := srv.EvaluateRule(context.Background(), rule, req)

	require.NoError(t, err)
	require.Equal(t, pb.DecisionType_DECISION_TYPE_DENY, legacy.GetDecision())
	require.Equal(t, "redact secret", legacy.GetReason())
	require.Equal(t, "unified-output-redact", legacy.GetRuleId())
	require.Equal(t, policy.DecisionRedact, unified.Type)
}

func TestEvaluateRuleUnifiedOutputQuarantinesAndEmitsUnifiedDecision(t *testing.T) {
	srv := &server{scanners: map[string]OutputScanner{}}
	rule := policy.Rule{
		ID:     "unified-output-quarantine",
		Type:   policy.RuleTypeOutput,
		Scope:  policy.RuleScope{Kind: policy.RuleScopeGlobal},
		Match:  json.RawMessage(`{"topics":["job.acme.evaluate"],"keywords":["q-tag"]}`),
		Decide: json.RawMessage(`{"decision":"quarantine","reason":"q-output","severity":"critical"}`),
	}
	req := &pb.PolicyCheckRequest{
		JobId:            "job-out-2",
		Tenant:           "tenant-acme",
		Topic:            "job.acme.evaluate",
		InputContent:     []byte("contains a q-tag"),
		InputContentType: "text/plain",
	}

	legacy, unified, err := srv.EvaluateRule(context.Background(), rule, req)

	require.NoError(t, err)
	require.Equal(t, pb.DecisionType_DECISION_TYPE_DENY, legacy.GetDecision())
	require.Equal(t, policy.DecisionQuarantine, unified.Type)
}

func TestEvaluateRuleUnifiedOutputDoesNotMatchEmitsAllow(t *testing.T) {
	srv := &server{scanners: map[string]OutputScanner{}}
	rule := policy.Rule{
		ID:     "unified-output-nomatch",
		Type:   policy.RuleTypeOutput,
		Scope:  policy.RuleScope{Kind: policy.RuleScopeGlobal},
		Match:  json.RawMessage(`{"topics":["job.other"],"keywords":["secret"]}`),
		Decide: json.RawMessage(`{"decision":"redact","reason":"would redact"}`),
	}
	req := &pb.PolicyCheckRequest{
		Topic:            "job.acme.evaluate",
		InputContent:     []byte("contains a secret"),
		InputContentType: "text/plain",
	}

	legacy, unified, err := srv.EvaluateRule(context.Background(), rule, req)

	require.NoError(t, err)
	require.Equal(t, pb.DecisionType_DECISION_TYPE_ALLOW, legacy.GetDecision())
	require.Equal(t, "output rule did not match", legacy.GetReason())
	require.Equal(t, policy.DecisionAllow, unified.Type)
}

func TestEvaluateRuleUnifiedVelocityThrottlesOnFourthCall(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	srv := &server{
		resultClient:    client,
		velocityChecker: newVelocityChecker(client),
	}
	rule := policy.Rule{
		ID:    "unified-velocity-throttle",
		Type:  policy.RuleTypeVelocity,
		Scope: policy.RuleScope{Kind: policy.RuleScopeGlobal},
		Match: json.RawMessage(`{"topics":["job.acme.velocity"]}`),
		Decide: json.RawMessage(`{
			"decision":"throttle",
			"reason":"velocity throttled",
			"velocity":{"max_requests":3,"window_seconds":60,"key":"labels.session_id"}
		}`),
	}

	for i := 1; i <= 3; i++ {
		req := &pb.PolicyCheckRequest{
			JobId:  fmt.Sprintf("job-%d", i),
			Topic:  "job.acme.velocity",
			Tenant: "tenant-acme",
			Labels: map[string]string{"session_id": "sess-1"},
		}
		legacy, _, err := srv.EvaluateRule(context.Background(), rule, req)
		require.NoError(t, err)
		require.Equal(t, pb.DecisionType_DECISION_TYPE_ALLOW, legacy.GetDecision(), "call %d should allow", i)
	}

	throttledReq := &pb.PolicyCheckRequest{
		JobId:  "job-4",
		Topic:  "job.acme.velocity",
		Tenant: "tenant-acme",
		Labels: map[string]string{"session_id": "sess-1"},
	}
	legacy, unified, err := srv.EvaluateRule(context.Background(), rule, throttledReq)

	require.NoError(t, err)
	require.Equal(t, pb.DecisionType_DECISION_TYPE_THROTTLE, legacy.GetDecision())
	require.Equal(t, policy.DecisionThrottle, unified.Type)
	require.NotEmpty(t, unified.Trace)
}
