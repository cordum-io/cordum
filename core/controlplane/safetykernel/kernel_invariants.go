package safetykernel

import (
	"strings"

	"github.com/cordum/cordum/core/infra/config"
)

// kernelInvariantsBundleKey mirrors policybundles.PolicyInvariantsBundleKey
// for kernel-side bundle iteration. Both must remain in sync — the gateway
// merger and the kernel loader BOTH consult bundles[kernelInvariantsBundleKey]
// and apply ApplyInvariants precedence so the same studio-authored security
// floor enforces uniformly across surfaces. If you change the bundle key
// here, also change it in core/controlplane/gateway/policybundles/types.go.
const kernelInvariantsBundleKey = "secops/invariants"

// applyKernelInvariants overlays invariant rules onto a merged base policy
// with the same security-floor precedence as policybundles.ApplyInvariants:
// invariant DENYs prepended, invariant ALLOWs appended, invariant
// OutputRules prepended. The kernel intentionally re-implements this
// (rather than importing the gateway-side helper) to keep the
// safetykernel package free of a downward gateway dependency.
//
// base may be nil; inv may be nil. nil base + non-nil inv yields a fresh
// policy carrying only the invariants. The returned policy is a fresh
// clone so callers may retain references to base/inv without aliasing.
func applyKernelInvariants(base, inv *config.SafetyPolicy) *config.SafetyPolicy {
	if inv == nil {
		if base == nil {
			return nil
		}
		return clonePolicy(base)
	}
	out := clonePolicy(base)
	if out == nil {
		out = &config.SafetyPolicy{}
	}

	denies, allows := splitKernelInvariantRulesByDecision(inv.Rules)
	if len(denies) > 0 {
		prepended := make([]config.PolicyRule, 0, len(denies)+len(out.Rules))
		prepended = append(prepended, denies...)
		prepended = append(prepended, out.Rules...)
		out.Rules = prepended
	}
	if len(allows) > 0 {
		out.Rules = append(out.Rules, allows...)
	}
	if len(inv.OutputRules) > 0 {
		merged := make([]config.OutputPolicyRule, 0, len(inv.OutputRules)+len(out.OutputRules))
		merged = append(merged, inv.OutputRules...)
		merged = append(merged, out.OutputRules...)
		out.OutputRules = merged
	}
	if !out.OutputPolicy.Enabled && inv.OutputPolicy.Enabled {
		out.OutputPolicy.Enabled = true
	}
	if strings.TrimSpace(out.OutputPolicy.FailMode) == "" {
		out.OutputPolicy.FailMode = strings.TrimSpace(inv.OutputPolicy.FailMode)
	}
	if len(inv.InputRules) > 0 {
		// InputRules (content-pattern scanning) follow the same
		// security-floor logic — invariant input scans run first.
		merged := make([]config.InputPolicyRule, 0, len(inv.InputRules)+len(out.InputRules))
		merged = append(merged, inv.InputRules...)
		merged = append(merged, out.InputRules...)
		out.InputRules = merged
	}
	return out
}

// splitKernelInvariantRulesByDecision partitions invariant rules into the
// DENY-uncrossable bucket (decisions that block or escalate execution)
// and the ALLOW fallback bucket. Mirrors splitInvariantRulesByDecision in
// the gateway-side policybundles package.
func splitKernelInvariantRulesByDecision(rules []config.PolicyRule) (denies, allows []config.PolicyRule) {
	for _, r := range rules {
		switch strings.ToLower(strings.TrimSpace(r.Decision)) {
		case "deny", "require_approval", "require-approval", "require_human", "throttle":
			denies = append(denies, r)
		default:
			allows = append(allows, r)
		}
	}
	return denies, allows
}

// currentGlobalPolicy returns the kernel's typed cross-evaluator view.
// Callers must NOT mutate the returned struct; it shares slices with the
// kernel's locked state. Use this for the /api/v1/policy/global endpoint
// and for MCP-gate Invariants consultation. Returns nil when no policy
// has been loaded yet (the kernel's fail-closed startup window).
func (s *server) currentGlobalPolicy() *GlobalPolicy {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.global
}
