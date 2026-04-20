import { describe, it, expect, beforeEach } from "vitest";
import { useEventStore } from "../events";
import { useConfigStore } from "../config";

describe("EventStore.reset()", () => {
  beforeEach(() => {
    // Seed the store with data
    const store = useEventStore.getState();
    store.addEvent({ id: "e1", type: "job.submit", timestamp: "2026-01-01T00:00:00Z", payload: {} });
    store.pushSafetyDecision({ id: "sd1", timestamp: "2026-01-01T00:00:00Z", topic: "job.test", decision: "allow" });
    store.setReviewing("apr-1", "alice");
    store.assignApproval("apr-2", "bob");
    store.setStatus("connected");
  });

  it("clears all state fields back to initial values", () => {
    // Verify pre-conditions
    const before = useEventStore.getState();
    expect(before.events).toHaveLength(1);
    expect(before.safetyDecisions).toHaveLength(1);
    expect(before.approvalPresence.size).toBe(1);
    expect(before.approvalAssignments.size).toBe(1);
    expect(before.status).toBe("connected");

    // Reset
    useEventStore.getState().reset();

    // Verify post-conditions
    const after = useEventStore.getState();
    expect(after.events).toHaveLength(0);
    expect(after.safetyDecisions).toHaveLength(0);
    expect(after.approvalPresence.size).toBe(0);
    expect(after.approvalAssignments.size).toBe(0);
    expect(after.status).toBe("disconnected");
  });

  it("produces fresh Map instances (no stale references)", () => {
    const beforePresence = useEventStore.getState().approvalPresence;
    const beforeAssignments = useEventStore.getState().approvalAssignments;

    useEventStore.getState().reset();

    const afterPresence = useEventStore.getState().approvalPresence;
    const afterAssignments = useEventStore.getState().approvalAssignments;

    expect(afterPresence).not.toBe(beforePresence);
    expect(afterAssignments).not.toBe(beforeAssignments);
  });
});

describe("ConfigStore.logout() resets EventStore", () => {
  beforeEach(() => {
    // Seed event store with cross-session data
    const store = useEventStore.getState();
    store.addEvent({ id: "e1", type: "job.submit", timestamp: "2026-01-01T00:00:00Z", payload: {} });
    store.setReviewing("apr-1", "alice");
    store.assignApproval("apr-2", "bob");
    store.setStatus("connected");
  });

  it("clears event store presence data on logout", () => {
    // Pre-condition: event store has data
    expect(useEventStore.getState().approvalPresence.size).toBe(1);
    expect(useEventStore.getState().approvalAssignments.size).toBe(1);
    expect(useEventStore.getState().events).toHaveLength(1);

    // Logout
    useConfigStore.getState().logout();

    // Event store should be reset
    expect(useEventStore.getState().approvalPresence.size).toBe(0);
    expect(useEventStore.getState().approvalAssignments.size).toBe(0);
    expect(useEventStore.getState().events).toHaveLength(0);
    expect(useEventStore.getState().status).toBe("disconnected");
  });
});

describe("ConfigStore.update() resets EventStore on tenant switch", () => {
  beforeEach(() => {
    // Set initial tenant
    useConfigStore.setState({ tenantId: "tenant-a" });
    // Seed event store
    const store = useEventStore.getState();
    store.setReviewing("apr-1", "alice");
    store.assignApproval("apr-2", "bob");
    store.setStatus("connected");
  });

  it("resets event store when tenantId changes", () => {
    expect(useEventStore.getState().approvalPresence.size).toBe(1);

    useConfigStore.getState().update({ tenantId: "tenant-b" });

    expect(useEventStore.getState().approvalPresence.size).toBe(0);
    expect(useEventStore.getState().approvalAssignments.size).toBe(0);
    expect(useEventStore.getState().status).toBe("disconnected");
  });

  it("does not reset event store when tenantId stays the same", () => {
    expect(useEventStore.getState().approvalPresence.size).toBe(1);

    useConfigStore.getState().update({ tenantId: "tenant-a" });

    // Should NOT reset — same tenant
    expect(useEventStore.getState().approvalPresence.size).toBe(1);
    expect(useEventStore.getState().status).toBe("connected");
  });
});
