import type { AuthProvider } from "./auth.js";
import { TimeoutError } from "./errors.js";

type FetchLike = typeof fetch;

export function withTenantHeader(
  innerFetch: FetchLike,
  options: { tenantId?: string | undefined; userAgent?: string | undefined } = {},
): FetchLike {
  const { tenantId, userAgent } = options;

  return async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    const headers = new Headers(request.headers);

    if (tenantId) {
      if (!headers.has("X-Cordum-Tenant")) {
        headers.set("X-Cordum-Tenant", tenantId);
      }
      if (!headers.has("X-Tenant-ID")) {
        headers.set("X-Tenant-ID", tenantId);
      }
    }

    if (userAgent && !headers.has("User-Agent")) {
      headers.set("User-Agent", userAgent);
    }

    return innerFetch(rebuildRequest(request, { headers }));
  };
}

export function withTimeout(
  innerFetch: FetchLike,
  options: { timeoutMs: number; rootSignal?: AbortSignal | undefined; logger?: Pick<Console, "debug" | "warn" | "error"> | undefined },
): FetchLike {
  const { timeoutMs, rootSignal, logger } = options;

  return async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    const timeoutController = new AbortController();
    const signal = mergeSignals(request.signal, rootSignal, timeoutController.signal);
    const timeoutError = new TimeoutError(`Request timed out after ${timeoutMs}ms`);
    const timer = timeoutMs > 0
      ? setTimeout(() => {
          logger?.warn?.(`Request timed out after ${timeoutMs}ms`);
          timeoutController.abort(timeoutError);
        }, timeoutMs)
      : undefined;

    try {
      const compatibleSignal = signal && isRequestSignalCompatible(signal) ? signal : undefined;
      return await innerFetch(rebuildRequest(request, compatibleSignal ? { signal: compatibleSignal } : {}));
    } catch (error) {
      if (rootSignal?.aborted) {
        throw rootSignal.reason ?? error;
      }
      if (request.signal.aborted) {
        throw request.signal.reason ?? error;
      }
      if (timeoutController.signal.aborted && !request.signal.aborted && !rootSignal?.aborted) {
        throw timeoutError;
      }
      throw error;
    } finally {
      if (timer !== undefined) {
        clearTimeout(timer);
      }
    }
  };
}

export function withAuth(innerFetch: FetchLike, auth: AuthProvider): FetchLike {
  return async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    const headers = new Headers(request.headers);
    const nextInit: RequestInit = {
      cache: request.cache,
      credentials: request.credentials,
      headers,
      integrity: request.integrity,
      keepalive: request.keepalive,
      method: request.method,
      mode: request.mode,
      redirect: request.redirect,
      referrer: request.referrer,
      referrerPolicy: request.referrerPolicy,
      signal: request.signal,
      ...(request.body !== null ? { body: request.body } : {}),
    };

    await auth.applyHeaders(headers, nextInit);
    return innerFetch(rebuildRequest(request, { headers }));
  };
}

function mergeSignals(...signals: Array<AbortSignal | undefined>): AbortSignal | undefined {
  const activeSignals = signals.filter((signal): signal is AbortSignal => signal !== undefined);
  if (activeSignals.length === 0) {
    return undefined;
  }
  if (activeSignals.length === 1) {
    return activeSignals[0];
  }

  const controller = new AbortController();
  const listeners: Array<{ signal: AbortSignal; listener: () => void }> = [];
  const cleanup = () => {
    for (const entry of listeners) {
      entry.signal.removeEventListener("abort", entry.listener);
    }
  };

  const onAbort = (signal: AbortSignal) => {
    cleanup();
    controller.abort(signal.reason);
  };

  for (const signal of activeSignals) {
    if (signal.aborted) {
      onAbort(signal);
      return controller.signal;
    }

    const listener = () => onAbort(signal);
    listeners.push({ signal, listener });
    signal.addEventListener("abort", listener, { once: true });
  }

  return controller.signal;
}

function rebuildRequest(request: Request, overrides: RequestInit): Request {
  const compatibleSignal = isRequestSignalCompatible(request.signal) ? request.signal : undefined;
  const requestInit: RequestInit & { duplex?: "half" } = {
    cache: request.cache,
    credentials: request.credentials,
    headers: request.headers,
    integrity: request.integrity,
    keepalive: request.keepalive,
    method: request.method,
    mode: request.mode,
    redirect: request.redirect,
    referrer: request.referrer,
    referrerPolicy: request.referrerPolicy,
    ...(compatibleSignal ? { signal: compatibleSignal } : {}),
    ...(request.body !== null ? { body: request.body } : {}),
    ...overrides,
  };

  if (requestInit.body !== undefined && request.method !== "GET" && request.method !== "HEAD") {
    requestInit.duplex = "half";
  }

  return new Request(request.url, requestInit);
}

function isRequestSignalCompatible(signal: AbortSignal): boolean {
  try {
    void new Request("https://cordum.invalid", { signal });
    return true;
  } catch {
    return false;
  }
}
