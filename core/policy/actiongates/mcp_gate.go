package actiongates

import (
	"bytes"
	"context"
	"encoding/json"
	"path"
	"reflect"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/mcp"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// MCPIdentityResolver maps (tenant, agent_id) to the scope-filter view of
// the calling identity. Implementations MUST be safe for concurrent use and
// MUST respect ctx cancellation. A miss (no error, identity absent) is
// signaled by (nil, nil); errors fail closed at the gate.
type MCPIdentityResolver interface {
	ResolveMCPIdentity(ctx context.Context, tenant, agentID string) (*mcp.AgentIdentity, error)
}

// DangerousParamRule names an action argument whose presence at a specific
// value is automatically denied. Rules are scoped to a tool-name glob (see
// MCPGateOptions.DangerousParamRules). Comparison is deep-equality on Value.
type DangerousParamRule struct {
	Name  string
	Value any
}

// MCPGateOptions configures the MCP/tool-call gate. Identities is required;
// the gate fails closed without it. Reachability is optional; a nil probe
// skips the unavailability check. DangerousParamRules is keyed by a tool-
// name glob ("*" applies to all tools); rules from every matching key are
// evaluated.
type MCPGateOptions struct {
	Identities          MCPIdentityResolver
	Reachability        ReachabilityProbe
	DangerousParamRules map[string][]DangerousParamRule
}

// MCPGate enforces MCP/tool-call admission. It converges on the same
// allow/deny semantics as core/mcp.FilterForIdentity for the AllowedTools
// field but additionally validates: AllowedServers, AllowedResources,
// RequiredEntitlement, DangerousParamRules and Reachability.
type MCPGate struct {
	identities   MCPIdentityResolver
	reachability ReachabilityProbe
	dangerous    map[string][]DangerousParamRule
}

// NewMCPGate returns a gate bound to the resolver/probe in opts. Rule
// values are normalized through a JSON round-trip at construction so
// the dangerous-param check compares two values in the same shape that
// BuildActionDescriptorFromToolCall produces (float64 for numbers,
// map[string]any for objects, []any for arrays). Without this, an
// admin configuring `DangerousParamRule{Value: int(1)}` in Go would
// silently never match a JSON `1` that arrives over the wire.
func NewMCPGate(opts MCPGateOptions) *MCPGate {
	return &MCPGate{
		identities:   opts.Identities,
		reachability: opts.Reachability,
		dangerous:    normalizeDangerousParamRules(opts.DangerousParamRules),
	}
}

// normalizeDangerousParamRules JSON-round-trips every rule Value so
// reflect.DeepEqual against action args (which are always JSON-decoded)
// catches numeric and composite matches. A Value that fails to marshal
// is preserved as-is so a misconfigured rule still loads — it just
// won't fire against a JSON-shaped Args slot.
func normalizeDangerousParamRules(in map[string][]DangerousParamRule) map[string][]DangerousParamRule {
	if len(in) == 0 {
		return in
	}
	out := make(map[string][]DangerousParamRule, len(in))
	for k, set := range in {
		ns := make([]DangerousParamRule, 0, len(set))
		for _, r := range set {
			ns = append(ns, DangerousParamRule{Name: r.Name, Value: jsonRoundTripValue(r.Value)})
		}
		out[k] = ns
	}
	return out
}

func jsonRoundTripValue(v any) any {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return v
	}
	return out
}

func (g *MCPGate) ID() string { return GateIDMCP }

func (g *MCPGate) Evaluate(ctx context.Context, in *config.PolicyInput) ActionGateDecision {
	if in == nil || in.Action == nil {
		return ActionGateDecision{}
	}
	act := in.Action
	if act.Kind != config.ActionKindMCPCall {
		return ActionGateDecision{}
	}

	actx := auth.FromContext(ctx)
	if actx == nil || strings.TrimSpace(actx.Tenant) == "" {
		return mcpDecision(pb.DecisionType_DECISION_TYPE_DENY, act, CodeUnauthorized,
			"authentication required", "missing_auth")
	}

	agentID := strings.TrimSpace(in.Meta.AgentID)
	if agentID == "" {
		agentID = strings.TrimSpace(in.Labels["agent_id"])
	}
	if agentID == "" {
		return mcpDecision(pb.DecisionType_DECISION_TYPE_DENY, act, CodeUnauthorized,
			"agent identity required for mcp_call", "missing_agent_id")
	}

	if g.identities == nil {
		return mcpDecision(pb.DecisionType_DECISION_TYPE_DENY, act, CodeInternalError,
			"identity resolver unavailable", "identity_resolver_unavailable")
	}
	id, err := g.identities.ResolveMCPIdentity(ctx, actx.Tenant, agentID)
	if err != nil {
		return mcpDecision(pb.DecisionType_DECISION_TYPE_DENY, act, CodeInternalError,
			"identity resolution failed", "identity_lookup_failed:"+sanitizeErr(err))
	}
	if id == nil {
		return mcpDecision(pb.DecisionType_DECISION_TYPE_DENY, act, CodeUnauthorized,
			"agent identity not found", "no_identity")
	}

	if !globAdmits(id.AllowedServers, act.Server) {
		return mcpDecision(pb.DecisionType_DECISION_TYPE_DENY, act, CodeAccessDenied,
			"MCP server is not allow-listed for this identity", "server_not_allowlisted")
	}
	if !globAdmits(id.AllowedTools, act.Tool) {
		return mcpDecision(pb.DecisionType_DECISION_TYPE_DENY, act, CodeAccessDenied,
			"MCP tool is not allow-listed for this identity", "tool_not_allowlisted")
	}
	if act.TargetURL != "" {
		if !globAdmits(id.AllowedResources, act.TargetURL) {
			return mcpDecision(pb.DecisionType_DECISION_TYPE_DENY, act, CodeAccessDenied,
				"MCP resource is not allow-listed for this identity", "resource_not_allowlisted")
		}
	}

	if strings.TrimSpace(act.RequiredEntitlement) != "" && !hasEntitlement(id.Entitlements, act.RequiredEntitlement) {
		return mcpDecision(pb.DecisionType_DECISION_TYPE_DENY, act, CodeAccessDenied,
			"identity lacks required entitlement", "unlicensed")
	}

	if sub, hit := matchDangerousParams(g.dangerous, act.Tool, act.Args); hit {
		return mcpDecision(pb.DecisionType_DECISION_TYPE_DENY, act, CodeAccessDenied,
			"action carries a dangerous parameter", sub)
	}

	if g.reachability != nil && strings.TrimSpace(act.Server) != "" {
		reachable, perr := g.reachability.MCPServerReachable(ctx, act.Server)
		if perr != nil {
			return mcpDecision(pb.DecisionType_DECISION_TYPE_DENY, act, CodeServiceUnavailable,
				"MCP server reachability probe failed", "reachability_probe_failed:"+sanitizeErr(perr))
		}
		if !reachable {
			return mcpDecision(pb.DecisionType_DECISION_TYPE_DENY, act, CodeServiceUnavailable,
				"MCP server is currently unreachable", "server_unreachable")
		}
	}

	return ActionGateDecision{
		Decision:  pb.DecisionType_DECISION_TYPE_ALLOW,
		GateID:    GateIDMCP,
		Reason:    "mcp call authorized",
		SubReason: "allowed",
		Extra:     mcpExtra(act, "allowed"),
	}
}

func mcpDecision(decision pb.DecisionType, act *config.ActionDescriptor, code, reason, sub string) ActionGateDecision {
	return ActionGateDecision{
		Decision:  decision,
		GateID:    GateIDMCP,
		Code:      code,
		Reason:    reason,
		SubReason: sub,
		Extra:     mcpExtra(act, sub),
	}
}

func mcpExtra(act *config.ActionDescriptor, sub string) map[string]string {
	out := map[string]string{
		"gate":       GateIDMCP,
		"sub_reason": sub,
	}
	if act.Server != "" {
		out["server"] = act.Server
	}
	if act.Tool != "" {
		out["tool"] = act.Tool
	}
	return out
}

// globAdmits returns true when value matches any glob in patterns. Empty
// patterns or empty value returns false (fail-closed). Patterns use the
// stdlib path.Match grammar — same as the existing core/mcp filter and the
// matchTopic helper in core/infra/config.
func globAdmits(patterns []string, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		ok, err := path.Match(p, value)
		if err == nil && ok {
			return true
		}
	}
	return false
}

func hasEntitlement(have []string, required string) bool {
	want := strings.TrimSpace(required)
	if want == "" {
		return true
	}
	for _, e := range have {
		if strings.EqualFold(strings.TrimSpace(e), want) {
			return true
		}
	}
	return false
}

// matchDangerousParams walks the configured rule sets, picking up entries
// whose tool-name glob matches the action's tool. For each entry it
// compares Args[Name] to Value with reflect.DeepEqual so map/slice payloads
// are handled consistently. The first match wins; sub_reason carries the
// param name so SIEM can pivot.
func matchDangerousParams(rules map[string][]DangerousParamRule, tool string, args map[string]any) (string, bool) {
	if len(rules) == 0 || len(args) == 0 {
		return "", false
	}
	tool = strings.TrimSpace(tool)
	for pattern, set := range rules {
		p := strings.TrimSpace(pattern)
		if p == "" {
			continue
		}
		ok, err := path.Match(p, tool)
		if err != nil || !ok {
			continue
		}
		for _, rule := range set {
			name := strings.TrimSpace(rule.Name)
			if name == "" {
				continue
			}
			actual, present := args[name]
			if !present {
				continue
			}
			if dangerousParamMatches(actual, rule.Value) {
				return "dangerous_param:" + name, true
			}
		}
	}
	return "", false
}

// dangerousParamMatches is a Go-vs-JSON-aware equality test. act.Args
// arrives via json.Unmarshal(..., &any), so a JSON `1` becomes float64(1)
// while an admin-configured DangerousParamRule{Value: 1} or
// {Value: int64(1)} or {Value: "1"} stays in its source-typed form. A
// raw reflect.DeepEqual returns false in those cases and silently lets a
// dangerous-value match slip past. Two normalization passes catch the
// real-world configs:
//
//  1. Numeric coercion: if BOTH sides cast cleanly to float64, compare
//     as float64. Covers admin int / int64 / json.Number / float matched
//     against a json-decoded float64.
//  2. JSON-roundtrip equality: marshal both sides and compare bytes.
//     Catches map[string]any{"x":1} vs custom struct shapes that DeepEqual
//     would reject for type identity even when the JSON-shape matches.
//
// Falls back to reflect.DeepEqual so existing same-type rules (string vs
// string, bool vs bool) keep their semantics.
func dangerousParamMatches(actual, want any) bool {
	if reflect.DeepEqual(actual, want) {
		return true
	}
	if a, aok := toFloat64(actual); aok {
		if w, wok := toFloat64(want); wok {
			return a == w
		}
	}
	ab, aerr := json.Marshal(actual)
	wb, werr := json.Marshal(want)
	if aerr == nil && werr == nil && bytes.Equal(ab, wb) {
		return true
	}
	return false
}

func toFloat64(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint8:
		return float64(x), true
	case uint16:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}
