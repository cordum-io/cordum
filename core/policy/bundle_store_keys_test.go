package policy

import "testing"

func TestBundleKey(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want string
	}{
		{name: "simple id", id: "b1", want: "policy:bundle:b1"},
		{name: "uuid id", id: "01H8Z1...EOF", want: "policy:bundle:01H8Z1...EOF"},
		{name: "empty id is allowed at the helper layer", id: "", want: "policy:bundle:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bundleKey(tt.id); got != tt.want {
				t.Errorf("bundleKey(%q) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

func TestBundleVersionKey(t *testing.T) {
	got := bundleVersionKey("b1", "v3")
	want := "policy:bundle:b1:version:v3"
	if got != want {
		t.Errorf("bundleVersionKey(b1, v3) = %q, want %q", got, want)
	}
}

func TestBundleVersionsIndexKey(t *testing.T) {
	got := bundleVersionsIndexKey("b1")
	want := "policy:bundle:b1:versions"
	if got != want {
		t.Errorf("bundleVersionsIndexKey(b1) = %q, want %q", got, want)
	}
}

func TestScopeActiveKey(t *testing.T) {
	tests := []struct {
		name  string
		scope RuleScope
		want  string
	}{
		{
			name:  "tenant scope",
			scope: RuleScope{Kind: RuleScopeTenant, Value: "acme"},
			want:  "policy:scope:tenant:acme:active",
		},
		{
			name:  "global scope (empty value)",
			scope: RuleScope{Kind: RuleScopeGlobal, Value: ""},
			want:  "policy:scope:global::active",
		},
		{
			name:  "edge fleet scope",
			scope: RuleScope{Kind: RuleScopeEdgeFleet, Value: "fleet-1"},
			want:  "policy:scope:edge_fleet:fleet-1:active",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := scopeActiveKey(tt.scope); got != tt.want {
				t.Errorf("scopeActiveKey(%+v) = %q, want %q", tt.scope, got, tt.want)
			}
		})
	}
}

func TestScopeDeploymentHistoryKey(t *testing.T) {
	scope := RuleScope{Kind: RuleScopeTenant, Value: "acme"}
	got := scopeDeploymentHistoryKey(scope)
	want := "policy:scope:tenant:acme:history"
	if got != want {
		t.Errorf("scopeDeploymentHistoryKey(%+v) = %q, want %q", scope, got, want)
	}
}

func TestKeyPrefixIsolation(t *testing.T) {
	// Step-2 inventory confirmed no existing core store uses these prefixes.
	// This test pins the prefix constants so accidental rename is caught.
	if bundleKeyPrefix != "policy:bundle:" {
		t.Errorf("bundleKeyPrefix changed: %q (must stay 'policy:bundle:' to avoid collision audit)", bundleKeyPrefix)
	}
	if scopeKeyPrefix != "policy:scope:" {
		t.Errorf("scopeKeyPrefix changed: %q (must stay 'policy:scope:' to avoid collision audit)", scopeKeyPrefix)
	}
}
