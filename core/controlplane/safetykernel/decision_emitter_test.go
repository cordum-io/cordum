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
