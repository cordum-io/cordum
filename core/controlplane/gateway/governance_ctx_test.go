package gateway

import (
	"errors"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/config"
)

func TestRejectReservedGovernanceLabels(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		labels  map[string]string
		wantErr error
	}{
		{"nil map", nil, nil},
		{"empty map", map[string]string{}, nil},
		{"normal labels", map[string]string{"team": "blue", "priority": "high"}, nil},
		{"unrelated underscore label", map[string]string{"_delegation.depth": "2"}, nil},
		{"unrelated content label", map[string]string{"_content.prompt": "..."}, nil},
		{"spoofed _governance.tenant", map[string]string{"_governance.tenant": "victim"}, ErrSpoofedGovernanceLabel},
		{"spoofed _governance.provenance_ref", map[string]string{"_governance.provenance_ref": "fake-001"}, ErrSpoofedGovernanceLabel},
		{"spoofed _ma.parent_agent_id", map[string]string{"_ma.parent_agent_id": "safety-agent"}, ErrSpoofedGovernanceLabel},
		{"spoofed _ma.issuer_root", map[string]string{"_ma.issuer_root": "trusted-root"}, ErrSpoofedGovernanceLabel},
		{"mixed spoof + normal", map[string]string{"team": "blue", "_ma.child_tenant": "victim"}, ErrSpoofedGovernanceLabel},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := rejectReservedGovernanceLabels(tc.labels)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected %v, got %v", tc.wantErr, err)
			}
		})
	}
}

// TestBuildGovernanceInputAuthTenantOverride proves that the
// authenticated tenant from auth.AuthContext is the source of truth —
// the request shape's ChildTenant only sets the CHILD agent's tenant,
// never the operation tenant nor the parent's tenant. A client cannot
// escalate cross-tenant by lying in the body.
func TestBuildGovernanceInputAuthTenantOverride(t *testing.T) {
	t.Parallel()

	authCtx := &auth.AuthContext{
		Tenant:      "tenant-authoritative",
		PrincipalID: "parent-agent-1",
	}

	in := BuildGovernanceInput(BuildGovernanceInputParams{
		Op:           config.GovernanceOpDelegation,
		AuthCtx:      authCtx,
		ChildAgentID: "child-agent-1",
		ChildTenant:  "tenant-victim", // body-claimed — should populate Child.Tenant ONLY, never Tenant or Parent.Tenant
		FreshnessSec: 300,
	})
	if in == nil {
		t.Fatal("expected non-nil governance input")
	}
	if in.Tenant != "tenant-authoritative" {
		t.Errorf("operation tenant: got %q, want %q (auth-derived, body cannot override)", in.Tenant, "tenant-authoritative")
	}
	if in.Parent.Tenant != "tenant-authoritative" {
		t.Errorf("parent.tenant: got %q, want %q", in.Parent.Tenant, "tenant-authoritative")
	}
	if in.Child.Tenant != "tenant-victim" {
		t.Errorf("child.tenant: got %q, want %q (explicit child placement only)", in.Child.Tenant, "tenant-victim")
	}
	if in.Parent.AgentID != "parent-agent-1" {
		t.Errorf("parent.agent_id: got %q, want %q (must come from AuthContext.PrincipalID)", in.Parent.AgentID, "parent-agent-1")
	}
}

func TestBuildGovernanceInputNilAuthContext(t *testing.T) {
	t.Parallel()
	got := BuildGovernanceInput(BuildGovernanceInputParams{Op: config.GovernanceOpDelegation})
	if got != nil {
		t.Fatalf("expected nil when AuthCtx is nil, got %#v", got)
	}
}

func TestBuildGovernanceInputDefaultsChildTenantToParent(t *testing.T) {
	t.Parallel()
	in := BuildGovernanceInput(BuildGovernanceInputParams{
		Op:           config.GovernanceOpHandoff,
		AuthCtx:      &auth.AuthContext{Tenant: "tenant-a", PrincipalID: "p-1"},
		ChildAgentID: "c-1",
		// ChildTenant omitted — should default to parent tenant (same-tenant default)
		FreshnessSec: 60,
	})
	if in == nil {
		t.Fatal("nil input")
	}
	if in.Child.Tenant != "tenant-a" {
		t.Errorf("child.tenant: got %q, want %q (default to parent)", in.Child.Tenant, "tenant-a")
	}
}

func TestProjectGovernanceIssuerChain(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   *config.DelegationContext
		wantLen int
		wantJTI string
	}{
		{"nil", nil, 0, ""},
		{"empty chain", &config.DelegationContext{}, 0, ""},
		{"single hop carries JTI", &config.DelegationContext{
			IssuerChain: []string{"agent-a"},
			RootIssuer:  "root-1",
			JTI:         "jti-abc",
		}, 1, "jti-abc"},
		{"multi-hop only first carries JTI", &config.DelegationContext{
			IssuerChain: []string{"agent-a", "agent-b", "agent-c"},
			RootIssuer:  "root-1",
			JTI:         "jti-abc",
		}, 3, "jti-abc"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := projectGovernanceIssuerChain(tc.input)
			if len(got) != tc.wantLen {
				t.Fatalf("len: got %d, want %d", len(got), tc.wantLen)
			}
			if tc.wantLen == 0 {
				return
			}
			if got[0].JTI != tc.wantJTI {
				t.Errorf("first entry JTI: got %q, want %q", got[0].JTI, tc.wantJTI)
			}
			for i := 1; i < len(got); i++ {
				if got[i].JTI != "" {
					t.Errorf("entry %d JTI: got %q, want empty (only first entry carries JTI)", i, got[i].JTI)
				}
			}
			for i, entry := range got {
				if entry.IssuerRoot != tc.input.RootIssuer {
					t.Errorf("entry %d IssuerRoot: got %q, want %q", i, entry.IssuerRoot, tc.input.RootIssuer)
				}
				if entry.Issuer != tc.input.IssuerChain[i] {
					t.Errorf("entry %d Issuer: got %q, want %q", i, entry.Issuer, tc.input.IssuerChain[i])
				}
			}
		})
	}
}

// TestBuildGovernanceInputProjectsDelegationContext verifies the
// integration path: when DelegCtx is provided, the resulting GovernanceInput
// carries the projected issuer chain — the chain is NOT carried via labels
// or by trusting client text.
func TestBuildGovernanceInputProjectsDelegationContext(t *testing.T) {
	t.Parallel()
	delegCtx := &config.DelegationContext{
		Depth:        2,
		IssuerChain:  []string{"agent-root", "agent-mid"},
		RootIssuer:   "agent-root",
		ParentIssuer: "agent-mid",
		JTI:          "jti-001",
	}
	in := BuildGovernanceInput(BuildGovernanceInputParams{
		Op:           config.GovernanceOpDelegation,
		AuthCtx:      &auth.AuthContext{Tenant: "t", PrincipalID: "p"},
		DelegCtx:     delegCtx,
		ChildAgentID: "c",
		FreshnessSec: 60,
	})
	if in == nil {
		t.Fatal("nil input")
	}
	if len(in.IssuerChain) != 2 {
		t.Fatalf("issuer_chain len: got %d, want 2", len(in.IssuerChain))
	}
	if in.IssuerChain[0].JTI != "jti-001" {
		t.Errorf("first JTI: got %q, want jti-001", in.IssuerChain[0].JTI)
	}
	if in.IssuerChain[1].JTI != "" {
		t.Errorf("second JTI: got %q, want empty", in.IssuerChain[1].JTI)
	}
}
