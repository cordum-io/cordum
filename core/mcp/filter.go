package mcp

import "strings"

// AgentIdentity is the narrow view of an agent identity that the scope
// filter needs. It deliberately mirrors the field names on
// core/infra/store.AgentIdentity so callers can copy values directly
// without taking a heavy import into core/mcp. A nil pointer or a
// zero-value identity fails closed — no tools are visible.
//
// Immutable after publish. Once a producer hands an *AgentIdentity
// off — typically by stashing it in a request context via
// ContextWithIdentity — no field MAY be reassigned and the backing
// arrays of AllowedTools / AllowedServers / AllowedResources /
// Entitlements / DataClassifications MUST NOT be mutated. The
// filterCache's keyFor reads these slice fields without any lock,
// relying on this contract; concurrent mutation would race
// slice-header reads against writes.
//
// Producers MUST construct a fresh struct, copy any caller-owned
// slice via `append([]string{}, src.Xs...)`, and never offer a write
// path on a published pointer. The two production producers
// (gateway.mcpIdentityFromStore in
// core/controlplane/gateway/mcp_identity.go and the gateway-fetched
// identity in core/mcp/tools/register.go) satisfy this; an
// identity-store refresh constructs a NEW *AgentIdentity rather than
// mutating an existing one. Refreshing an in-flight identity must
// follow the same copy-on-publish pattern.
type AgentIdentity struct {
	// ID is used for audit events when a call is denied. Not load-bearing
	// for filtering itself.
	ID string

	// AllowedServers is a list of MCP server-name glob patterns. The
	// actiongates MCP gate admits an mcp_call action only when its Server
	// matches at least one pattern. Empty = no servers (fail-closed).
	// FilterForIdentity does not consult this field; it is enforced at
	// the action gate layer.
	AllowedServers []string

	// AllowedTools is a list of tool-name glob patterns. A tool is
	// admitted only if its Name matches at least one pattern. The empty
	// slice means "no tools" — operators must opt in to every tool an
	// identity can use. The pattern "*" admits every tool.
	AllowedTools []string

	// AllowedResources is a list of cordum:// resource URI glob patterns
	// the identity may target. Consulted by the actiongates MCP gate when
	// an action specifies TargetURL. Empty = no resources (fail-closed)
	// for actions that name one. Actions with no TargetURL skip this
	// check.
	AllowedResources []string

	// Entitlements lists license/capability tokens the identity holds
	// (e.g. "vault.read", "anthropic.claude", "billing.export"). The
	// actiongates MCP gate denies actions whose RequiredEntitlement is
	// not present here. Empty = no entitlements granted.
	Entitlements []string

	// RiskTier is the actor's own risk tier. The filter admits tools
	// whose required tier is less-than-or-equal to the actor's tier.
	RiskTier string

	// DataClassifications is the set of sensitivities the actor is
	// authorised to access (e.g. "pii", "phi", "secrets"). The filter
	// admits tools whose DataClassifications are a subset of this set.
	DataClassifications []string
}

// FilterForIdentity returns the subset of tools that identity is
// allowed to see / call. The three gates are applied in order:
//
//  1. AllowedTools glob match on tool.Name
//  2. risk_tier ordering (Allows(actor, tool))
//  3. DataClassifications superset (every tool classification must be
//     present in the actor's classifications)
//
// A nil identity — or any identity that fails the fail-closed defaults
// (empty AllowedTools, unknown RiskTier) — returns an empty slice.
//
// The returned slice is a freshly allocated copy; callers may mutate it
// without affecting the input.
func FilterForIdentity(tools []Tool, id *AgentIdentity) []Tool {
	if id == nil {
		return []Tool{}
	}
	actor := ParseRiskTier(id.RiskTier)
	if actor == RiskTierUnknown {
		// Fail-closed: an identity with no declared tier sees nothing.
		return []Tool{}
	}
	if len(id.AllowedTools) == 0 {
		return []Tool{}
	}
	classSet := make(map[string]struct{}, len(id.DataClassifications))
	for _, c := range id.DataClassifications {
		c = strings.ToLower(strings.TrimSpace(c))
		if c == "" {
			continue
		}
		classSet[c] = struct{}{}
	}

	out := make([]Tool, 0, len(tools))
	for _, tool := range tools {
		if !identityAdmitsTool(id.AllowedTools, tool.Name) {
			continue
		}
		toolTier := ParseRiskTier(tool.RiskTier)
		if !Allows(actor, toolTier) {
			continue
		}
		if !classificationsCovered(classSet, tool.DataClassifications) {
			continue
		}
		out = append(out, tool)
	}
	return out
}

// DenyReason enumerates why a specific tool was filtered out for an
// identity. It is surfaced in the JSON-RPC error.data payload when a
// gated tools/call is rejected, and in the mcp_tool_denied SIEM event.
type DenyReason string

const (
	// DenyReasonNone means the tool is admitted.
	DenyReasonNone DenyReason = ""
	// DenyReasonNotInAllowedList means the tool's Name did not match
	// any pattern in the identity's AllowedTools.
	DenyReasonNotInAllowedList DenyReason = "tool_not_in_allowed_list"
	// DenyReasonRiskTierTooLow means the actor's RiskTier was below
	// the tool's required tier.
	DenyReasonRiskTierTooLow DenyReason = "risk_tier_too_low"
	// DenyReasonMissingDataClassification means the tool declared a
	// data classification the actor is not authorised for.
	DenyReasonMissingDataClassification DenyReason = "missing_data_classification"
	// DenyReasonNoIdentity means the request carried no resolvable
	// agent identity.
	DenyReasonNoIdentity DenyReason = "no_identity"
)

// NotAuthorized is the error ToolRegistry.Call returns when the caller
// identity fails one of the scope filter gates. It travels through the
// server as a JSON-RPC error with code=-32098 and the SubReason
// attached in error.data.sub_reason so MCP clients can programmatically
// distinguish "your identity lacks the tool" from "your tier is too
// low" from "your classifications don't cover this tool".
type NotAuthorized struct {
	// Tool is the tool name the call targeted.
	Tool string `json:"tool"`
	// SubReason is one of the DenyReason constants.
	SubReason DenyReason `json:"sub_reason"`
	// AgentID is the identity that was denied. Empty when no identity
	// could be resolved from the request context.
	AgentID string `json:"agent_id,omitempty"`
}

// Error satisfies the error interface so NotAuthorized can flow
// through the regular ToolRegistry.Call return path. The server layer
// switches on this type via errors.As to emit JSON-RPC -32098.
func (n *NotAuthorized) Error() string {
	if n == nil {
		return "mcp: not authorized"
	}
	return "mcp: not authorized: " + string(n.SubReason) + " for tool " + n.Tool
}

// EvaluateForIdentity runs the same three gates as FilterForIdentity
// but returns a structured DenyReason for a single tool. Used by
// tools/call enforcement to emit a specific sub-reason rather than a
// generic "not authorized" message.
func EvaluateForIdentity(tool Tool, id *AgentIdentity) DenyReason {
	if id == nil {
		return DenyReasonNoIdentity
	}
	actor := ParseRiskTier(id.RiskTier)
	if actor == RiskTierUnknown {
		return DenyReasonNoIdentity
	}
	if !identityAdmitsTool(id.AllowedTools, tool.Name) {
		return DenyReasonNotInAllowedList
	}
	toolTier := ParseRiskTier(tool.RiskTier)
	if !Allows(actor, toolTier) {
		return DenyReasonRiskTierTooLow
	}
	classSet := make(map[string]struct{}, len(id.DataClassifications))
	for _, c := range id.DataClassifications {
		c = strings.ToLower(strings.TrimSpace(c))
		if c == "" {
			continue
		}
		classSet[c] = struct{}{}
	}
	if !classificationsCovered(classSet, tool.DataClassifications) {
		return DenyReasonMissingDataClassification
	}
	return DenyReasonNone
}

// identityAdmitsTool checks AllowedTools glob matches against toolName.
func identityAdmitsTool(patterns []string, toolName string) bool {
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if globMatch(p, toolName) {
			return true
		}
	}
	return false
}

// classificationsCovered reports whether every entry in required is
// present in have. Case-insensitive; empty required is trivially true.
func classificationsCovered(have map[string]struct{}, required []string) bool {
	for _, r := range required {
		r = strings.ToLower(strings.TrimSpace(r))
		if r == "" {
			continue
		}
		if _, ok := have[r]; !ok {
			return false
		}
	}
	return true
}
