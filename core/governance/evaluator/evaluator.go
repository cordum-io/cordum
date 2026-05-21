// Package evaluator implements the deterministic multi-agent governance
// evaluator. It consumes a typed config.GovernanceInput populated from
// backend-verified records and emits ALLOW/DENY/REQUIRE_HUMAN with a
// stable rule ID + sanitized reason string.
//
// Per epic-f3da4017 rail #4 (deterministic pre-dispatch gates) the
// evaluator is content-free: it does NOT inspect prompt text. The
// evaluator's signal source is the typed input — issuer chain,
// delegated scopes, resource deltas, write kind, provenance refs —
// all populated from auth/delegation/approval/scheduler stores by the
// gateway.
//
// Rule IDs are stable constants on core/infra/config (e.g.
// config.GovernanceRuleCrossTenant). Changing them is a breaking API
// change since they surface in audit, SIEM events, and HTTP error
// envelopes.
package evaluator

import (
	"context"
	"time"

	"github.com/cordum/cordum/core/infra/config"
)

// DecisionKind is the evaluator's verdict on a GovernanceInput. It is
// intentionally a small, package-local enum rather than re-exposing
// pb.DecisionType — callers translate to the wire enum at the boundary
// (kernel or gateway) so the evaluator stays decoupled from proto.
type DecisionKind int

const (
	// DecisionUnspecified is the zero value — the evaluator did not
	// fire (e.g. nil input, no governance opinion). Pipeline callers
	// MUST treat this as "no governance ruling, fall through to the
	// next gate".
	DecisionUnspecified DecisionKind = iota
	DecisionAllow
	DecisionDeny
	DecisionRequireHuman
)

// Decision is the evaluator output. Type carries the verdict; RuleID +
// Reason describe WHY for audit/HTTP envelopes; SubReason is audit-only
// (richer detail not safe for user-facing reason).
type Decision struct {
	Type      DecisionKind
	RuleID    string
	Reason    string
	Tenant    string
	SubReason string
}

// Fired reports whether this Decision short-circuits the evaluator
// pipeline (Type != Unspecified). Convenience predicate so callers
// don't have to remember the zero-value semantics.
func (d Decision) Fired() bool { return d.Type != DecisionUnspecified }

// Evaluator is the interface the safetykernel / gateway depend on. The
// default implementation is DefaultEvaluator below; tests can supply a
// fake.
type Evaluator interface {
	Evaluate(ctx context.Context, in *config.GovernanceInput, policy config.GovernancePolicy) Decision
}

// DefaultEvaluator runs the canonical rule chain (rules.go ::
// defaultRuleOrder) and returns the first firing rule. clock is
// overridable for deterministic tests; production callers leave it nil
// and time.Now is used.
type DefaultEvaluator struct {
	clock func() time.Time
}

// New returns a DefaultEvaluator that uses time.Now for staleness
// checks. Tests should use NewWithClock to inject a fixed time.
func New() *DefaultEvaluator { return &DefaultEvaluator{} }

// NewWithClock returns a DefaultEvaluator that calls clock() instead
// of time.Now. Pass nil to fall back to time.Now (the constructor
// validates this transparently).
func NewWithClock(clock func() time.Time) *DefaultEvaluator {
	return &DefaultEvaluator{clock: clock}
}

// Evaluate runs the canonical rule chain against the input + policy
// and returns the first firing rule's decision. Returns
// DecisionUnspecified when the input is nil (no governance opinion)
// or when every rule returned the zero Decision (governance-allowed).
//
// The result is intentionally "first match wins". Rules are ordered
// in rules.go::defaultRuleOrder so the cheap, non-overridable invariants
// (cross-tenant, trust-assertion-needs-chain) fire before the
// policy-dependent escalation checks. A cross-tenant operation never
// reaches the resource-escalation rule even if it would also fire
// there.
func (e *DefaultEvaluator) Evaluate(ctx context.Context, in *config.GovernanceInput, policy config.GovernancePolicy) Decision {
	if in == nil {
		return Decision{}
	}
	now := e.now()
	for _, rule := range defaultRuleOrder() {
		if dec := rule(in, policy, now); dec.Fired() {
			return dec
		}
	}
	return Decision{
		Type:   DecisionAllow,
		RuleID: "ma_allow",
		Reason: "governance evaluator: all invariants satisfied",
		Tenant: in.Tenant,
	}
}

func (e *DefaultEvaluator) now() time.Time {
	if e.clock != nil {
		return e.clock()
	}
	return time.Now()
}
