import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from "vitest";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import { AuthenticationError, NotFoundError } from "../src/errors.js";
import { CordumClient } from "../src/client.js";

const server = setupServer();

beforeAll(() => {
  server.listen({ onUnhandledRequest: "error" });
});

afterEach(() => {
  server.resetHandlers();
});

afterAll(() => {
  server.close();
});

describe("CordumClient", () => {
  let observedHeaders: Headers[] = [];

  beforeEach(() => {
    observedHeaders = [];
  });

  it("accepts string auth shorthand and dispatches jobs/workflows/policies namespaces", async () => {
    server.use(
      http.get("https://cordum.test/api/v1/jobs", ({ request }) => {
        observedHeaders.push(new Headers(request.headers));
        return HttpResponse.json({ items: [{ id: "job-1" }] });
      }),
      http.get("https://cordum.test/api/v1/jobs/:id", ({ params, request }) => {
        observedHeaders.push(new Headers(request.headers));
        return HttpResponse.json({ id: params.id, state: "running" });
      }),
      http.post("https://cordum.test/api/v1/jobs", async ({ request }) => {
        observedHeaders.push(new Headers(request.headers));
        const body = await request.json();
        return HttpResponse.json({ accepted: true, body });
      }),
      http.get("https://cordum.test/api/v1/workflows", () => HttpResponse.json([{ id: "wf-1" }])),
      http.get("https://cordum.test/api/v1/policy/bundles/:id", ({ params }) => HttpResponse.json({ id: params.id })),
    );

    const client = new CordumClient({
      baseUrl: "https://cordum.test",
      auth: "sdk-key",
      tenantId: "tenant-a",
    });

    const jobs = await client.jobs.list();
    const job = await client.jobs.get("job-1");
    const created = await client.jobs.create({ topic: "job.default", prompt: "run this" } as never, {
      idempotencyKey: "idem-123",
    });
    const workflows = await client.workflows.list();
    const bundle = await client.policies.getBundle("bundle-1");

    expect(jobs).toEqual({ items: [{ id: "job-1" }] });
    expect(job).toEqual({ id: "job-1", state: "running" });
    expect(created).toEqual({ accepted: true, body: { topic: "job.default", prompt: "run this" } });
    expect(workflows).toEqual([{ id: "wf-1" }]);
    expect(bundle).toEqual({ id: "bundle-1" });
    expect(observedHeaders[0]?.get("X-API-Key")).toBe("sdk-key");
    expect(observedHeaders[0]?.get("X-Cordum-Tenant")).toBe("tenant-a");
    expect(observedHeaders[0]?.get("X-Tenant-ID")).toBe("tenant-a");
  });

  it("emits tenant and user-agent headers", async () => {
    server.use(
      http.get("https://cordum.test/api/v1/jobs", ({ request }) => {
        observedHeaders.push(new Headers(request.headers));
        return HttpResponse.json({ items: [] });
      }),
    );

    const client = new CordumClient({
      baseUrl: "https://cordum.test",
      auth: "sdk-key",
      tenantId: "tenant-b",
      userAgent: "integration-test",
    });

    await client.jobs.list();

    expect(observedHeaders[0]?.get("User-Agent")).toContain("@cordum/sdk/0.1.0");
    expect(observedHeaders[0]?.get("User-Agent")).toContain("integration-test");
    expect(observedHeaders[0]?.get("X-Cordum-Tenant")).toBe("tenant-b");
  });

  it("composes bearer auth headers from custom providers", async () => {
    server.use(
      http.get("https://cordum.test/api/v1/jobs", ({ request }) => {
        observedHeaders.push(new Headers(request.headers));
        return HttpResponse.json({ items: [] });
      }),
    );

    const client = new CordumClient({
      baseUrl: "https://cordum.test",
      auth: {
        async applyHeaders(headers: Headers) {
          headers.set("Authorization", "Bearer custom-token");
        },
      },
    });

    await client.jobs.list();
    expect(observedHeaders[0]?.get("Authorization")).toBe("Bearer custom-token");
  });

  it("maps 404 responses to NotFoundError", async () => {
    server.use(
      http.get("https://cordum.test/api/v1/jobs/:id", () => HttpResponse.json({ message: "missing" }, { status: 404 })),
    );

    const client = new CordumClient({
      baseUrl: "https://cordum.test",
      auth: "sdk-key",
    });

    await expect(client.jobs.get("missing")).rejects.toBeInstanceOf(NotFoundError);
  });

  it("maps 401 responses to AuthenticationError", async () => {
    server.use(
      http.get("https://cordum.test/api/v1/jobs", () => HttpResponse.json({ message: "unauthorized" }, { status: 401 })),
    );

    const client = new CordumClient({
      baseUrl: "https://cordum.test",
      auth: "sdk-key",
    });

    await expect(client.jobs.list()).rejects.toBeInstanceOf(AuthenticationError);
  });

  it("aborts in-flight requests when close() is called", async () => {
    const client = new CordumClient({
      baseUrl: "https://cordum.test",
      auth: "sdk-key",
      fetch: (input, init) =>
        new Promise<Response>((_resolve, reject) => {
          const signal = input instanceof Request ? input.signal : init?.signal;
          if (signal?.aborted) {
            reject(signal.reason ?? new DOMException("The operation was aborted.", "AbortError"));
            return;
          }

          signal?.addEventListener(
            "abort",
            () => {
              reject(signal.reason ?? new DOMException("The operation was aborted.", "AbortError"));
            },
            { once: true },
          );
        }),
    });

    const request = client.jobs.list();
    await Promise.resolve();
    client.close();

    await expect(request).rejects.toMatchObject({ name: "AbortError" });
  });
});
