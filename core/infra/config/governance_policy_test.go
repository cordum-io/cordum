package config

import (
	"errors"
	"testing"
)

func TestValidateGovernanceInput(t *testing.T) {
	t.Parallel()

	base := func() *GovernanceInput {
		return &GovernanceInput{
			Operation:          GovernanceOpDelegation,
			Parent:             AgentIdentity{AgentID: "parent-1", Tenant: "tenant-a"},
			Child:              AgentIdentity{AgentID: "child-1", Tenant: "tenant-a"},
			Tenant:             "tenant-a",
			IssuerChain:        []IssuerChainEntry{{IssuerRoot: "root-1", Issuer: "issuer-1", JTI: "jti-1", IssuedAt: 1000, ExpiresAt: 2000}},
			DelegatedScopes:    []string{"jobs:submit"},
			ApprovalRef:        "appr-001",
			ApprovalStatus:     "approved",
			ProvenanceRef:      "prov-001",
			VerifiedAt:         1500,
			FreshnessWindowSec: 300,
		}
	}

	tests := []struct {
		name    string
		mutate  func(*GovernanceInput)
		wantErr error
	}{
		{"nil input", nil, ErrGovernanceEmptyOperation},
		{"empty operation", func(in *GovernanceInput) { in.Operation = "" }, ErrGovernanceEmptyOperation},
		{"invalid operation", func(in *GovernanceInput) { in.Operation = "spoof" }, ErrGovernanceInvalidOperation},
		{"empty tenant", func(in *GovernanceInput) { in.Tenant = "" }, ErrGovernanceEmptyTenant},
		{"empty parent agent_id", func(in *GovernanceInput) { in.Parent.AgentID = "" }, ErrGovernanceEmptyParentAgentID},
		{"empty parent tenant", func(in *GovernanceInput) { in.Parent.Tenant = "" }, ErrGovernanceEmptyParentTenant},
		{"empty child agent_id", func(in *GovernanceInput) { in.Child.AgentID = "" }, ErrGovernanceEmptyChildAgentID},
		{"empty child tenant", func(in *GovernanceInput) { in.Child.Tenant = "" }, ErrGovernanceEmptyChildTenant},
		{"zero freshness on delegation", func(in *GovernanceInput) { in.FreshnessWindowSec = 0 }, ErrGovernanceNegativeFreshnessWindow},
		{"negative freshness on handoff", func(in *GovernanceInput) {
			in.Operation = GovernanceOpHandoff
			in.FreshnessWindowSec = -1
		}, ErrGovernanceNegativeFreshnessWindow},
		{"empty issuer chain on delegation", func(in *GovernanceInput) { in.IssuerChain = nil }, ErrGovernanceEmptyIssuerChain},
		{"chain entry empty issuer", func(in *GovernanceInput) {
			in.IssuerChain = []IssuerChainEntry{{IssuerRoot: "root-1", Issuer: ""}}
		}, ErrGovernanceEmptyIssuerChainEntry},
		{"chain entry empty root", func(in *GovernanceInput) {
			in.IssuerChain = []IssuerChainEntry{{IssuerRoot: "", Issuer: "issuer-1"}}
		}, ErrGovernanceEmptyIssuerChainEntry},
		{"resource delta empty scope", func(in *GovernanceInput) {
			in.ResourceDeltas = []ResourceDelta{{Scope: "", Amount: 1}}
		}, ErrGovernanceInvalidResourceDelta},
		{"resource delta negative amount", func(in *GovernanceInput) {
			in.ResourceDeltas = []ResourceDelta{{Scope: "cpu", Amount: -5}}
		}, ErrGovernanceInvalidResourceDelta},
		{"shared write empty target_key", func(in *GovernanceInput) {
			in.Operation = GovernanceOpSharedContextWrite
			in.WriteKind = SharedMemoryWriteRaw
			in.IssuerChain = nil
		}, ErrGovernanceMalformedSharedMemoryWrite},
		{"shared write empty write_kind", func(in *GovernanceInput) {
			in.Operation = GovernanceOpSharedContextWrite
			in.SharedMemoryTargetKey = "tenant-a/memory/k"
			in.WriteKind = ""
			in.IssuerChain = nil
		}, ErrGovernanceMalformedSharedMemoryWrite},
		{"shared write invalid write_kind", func(in *GovernanceInput) {
			in.Operation = GovernanceOpSharedContextWrite
			in.SharedMemoryTargetKey = "tenant-a/memory/k"
			in.WriteKind = "shared_universe"
			in.IssuerChain = nil
		}, ErrGovernanceInvalidWriteKind},
		{"shared write policy-state without freshness", func(in *GovernanceInput) {
			in.Operation = GovernanceOpSharedContextWrite
			in.SharedMemoryTargetKey = "tenant-a/memory/k"
			in.WriteKind = SharedMemoryWriteSharedPolicyState
			in.PolicyStateMutation = true
			in.FreshnessWindowSec = 0
			in.IssuerChain = nil
		}, ErrGovernanceNegativeFreshnessWindow},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var in *GovernanceInput
			if tc.mutate != nil {
				in = base()
				tc.mutate(in)
			}
			err := ValidateGovernanceInput(in)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestValidateGovernanceInputAccepts(t *testing.T) {
	t.Parallel()

	cases := map[string]*GovernanceInput{
		"valid delegation": {
			Operation:          GovernanceOpDelegation,
			Parent:             AgentIdentity{AgentID: "parent-1", Tenant: "tenant-a"},
			Child:              AgentIdentity{AgentID: "child-1", Tenant: "tenant-a"},
			Tenant:             "tenant-a",
			IssuerChain:        []IssuerChainEntry{{IssuerRoot: "root-1", Issuer: "issuer-1"}},
			FreshnessWindowSec: 300,
		},
		"valid raw shared write": {
			Operation:             GovernanceOpSharedContextWrite,
			Parent:                AgentIdentity{AgentID: "p", Tenant: "t"},
			Child:                 AgentIdentity{AgentID: "c", Tenant: "t"},
			Tenant:                "t",
			SharedMemoryTargetKey: "t/m/k",
			WriteKind:             SharedMemoryWriteRaw,
		},
		"valid policy-state shared write with freshness": {
			Operation:             GovernanceOpSharedContextWrite,
			Parent:                AgentIdentity{AgentID: "p", Tenant: "t"},
			Child:                 AgentIdentity{AgentID: "c", Tenant: "t"},
			Tenant:                "t",
			SharedMemoryTargetKey: "t/m/k",
			WriteKind:             SharedMemoryWriteSharedPolicyState,
			PolicyStateMutation:   true,
			FreshnessWindowSec:    120,
		},
		"valid trust assertion": {
			Operation:          GovernanceOpTrustAssertion,
			Parent:             AgentIdentity{AgentID: "p", Tenant: "t"},
			Child:              AgentIdentity{AgentID: "c", Tenant: "t"},
			Tenant:             "t",
			IssuerChain:        []IssuerChainEntry{{IssuerRoot: "r", Issuer: "i"}},
			FreshnessWindowSec: 60,
		},
		"valid resource allocation with non-empty deltas": {
			Operation:          GovernanceOpResourceAllocation,
			Parent:             AgentIdentity{AgentID: "p", Tenant: "t"},
			Child:              AgentIdentity{AgentID: "c", Tenant: "t"},
			Tenant:             "t",
			IssuerChain:        []IssuerChainEntry{{IssuerRoot: "r", Issuer: "i"}},
			ResourceDeltas:     []ResourceDelta{{Scope: "cpu", Amount: 2}, {Scope: "mem", Capability: "rw"}},
			FreshnessWindowSec: 60,
		},
	}

	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := ValidateGovernanceInput(in); err != nil {
				t.Fatalf("expected nil, got %v", err)
			}
		})
	}
}

func TestIsValidGovernanceOperation(t *testing.T) {
	t.Parallel()
	for _, op := range []GovernanceOperation{
		GovernanceOpDelegation,
		GovernanceOpHandoff,
		GovernanceOpSharedContextWrite,
		GovernanceOpApprovalBypass,
		GovernanceOpResourceAllocation,
		GovernanceOpTrustAssertion,
	} {
		if !isValidGovernanceOperation(op) {
			t.Errorf("expected %q valid", op)
		}
	}
	for _, op := range []GovernanceOperation{"", "spoof", "DELEGATION", "delegate"} {
		if isValidGovernanceOperation(op) {
			t.Errorf("expected %q invalid", op)
		}
	}
}

func TestIsValidSharedMemoryWriteKind(t *testing.T) {
	t.Parallel()
	for _, k := range []SharedMemoryWriteKind{
		SharedMemoryWriteRaw,
		SharedMemoryWriteChat,
		SharedMemoryWriteSharedPolicyState,
		SharedMemoryWriteSharedTrustState,
		SharedMemoryWriteSharedDirective,
	} {
		if !isValidSharedMemoryWriteKind(k) {
			t.Errorf("expected %q valid", k)
		}
	}
	for _, k := range []SharedMemoryWriteKind{"", "shared_universe", "RAW"} {
		if isValidSharedMemoryWriteKind(k) {
			t.Errorf("expected %q invalid", k)
		}
	}
}
