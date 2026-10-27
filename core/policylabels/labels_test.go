package policylabels_test

import (
	"maps"
	"testing"

	"github.com/cordum/cordum/core/policylabels"
)

func TestWithWorkflowScope(t *testing.T) {
	claimed := map[string]string{
		"workflow_id":     "claimed-id",
		"workflow":        "claimed-name",
		"workflow_run_id": "claimed-run",
		"workflow.owner":  "claimed-owner",
		"run_id":          "run-1",
		"step_id":         "step-1",
		"environment":     "test",
	}
	tests := []struct {
		name       string
		labels     map[string]string
		workflowID string
		want       map[string]string
	}{
		{name: "nil labels without scope", want: map[string]string{}},
		{name: "nil labels with scope", workflowID: " workflow-1 ", want: map[string]string{"workflow_id": "workflow-1"}},
		{
			name:       "drop claimed scope without canonical workflow",
			labels:     claimed,
			workflowID: " \t\n ",
			want:       map[string]string{"run_id": "run-1", "step_id": "step-1", "environment": "test"},
		},
		{
			name:       "replace claimed scope with canonical workflow",
			labels:     claimed,
			workflowID: " workflow-1 ",
			want:       map[string]string{"workflow_id": "workflow-1", "run_id": "run-1", "step_id": "step-1", "environment": "test"},
		},
		{
			name:       "add canonical scope to ordinary labels",
			labels:     map[string]string{"workflowish": "ordinary", "environment": "test"},
			workflowID: "workflow-1",
			want:       map[string]string{"workflow_id": "workflow-1", "workflowish": "ordinary", "environment": "test"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := maps.Clone(tt.labels)
			got := policylabels.WithWorkflowScope(tt.labels, tt.workflowID)
			if got == nil || !maps.Equal(got, tt.want) {
				t.Fatalf("WithWorkflowScope() = %v, want mutable copy %v", got, tt.want)
			}
			if !maps.Equal(tt.labels, before) {
				t.Fatalf("input labels changed: got %v, want %v", tt.labels, before)
			}
			got["copy-only"] = "value"
			delete(got, "environment")
			if !maps.Equal(tt.labels, before) {
				t.Fatalf("output aliases input labels: got %v, want %v", tt.labels, before)
			}
		})
	}
}
