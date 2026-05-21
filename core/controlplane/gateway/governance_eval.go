package gateway

import (
	"context"

	governanceeval "github.com/cordum/cordum/core/governance/evaluator"
	"github.com/cordum/cordum/core/infra/config"
)

type governanceRunner interface {
	Evaluate(context.Context, *config.GovernanceInput, config.GovernancePolicy) governanceeval.Decision
}

func (s *server) wireGovernanceEvaluator() {
	s.SetGovernanceEvaluator(governanceeval.New(), config.DefaultGovernancePolicy())
}

func (s *server) SetGovernanceEvaluator(e governanceRunner, policy config.GovernancePolicy) {
	if s == nil {
		return
	}
	s.governanceEvalMu.Lock()
	s.governanceEvaluator = e
	s.governancePolicy = policy
	s.governanceEvalMu.Unlock()
}

func (s *server) EvaluateGovernance(ctx context.Context, in *config.GovernanceInput) governanceeval.Decision {
	if s == nil || in == nil {
		return governanceeval.Decision{}
	}
	s.governanceEvalMu.RLock()
	e := s.governanceEvaluator
	policy := s.governancePolicy
	s.governanceEvalMu.RUnlock()
	if e == nil {
		return governanceeval.Decision{}
	}
	return e.Evaluate(ctx, in, policy)
}
