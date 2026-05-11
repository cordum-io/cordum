package safetykernel

import (
	"context"
	"testing"

	"github.com/cordum/cordum/core/policy"
	"github.com/stretchr/testify/require"
)

func TestEmitDecisionBuildsUnifiedDecisionForEveryDecisionType(t *testing.T) {
	types := []policy.DecisionType{
		policy.DecisionAllow,
		policy.DecisionDeny,
		policy.DecisionRequireHuman,
		policy.DecisionThrottle,
		policy.DecisionAllowWithConstraints,
		policy.DecisionQuarantine,
		policy.DecisionRedact,
	}

	for _, decisionType := range types {
		t.Run(decisionType.String(), func(t *testing.T) {
			ctx := WithBundleBinding(context.Background(), BundleBinding{
				BundleID:      "bundle-main",
				BundleVersion: "v3",
			})
			rule := policy.Rule{ID: "rule-" + decisionType.String(), Type: policy.RuleTypeInput}
			trace := []policy.TraceStep{{
				RuleID:       rule.ID,
				DecisionType: decisionType,
				Reason:       "matched",
			}}

			got := EmitDecision(ctx, rule, decisionType, trace, "blob://input", "blob://output", "sha256:audit")

			require.Equal(t, policy.DecisionSourceJob, got.Source)
			require.Equal(t, rule.ID, got.RuleID)
			require.Equal(t, "bundle-main", got.BundleID)
			require.Equal(t, "v3", got.BundleVersion)
			require.Equal(t, decisionType, got.Type)
			require.Equal(t, trace, got.Trace)
			require.Equal(t, "blob://input", got.InputRef)
			require.Equal(t, "blob://output", got.OutputRef)
			require.Equal(t, "sha256:audit", got.AuditHash)
			require.False(t, got.Timestamp.IsZero())
			require.Equal(t, got.Timestamp.UTC(), got.Timestamp)
		})
	}
}

func TestEmitDecisionMissingBundleBindingUsesEmptyFields(t *testing.T) {
	got := EmitDecision(
		context.Background(),
		policy.Rule{ID: "rule-no-binding", Type: policy.RuleTypeInput},
		policy.DecisionAllow,
		nil,
		"",
		"",
		"",
	)

	require.Equal(t, "rule-no-binding", got.RuleID)
	require.Empty(t, got.BundleID)
	require.Empty(t, got.BundleVersion)
	require.Equal(t, policy.DecisionAllow, got.Type)
}

func TestWithBundleBindingAcceptsNilContext(t *testing.T) {
	//nolint:staticcheck // SA1012: deliberate nil context to exercise the default branch.
	ctx := WithBundleBinding(nil, BundleBinding{BundleID: " bundle ", BundleVersion: " v1 "})
	require.NotNil(t, ctx)

	got := EmitDecision(ctx, policy.Rule{ID: "r-nil-ctx", Type: policy.RuleTypeInput}, policy.DecisionAllow, nil, "", "", "")
	require.Equal(t, "bundle", got.BundleID)
	require.Equal(t, "v1", got.BundleVersion)
}

func TestBundleBindingFromContextIgnoresWrongType(t *testing.T) {
	type otherKey struct{}
	ctx := context.WithValue(context.Background(), otherKey{}, "not-a-binding")

	got := EmitDecision(ctx, policy.Rule{ID: "r-other", Type: policy.RuleTypeInput}, policy.DecisionAllow, nil, "", "", "")
	require.Empty(t, got.BundleID)
	require.Empty(t, got.BundleVersion)
}

func TestBundleBindingFromContextHandlesNil(t *testing.T) {
	//nolint:staticcheck // SA1012: deliberate nil context to exercise the default branch.
	got := EmitDecision(nil, policy.Rule{ID: "r-nil"}, policy.DecisionAllow, nil, "", "", "")
	require.Empty(t, got.BundleID)
	require.Empty(t, got.BundleVersion)
}

// Backend 5e lock-in. EmitDecision must populate JobID/AgentID/PrincipalID/
// TenantID/Topic from the JobContext attached via WithJobContext. SessionID
// stays empty for JOB-source decisions (EDGE emitter populates it).
func TestEmitDecisionPopulatesJobContextIdentity(t *testing.T) {
	ctx := WithJobContext(context.Background(), JobContext{
		Tenant:      "acme",
		WorkflowID:  "wf-deploy",
		JobID:       "job-1234",
		AgentID:     "agent-alpha",
		PrincipalID: "principal-yaron",
		Topic:       "job.deploy",
	})
	got := EmitDecision(ctx, policy.Rule{ID: "rule-with-context", Type: policy.RuleTypeInput},
		policy.DecisionAllow, nil, "", "", "")

	require.Equal(t, "job-1234", got.JobID)
	require.Equal(t, "agent-alpha", got.AgentID)
	require.Equal(t, "principal-yaron", got.PrincipalID)
	require.Equal(t, "acme", got.TenantID)
	require.Equal(t, "job.deploy", got.Topic)
	require.Empty(t, got.SessionID, "JOB-source decision must NOT populate SessionID")
}

// Identity context is optional — EmitDecision must not populate the new
// identity fields when the context lacks JobContext (e.g. unit tests, legacy
// callers that don't yet thread identity).
func TestEmitDecisionWithoutJobContextLeavesIdentityFieldsEmpty(t *testing.T) {
	got := EmitDecision(context.Background(),
		policy.Rule{ID: "rule-no-identity", Type: policy.RuleTypeInput},
		policy.DecisionAllow, nil, "", "", "")
	require.Empty(t, got.JobID)
	require.Empty(t, got.AgentID)
	require.Empty(t, got.PrincipalID)
	require.Empty(t, got.TenantID)
	require.Empty(t, got.Topic)
	require.Empty(t, got.SessionID)
}

// WithJobContext + bundleBinding combine — both context values must coexist on
// the emitted Decision without trampling each other.
func TestEmitDecisionCombinesBundleBindingAndJobContext(t *testing.T) {
	ctx := WithBundleBinding(context.Background(), BundleBinding{BundleID: "bnd", BundleVersion: "v2"})
	ctx = WithJobContext(ctx, JobContext{JobID: "job-merge", Tenant: "tenA"})
	got := EmitDecision(ctx, policy.Rule{ID: "rule-merge", Type: policy.RuleTypeInput},
		policy.DecisionAllow, nil, "", "", "")
	require.Equal(t, "bnd", got.BundleID)
	require.Equal(t, "v2", got.BundleVersion)
	require.Equal(t, "job-merge", got.JobID)
	require.Equal(t, "tenA", got.TenantID)
}

func TestEmitDecisionIsConcurrentSafe(t *testing.T) {
	ctx := WithBundleBinding(context.Background(), BundleBinding{BundleID: "bundle", BundleVersion: "v1"})
	rule := policy.Rule{ID: "rule-concurrent", Type: policy.RuleTypeInput}
	errCh := make(chan error, 32)

	for i := 0; i < cap(errCh); i++ {
		go func() {
			got := EmitDecision(ctx, rule, policy.DecisionDeny, nil, "", "", "")
			if got.Source != policy.DecisionSourceJob || got.RuleID != rule.ID || got.Type != policy.DecisionDeny {
				errCh <- context.Canceled
				return
			}
			errCh <- nil
		}()
	}

	for i := 0; i < cap(errCh); i++ {
		require.NoError(t, <-errCh)
	}
}
