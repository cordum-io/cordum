import { describe, it, expect, beforeEach } from "vitest";
import { useEventStore } from "./events";
import type { StreamEvent } from "../api/types";

function makeEvent(id: string, type = "job.submit"): StreamEvent {
  return { id, type, timestamp: new Date().toISOString(), payload: {} };
}

describe("useEventStore", () => {
  beforeEach(() => {
    useEventStore.setState({
      status: "disconnected",
      events: [],
      safetyDecisions: [],
    });
  });

  // -------------------------------------------------------------------------
  // Connection status
  // -------------------------------------------------------------------------

  it("starts with disconnected status", () => {
    expect(useEventStore.getState().status).toBe("disconnected");
  });

  it("transitions through all status values", () => {
    const { setStatus } = useEventStore.getState();
    const statuses = ["connecting", "connected", "reconnecting", "disconnected"] as const;
    for (const s of statuses) {
      setStatus(s);
      expect(useEventStore.getState().status).toBe(s);
    }
  });

  // -------------------------------------------------------------------------
  // Event buffer
  // -------------------------------------------------------------------------

  it("adds events to the buffer (newest first)", () => {
    const { addEvent } = useEventStore.getState();
    addEvent(makeEvent("a"));
    addEvent(makeEvent("b"));
    const ids = useEventStore.getState().events.map((e) => e.id);
    expect(ids).toEqual(["b", "a"]);
  });

  it("caps the buffer at 100 events", () => {
    const { addEvent } = useEventStore.getState();
    for (let i = 0; i < 120; i++) {
      addEvent(makeEvent(`e-${i}`));
    }
    expect(useEventStore.getState().events).toHaveLength(100);
    // Most recent should be first
    expect(useEventStore.getState().events[0].id).toBe("e-119");
  });

  it("clearEvents empties the buffer", () => {
    const { addEvent, clearEvents } = useEventStore.getState();
    addEvent(makeEvent("a"));
    addEvent(makeEvent("b"));
    clearEvents();
    expect(useEventStore.getState().events).toHaveLength(0);
  });

  // -------------------------------------------------------------------------
  // Safety decisions
  // -------------------------------------------------------------------------

  it("pushes safety decisions (newest first)", () => {
    const { pushSafetyDecision } = useEventStore.getState();
    pushSafetyDecision({
      id: "s1",
      timestamp: "2026-01-01T00:00:00Z",
      topic: "job.default",
      decision: "allow",
    });
    pushSafetyDecision({
      id: "s2",
      timestamp: "2026-01-01T00:00:01Z",
      topic: "job.default",
      decision: "deny",
    });
    const ids = useEventStore.getState().safetyDecisions.map((d) => d.id);
    expect(ids).toEqual(["s2", "s1"]);
  });

  it("caps safety decisions at 100", () => {
    const { pushSafetyDecision } = useEventStore.getState();
    for (let i = 0; i < 120; i++) {
      pushSafetyDecision({
        id: `sd-${i}`,
        timestamp: new Date().toISOString(),
        topic: "t",
        decision: "allow",
      });
    }
    expect(useEventStore.getState().safetyDecisions).toHaveLength(100);
    expect(useEventStore.getState().safetyDecisions[0].id).toBe("sd-119");
  });
});
