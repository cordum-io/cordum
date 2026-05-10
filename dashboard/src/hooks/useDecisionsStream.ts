import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useConfigStore } from "@/state/config";
import { listPolicyDecisions } from "@/api/generated/policy/policy";
import type { Decision } from "@/api/generated/model/decision";
import type { DecisionSource } from "@/api/generated/model/decisionSource";
import type { DecisionType } from "@/api/generated/model/decisionType";
import { logger } from "@/lib/logger";

// Backend 5b notes (cordum/docs/api/decisions-stream.md):
//   - GET /api/v1/policy/decisions/stream upgrades to WebSocket; emits one
//     Decision JSON per TEXT frame.
//   - Server-side filters: ?source=, ?type= (no ?since= on WS).
//   - Edge decisions go to the broker; job decisions DO NOT yet (scheduler
//     is a separate process). Live UIs that need job rows poll the REST
//     endpoint with a rolling `since=` window.
// useDecisionsStream embraces that split via auto-fallback to polling.

export interface DecisionsStreamFilters {
  source?: DecisionSource;
  type?: DecisionType;
}

export type DecisionsStreamMode = "ws" | "polling" | "closed";

export interface UseDecisionsStreamResult {
  decisions: Decision[];
  mode: DecisionsStreamMode;
}

const RING_BUFFER_CAP = 200;
const WS_CONNECT_TIMEOUT_MS = 3_000;
const POLLING_INTERVAL_MS = 2_000;
const POLLING_LIMIT = 100;

function streamUrl(
  apiBaseUrl: string | undefined,
  filters: DecisionsStreamFilters,
): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  const override = import.meta.env.VITE_WS_URL;
  const baseRoot = (apiBaseUrl || import.meta.env.VITE_API_URL || "/api/v1").trim();
  const trimmed = baseRoot.endsWith("/") ? baseRoot.slice(0, -1) : baseRoot;
  const search = filterQueryString(filters);

  if (override) {
    return `${override.replace(/\/+$/, "")}/policy/decisions/stream${search}`;
  }
  if (/^https?:\/\//i.test(trimmed)) {
    return `${trimmed.replace(/^http/i, "ws")}/policy/decisions/stream${search}`;
  }
  return `${proto}//${window.location.host}${trimmed}/policy/decisions/stream${search}`;
}

function filterQueryString(filters: DecisionsStreamFilters): string {
  const params = new URLSearchParams();
  if (filters.source) params.set("source", filters.source);
  if (filters.type) params.set("type", filters.type);
  const s = params.toString();
  return s ? `?${s}` : "";
}

function filterKey(filters: DecisionsStreamFilters): string {
  return `${filters.source ?? ""}|${filters.type ?? ""}`;
}

function base64UrlApiKey(apiKey: string): string {
  return btoa(apiKey).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

/**
 * Live-mode Decisions stream. Returns the most recent decisions in a
 * 200-event ring buffer (newest first), and the current mode so the page
 * can branch UI between live + historical states.
 *
 * - `enabled=false`: closed. No WS, no polling, decisions=[].
 * - `enabled=true` + WS opens within 3s: mode='ws'. Frames push into the
 *   ring buffer. WS server-side filters source/type.
 * - `enabled=true` + WS fails to open OR closes after open: mode='polling'.
 *   React Query polls /api/v1/policy/decisions every 2s and replaces the
 *   ring buffer with the freshest server snapshot.
 *
 * Cleanup is exhaustive: connect-timeout cleared, WS closed, polling
 * disabled on unmount or enabled flip.
 */
export function useDecisionsStream(
  filters: DecisionsStreamFilters,
  enabled: boolean,
): UseDecisionsStreamResult {
  const apiKey = useConfigStore((s) => s.apiKey);
  const apiBaseUrl = useConfigStore((s) => s.apiBaseUrl);

  const [mode, setMode] = useState<DecisionsStreamMode>("closed");
  // tick forces a re-render after we mutate the ring-buffer ref. Using a
  // ref + manual tick avoids per-frame React state churn for the buffer
  // itself (React Query renders are still cheap).
  const [, setTick] = useState(0);
  const ringRef = useRef<Decision[]>([]);
  const wsRef = useRef<WebSocket | null>(null);
  const connectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const unmountedRef = useRef(false);

  const fkey = filterKey(filters);

  // Polling path: only active when enabled && mode === 'polling'. The
  // queryKey includes the filters so source/type changes refetch cleanly.
  const pollingQuery = useQuery({
    queryKey: ["decisions-stream-polling", filters.source ?? "", filters.type ?? ""],
    queryFn: ({ signal }) =>
      listPolicyDecisions(
        {
          source: filters.source,
          type: filters.type,
          limit: POLLING_LIMIT,
        },
        signal,
      ),
    enabled: enabled && mode === "polling",
    refetchInterval: enabled && mode === "polling" ? POLLING_INTERVAL_MS : false,
    staleTime: 0,
  });

  // Refill the ring buffer with each polling snapshot. The server is the
  // source of truth in polling mode; we replace rather than append so a
  // single buffer never grows unbounded across pages.
  useEffect(() => {
    if (mode !== "polling") return;
    const items = pollingQuery.data?.items;
    if (!items) return;
    ringRef.current = items.slice(0, RING_BUFFER_CAP);
    setTick((t) => t + 1);
  }, [mode, pollingQuery.data]);

  useEffect(() => {
    unmountedRef.current = false;

    function cleanup() {
      if (connectTimerRef.current !== null) {
        clearTimeout(connectTimerRef.current);
        connectTimerRef.current = null;
      }
      if (wsRef.current) {
        wsRef.current.close();
        wsRef.current = null;
      }
    }

    if (!enabled) {
      cleanup();
      ringRef.current = [];
      setMode("closed");
      setTick((t) => t + 1);
      return;
    }
    if (!apiKey) {
      // No credential -> degrade silently to polling (REST will still
      // attach the configured auth header via apiClient).
      setMode("polling");
      return;
    }

    // Reset the ring on a fresh attempt so stale frames from a prior
    // filter don't bleed across.
    ringRef.current = [];

    const url = streamUrl(apiBaseUrl, filters);
    const subprotocols = ["cordum-api-key", base64UrlApiKey(apiKey)];

    let ws: WebSocket;
    try {
      ws = new WebSocket(url, subprotocols);
    } catch (err) {
      logger.warn("decisions-stream", "WebSocket constructor failed", { url, err: String(err) });
      setMode("polling");
      return () => {
        unmountedRef.current = true;
      };
    }
    wsRef.current = ws;

    // 3s connect timeout: if onopen has not fired, fall back to polling.
    // We set mode='polling' directly here rather than relying on onclose,
    // because real WebSocket close() fires the close event asynchronously
    // (next tick) while our test mock is synchronous; both ways the user
    // ends up in polling, but the explicit setMode keeps semantics tight.
    connectTimerRef.current = setTimeout(() => {
      if (unmountedRef.current) return;
      if (ws.readyState !== WebSocket.OPEN) {
        logger.info("decisions-stream", "WS connect timed out -> polling");
        ws.close();
        setMode("polling");
      }
    }, WS_CONNECT_TIMEOUT_MS);

    ws.onopen = () => {
      if (unmountedRef.current) {
        ws.close();
        return;
      }
      if (connectTimerRef.current !== null) {
        clearTimeout(connectTimerRef.current);
        connectTimerRef.current = null;
      }
      setMode("ws");
    };

    ws.onmessage = (msg) => {
      let raw: unknown;
      try {
        raw = JSON.parse(msg.data as string) as unknown;
      } catch {
        // Drop non-JSON frame. The Backend 5b broker is JSON-only; this is
        // defensive against transient corruption.
        return;
      }
      if (!isDecision(raw)) return;
      const next = [raw, ...ringRef.current];
      ringRef.current = next.length > RING_BUFFER_CAP ? next.slice(0, RING_BUFFER_CAP) : next;
      setTick((t) => t + 1);
    };

    ws.onerror = () => {
      logger.debug("decisions-stream", "WS error event");
    };

    ws.onclose = () => {
      wsRef.current = null;
      if (connectTimerRef.current !== null) {
        clearTimeout(connectTimerRef.current);
        connectTimerRef.current = null;
      }
      if (unmountedRef.current) return;
      // Whether we lost connection during connect or after onopen, the
      // safe degraded mode is polling. The user can flip Live off to
      // stop trying.
      setMode("polling");
    };

    return () => {
      unmountedRef.current = true;
      cleanup();
    };
    // The filter key collapses {source, type} into a string so we re-init
    // the connection only when one of those actually changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [enabled, apiKey, apiBaseUrl, fkey]);

  return { decisions: ringRef.current, mode };
}

function isDecision(value: unknown): value is Decision {
  if (!value || typeof value !== "object") return false;
  const v = value as Record<string, unknown>;
  return (
    typeof v.rule_id === "string" &&
    typeof v.timestamp === "string" &&
    typeof v.type === "string" &&
    typeof v.source === "string"
  );
}
