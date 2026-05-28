import { useEffect, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useConfigStore } from "../state/config";
import { useEventStore } from "../state/events";
import { useToastStore } from "../state/toast";
import type { EdgeStreamPayload, StreamEvent } from "../api/types";
import { API_PATHS } from "../lib/constants";
import { queryKeys } from "../lib/queryKeys";
import {
  mapEdgeEventStreamEnvelope,
  mapEdgeStreamPayload,
  normalizeDecisionType,
  type BackendEdgeEventStreamEnvelope,
} from "../api/transform";
import { generateUUID } from "../lib/uuid";
import { logger } from "../lib/logger";

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const MIN_BACKOFF_MS = 1_000;
const MAX_BACKOFF_MS = 30_000;
const BACKOFF_FACTOR = 2;
const PARSE_FAILURE_THRESHOLD = 5;

// ---------------------------------------------------------------------------
// Derive WebSocket URL from API base URL or current origin
// ---------------------------------------------------------------------------

function wsUrl(apiBaseUrl?: string): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  const override = import.meta.env.VITE_WS_URL;
  if (override) {
    return `${override.replace(/\/+$/, "")}${API_PATHS.stream}`;
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
  return raw.toLowerCase();
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
      id: traceId || jobId || generateUUID(),
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
      id: traceId || jobId || generateUUID(),
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
      id: traceId || jobId || generateUUID(),
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
      id: traceId || jobId || generateUUID(),
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
      id: traceId || workerId || generateUUID(),
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
      id: traceId || generateUUID(),
      type: "system.alert",
      timestamp: ts,
      payload: packet.alert as Record<string, unknown>,
    };
  }
  return null;
}

// ---------------------------------------------------------------------------
// Edge event envelopes to StreamEvent
// ---------------------------------------------------------------------------

// Keep this live-feed surface intentionally narrow: stream cache entries may
// contain redacted summaries, ids, decisions, hashes, and artifact pointers;
// raw prompts, tool payloads, transcripts, signed URLs, command output, and
// tokens must never be copied from the WebSocket frame into dashboard state.

function edgePayloadRecord(payload: EdgeStreamPayload): Record<string, unknown> {
  const record: Record<string, unknown> = {};
  if (payload.tenantId) record.tenantId = payload.tenantId;
  if (payload.sessionId) record.sessionId = payload.sessionId;
  if (payload.executionId) record.executionId = payload.executionId;
  if (payload.eventId) record.eventId = payload.eventId;
  if (payload.kind) record.kind = payload.kind;
  if (payload.layer) record.layer = payload.layer;
  if (payload.decision) record.decision = payload.decision;
  if (payload.approvalRef) record.approvalRef = payload.approvalRef;
  if (payload.artifactPtrs && payload.artifactPtrs.length > 0) {
    record.artifactPtrs = payload.artifactPtrs;
  }
  if (payload.summary) record.summary = payload.summary;
  return record;
}

function edgeEnvelopeToEvent(raw: unknown): StreamEvent | null {
  if (!raw || typeof raw !== "object") return null;
  const envelope = mapEdgeEventStreamEnvelope(raw as BackendEdgeEventStreamEnvelope);
  if (!envelope) return null;
  const payload = mapEdgeStreamPayload(envelope);
  if (!payload.eventId || !payload.sessionId || !payload.executionId) return null;
  return {
    id: payload.eventId ?? payload.executionId ?? payload.sessionId ?? generateUUID(),
    type: "edge.event",
    timestamp: envelope.event?.ts ?? new Date().toISOString(),
    payload: edgePayloadRecord(payload),
    eventType: payload.kind,
    source: "edge",
  };
}

function stringField(payload: Record<string, unknown>, key: string): string | undefined {
  const value = payload[key];
  return typeof value === "string" && value.trim() ? value : undefined;
}

function edgeKindNeedsApprovalInvalidation(kind?: string, approvalRef?: string): boolean {
  if (approvalRef) return true;
  const normalized = (kind ?? "").toLowerCase();
  return normalized.includes("approval");
}

function edgeKindNeedsExportInvalidation(
  payload: Record<string, unknown>,
  kind?: string,
): boolean {
  if (Array.isArray(payload.artifactPtrs) && payload.artifactPtrs.length > 0) return true;
  const normalized = (kind ?? "").toLowerCase();
  return (
    normalized.includes("artifact") ||
    normalized.includes("export") ||
    normalized.includes("session.end") ||
    normalized.includes("session_ended")
  );
}

// ---------------------------------------------------------------------------
// Map event type prefixes to React Query cache keys to invalidate
// ---------------------------------------------------------------------------

const INVALIDATION_MAP: Record<string, string[][]> = {
  "job.": [["jobs"], ["dlq"], ["dlq", "nav"]],
  "workflow.": [["workflows"]],
  "approval.": [["approvals"], ["approvals", "nav"]],
  "worker.": [["workers"]],
  "dlq.": [["dlq"], ["dlq", "nav"]],
  "policy.": [["policy-bundles"], ["policy-rules"]],
  "run.": [["workflow-runs"], ["runs"]],
  "pack.": [["packs"]],
  "safety.": [["safety"]],
  "audit.": [["audit"]],
  "scheduler.": [["jobs"], ["workers"]],
  "context.": [["context"]],
};

function invalidateForEvent(
  queryClient: ReturnType<typeof useQueryClient>,
  eventType: string,
  event?: StreamEvent | null,
): void {
  // Extract specific resource ID from the event payload
  const payload = event?.payload as Record<string, unknown> | undefined;
  const jobId = payload?.jobId as string | undefined;
  const workerId = payload?.workerId as string | undefined;

  if (eventType === "edge.event" && payload) {
    const sessionId = stringField(payload, "sessionId");
    const executionId = stringField(payload, "executionId");
    const approvalRef = stringField(payload, "approvalRef");
    const kind = stringField(payload, "kind");

    if (sessionId) {
      queryClient.invalidateQueries({ queryKey: queryKeys.edge.sessions.lists() });
      queryClient.invalidateQueries({ queryKey: queryKeys.edge.executions.lists() });
      queryClient.invalidateQueries({ queryKey: queryKeys.edge.sessions.detail(sessionId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.edge.sessions.eventLists(sessionId) });
      if (edgeKindNeedsExportInvalidation(payload, kind)) {
        queryClient.invalidateQueries({ queryKey: queryKeys.edge.sessions.export(sessionId) });
      }
    }
    if (executionId) {
      queryClient.invalidateQueries({ queryKey: queryKeys.edge.executions.detail(executionId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.edge.executions.eventLists(executionId) });
    }
    if (edgeKindNeedsApprovalInvalidation(kind, approvalRef)) {
      queryClient.invalidateQueries({ queryKey: queryKeys.edge.approvals.lists() });
      if (approvalRef) {
        queryClient.invalidateQueries({ queryKey: queryKeys.edge.approvals.detail(approvalRef) });
      }
    }
    return;
  }

  // Invalidate both detail and list queries so filtered views update in real-time.
  // Using default refetchType ("active") so visible queries refetch immediately.
  if (eventType.startsWith("job.") && jobId) {
    queryClient.invalidateQueries({ queryKey: ["job", jobId] });
    queryClient.invalidateQueries({ queryKey: ["jobs"] });
    queryClient.invalidateQueries({ queryKey: ["dlq"] });
    return;
  }
  if (eventType.startsWith("worker.") && workerId) {
    queryClient.invalidateQueries({ queryKey: ["worker", workerId] });
    queryClient.invalidateQueries({ queryKey: ["workers"] });
    return;
  }
  if (eventType.startsWith("workflow.run") || eventType.startsWith("workflow.step")) {
    const eventObj = event as unknown as Record<string, unknown> | null | undefined;
    const runId = eventObj?.run_id ?? eventObj?.runId;
    if (typeof runId === "string" && runId) {
      queryClient.invalidateQueries({ queryKey: ["workflow-run", runId] });
    }
    queryClient.invalidateQueries({ queryKey: ["workflows"] });
    queryClient.invalidateQueries({ queryKey: ["workflow-runs"] });
    return;
  }

  // Fallback: broad invalidation for events without extractable IDs
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
  const authMode = useConfigStore((s) => s.authMode);
  const isAuthenticated = useConfigStore((s) => s.isAuthenticated);
  const wsRef = useRef<WebSocket | null>(null);
  const backoffRef = useRef(MIN_BACKOFF_MS);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const unmountedRef = useRef(false);
  const parseFailuresRef = useRef(0);

  useEffect(() => {
    unmountedRef.current = false;

    // Connect when authenticated by EITHER credential mode:
    //   - apikey mode  → auth via WS subprotocol `cordum-api-key.<base64(key)>`
    //   - session mode → no subprotocol; the browser sends the httpOnly
    //                    `cordum_session` cookie automatically, and the
    //                    gateway's standard auth middleware
    //                    (handlers_stream.go: auth.FromRequest) accepts it.
    // Previously this gated on `apiKey` alone, so password-logged-in users
    // never connected and the status badge stayed stuck on "disconnected".
    if (!isAuthenticated) return;
    if (authMode === "apikey" && !apiKey) return;

    const { setStatus, addEvent, pushSafetyDecision } =
      useEventStore.getState();

    function connect() {
      if (unmountedRef.current) return;

      setStatus("connecting");
      const url = wsUrl(apiBaseUrl);
      logger.info("ws", "Connecting", { url, authMode });

      let ws: WebSocket;
      if (authMode === "apikey" && apiKey) {
        // Auth via subprotocol — send identifier and credential as separate list
        // entries so the server echoes only "cordum-api-key" (never the key itself).
        const encoded = btoa(apiKey)
          .replace(/\+/g, "-")
          .replace(/\//g, "_")
          .replace(/=+$/, "");
        ws = new WebSocket(url, ["cordum-api-key", encoded]);
      } else {
        // Session mode: cookie carries auth; no subprotocol.
        ws = new WebSocket(url);
      }
      wsRef.current = ws;

      ws.onopen = () => {
        if (unmountedRef.current) {
          ws.close();
          return;
        }
        const wasReconnect = backoffRef.current > MIN_BACKOFF_MS;
        backoffRef.current = MIN_BACKOFF_MS;
        setStatus("connected");
        logger.info("ws", "Connected");

        // On reconnect, selectively invalidate caches to recover missed events.
        // Skip queries that are currently fetching (e.g. in-flight mutations or
        // active refetches) to prevent desync when a user is mid-save.
        if (wasReconnect) {
          const allQueries = queryClient.getQueryCache().getAll();
          const pendingCount = allQueries.filter(
            (q) => q.state.fetchStatus === "fetching",
          ).length;
          logger.info("ws", "Reconnected — selective cache invalidation", {
            total: allQueries.length,
            skipped: pendingCount,
          });
          queryClient.invalidateQueries({
            predicate: (query) => query.state.fetchStatus !== "fetching",
          });
          useToastStore.getState().addToast({
            type: "info",
            title: "Connection restored",
            description: "Data refreshed automatically.",
            duration: 5000,
          });
        }
      };

      ws.onmessage = (msg) => {
        let raw: unknown;
        try {
          raw = JSON.parse(msg.data as string) as unknown;
        } catch {
          parseFailuresRef.current++;
          logger.warn("ws", "Non-JSON frame dropped", {
            length: (msg.data as string).length,
            consecutiveFailures: parseFailuresRef.current,
          });
          if (parseFailuresRef.current >= PARSE_FAILURE_THRESHOLD) {
            setStatus("degraded");
            useToastStore.getState().addToast({
              type: "error",
              title: "Event stream degraded",
              description: "Receiving invalid data — reconnecting...",
              duration: 8000,
            });
            parseFailuresRef.current = 0;
            ws.close();
          }
          return;
        }
        // Reset consecutive failure counter on successful parse.
        parseFailuresRef.current = 0;
        const event = edgeEnvelopeToEvent(raw) ?? busPacketToEvent(raw as BusPacket);
        if (!event) {
          logger.debug("ws", "Unrecognized packet dropped");
          return;
        }
        logger.debug("ws", "Message received", { type: event.type, id: event.id });

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
                : "deny",
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
        invalidateForEvent(queryClient, event.type, event);
      };

      ws.onerror = () => {
        logger.error("ws", "Connection error");
      };

      ws.onclose = (ev) => {
        wsRef.current = null;
        if (unmountedRef.current) {
          logger.info("ws", "Disconnected", { code: ev.code, reason: ev.reason });
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
      // Clear pending reconnect timer FIRST to prevent it firing during cleanup.
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current);
        timerRef.current = null;
      }
      unmountedRef.current = true;
      if (wsRef.current) {
        wsRef.current.close();
        wsRef.current = null;
      }
      useEventStore.getState().setStatus("disconnected");
    };
    // Re-connect when auth state changes (login/logout) or when the API key
    // is set/cleared (embedded-config path can flip apikey mode at runtime).
  }, [apiKey, apiBaseUrl, authMode, isAuthenticated, queryClient]);
}
