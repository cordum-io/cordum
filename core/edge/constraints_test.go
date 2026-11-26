package edge

import (
	"slices"
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestUnenforceableConstraintKinds(t *testing.T) {
	for _, tc := range []struct {
		name        string
		constraints map[string]any
		want        []string
	}{
		{"nil", nil, nil},
		{"budgets", map[string]any{"budgets": map[string]any{"maxRuntimeMs": "600000"}}, []string{"budgets"}},
		{"sandbox", map[string]any{"sandbox": map[string]any{"isolated": true}}, []string{"sandbox"}},
		{"toolchain", map[string]any{"toolchain": map[string]any{"allowedCommands": []any{"go test"}}}, []string{"toolchain"}},
		{"diff", map[string]any{"diff": map[string]any{"maxFiles": float64(1)}}, []string{"diff"}},
		{"redactionLevel", map[string]any{"redactionLevel": "strict"}, []string{"redactionLevel"}},
		{"unknown kind", map[string]any{"maxCostUsd": 5.0}, []string{"maxCostUsd"}},
		{"empty values", map[string]any{"budgets": nil, "redactionLevel": "", "sandbox": map[string]any{}, "futureKind": []any{}}, nil},
		// The kernel sends every submessage, so the unused ones arrive as {}.
		{"kernel wire shape", map[string]any{
			"budgets": map[string]any{}, "sandbox": map[string]any{"isolated": true}, "toolchain": map[string]any{},
			"diff": map[string]any{}, "redactionLevel": "strict",
		}, []string{"redactionLevel", "sandbox"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := UnenforceableConstraintKinds(tc.constraints); !slices.Equal(got, tc.want) {
				t.Fatalf("UnenforceableConstraintKinds() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUnenforceableConstraintReasonNamesKinds(t *testing.T) {
	got := UnenforceableConstraintReason([]string{"redactionLevel", "sandbox"})
	want := `Cordum Edge cannot apply the policy constraint kinds ["redactionLevel" "sandbox"] on this hook; action was not run`
	if got != want {
		t.Fatalf("reason = %q, want %q", got, want)
	}
}

// Every PolicyConstraints field needs a decision in the list, and every list
// entry needs a field behind it.
func TestHookConstraintKindsCoverPolicyConstraints(t *testing.T) {
	fields := (&pb.PolicyConstraints{}).ProtoReflect().Descriptor().Fields()
	names := map[string]bool{}
	for i := range fields.Len() {
		name := fields.Get(i).JSONName()
		names[name] = true
		if _, ok := hookEnforcedConstraintKinds[name]; !ok {
			t.Errorf("PolicyConstraints field %q has no entry in hookEnforcedConstraintKinds", name)
		}
	}
	for kind := range hookEnforcedConstraintKinds {
		if !names[kind] {
			t.Errorf("hookEnforcedConstraintKinds entry %q matches no PolicyConstraints field", kind)
		}
	}
}
