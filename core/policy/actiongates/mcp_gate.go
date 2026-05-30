package actiongates

import (
	"bytes"
	"context"
	"encoding/json"
	"path"
	"reflect"
	"regexp"
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
	// DestructiveToolGlobs lists path.Match globs for tool names treated as
	// destructive. A session-tainted action whose tool matches one is DENIED
	// (content-aware session-taint deny). Unset => defaultDestructiveToolGlobs.
	// This is a SCOPING predicate only — it never denies on its own.
	DestructiveToolGlobs []string
	// DestructiveMutationArgKeys names string args that may carry a GraphQL
	// mutation document for generic API passthrough tools. Unset => defaults.
	// This is a SCOPING predicate only -- it never denies on its own.
	DestructiveMutationArgKeys []string
	// DestructiveMutationFieldGlobs lists path.Match globs for GraphQL mutation
	// field names treated as destructive. Unset => defaults.
	// This is a SCOPING predicate only -- it never denies on its own.
	DestructiveMutationFieldGlobs []string
	// FailClosedDestructiveOnTaintLookupError requires human approval for a
	// destructive MCP call when the session-taint lookup errored. Default false
	// preserves the historical fail-open path.
	FailClosedDestructiveOnTaintLookupError bool
}

// MCPGate enforces MCP/tool-call admission. It converges on the same
// allow/deny semantics as core/mcp.FilterForIdentity for the AllowedTools
// field but additionally validates: AllowedServers, AllowedResources,
// RequiredEntitlement, DangerousParamRules and Reachability.
type MCPGate struct {
	identities               MCPIdentityResolver
	reachability             ReachabilityProbe
	dangerous                map[string][]DangerousParamRule
	destructiveTool          []string
	destructiveMutArgKeys    []string
	destructiveMutFieldGlobs []string
	failClosedTaintLookup    bool
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
		identities:               opts.Identities,
		reachability:             opts.Reachability,
		dangerous:                normalizeDangerousParamRules(opts.DangerousParamRules),
		destructiveTool:          normalizeDestructiveToolGlobs(opts.DestructiveToolGlobs),
		destructiveMutArgKeys:    normalizeStringList(opts.DestructiveMutationArgKeys, defaultDestructiveMutationArgKeys),
		destructiveMutFieldGlobs: normalizeStringList(opts.DestructiveMutationFieldGlobs, defaultDestructiveMutationFieldGlobs),
		failClosedTaintLookup:    opts.FailClosedDestructiveOnTaintLookupError,
	}
}

// defaultDestructiveToolGlobs is the fallback set of tool-name globs (path.Match)
// treated as destructive when MCPGateOptions.DestructiveToolGlobs is unset. It
// covers the common destructive verbs across MCP tool catalogs; P3b may tighten
// it for the Monday pack.
var defaultDestructiveToolGlobs = []string{"*delete*", "*remove*", "*archive*"}

var defaultDestructiveMutationArgKeys = []string{"query", "mutation", "gql", "graphql"}

var defaultDestructiveMutationFieldGlobs = []string{"delete_*", "archive_*", "remove_*", "delete", "archive", "remove"}

const maxArgMutationScanBytes = 16 << 10

var (
	mutationKeywordRe        = regexp.MustCompile(`(?i)\bmutation\b`)
	graphqlFieldInvocationRe = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
)

// normalizeDestructiveToolGlobs trims blanks and applies the default set when the
// caller supplied none.
func normalizeDestructiveToolGlobs(globs []string) []string {
	out := make([]string, 0, len(globs))
	for _, g := range globs {
		if g = strings.TrimSpace(g); g != "" {
			out = append(out, g)
		}
	}
	if len(out) == 0 {
		return append([]string(nil), defaultDestructiveToolGlobs...)
	}
	return out
}

func normalizeStringList(values, defaults []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return append([]string(nil), defaults...)
	}
	return out
}

// isDestructiveTool reports whether the tool name itself is configured as
// destructive. It is a scoping predicate only, never a standalone deny.
func (g *MCPGate) isDestructiveTool(tool string) bool {
	return globAdmits(g.destructiveTool, tool)
}

// destructiveCall reports whether a tool call is destructive and returns the
// safe identifier that matched. Tool-name globs are the primary configured
// signal; the GraphQL fallback catches generic passthrough tools whose name is
// benign but whose arguments carry a destructive delete/archive/remove mutation
// (for example all_monday_api with "mutation { delete_item(...) }"). This
// remains a SCOPING predicate only for the session-taint deny -- NEVER a
// standalone deny: a clean destructive call still passes the gate.
func (g *MCPGate) destructiveCall(act *config.ActionDescriptor) (string, bool) {
	if act == nil {
		return "", false
	}
	if g.isDestructiveTool(act.Tool) {
		return act.Tool, true
	}
	if field, ok := matchesDestructiveMutationArgs(act.Args, g.destructiveMutArgKeys, g.destructiveMutFieldGlobs); ok {
		return "mutation:" + field, true
	}
	return "", false
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

	// Content-aware session-taint deny (DoD#2/#3/#4): block ONLY when the action
	// is BOTH tainted (a prompt injection was detected in a PRIOR tool-call result
	// this session, stamped pre-dispatch in EvaluateToolCall) AND destructive. The
	// destructive match can come from a tool-name glob or from a GraphQL-proxy
	// tool whose args carry a delete/archive/remove mutation. The conjunction is
	// the whole point: destructiveCall alone is a scoping predicate, never a
	// standalone deny -- a clean session's delete carries no taint tag and falls
	// through to the normal allow-list ALLOW (DoD#3), and a benign tool while
	// tainted is not denied (DoD#4). That is what makes this deny content-DERIVED,
	// not a bare "deny deletes" metadata rule. The injected snippet is cited in
	// the decision Extra (mcpExtra) for the audit trail / UI.
	tainted := containsRiskTag(act.RiskTags, config.RiskTagSessionPromptInjection)
	if tainted || (g.failClosedTaintLookup && act.TaintLookupFailed) {
		if match, destructive := g.destructiveCall(act); destructive {
			if tainted {
				dec := mcpDecision(pb.DecisionType_DECISION_TYPE_DENY, act, CodeAccessDenied,
					"destructive action blocked: session tainted by prompt injection detected in a prior tool result",
					"session_tainted_prompt_injection")
				dec.Extra["taint_destructive_match"] = match
				return dec
			}
			dec := mcpDecision(pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, act, CodeRequireHuman,
				"destructive action held: session taint store unavailable, cannot confirm session is clean",
				"taint_lookup_unavailable_failclosed")
			dec.Extra["taint_destructive_match"] = match
			return dec
		}
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
	// Cite the actual injected content when the action carries a session taint,
	// so the DENY / audit / UI is verifiably content-aware. The snippet was
	// already bounded + control-char stripped at detection time.
	if act.SessionTaint != nil {
		if act.SessionTaint.Pattern != "" {
			out["taint_pattern"] = act.SessionTaint.Pattern
		}
		if act.SessionTaint.Snippet != "" {
			out["taint_snippet"] = act.SessionTaint.Snippet
		}
		if act.SessionTaint.SourceTool != "" {
			out["taint_source_tool"] = act.SessionTaint.SourceTool
		}
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

func matchesDestructiveMutationArgs(args map[string]any, argKeys, fieldGlobs []string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	for _, key := range argKeys {
		value, ok := args[key]
		if !ok {
			continue
		}
		doc, ok := value.(string)
		if !ok || strings.TrimSpace(doc) == "" {
			continue
		}
		if len(doc) > maxArgMutationScanBytes {
			doc = doc[:maxArgMutationScanBytes]
		}
		lower := strings.ToLower(doc)
		if !mutationKeywordRe.MatchString(lower) {
			continue
		}
		for _, match := range graphqlFieldInvocationRe.FindAllStringSubmatch(lower, -1) {
			if len(match) < 2 {
				continue
			}
			field := match[1]
			if globAdmits(fieldGlobs, field) {
				return field, true
			}
		}
	}
	return "", false
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
