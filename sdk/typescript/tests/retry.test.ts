import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { RetryExhaustedError } from "../src/errors.js";
import { applyJitter, computeRetryDelay, createRetryFetch, resolveRetryPolicy } from "../src/retry.js";

describe("retry middleware", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("applies capped exponential growth without jitter", async () => {
    const responses = [
      new Response("busy", { status: 503 }),
      new Response("still busy", { status: 503 }),
      new Response("ok", { status: 200 }),
    ];
    const fetchMock = vi.fn(async (..._args: Parameters<typeof fetch>) => responses.shift() ?? new Response("ok", { status: 200 }));
    const timeoutSpy = vi.spyOn(globalThis, "setTimeout");
    const retryingFetch = createRetryFetch(fetchMock, { jitter: false, maxRetries: 2 });

    const promise = retryingFetch("https://cordum.test/jobs");
    await vi.advanceTimersByTimeAsync(500);
    await vi.advanceTimersByTimeAsync(1_000);

    const result = await promise;
    expect(result.status).toBe(200);
    expect(fetchMock).toHaveBeenCalledTimes(3);
    expect(timeoutSpy.mock.calls.map(([, delay]) => delay)).toEqual([500, 1_000]);
  });

  it("honors numeric Retry-After headers", async () => {
    const fetchMock = vi
      .fn(async (..._args: Parameters<typeof fetch>) => new Response("ok", { status: 200 }))
      .mockResolvedValueOnce(new Response("slow down", { status: 429, headers: { "Retry-After": "2" } }))
      .mockResolvedValueOnce(new Response("ok", { status: 200 }));
    const timeoutSpy = vi.spyOn(globalThis, "setTimeout");
    const retryingFetch = createRetryFetch(fetchMock, { jitter: false, maxRetries: 1 });

    const promise = retryingFetch("https://cordum.test/jobs");
    await vi.advanceTimersByTimeAsync(2_000);

    const result = await promise;
    expect(result.status).toBe(200);
    expect(timeoutSpy.mock.calls.map(([, delay]) => delay)).toEqual([2_000]);
  });

  it("honors HTTP-date Retry-After headers", async () => {
    vi.setSystemTime(new Date("2026-04-20T08:00:00.000Z"));
    const fetchMock = vi
      .fn(async (..._args: Parameters<typeof fetch>) => new Response("ok", { status: 200 }))
      .mockResolvedValueOnce(
        new Response("slow down", {
          status: 503,
          headers: { "Retry-After": "Mon, 20 Apr 2026 08:00:02 GMT" },
        }),
      )
      .mockResolvedValueOnce(new Response("ok", { status: 200 }));
    const timeoutSpy = vi.spyOn(globalThis, "setTimeout");
    const retryingFetch = createRetryFetch(fetchMock, { jitter: false, maxRetries: 1 });

    const promise = retryingFetch("https://cordum.test/jobs");
    await vi.advanceTimersByTimeAsync(2_000);

    const result = await promise;
    expect(result.status).toBe(200);
    expect(timeoutSpy.mock.calls.map(([, delay]) => delay)).toEqual([2_000]);
  });

  it("produces jittered delays with crypto-backed randomness", () => {
    const policy = resolveRetryPolicy({ jitter: true, initialBackoffMs: 1_000 });
    const samples = Array.from({ length: 100 }, () => computeRetryDelay(0, policy));

    expect(new Set(samples).size).toBeGreaterThan(1);
    for (const sample of samples) {
      expect(sample).toBeGreaterThanOrEqual(0);
      expect(sample).toBeLessThanOrEqual(1_000);
    }
  });

  it("returns non-retryable 400 responses immediately", async () => {
    const fetchMock = vi.fn(async (..._args: Parameters<typeof fetch>) => new Response("bad request", { status: 400 }));
    const retryingFetch = createRetryFetch(fetchMock, { jitter: false, maxRetries: 3 });

    const response = await retryingFetch("https://cordum.test/jobs");
    expect(response.status).toBe(400);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("does not retry POST without an idempotency key", async () => {
    const fetchMock = vi.fn(async (..._args: Parameters<typeof fetch>) => new Response("busy", { status: 503 }));
    const retryingFetch = createRetryFetch(fetchMock, { jitter: false, maxRetries: 3 });

    const response = await retryingFetch("https://cordum.test/jobs", { method: "POST" });
    expect(response.status).toBe(503);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("retries POST with an idempotency key", async () => {
    const fetchMock = vi
      .fn(async (..._args: Parameters<typeof fetch>) => new Response("ok", { status: 200 }))
      .mockResolvedValueOnce(new Response("busy", { status: 503 }))
      .mockResolvedValueOnce(new Response("ok", { status: 200 }));
    const retryingFetch = createRetryFetch(fetchMock, { jitter: false, maxRetries: 1 });

    const promise = retryingFetch("https://cordum.test/jobs", {
      method: "POST",
      headers: { "Idempotency-Key": "idem-123" },
    });
    await vi.advanceTimersByTimeAsync(500);

    const response = await promise;
    expect(response.status).toBe(200);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("aborts immediately when the caller cancels during backoff", async () => {
    const controller = new AbortController();
    const fetchMock = vi.fn(async (..._args: Parameters<typeof fetch>) => new Response("busy", { status: 503 }));
    const retryingFetch = createRetryFetch(fetchMock, { jitter: false, maxRetries: 3, initialBackoffMs: 5_000 });

    const promise = retryingFetch("https://cordum.test/jobs", { signal: controller.signal });
    controller.abort(new DOMException("Cancelled", "AbortError"));

    const error = await promise.catch((reason: unknown) => reason);
    expect(error).toBeInstanceOf(DOMException);
    expect(error).toMatchObject({ name: "AbortError" });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("wraps the last response after retry exhaustion", async () => {
    const fetchMock = vi.fn(async (..._args: Parameters<typeof fetch>) => new Response("still busy", { status: 503 }));
    const retryingFetch = createRetryFetch(fetchMock, { jitter: false, maxRetries: 1 });

    const promise = retryingFetch("https://cordum.test/jobs");
    const errorPromise = promise.catch((reason: unknown) => reason);
    await vi.advanceTimersByTimeAsync(500);

    const error = await errorPromise;
    expect(error).toBeInstanceOf(RetryExhaustedError);
    expect(error).toMatchObject({
      status: 503,
      lastError: expect.any(Response),
    });
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("retries transient network errors and wraps the final error on exhaustion", async () => {
    const networkError = new TypeError("fetch failed");
    const fetchMock = vi.fn(async (..._args: Parameters<typeof fetch>) => {
      throw networkError;
    });
    const retryingFetch = createRetryFetch(fetchMock, { jitter: false, maxRetries: 1 });

    const promise = retryingFetch("https://cordum.test/jobs");
    const errorPromise = promise.catch((reason: unknown) => reason);
    await vi.advanceTimersByTimeAsync(500);

    const error = await errorPromise;
    expect(error).toBeInstanceOf(RetryExhaustedError);
    expect(error).toMatchObject({ lastError: networkError });
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("applies jitter through the exported helper", () => {
    const samples = Array.from({ length: 50 }, () => applyJitter(500));

    expect(samples.every((value) => value >= 0 && value <= 500)).toBe(true);
    expect(new Set(samples).size).toBeGreaterThan(1);
  });
});
