package edge

import (
	"fmt"
	"slices"
)

// hookEnforcedConstraintKinds has one entry per top-level PolicyConstraints
// field, keyed by the protojson name the gateway sends on /api/v1/edge/evaluate.
// The value says whether this Edge build applies that kind on the hook path.
// None is applied today (docs/contract/enforcement-matrix.yaml, edge-hook-* rows).
var hookEnforcedConstraintKinds = map[string]bool{
	"budgets":        false,
	"sandbox":        false,
	"toolchain":      false,
	"diff":           false,
	"redactionLevel": false,
}

// UnenforceableConstraintKinds returns, sorted, the kinds in constraints that
// the Edge hook path cannot apply; an unknown kind counts as one. A nil value,
// an empty string, an empty map or an empty list carries no constraint.
func UnenforceableConstraintKinds(constraints map[string]any) []string {
	var kinds []string
	for kind, value := range constraints {
		if !hookEnforcedConstraintKinds[kind] && !emptyConstraintValue(value) {
			kinds = append(kinds, kind)
		}
	}
	slices.Sort(kinds)
	return kinds
}

// UnenforceableConstraintReason is the deny reason for kinds returned by
// UnenforceableConstraintKinds.
func UnenforceableConstraintReason(kinds []string) string {
	return fmt.Sprintf("Cordum Edge cannot apply the policy constraint kinds %q on this hook; action was not run", kinds)
}

func emptyConstraintValue(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case string:
		return v == ""
	case map[string]any:
		return len(v) == 0
	case []any:
		return len(v) == 0
	default:
		return false
	}
}
