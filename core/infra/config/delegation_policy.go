package config

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	LabelDelegationDepth       = "_delegation.depth"
	LabelDelegationIssuer      = "_delegation.issuer"
	LabelDelegationIssuerChain = "_delegation.issuer_chain"
	LabelDelegationScope       = "_delegation.scope"
	LabelDelegationSubject     = "_delegation.subject"
)

// DelegationContext carries verified delegation metadata from the gateway into
// policy evaluation. The gateway verifies the JWT and serializes only the
// fields the kernel needs for policy rules.
type DelegationContext struct {
	Depth       int
	IssuerChain []string
	Scope       []string
	RootIssuer  string
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
	return &DelegationContext{
		Depth:       depth,
		IssuerChain: issuerChain,
		Scope:       normalizeDelegationList(labels[LabelDelegationScope]),
		RootIssuer:  rootIssuer,
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
