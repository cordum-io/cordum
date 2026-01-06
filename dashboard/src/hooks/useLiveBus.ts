import { useEffect, useRef } from "react";
import { wsUrl } from "../lib/api";
import { useConfigStore } from "../state/config";
import { useEventStore, type LiveEvent } from "../state/events";
import type { BusPacket } from "../types/api";

function randomId() {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
    return crypto.randomUUID();
  }
  return `evt-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function eventFromPacket(packet: BusPacket) {
  const payload = packet.payload || {};
  if (payload.jobResult) {
    const status = payload.jobResult.status || "UNKNOWN";
    const severity: LiveEvent["severity"] =
      status.includes("SUCCEEDED") ? "success" : status.includes("FAILED") ? "danger" : "warning";
    return {
      id: randomId(),
      timestamp: packet.createdAt || new Date().toISOString(),
      title: `Job result: ${payload.jobResult.jobId || "unknown"}`,
      detail: status.toLowerCase(),
      severity,
      source: packet.senderId,
      jobId: payload.jobResult.jobId,
      eventType: "job_result",
    };
  }
  if (payload.jobProgress) {
    return {
      id: randomId(),
      timestamp: packet.createdAt || new Date().toISOString(),
      title: `Job progress: ${payload.jobProgress.jobId || "unknown"}`,
      detail: payload.jobProgress.message || "progress update",
      severity: "info" as const,
      source: packet.senderId,
      jobId: payload.jobProgress.jobId,
      eventType: "job_progress",
    };
  }
  if (payload.jobRequest) {
    const labels = payload.jobRequest.labels || {};
    const runId = labels.run_id || labels.runId;
    const workflowId = payload.jobRequest.workflowId || labels.workflow_id || labels.workflowId;
    return {
      id: randomId(),
      timestamp: packet.createdAt || new Date().toISOString(),
      title: `Job submitted: ${payload.jobRequest.jobId || "unknown"}`,
      detail: payload.jobRequest.topic || "new request",
      severity: "info" as const,
      source: packet.senderId,
      jobId: payload.jobRequest.jobId,
      runId,
      workflowId,
      eventType: "job_request",
    };
  }
  if (payload.heartbeat) {
    return {
      id: randomId(),
      timestamp: packet.createdAt || new Date().toISOString(),
      title: `Worker heartbeat: ${payload.heartbeat.workerId || "worker"}`,
      detail: payload.heartbeat.pool ? `pool ${payload.heartbeat.pool}` : "",
      severity: "info" as const,
      source: packet.senderId,
      eventType: "heartbeat",
    };
  }
  if (payload.alert) {
    return {
      id: randomId(),
      timestamp: packet.createdAt || new Date().toISOString(),
      title: payload.alert.message || "System alert",
      detail: payload.alert.code || "alert",
      severity: (payload.alert.level === "critical" ? "danger" : "warning") as LiveEvent["severity"],
      source: packet.senderId,
      eventType: "alert",
    };
  }
  if (payload.jobCancel) {
    return {
      id: randomId(),
      timestamp: packet.createdAt || new Date().toISOString(),
      title: `Job cancelled: ${payload.jobCancel.jobId || "unknown"}`,
      detail: payload.jobCancel.reason || "cancelled",
      severity: "warning" as const,
      source: packet.senderId,
      jobId: payload.jobCancel.jobId,
      eventType: "job_cancel",
    };
  }
  return null;
}

export function useLiveBus() {
  const apiKey = useConfigStore((state) => state.apiKey);
  const loaded = useConfigStore((state) => state.loaded);
  const addEvent = useEventStore((state) => state.addEvent);
  const setStatus = useEventStore((state) => state.setStatus);
  const retryRef = useRef(0);

  useEffect(() => {
    if (!loaded) {
      return;
    }
    let ws: WebSocket | null = null;
    let alive = true;
    let reconnectTimer: number | undefined;

    const connect = () => {
      if (!alive) {
        return;
      }
      setStatus("connecting");
      const url = wsUrl("/api/v1/stream", apiKey);
      ws = new WebSocket(url);
      ws.onopen = () => {
        retryRef.current = 0;
        setStatus("connected");
      };
      ws.onmessage = (event) => {
        try {
          const packet = JSON.parse(event.data) as BusPacket;
          const liveEvent = eventFromPacket(packet);
          if (liveEvent) {
            addEvent(liveEvent);
          }
        } catch {
          // Ignore malformed messages.
        }
      };
      ws.onerror = () => {
        setStatus("disconnected");
      };
      ws.onclose = () => {
        setStatus("disconnected");
        if (!alive) {
          return;
        }
        retryRef.current += 1;
        const delay = Math.min(15000, 1000 * Math.pow(1.6, retryRef.current));
        reconnectTimer = window.setTimeout(connect, delay);
      };
    };

    connect();

    return () => {
      alive = false;
      if (reconnectTimer) {
        window.clearTimeout(reconnectTimer);
      }
      if (ws) {
        ws.close();
      }
    };
  }, [apiKey, loaded, addEvent, setStatus]);
}
