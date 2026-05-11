import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { renderHook, act, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { http, HttpResponse } from "msw";

import { MockWebSocket } from "@/test-utils/ws";
import { server } from "@/test-utils/msw";
import { fixturePolicyDecisions } from "@/test-utils/fixtures/decisions";
import { DecisionSource } from "@/api/generated/model/decisionSource";
import { DecisionType } from "@/api/generated/model/decisionType";
import type { Decision } from "@/api/generated/model/decision";
import { useConfigStore } from "@/state/config";
import { useDecisionsStream } from "./useDecisionsStream";

// MSW v2.14 wraps `globalThis.WebSocket` when its node interceptor is
// listening, which means a top-level `vi.stubGlobal` is overwritten by the
// time tests run. We instead re-stub inside beforeEach AFTER MSW starts so
// our MockWebSocket is the one the hook sees.

// Helper — wrap renderHook with a QueryClientProvider so the hook's
// internal React Query polling has a client.
function makeWrapper(client: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
  };
}

function newClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0 },
      mutations: { retry: false },
    },
  });
}

function makeDecision(index: number, overrides: Partial<Decision> = {}): Decision {
  return {
    source: DecisionSource.edge,
    rule_id: `rule-${index}`,
    bundle_id: "bundle-x",
    bundle_version: "v1",
    type: DecisionType.allow,
    timestamp: new Date(Date.UTC(2026, 4, 10, 12, 0, index)).toISOString(),
    audit_hash: `sha256:${index}`,
    ...overrides,
  };
}

// MSW server runs lazily on first renderWithProviders; since we use
// renderHook directly, listen explicitly so server.use(...) handlers
// intercept the polling fetch.
import { ensureMswServerListening } from "@/test-utils/msw";

describe("useDecisionsStream", () => {
  beforeEach(() => {
    ensureMswServerListening();
    // Re-stub AFTER msw started. MSW's WebSocket interceptor wraps the
    // global; vi.stubGlobal replaces that wrapper with MockWebSocket.
    vi.stubGlobal("WebSocket", MockWebSocket);
    MockWebSocket.resetInstances();
    // Seed the real Zustand config store so the hook + apiClient both
    // see a non-empty apiKey. Reset to empty afterward to keep stores
    // isolated between describe blocks.
    useConfigStore.setState({ apiKey: "test-key", apiBaseUrl: "" });
  });

  afterEach(() => {
    // Defensive: if a test opted into fake timers, restore real timers.
    vi.useRealTimers();
    useConfigStore.setState({ apiKey: "", apiBaseUrl: "" });
  });

  it("does not open a WebSocket when enabled=false", () => {
    const { result } = renderHook(() => useDecisionsStream({}, false), {
      wrapper: makeWrapper(newClient()),
    });
    expect(MockWebSocket.instances).toHaveLength(0);
    expect(result.current.mode).toBe("closed");
    expect(result.current.decisions).toEqual([]);
  });

  it("opens a WebSocket on enabled=true and reaches mode='ws' after onopen", async () => {
    const { result } = renderHook(() => useDecisionsStream({}, true), {
      wrapper: makeWrapper(newClient()),
    });
    expect(MockWebSocket.instances).toHaveLength(1);
    const ws = MockWebSocket.instances[0];
    expect(ws.url).toContain("/api/v1/policy/decisions/stream");
    expect(ws.protocols).toEqual(["cordum-api-key", expect.any(String)]);
    act(() => ws.simulateOpen());
    await waitFor(() => expect(result.current.mode).toBe("ws"));
  });

  it("prepends incoming WS frames to the ring buffer (newest first)", async () => {
    const { result } = renderHook(() => useDecisionsStream({}, true), {
      wrapper: makeWrapper(newClient()),
    });
    const ws = MockWebSocket.instances[0];
    act(() => ws.simulateOpen());
    act(() => ws.simulateMessage(makeDecision(1)));
    act(() => ws.simulateMessage(makeDecision(2)));
    act(() => ws.simulateMessage(makeDecision(3)));
    await waitFor(() => expect(result.current.decisions).toHaveLength(3));
    expect(result.current.decisions.map((d) => d.rule_id)).toEqual([
      "rule-3",
      "rule-2",
      "rule-1",
    ]);
  });

  it("caps the ring buffer at 200 events (oldest evicted)", async () => {
    const { result } = renderHook(() => useDecisionsStream({}, true), {
      wrapper: makeWrapper(newClient()),
    });
    const ws = MockWebSocket.instances[0];
    act(() => ws.simulateOpen());
    act(() => {
      for (let i = 0; i < 300; i++) {
        ws.simulateMessage(makeDecision(i));
      }
    });
    await waitFor(() => expect(result.current.decisions).toHaveLength(200));
    // Newest first; rule-299 at head, rule-100 at tail.
    expect(result.current.decisions[0].rule_id).toBe("rule-299");
    expect(result.current.decisions[199].rule_id).toBe("rule-100");
  });

  it("ignores non-JSON WS frames", async () => {
    const { result } = renderHook(() => useDecisionsStream({}, true), {
      wrapper: makeWrapper(newClient()),
    });
    const ws = MockWebSocket.instances[0];
    act(() => ws.simulateOpen());
    act(() => ws.simulateMessageRaw("not-json"));
    expect(result.current.decisions).toEqual([]);
    expect(result.current.mode).toBe("ws");
  });

  it("closes the WebSocket and reverts to mode='closed' when enabled flips to false", async () => {
    const { result, rerender } = renderHook(
      ({ enabled }: { enabled: boolean }) =>
        useDecisionsStream({}, enabled),
      { wrapper: makeWrapper(newClient()), initialProps: { enabled: true } },
    );
    const ws = MockWebSocket.instances[0];
    act(() => ws.simulateOpen());
    await waitFor(() => expect(result.current.mode).toBe("ws"));
    rerender({ enabled: false });
    await waitFor(() => expect(ws.closed).toBe(true));
    await waitFor(() => expect(result.current.mode).toBe("closed"));
  });

  it("re-opens with a new socket when filters change", async () => {
    const { rerender } = renderHook(
      ({ source }: { source: DecisionSource }) =>
        useDecisionsStream({ source }, true),
      {
        wrapper: makeWrapper(newClient()),
        initialProps: { source: DecisionSource.edge as DecisionSource },
      },
    );
    expect(MockWebSocket.instances).toHaveLength(1);
    const first = MockWebSocket.instances[0];
    rerender({ source: DecisionSource.job });
    await waitFor(() => expect(first.closed).toBe(true));
    expect(MockWebSocket.instances).toHaveLength(2);
    const second = MockWebSocket.instances[1];
    expect(second.url).toContain("source=job");
  });

  it("auto-falls back to mode='polling' when WS connect times out (3s)", async () => {
    server.use(
      http.get("*/api/v1/policy/decisions", () =>
        HttpResponse.json({
          items: [makeDecision(7, { rule_id: "polled-rule-7" })],
          has_more: false,
        }),
      ),
    );
    // Fake-time only setTimeout/clearTimeout so we can advance the 3s
    // connect timeout. Real timers stay for waitFor + MSW resolution.
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    const { result } = renderHook(() => useDecisionsStream({}, true), {
      wrapper: makeWrapper(newClient()),
    });
    expect(result.current.mode).toBe("closed");
    act(() => {
      vi.advanceTimersByTime(3_100);
    });
    vi.useRealTimers();
    await waitFor(() => expect(result.current.mode).toBe("polling"));
    await waitFor(
      () =>
        expect(
          result.current.decisions.some((d) => d.rule_id === "polled-rule-7"),
        ).toBe(true),
      { timeout: 3000 },
    );
  });

  it("auto-falls back to mode='polling' when WS closes before opening", async () => {
    const { result } = renderHook(() => useDecisionsStream({}, true), {
      wrapper: makeWrapper(newClient()),
    });
    const ws = MockWebSocket.instances[0];
    act(() => ws.simulateClose(1006, "connect refused"));
    await waitFor(() => expect(result.current.mode).toBe("polling"));
  });

  it("polling refills the ring buffer with the freshest server snapshot", async () => {
    server.use(
      http.get("*/api/v1/policy/decisions", () =>
        HttpResponse.json({
          items: fixturePolicyDecisions.slice(0, 3),
          has_more: false,
        }),
      ),
    );
    const { result } = renderHook(() => useDecisionsStream({}, true), {
      wrapper: makeWrapper(newClient()),
    });
    const ws = MockWebSocket.instances[0];
    act(() => ws.simulateClose(1006, "connect refused"));
    await waitFor(() => expect(result.current.mode).toBe("polling"));
    await waitFor(
      () => expect(result.current.decisions).toHaveLength(3),
      { timeout: 3000 },
    );
  });

  it("cleans up on unmount: socket closed, no zombie connections", () => {
    const { unmount } = renderHook(() => useDecisionsStream({}, true), {
      wrapper: makeWrapper(newClient()),
    });
    const ws = MockWebSocket.instances[0];
    unmount();
    expect(ws.closed).toBe(true);
  });

  // Bug 1 (HIGH) lock-in — apiBaseUrl change must tear down the old WS
  // and reopen against the new URL. Without apiBaseUrl in the effect
  // deps, the existing WS keeps streaming from the OLD URL while polling
  // fallback hits the NEW URL — tenant-switch / env-reload split-brain.
  it("re-opens the WebSocket against the new URL when apiBaseUrl changes", async () => {
    useConfigStore.setState({
      apiKey: "test-key",
      apiBaseUrl: "https://api-a.example/api/v1",
    });
    const { result } = renderHook(() => useDecisionsStream({}, true), {
      wrapper: makeWrapper(newClient()),
    });
    expect(MockWebSocket.instances).toHaveLength(1);
    const first = MockWebSocket.instances[0];
    expect(first.url).toContain("api-a.example");
    act(() => first.simulateOpen());
    await waitFor(() => expect(result.current.mode).toBe("ws"));

    act(() => {
      useConfigStore.setState({ apiBaseUrl: "https://api-b.example/api/v1" });
    });
    await waitFor(() => expect(first.closed).toBe(true));
    expect(MockWebSocket.instances).toHaveLength(2);
    const second = MockWebSocket.instances[1];
    expect(second.url).toContain("api-b.example");
  });

  // Bug 2 (MED) lock-in — when the user navigates away during the 3s
  // WS-connect-timeout window, the cleanup return must (a) flip
  // unmountedRef so any in-flight timer callback short-circuits before
  // setMode('polling'), and (b) clearTimeout so the callback never even
  // fires. Together they prevent a setState-on-unmounted-component warn.
  it("does not setState or warn when the 3s connect timeout would fire post-unmount", () => {
    const errorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    const { unmount } = renderHook(() => useDecisionsStream({}, true), {
      wrapper: makeWrapper(newClient()),
    });
    expect(MockWebSocket.instances).toHaveLength(1);
    const ws = MockWebSocket.instances[0];
    // Intentionally do NOT call simulateOpen — leave WS in CONNECTING so
    // the 3s timer is the only side-effect path remaining.
    unmount();
    expect(ws.closed).toBe(true);
    // Advance well past the 3s connect timeout. With cleanup + the guard
    // both in place, no callback should reach setMode('polling').
    vi.advanceTimersByTime(3_500);
    vi.useRealTimers();
    const unmountedWarns = errorSpy.mock.calls.filter((call) =>
      /update.*unmounted|memory leak|can'?t perform a react state update/i.test(
        String(call[0]),
      ),
    );
    expect(unmountedWarns).toEqual([]);
    errorSpy.mockRestore();
  });
});
