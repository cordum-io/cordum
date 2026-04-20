import { useCallback, useEffect, useRef, useState } from "react";
import { useConfigStore } from "../state/config";
import { logger } from "../lib/logger";
import type { JobProgressEvent } from "../api/types";

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const MIN_BACKOFF_MS = 1_000;
const MAX_BACKOFF_MS = 30_000;
const BACKOFF_FACTOR = 2;
const MAX_EVENTS = 500;

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface UseJobStreamReturn {
  progressEvents: JobProgressEvent[];
  latestPercent: number | null;
  isConnected: boolean;
  lastHeartbeat: string | null;
}

// ---------------------------------------------------------------------------
// URL helper (local — mirrors useEventStream pattern but targets per-job endpoint)
// ---------------------------------------------------------------------------

function jobStreamUrl(apiBaseUrl: string, jobId: string): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  const base = (apiBaseUrl || "/api/v1").trim();
  const trimmed = base.endsWith("/") ? base.slice(0, -1) : base;
  if (/^https?:\/\//i.test(trimmed)) {
    return `${trimmed.replace(/^http/i, "ws")}/stream`;
  }
  return `${proto}//${window.location.host}${trimmed}/stream`;
}

// ---------------------------------------------------------------------------
// Hook
// ---------------------------------------------------------------------------

/**
 * Per-job WebSocket stream for live progress updates.
 * Connects when `enabled` is true (job is running), auto-disconnects on terminal state.
 * Reconnects with exponential backoff on unexpected disconnects.
 */
export function useJobStream(jobId: string, enabled: boolean): UseJobStreamReturn {
  const eventsRef = useRef<JobProgressEvent[]>([]);
  const [latestPercent, setLatestPercent] = useState<number | null>(null);
  const [isConnected, setIsConnected] = useState(false);
  const [lastHeartbeat, setLastHeartbeat] = useState<string | null>(null);
  // Trigger re-render when events change (bumped on each batch)
  const [eventVersion, setEventVersion] = useState(0);

  const wsRef = useRef<WebSocket | null>(null);
  const backoffRef = useRef(MIN_BACKOFF_MS);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const unmountedRef = useRef(false);

  const apiBaseUrl = useConfigStore((s) => s.apiBaseUrl);
  const apiKey = useConfigStore((s) => s.apiKey);

  const cleanup = useCallback(() => {
    if (timerRef.current !== null) {
      clearTimeout(timerRef.current);
      timerRef.current = null;
    }
    if (wsRef.current) {
      wsRef.current.close();
      wsRef.current = null;
    }
    setIsConnected(false);
  }, []);

  useEffect(() => {
    unmountedRef.current = false;

    if (!enabled || !jobId) {
      cleanup();
      return;
    }

    function connect() {
      if (unmountedRef.current || !enabled) return;

      const url = jobStreamUrl(apiBaseUrl, jobId);

      // Auth via subprotocol (base64url without padding)
      const protocols: string[] = [];
      if (apiKey) {
        const encoded = btoa(apiKey).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
        protocols.push(`cordum-api-key.${encoded}`);
      }

      try {
        const ws = new WebSocket(url, protocols.length > 0 ? protocols : undefined);
        wsRef.current = ws;

        ws.onopen = () => {
          if (unmountedRef.current) {
            ws.close();
            return;
          }
          backoffRef.current = MIN_BACKOFF_MS;
          setIsConnected(true);
          logger.debug("job-stream", "WebSocket connected", { jobId });
          // Subscribe to job-specific events
          ws.send(JSON.stringify({ type: "subscribe", jobId }));
        };

        ws.onmessage = (event) => {
          try {
            const data = JSON.parse(String(event.data)) as Record<string, unknown>;
            const type = data.type as string;

            if (type === "heartbeat") {
              setLastHeartbeat(new Date().toISOString());
              return;
            }

            if (type === "jobProgress" && data.jobId === jobId) {
              const percent = typeof data.percent === "number" ? data.percent : null;
              const message = typeof data.message === "string" ? data.message : "";
              const status = typeof data.status === "string" ? data.status : undefined;

              if (percent !== null) setLatestPercent(percent);
              setLastHeartbeat(new Date().toISOString());

              const newEvent: JobProgressEvent = {
                timestamp: new Date().toISOString(),
                percent,
                message,
                status,
              };

              // Cap at MAX_EVENTS to prevent memory growth on long-running jobs
              const events = eventsRef.current;
              if (events.length >= MAX_EVENTS) {
                events.splice(0, events.length - MAX_EVENTS + 1);
              }
              events.push(newEvent);
              setEventVersion((v) => v + 1);
            }
          } catch {
            logger.debug("job-stream", "Failed to parse WS message");
          }
        };

        ws.onclose = () => {
          setIsConnected(false);
          logger.debug("job-stream", "WebSocket closed", { jobId });

          // Reconnect with exponential backoff unless disabled or unmounted
          if (!unmountedRef.current && enabled) {
            const delay = backoffRef.current;
            backoffRef.current = Math.min(delay * BACKOFF_FACTOR, MAX_BACKOFF_MS);
            logger.debug("job-stream", "Reconnecting", { backoffMs: delay, jobId });
            timerRef.current = setTimeout(connect, delay);
          }
        };

        ws.onerror = () => {
          logger.warn("job-stream", "WebSocket error", { jobId });
        };
      } catch (err) {
        logger.error("job-stream", "Failed to create WebSocket", {
          error: err instanceof Error ? err.message : "unknown",
        });
      }
    }

    connect();

    return () => {
      unmountedRef.current = true;
      cleanup();
    };
  }, [enabled, jobId, apiBaseUrl, apiKey, cleanup]);

  // Suppress unused-var warning for eventVersion — it triggers re-renders
  void eventVersion;

  return { progressEvents: eventsRef.current, latestPercent, isConnected, lastHeartbeat };
}
