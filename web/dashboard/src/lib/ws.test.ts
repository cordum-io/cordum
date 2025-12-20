import { describe, it, expect, beforeAll, afterAll, beforeEach } from "vitest";
import { connectStream } from "./ws";
import { useSettingsStore } from "../state/settingsStore";
import { useAuthStore } from "../state/authStore";

const sockets: FakeWebSocket[] = [];

class FakeWebSocket {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSING = 2;
  static CLOSED = 3;

  readyState = FakeWebSocket.CONNECTING;
  url: string;
  closeCalls = 0;

  onopen: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: ((evt: { data: unknown }) => void) | null = null;

  constructor(url: string) {
    this.url = url;
    sockets.push(this);
  }

  open() {
    this.readyState = FakeWebSocket.OPEN;
    this.onopen?.();
  }

  close() {
    this.closeCalls += 1;
    this.readyState = FakeWebSocket.CLOSED;
    this.onclose?.();
  }
}

let realWebSocket: typeof WebSocket | undefined;

beforeAll(() => {
  realWebSocket = globalThis.WebSocket;
  globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
});

afterAll(() => {
  if (realWebSocket) {
    globalThis.WebSocket = realWebSocket;
  } else {
    // @ts-expect-error test cleanup
    delete globalThis.WebSocket;
  }
});

beforeEach(() => {
  sockets.length = 0;
  useAuthStore.setState({ status: "authorized" });
  useSettingsStore.setState({
    wsBase: "ws://localhost:8081/api/v1/stream",
    apiKey: "super-secret-key",
  });
});

describe("connectStream", () => {
  it("avoids closing before websocket is established", () => {
    const statuses: string[] = [];
    const conn = connectStream({
      onStatus: (s) => statuses.push(s),
      onPacket: () => {},
    });

    expect(sockets).toHaveLength(1);
    expect(sockets[0]?.url).toContain("api_key=super-secret-key");

    conn.close();
    expect(sockets[0]?.closeCalls).toBe(0);

    sockets[0]?.open();
    expect(sockets[0]?.closeCalls).toBe(1);
    expect(statuses).toEqual(["connecting"]);
  });
});

