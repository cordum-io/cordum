package scheduler

import "github.com/cordum/cordum/core/model"

// Dependencies captures optional engine integrations that can be wired without
// changing the core constructor signature used across existing tests.
type Dependencies struct {
	DecisionLog model.DecisionLogStore
}

// WithDependencies applies optional integrations while preserving the existing
// constructor call sites used throughout the scheduler test suite.
func (e *Engine) WithDependencies(deps Dependencies) *Engine {
	if e == nil {
		return nil
	}
	if deps.DecisionLog != nil {
		e.decisionLog = deps.DecisionLog
	}
	return e
}

// WithDecisionLog wires a Policy Decision Log sink directly.
func (e *Engine) WithDecisionLog(store model.DecisionLogStore) *Engine {
	if e == nil {
		return nil
	}
	if store == nil {
		e.decisionLog = NoopDecisionLogStore{}
		return e
	}
	e.decisionLog = store
	return e
}
