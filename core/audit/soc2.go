package audit

// SOC2 control-framework mapping for Cordum audit events.
//
// Each SIEMEvent is tagged with zero or more SOC2 2017 Trust Services
// Criteria (TSC) control IDs so compliance exports carry the right
// evidence labels without operators having to hand-code the mapping.
//
// Default mapping (kept authoritative here; docs/compliance/soc2_mapping.md
// references this file):
//
//	Event Type                 | Controls                                       | Overlay
//	---------------------------|------------------------------------------------|-----------------------------------
//	safety.decision            | CC7.2                                          | +CC7.3 when Decision=="deny"
//	safety.approval            | CC6.1, CC7.2                                   | —
//	safety.policy_change       | CC8.1                                          | —
//	safety.violation           | CC7.3                                          | —
//	system.auth                | CC6.1                                          | —
//	mcp.tool_approval          | CC6.1, CC7.2                                   | +CC6.3 when Extra[outcome]=="revoke"
//	mcp.tool_denied            | CC7.3                                          | —
//	shadow_eval                | CC7.2                                          | —
//
// SOC2 2017 TSC IDs referenced:
//
//	CC6.1 — Logical and physical access controls
//	CC6.3 — Access revocation
//	CC7.2 — Monitoring of controls
//	CC7.3 — Detection of security incidents
//	CC8.1 — Change management
//
// Operators can override the defaults at runtime by setting
// CORDUM_SOC2_MAPPING_PATH to a YAML file with the same shape; see
// LoadSOC2Mapping below.

import (
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// EnvSOC2MappingPath is the env var a compliance admin sets to override
// the baked-in SOC2 map. Missing / unreadable / malformed files fall
// back to the default with a single slog.Warn on load.
const EnvSOC2MappingPath = "CORDUM_SOC2_MAPPING_PATH"

// SOC2Mapping maps SIEMEvent.EventType → a slice of SOC2 control IDs.
//
// Iteration is deterministic (ControlsFor returns a sorted, de-duplicated
// slice) so exports are reproducible byte-for-byte when the underlying
// data is unchanged — important for audit artefacts.
type SOC2Mapping map[string][]string

// ControlsFor returns the SOC2 controls assigned to ev, including any
// decision/outcome overlay. Guarantees:
//
//   - The return slice is never nil — callers can marshal `"soc2_controls":
//     []` cleanly without a null special-case.
//   - The return slice is sorted ascending and has duplicates removed.
//   - An unknown EventType yields an empty slice (not an error); the
//     export writer emits [] rather than dropping the row.
func (m SOC2Mapping) ControlsFor(ev SIEMEvent) []string {
	if m == nil {
		return []string{}
	}
	collected := make(map[string]struct{})
	for _, ctrl := range m[ev.EventType] {
		collected[ctrl] = struct{}{}
	}
	// Apply overlay: same event-type with a decision-specific control
	// set. This lets an allow/deny split keep the base controls while
	// layering on the deny-only signal (CC7.3).
	switch ev.EventType {
	case EventSafetyDecision:
		if strings.EqualFold(strings.TrimSpace(ev.Decision), "deny") {
			collected["CC7.3"] = struct{}{}
		}
	case EventMCPToolApproval:
		if ev.Extra != nil {
			if outcome, ok := ev.Extra["outcome"]; ok {
				if strings.EqualFold(strings.TrimSpace(outcome), "revoke") {
					collected["CC6.3"] = struct{}{}
				}
			}
		}
	}
	if len(collected) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(collected))
	for c := range collected {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// DefaultSOC2Mapping returns the vetted initial mapping. Every EventType
// constant declared in exporter.go has an entry so operators don't see
// an empty `soc2_controls` on known events.
func DefaultSOC2Mapping() SOC2Mapping {
	return SOC2Mapping{
		EventSafetyDecision:  {"CC7.2"},
		EventSafetyApproval:  {"CC6.1", "CC7.2"},
		EventPolicyChange:    {"CC8.1"},
		EventSafetyViolation: {"CC7.3"},
		EventSystemAuth:      {"CC6.1"},
		EventMCPToolApproval: {"CC6.1", "CC7.2"},
		// EventMCPToolDenied and EventShadowEval are referenced by the
		// exporter package's constants; include them so downstream
		// dashboards always see a non-empty mapping.
		"mcp.tool_denied": {"CC7.3"},
		"shadow_eval":     {"CC7.2"},
		// shadow_agent.* lifecycle events (EDGE-141). Detection fits
		// CC7.2 (monitoring of controls); resolve/suppress carry
		// change-management evidence (CC8.1) because they are operator
		// dispositions of detected risk.
		EventShadowAgentDetected:   {"CC7.2"},
		EventShadowAgentResolved:   {"CC7.2", "CC8.1"},
		EventShadowAgentSuppressed: {"CC7.2", "CC8.1"},
		// EDGE-143.6 — operator exception lifecycle (§10.3 + §11.1).
		// Creation/revocation are operator change-management evidence
		// (CC8.1). Apply events fit detection-control monitoring (CC7.2)
		// because they record which findings were silenced.
		EventShadowAgentExceptionCreated: {"CC7.2", "CC8.1"},
		EventShadowAgentExceptionRevoked: {"CC7.2", "CC8.1"},
		EventShadowAgentExceptionApplied: {"CC7.2"},
	}
}

// DefaultSOC2Legend returns a human-readable description per control ID,
// embedded in every compliance export manifest so a reviewer can
// interpret the mapping without cross-referencing SOC2 documentation.
func DefaultSOC2Legend() map[string]string {
	return map[string]string{
		"CC6.1": "Logical and physical access controls",
		"CC6.3": "Access revocation",
		"CC7.2": "Monitoring of controls",
		"CC7.3": "Detection of security incidents",
		"CC8.1": "Change management",
	}
}

// LoadSOC2Mapping returns the mapping configured for this process.
//
// If path is non-empty and points at a readable YAML file matching the
// SOC2Mapping shape, the custom mapping is merged OVER the default —
// every default entry is preserved unless the override specifies the
// same key, which keeps unknown event types from silently losing their
// controls when an operator ships a partial override.
//
// Missing or malformed paths fall back to DefaultSOC2Mapping with a
// single slog.Warn so boot logs capture the misconfiguration without
// taking down the gateway.
func LoadSOC2Mapping(path string) (SOC2Mapping, error) {
	base := DefaultSOC2Mapping()
	path = strings.TrimSpace(path)
	if path == "" {
		return base, nil
	}
	// #nosec G304 -- path comes from operator env var, deliberately file-system accessible.
	data, err := os.ReadFile(path)
	if err != nil {
		slog.Warn("soc2 mapping override unreadable; using default",
			"path", path,
			"error", err,
		)
		return base, nil
	}
	var override SOC2Mapping
	if err := yaml.Unmarshal(data, &override); err != nil {
		slog.Warn("soc2 mapping override malformed; using default",
			"path", path,
			"error", err,
		)
		return base, nil
	}
	// Merge: override keys win; default keys survive untouched.
	merged := make(SOC2Mapping, len(base)+len(override))
	for k, v := range base {
		merged[k] = append([]string(nil), v...)
	}
	for k, v := range override {
		merged[k] = append([]string(nil), v...)
	}
	slog.Info("soc2 mapping override loaded",
		"path", path,
		"override_keys", len(override),
		"merged_keys", len(merged),
	)
	return merged, nil
}

// LoadSOC2MappingFromEnv is a convenience wrapper reading EnvSOC2MappingPath.
// Always returns a usable mapping — falls back to default on any miss.
func LoadSOC2MappingFromEnv() SOC2Mapping {
	m, err := LoadSOC2Mapping(os.Getenv(EnvSOC2MappingPath))
	if err != nil {
		slog.Warn("soc2 mapping load error; using default", "error", err)
		return DefaultSOC2Mapping()
	}
	return m
}

// String renders the mapping as a deterministic human-readable dump
// suitable for debug logging and the export manifest's legend section.
func (m SOC2Mapping) String() string {
	if len(m) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%s=[%s]", k, strings.Join(m[k], ","))
	}
	b.WriteByte('}')
	return b.String()
}

// ---------------------------------------------------------------------------
// Event categories (governance vs routine)
// ---------------------------------------------------------------------------

// Event categories partition audit event types into security-relevant
// governance events and high-volume operational telemetry. The compliance
// export (core/audit/export_compliance.go) and the /api/v1/audit/events read
// surface both consume this single source of truth so their category filters
// can never drift apart.
const (
	// CategoryGovernance marks security-relevant events an auditor cares
	// about: policy decisions, approvals, denials, key/role changes, license
	// break-glass, and shadow-agent findings.
	CategoryGovernance = "governance"
	// CategoryRoutine marks high-volume operational telemetry: auth checks,
	// audit-read meta-events, edge/MCP lifecycle, worker handshakes, and topic
	// registration. These dominate an unfiltered export and bury governance
	// signal (the reason CORDUM_AUDIT_READ_SAMPLE_RATE defaults to 0.0).
	CategoryRoutine = "routine"
)

// eventCategories is the canonical event-type → category map. It covers every
// Event* constant in AllEventTypes (enforced by
// TestEventCategories_CoversAllEventTypes) plus the bare-string event types
// emitted from packages outside core/audit: "audit.read.events" (the audit
// read handler), "mcp.tool_called" (core/mcp), and "worker_handshake" (the
// scheduler). Any event type NOT listed here resolves to governance via
// CategoryFor (fail-open) so a newly added or caller-supplied event is never
// silently hidden from a governance-filtered export.
//
// Borderline calls (surfaced in docs/audit.md's category table for review):
// the edge session/execution lifecycle and edge.action_attempted,
// mcp.tool_invocation/outbound_invocation, and topic_(un)registered are
// ROUTINE (operational volume); edge.policy_decision/action_denied/approval_*
// /artifact_exported and every shadow_agent.* finding stay GOVERNANCE.
var eventCategories = map[string]string{
	// --- routine: operational telemetry / noise ---
	EventSystemAuth:                CategoryRoutine,
	EventEdgeSessionStarted:        CategoryRoutine,
	EventEdgeSessionEnded:          CategoryRoutine,
	EventEdgeExecutionStarted:      CategoryRoutine,
	EventEdgeExecutionEnded:        CategoryRoutine,
	EventEdgeActionAttempted:       CategoryRoutine,
	EventMCPToolInvocation:         CategoryRoutine,
	EventMCPToolOutboundInvocation: CategoryRoutine,
	EventTopicRegistered:           CategoryRoutine,
	EventTopicUnregistered:         CategoryRoutine,
	"audit.read.events":            CategoryRoutine,
	"mcp.tool_called":              CategoryRoutine,
	"worker_handshake":             CategoryRoutine,

	// --- governance: security-relevant ---
	EventSafetyDecision:                  CategoryGovernance,
	EventDelegationLineage:               CategoryGovernance,
	EventDelegationRejected:              CategoryGovernance,
	EventDelegationRevokedBeforeDispatch: CategoryGovernance,
	EventSafetyApproval:                  CategoryGovernance,
	EventPolicyChange:                    CategoryGovernance,
	EventSafetyViolation:                 CategoryGovernance,
	EventMCPToolApproval:                 CategoryGovernance,
	EventMCPToolDenied:                   CategoryGovernance,
	EventMCPSignatureInvalid:             CategoryGovernance,
	EventHeartbeatDisagreement:           CategoryGovernance,
	EventApprovalRevisionMismatch:        CategoryGovernance,
	EventWorkerTrustChange:               CategoryGovernance,
	EventLicenseLegacyRejected:           CategoryGovernance,
	EventLicenseBreakglassActivated:      CategoryGovernance,
	EventShadowEval:                      CategoryGovernance,
	EventAuthAPIKeyCreated:               CategoryGovernance,
	EventAuthAPIKeyRevoked:               CategoryGovernance,
	EventAuthRoleUpserted:                CategoryGovernance,
	EventAuthRoleDeleted:                 CategoryGovernance,
	EventEdgePolicyDecision:              CategoryGovernance,
	EventEdgeActionDenied:                CategoryGovernance,
	EventEdgeApprovalRequested:           CategoryGovernance,
	EventEdgeApprovalResolved:            CategoryGovernance,
	EventEdgeApprovalRejected:            CategoryGovernance,
	EventEdgeApprovalExpired:             CategoryGovernance,
	EventEdgeArtifactExported:            CategoryGovernance,
	EventSafetyBypassAdmit:               CategoryGovernance,
	EventShadowAgentDetected:             CategoryGovernance,
	EventShadowAgentResolved:             CategoryGovernance,
	EventShadowAgentSuppressed:           CategoryGovernance,
	EventShadowAgentExceptionCreated:     CategoryGovernance,
	EventShadowAgentExceptionRevoked:     CategoryGovernance,
	EventShadowAgentExceptionApplied:     CategoryGovernance,
	EventActionGateDenied:                CategoryGovernance,
	EventEdgeAgentdDegraded:              CategoryGovernance,
	EventEdgeFailClosed:                  CategoryGovernance,
	EventGovernanceDecision:              CategoryGovernance,
	EventGovernanceLabelSpoof:            CategoryGovernance,
}

// CategoryFor returns the governance/routine category for an event type.
// Unknown or empty types FAIL OPEN to governance: a security event must never
// be silently dropped from a governance-filtered export just because it was
// introduced without a category mapping.
func CategoryFor(eventType string) string {
	if cat, ok := eventCategories[eventType]; ok {
		return cat
	}
	return CategoryGovernance
}

// IsGovernanceEvent reports whether an event type is governance-relevant.
func IsGovernanceEvent(eventType string) bool {
	return CategoryFor(eventType) == CategoryGovernance
}
