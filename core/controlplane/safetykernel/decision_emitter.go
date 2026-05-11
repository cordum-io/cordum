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
type jobContextKey struct{}

// WithBundleBinding attaches optional bundle metadata to decision emission.
func WithBundleBinding(ctx context.Context, binding BundleBinding) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, bundleBindingContextKey{}, sanitizeBundleBinding(binding))
}

// WithJobContext attaches per-evaluation job identity to decision emission.
// Mirrors the BundleBinding context pattern so EmitDecision can read fields
// without a signature break for callers that don't yet thread identity.
func WithJobContext(ctx context.Context, jc JobContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, jobContextKey{}, sanitizeJobContext(jc))
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
	jc := jobContextFromContext(ctx)
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
		JobID:         jc.JobID,
		AgentID:       jc.AgentID,
		PrincipalID:   jc.PrincipalID,
		TenantID:      jc.Tenant,
		Topic:         jc.Topic,
		// jc.WorkflowID intentionally not propagated to Decision —
		// WorkflowID is RuleScopeMatchesJob input, not Decision identity.
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

func jobContextFromContext(ctx context.Context) JobContext {
	if ctx == nil {
		return JobContext{}
	}
	jc, ok := ctx.Value(jobContextKey{}).(JobContext)
	if !ok {
		return JobContext{}
	}
	return sanitizeJobContext(jc)
}

func sanitizeJobContext(jc JobContext) JobContext {
	return JobContext{
		Tenant:      strings.TrimSpace(jc.Tenant),
		WorkflowID:  strings.TrimSpace(jc.WorkflowID),
		JobID:       strings.TrimSpace(jc.JobID),
		AgentID:     strings.TrimSpace(jc.AgentID),
		PrincipalID: strings.TrimSpace(jc.PrincipalID),
		Topic:       strings.TrimSpace(jc.Topic),
	}
}
