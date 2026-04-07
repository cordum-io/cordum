import { toast } from "sonner";
import { useConfigStore } from "../state/config";
import { logger } from "../lib/logger";
import type { TopicsResponse } from "./types";

function baseUrl(): string {
  const { apiBaseUrl } = useConfigStore.getState();
  const raw = (apiBaseUrl || import.meta.env.VITE_API_URL || "/api/v1").trim();
  return raw.endsWith("/") ? raw.slice(0, -1) : raw;
}

// ---------------------------------------------------------------------------
// ApiError
// ---------------------------------------------------------------------------

export class ApiError extends Error {
  constructor(
    public readonly status: number,
    message: string,
    public readonly body?: unknown,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function requestId(): string {
  return crypto.randomUUID();
}

function authHeaders(): Record<string, string> {
  const { apiKey, tenantId, principalId, principalRole, user } =
    useConfigStore.getState();
  const h: Record<string, string> = {
    "Content-Type": "application/json",
    "X-Request-Id": requestId(),
  };
  if (apiKey) {
    h["X-API-Key"] = apiKey;
  }
  if (tenantId) {
    h["X-Tenant-ID"] = tenantId;
  }
  const principal = principalId || user?.id;
  if (principal) {
    h["X-Principal-Id"] = principal;
  }
  if (principalRole) {
    h["X-Principal-Role"] = principalRole;
  }
  return h;
}

async function handleResponse<T>(res: Response, meta: { method: string; path: string; requestId: string; startMs: number }): Promise<T> {
  const durationMs = Math.round(performance.now() - meta.startMs);

  const traceId = res.headers.get("X-Trace-Id") ?? undefined;

  if (res.ok) {
    logger.info("api-client", `${res.status} ${meta.path}`, {
      method: meta.method,
      requestId: meta.requestId,
      ...(traceId ? { traceId } : {}),
      durationMs,
    });
    // 204 No Content
    if (res.status === 204) return undefined as T;
    return res.json() as Promise<T>;
  }

  let body: unknown;
  try {
    body = await res.json();
  } catch {
    logger.debug("api-client", "Non-JSON error body", { path: meta.path, requestId: meta.requestId });
  }

  // 401 — notify user, clear auth, and redirect with delay so unsaved work
  // isn't silently lost. The isLoggingOut guard prevents duplicate handlers
  // when multiple in-flight requests all receive 401 simultaneously.
  if (res.status === 401) {
    const { isLoggingOut, logout } = useConfigStore.getState();
    if (!isLoggingOut) {
      logger.warn("api-client", "Unauthorized", { path: meta.path, requestId: meta.requestId, durationMs });
      toast.error("Session expired — please log in again.");
      logout();
      if (typeof window !== "undefined" && !window.location.pathname.startsWith("/login")) {
        setTimeout(() => {
          window.location.href = "/login";
        }, 1500);
      }
    } else {
      logger.debug("api-client", "Skipping duplicate logout during unauthorized response", {
        path: meta.path,
        requestId: meta.requestId,
        durationMs,
      });
    }
    throw new ApiError(401, "Unauthorized — session expired");
  }

  if (res.status === 403) {
    logger.warn("api-client", "Forbidden", { path: meta.path, requestId: meta.requestId, durationMs });
    throw new ApiError(403, "Forbidden — insufficient permissions", body);
  }

  if (res.status === 429) {
    logger.warn("api-client", "Rate limited", { path: meta.path, requestId: meta.requestId, durationMs });
    throw new ApiError(429, "Rate limit exceeded — please slow down", body);
  }

  const msg =
    (body && typeof body === "object" && ("error" in body || "message" in body)
      ? String((body as Record<string, unknown>).error ?? (body as Record<string, unknown>).message)
      : null) ?? res.statusText;

  logger.error("api-client", `${res.status} ${meta.path}`, {
    method: meta.method,
    requestId: meta.requestId,
    durationMs,
    error: msg,
  });

  throw new ApiError(res.status, msg, body);
}

const REQUEST_TIMEOUT_MS = 30_000;

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = { ...authHeaders(), ...(init?.headers as Record<string, string> | undefined) };
  const reqId = headers["X-Request-Id"] ?? "unknown";
  const method = init?.method ?? "GET";

  logger.debug("api-client", `${method} ${path}`, { requestId: reqId });

  const timeoutSignal = AbortSignal.timeout(REQUEST_TIMEOUT_MS);
  const signal = init?.signal
    ? AbortSignal.any([init.signal, timeoutSignal])
    : timeoutSignal;

  const startMs = performance.now();
  const res = await fetch(`${baseUrl()}${path}`, {
    ...init,
    headers,
    signal,
  });
  return handleResponse<T>(res, { method, path, requestId: reqId, startMs });
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

export function get<T>(path: string): Promise<T> {
  return request<T>(path, { method: "GET" });
}

type BackendTopicRegistration = {
  name: string;
  pool: string;
  input_schema_id?: string;
  output_schema_id?: string;
  pack_id?: string;
  requires?: string[];
  risk_tags?: string[];
  status?: string;
  active_worker_count?: number;
};

export async function fetchTopics(): Promise<TopicsResponse> {
  const res = await get<{
    items?: BackendTopicRegistration[];
    registry_empty?: boolean;
  }>("/topics");

  return {
    items: (res.items ?? []).map((topic) => ({
      name: topic.name,
      pool: topic.pool,
      inputSchemaId: topic.input_schema_id,
      outputSchemaId: topic.output_schema_id,
      packId: topic.pack_id,
      requires: topic.requires ?? [],
      riskTags: topic.risk_tags ?? [],
      status: topic.status ?? "active",
      activeWorkers: topic.active_worker_count ?? 0,
    })),
    registryEmpty: res.registry_empty ?? false,
  };
}

export function post<T>(path: string, body?: unknown, init?: RequestInit): Promise<T> {
  return request<T>(path, {
    ...init,
    method: "POST",
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
}

export function put<T>(path: string, body?: unknown): Promise<T> {
  return request<T>(path, {
    method: "PUT",
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
}

export function patch<T>(path: string, body?: unknown): Promise<T> {
  return request<T>(path, {
    method: "PATCH",
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
}

export function del<T = void>(path: string): Promise<T> {
  return request<T>(path, { method: "DELETE" });
}
