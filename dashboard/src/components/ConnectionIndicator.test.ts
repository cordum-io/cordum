import { describe, it, expect, beforeEach } from "vitest";
import { useEventStore, type WsStatus } from "../state/events";

// Without @testing-library/react, test the store-driven logic that
// ConnectionIndicator depends on — status values map correctly.

type ConnectionStatus = "connected" | "reconnecting" | "disconnected";

/** Mirrors the mapping logic in ConnectionIndicator */
function deriveStatus(wsStatus: WsStatus, browserOnline: boolean): ConnectionStatus {
  if (!browserOnline) return "disconnected";
  if (wsStatus === "connected") return "connected";
  if (wsStatus === "connecting" || wsStatus === "reconnecting") return "reconnecting";
  return "disconnected";
}

describe("ConnectionIndicator (store integration)", () => {
  beforeEach(() => {
    useEventStore.setState({ status: "disconnected" });
  });

  it("exports all four WsStatus values", () => {
    const statuses: WsStatus[] = ["connected", "connecting", "disconnected", "reconnecting"];
    for (const s of statuses) {
      useEventStore.getState().setStatus(s);
      expect(useEventStore.getState().status).toBe(s);
    }
  });

  it("shows disconnected when WS is down even if browser is online", () => {
    useEventStore.getState().setStatus("disconnected");
    expect(deriveStatus(useEventStore.getState().status, true)).toBe("disconnected");
  });

  it("shows connected only when WS is actually connected", () => {
    useEventStore.getState().setStatus("connected");
    expect(deriveStatus(useEventStore.getState().status, true)).toBe("connected");
  });

  it("shows reconnecting for connecting/reconnecting WS states", () => {
    useEventStore.getState().setStatus("connecting");
    expect(deriveStatus(useEventStore.getState().status, true)).toBe("reconnecting");

    useEventStore.getState().setStatus("reconnecting");
    expect(deriveStatus(useEventStore.getState().status, true)).toBe("reconnecting");
  });

  it("shows disconnected when browser is offline regardless of WS state", () => {
    useEventStore.getState().setStatus("connected");
    expect(deriveStatus(useEventStore.getState().status, false)).toBe("disconnected");

    useEventStore.getState().setStatus("reconnecting");
    expect(deriveStatus(useEventStore.getState().status, false)).toBe("disconnected");
  });

  it("settles on connected after rapid connecting-to-connected transitions", () => {
    useEventStore.getState().setStatus("disconnected");
    expect(deriveStatus(useEventStore.getState().status, true)).toBe("disconnected");

    useEventStore.getState().setStatus("connecting");
    expect(deriveStatus(useEventStore.getState().status, true)).toBe("reconnecting");

    useEventStore.getState().setStatus("connected");
    expect(deriveStatus(useEventStore.getState().status, true)).toBe("connected");
  });
});
