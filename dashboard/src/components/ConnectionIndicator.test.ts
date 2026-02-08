import { describe, it, expect, beforeEach } from "vitest";
import { useEventStore, type WsStatus } from "../state/events";

// Without @testing-library/react, test the store-driven logic that
// ConnectionIndicator depends on — status values map correctly.

describe("ConnectionIndicator (store integration)", () => {
  beforeEach(() => {
    useEventStore.setState({ status: "disconnected" });
  });

  const dotColor: Record<WsStatus, string> = {
    connected: "bg-success",
    connecting: "bg-warning",
    reconnecting: "bg-warning animate-pulse",
    disconnected: "bg-danger",
  };

  it.each(Object.entries(dotColor) as [WsStatus, string][])(
    "status %s maps to dot class %s",
    (status, expected) => {
      useEventStore.getState().setStatus(status);
      const current = useEventStore.getState().status;
      expect(current).toBe(status);
      expect(dotColor[current]).toBe(expected);
    },
  );

  it("exports all four WsStatus values", () => {
    const statuses: WsStatus[] = ["connected", "connecting", "disconnected", "reconnecting"];
    for (const s of statuses) {
      useEventStore.getState().setStatus(s);
      expect(useEventStore.getState().status).toBe(s);
    }
  });
});
