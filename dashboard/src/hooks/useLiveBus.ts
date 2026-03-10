import { useEffect, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { wsProtocols, wsUrl } from "../lib/api";
import { useConfigStore } from "../state/config";
import { useEventStore, type LiveEvent } from "../state/events";
import type { BusPacket } from "../types/api";

function randomId() {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
    return crypto.randomUUID();
  }
  return `evt-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function buildEvent(
  packet: BusPacket,
  input: {
    type: string;
    eventType: string;
    payload: Record<string, unknown>;
    severity?: string;
    jobId?: string;
    runId?: string;
    workflowId?: string;
    chatData?: unknown;
  },
): LiveEvent {
  return {
    id: randomId(),
    type: input.type,
    timestamp: packet.createdAt || new Date().toISOString(),
    payload: input.payload,
    severity: input.severity,
    eventType: input.eventType,
    source: packet.senderId,
    jobId: input.jobId,
    runId: input.runId,
    workflowId: input.workflowId,
    chatData: input.chatData,
  };
}

function eventFromPacket(packet: BusPacket): LiveEvent | null {
  const payload = packet.payload || {};
  if (payload.jobResult) {
    const status = payload.jobResult.status || "UNKNOWN";
    const severity: LiveEvent["severity"] =
      status.includes("SUCCEEDED") ? "success" : status.includes("FAILED") ? "danger" : "warning";
    const title = `Job result: ${payload.jobResult.jobId || "unknown"}`;
    const detail = status.toLowerCase();
    return buildEvent(packet, {
      type: "job.result",
      eventType: "job_result",
      severity,
      jobId: payload.jobResult.jobId,
      payload: {
        ...payload.jobResult,
        title,
        detail,
      },
    });
  }
  if (payload.jobProgress) {
    const title = `Job progress: ${payload.jobProgress.jobId || "unknown"}`;
    const detail = payload.jobProgress.message || "progress update";
    return buildEvent(packet, {
      type: "job.progress",
      eventType: "job_progress",
      severity: "info",
      jobId: payload.jobProgress.jobId,
      payload: {
        ...payload.jobProgress,
        title,
        detail,
      },
    });
  }
  if (payload.jobRequest) {
    const labels = payload.jobRequest.labels || {};
    const runId = labels.run_id || labels.runId;
    const workflowId = payload.jobRequest.workflowId || labels.workflow_id || labels.workflowId;
    const title = `Job submitted: ${payload.jobRequest.jobId || "unknown"}`;
    const detail = payload.jobRequest.topic || "new request";
    return buildEvent(packet, {
      type: "job.submit",
      eventType: "job_request",
      severity: "info",
      jobId: payload.jobRequest.jobId,
      runId,
      workflowId,
      payload: {
        ...payload.jobRequest,
        title,
        detail,
      },
    });
  }
  if (payload.heartbeat) {
    const title = `Worker heartbeat: ${payload.heartbeat.workerId || "worker"}`;
    const detail = payload.heartbeat.pool ? `pool ${payload.heartbeat.pool}` : "";
    return buildEvent(packet, {
      type: "worker.heartbeat",
      eventType: "heartbeat",
      severity: "info",
      payload: {
        ...payload.heartbeat,
        title,
        detail,
      },
    });
  }
  if (payload.alert) {
    return buildEvent(packet, {
      type: "system.alert",
      eventType: "alert",
      severity: payload.alert.level === "critical" ? "danger" : "warning",
      payload: {
        ...payload.alert,
        title: payload.alert.message || "System alert",
        detail: payload.alert.code || "alert",
      },
    });
  }
  if (payload.jobCancel) {
    const title = `Job cancelled: ${payload.jobCancel.jobId || "unknown"}`;
    const detail = payload.jobCancel.reason || "cancelled";
    return buildEvent(packet, {
      type: "job.cancel",
      eventType: "job_cancel",
      severity: "warning",
      jobId: payload.jobCancel.jobId,
      payload: {
        ...payload.jobCancel,
        title,
        detail,
      },
    });
  }
  if (payload.chatMessage) {
    const msg = payload.chatMessage;
    const runId = msg.runId;
    return {
      id: msg.id || randomId(),
      type: "chat.message",
      timestamp: msg.createdAt || packet.createdAt || new Date().toISOString(),
      payload: {
        ...msg,
        title: `Chat: ${msg.role || "agent"}`,
        detail: msg.content?.slice(0, 100) || "",
      },
      severity: "info",
      source: packet.senderId,
      jobId: msg.jobId,
      eventType: "chat_message",
      runId,
      workflowId: undefined,
      chatData: {
        id: msg.id,
        role: msg.role,
        content: msg.content,
        stepId: msg.stepId,
        jobId: msg.jobId,
        agentId: msg.agentId,
        agentName: msg.agentName,
        createdAt: msg.createdAt,
        metadata: msg.metadata,
      },
    };
  }
  return null;
}

export function useLiveBus() {
  const queryClient = useQueryClient();
  const apiKey = useConfigStore((state) => state.apiKey);
  const tenantId = useConfigStore((state) => state.tenantId);
  const loaded = useConfigStore((state) => state.loaded);
  const addEvent = useEventStore((state) => state.addEvent);
  const setStatus = useEventStore((state) => state.setStatus);
  const retryRef = useRef(0);
  const invalidateRef = useRef<Map<string, number>>(new Map());

  const invalidate = (key: unknown[], minInterval = 3000) => {
    const now = Date.now();
    const sig = JSON.stringify(key);
    const last = invalidateRef.current.get(sig) || 0;
    if (now-last < minInterval) {
      return;
    }
    invalidateRef.current.set(sig, now);
    queryClient.invalidateQueries({ queryKey: key });
  };

  const invalidateForEvent = (event: LiveEvent) => {
    switch (event.eventType) {
      case "job_request":
        invalidate(["jobs"]);
        invalidate(["runs"]);
        break;
      case "job_progress":
        invalidate(["jobs"]);
        if (event.jobId) {
          invalidate(["job", event.jobId], 1500);
        }
        break;
      case "job_result":
        invalidate(["jobs"]);
        invalidate(["runs"]);
        invalidate(["approvals"]);
        invalidate(["dlq"]);
        if (event.jobId) {
          invalidate(["job", event.jobId], 1500);
        }
        break;
      case "job_cancel":
        invalidate(["jobs"]);
        invalidate(["runs"]);
        if (event.jobId) {
          invalidate(["job", event.jobId], 1500);
        }
        break;
      case "heartbeat":
        invalidate(["workers"]);
        invalidate(["status"]);
        break;
      case "alert":
        invalidate(["status"]);
        break;
      case "chat_message":
        if (event.runId) {
          invalidate(["chat", event.runId], 1000);
        }
        break;
      default:
        break;
    }
  };

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
      const url = wsUrl("/api/v1/stream", { tenant_id: tenantId });
      const protocols = wsProtocols(apiKey);
      ws = protocols.length > 0 ? new WebSocket(url, protocols) : new WebSocket(url);
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
            invalidateForEvent(liveEvent);
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
  }, [apiKey, tenantId, loaded, addEvent, setStatus]);
}
