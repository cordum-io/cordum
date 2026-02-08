import { useConfigStore } from "../state/config";
import { logger } from "../lib/logger";

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

  if (res.ok) {
    logger.info("api-client", `${res.status} ${meta.path}`, {
      method: meta.method,
      requestId: meta.requestId,
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

  // 401 — clear auth and redirect
  if (res.status === 401) {
    logger.warn("api-client", "Unauthorized", { path: meta.path, requestId: meta.requestId, durationMs });
    useConfigStore.getState().logout();
    if (typeof window !== "undefined" && !window.location.pathname.startsWith("/login")) {
      window.location.href = "/login";
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

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = { ...authHeaders(), ...(init?.headers as Record<string, string> | undefined) };
  const reqId = headers["X-Request-Id"] ?? "unknown";
  const method = init?.method ?? "GET";

  logger.debug("api-client", `${method} ${path}`, { requestId: reqId });

  const startMs = performance.now();
  const res = await fetch(`${baseUrl()}${path}`, {
    ...init,
    headers,
  });
  return handleResponse<T>(res, { method, path, requestId: reqId, startMs });
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

export function get<T>(path: string): Promise<T> {
  return request<T>(path, { method: "GET" });
}

export function post<T>(path: string, body?: unknown): Promise<T> {
  return request<T>(path, {
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
