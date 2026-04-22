import { RetryExhaustedError, parseRetryAfterHeader } from "./errors.js";

export interface RetryPolicy {
  maxRetries?: number | undefined;
  initialBackoffMs?: number | undefined;
  maxBackoffMs?: number | undefined;
  backoffMultiplier?: number | undefined;
  jitter?: boolean | undefined;
  retryableStatuses?: ReadonlySet<number> | undefined;
  retryableMethods?: ReadonlySet<string> | undefined;
}

export interface ResolvedRetryPolicy {
  readonly maxRetries: number;
  readonly initialBackoffMs: number;
  readonly maxBackoffMs: number;
  readonly backoffMultiplier: number;
  readonly jitter: boolean;
  readonly retryableStatuses: ReadonlySet<number>;
  readonly retryableMethods: ReadonlySet<string>;
}

const DEFAULT_RETRYABLE_STATUSES = new Set([408, 425, 429, 500, 502, 503, 504]);
const DEFAULT_RETRYABLE_METHODS = new Set(["GET", "HEAD", "PUT", "DELETE", "OPTIONS"]);

export const DEFAULT_RETRY_POLICY: ResolvedRetryPolicy = Object.freeze({
  maxRetries: 3,
  initialBackoffMs: 500,
  maxBackoffMs: 30_000,
  backoffMultiplier: 2,
  jitter: true,
  retryableStatuses: DEFAULT_RETRYABLE_STATUSES,
  retryableMethods: DEFAULT_RETRYABLE_METHODS,
});

export function resolveRetryPolicy(policy: RetryPolicy = {}): ResolvedRetryPolicy {
  return {
    maxRetries: Math.max(0, policy.maxRetries ?? DEFAULT_RETRY_POLICY.maxRetries),
    initialBackoffMs: Math.max(0, policy.initialBackoffMs ?? DEFAULT_RETRY_POLICY.initialBackoffMs),
    maxBackoffMs: Math.max(0, policy.maxBackoffMs ?? DEFAULT_RETRY_POLICY.maxBackoffMs),
    backoffMultiplier: Math.max(1, policy.backoffMultiplier ?? DEFAULT_RETRY_POLICY.backoffMultiplier),
    jitter: policy.jitter ?? DEFAULT_RETRY_POLICY.jitter,
    retryableStatuses: policy.retryableStatuses ?? DEFAULT_RETRY_POLICY.retryableStatuses,
    retryableMethods: policy.retryableMethods ?? DEFAULT_RETRY_POLICY.retryableMethods,
  };
}

export function applyJitter(delayMs: number): number {
  if (delayMs <= 0) {
    return 0;
  }

  return Math.round(getCryptoRandomUnitInterval() * delayMs);
}

export function computeRetryDelay(
  retryAttempt: number,
  policy: ResolvedRetryPolicy,
  retryAfterMs?: number,
): number {
  if (retryAfterMs !== undefined) {
    return Math.min(policy.maxBackoffMs, Math.max(0, retryAfterMs));
  }

  const rawDelay = policy.initialBackoffMs * policy.backoffMultiplier ** retryAttempt;
  const boundedDelay = Math.min(policy.maxBackoffMs, Math.max(0, Math.round(rawDelay)));
  return policy.jitter ? applyJitter(boundedDelay) : boundedDelay;
}

export function createRetryFetch(innerFetch: typeof fetch, policy: RetryPolicy = {}): typeof fetch {
  const resolvedPolicy = resolveRetryPolicy(policy);

  return async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    const signal = request.signal;
    let retryAttempt = 0;

    while (true) {
      throwIfAborted(signal);

      let response: Response;
      try {
        response = await innerFetch(request.clone());
      } catch (error) {
        if (isAbortError(error) || signal?.aborted) {
          throw normalizeAbortError(signal, error);
        }

        if (!isRetryAllowed(request, resolvedPolicy)) {
          throw error;
        }

        if (retryAttempt >= resolvedPolicy.maxRetries) {
          throw new RetryExhaustedError(createRetryExhaustedMessage(request, error, retryAttempt), {
            lastError: error,
          });
        }

        const delayMs = computeRetryDelay(retryAttempt, resolvedPolicy);
        retryAttempt += 1;
        await sleep(delayMs, signal);
        continue;
      }

      if (!shouldRetryResponse(request, response, resolvedPolicy)) {
        return response;
      }

      if (retryAttempt >= resolvedPolicy.maxRetries) {
        throw new RetryExhaustedError(createRetryExhaustedMessage(request, response, retryAttempt), {
          lastError: response,
          requestId: response.headers.get("X-Request-Id") ?? undefined,
          status: response.status,
        });
      }

      const delayMs = computeRetryDelay(
        retryAttempt,
        resolvedPolicy,
        parseRetryAfterHeader(response.headers.get("Retry-After")),
      );
      retryAttempt += 1;
      await sleep(delayMs, signal);
    }
  };
}

function shouldRetryResponse(request: Request, response: Response, policy: ResolvedRetryPolicy): boolean {
  return isRetryAllowed(request, policy) && policy.retryableStatuses.has(response.status);
}

function isRetryAllowed(request: Request, policy: ResolvedRetryPolicy): boolean {
  const method = request.method.toUpperCase();
  if (method === "POST") {
    return hasIdempotencyKey(request.headers);
  }
  return policy.retryableMethods.has(method);
}

function hasIdempotencyKey(headers: Headers): boolean {
  const idempotencyKey = headers.get("Idempotency-Key");
  return idempotencyKey !== null && idempotencyKey.trim().length > 0;
}

function getCryptoRandomUnitInterval(): number {
  const cryptoApi = globalThis.crypto;
  if (!cryptoApi?.getRandomValues) {
    throw new Error("crypto.getRandomValues is required for retry jitter");
  }

  const randomBuffer = new Uint32Array(1);
  cryptoApi.getRandomValues(randomBuffer);
  return randomBuffer[0]! / 0x1_0000_0000;
}

function createRetryExhaustedMessage(request: Request, lastFailure: unknown, retryAttempt: number): string {
  const prefix = `Retry budget exhausted for ${request.method.toUpperCase()} ${request.url} after ${retryAttempt} retr${retryAttempt === 1 ? "y" : "ies"}`;
  if (lastFailure instanceof Response) {
    return `${prefix} (last status ${lastFailure.status})`;
  }
  if (lastFailure instanceof Error && lastFailure.message) {
    return `${prefix}: ${lastFailure.message}`;
  }
  return prefix;
}

function isAbortError(error: unknown): boolean {
  return (
    (error instanceof DOMException && error.name === "AbortError") ||
    (typeof error === "object" &&
      error !== null &&
      "name" in error &&
      typeof (error as { name?: unknown }).name === "string" &&
      (error as { name: string }).name === "AbortError")
  );
}

function normalizeAbortError(signal: AbortSignal | null | undefined, fallback: unknown): unknown {
  if (signal?.reason !== undefined) {
    return signal.reason;
  }
  if (fallback !== undefined) {
    return fallback;
  }
  return new DOMException("The operation was aborted.", "AbortError");
}

function throwIfAborted(signal: AbortSignal | null | undefined): void {
  if (signal?.aborted) {
    throw normalizeAbortError(signal, undefined);
  }
}

function sleep(delayMs: number, signal: AbortSignal | null | undefined): Promise<void> {
  if (delayMs <= 0) {
    throwIfAborted(signal);
    return Promise.resolve();
  }

  if (signal?.aborted) {
    return Promise.reject(normalizeAbortError(signal, undefined));
  }

  return new Promise<void>((resolve, reject) => {
    const timer = setTimeout(() => {
      cleanup();
      resolve();
    }, delayMs);

    const onAbort = () => {
      clearTimeout(timer);
      cleanup();
      reject(normalizeAbortError(signal, undefined));
    };

    const cleanup = () => {
      signal?.removeEventListener("abort", onAbort);
    };

    signal?.addEventListener("abort", onAbort, { once: true });
  });
}
