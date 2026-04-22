package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	LabelDelegationDepth        = "_delegation.depth"
	LabelDelegationIssuer       = "_delegation.issuer"
	LabelDelegationIssuerChain  = "_delegation.issuer_chain"
	LabelDelegationParentIssuer = "_delegation.parent_issuer"
	LabelDelegationJTI          = "_delegation.jti"
	LabelDelegationExpiresAt    = "_delegation.expires_at"
	LabelDelegationAudience     = "_delegation.audience"
	LabelDelegationScope        = "_delegation.scope"
	LabelDelegationSubject      = "_delegation.subject"
)

// DelegationContext carries verified delegation metadata from the gateway into
// policy evaluation. The gateway verifies the JWT and serializes only the
// fields the kernel needs for policy rules.
type DelegationContext struct {
	Depth        int
	IssuerChain  []string
	Scope        []string
	RootIssuer   string
	ParentIssuer string
	JTI          string
	ExpiresAt    string
	Audience     string
}

// DelegationMatch is the structured rule-match block under
// PolicyRule.Match.Delegation. Every field is optional. An empty
// DelegationMatch (no constraints) is delegation-neutral and matches
// both direct and delegated requests.
//
// Policies that specify ANY delegation-specific constraint (MaxDepth,
// Issuers, RequireIssuer, RequiredScope) are treated as delegation-
// scoped rules: evaluateDelegationMatch fails closed when the request
// carries no delegation chain, rather than silently passing direct
// calls through constraints designed for delegated ones. This closes
// the foot-gun where a deny rule written for "delegated calls with
// depth>3" would also fire against direct calls.
//
// Use DelegationRequired=false to OPT OUT of fail-closed behaviour
// (rule matches BOTH direct and delegated), true to require delegation,
// or leave nil to accept the auto-inferred default from the presence
// of constraints.
type DelegationMatch struct {
	// MaxDepth matches when delegation!=nil AND delegation.Depth
	// <= *MaxDepth. A pointer (not int) so the zero value and
	// "MaxDepth==0 means allow only direct chains" are
	// distinguishable.
	MaxDepth *int `yaml:"max_depth,omitempty"`
	// Issuers matches when delegation!=nil AND every id in the
	// chain is in this allowlist. Strictest interpretation —
	// relax via RequireIssuer (root-only) if operators want.
	Issuers []string `yaml:"issuers,omitempty"`
	// RequireIssuer matches when delegation!=nil AND
	// delegation.RootIssuer == RequireIssuer.
	RequireIssuer string `yaml:"require_issuer,omitempty"`
	// RequiredScope matches when delegation!=nil AND
	// RequiredScope ⊆ delegation.Scope (set-subset).
	RequiredScope []string `yaml:"required_scope,omitempty"`
	// ForbidDelegated=true matches ONLY non-delegated requests
	// (delegation==nil). Lets operators write "this rule fires
	// only on direct calls" rules without inverting the match
	// logic elsewhere.
	ForbidDelegated bool `yaml:"forbid_delegated,omitempty"`
	// DelegationRequired is an explicit tri-state override for the
	// auto-inferred "this rule is delegation-scoped" behaviour:
	//   nil   → infer from constraints (constraints present → true)
	//   true  → rule applies ONLY to delegated requests
	//   false → rule applies to BOTH direct and delegated (legacy
	//           permissive behaviour; use only when the constraints
	//           are intentionally non-gating for direct calls).
	// Policy authors should almost never need false — it exists
	// purely for the narrow case where an operator wants a
	// delegation-neutral rule to also record delegation metadata.
	DelegationRequired *bool `yaml:"delegation_required,omitempty"`
}

// hasDelegationScopedConstraint reports whether the match carries any
// field whose semantics only make sense for a delegated request. Used
// by evaluateDelegationMatch to default delegation-scoped rules to
// fail-closed when the request is direct. DelegationRequired is an
// explicit operator-facing override that wins over this inference.
func (m *DelegationMatch) hasDelegationScopedConstraint() bool {
	if m == nil {
		return false
	}
	if m.MaxDepth != nil {
		return true
	}
	if len(m.Issuers) > 0 {
		return true
	}
	if strings.TrimSpace(m.RequireIssuer) != "" {
		return true
	}
	if len(m.RequiredScope) > 0 {
		return true
	}
	return false
}

// agentIDRegex is the validation pattern for issuer and
// require-issuer values. Mirrors the existing agent-identity
// naming convention used across the RBAC surface.
var agentIDRegex = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_\-\.]{0,127}$`)

// Validate checks the structural integrity of a DelegationMatch
// block before it reaches evaluation. Returns a typed error so the
// policy-parse path can reject malformed bundles at load time.
func (m *DelegationMatch) Validate() error {
	if m == nil {
		return nil
	}
	if m.MaxDepth != nil && *m.MaxDepth < 0 {
		return fmt.Errorf("delegation.max_depth must be >= 0, got %d", *m.MaxDepth)
	}
	seen := make(map[string]struct{}, len(m.Issuers))
	for _, id := range m.Issuers {
		trim := strings.TrimSpace(id)
		if trim == "" {
			return fmt.Errorf("delegation.issuers must not contain empty ids")
		}
		if !agentIDRegex.MatchString(trim) {
			return fmt.Errorf("delegation.issuers: invalid agent id %q", trim)
		}
		if _, dup := seen[trim]; dup {
			return fmt.Errorf("delegation.issuers: duplicate agent id %q", trim)
		}
		seen[trim] = struct{}{}
	}
	if trim := strings.TrimSpace(m.RequireIssuer); trim != "" && !agentIDRegex.MatchString(trim) {
		return fmt.Errorf("delegation.require_issuer: invalid agent id %q", m.RequireIssuer)
	}
	if len(m.RequiredScope) > 0 {
		for _, s := range m.RequiredScope {
			if strings.TrimSpace(s) == "" {
				return fmt.Errorf("delegation.required_scope must not contain empty values")
			}
		}
	}
	return nil
}

// evaluateDelegationMatch applies the six DoD match semantics:
//
//  1. ForbidDelegated=true matches ONLY when delegation==nil.
//  2. delegation==nil with only non-Forbid fields set → neutral
//     (matches) — "no-delegation = direct call = passes all
//     delegation rules" per the DoD.
//  3. MaxDepth: matches when *MaxDepth >= delegation.Depth.
//  4. Issuers: matches when every id in delegation.IssuerChain
//     ∈ allowlist.
//  5. RequireIssuer: matches when delegation.RootIssuer == it.
//  6. RequiredScope: matches when the required set ⊆
//     delegation.Scope (both canonicalised before comparison so
//     [read,write] and [write,read] evaluate equivalently).
//
// A match helper returns true when the rule should FIRE for the
// given (match, delegation) pair — consistent with the existing
// PolicyMatch semantics throughout this package.
// delegationMatchDenyCallback, when set by an observer package, is invoked
// exactly once per evaluateDelegationMatch call that rejects a rule. The
// `field` argument names the sub-field that short-circuited the rule
// (forbid_delegated|max_depth|issuers|require_issuer|required_scope). The
// callback seam keeps this package metric-library-agnostic — safetykernel's
// metrics.go wires it to the Prometheus counter.
var delegationMatchDenyCallback func(field string)

// SetDelegationMatchDenyCallback registers (or clears, when passed nil)
// the observer invoked whenever evaluateDelegationMatch rejects a rule.
// Exported so safetykernel's metrics bootstrap can inject its Prometheus
// collector without importing from this package at evaluator-run time.
func SetDelegationMatchDenyCallback(cb func(field string)) {
	delegationMatchDenyCallback = cb
}

// DelegationMatchDenyFields enumerates the sub-field names that
// evaluateDelegationMatch may report when a rule is rejected. Exposed so
// observer packages can pre-register label values with their collector and
// so tests can enumerate expected values without hard-coding strings.
var DelegationMatchDenyFields = [...]string{
	"forbid_delegated",
	"max_depth",
	"issuers",
	"require_issuer",
	"required_scope",
	"delegation_required",
}

func evaluateDelegationMatch(match *DelegationMatch, delegation *DelegationContext) bool {
	if match == nil {
		return true
	}
	// ForbidDelegated flips the direct-vs-delegated decision.
	if match.ForbidDelegated {
		if delegation == nil {
			return true
		}
		reportDelegationDeny("forbid_delegated")
		return false
	}
	// When delegation is absent, decide whether this rule is
	// delegation-scoped. The explicit DelegationRequired flag
	// overrides auto-inference; otherwise a match with any
	// delegation-specific constraint is treated as delegation-
	// scoped and fails closed for direct calls.
	if delegation == nil {
		if match.DelegationRequired != nil {
			if *match.DelegationRequired {
				reportDelegationDeny("delegation_required")
				return false
			}
			return true
		}
		if match.hasDelegationScopedConstraint() {
			reportDelegationDeny("delegation_required")
			return false
		}
		return true
	}
	// Structured checks — every failing sub-field short-circuits.
	if match.MaxDepth != nil && delegation.Depth > *match.MaxDepth {
		reportDelegationDeny("max_depth")
		return false
	}
	if len(match.Issuers) > 0 {
		allowed := make(map[string]struct{}, len(match.Issuers))
		for _, id := range match.Issuers {
			allowed[strings.TrimSpace(id)] = struct{}{}
		}
		for _, chainID := range delegation.IssuerChain {
			if _, ok := allowed[strings.TrimSpace(chainID)]; !ok {
				reportDelegationDeny("issuers")
				return false
			}
		}
	}
	if req := strings.TrimSpace(match.RequireIssuer); req != "" {
		if !strings.EqualFold(strings.TrimSpace(delegation.RootIssuer), req) {
			reportDelegationDeny("require_issuer")
			return false
		}
	}
	if len(match.RequiredScope) > 0 {
		have := make(map[string]struct{}, len(delegation.Scope))
		for _, s := range delegation.Scope {
			have[strings.ToLower(strings.TrimSpace(s))] = struct{}{}
		}
		for _, required := range match.RequiredScope {
			key := strings.ToLower(strings.TrimSpace(required))
			if _, ok := have[key]; !ok {
				reportDelegationDeny("required_scope")
				return false
			}
		}
	}
	return true
}

func reportDelegationDeny(field string) {
	if cb := delegationMatchDenyCallback; cb != nil {
		cb(field)
	}
}

// DelegationAuditExtras projects a verified DelegationContext into the
// SIEMEvent.Extra map keys the audit trail should carry at safety-decision
// emission time. Returns nil when the context is nil (direct call — no
// delegation keys to emit). The companion delegation.lineage audit event
// carries the full issuer chain, so the safety-decision event only includes
// compact routing fields that stay under the syslog size rail.
func DelegationAuditExtras(ctx *DelegationContext) map[string]string {
	if ctx == nil {
		return nil
	}
	out := map[string]string{
		"delegation.depth": strconv.Itoa(ctx.Depth),
	}
	if root := strings.TrimSpace(ctx.RootIssuer); root != "" {
		out["delegation.root_issuer"] = root
	}
	if parent := strings.TrimSpace(ctx.ParentIssuer); parent != "" {
		out["delegation.parent_issuer"] = parent
	}
	if jti := strings.TrimSpace(ctx.JTI); jti != "" {
		out["delegation.jti"] = jti
	}
	if expiresAt := strings.TrimSpace(ctx.ExpiresAt); expiresAt != "" {
		out["delegation.expires_at"] = expiresAt
	}
	if audience := strings.TrimSpace(ctx.Audience); audience != "" {
		out["delegation.audience"] = audience
	}
	return out
}

// DelegationContextFromLabels reconstructs a delegation context from reserved
// internal labels injected by the gateway.
func DelegationContextFromLabels(labels map[string]string) *DelegationContext {
	if len(labels) == 0 {
		return nil
	}
	depth, err := strconv.Atoi(strings.TrimSpace(labels[LabelDelegationDepth]))
	if err != nil || depth <= 0 {
		return nil
	}
	issuerChain := normalizeDelegationList(labels[LabelDelegationIssuerChain])
	rootIssuer := strings.TrimSpace(labels[LabelDelegationIssuer])
	if rootIssuer == "" && len(issuerChain) > 0 {
		rootIssuer = issuerChain[0]
	}
	if len(issuerChain) == 0 && rootIssuer != "" {
		issuerChain = []string{rootIssuer}
	}
	parentIssuer := strings.TrimSpace(labels[LabelDelegationParentIssuer])
	if parentIssuer == "" && len(issuerChain) > 0 {
		parentIssuer = issuerChain[len(issuerChain)-1]
	}
	return &DelegationContext{
		Depth:        depth,
		IssuerChain:  issuerChain,
		Scope:        normalizeDelegationList(labels[LabelDelegationScope]),
		RootIssuer:   rootIssuer,
		ParentIssuer: parentIssuer,
		JTI:          strings.TrimSpace(labels[LabelDelegationJTI]),
		ExpiresAt:    strings.TrimSpace(labels[LabelDelegationExpiresAt]),
		Audience:     strings.TrimSpace(labels[LabelDelegationAudience]),
	}
}

type delegationPredicate struct {
	kind  string
	op    string
	depth int
	value string
}

func validateDelegationPredicate(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	_, err := parseDelegationPredicate(raw)
	return err
}

func delegationPredicateMatch(raw string, delegation *DelegationContext) bool {
	if strings.TrimSpace(raw) == "" {
		return true
	}
	predicate, err := parseDelegationPredicate(raw)
	if err != nil || delegation == nil {
		return false
	}
	switch predicate.kind {
	case "depth":
		return compareDelegationDepth(delegation.Depth, predicate.op, predicate.depth)
	case "issuer":
		return strings.EqualFold(strings.TrimSpace(delegation.RootIssuer), predicate.value)
	case "scope_contains":
		return containsString(delegation.Scope, predicate.value)
	default:
		return false
	}
}

func parseDelegationPredicate(raw string) (delegationPredicate, error) {
	raw = strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(raw, "delegation.depth"):
		tail := strings.TrimSpace(strings.TrimPrefix(raw, "delegation.depth"))
		for _, op := range []string{">=", "<=", "==", ">", "<"} {
			if strings.HasPrefix(tail, op) {
				value, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(tail, op)))
				if err != nil {
					return delegationPredicate{}, fmt.Errorf("invalid delegation depth predicate %q", raw)
				}
				return delegationPredicate{kind: "depth", op: op, depth: value}, nil
			}
		}
	case strings.HasPrefix(raw, "delegation.issuer"):
		tail := strings.TrimSpace(strings.TrimPrefix(raw, "delegation.issuer"))
		if !strings.HasPrefix(tail, "==") {
			return delegationPredicate{}, fmt.Errorf("invalid delegation issuer predicate %q", raw)
		}
		value, err := parseDelegationPredicateValue(strings.TrimSpace(strings.TrimPrefix(tail, "==")))
		if err != nil {
			return delegationPredicate{}, err
		}
		return delegationPredicate{kind: "issuer", value: value}, nil
	case strings.HasPrefix(raw, "delegation.scope.contains(") && strings.HasSuffix(raw, ")"):
		inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(raw, "delegation.scope.contains("), ")"))
		value, err := parseDelegationPredicateValue(inner)
		if err != nil {
			return delegationPredicate{}, err
		}
		return delegationPredicate{kind: "scope_contains", value: value}, nil
	}
	return delegationPredicate{}, fmt.Errorf("invalid delegation predicate %q", raw)
}

func parseDelegationPredicateValue(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("delegation predicate value required")
	}
	if len(raw) >= 2 {
		if (raw[0] == '\'' && raw[len(raw)-1] == '\'') || (raw[0] == '"' && raw[len(raw)-1] == '"') {
			raw = raw[1 : len(raw)-1]
		}
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("delegation predicate value required")
	}
	return strings.ToLower(raw), nil
}

func compareDelegationDepth(actual int, op string, expected int) bool {
	switch op {
	case ">":
		return actual > expected
	case ">=":
		return actual >= expected
	case "<":
		return actual < expected
	case "<=":
		return actual <= expected
	case "==":
		return actual == expected
	default:
		return false
	}
}

func normalizeDelegationList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key := strings.ToLower(part)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, part)
	}
	return out
}
