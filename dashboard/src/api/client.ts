import { toast } from "sonner";
import { useConfigStore } from "../state/config";
import { generateUUID } from "../lib/uuid";
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
  return generateUUID();
}

function authHeaders(): Record<string, string> {
  const { apiKey, authMode, tenantId, principalId, principalRole, user } =
    useConfigStore.getState();
  const h: Record<string, string> = {
    "Content-Type": "application/json",
    "X-Request-Id": requestId(),
  };
  // X-API-Key is for long-lived API keys only. Session tokens belong in the
  // httpOnly `cordum_session` cookie which the browser sends automatically
  // (via `credentials: "include"` below). Sending a session token here is
  // rejected by the gateway and triggers an immediate logout loop — gate
  // the header on authMode to make the contract explicit.
  if (authMode === "apikey" && apiKey) {
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

type ApiResponseType = "json" | "text" | "blob" | "arraybuffer" | string;

interface RequestOptions extends RequestInit {
  responseType?: ApiResponseType;
}

async function parseSuccessfulResponse<T>(
  res: Response,
  responseType?: ApiResponseType,
): Promise<T> {
  const normalizedResponseType = responseType?.toLowerCase();
  const contentType = res.headers.get("Content-Type")?.toLowerCase() ?? "";

  if (normalizedResponseType === "blob") {
    return res.blob() as Promise<T>;
  }

  if (normalizedResponseType === "arraybuffer") {
    return res.arrayBuffer() as Promise<T>;
  }

  if (
    normalizedResponseType === "text" ||
    contentType.includes("text/") ||
    contentType.includes("csv") ||
    contentType.includes("ndjson") ||
    contentType.includes("jsonl")
  ) {
    return res.text() as Promise<T>;
  }

  return res.json() as Promise<T>;
}

async function handleResponse<T>(
  res: Response,
  meta: {
    method: string;
    path: string;
    requestId: string;
    startMs: number;
    responseType?: ApiResponseType;
  },
): Promise<T> {
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
    return parseSuccessfulResponse<T>(res, meta.responseType);
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

async function request<T>(path: string, init?: RequestOptions): Promise<T> {
  const { responseType, ...fetchInit } = init ?? {};
  const headers = { ...authHeaders(), ...(fetchInit.headers as Record<string, string> | undefined) };
  // FormData / Blob / URLSearchParams bodies must let the runtime set the
  // correct Content-Type (multipart boundary, url-encoded marker, etc.).
  // authHeaders() injects application/json by default, which breaks these.
  const reqBody = fetchInit.body;
  if (
    reqBody instanceof FormData ||
    reqBody instanceof Blob ||
    reqBody instanceof URLSearchParams
  ) {
    delete headers["Content-Type"];
  }
  const reqId = headers["X-Request-Id"] ?? "unknown";
  const method = fetchInit.method ?? "GET";

  logger.debug("api-client", `${method} ${path}`, { requestId: reqId });

  const timeoutSignal = AbortSignal.timeout(REQUEST_TIMEOUT_MS);
  const signal = fetchInit.signal
    ? AbortSignal.any([fetchInit.signal, timeoutSignal])
    : timeoutSignal;

  const startMs = performance.now();
  try {
    const res = await fetch(`${baseUrl()}${path}`, {
      ...fetchInit,
      headers,
      signal,
      credentials: "include",
    });
    return await handleResponse<T>(res, { method, path, requestId: reqId, startMs, responseType });
  } catch (err) {
    if (err instanceof ApiError) throw err;
    // Distinguish our internal timeout from caller cancellation by reading
    // the local timeoutSignal — runtime-proof across jsdom/Node/browser.
    if (timeoutSignal.aborted) {
      logger.warn("api-client", `timeout ${path}`, {
        requestId: reqId,
        timeoutMs: REQUEST_TIMEOUT_MS,
      });
      throw new ApiError(408, `Request timed out after ${REQUEST_TIMEOUT_MS / 1000} seconds`);
    }
    if (err instanceof DOMException && err.name === "AbortError") throw err;
    if (err instanceof TypeError) {
      logger.warn("api-client", `network ${path}`, {
        requestId: reqId,
        error: err.message,
      });
      throw new ApiError(0, "Network error — please check your connection");
    }
    throw err;
  }
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

// ---------------------------------------------------------------------------
// orval mutator adapter
//
// orval-generated react-query hooks call this with a normalized config object
// (`{ url, method, params, data, headers, signal }`). Routing the call through
// `request<T>` keeps auth headers, tenant routing, 30s timeout, structured
// logging, 401-redirect, and ApiError normalization centralized.
// ---------------------------------------------------------------------------

export interface ApiClientConfig {
  url: string;
  method: string;
  params?: Record<string, unknown>;
  data?: unknown;
  headers?: Record<string, string>;
  responseType?: string;
  signal?: AbortSignal;
}

function buildQueryString(params: Record<string, unknown>): string {
  const usp = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null) continue;
    if (Array.isArray(value)) {
      for (const item of value) {
        if (item === undefined || item === null) continue;
        usp.append(key, String(item));
      }
    } else {
      usp.append(key, String(value));
    }
  }
  const qs = usp.toString();
  return qs ? `?${qs}` : "";
}

function serializeBody(data: unknown): BodyInit | undefined {
  if (data === undefined || data === null) return undefined;
  if (
    data instanceof FormData ||
    data instanceof Blob ||
    data instanceof URLSearchParams ||
    data instanceof ArrayBuffer ||
    typeof data === "string"
  ) {
    return data as BodyInit;
  }
  return JSON.stringify(data);
}

export function apiClient<T>(config: ApiClientConfig): Promise<T> {
  const queryString = config.params ? buildQueryString(config.params) : "";
  // orval emits absolute URLs that already start with `/api/v1` (the spec's
  // server prefix). `request()` re-prepends `baseUrl()` which is also
  // `/api/v1`, so a naive concat fetches `/api/v1/api/v1/...` and 404s.
  // Strip the leading prefix before handing the path to request().
  const normalizedUrl = config.url.replace(/^\/api\/v1/, "");
  const path = `${normalizedUrl}${queryString}`;
  return request<T>(path, {
    method: config.method.toUpperCase(),
    body: serializeBody(config.data),
    headers: config.headers,
    responseType: config.responseType,
    signal: config.signal,
  });
}
