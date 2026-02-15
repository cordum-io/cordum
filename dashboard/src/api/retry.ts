import { ApiError } from "./client";

/** Status codes that should never be retried — the request will fail the same way. */
const NON_RETRIABLE_STATUSES = [400, 401, 403, 404, 409, 410];

/** Maximum number of retries for transient errors (5xx, network, 429). */
const MAX_RETRIES = 2;

/** Maximum backoff delay in milliseconds. */
const MAX_BACKOFF_MS = 4000;

/** Base delay in milliseconds for exponential backoff. */
const BASE_DELAY_MS = 1000;

/** Maximum jitter in milliseconds to prevent thundering herd. */
const JITTER_MS = 500;

/**
 * Global retry function for React Query.
 *
 * - Never retries 400/401/403/404/409/410 (client errors that won't resolve)
 * - Retries 5xx, network errors, and 429 up to {@link MAX_RETRIES} times
 */
export function shouldRetry(failureCount: number, error: Error): boolean {
  if (
    error instanceof ApiError &&
    NON_RETRIABLE_STATUSES.includes(error.status)
  ) {
    return false;
  }
  return failureCount < MAX_RETRIES;
}

/**
 * Exponential backoff with jitter for React Query retryDelay.
 *
 * Delay = min(BASE * 2^attempt, MAX) + random(0, JITTER)
 */
export function retryDelay(attemptIndex: number): number {
  const base = Math.min(BASE_DELAY_MS * 2 ** attemptIndex, MAX_BACKOFF_MS);
  return base + Math.random() * JITTER_MS;
}
