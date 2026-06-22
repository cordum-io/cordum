package claude

import "testing"

// TestRiskyPreToolUse locks the classification used by the localDevEnforce
// degrade-closed path (handleAgentdError, runner.go:160-173). File-mutating
// Claude tools (Write/Edit/MultiEdit/NotebookEdit) must be classified risky so
// the degrade-closed path covers edits, not just shell — while the existing
// Bash rm-rf detection and the fail-closed-on-empty behavior are preserved and
// read-only tools stay non-risky.
func TestRiskyPreToolUse(t *testing.T) {
	cases := []struct {
		name  string
		input HookInput
		want  bool
	}{
		// File-mutating tools => risky (the gap this task closes).
		{"write", HookInput{ToolName: "Write", ToolInput: map[string]any{"file_path": "/tmp/a", "content": "x"}}, true},
		{"edit", HookInput{ToolName: "Edit", ToolInput: map[string]any{"file_path": "/tmp/a"}}, true},
		{"multiedit", HookInput{ToolName: "MultiEdit", ToolInput: map[string]any{"file_path": "/tmp/a"}}, true},
		{"notebookedit", HookInput{ToolName: "NotebookEdit", ToolInput: map[string]any{"notebook_path": "/tmp/a.ipynb"}}, true},

		// Case-insensitivity: lower and upper variants still classify risky.
		{"write_lowercase", HookInput{ToolName: "write", ToolInput: map[string]any{"file_path": "/tmp/a"}}, true},
		{"edit_lowercase", HookInput{ToolName: "edit", ToolInput: map[string]any{"file_path": "/tmp/a"}}, true},
		{"write_uppercase", HookInput{ToolName: "WRITE", ToolInput: map[string]any{"file_path": "/tmp/a"}}, true},
		{"notebookedit_lowercase", HookInput{ToolName: "notebookedit", ToolInput: map[string]any{"notebook_path": "/tmp/a.ipynb"}}, true},

		// Bash rm-rf family => risky (preserved exactly).
		{"bash_rm_rf", HookInput{ToolName: "Bash", ToolInput: map[string]any{"command": "rm -rf /tmp/x"}}, true},
		{"bash_rm_fr", HookInput{ToolName: "Bash", ToolInput: map[string]any{"command": "rm -fr /tmp/x"}}, true},
		{"bash_sudo_rm_rf", HookInput{ToolName: "Bash", ToolInput: map[string]any{"command": "sudo rm -rf /"}}, true},
		{"bash_doas_rm_rf", HookInput{ToolName: "Bash", ToolInput: map[string]any{"command": "doas rm -rf /"}}, true},

		// Bash, but not destructive => not risky (preserved exactly).
		{"bash_npm_test", HookInput{ToolName: "Bash", ToolInput: map[string]any{"command": "npm test"}}, false},

		// Bash fail-closed-on-unknown-shape => risky (preserved exactly).
		{"bash_missing_command", HookInput{ToolName: "Bash", ToolInput: map[string]any{}}, true},
		{"bash_empty_command", HookInput{ToolName: "Bash", ToolInput: map[string]any{"command": ""}}, true},
		{"bash_nonstring_command", HookInput{ToolName: "Bash", ToolInput: map[string]any{"command": 42}}, true},

		// Empty tool name => risky (fail closed on unknown; preserved exactly).
		{"empty_tool_name", HookInput{ToolName: "", ToolInput: map[string]any{}}, true},

		// Read-only tools must stay non-risky (do NOT turn the degrade path into
		// deny-everything).
		{"read", HookInput{ToolName: "Read", ToolInput: map[string]any{"file_path": "/tmp/a"}}, false},
		{"grep", HookInput{ToolName: "Grep", ToolInput: map[string]any{"pattern": "x"}}, false},
		{"glob", HookInput{ToolName: "Glob", ToolInput: map[string]any{"pattern": "*"}}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := riskyPreToolUse(tc.input); got != tc.want {
				t.Errorf("riskyPreToolUse(ToolName=%q) = %v, want %v", tc.input.ToolName, got, tc.want)
			}
		})
	}
}
