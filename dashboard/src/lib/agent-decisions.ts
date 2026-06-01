import type { AuditEntry } from "@/api/types";

/**
 * AgentDecisionEvidence is the citation surfaced for one governance decision:
 * the prompt-injection taint trail (the cited snippet, the tool that introduced
 * it, the matched taint pattern), the policy sub-reason, the firing rule, the
 * human-readable reason, and — for Edge approvals — the approver identity.
 *
 * Every field is optional; absent or blank sources normalise to `undefined`
 * so the UI can skip them without rendering empty rows.
 */
export interface AgentDecisionEvidence {
  taintSnippet?: string;
  taintPattern?: string;
  taintSourceTool?: string;
  subReason?: string;
  matchedRule?: string;
  reason?: string;
  approver?: string;
}

// trimToUndefined coerces a possibly-blank value to a trimmed string, mapping
// non-strings and empty/whitespace-only strings to undefined.
function trimToUndefined(value: unknown): string | undefined {
  if (typeof value !== "string") return undefined;
  const trimmed = value.trim();
  return trimmed === "" ? undefined : trimmed;
}

/**
 * buildAgentDecisionDeepLink maps one audit entry to the Audit Log deep-link
 * that filters the global feed to exactly this decision. It reuses
 * AuditLogPage's nuqs URL contract:
 *   - `agent`  -> exact agent_id filter
 *   - `action` -> event_type filter
 *   - `search` -> a session/job pivot (the search box matches session/job ids,
 *                 NOT the opaque event id), omitted when neither id exists.
 *
 * Pure + side-effect-free.
 */
export function buildAgentDecisionDeepLink(e: AuditEntry): string {
  const agent = encodeURIComponent(e.agentId ?? "");
  const action = encodeURIComponent(e.eventType ?? "");
  let href = `/audit?agent=${agent}&action=${action}`;
  const pivot = (e.sessionId ?? "") || (e.jobId ?? "");
  if (pivot) {
    href += `&search=${encodeURIComponent(pivot)}`;
  }
  return href;
}

/**
 * decisionEvidence extracts the citation fields for a decision row from the
 * audit entry's typed fields plus its `payload` (the server-redacted Extra
 * map). The taint keys (taint_snippet / taint_pattern / taint_source_tool /
 * sub_reason) survive the audit secret-scrub; the firing rule is `matchedRule`;
 * the Edge approver is `resolved_by`, falling back to `resolver_id` then
 * `approver` (all real EdgeApproval fields).
 *
 * Pure + side-effect-free.
 */
export function decisionEvidence(e: AuditEntry): AgentDecisionEvidence {
  const payload = e.payload ?? {};
  return {
    taintSnippet: trimToUndefined(payload.taint_snippet),
    taintPattern: trimToUndefined(payload.taint_pattern),
    taintSourceTool: trimToUndefined(payload.taint_source_tool),
    subReason: trimToUndefined(payload.sub_reason),
    matchedRule: trimToUndefined(e.matchedRule),
    reason: trimToUndefined(e.reason),
    approver:
      trimToUndefined(payload.resolved_by) ??
      trimToUndefined(payload.resolver_id) ??
      trimToUndefined(payload.approver),
  };
}
