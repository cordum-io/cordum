package gateway

import (
	"slices"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/infra/config"
)

func TestMergeTenantPolicies(t *testing.T) {
	base := map[string]config.TenantPolicy{
		"default": {
			AllowTopics:   []string{"job.a"},
			MaxConcurrent: 10,
			MCP: config.MCPPolicy{
				AllowServers: []string{"a.example.com"},
			},
		},
	}
	extra := map[string]config.TenantPolicy{
		"default": {
			AllowTopics:   []string{"job.b"},
			MaxConcurrent: 5,
			MCP: config.MCPPolicy{
				DenyServers: []string{"b.example.com"},
			},
		},
	}
	merged := policybundles.MergeTenantPolicies(base, extra)
	policy := merged["default"]
	if len(policy.AllowTopics) != 2 {
		t.Fatalf("expected allow topics merged, got %#v", policy.AllowTopics)
	}
	if policy.MaxConcurrent != 5 {
		t.Fatalf("expected lower max concurrent applied, got %d", policy.MaxConcurrent)
	}
	if len(policy.MCP.AllowServers) != 1 || len(policy.MCP.DenyServers) != 1 {
		t.Fatalf("expected mcp policy merged")
	}
}

func TestMergeMCPPolicy(t *testing.T) {
	base := config.MCPPolicy{AllowTools: []string{"read"}}
	extra := config.MCPPolicy{AllowTools: []string{"write"}, DenyTools: []string{"delete"}}
	merged := policybundles.MergeMCPPolicy(base, extra)
	if len(merged.AllowTools) != 2 || len(merged.DenyTools) != 1 {
		t.Fatalf("unexpected mcp merge: %#v", merged)
	}
}

func TestPickLabel(t *testing.T) {
	labels := map[string]string{"a": "1", "b": "2"}
	if got := policybundles.PickLabel(labels, "b", "a"); got != "2" {
		t.Fatalf("expected first matching label")
	}
	if got := policybundles.PickLabel(labels, "missing"); got != "" {
		t.Fatalf("expected empty for missing labels")
	}
}

func TestMatchAny(t *testing.T) {
	if !policybundles.MatchAny([]string{"job.*"}, "job.test") {
		t.Fatalf("expected wildcard match")
	}
	if policybundles.MatchAny([]string{"job.*"}, "") {
		t.Fatalf("expected empty value to fail")
	}
	if policybundles.MatchAny([]string{"[invalid"}, "job.test") {
		t.Fatalf("expected invalid pattern to fail")
	}
}

func TestConstraintsConversion(t *testing.T) {
	if policybundles.ToProtoConstraints(config.PolicyConstraints{}) != nil {
		t.Fatalf("expected nil proto constraints for empty config")
	}
	converted := policybundles.ToProtoConstraints(config.PolicyConstraints{
		Budgets: config.BudgetConstraints{MaxRuntimeMs: 1000, MaxRetries: 2},
		Sandbox: config.SandboxProfile{
			Isolated:         true,
			NetworkAllowlist: []string{"api.cordum.io"},
			FsReadOnly:       []string{"/workspace"},
		},
		Toolchain:      config.ToolchainConstraints{AllowedTools: []string{"Bash"}, AllowedCommands: []string{"npm test"}},
		Diff:           config.DiffConstraints{MaxFiles: 3, DenyPathGlobs: []string{"secrets/*"}},
		RedactionLevel: "strict",
	})
	if converted == nil || converted.GetBudgets().GetMaxRuntimeMs() != 1000 || converted.GetBudgets().GetMaxRetries() != 2 {
		t.Fatalf("expected typed policy-bundle budget constraints converted, got %#v", converted)
	}
	if !converted.GetSandbox().GetIsolated() || !slices.Equal(converted.GetSandbox().GetNetworkAllowlist(), []string{"api.cordum.io"}) {
		t.Fatalf("expected typed sandbox constraints converted, got %#v", converted.GetSandbox())
	}
	if !slices.Equal(converted.GetToolchain().GetAllowedCommands(), []string{"npm test"}) {
		t.Fatalf("expected typed toolchain constraints converted, got %#v", converted.GetToolchain())
	}
	if converted.GetDiff().GetMaxFiles() != 3 || !slices.Equal(converted.GetDiff().GetDenyPathGlobs(), []string{"secrets/*"}) {
		t.Fatalf("expected typed diff constraints converted, got %#v", converted.GetDiff())
	}
	if converted.GetRedactionLevel() != "strict" {
		t.Fatalf("redaction_level = %q, want strict", converted.GetRedactionLevel())
	}
}

func TestStringSliceFromAny(t *testing.T) {
	out := policybundles.StringSliceFromAny([]any{"a", " b ", 3})
	if len(out) != 3 || out[1] != "b" {
		t.Fatalf("unexpected slice from any: %#v", out)
	}
	out = policybundles.StringSliceFromAny([]string{"x", "y"})
	if len(out) != 2 || out[0] != "x" {
		t.Fatalf("unexpected slice from strings: %#v", out)
	}
	if policybundles.StringSliceFromAny("bad") != nil {
		t.Fatalf("expected nil for unsupported type")
	}
}

func TestLegacyPolicyRules(t *testing.T) {
	tenants := map[string]any{
		"default": map[string]any{
			"allow_topics": []any{"job.allow"},
			"deny_topics":  []any{"job.deny"},
		},
	}
	rules := policybundles.LegacyPolicyRules(tenants)
	if len(rules) != 2 {
		t.Fatalf("expected 2 legacy rules, got %d", len(rules))
	}
	if rules[0]["id"] == "" || rules[1]["id"] == "" {
		t.Fatalf("expected legacy rule ids")
	}
}
