import { useEffect, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useConfigStore } from "../state/config";
import { useEventStore } from "../state/events";
import type { StreamEvent } from "../api/types";
import { normalizeDecisionType } from "../api/transform";
import { logger } from "../lib/logger";

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const MIN_BACKOFF_MS = 1_000;
const MAX_BACKOFF_MS = 30_000;
const BACKOFF_FACTOR = 2;

// ---------------------------------------------------------------------------
// Derive WebSocket URL from API base URL or current origin
// ---------------------------------------------------------------------------

function wsUrl(apiBaseUrl?: string): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  const override = import.meta.env.VITE_WS_URL;
  if (override) {
    return `${override.replace(/\/+$/, "")}/api/v1/stream`;
  }
  const base = (apiBaseUrl || import.meta.env.VITE_API_URL || "/api/v1").trim();
  const trimmed = base.endsWith("/") ? base.slice(0, -1) : base;
  if (/^https?:\/\//i.test(trimmed)) {
    return `${trimmed.replace(/^http/i, "ws")}/stream`;
  }
  return `${proto}//${window.location.host}${trimmed}/stream`;
}

// ---------------------------------------------------------------------------
// BusPacket (protojson) to StreamEvent
// ---------------------------------------------------------------------------

type BusTimestamp = { seconds?: string | number; nanos?: number };

type BusPacket = {
  traceId?: string;
  senderId?: string;
  createdAt?: BusTimestamp;
  jobRequest?: Record<string, unknown>;
  jobResult?: Record<string, unknown>;
  jobProgress?: Record<string, unknown>;
  jobCancel?: Record<string, unknown>;
  heartbeat?: Record<string, unknown>;
  alert?: Record<string, unknown>;
};

function normalizeEnum(raw?: unknown): string {
  if (typeof raw !== "string") return "";
  return raw.replace(/^.*_/, "").toLowerCase();
}

function timestampFromProto(ts?: BusTimestamp): string {
  if (!ts) return new Date().toISOString();
  const seconds = typeof ts.seconds === "string" ? Number(ts.seconds) : ts.seconds ?? 0;
  const nanos = ts.nanos ?? 0;
  const ms = seconds * 1000 + Math.floor(nanos / 1_000_000);
  const d = new Date(ms);
  return isNaN(d.getTime()) ? new Date().toISOString() : d.toISOString();
}

function busPacketToEvent(packet: BusPacket): StreamEvent | null {
  if (!packet) return null;
  const ts = timestampFromProto(packet.createdAt);
  const traceId = packet.traceId || "";

  if (packet.jobRequest) {
    const jobId = String(packet.jobRequest.jobId ?? "");
    return {
      id: traceId || jobId || crypto.randomUUID(),
      type: "job.submit",
      timestamp: ts,
      payload: {
        jobId,
        topic: packet.jobRequest.topic,
        tenantId: packet.jobRequest.tenantId,
        labels: packet.jobRequest.labels,
      },
    };
  }
  if (packet.jobResult) {
    const jobId = String(packet.jobResult.jobId ?? "");
    const status = normalizeEnum(packet.jobResult.status);
    return {
      id: traceId || jobId || crypto.randomUUID(),
      type: status ? `job.result.${status}` : "job.result",
      timestamp: ts,
      payload: {
        jobId,
        status,
        errorCode: packet.jobResult.errorCode,
        errorMessage: packet.jobResult.errorMessage,
        executionMs: packet.jobResult.executionMs,
        workerId: packet.jobResult.workerId,
      },
    };
  }
  if (packet.jobProgress) {
    const jobId = String(packet.jobProgress.jobId ?? "");
    return {
      id: traceId || jobId || crypto.randomUUID(),
      type: "job.progress",
      timestamp: ts,
      payload: {
        jobId,
        percent: packet.jobProgress.percent,
        message: packet.jobProgress.message,
        status: normalizeEnum(packet.jobProgress.status),
      },
    };
  }
  if (packet.jobCancel) {
    const jobId = String(packet.jobCancel.jobId ?? "");
    return {
      id: traceId || jobId || crypto.randomUUID(),
      type: "job.cancel",
      timestamp: ts,
      payload: {
        jobId,
        reason: packet.jobCancel.reason,
      },
    };
  }
  if (packet.heartbeat) {
    const workerId = String(packet.heartbeat.workerId ?? "");
    return {
      id: traceId || workerId || crypto.randomUUID(),
      type: "worker.heartbeat",
      timestamp: ts,
      payload: {
        workerId,
        pool: packet.heartbeat.pool,
        activeJobs: packet.heartbeat.activeJobs,
        maxParallelJobs: packet.heartbeat.maxParallelJobs,
      },
    };
  }
  if (packet.alert) {
    return {
      id: traceId || crypto.randomUUID(),
      type: "system.alert",
      timestamp: ts,
      payload: packet.alert as Record<string, unknown>,
    };
  }
  return null;
}

// ---------------------------------------------------------------------------
// Map event type prefixes to React Query cache keys to invalidate
// ---------------------------------------------------------------------------

const INVALIDATION_MAP: Record<string, string[][]> = {
  "job.": [["jobs"]],
  "workflow.": [["workflows"]],
  "approval.": [["approvals"], ["approvals", "nav"]],
  "worker.": [["workers"]],
  "dlq.": [["dlq"], ["dlq", "nav"]],
  "policy.": [["policy-bundles"], ["policy-rules"]],
  "run.": [["workflow-runs"], ["runs"]],
  "pack.": [["packs"]],
  "safety.": [["safety"]],
  "audit.": [["audit"]],
};

function invalidateForEvent(
  queryClient: ReturnType<typeof useQueryClient>,
  eventType: string,
): void {
  for (const [prefix, keys] of Object.entries(INVALIDATION_MAP)) {
    if (eventType.startsWith(prefix)) {
      for (const key of keys) {
        queryClient.invalidateQueries({ queryKey: key });
      }
      return;
    }
  }
}

// ---------------------------------------------------------------------------
// Hook
// ---------------------------------------------------------------------------

/**
 * Manages a single WebSocket connection to /api/v1/stream.
 * - Authenticates via subprotocol `cordum-api-key.<base64(apiKey)>`.
 * - Auto-reconnects with exponential backoff.
 * - Dispatches incoming events to React Query cache invalidation.
 * - Pushes safety decision events to the Zustand event store.
 *
 * Call this hook once inside the authenticated app boundary.
 */
export function useEventStream(): void {
  const queryClient = useQueryClient();
  const apiKey = useConfigStore((s) => s.apiKey);
  const apiBaseUrl = useConfigStore((s) => s.apiBaseUrl);
  const wsRef = useRef<WebSocket | null>(null);
  const backoffRef = useRef(MIN_BACKOFF_MS);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const unmountedRef = useRef(false);

  useEffect(() => {
    unmountedRef.current = false;

    if (!apiKey) return;

    const { setStatus, addEvent, pushSafetyDecision } =
      useEventStore.getState();

    function connect() {
      if (unmountedRef.current) return;

      // Auth via subprotocol (use base64url without padding — '=' is invalid in subprotocol names)
      const encoded = btoa(apiKey).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
      const subprotocol = `cordum-api-key.${encoded}`;

      setStatus("connecting");
      const url = wsUrl(apiBaseUrl);
      logger.info("ws", "Connecting", { url });

      const ws = new WebSocket(url, [subprotocol]);
      wsRef.current = ws;

      ws.onopen = () => {
        if (unmountedRef.current) {
          ws.close();
          return;
        }
        backoffRef.current = MIN_BACKOFF_MS;
        setStatus("connected");
        logger.info("ws", "Connected");
      };

      ws.onmessage = (msg) => {
        let packet: BusPacket;
        try {
          packet = JSON.parse(msg.data as string) as BusPacket;
        } catch {
          logger.warn("ws", "Non-JSON frame dropped", { length: (msg.data as string).length });
          return;
        }
        const event = busPacketToEvent(packet);
        if (!event) {
          logger.debug("ws", "Unrecognized packet dropped");
          return;
        }

        // Buffer into Zustand store for live feed
        addEvent(event);

        // Push safety decisions to dedicated buffer
        if (
          event.type.startsWith("safety.") &&
          event.payload &&
          typeof event.payload === "object"
        ) {
          pushSafetyDecision({
            id: event.id,
            timestamp: event.timestamp,
            topic:
              "topic" in event.payload
                ? String(event.payload.topic)
                : "",
            decision:
              "decision" in event.payload && typeof event.payload.decision === "string"
                ? normalizeDecisionType(event.payload.decision)
                : "allow",
            matchedRule:
              "matchedRule" in event.payload
                ? String(event.payload.matchedRule)
                : "rule_id" in event.payload
                  ? String((event.payload as Record<string, unknown>).rule_id)
                  : undefined,
            evalTimeMs:
              "evalTimeMs" in event.payload
                ? Number(event.payload.evalTimeMs)
                : undefined,
          });
        }

        // Invalidate React Query caches
        invalidateForEvent(queryClient, event.type);
      };

      ws.onerror = () => {
        logger.error("ws", "Connection error");
      };

      ws.onclose = () => {
        wsRef.current = null;
        if (unmountedRef.current) {
          setStatus("disconnected");
          return;
        }

        setStatus("reconnecting");
        const delay = backoffRef.current;
        backoffRef.current = Math.min(
          delay * BACKOFF_FACTOR,
          MAX_BACKOFF_MS,
        );
        logger.warn("ws", "Reconnecting", { backoffMs: delay });
        timerRef.current = setTimeout(connect, delay);
      };
    }

    connect();

    return () => {
      unmountedRef.current = true;
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current);
        timerRef.current = null;
      }
      if (wsRef.current) {
        wsRef.current.close();
        wsRef.current = null;
      }
      useEventStore.getState().setStatus("disconnected");
    };
    // Re-connect when apiKey changes (login/logout)
  }, [apiKey, apiBaseUrl, queryClient]);
}
