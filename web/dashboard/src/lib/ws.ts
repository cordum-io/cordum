import { useSettingsStore } from "../state/settingsStore";
import { useAuthStore } from "../state/authStore";
import type { StreamPacket } from "../state/streamStore";

export type WSStatus = "disconnected" | "connecting" | "connected" | "unauthorized" | "error";

export type StreamCallbacks = {
  onStatus: (status: WSStatus) => void;
  onPacket: (packet: StreamPacket) => void;
};

function buildWSURL(): string {
  const { wsBase, apiKey } = useSettingsStore.getState();
  const url = new URL(wsBase);
  if (apiKey) {
    url.searchParams.set("api_key", apiKey);
  }
  return url.toString();
}

export function connectStream(callbacks: StreamCallbacks) {
  const authStatus = useAuthStore.getState().status;
  if (authStatus === "missing_api_key" || authStatus === "invalid_api_key") {
    callbacks.onStatus("unauthorized");
    return { close: () => {} };
  }

  let ws: WebSocket | null = null;
  let closed = false;
  let attempt = 0;

  const connect = () => {
    if (closed) {
      return;
    }
    attempt += 1;
    callbacks.onStatus("connecting");
    const url = buildWSURL();
    ws = new WebSocket(url);

    ws.onopen = () => {
      if (closed) {
        ws?.close();
        return;
      }
      attempt = 0;
      callbacks.onStatus("connected");
    };

    ws.onclose = () => {
      if (closed) {
        return;
      }
      const currentAuth = useAuthStore.getState().status;
      if (currentAuth === "missing_api_key" || currentAuth === "invalid_api_key") {
        callbacks.onStatus("unauthorized");
        return;
      }
      callbacks.onStatus("disconnected");
      const delay = Math.min(10_000, 250 * Math.pow(2, attempt));
      setTimeout(connect, delay);
    };

    ws.onerror = () => {
      if (closed) {
        return;
      }
      callbacks.onStatus("error");
    };

    ws.onmessage = (evt) => {
      if (closed) {
        return;
      }
      try {
        const packet = JSON.parse(evt.data) as StreamPacket;
        callbacks.onPacket(packet);
      } catch {
        // ignore
      }
    };
  };

  connect();

  return {
    close: () => {
      closed = true;
      if (ws?.readyState === WebSocket.CONNECTING) {
        return;
      }
      ws?.close();
      ws = null;
    },
  };
}
