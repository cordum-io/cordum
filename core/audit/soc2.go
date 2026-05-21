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
