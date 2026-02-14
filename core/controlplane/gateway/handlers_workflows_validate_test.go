package gateway

import (
	"strings"
	"testing"

	wf "github.com/cordum/cordum/core/workflow"
)

func TestValidateWorkflowStepID_ValidIDs(t *testing.T) {
	valid := []string{
		"a",
		"step1",
		"my-step",
		"my_step",
		"my.step",
		"A1-b2_c3.d4",
		"ALLCAPS",
		strings.Repeat("a", 64), // exactly at limit
	}
	for _, id := range valid {
		if err := validateWorkflowStepID(id); err != nil {
			t.Errorf("expected %q to be valid, got: %v", id, err)
		}
	}
}

func TestValidateWorkflowStepID_InvalidIDs(t *testing.T) {
	cases := []struct {
		id   string
		desc string
	}{
		{"", "empty string"},
		{" ", "single space"},
		{"has space", "contains space"},
		{"step@1", "contains @"},
		{"step/1", "contains /"},
		{"step#1", "contains #"},
		{"-leading", "starts with dash"},
		{"_leading", "starts with underscore"},
		{".leading", "starts with dot"},
		{strings.Repeat("a", 65), "exceeds 64 chars"},
		{"step\ttab", "contains tab"},
		{"step\nnewline", "contains newline"},
	}
	for _, tc := range cases {
		if err := validateWorkflowStepID(tc.id); err == nil {
			t.Errorf("expected %q (%s) to be invalid", tc.id, tc.desc)
		}
	}
}

func TestValidateWorkflowStepID_EmptyReturnsSpecificError(t *testing.T) {
	err := validateWorkflowStepID("")
	if err == nil || err.Error() != "workflow step id required" {
		t.Fatalf("expected 'workflow step id required', got: %v", err)
	}
}

func TestValidateWorkflowStepID_TooLongContainsLimit(t *testing.T) {
	long := strings.Repeat("x", 65)
	err := validateWorkflowStepID(long)
	if err == nil || !strings.Contains(err.Error(), "64") {
		t.Fatalf("expected error mentioning 64 char limit, got: %v", err)
	}
}

func TestValidateWorkflowStepID_BadPatternContainsRegex(t *testing.T) {
	err := validateWorkflowStepID("bad@id")
	if err == nil || !strings.Contains(err.Error(), "must match") {
		t.Fatalf("expected error mentioning pattern, got: %v", err)
	}
}

func TestValidateWorkflowSteps_AllValid(t *testing.T) {
	steps := map[string]wf.Step{
		"step1": {Type: "job"},
		"step2": {Type: "condition"},
		"a.b-c": {Type: "notify"},
	}
	if err := validateWorkflowSteps(steps); err != nil {
		t.Fatalf("expected valid, got: %v", err)
	}
}

func TestValidateWorkflowSteps_OneInvalid(t *testing.T) {
	steps := map[string]wf.Step{
		"valid":   {Type: "job"},
		"bad id!": {Type: "job"},
	}
	if err := validateWorkflowSteps(steps); err == nil {
		t.Fatal("expected error for invalid step ID")
	}
}

func TestValidateWorkflowStepMap_AllValid(t *testing.T) {
	steps := map[string]any{
		"step1": map[string]any{"type": "job"},
		"step2": map[string]any{"type": "condition"},
	}
	if err := validateWorkflowStepMap(steps); err != nil {
		t.Fatalf("expected valid, got: %v", err)
	}
}

func TestValidateWorkflowStepMap_OneInvalid(t *testing.T) {
	steps := map[string]any{
		"valid":  map[string]any{},
		"no/way": map[string]any{},
	}
	if err := validateWorkflowStepMap(steps); err == nil {
		t.Fatal("expected error for invalid step ID")
	}
}

func TestValidateWorkflowStepMap_Empty(t *testing.T) {
	steps := map[string]any{}
	if err := validateWorkflowStepMap(steps); err != nil {
		t.Fatalf("expected empty map to be valid, got: %v", err)
	}
}

// --- DAG validation tests ---

func TestValidateDAG_SimpleCycle(t *testing.T) {
	steps := map[string]wf.Step{
		"A": {DependsOn: []string{"B"}},
		"B": {DependsOn: []string{"A"}},
	}
	err := validateDAG(steps)
	if err == nil {
		t.Fatal("expected cycle error")
	}
	if !strings.Contains(err.Error(), "circular dependency") {
		t.Fatalf("expected circular dependency message, got: %v", err)
	}
}

func TestValidateDAG_ComplexCycle(t *testing.T) {
	steps := map[string]wf.Step{
		"A": {DependsOn: []string{"C"}},
		"B": {DependsOn: []string{"A"}},
		"C": {DependsOn: []string{"B"}},
	}
	err := validateDAG(steps)
	if err == nil {
		t.Fatal("expected cycle error for A->C->B->A")
	}
	if !strings.Contains(err.Error(), "circular dependency") {
		t.Fatalf("expected circular dependency message, got: %v", err)
	}
}

func TestValidateDAG_SelfReference(t *testing.T) {
	steps := map[string]wf.Step{
		"A": {DependsOn: []string{"A"}},
	}
	err := validateDAG(steps)
	if err == nil {
		t.Fatal("expected cycle error for self-reference")
	}
	if !strings.Contains(err.Error(), "circular dependency") {
		t.Fatalf("expected circular dependency message, got: %v", err)
	}
}

func TestValidateDAG_DanglingReference(t *testing.T) {
	steps := map[string]wf.Step{
		"A": {DependsOn: []string{"nonexistent"}},
	}
	err := validateDAG(steps)
	if err == nil {
		t.Fatal("expected dangling reference error")
	}
	if !strings.Contains(err.Error(), "non-existent step") {
		t.Fatalf("expected non-existent step message, got: %v", err)
	}
}

func TestValidateDAG_Diamond_NoCycle(t *testing.T) {
	steps := map[string]wf.Step{
		"A": {},
		"B": {DependsOn: []string{"A"}},
		"C": {DependsOn: []string{"A"}},
		"D": {DependsOn: []string{"B", "C"}},
	}
	if err := validateDAG(steps); err != nil {
		t.Fatalf("diamond DAG should be valid, got: %v", err)
	}
}

func TestValidateDAG_DeepChain(t *testing.T) {
	steps := map[string]wf.Step{
		"s1": {},
		"s2": {DependsOn: []string{"s1"}},
		"s3": {DependsOn: []string{"s2"}},
		"s4": {DependsOn: []string{"s3"}},
		"s5": {DependsOn: []string{"s4"}},
	}
	if err := validateDAG(steps); err != nil {
		t.Fatalf("deep chain should be valid, got: %v", err)
	}
}

func TestValidateDAG_EmptySteps(t *testing.T) {
	if err := validateDAG(map[string]wf.Step{}); err != nil {
		t.Fatalf("empty map should be valid, got: %v", err)
	}
}

func TestValidateDAGPtr_CycleDetected(t *testing.T) {
	steps := map[string]*wf.Step{
		"X": {DependsOn: []string{"Y"}},
		"Y": {DependsOn: []string{"X"}},
	}
	err := validateDAG(flattenWorkflowSteps(steps))
	if err == nil {
		t.Fatal("expected cycle error in pointer variant")
	}
	if !strings.Contains(err.Error(), "circular dependency") {
		t.Fatalf("expected circular dependency message, got: %v", err)
	}
}

func TestValidateDAGPtr_NilStepSkipped(t *testing.T) {
	steps := map[string]*wf.Step{
		"A": nil,
		"B": {DependsOn: []string{}},
	}
	if err := validateDAG(flattenWorkflowSteps(steps)); err != nil {
		t.Fatalf("nil step should be skipped, got: %v", err)
	}
}

func flattenWorkflowSteps(steps map[string]*wf.Step) map[string]wf.Step {
	flat := make(map[string]wf.Step, len(steps))
	for id, step := range steps {
		if step == nil {
			continue
		}
		flat[id] = *step
	}
	return flat
}
