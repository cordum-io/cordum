package mcp

import (
	"reflect"
	"sort"
	"testing"
)

func toolNames(tools []Tool) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Name)
	}
	sort.Strings(out)
	return out
}

var filterCatalog = []Tool{
	{Name: "fs.read", RiskTier: "low"},
	{Name: "fs.write", RiskTier: "medium"},
	{Name: "jobs.submit", RiskTier: "medium"},
	{Name: "jobs.delete", RiskTier: "high"},
	{Name: "nuke.everything", RiskTier: "critical"},
	{Name: "pii.read", RiskTier: "medium", DataClassifications: []string{"pii"}},
	{Name: "phi.export", RiskTier: "high", DataClassifications: []string{"phi", "pii"}},
	{Name: "untagged.tool"}, // empty RiskTier → fail-closed as high
}

func TestFilterForIdentity_NilOrEmpty(t *testing.T) {
	t.Parallel()

	if got := FilterForIdentity(filterCatalog, nil); len(got) != 0 {
		t.Fatalf("nil identity: want empty, got %v", toolNames(got))
	}
	// Empty identity → unknown tier → zero tools.
	if got := FilterForIdentity(filterCatalog, &AgentIdentity{}); len(got) != 0 {
		t.Fatalf("empty identity: want empty, got %v", toolNames(got))
	}
	// Has tier but no AllowedTools → still zero (opt-in).
	if got := FilterForIdentity(filterCatalog, &AgentIdentity{RiskTier: "critical"}); len(got) != 0 {
		t.Fatalf("no allowed tools: want empty, got %v", toolNames(got))
	}
}

func TestFilterForIdentity_AllowedToolsGlob(t *testing.T) {
	t.Parallel()

	id := &AgentIdentity{
		RiskTier:     "critical",
		AllowedTools: []string{"fs.*", "jobs.submit"},
	}
	got := toolNames(FilterForIdentity(filterCatalog, id))
	want := []string{"fs.read", "fs.write", "jobs.submit"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("glob filter: want %v, got %v", want, got)
	}
}

func TestFilterForIdentity_RiskTierCeiling(t *testing.T) {
	t.Parallel()

	id := &AgentIdentity{
		RiskTier:     "medium",
		AllowedTools: []string{"*"},
	}
	got := toolNames(FilterForIdentity(filterCatalog, id))
	// medium can call low+medium. high, critical, and untagged (defaults high) are denied.
	// pii.read requires pii classification which this identity doesn't have.
	want := []string{"fs.read", "fs.write", "jobs.submit"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("risk-tier filter: want %v, got %v", want, got)
	}
}

func TestFilterForIdentity_DataClassificationsSuperset(t *testing.T) {
	t.Parallel()

	// Actor has pii but not phi — can see pii.read, not phi.export.
	id := &AgentIdentity{
		RiskTier:            "critical",
		AllowedTools:        []string{"*"},
		DataClassifications: []string{"pii"},
	}
	got := toolNames(FilterForIdentity(filterCatalog, id))
	for _, name := range got {
		if name == "phi.export" {
			t.Fatalf("phi.export must be filtered out when actor lacks phi classification")
		}
	}
	// pii.read should be visible.
	if !containsName(got, "pii.read") {
		t.Fatalf("pii.read must be visible: got %v", got)
	}

	// Full classifications admits both.
	id.DataClassifications = []string{"pii", "phi"}
	got = toolNames(FilterForIdentity(filterCatalog, id))
	if !containsName(got, "pii.read") || !containsName(got, "phi.export") {
		t.Fatalf("full classifications should admit pii.read and phi.export: got %v", got)
	}
}

func TestFilterForIdentity_UntaggedToolDefaultsHigh(t *testing.T) {
	t.Parallel()

	medium := &AgentIdentity{RiskTier: "medium", AllowedTools: []string{"untagged.tool"}}
	if got := FilterForIdentity(filterCatalog, medium); len(got) != 0 {
		t.Fatalf("medium actor must not see untagged (defaults high) tool: got %v", toolNames(got))
	}
	high := &AgentIdentity{RiskTier: "high", AllowedTools: []string{"untagged.tool"}}
	got := toolNames(FilterForIdentity(filterCatalog, high))
	if !reflect.DeepEqual(got, []string{"untagged.tool"}) {
		t.Fatalf("high actor should see untagged tool: got %v", got)
	}
}

func TestFilterForIdentity_AdminEverything(t *testing.T) {
	t.Parallel()

	admin := &AgentIdentity{
		ID:                  "admin",
		RiskTier:            "critical",
		AllowedTools:        []string{"*"},
		DataClassifications: []string{"pii", "phi", "secrets"},
	}
	got := FilterForIdentity(filterCatalog, admin)
	if len(got) != len(filterCatalog) {
		t.Fatalf("admin should see full catalog (%d), got %d: %v", len(filterCatalog), len(got), toolNames(got))
	}
}

func TestEvaluateForIdentity_Reasons(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		id   *AgentIdentity
		tool Tool
		want DenyReason
	}{
		{"nil_identity", nil, Tool{Name: "fs.read", RiskTier: "low"}, DenyReasonNoIdentity},
		{"unknown_tier", &AgentIdentity{AllowedTools: []string{"*"}}, Tool{Name: "fs.read", RiskTier: "low"}, DenyReasonNoIdentity},
		{"not_in_allowlist",
			&AgentIdentity{RiskTier: "critical", AllowedTools: []string{"fs.*"}},
			Tool{Name: "jobs.submit", RiskTier: "low"},
			DenyReasonNotInAllowedList,
		},
		{"tier_too_low",
			&AgentIdentity{RiskTier: "low", AllowedTools: []string{"*"}},
			Tool{Name: "nuke", RiskTier: "critical"},
			DenyReasonRiskTierTooLow,
		},
		{"missing_classification",
			&AgentIdentity{RiskTier: "critical", AllowedTools: []string{"*"}},
			Tool{Name: "phi.export", RiskTier: "low", DataClassifications: []string{"phi"}},
			DenyReasonMissingDataClassification,
		},
		{"allowed",
			&AgentIdentity{RiskTier: "critical", AllowedTools: []string{"*"}, DataClassifications: []string{"phi"}},
			Tool{Name: "phi.export", RiskTier: "low", DataClassifications: []string{"phi"}},
			DenyReasonNone,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EvaluateForIdentity(tc.tool, tc.id); got != tc.want {
				t.Fatalf("want %q, got %q", tc.want, got)
			}
		})
	}
}

func TestFilterForIdentity_CaseInsensitiveClassifications(t *testing.T) {
	t.Parallel()

	id := &AgentIdentity{
		RiskTier:            "critical",
		AllowedTools:        []string{"pii.read"},
		DataClassifications: []string{"PII"}, // uppercase
	}
	got := FilterForIdentity([]Tool{{Name: "pii.read", RiskTier: "low", DataClassifications: []string{"pii"}}}, id)
	if len(got) != 1 {
		t.Fatalf("case-insensitive classifications should match: got %v", toolNames(got))
	}
}

func containsName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}
