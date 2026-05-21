package actiongates

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// TenantGate enforces multi-tenant isolation invariants on tenant_query and
// mutation actions. Tenant identity is sourced exclusively from
// AuthContext.Tenant; ActionDescriptor.TargetResource.OwnerTenant and any
// body-claimed tenant are inputs to compare against, not authoritative.
//
// The gate fires for ActionKindTenantQuery and ActionKindMutation actions.
// For other kinds (file/url/mcp/provenance) it returns the zero decision so
// the pipeline continues.
type TenantGate struct{}

// NewTenantGate returns a fresh, stateless TenantGate.
func NewTenantGate() *TenantGate { return &TenantGate{} }

func (g *TenantGate) ID() string { return GateIDTenant }

// Match tnt_<key>_<rest> in resource IDs.
var tenantPrefixRe = regexp.MustCompile(`^(tnt_[A-Za-z0-9-]+)_`)

// largeEnumPageSize is the threshold above which a single page_size triggers
// REQUIRE_HUMAN. Sites that legitimately page very wide raise this via per-
// tenant config; this default protects naive scans.
const largeEnumPageSize = 10000

// largeEnumInClauseSize is the threshold for an IN-clause size that triggers
// REQUIRE_HUMAN. Below this we trust legit pagination patterns.
const largeEnumInClauseSize = 1000

func (g *TenantGate) Evaluate(ctx context.Context, in *config.PolicyInput) ActionGateDecision {
	if in == nil || in.Action == nil {
		return ActionGateDecision{}
	}
	act := in.Action
	if act.Kind != config.ActionKindTenantQuery && act.Kind != config.ActionKindMutation {
		return ActionGateDecision{}
	}

	actx := auth.FromContext(ctx)
	if actx == nil || strings.TrimSpace(actx.Tenant) == "" {
		return ActionGateDecision{
			Decision:  pb.DecisionType_DECISION_TYPE_DENY,
			GateID:    GateIDTenant,
			Code:      CodeUnauthorized,
			Reason:    "authentication required",
			SubReason: "missing_auth",
			Extra:     map[string]string{"gate": GateIDTenant, "sub_reason": "missing_auth"},
		}
	}

	authTenant := actx.Tenant
	isComplianceOrLegal := roleHasCompliance(actx.Role)

	// 1) Cross-tenant via TargetResource (OwnerTenant set).
	if act.TargetResource != nil && act.TargetResource.OwnerTenant != "" {
		if act.TargetResource.OwnerTenant != authTenant && !actx.AllowCrossTenant {
			return denyTenant(authTenant, "tenant boundary violation",
				"cross_tenant:owner_mismatch",
				map[string]string{
					"resource_type": act.TargetResource.Type,
					"auth_tenant":   authTenant,
				})
		}
	}

	// 2) Tenant-prefixed-ID mismatch (resource ID encodes a different tenant).
	if act.TargetResource != nil && act.TargetResource.ID != "" {
		if m := tenantPrefixRe.FindStringSubmatch(act.TargetResource.ID); len(m) == 2 {
			if m[1] != authTenant && !actx.AllowCrossTenant {
				return denyTenant(authTenant, "tenant boundary violation",
					"cross_tenant:prefixed_id_mismatch",
					map[string]string{
						"resource_type":   act.TargetResource.Type,
						"detected_prefix": m[1],
					})
			}
		}
	}

	// 3) Wildcard owner/tenant filters.
	if reason, ok := matchWildcardOwnerFilter(act.Filters, act.Wildcards); ok && !actx.AllowCrossTenant {
		return denyTenant(authTenant, "tenant boundary violation", reason, nil)
	}

	// 4) Archived/deleted bypass — unless compliance/legal role on own tenant.
	if reason, ok := matchArchivedBypass(act.Filters); ok && !isComplianceOrLegal {
		return denyTenant(authTenant, "archived record query denied", reason, nil)
	}

	// 5) Anti-enumeration (very large page_size or in-clause).
	if reason, ok := matchLargeEnumeration(act.Args); ok {
		return ActionGateDecision{
			Decision:  pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
			GateID:    GateIDTenant,
			Code:      CodeRequireHuman,
			Reason:    "large enumeration requires human approval",
			SubReason: reason,
			Extra: map[string]string{
				"gate":        GateIDTenant,
				"sub_reason":  reason,
				"auth_tenant": authTenant,
			},
		}
	}

	return ActionGateDecision{Decision: pb.DecisionType_DECISION_TYPE_ALLOW, GateID: GateIDTenant}
}

func denyTenant(authTenant, reason, subReason string, extra map[string]string) ActionGateDecision {
	out := map[string]string{
		"gate":        GateIDTenant,
		"sub_reason":  subReason,
		"auth_tenant": authTenant,
	}
	for k, v := range extra {
		out[k] = v
	}
	return ActionGateDecision{
		Decision:  pb.DecisionType_DECISION_TYPE_DENY,
		GateID:    GateIDTenant,
		Code:      CodeAccessDenied,
		Reason:    reason,
		SubReason: subReason,
		Extra:     out,
	}
}

func roleHasCompliance(role string) bool {
	r := strings.ToLower(role)
	return strings.Contains(r, "compliance") || strings.Contains(r, "legal")
}

func matchWildcardOwnerFilter(filters map[string]string, wildcards []string) (string, bool) {
	wildcardKeys := []string{"owner_id", "tenant_id", "tenant", "owner", "account_id", "org_id"}
	for _, k := range wildcardKeys {
		if v, ok := filters[k]; ok && (v == "*" || v == "%" || strings.Contains(v, "%")) {
			return "wildcard_owner:" + k, true
		}
	}
	for _, k := range wildcards {
		for _, w := range wildcardKeys {
			if strings.EqualFold(k, w) {
				return "wildcard_owner:" + k, true
			}
		}
	}
	// Raw SQL-injected predicates.
	if raw, ok := filters["raw_where"]; ok {
		lower := strings.ToLower(strings.ReplaceAll(raw, " ", ""))
		if strings.Contains(lower, "1=1") || strings.Contains(lower, "or1=1") || strings.Contains(lower, "true=true") {
			return "wildcard_owner:raw_where", true
		}
	}
	return "", false
}

func matchArchivedBypass(filters map[string]string) (string, bool) {
	if v, ok := filters["include_archived"]; ok && (strings.EqualFold(v, "true") || v == "1") {
		return "archived_bypass:include_archived", true
	}
	if v, ok := filters["deleted_at"]; ok {
		lv := strings.ToLower(strings.TrimSpace(v))
		if lv == "any" || lv == "*" || lv == "" {
			return "archived_bypass:deleted_at_any", true
		}
		if strings.Contains(lv, "is null") && strings.Contains(lv, "is not null") {
			return "archived_bypass:contradictory", true
		}
	}
	if v, ok := filters["status"]; ok && strings.EqualFold(strings.TrimSpace(v), "any") {
		return "archived_bypass:status_any", true
	}
	return "", false
}

func matchLargeEnumeration(args map[string]any) (string, bool) {
	if n, ok := readInt(args, "page_size"); ok && n > largeEnumPageSize {
		return "large_enumeration:page_size", true
	}
	if n, ok := readInt(args, "limit"); ok && n > largeEnumPageSize {
		return "large_enumeration:limit", true
	}
	if n, ok := readInt(args, "in_clause_size"); ok && n > largeEnumInClauseSize {
		return "large_enumeration:in_clause", true
	}
	return "", false
}

func readInt(args map[string]any, key string) (int, bool) {
	v, ok := args[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float32:
		return int(n), true
	case float64:
		return int(n), true
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
			return i, true
		}
	}
	return 0, false
}
