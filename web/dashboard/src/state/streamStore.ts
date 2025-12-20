import { create } from "zustand";
import { connectStream, type WSStatus } from "../lib/ws";

export type StreamPacket = Record<string, unknown>;

export type StreamEvent = {
  received_at: number;
  trace_id?: string;
  sender_id?: string;
  kind: string;
  summary: string;
  packet: StreamPacket;
};

type StreamState = {
  status: WSStatus;
  events: StreamEvent[];
  connect: () => void;
  disconnect: () => void;
  clear: () => void;
};

const maxEvents = 500;

function detectKind(packet: StreamPacket): string {
  if ("jobRequest" in packet) return "jobRequest";
  if ("jobResult" in packet) return "jobResult";
  if ("heartbeat" in packet) return "heartbeat";
  if ("alert" in packet) return "alert";
  return "unknown";
}

function buildSummary(kind: string, packet: StreamPacket): string {
  try {
    if (kind === "jobRequest") {
      const req = packet.jobRequest as any;
      return `jobRequest jobId=${req?.jobId ?? "-"} topic=${req?.topic ?? "-"}`;
    }
    if (kind === "jobResult") {
      const res = packet.jobResult as any;
      return `jobResult jobId=${res?.jobId ?? "-"} status=${res?.status ?? "-"}`;
    }
    if (kind === "heartbeat") {
      const hb = packet.heartbeat as any;
      return `heartbeat workerId=${hb?.workerId ?? "-"} pool=${hb?.pool ?? "-"}`;
    }
  } catch {
    // ignore
  }
  return kind;
}

let conn: { close: () => void } | null = null;

export const useStreamStore = create<StreamState>((set, get) => ({
  status: "disconnected",
  events: [],
  connect: () => {
    conn?.close();
    conn = connectStream({
      onStatus: (status) => {
        set({ status });
        if (status === "error") {
          const evt: StreamEvent = {
            received_at: Date.now(),
            kind: "error",
            summary: "WebSocket connection error. Check the API server.",
            packet: {},
          };
          const next = [...get().events, evt];
          set({ events: next.slice(-maxEvents) });
        }
      },
      onPacket: (packet) => {
        const kind = detectKind(packet);
        const evt: StreamEvent = {
          received_at: Date.now(),
          trace_id: typeof packet.traceId === "string" ? packet.traceId : undefined,
          sender_id: typeof packet.senderId === "string" ? packet.senderId : undefined,
          kind,
          summary: buildSummary(kind, packet),
          packet,
        };
        const next = [...get().events, evt];
        set({ events: next.slice(-maxEvents) });
      },
    });
  },
  disconnect: () => {
    conn?.close();
    conn = null;
    set({ status: "disconnected" });
  },
  clear: () => set({ events: [] }),
}));

