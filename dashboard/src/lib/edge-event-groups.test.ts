import { describe, expect, it } from "vitest";
import type { AgentActionEvent } from "@/api/types";
import {
  EDGE_EVENT_GROUP_WINDOW_MS,
  groupEdgeEvents,
} from "./edge-event-groups";

// EDGE-050 — unit tests for the edge-event-grouping selector.
// Pure logic; isolated from React. Invariants from the architect's
// step 2 plan + identifier definitions in
// docs/edge/identity-contract.md.

function makeEvent(overrides: Partial<AgentActionEvent>): AgentActionEvent {
  const base: AgentActionEvent = {
    eventId: overrides.eventId ?? "evt-default",
    sessionId: "sess-1",
    executionId: "exec-1",
    tenantId: "default",
    seq: 0,
    ts: "2026-05-04T10:00:00.000Z",
    layer: "hook",
    kind: "hook.pre_tool_use",
    decision: "allow",
    status: "ok",
  };
  return { ...base, ...overrides };
}

describe("groupEdgeEvents", () => {
  it("returns empty array for empty input", () => {
    expect(groupEdgeEvents([])).toEqual([]);
    expect(groupEdgeEvents(undefined)).toEqual([]);
  });

  it("groups three events (receipt + gateway + agentd-evidence) into one row", () => {
    const events: AgentActionEvent[] = [
      // 1) receipt: agentd-prefixed id, status=degraded, kind = tool-call hook.
      makeEvent({
        eventId: "agentd-receipt-1",
        kind: "hook.pre_tool_use",
        toolUseId: "tool-use-A",
        toolName: "Read",
        status: "degraded",
        decisionReason: "received by cordum-agentd; evaluation not ready",
        ts: "2026-05-04T10:00:00.100Z",
      }),
      // 2) gateway authoritative decision: kind=policy_decision, plain UUID.
      makeEvent({
        eventId: "evt-gateway-uuid-1",
        kind: "hook.policy_decision",
        toolUseId: "tool-use-A",
        toolName: "Read",
        decision: "deny",
        ruleId: "claude-code.deny-secret-reads",
        policySnapshot: "cfg:abc",
        ts: "2026-05-04T10:00:00.200Z",
      }),
      // 3) agentd evidence: kind=policy_decision, agentd-prefixed id.
      makeEvent({
        eventId: "agentd-evidence-1",
        kind: "hook.policy_decision",
        toolUseId: "tool-use-A",
        toolName: "Read",
        decision: "deny",
        ruleId: "claude-code.deny-secret-reads",
        policySnapshot: "cfg:abc",
        ts: "2026-05-04T10:00:00.300Z",
      }),
    ];
    const groups = groupEdgeEvents(events);
    expect(groups).toHaveLength(1);
    const g = groups[0];
    expect(g.events).toHaveLength(3);
    expect(g.receipt?.eventId).toBe("agentd-receipt-1");
    expect(g.gatewayDecision?.eventId).toBe("evt-gateway-uuid-1");
    expect(g.agentdEvidence?.eventId).toBe("agentd-evidence-1");
    // Headline = gatewayDecision when both witness and tool-call exist.
    expect(g.headline.eventId).toBe("evt-gateway-uuid-1");
    // No divergence — gateway and agentd agree on every watched field.
    expect(g.divergence).toBeUndefined();
  });

  it("partial group (receipt + gateway only) renders without divergence chip", () => {
    const events: AgentActionEvent[] = [
      makeEvent({
        eventId: "agentd-receipt-2",
        kind: "hook.pre_tool_use",
        toolUseId: "tool-use-B",
        status: "degraded",
        decisionReason: "received by cordum-agentd; evaluation not ready",
        ts: "2026-05-04T10:01:00.000Z",
      }),
      makeEvent({
        eventId: "evt-gateway-uuid-2",
        kind: "hook.policy_decision",
        toolUseId: "tool-use-B",
        decision: "allow",
        ts: "2026-05-04T10:01:00.150Z",
      }),
    ];
    const groups = groupEdgeEvents(events);
    expect(groups).toHaveLength(1);
    expect(groups[0].divergence).toBeUndefined();
    expect(groups[0].agentdEvidence).toBeUndefined();
    expect(groups[0].gatewayDecision?.eventId).toBe("evt-gateway-uuid-2");
  });

  it("events outside the window stay in separate groups", () => {
    const events: AgentActionEvent[] = [
      makeEvent({ eventId: "evt-1", toolUseId: "T", ts: "2026-05-04T10:00:00.000Z" }),
      makeEvent({
        eventId: "evt-2",
        toolUseId: "T",
        ts: "2026-05-04T10:00:01.500Z", // 1500ms later — past 500ms window
      }),
    ];
    const groups = groupEdgeEvents(events);
    expect(groups).toHaveLength(2);
  });

  it("different toolUseId stays in separate groups even within window", () => {
    const events: AgentActionEvent[] = [
      makeEvent({
        eventId: "evt-A",
        toolUseId: "tool-A",
        ts: "2026-05-04T10:00:00.000Z",
      }),
      makeEvent({
        eventId: "evt-B",
        toolUseId: "tool-B",
        ts: "2026-05-04T10:00:00.100Z",
      }),
    ];
    const groups = groupEdgeEvents(events);
    expect(groups).toHaveLength(2);
  });

  it("divergence chip fires when gateway and agentd disagree on decision", () => {
    const events: AgentActionEvent[] = [
      makeEvent({
        eventId: "evt-gateway-3",
        kind: "hook.policy_decision",
        toolUseId: "tool-D",
        decision: "allow",
        ruleId: "rule-xyz",
        policySnapshot: "cfg:abc",
        ts: "2026-05-04T10:02:00.000Z",
      }),
      makeEvent({
        eventId: "agentd-evidence-3",
        kind: "hook.policy_decision",
        toolUseId: "tool-D",
        decision: "deny", // DISAGREES with gateway
        ruleId: "rule-xyz",
        policySnapshot: "cfg:abc",
        ts: "2026-05-04T10:02:00.150Z",
      }),
    ];
    const groups = groupEdgeEvents(events);
    expect(groups).toHaveLength(1);
    expect(groups[0].divergence).toBeDefined();
    expect(groups[0].divergence?.decision).toBe(true);
    expect(groups[0].divergence?.ruleId).toBeFalsy();
    expect(groups[0].divergence?.policySnapshot).toBeFalsy();
  });

  it("pagination boundary: only-receipt-visible yields a single-event group with receipt as headline", () => {
    const events: AgentActionEvent[] = [
      makeEvent({
        eventId: "agentd-receipt-pagination",
        kind: "hook.pre_tool_use",
        toolUseId: "tool-P",
        status: "degraded",
        decisionReason: "received by cordum-agentd; evaluation not ready",
        ts: "2026-05-04T10:03:00.000Z",
      }),
    ];
    const groups = groupEdgeEvents(events);
    expect(groups).toHaveLength(1);
    expect(groups[0].receipt?.eventId).toBe("agentd-receipt-pagination");
    expect(groups[0].headline.eventId).toBe("agentd-receipt-pagination");
    expect(groups[0].divergence).toBeUndefined();
  });

  it("custom window override is respected", () => {
    const events: AgentActionEvent[] = [
      makeEvent({ eventId: "evt-w1", toolUseId: "T", ts: "2026-05-04T10:00:00.000Z" }),
      makeEvent({
        eventId: "evt-w2",
        toolUseId: "T",
        ts: "2026-05-04T10:00:00.100Z",
      }),
    ];
    // 50ms window — events 100ms apart should split.
    expect(groupEdgeEvents(events, { windowMs: 50 })).toHaveLength(2);
    // Default window groups them.
    expect(groupEdgeEvents(events)).toHaveLength(1);
  });

  it("EDGE_EVENT_GROUP_WINDOW_MS default is 500ms", () => {
    expect(EDGE_EVENT_GROUP_WINDOW_MS).toBe(500);
  });
});
