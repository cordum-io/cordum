// EDGE-050 — group raw Edge events into one logical row per hook fire
// for the dashboard timeline. Pure function, isolated from React, so the
// page-level component can apply this in a useMemo over the React-Query
// cache without taking ownership of the cache shape.
//
// Background: every hook fire produces THREE backend events by design
// (gateway-issued event_id authoritative decision + agentd-prefixed
// pre-evaluation receipt + agentd-prefixed evidence record). The 3-event
// shape is production-correct for audit verifiability. The dashboard
// view collapses them into ONE visual row with an expand caret —
// presentation only, no server-side change.
//
// See docs/edge/identity-contract.md (EDGE-057) for the full
// dual-witness model + identifier semantics this selector relies on.

import type { AgentActionEvent } from "@/api/types";

/**
 * Default grouping window. Events with a matching tuple
 * (sessionId, executionId, toolUseId, kind_family) within this many
 * milliseconds are merged into one EdgeEventGroup. 500ms keeps siblings
 * together on slow hosts without bridging legitimately distinct hooks.
 */
export const EDGE_EVENT_GROUP_WINDOW_MS = 500;

/**
 * EdgeEventGroup represents one collapsed timeline row. headline drives
 * the row's visible decision/timestamp/tool. receipt + agentdEvidence
 * surface the underlying audit witnesses (when present). divergence is
 * non-undefined ONLY when both gatewayDecision and agentdEvidence are
 * present AND a watched field (decision / ruleId / policySnapshot)
 * differs between them — that's the dashboard surface for the
 * dual-witness audit alarm.
 */
export interface EdgeEventGroup {
  /** Stable id for React keys. Composed from earliest event's id + tuple. */
  id: string;
  /** The event the row's headline UI binds to. */
  headline: AgentActionEvent;
  /** Pre-evaluation receipt event from agentd, if present. */
  receipt?: AgentActionEvent;
  /** Authoritative gateway-issued policy decision, if present. */
  gatewayDecision?: AgentActionEvent;
  /** Agentd-side evidence record of the decision, if present. */
  agentdEvidence?: AgentActionEvent;
  /** All events in the group, in original timestamp order. */
  events: AgentActionEvent[];
  /**
   * Per-field divergence between gatewayDecision and agentdEvidence.
   * Undefined when one of the witnesses is absent OR all watched fields
   * agree. When defined, the row renders an "audit divergence" warning
   * chip and the expand panel highlights the differing field(s).
   */
  divergence?: {
    decision?: boolean;
    ruleId?: boolean;
    policySnapshot?: boolean;
  };
}

interface GroupOptions {
  /** Override EDGE_EVENT_GROUP_WINDOW_MS. */
  windowMs?: number;
}

/**
 * groupEdgeEvents collapses a flat AgentActionEvent[] into per-hook-fire
 * EdgeEventGroup[] for the dashboard timeline. Pure: same input ⇒ same
 * output, no side effects, no hidden state.
 *
 * Algorithm (single pass, O(n) over sorted events):
 *  1. Sort events by ts ascending (stable on existing order for ties).
 *  2. For each event, derive its (sessionId, executionId, toolUseId,
 *     kind_family) tuple. kind_family for receipt + tool-call hooks is
 *     event.kind itself; for hook.policy_decision events the family is
 *     borrowed from the matching tool-call event already in the open
 *     group, OR a synthetic "policy_decision" family if no parent
 *     tool-call event has been seen.
 *  3. Attach to the most recent open group with the same tuple AND
 *     timestamp within windowMs of the group's last event. Else open a
 *     new group.
 *  4. After all events placed: classify each event in each group by
 *     role (receipt / gatewayDecision / agentdEvidence) and compute
 *     headline + divergence.
 *
 * Safe-fallbacks:
 *  - Empty input → empty array.
 *  - Single event in a group → group with only that event populated;
 *    headline = the event itself; no divergence.
 *  - Pagination boundary (only some witnesses visible in the current
 *    page): the group reflects what we see. divergence stays undefined
 *    until both gatewayDecision and agentdEvidence are visible
 *    (false-positive guard per architect's risks R4).
 *  - Unknown kind_family / missing toolUseId: still groups by
 *    (sessionId, executionId, "", event.kind) — same window applies.
 *
 * Identifiers (mirror docs/edge/identity-contract.md §2 Decision IDs):
 *  - receipt: status === "degraded" && reason starts "received by
 *    cordum-agentd" && eventId starts "agentd-" && kind !==
 *    "hook.policy_decision".
 *  - gatewayDecision: kind === "hook.policy_decision" && eventId does
 *    NOT start with "agentd-".
 *  - agentdEvidence: kind === "hook.policy_decision" && eventId starts
 *    with "agentd-".
 */
export function groupEdgeEvents(
  events: AgentActionEvent[] | undefined,
  opts: GroupOptions = {},
): EdgeEventGroup[] {
  if (!events || events.length === 0) {
    return [];
  }
  const windowMs = opts.windowMs ?? EDGE_EVENT_GROUP_WINDOW_MS;

  // Sort by timestamp ascending. Use a stable sort (Array.prototype.sort
  // in modern engines) and parse ts only once per event.
  const sorted = events.slice().sort((a, b) => parseTs(a.ts) - parseTs(b.ts));

  const open: Array<{ tuple: string; lastTs: number; events: AgentActionEvent[] }> = [];

  for (const event of sorted) {
    const eventTs = parseTs(event.ts);
    const family = kindFamily(event);
    const tuple = tupleKey(event, family);
    // Look for an open group with matching tuple and within window.
    const idx = open.findIndex(
      (g) => g.tuple === tuple && eventTs - g.lastTs <= windowMs,
    );
    if (idx >= 0) {
      open[idx].events.push(event);
      open[idx].lastTs = eventTs;
      continue;
    }
    // For hook.policy_decision events that did not match by their own
    // tuple, try to attach to a group that has a tool-call event with
    // matching (sessionId, executionId, toolUseId) and is within window.
    if (event.kind === "hook.policy_decision") {
      const fallback = open.findIndex(
        (g) =>
          g.events[0].sessionId === event.sessionId &&
          g.events[0].executionId === event.executionId &&
          (g.events[0].toolUseId ?? "") === (event.toolUseId ?? "") &&
          eventTs - g.lastTs <= windowMs,
      );
      if (fallback >= 0) {
        open[fallback].events.push(event);
        open[fallback].lastTs = eventTs;
        continue;
      }
    }
    open.push({ tuple, lastTs: eventTs, events: [event] });
  }

  return open.map((g) => buildGroup(g.events));
}

function parseTs(ts: string | undefined): number {
  if (!ts) return 0;
  const n = Date.parse(ts);
  return Number.isFinite(n) ? n : 0;
}

function kindFamily(event: AgentActionEvent): string {
  // For tool-call hook events, the kind itself IS the family. For
  // hook.policy_decision events, return a synthetic family — they'll
  // attach to a tool-call group via the toolUseId fallback when
  // possible, otherwise stand alone.
  if (event.kind === "hook.policy_decision") {
    return "policy_decision";
  }
  return event.kind;
}

function tupleKey(event: AgentActionEvent, family: string): string {
  return [
    event.sessionId,
    event.executionId,
    event.toolUseId ?? "",
    family,
  ].join("|");
}

function buildGroup(events: AgentActionEvent[]): EdgeEventGroup {
  // Classify each event by role.
  let receipt: AgentActionEvent | undefined;
  let gatewayDecision: AgentActionEvent | undefined;
  let agentdEvidence: AgentActionEvent | undefined;
  let firstNonReceipt: AgentActionEvent | undefined;

  for (const event of events) {
    if (isReceiptEvent(event)) {
      // First receipt wins; subsequent receipts in the window are rare
      // and would indicate a backend duplicate-emit bug — keep the
      // earliest so the timestamp accurately reflects hook arrival.
      if (!receipt) receipt = event;
      continue;
    }
    if (event.kind === "hook.policy_decision") {
      if (isAgentdPrefixedId(event.eventId)) {
        if (!agentdEvidence) agentdEvidence = event;
      } else {
        if (!gatewayDecision) gatewayDecision = event;
      }
    }
    if (!firstNonReceipt) firstNonReceipt = event;
  }

  const headline = gatewayDecision ?? firstNonReceipt ?? events[0];
  const divergence = computeDivergence(gatewayDecision, agentdEvidence);

  return {
    id: events[0].eventId + "|" + tupleKey(events[0], kindFamily(events[0])),
    headline,
    receipt,
    gatewayDecision,
    agentdEvidence,
    events,
    divergence,
  };
}

function isReceiptEvent(event: AgentActionEvent): boolean {
  return (
    event.status === "degraded" &&
    typeof event.decisionReason === "string" &&
    event.decisionReason.startsWith("received by cordum-agentd") &&
    isAgentdPrefixedId(event.eventId) &&
    event.kind !== "hook.policy_decision"
  );
}

function isAgentdPrefixedId(eventId: string | undefined): boolean {
  return typeof eventId === "string" && eventId.startsWith("agentd-");
}

function computeDivergence(
  gateway: AgentActionEvent | undefined,
  agentd: AgentActionEvent | undefined,
): EdgeEventGroup["divergence"] {
  if (!gateway || !agentd) return undefined;
  const out: NonNullable<EdgeEventGroup["divergence"]> = {};
  if ((gateway.decision ?? "") !== (agentd.decision ?? "")) {
    out.decision = true;
  }
  if ((gateway.ruleId ?? "") !== (agentd.ruleId ?? "")) {
    out.ruleId = true;
  }
  if ((gateway.policySnapshot ?? "") !== (agentd.policySnapshot ?? "")) {
    out.policySnapshot = true;
  }
  if (!out.decision && !out.ruleId && !out.policySnapshot) {
    return undefined;
  }
  return out;
}
