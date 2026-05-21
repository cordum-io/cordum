package actiongates

import (
	"context"
	"maps"

	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// Pipeline runs the action-layer gates in fixed evaluation order:
// tenant → file → url → mcp → mutation → provenance. The ordering is
// deliberate:
//
//   - tenant first: cross-tenant denials short-circuit everything; we don't
//     want a target_path check leaking that resource exists in another tenant.
//   - file / url: enforce egress + filesystem invariants before tool-/MCP-
//     specific logic that might rely on them.
//   - mcp: validate tool/server/resource scope before evaluating mutations
//     so a non-allowlisted MCP server is rejected without revealing approval
//     state for its tools.
//   - mutation: approval+self-approval+expiry+consumption checks.
//   - provenance: chain-evidence verification — last because it depends on
//     the approval record the mutation gate already validated.
//
// A gate may return a zero decision (Decision == UNSPECIFIED) to signal
// "does not apply to this input"; the pipeline simply continues. An
// ALLOW lets subsequent gates run because each gate enforces a different
// invariant. ALLOW_WITH_CONSTRAINTS is also treated as non-terminal for
// defensive compatibility, but production action gates do not use it as an
// enforcement surface. Any other decision (DENY / REQUIRE_HUMAN / THROTTLE)
// short-circuits.
type Pipeline struct {
	gates []ActionGate
}

// NewPipeline returns a pipeline with the supplied gates in evaluation
// order. nil gates are filtered out so deployments can omit a gate
// (e.g. provenance gate when an approval store is not yet wired) by
// passing nil rather than wrapping the option struct in conditional
// build logic.
func NewPipeline(gates ...ActionGate) *Pipeline {
	if len(gates) == 0 {
		return &Pipeline{}
	}
	out := make([]ActionGate, 0, len(gates))
	for _, g := range gates {
		if g == nil {
			continue
		}
		out = append(out, g)
	}
	return &Pipeline{gates: out}
}

// Gates returns a shallow copy of the registered gates in evaluation
// order. Exposed for observability (admin endpoints, tests) — never
// mutate the returned slice.
func (p *Pipeline) Gates() []ActionGate {
	if p == nil || len(p.gates) == 0 {
		return nil
	}
	out := make([]ActionGate, len(p.gates))
	copy(out, p.gates)
	return out
}

// Run evaluates each gate in order. It returns (decision, true) on the
// first non-allow decision and short-circuits the remaining gates.
// A nil pipeline / nil input / Action-less input returns the zero
// decision with fired=false so callers can fall through to legacy
// rule evaluation without an action-layer ambiguity.
//
// When every fired gate ALLOWs (DECISION_TYPE_ALLOW, or the compatibility
// DECISION_TYPE_ALLOW_WITH_CONSTRAINTS anomaly) the pipeline returns
// (mergedExtra, false): fired stays false so the existing "no blocking
// decision" caller contract is preserved, and the returned decision
// keeps Decision == UNSPECIFIED (Fired() reports false). The Extra map
// carries merged non-PII breadcrumbs from every firing allow gate. Generic
// action-gate Constraints are intentionally not merged: typed runtime
// constraints are enforced through policy bundles / SafetyKernel
// PolicyConstraints, not through the blockers-only action-gate pipeline.
func (p *Pipeline) Run(ctx context.Context, in *config.PolicyInput) (ActionGateDecision, bool) {
	if p == nil || len(p.gates) == 0 {
		return ActionGateDecision{}, false
	}
	if in == nil || in.Action == nil {
		return ActionGateDecision{}, false
	}
	var mergedExtra map[string]string
	for _, g := range p.gates {
		select {
		case <-ctx.Done():
			return ActionGateDecision{
				Decision:  pb.DecisionType_DECISION_TYPE_DENY,
				GateID:    g.ID(),
				Code:      CodeInternalError,
				Reason:    "context canceled before pipeline completed",
				SubReason: "context_canceled",
				Extra:     map[string]string{"gate": g.ID(), "sub_reason": "context_canceled"},
			}, true
		default:
		}
		dec := g.Evaluate(ctx, in)
		if !dec.Fired() {
			continue
		}
		if dec.Allowed() {
			if len(dec.Extra) > 0 {
				if mergedExtra == nil {
					mergedExtra = make(map[string]string, len(dec.Extra))
				}
				maps.Copy(mergedExtra, dec.Extra)
			}
			continue
		}
		return dec, true
	}
	return ActionGateDecision{Extra: mergedExtra}, false
}
