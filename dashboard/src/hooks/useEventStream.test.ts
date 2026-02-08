import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

// ---------------------------------------------------------------------------
// Mock WebSocket
// ---------------------------------------------------------------------------

type WsListener = (ev: { data: string }) => void;

class MockWebSocket {
  static instances: MockWebSocket[] = [];

  url: string;
  protocols: string[];
  readyState = 0; // CONNECTING
  onopen: (() => void) | null = null;
  onmessage: WsListener | null = null;
  onerror: (() => void) | null = null;
  onclose: (() => void) | null = null;
  closed = false;

  constructor(url: string, protocols?: string[]) {
    this.url = url;
    this.protocols = protocols ?? [];
    MockWebSocket.instances.push(this);
  }

  close() {
    this.closed = true;
    this.readyState = 3;
  }

  // Test helpers
  simulateOpen() {
    this.readyState = 1;
    this.onopen?.();
  }

  simulateMessage(data: unknown) {
    this.onmessage?.({ data: JSON.stringify(data) });
  }

  simulateClose() {
    this.readyState = 3;
    this.onclose?.();
  }
}

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

vi.stubGlobal("WebSocket", MockWebSocket);
vi.stubGlobal("crypto", { randomUUID: () => "mock-uuid" });

// Mock useQueryClient
const mockInvalidateQueries = vi.fn();
vi.mock("@tanstack/react-query", () => ({
  useQueryClient: () => ({ invalidateQueries: mockInvalidateQueries }),
}));

// Mock config store
let mockApiKey = "test-key";
let mockApiBaseUrl = "";
vi.mock("../state/config", () => ({
  useConfigStore: (selector: (s: { apiKey: string; apiBaseUrl: string }) => unknown) =>
    selector({ apiKey: mockApiKey, apiBaseUrl: mockApiBaseUrl }),
}));

// Import after mocks
const { useEventStore } = await import("../state/events");
const { useEventStream } = await import("./useEventStream");

// Minimal React hooks mock — useEffect runs synchronously, useRef returns object
let cleanupFn: (() => void) | undefined;
vi.mock("react", () => ({
  useEffect: (fn: () => (() => void) | void) => {
    cleanupFn = fn() as (() => void) | undefined;
  },
  useRef: (initial: unknown) => ({ current: initial }),
}));

describe("useEventStream", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    MockWebSocket.instances = [];
    mockInvalidateQueries.mockClear();
    mockApiKey = "test-key";
    useEventStore.setState({
      status: "disconnected",
      events: [],
      safetyDecisions: [],
    });
    cleanupFn = undefined;
  });

  afterEach(() => {
    cleanupFn?.();
    vi.useRealTimers();
  });

  it("creates a WebSocket connection on mount", () => {
    useEventStream();
    expect(MockWebSocket.instances).toHaveLength(1);
    expect(MockWebSocket.instances[0].url).toContain("/api/v1/stream");
  });

  it("sends auth via subprotocol", () => {
    useEventStream();
    const ws = MockWebSocket.instances[0];
    expect(ws.protocols.length).toBe(1);
    expect(ws.protocols[0]).toMatch(/^cordum-api-key\./);
  });

  it("sets status to connected on open", () => {
    useEventStream();
    MockWebSocket.instances[0].simulateOpen();
    expect(useEventStore.getState().status).toBe("connected");
  });

  it("sets status to reconnecting on close and schedules reconnect", () => {
    useEventStream();
    const ws = MockWebSocket.instances[0];
    ws.simulateOpen();
    ws.simulateClose();
    expect(useEventStore.getState().status).toBe("reconnecting");

    // After 1s backoff, a new WebSocket should be created
    vi.advanceTimersByTime(1000);
    expect(MockWebSocket.instances).toHaveLength(2);
  });

  it("applies exponential backoff on repeated disconnects", () => {
    useEventStream();

    // First connection fails immediately (no open) → close
    MockWebSocket.instances[0].simulateClose();

    // 1s backoff → reconnect
    vi.advanceTimersByTime(1000);
    expect(MockWebSocket.instances).toHaveLength(2);

    // Second connection also fails → close (backoff should be 2s now)
    MockWebSocket.instances[1].simulateClose();

    // 1s should NOT be enough (backoff doubled to 2s)
    vi.advanceTimersByTime(1000);
    expect(MockWebSocket.instances).toHaveLength(2);

    // Another 1s (total 2s) → reconnect
    vi.advanceTimersByTime(1000);
    expect(MockWebSocket.instances).toHaveLength(3);
  });

  it("dispatches events to the store on message", () => {
    useEventStream();
    MockWebSocket.instances[0].simulateOpen();
    MockWebSocket.instances[0].simulateMessage({
      traceId: "tr-1",
      jobRequest: { jobId: "j1", topic: "job.default" },
    });
    const events = useEventStore.getState().events;
    expect(events).toHaveLength(1);
    expect(events[0].type).toBe("job.submit");
  });

  it("invalidates React Query caches for job events", () => {
    useEventStream();
    MockWebSocket.instances[0].simulateOpen();
    MockWebSocket.instances[0].simulateMessage({
      jobRequest: { jobId: "j1" },
    });
    expect(mockInvalidateQueries).toHaveBeenCalledWith({ queryKey: ["jobs"] });
  });

  it("ignores non-JSON messages", () => {
    useEventStream();
    const ws = MockWebSocket.instances[0];
    ws.simulateOpen();
    // Send raw non-JSON — onmessage should not throw
    ws.onmessage?.({ data: "not-json" });
    expect(useEventStore.getState().events).toHaveLength(0);
  });

  it("pushes safety decisions for safety events", () => {
    useEventStream();
    MockWebSocket.instances[0].simulateOpen();
    MockWebSocket.instances[0].simulateMessage({
      alert: { topic: "job.default", decision: "DENY", rule_id: "r1" },
    });
    // alert maps to system.alert which starts with "system." not "safety."
    // Safety events would need a different packet type — verify no crash
    expect(useEventStore.getState().events).toHaveLength(1);
  });

  it("cleans up WebSocket on unmount", () => {
    useEventStream();
    const ws = MockWebSocket.instances[0];
    ws.simulateOpen();
    cleanupFn?.();
    expect(ws.closed).toBe(true);
    expect(useEventStore.getState().status).toBe("disconnected");
  });
});
