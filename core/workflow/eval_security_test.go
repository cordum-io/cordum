package workflow

import (
	"strings"
	"testing"
)

func TestExpressionLengthLimit(t *testing.T) {
	// Expression exceeding MaxExprLength should be rejected before parsing
	long := strings.Repeat("x", defaultSandbox.MaxExprLength+1)
	_, err := Eval(long, map[string]any{})
	if err == nil {
		t.Fatal("expected error for oversized expression")
	}
	if !strings.Contains(err.Error(), "maximum length") {
		t.Fatalf("expected length error, got: %v", err)
	}
}

func TestBlockedPatterns(t *testing.T) {
	tests := []string{
		"import('os')",
		"require('fs')",
		"reflect.TypeOf(ctx)",
		"unsafe.Pointer(0)",
		"runtime.GOMAXPROCS(0)",
		"os.Exit(1)",
		"exec('cmd')",
		"ctx.__proto__.constructor",
	}
	for _, expr := range tests {
		t.Run(expr, func(t *testing.T) {
			_, err := Eval(expr, map[string]any{"ctx": map[string]any{}})
			if err == nil {
				t.Fatalf("expected blocked pattern error for %q", expr)
			}
			if !strings.Contains(err.Error(), "blocked pattern") {
				t.Fatalf("expected blocked pattern error, got: %v", err)
			}
		})
	}
}

func TestAllowedBuiltins(t *testing.T) {
	env := map[string]any{
		"ctx": map[string]any{
			"items": []any{"a", "b", "c"},
			"name":  "hello",
			"flags": []any{},
		},
	}

	tests := []struct {
		expr     string
		expected any
	}{
		{"length(ctx.items)", 3},
		{"length(ctx.name)", 5},
		{"length(ctx.flags)", 0},
		{"first(ctx.items)", "a"},
		{"1 + 2", 3},
		{"ctx.name == 'hello'", true},
		{"ctx.name != 'world'", true},
		{"length(ctx.items) > 0", true},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			result, err := Eval(tt.expr, env)
			if err != nil {
				t.Fatalf("expected success for %q, got error: %v", tt.expr, err)
			}
			if result != tt.expected {
				t.Fatalf("expected %v for %q, got %v", tt.expected, tt.expr, result)
			}
		})
	}
}

func TestErrorMessageSanitization(t *testing.T) {
	// Trigger "cannot fetch" error - should be sanitized
	env := map[string]any{"ctx": map[string]any{}}
	_, err := Eval("ctx.missing.deep.path", env)
	if err == nil {
		t.Fatal("expected error for nil property access")
	}
	msg := err.Error()
	// Should not contain raw "cannot fetch" from expr-lang
	if strings.Contains(msg, "cannot fetch") {
		t.Fatalf("error message leaks internal details: %v", err)
	}
	// Should contain sanitized message
	if !strings.Contains(msg, "property access failed") && !strings.Contains(msg, "evaluation failed") {
		t.Fatalf("expected sanitized error, got: %v", err)
	}
}

func TestNilContextKeys(t *testing.T) {
	// Accessing missing keys should not panic
	env := map[string]any{"ctx": map[string]any{}}

	// These should return nil or error, never panic
	result, err := Eval("ctx.nonexistent", env)
	if err != nil {
		t.Fatalf("expected nil for missing key, got error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil for missing key, got: %v", result)
	}
}

func TestBackwardCompatibility(t *testing.T) {
	// Test expression patterns from actual GTM workflow YAML files
	env := map[string]any{
		"ctx": map[string]any{
			"research": map[string]any{
				"qualification":     map[string]any{"has_existing_automation": true},
				"recommended_wedge": "governed-mcp",
				"founder_summary":   "Strong fit for MCP governance",
				"current_stack":     []any{"langchain", "openai"},
				"known_agents":      []any{"chatbot-v1"},
			},
			"kill_eval": map[string]any{
				"flags":        []any{},
				"max_severity": "warning",
			},
			"gates": map[string]any{
				"viable": true,
			},
			"draft": map[string]any{
				"subject": "Intro from Cordum",
				"body":    "Hi there",
			},
			"approved_draft": map[string]any{
				"subject": "Updated subject",
				"body":    "Updated body",
			},
		},
		"input": map[string]any{
			"account_id":    "acme-corp",
			"company_url":   "https://acme.com",
			"contact_email": "jane@acme.com",
		},
		"steps": map[string]any{},
	}

	tests := []struct {
		name string
		expr string
	}{
		{"research qualification", "ctx.research.qualification"},
		{"kill flags length", "length(ctx.kill_eval.flags) == 0"},
		{"viable gate", "ctx.gates.viable == true"},
		{"kill severity", "ctx.kill_eval.max_severity != 'high'"},
		{"boolean OR condition", "length(ctx.kill_eval.flags) == 0 || ctx.kill_eval.max_severity != 'high'"},
		{"ternary nil check", "ctx.approved_draft != nil ? ctx.approved_draft.subject : ctx.draft.subject"},
		{"input access", "input.account_id"},
		{"nested object", "ctx.research.recommended_wedge"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Eval(tt.expr, env)
			if err != nil {
				t.Fatalf("backward compatibility failed for %q: %v", tt.expr, err)
			}
		})
	}
}

func TestForEachResultSizeLimit(t *testing.T) {
	// Create array larger than MaxResultItems
	oldMax := defaultSandbox.MaxResultItems
	defaultSandbox.MaxResultItems = 5
	defer func() { defaultSandbox.MaxResultItems = oldMax }()

	large := make([]any, 10)
	for i := range large {
		large[i] = i
	}
	scope := map[string]any{"items": large}

	result, err := evalForEach("items", scope)
	if err != nil {
		t.Fatalf("expected truncation not error, got: %v", err)
	}
	if len(result) != 5 {
		t.Fatalf("expected truncated to 5, got %d", len(result))
	}
}

func TestSandboxConfigDefaults(t *testing.T) {
	cfg := DefaultSandboxConfig()
	if cfg.MaxExprLength != 4096 {
		t.Fatalf("expected MaxExprLength 4096, got %d", cfg.MaxExprLength)
	}
	if cfg.MaxResultItems != 10000 {
		t.Fatalf("expected MaxResultItems 10000, got %d", cfg.MaxResultItems)
	}
	if len(cfg.AllowedVars) == 0 {
		t.Fatal("expected non-empty AllowedVars")
	}
}

func TestSandboxConfigFromEnv(t *testing.T) {
	t.Setenv("WORKFLOW_EXPR_MAX_LENGTH", "2048")
	t.Setenv("WORKFLOW_EXPR_TIMEOUT_MS", "50")
	t.Setenv("WORKFLOW_EXPR_MAX_RESULT_ITEMS", "500")

	cfg := SandboxConfigFromEnv()
	if cfg.MaxExprLength != 2048 {
		t.Fatalf("expected MaxExprLength 2048 from env, got %d", cfg.MaxExprLength)
	}
	if cfg.MaxResultItems != 500 {
		t.Fatalf("expected MaxResultItems 500 from env, got %d", cfg.MaxResultItems)
	}
}
