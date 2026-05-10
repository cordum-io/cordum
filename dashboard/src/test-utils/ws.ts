// MockWebSocket test helper. Extracted from useEventStream.test.ts so a
// second consumer (useDecisionsStream.test.ts) can reuse the same surface
// without copy-pasting the class. Per epic SHARED CODE FIRST rail: 2+
// consumers + co-located test (useEventStream/useDecisionsStream).

type WsListener = (ev: { data: string }) => void;

export class MockWebSocket {
  static instances: MockWebSocket[] = [];

  url: string;
  protocols: string[];
  readyState = 0; // CONNECTING
  onopen: (() => void) | null = null;
  onmessage: WsListener | null = null;
  onerror: (() => void) | null = null;
  onclose:
    | ((ev: { code: number; reason: string; wasClean: boolean }) => void)
    | null = null;
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

  // Test helpers --------------------------------------------------------

  simulateOpen() {
    this.readyState = 1;
    this.onopen?.();
  }

  simulateMessage(data: unknown) {
    this.onmessage?.({ data: JSON.stringify(data) });
  }

  simulateMessageRaw(raw: string) {
    this.onmessage?.({ data: raw });
  }

  simulateClose(code = 1006, reason = "") {
    this.readyState = 3;
    this.onclose?.({ code, reason, wasClean: false });
  }

  static resetInstances() {
    MockWebSocket.instances = [];
  }
}

/**
 * Install MockWebSocket as the global `WebSocket`. Returns a cleanup that
 * resets `instances` to empty (call from `beforeEach`/`afterEach`).
 */
export function installMockWebSocket(): { reset: () => void } {
  // vi.stubGlobal is not available outside a test file; the caller can do
  // `vi.stubGlobal("WebSocket", MockWebSocket)` directly. This export keeps
  // the API symmetric when the call site already sets up the global.
  return {
    reset: () => MockWebSocket.resetInstances(),
  };
}
