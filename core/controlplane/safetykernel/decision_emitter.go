package safetykernel

import (
	"context"
	"strings"
	"time"

	"github.com/cordum/cordum/core/policy"
)

// BundleBinding identifies the bundle snapshot that supplied a rule.
type BundleBinding struct {
	BundleID      string
	BundleVersion string
}

type bundleBindingContextKey struct{}

// WithBundleBinding attaches optional bundle metadata to decision emission.
func WithBundleBinding(ctx context.Context, binding BundleBinding) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, bundleBindingContextKey{}, sanitizeBundleBinding(binding))
}

// EmitDecision builds a unified, job-sourced policy.Decision.
func EmitDecision(
	ctx context.Context,
	rule policy.Rule,
	decisionType policy.DecisionType,
	trace []policy.TraceStep,
	inputRef string,
	outputRef string,
	auditHash string,
) policy.Decision {
	binding := bundleBindingFromContext(ctx)
	return policy.Decision{
		Source:        policy.DecisionSourceJob,
		RuleID:        strings.TrimSpace(rule.ID),
		BundleID:      binding.BundleID,
		BundleVersion: binding.BundleVersion,
		Type:          decisionType,
		Trace:         append([]policy.TraceStep{}, trace...),
		InputRef:      strings.TrimSpace(inputRef),
		OutputRef:     strings.TrimSpace(outputRef),
		AuditHash:     strings.TrimSpace(auditHash),
		Timestamp:     time.Now().UTC(),
	}
}

func bundleBindingFromContext(ctx context.Context) BundleBinding {
	if ctx == nil {
		return BundleBinding{}
	}
	binding, ok := ctx.Value(bundleBindingContextKey{}).(BundleBinding)
	if !ok {
		return BundleBinding{}
	}
	return sanitizeBundleBinding(binding)
}

func sanitizeBundleBinding(binding BundleBinding) BundleBinding {
	return BundleBinding{
		BundleID:      strings.TrimSpace(binding.BundleID),
		BundleVersion: strings.TrimSpace(binding.BundleVersion),
	}
}
