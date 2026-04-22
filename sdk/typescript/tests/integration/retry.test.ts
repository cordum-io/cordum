import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { setupServer } from "msw/node";
import { HttpResponse, http } from "msw";
import { CordumClient } from "../../src/client.js";
import { RetryExhaustedError, ServerError } from "../../src/errors.js";
import { createRetryFetch } from "../../src/retry.js";
import { createIntegrationHandlers, createIntegrationState, type IntegrationState } from "./handlers.js";

describe("integration retry flows", () => {
  let state: IntegrationState;
  const server = setupServer();

  beforeAll(() => {
    server.listen({ onUnhandledRequest: "error" });
  });

  beforeEach(() => {
    vi.useFakeTimers();
    state = createIntegrationState();
    server.resetHandlers(...createIntegrationHandlers(() => state));
  });

  afterEach(() => {
    vi.useRealTimers();
    server.resetHandlers();
  });

  afterAll(() => {
    server.close();
  });

  it("retries a 429 response and succeeds after Retry-After", async () => {
    let attempts = 0;
    server.use(
      http.get("https://cordum.test/api/v1/jobs", () => {
        attempts += 1;
        if (attempts === 1) {
          return HttpResponse.json({ message: "slow down" }, { status: 429, headers: { "Retry-After": "2" } });
        }
        return HttpResponse.json({ items: [] });
      }),
    );

    const client = new CordumClient({ baseUrl: "https://cordum.test", auth: state.validApiKey, tenantId: "tenant-a" });
    const promise = client.jobs.list();
    await vi.advanceTimersByTimeAsync(2_000);

    await expect(promise).resolves.toEqual({ items: [] });
    expect(attempts).toBe(2);
  });

  it("does not retry POST 503 without an idempotency key", async () => {
    let attempts = 0;
    server.use(
      http.post("https://cordum.test/api/v1/jobs", () => {
        attempts += 1;
        return HttpResponse.json({ message: "busy" }, { status: 503 });
      }),
    );

    const client = new CordumClient({ baseUrl: "https://cordum.test", auth: state.validApiKey, tenantId: "tenant-a" });
    await expect(client.jobs.create({ topic: "job.default" } as never)).rejects.toBeInstanceOf(ServerError);
    expect(attempts).toBe(1);
  });

  it("retries POST 503 when an idempotency key is present", async () => {
    let attempts = 0;
    server.use(
      http.post("https://cordum.test/api/v1/jobs", async ({ request }) => {
        attempts += 1;
        if (attempts === 1) {
          return HttpResponse.json({ message: "busy" }, { status: 503 });
        }
        const body = await request.json();
        return HttpResponse.json({ accepted: true, body });
      }),
    );

    const client = new CordumClient({ baseUrl: "https://cordum.test", auth: state.validApiKey, tenantId: "tenant-a" });
    const promise = client.jobs.create({ topic: "job.default" } as never, { idempotencyKey: "idem-123" });
    await vi.advanceTimersByTimeAsync(500);

    await expect(promise).resolves.toMatchObject({ accepted: true });
    expect(attempts).toBe(2);
  });

  it("preserves the last response when retries exhaust", async () => {
    server.use(
      http.get("https://cordum.test/api/v1/jobs", () => HttpResponse.json({ message: "boom" }, { status: 500 })),
    );

    const client = new CordumClient({ baseUrl: "https://cordum.test", auth: state.validApiKey, tenantId: "tenant-a" });
    const promise = client.jobs.list();
    const errorPromise = promise.catch((reason: unknown) => reason);
    await vi.advanceTimersByTimeAsync(500 + 1_000 + 2_000);
    const error = await errorPromise;

    expect(error).toBeInstanceOf(RetryExhaustedError);
    expect(await ((error as RetryExhaustedError).lastError as Response).json()).toEqual({ message: "boom" });
  });

  it("aborts during backoff when the caller signal is cancelled", async () => {
    server.use(
      http.get("https://cordum.test/api/v1/jobs", () => HttpResponse.json({ message: "busy" }, { status: 503 })),
    );

    const controller = new AbortController();
    const retryingFetch = createRetryFetch(withApiKeyFetch(state.validApiKey), {
      jitter: false,
      initialBackoffMs: 5_000,
    });

    const promise = retryingFetch("https://cordum.test/api/v1/jobs", { signal: controller.signal });
    controller.abort(new DOMException("Cancelled", "AbortError"));

    await expect(promise).rejects.toMatchObject({ name: "AbortError" });
  });
});

function withApiKeyFetch(apiKey: string): typeof fetch {
  return async (input: RequestInfo | URL, init?: RequestInit) => {
    const request = new Request(input, init);
    const headers = new Headers(request.headers);
    headers.set("X-API-Key", apiKey);
    headers.set("X-Tenant-ID", "tenant-a");
    return fetch(new Request(request, { headers }));
  };
}
