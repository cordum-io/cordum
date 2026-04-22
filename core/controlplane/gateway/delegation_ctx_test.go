package gateway

import (
	"testing"
	"time"

	"github.com/cordum/cordum/core/auth/delegation"
	"github.com/cordum/cordum/core/infra/config"
)

func TestProjectVerifiedDelegationContextUsesAgentChain(t *testing.T) {
	verified := delegation.VerifiedToken{
		ChainDepth:      2,
		AllowedActions:  []string{"read", "write"},
		Audience:        "agent-b",
		JTI:             "jti-child",
		ExpiresAt:       time.Date(2026, 4, 21, 13, 14, 15, 0, time.UTC),
		DelegationChain: []delegation.ChainLink{{AgentID: "agent-a", IssuedBy: "cordum"}, {AgentID: "agent-b", IssuedBy: "agent-a"}},
	}

	got := projectVerifiedDelegationContext(verified)
	if got == nil {
		t.Fatal("expected delegation context")
	}
	if got.Depth != 2 {
		t.Fatalf("Depth = %d, want 2", got.Depth)
	}
	if got.RootIssuer != "agent-a" {
		t.Fatalf("RootIssuer = %q, want agent-a", got.RootIssuer)
	}
	if got.ParentIssuer != "agent-b" {
		t.Fatalf("ParentIssuer = %q, want agent-b", got.ParentIssuer)
	}
	if len(got.IssuerChain) != 2 || got.IssuerChain[0] != "agent-a" || got.IssuerChain[1] != "agent-b" {
		t.Fatalf("IssuerChain = %#v, want [agent-a agent-b]", got.IssuerChain)
	}
	if len(got.Scope) != 2 || got.Scope[0] != "read" || got.Scope[1] != "write" {
		t.Fatalf("Scope = %#v, want [read write]", got.Scope)
	}
	if got.JTI != "jti-child" {
		t.Fatalf("JTI = %q, want jti-child", got.JTI)
	}
	if got.Audience != "agent-b" {
		t.Fatalf("Audience = %q, want agent-b", got.Audience)
	}
	if got.ExpiresAt != "2026-04-21T13:14:15Z" {
		t.Fatalf("ExpiresAt = %q, want 2026-04-21T13:14:15Z", got.ExpiresAt)
	}
}

func TestApplyDelegationContextLabelsIncludesParentJTIAndRouting(t *testing.T) {
	labels := applyDelegationContextLabels(map[string]string{"existing": "ok"}, &config.DelegationContext{
		Depth:        2,
		IssuerChain:  []string{"agent-a", "agent-b"},
		Scope:        []string{"read"},
		RootIssuer:   "agent-a",
		ParentIssuer: "agent-b",
		JTI:          "dlg-123",
		ExpiresAt:    "2026-04-21T13:14:15Z",
		Audience:     "agent-b",
	}, "agent-b")

	want := map[string]string{
		"existing":                         "ok",
		config.LabelDelegationDepth:        "2",
		config.LabelDelegationIssuer:       "agent-a",
		config.LabelDelegationIssuerChain:  "agent-a,agent-b",
		config.LabelDelegationScope:        "read",
		config.LabelDelegationParentIssuer: "agent-b",
		config.LabelDelegationJTI:          "dlg-123",
		config.LabelDelegationExpiresAt:    "2026-04-21T13:14:15Z",
		config.LabelDelegationAudience:     "agent-b",
		config.LabelDelegationSubject:      "agent-b",
	}
	for key, value := range want {
		if got := labels[key]; got != value {
			t.Fatalf("%s = %q, want %q", key, got, value)
		}
	}
}
