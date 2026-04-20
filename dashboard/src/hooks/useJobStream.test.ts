import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

// ---------------------------------------------------------------------------
// Mock WebSocket
// ---------------------------------------------------------------------------

type WsListener = (ev: { data: string }) => void;

class MockWebSocket {
  static instances: MockWebSocket[] = [];

  url: string;
  protocols: string[];
  readyState = 0;
  onopen: (() => void) | null = null;
  onmessage: WsListener | null = null;
  onerror: (() => void) | null = null;
  onclose: (() => void) | null = null;
  closed = false;
  sentMessages: string[] = [];

  constructor(url: string, protocols?: string[]) {
    this.url = url;
    this.protocols = protocols ?? [];
    MockWebSocket.instances.push(this);
  }

  send(data: string) {
    this.sentMessages.push(data);
  }

  close() {
    this.closed = true;
    this.readyState = 3;
  }

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

let mockApiKey = "test-key";
let mockApiBaseUrl = "";
vi.mock("../state/config", () => ({
  useConfigStore: (selector: (s: { apiKey: string; apiBaseUrl: string }) => unknown) =>
    selector({ apiKey: mockApiKey, apiBaseUrl: mockApiBaseUrl }),
}));

vi.mock("../lib/logger", () => ({
  logger: { debug: vi.fn(), info: vi.fn(), warn: vi.fn(), error: vi.fn() },
}));

// Minimal React hooks mock — useEffect runs synchronously
let cleanupFn: (() => void) | undefined;
const stateValues: Map<number, unknown> = new Map();
let stateCounter = 0;

vi.mock("react", () => ({
  useEffect: (fn: () => (() => void) | void) => {
    cleanupFn = fn() as (() => void) | undefined;
  },
  useRef: (initial: unknown) => ({ current: initial }),
  useCallback: (fn: unknown) => fn,
  useState: (initial: unknown) => {
    const idx = stateCounter++;
    if (!stateValues.has(idx)) stateValues.set(idx, initial);
    const setter = (v: unknown) => {
      stateValues.set(idx, typeof v === "function" ? (v as (prev: unknown) => unknown)(stateValues.get(idx)) : v);
    };
    return [stateValues.get(idx), setter];
  },
}));

const { useJobStream } = await import("./useJobStream");

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function resetStateMocks() {
  stateCounter = 0;
  stateValues.clear();
}

function callHook(jobId: string, enabled: boolean) {
  resetStateMocks();
  return useJobStream(jobId, enabled);
}

function latestWs(): MockWebSocket {
  return MockWebSocket.instances[MockWebSocket.instances.length - 1];
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("useJobStream", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    MockWebSocket.instances = [];
    mockApiKey = "test-key";
    mockApiBaseUrl = "";
    cleanupFn = undefined;
  });

  afterEach(() => {
    cleanupFn?.();
    vi.useRealTimers();
  });

  it("connects when enabled=true with correct URL and subprotocol", () => {
    callHook("j-123", true);
    expect(MockWebSocket.instances).toHaveLength(1);
    const ws = latestWs();
    expect(ws.url).toContain("/api/v1/stream");
    expect(ws.protocols[0]).toMatch(/^cordum-api-key\./);
  });

  it("sends subscribe message on open", () => {
    callHook("j-123", true);
    const ws = latestWs();
    ws.simulateOpen();
    expect(ws.sentMessages).toHaveLength(1);
    expect(JSON.parse(ws.sentMessages[0])).toEqual({ type: "subscribe", jobId: "j-123" });
  });

  it("does not connect when enabled=false", () => {
    callHook("j-123", false);
    expect(MockWebSocket.instances).toHaveLength(0);
  });

  it("does not connect when jobId is empty", () => {
    callHook("", true);
    expect(MockWebSocket.instances).toHaveLength(0);
  });

  it("parses jobProgress message into progressEvents", () => {
    const result = callHook("j-123", true);
    const ws = latestWs();
    ws.simulateOpen();
    ws.simulateMessage({ type: "jobProgress", jobId: "j-123", percent: 42, message: "Processing" });

    // progressEvents is a ref array — mutated in place
    expect(result.progressEvents).toHaveLength(1);
    expect(result.progressEvents[0].percent).toBe(42);
    expect(result.progressEvents[0].message).toBe("Processing");
  });

  it("ignores jobProgress for different jobId", () => {
    const result = callHook("j-123", true);
    const ws = latestWs();
    ws.simulateOpen();
    ws.simulateMessage({ type: "jobProgress", jobId: "j-other", percent: 50, message: "Wrong job" });

    expect(result.progressEvents).toHaveLength(0);
  });

  it("tracks heartbeat messages via lastHeartbeat state", () => {
    // Find the lastHeartbeat state index by checking which state was initially null
    // and gets updated to a truthy ISO string after heartbeat
    const result = callHook("j-123", true);
    const ws = latestWs();
    ws.simulateOpen();

    // Before heartbeat
    expect(result.lastHeartbeat).toBeNull();

    ws.simulateMessage({ type: "heartbeat" });

    // After heartbeat — check any state value is an ISO timestamp string
    let foundHeartbeat = false;
    for (const [, val] of stateValues) {
      if (typeof val === "string" && val.includes("T") && val.includes("Z")) {
        foundHeartbeat = true;
        break;
      }
    }
    expect(foundHeartbeat).toBe(true);
  });

  it("disconnects when cleanup is called (unmount)", () => {
    callHook("j-123", true);
    const ws = latestWs();
    ws.simulateOpen();
    expect(ws.closed).toBe(false);

    cleanupFn?.();
    expect(ws.closed).toBe(true);
  });

  it("reconnects with exponential backoff on close", () => {
    callHook("j-123", true);
    expect(MockWebSocket.instances).toHaveLength(1);
    const ws1 = latestWs();
    ws1.simulateOpen();
    ws1.simulateClose();

    // After 1s backoff, a new WebSocket should be created
    vi.advanceTimersByTime(1000);
    expect(MockWebSocket.instances).toHaveLength(2);

    // Second close WITHOUT open — backoff should still be 2s (1000 * 2, not reset)
    const ws2 = latestWs();
    ws2.simulateClose();

    // 1s should NOT be enough (backoff is now 2s)
    vi.advanceTimersByTime(1000);
    expect(MockWebSocket.instances).toHaveLength(2);

    // Another 1s (total 2s) — reconnect
    vi.advanceTimersByTime(1000);
    expect(MockWebSocket.instances).toHaveLength(3);
  });

  it("resets backoff on successful connection", () => {
    callHook("j-123", true);
    const ws1 = latestWs();
    ws1.simulateOpen();
    ws1.simulateClose();

    // First reconnect at 1s
    vi.advanceTimersByTime(1000);
    const ws2 = latestWs();
    ws2.simulateOpen(); // This resets backoff
    ws2.simulateClose();

    // Should reconnect after 1s again (not 2s) since backoff was reset
    vi.advanceTimersByTime(1000);
    expect(MockWebSocket.instances).toHaveLength(3);
  });

  it("does not reconnect after cleanup (unmount)", () => {
    callHook("j-123", true);
    const ws = latestWs();
    ws.simulateOpen();

    cleanupFn?.();
    ws.simulateClose();

    vi.advanceTimersByTime(5000);
    expect(MockWebSocket.instances).toHaveLength(1);
  });

  it("caps events at 500", () => {
    const result = callHook("j-123", true);
    const ws = latestWs();
    ws.simulateOpen();

    for (let i = 0; i < 510; i++) {
      ws.simulateMessage({ type: "jobProgress", jobId: "j-123", percent: i % 100, message: `msg-${i}` });
    }

    expect(result.progressEvents.length).toBeLessThanOrEqual(500);
  });

  it("handles malformed messages gracefully", () => {
    const result = callHook("j-123", true);
    const ws = latestWs();
    ws.simulateOpen();

    // Send raw invalid JSON
    ws.onmessage?.({ data: "not-json" });
    expect(result.progressEvents).toHaveLength(0);
  });

  it("uses base64url encoding for api key (no padding)", () => {
    mockApiKey = "key+with/special=chars";
    callHook("j-123", true);
    const ws = latestWs();
    const proto = ws.protocols[0];
    expect(proto).not.toContain("+");
    expect(proto).not.toContain("/");
    expect(proto).not.toContain("=");
  });
});
