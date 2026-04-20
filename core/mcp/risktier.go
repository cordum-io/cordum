package mcp

import "strings"

// RiskTier is an ordered risk classification used by scope-based tool
// filtering. Higher tiers denote greater operational risk; a tool's
// required tier is the minimum actor tier permitted to see or call it.
type RiskTier int

const (
	// RiskTierUnknown is the zero value; Allows treats it identically to
	// RiskTierHigh (fail-closed). Callers should not construct it
	// explicitly — Parse returns it only when the source string was
	// empty, in which case the caller's own defaulting rules apply.
	RiskTierUnknown RiskTier = iota
	RiskTierLow
	RiskTierMedium
	RiskTierHigh
	RiskTierCritical
)

// String returns the canonical lowercase name for a tier.
func (t RiskTier) String() string {
	switch t {
	case RiskTierLow:
		return "low"
	case RiskTierMedium:
		return "medium"
	case RiskTierHigh:
		return "high"
	case RiskTierCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// ParseRiskTier parses a free-form tier string (case-insensitive, trimmed).
// An empty string returns RiskTierUnknown so callers can distinguish
// "unspecified" from "invalid"; any non-matching value returns
// RiskTierHigh — the fail-closed default for unrecognised input.
func ParseRiskTier(s string) RiskTier {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return RiskTierUnknown
	case "low":
		return RiskTierLow
	case "medium", "med":
		return RiskTierMedium
	case "high":
		return RiskTierHigh
	case "critical", "crit":
		return RiskTierCritical
	default:
		return RiskTierHigh
	}
}

// Allows reports whether an actor at actorTier may use a tool declaring
// toolTier. The contract is "actor must meet or exceed the tool's
// required tier" — a critical-tier agent can call tools at any tier; a
// low-tier agent can only call low-tier tools.
//
// Unknown tiers fail closed: an unknown actor cannot reach any tool, and
// an unknown tool is treated as RiskTierHigh (matching the Tool struct
// documentation).
func Allows(actorTier, toolTier RiskTier) bool {
	if actorTier == RiskTierUnknown {
		return false
	}
	if toolTier == RiskTierUnknown {
		toolTier = RiskTierHigh
	}
	return actorTier >= toolTier
}
