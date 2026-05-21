package safetykernel

import (
	"context"

	"github.com/cordum/cordum/core/governance/evaluator"
	"github.com/cordum/cordum/core/infra/config"
)

// SetGovernanceEvaluator installs the multi-agent governance evaluator
// + the policy it evaluates against. Wired in-process by the gateway;
// nil disables governance evaluation (callers receive an unspecified
// Decision and proceed through normal rule eval).
func (s *server) SetGovernanceEvaluator(e evaluator.Evaluator, policy config.GovernancePolicy) {
	s.mu.Lock()
	s.governanceEvaluator = e
	s.governancePolicy = policy
	s.mu.Unlock()
}

// EvaluateGovernance runs the configured governance evaluator against
// a typed GovernanceInput. The kernel exposes this as a SEPARATE entry
// point (not folded into Check) so gateway callers can short-circuit
// multi-agent delegation/handoff/shared-memory operations BEFORE the
// rule loop ever runs.
//
// Returns a zero-value evaluator.Decision when no evaluator is
// configured or when the input is nil — callers MUST check Fired()
// before acting on the result.
//
// The evaluator does NOT need the *pb.PolicyCheckRequest because
// GovernanceInput is constructed server-side from authenticated records
// (auth.AuthContext + delegation store + approval store + scheduler
// resource records — see core/controlplane/gateway/governance_ctx.go
// ::BuildGovernanceInput). Client-supplied labels with reserved
// governance prefixes are already rejected at the gateway boundary.
func (s *server) EvaluateGovernance(ctx context.Context, in *config.GovernanceInput) evaluator.Decision {
	s.mu.RLock()
	e := s.governanceEvaluator
	policy := s.governancePolicy
	s.mu.RUnlock()
	if e == nil || in == nil {
		return evaluator.Decision{}
	}
	return e.Evaluate(ctx, in, policy)
}
