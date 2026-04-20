import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from "vitest";
import { setupServer } from "msw/node";
import { CordumClient } from "../../src/client.js";
import { SessionAuth } from "../../src/auth.js";
import { AuthenticationError } from "../../src/errors.js";
import { createIntegrationHandlers, createIntegrationState, type IntegrationState } from "./handlers.js";

describe("integration auth flows", () => {
  let state: IntegrationState;
  const server = setupServer();

  beforeAll(() => {
    server.listen({ onUnhandledRequest: "error" });
  });

  beforeEach(() => {
    state = createIntegrationState();
    server.resetHandlers(...createIntegrationHandlers(() => state));
  });

  afterEach(() => {
    server.resetHandlers();
  });

  afterAll(() => {
    server.close();
  });

  it("supports API-key success and rejects invalid API keys", async () => {
    const goodClient = new CordumClient({
      baseUrl: "https://cordum.test",
      auth: state.validApiKey,
      tenantId: "tenant-a",
    });
    const badClient = new CordumClient({
      baseUrl: "https://cordum.test",
      auth: "wrong-key",
      tenantId: "tenant-a",
    });

    await expect(goodClient.jobs.list()).resolves.toMatchObject({ items: expect.any(Array) });
    await expect(badClient.jobs.list()).rejects.toBeInstanceOf(AuthenticationError);
  });

  it("logs in once, caches the session token, refreshes exactly once on 401, and never re-sends the password outside login", async () => {
    const observedRequests: Array<{ url: string; bodyText?: string }> = [];
    const recordingFetch: typeof fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
      const request = new Request(input, init);
      const bodyText = request.body ? await request.clone().text() : undefined;
      observedRequests.push({
        url: request.url,
        ...(bodyText !== undefined ? { bodyText } : {}),
      });
      return fetch(request);
    };

    const sessionAuth = new SessionAuth({
      baseUrl: "https://cordum.test",
      email: "operator@cordum.test",
      password: "super-secret-password",
      fetch: recordingFetch,
    });
    const client = new CordumClient({
      baseUrl: "https://cordum.test",
      auth: sessionAuth,
      tenantId: "tenant-a",
      fetch: recordingFetch,
    });

    await expect(client.jobs.list()).resolves.toMatchObject({ items: expect.any(Array) });
    await expect(client.jobs.list()).resolves.toMatchObject({ items: expect.any(Array) });

    expect(state.loginCount).toBe(2);
    const nonLoginBodies = observedRequests.filter((request) => !request.url.endsWith("/api/v1/auth/login"));
    expect(nonLoginBodies.every((request) => !request.bodyText?.includes("super-secret-password"))).toBe(true);
  });

  it("passes through bearer authentication providers", async () => {
    const client = new CordumClient({
      baseUrl: "https://cordum.test",
      auth: {
        async applyHeaders(headers: Headers) {
          headers.set("Authorization", `Bearer ${state.validBearerToken}`);
        },
      },
      tenantId: "tenant-a",
    });

    await expect(client.jobs.list()).resolves.toMatchObject({ items: expect.any(Array) });
  });
});
