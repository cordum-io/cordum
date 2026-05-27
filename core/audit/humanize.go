package audit

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Human-readable rendering of audit SIEM events. All helpers here are PURE and
// deterministic: the same event always yields the same output. They never read
// the full Extra map into prose — only a hard-coded allowlist of safe,
// classifier-produced descriptor keys — and they scrub secret-shaped values, so
// a summary can never leak raw prompts, tool payloads, API keys, or tokens.

const (
	maxSummaryLen = 240
	maxLabelLen   = 96
	maxReasonLen  = 120
)

// humanizeExtraAllowlist is the ONLY set of Extra keys humanization may read.
// Extra can carry caller-supplied values, so iterating it wholesale would risk
// leaking secrets — every key here is a non-sensitive, bounded descriptor.
var humanizeExtraAllowlist = map[string]struct{}{
	"resource_id":            {},
	"resource_name":          {},
	"resource_type":          {},
	"session_id":             {},
	"execution_id":           {},
	"job_id":                 {},
	"tool_name":              {},
	"target_type":            {},
	"command_family":         {},
	"action_target_summary":  {},
	"target_summary":         {}, // Edge actionExtra emits this descriptor key
	"principal_id":           {},
	"principal_display_name": {},
	"agent_product":          {},
	"input_preview":          {},
	"output_preview":         {},
	"trace_id":               {},
	"artifact_id":            {},
	"approval_ref":           {},
	"outcome":                {},
}

// allowedExtra returns the trimmed value of ev.Extra[key] only when key is on
// the allowlist; otherwise it returns "". Non-allowlisted keys (e.g. api_key,
// authorization, password) are never surfaced.
func allowedExtra(ev SIEMEvent, key string) string {
	if ev.Extra == nil {
		return ""
	}
	if _, ok := humanizeExtraAllowlist[key]; !ok {
		return ""
	}
	return strings.TrimSpace(ev.Extra[key])
}

// PivotIDs are the navigable identifiers an investigator pivots on from a row.
type PivotIDs struct {
	JobID       string `json:"job_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	ExecutionID string `json:"execution_id,omitempty"`
	ResourceID  string `json:"resource_id,omitempty"`
}

// Pivots extracts the job/session/execution/resource IDs from an event, reading
// only the structured JobID field and allowlisted Extra keys.
func Pivots(ev SIEMEvent) PivotIDs {
	job := strings.TrimSpace(ev.JobID)
	if job == "" {
		job = allowedExtra(ev, "job_id")
	}
	return PivotIDs{
		JobID:       job,
		SessionID:   allowedExtra(ev, "session_id"),
		ExecutionID: allowedExtra(ev, "execution_id"),
		ResourceID:  allowedExtra(ev, "resource_id"),
	}
}

// ActorLabel returns the human/principal that initiated the event. Precedence:
// explicit principal display name → authenticated identity → principal id →
// "system" as a safe non-empty default.
func ActorLabel(ev SIEMEvent) string {
	if dn := allowedExtra(ev, "principal_display_name"); dn != "" {
		return safeSummaryField(dn, maxLabelLen)
	}
	if id := strings.TrimSpace(ev.Identity); id != "" {
		return safeSummaryField(id, maxLabelLen)
	}
	if pid := allowedExtra(ev, "principal_id"); pid != "" {
		return safeSummaryField(pid, maxLabelLen)
	}
	return "system"
}

// AgentLabel returns the governed agent. Precedence: resolved agent name →
// agent product → non-"unlinked" agent id → "".
func AgentLabel(ev SIEMEvent) string {
	if n := strings.TrimSpace(ev.AgentName); n != "" {
		return safeSummaryField(n, maxLabelLen)
	}
	if p := allowedExtra(ev, "agent_product"); p != "" {
		return safeSummaryField(p, maxLabelLen)
	}
	if id := strings.TrimSpace(ev.AgentID); id != "" && id != "unlinked" {
		return safeSummaryField(id, maxLabelLen)
	}
	return ""
}

// ResourceLabel returns the target of the action. Precedence: resource name →
// action target summary → resource type+id → tool name → "".
func ResourceLabel(ev SIEMEvent) string {
	if n := allowedExtra(ev, "resource_name"); n != "" {
		return safeSummaryField(n, maxLabelLen)
	}
	if s := allowedExtra(ev, "action_target_summary"); s != "" {
		return safeSummaryField(s, maxLabelLen)
	}
	if s := allowedExtra(ev, "target_summary"); s != "" {
		return safeSummaryField(s, maxLabelLen)
	}
	rt := allowedExtra(ev, "resource_type")
	rid := allowedExtra(ev, "resource_id")
	switch {
	case rt != "" && rid != "":
		return safeSummaryField(rt+" "+rid, maxLabelLen)
	case rt != "":
		return safeSummaryField(rt, maxLabelLen)
	case rid != "":
		return safeSummaryField(rid, maxLabelLen)
	}
	if tn := allowedExtra(ev, "tool_name"); tn != "" {
		return safeSummaryField(tn, maxLabelLen)
	}
	return ""
}

// HumanSummary returns a single-line, deterministic, bounded, redacted sentence
// describing who/what acted, what happened, and why — suitable for an audit row
// or export column. It uses only structured fields and allowlisted descriptors.
func HumanSummary(ev SIEMEvent) string {
	actor := ActorLabel(ev)
	agent := AgentLabel(ev)
	resource := ResourceLabel(ev)
	tool := safeSummaryField(allowedExtra(ev, "tool_name"), maxLabelLen)
	action := safeSummaryField(ev.Action, maxLabelLen)
	decision := safeSummaryField(ev.Decision, maxLabelLen)

	// Edge/agent activity reads better led by the agent; control-plane/auth
	// activity by the human actor.
	subject := agent
	if subject == "" {
		subject = actor
	}

	var head string
	switch ev.EventType {
	case EventSafetyDecision, EventEdgePolicyDecision, EventGovernanceDecision, EventActionGateDenied, EventSafetyViolation:
		head = fmt.Sprintf("%s — policy decision on %s", subject, orText(resource, action))
	case EventEdgeActionDenied:
		head = fmt.Sprintf("%s was denied %s", subject, orText(toolTarget(tool, resource), action))
	case EventEdgeApprovalRequested:
		head = fmt.Sprintf("%s requested approval for %s", subject, orText(toolTarget(tool, resource), action))
	case EventSafetyApproval, EventEdgeApprovalResolved, EventEdgeApprovalRejected, EventEdgeApprovalExpired:
		ref := allowedExtra(ev, "approval_ref")
		head = strings.TrimSpace(fmt.Sprintf("%s %s approval %s", actor, orText(allowedExtra(ev, "outcome"), "updated"), ref))
	case EventMCPToolInvocation, EventMCPToolOutboundInvocation:
		head = fmt.Sprintf("%s invoked MCP tool %s", actor, orText(tool, action))
	case EventMCPToolDenied:
		head = fmt.Sprintf("%s was denied MCP tool %s", actor, orText(tool, action))
	case EventMCPToolApproval:
		head = fmt.Sprintf("%s %s MCP tool %s", actor, orText(allowedExtra(ev, "outcome"), "reviewed"), orText(tool, action))
	default:
		// Worker/auth and any other (incl. unknown) event types.
		head = strings.TrimSpace(fmt.Sprintf("%s %s", actor, action))
	}

	parts := []string{strings.TrimSpace(head)}
	if decision != "" {
		parts = append(parts, decision)
	}
	if rule := safeSummaryField(ev.MatchedRule, maxLabelLen); rule != "" {
		parts = append(parts, "rule "+rule)
	}
	if reason := safeSummaryField(ev.Reason, maxReasonLen); reason != "" {
		parts = append(parts, "("+reason+")")
	}

	out := strings.TrimSpace(strings.Join(nonEmpty(parts), " — "))
	if out == "" {
		out = orText(action, safeSummaryField(ev.EventType, maxLabelLen))
	}
	if out == "" {
		out = "audit event"
	}
	return clampRunes(out, maxSummaryLen)
}

// BoundedPreview returns a trimmed, secret-scrubbed, length-bounded preview of
// s, suitable for an input_preview / output_preview metadata field or a
// detail-view snippet. It is NOT a raw transcript: a secret-shaped value
// collapses to "[redacted]" and the result is capped at max runes. Producers
// MUST pass only already-policy-permitted, redacted content here.
func BoundedPreview(s string, max int) string {
	return safeSummaryField(s, max)
}

// safeSummaryField trims, scrubs secret-shaped content, and bounds a value
// before it is allowed into a summary or label.
func safeSummaryField(s string, max int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if looksSecretValue(s) {
		s = "[redacted]"
	}
	return clampRunes(s, max)
}

// looksSecretValue reports whether s resembles a credential/secret that must
// never appear in a human-readable summary. Defense-in-depth on top of the
// key allowlist and producer-side redaction.
func looksSecretValue(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return false
	}
	lower := strings.ToLower(t)
	prefixes := []string{
		"sk-", "sk_", "pk-", "pk_", "rk_", "ghp_", "gho_", "ghu_", "ghs_",
		"github_pat_", "akia", "asia", "eyj", "bearer ", "-----begin", "xox",
		"glpat-", "npm_",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	for _, marker := range []string{"password=", "passwd=", "secret=", "token=", "api_key=", "apikey="} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	// Long opaque high-entropy token with no whitespace/path separators.
	if utf8.RuneCountInString(t) >= 32 && !strings.ContainsAny(t, " \t\n/\\") && isOpaqueToken(t) {
		return true
	}
	return false
}

// isOpaqueToken reports whether s is composed solely of base64/hex token runes
// (letters, digits, and -_+/=.), which is characteristic of credentials.
func isOpaqueToken(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '+' || r == '/' || r == '=' || r == '.':
		default:
			return false
		}
	}
	return true
}

// clampRunes truncates s to max runes, appending an ellipsis when cut.
func clampRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	if max == 1 {
		return string(r[:1])
	}
	return strings.TrimSpace(string(r[:max-1])) + "…"
}

// orText returns primary when non-empty, else fallback.
func orText(primary, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return primary
	}
	return fallback
}

// toolTarget joins a tool name and a resource/target descriptor for prose.
func toolTarget(tool, resource string) string {
	switch {
	case tool != "" && resource != "":
		return tool + " " + resource
	case tool != "":
		return tool
	default:
		return resource
	}
}

// nonEmpty filters out blank entries, preserving order.
func nonEmpty(parts []string) []string {
	out := parts[:0:0]
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return out
}
