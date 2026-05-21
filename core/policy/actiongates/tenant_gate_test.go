package actiongates

import (
	"context"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func ctxWithAuth(actx *auth.AuthContext) context.Context {
	return context.WithValue(context.Background(), auth.ContextKey{}, actx)
}

type tenantGateCase struct {
	name         string
	actx         *auth.AuthContext
	action       *config.ActionDescriptor
	wantDecision pb.DecisionType
	wantCode     string
	subReasonHas string
}

func runTenantGate(t *testing.T, tc tenantGateCase) {
	t.Helper()
	gate := NewTenantGate()
	in := &config.PolicyInput{Action: tc.action}
	if tc.actx != nil {
		in.Tenant = tc.actx.Tenant
	}
	// EXPLICIT-BYPASS: when tc.actx is nil we hand the gate a context
	// with NO auth.ContextKey{} value attached. This is intentional —
	// the gate is the SECURITY FLOOR for "the request reached us with
	// no AuthContext at all" (auth middleware bug, replay attempt,
	// internal caller forgot to plumb the context value). The bypass
	// is therefore the *positive* exercise of that path; using
	// `ctxWithAuth(nil)` instead would wrap a typed-nil *AuthContext
	// pointer and the gate's `actx, ok := ctx.Value(...)` check would
	// succeed with ok=true — masking the missing-auth code path the
	// `no_authcontext` case in TestTenantGate_UnauthDenies asserts on.
	ctx := context.Background()
	if tc.actx != nil {
		ctx = ctxWithAuth(tc.actx)
	}
	dec := gate.Evaluate(ctx, in)
	if dec.Decision != tc.wantDecision {
		t.Fatalf("decision = %v, want %v (reason=%q subReason=%q)", dec.Decision, tc.wantDecision, dec.Reason, dec.SubReason)
	}
	if tc.wantCode != "" && dec.Code != tc.wantCode {
		t.Fatalf("code = %q, want %q", dec.Code, tc.wantCode)
	}
	if tc.subReasonHas != "" && !strings.Contains(dec.SubReason, tc.subReasonHas) {
		t.Fatalf("subReason = %q, want substring %q", dec.SubReason, tc.subReasonHas)
	}
}

func TestTenantGate_SkipsWhenNoAction(t *testing.T) {
	t.Parallel()
	gate := NewTenantGate()
	// No action -> zero decision (pipeline continues).
	dec := gate.Evaluate(ctxWithAuth(&auth.AuthContext{Tenant: "tnt_a", PrincipalID: "p1"}),
		&config.PolicyInput{Tenant: "tnt_a"})
	if dec.Fired() {
		t.Fatalf("no action: gate fired (Decision=%v)", dec.Decision)
	}
}

func TestTenantGate_UnauthDenies(t *testing.T) {
	t.Parallel()
	// Missing AuthContext on any actioned request -> 401 unauthorized.
	runTenantGate(t, tenantGateCase{
		name:         "no_authcontext",
		actx:         nil,
		action:       &config.ActionDescriptor{Kind: config.ActionKindTenantQuery, Verb: config.ActionVerbRead},
		wantDecision: pb.DecisionType_DECISION_TYPE_DENY,
		wantCode:     CodeUnauthorized,
		subReasonHas: "missing_auth",
	})
	// Auth present but Tenant empty (mis-configured RBAC) -> 401.
	runTenantGate(t, tenantGateCase{
		name:         "empty_tenant",
		actx:         &auth.AuthContext{PrincipalID: "p1"},
		action:       &config.ActionDescriptor{Kind: config.ActionKindTenantQuery, Verb: config.ActionVerbRead},
		wantDecision: pb.DecisionType_DECISION_TYPE_DENY,
		wantCode:     CodeUnauthorized,
		subReasonHas: "missing_auth",
	})
}

func TestTenantGate_CrossTenantDenies(t *testing.T) {
	t.Parallel()
	// auth=A, target.OwnerTenant=B -> DENY.
	runTenantGate(t, tenantGateCase{
		name: "owner_tenant_mismatch",
		actx: &auth.AuthContext{Tenant: "tnt_a", PrincipalID: "p1", Role: "user"},
		action: &config.ActionDescriptor{
			Kind: config.ActionKindMutation, Verb: config.ActionVerbWrite,
			TargetResource: &config.ActionTargetResource{Type: "user", ID: "user_42", OwnerTenant: "tnt_b"},
		},
		wantDecision: pb.DecisionType_DECISION_TYPE_DENY,
		wantCode:     CodeAccessDenied,
		subReasonHas: "cross_tenant",
	})
	// Tenant-prefixed ID mismatch (auth=tnt_a but id=tnt_b_user_42).
	runTenantGate(t, tenantGateCase{
		name: "prefixed_id_mismatch",
		actx: &auth.AuthContext{Tenant: "tnt_a", PrincipalID: "p1", Role: "user"},
		action: &config.ActionDescriptor{
			Kind: config.ActionKindMutation, Verb: config.ActionVerbWrite,
			TargetResource: &config.ActionTargetResource{Type: "user", ID: "tnt_b_user_42"},
		},
		wantDecision: pb.DecisionType_DECISION_TYPE_DENY,
		wantCode:     CodeAccessDenied,
		subReasonHas: "cross_tenant",
	})
	// SuperAdmin bypass (AllowCrossTenant=true) -> ALLOW.
	runTenantGate(t, tenantGateCase{
		name: "superadmin_cross_tenant_allowed",
		actx: &auth.AuthContext{Tenant: "tnt_a", PrincipalID: "p1", Role: "superadmin", AllowCrossTenant: true},
		action: &config.ActionDescriptor{
			Kind: config.ActionKindMutation, Verb: config.ActionVerbWrite,
			TargetResource: &config.ActionTargetResource{Type: "user", ID: "user_42", OwnerTenant: "tnt_b"},
		},
		wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW,
	})
}

func TestTenantGate_WildcardOwnerFiltersDeny(t *testing.T) {
	t.Parallel()
	cases := []tenantGateCase{
		{
			name: "owner_id_star_in_filters",
			actx: &auth.AuthContext{Tenant: "tnt_a", PrincipalID: "p1", Role: "user"},
			action: &config.ActionDescriptor{
				Kind: config.ActionKindTenantQuery, Verb: config.ActionVerbRead,
				Filters: map[string]string{"owner_id": "*"},
			},
			wantDecision: pb.DecisionType_DECISION_TYPE_DENY,
			wantCode:     CodeAccessDenied,
			subReasonHas: "wildcard_owner",
		},
		{
			name: "tenant_id_star_in_filters",
			actx: &auth.AuthContext{Tenant: "tnt_a", PrincipalID: "p1", Role: "user"},
			action: &config.ActionDescriptor{
				Kind: config.ActionKindTenantQuery, Verb: config.ActionVerbRead,
				Filters: map[string]string{"tenant_id": "*"},
			},
			wantDecision: pb.DecisionType_DECISION_TYPE_DENY,
			wantCode:     CodeAccessDenied,
			subReasonHas: "wildcard_owner",
		},
		{
			name: "wildcards_owner_listed",
			actx: &auth.AuthContext{Tenant: "tnt_a", PrincipalID: "p1", Role: "user"},
			action: &config.ActionDescriptor{
				Kind: config.ActionKindTenantQuery, Verb: config.ActionVerbRead,
				Wildcards: []string{"owner_id"},
			},
			wantDecision: pb.DecisionType_DECISION_TYPE_DENY,
			wantCode:     CodeAccessDenied,
			subReasonHas: "wildcard_owner",
		},
		{
			name: "where_1_eq_1_filter",
			actx: &auth.AuthContext{Tenant: "tnt_a", PrincipalID: "p1", Role: "user"},
			action: &config.ActionDescriptor{
				Kind: config.ActionKindTenantQuery, Verb: config.ActionVerbRead,
				Filters: map[string]string{"raw_where": "1=1"},
			},
			wantDecision: pb.DecisionType_DECISION_TYPE_DENY,
			wantCode:     CodeAccessDenied,
			subReasonHas: "raw_where",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); runTenantGate(t, tc) })
	}
}

func TestTenantGate_ArchivedRecordBypass(t *testing.T) {
	t.Parallel()
	cases := []tenantGateCase{
		{
			name: "include_archived_true_normal_user",
			actx: &auth.AuthContext{Tenant: "tnt_a", PrincipalID: "p1", Role: "user"},
			action: &config.ActionDescriptor{
				Kind: config.ActionKindTenantQuery, Verb: config.ActionVerbRead,
				Filters: map[string]string{"include_archived": "true"},
			},
			wantDecision: pb.DecisionType_DECISION_TYPE_DENY,
			wantCode:     CodeAccessDenied,
			subReasonHas: "archived_bypass",
		},
		{
			name: "deleted_at_any",
			actx: &auth.AuthContext{Tenant: "tnt_a", PrincipalID: "p1", Role: "user"},
			action: &config.ActionDescriptor{
				Kind: config.ActionKindTenantQuery, Verb: config.ActionVerbRead,
				Filters: map[string]string{"deleted_at": "any"},
			},
			wantDecision: pb.DecisionType_DECISION_TYPE_DENY,
			wantCode:     CodeAccessDenied,
			subReasonHas: "archived_bypass",
		},
		{
			name: "contradictory_deleted_at",
			actx: &auth.AuthContext{Tenant: "tnt_a", PrincipalID: "p1", Role: "user"},
			action: &config.ActionDescriptor{
				Kind: config.ActionKindTenantQuery, Verb: config.ActionVerbRead,
				Filters: map[string]string{"deleted_at": "IS NULL OR IS NOT NULL"},
			},
			wantDecision: pb.DecisionType_DECISION_TYPE_DENY,
			wantCode:     CodeAccessDenied,
			subReasonHas: "archived_bypass",
		},
		{
			name: "compliance_role_can_query_archived",
			actx: &auth.AuthContext{Tenant: "tnt_a", PrincipalID: "p1", Role: "compliance_admin"},
			action: &config.ActionDescriptor{
				Kind: config.ActionKindTenantQuery, Verb: config.ActionVerbRead,
				Filters: map[string]string{"include_archived": "true"},
			},
			wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW,
		},
		{
			name: "legal_role_can_query_archived",
			actx: &auth.AuthContext{Tenant: "tnt_a", PrincipalID: "p1", Role: "legal_hold"},
			action: &config.ActionDescriptor{
				Kind: config.ActionKindTenantQuery, Verb: config.ActionVerbRead,
				Filters: map[string]string{"deleted_at": "any"},
			},
			wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); runTenantGate(t, tc) })
	}
}

func TestTenantGate_AntiEnumerationRequireHuman(t *testing.T) {
	t.Parallel()
	cases := []tenantGateCase{
		{
			name: "page_size_too_large",
			actx: &auth.AuthContext{Tenant: "tnt_a", PrincipalID: "p1", Role: "user"},
			action: &config.ActionDescriptor{
				Kind: config.ActionKindTenantQuery, Verb: config.ActionVerbRead,
				Args: map[string]any{"page_size": 50000},
			},
			wantDecision: pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
			wantCode:     CodeRequireHuman,
			subReasonHas: "large_enumeration",
		},
		{
			name: "in_clause_too_large",
			actx: &auth.AuthContext{Tenant: "tnt_a", PrincipalID: "p1", Role: "user"},
			action: &config.ActionDescriptor{
				Kind: config.ActionKindTenantQuery, Verb: config.ActionVerbRead,
				Args: map[string]any{"in_clause_size": 5000},
			},
			wantDecision: pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
			wantCode:     CodeRequireHuman,
			subReasonHas: "large_enumeration",
		},
		{
			name: "normal_page_size_allowed",
			actx: &auth.AuthContext{Tenant: "tnt_a", PrincipalID: "p1", Role: "user"},
			action: &config.ActionDescriptor{
				Kind: config.ActionKindTenantQuery, Verb: config.ActionVerbRead,
				Args: map[string]any{"page_size": 50},
			},
			wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW,
		},
		{
			name: "page_size_as_string",
			actx: &auth.AuthContext{Tenant: "tnt_a", PrincipalID: "p1", Role: "user"},
			action: &config.ActionDescriptor{
				Kind: config.ActionKindTenantQuery, Verb: config.ActionVerbRead,
				Args: map[string]any{"page_size": "50000"},
			},
			wantDecision: pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
			wantCode:     CodeRequireHuman,
			subReasonHas: "large_enumeration",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); runTenantGate(t, tc) })
	}
}

func TestTenantGate_AllowsSameTenantAndPaginatedQueries(t *testing.T) {
	t.Parallel()
	cases := []tenantGateCase{
		{
			name: "same_tenant_mutation",
			actx: &auth.AuthContext{Tenant: "tnt_a", PrincipalID: "p1", Role: "user"},
			action: &config.ActionDescriptor{
				Kind: config.ActionKindMutation, Verb: config.ActionVerbWrite,
				TargetResource: &config.ActionTargetResource{Type: "user", ID: "user_42", OwnerTenant: "tnt_a"},
			},
			wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW,
		},
		{
			name: "paginated_list_default",
			actx: &auth.AuthContext{Tenant: "tnt_a", PrincipalID: "p1", Role: "user"},
			action: &config.ActionDescriptor{
				Kind: config.ActionKindTenantQuery, Verb: config.ActionVerbRead,
				Args: map[string]any{"page_size": 50, "page": 1},
			},
			wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW,
		},
		{
			name: "prefixed_id_match_passes",
			actx: &auth.AuthContext{Tenant: "tnt_a", PrincipalID: "p1", Role: "user"},
			action: &config.ActionDescriptor{
				Kind: config.ActionKindMutation, Verb: config.ActionVerbWrite,
				TargetResource: &config.ActionTargetResource{Type: "user", ID: "tnt_a_user_42"},
			},
			wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW,
		},
		{
			name: "sequential_ids_legit_pagination",
			actx: &auth.AuthContext{Tenant: "tnt_a", PrincipalID: "p1", Role: "user"},
			action: &config.ActionDescriptor{
				Kind: config.ActionKindTenantQuery, Verb: config.ActionVerbRead,
				Filters: map[string]string{"order_by": "id", "limit": "50"},
			},
			wantDecision: pb.DecisionType_DECISION_TYPE_ALLOW,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); runTenantGate(t, tc) })
	}
}

func TestTenantGate_FileKindSkips(t *testing.T) {
	t.Parallel()
	// File / URL / MCP kinds are not the tenant gate's domain; it should skip.
	gate := NewTenantGate()
	dec := gate.Evaluate(ctxWithAuth(&auth.AuthContext{Tenant: "tnt_a", PrincipalID: "p1"}),
		&config.PolicyInput{Tenant: "tnt_a", Action: &config.ActionDescriptor{Kind: config.ActionKindFile, TargetPath: "/tmp/x"}})
	if dec.Fired() {
		t.Fatalf("file kind: tenant gate fired (Decision=%v)", dec.Decision)
	}
}
