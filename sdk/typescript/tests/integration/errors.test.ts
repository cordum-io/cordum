import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from "vitest";
import { setupServer } from "msw/node";
import { HttpResponse, http } from "msw";
import { CordumClient } from "../../src/client.js";
import { ServerError, ValidationError } from "../../src/errors.js";
import { createIntegrationHandlers, createIntegrationState, type IntegrationState } from "./handlers.js";

describe("integration error handling", () => {
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

  it("parses gateway validation envelopes into fieldErrors", async () => {
    server.use(
      http.post("https://cordum.test/api/v1/jobs", () =>
        HttpResponse.json(
          {
            error: {
              code: "validation_failed",
              message: "Validation failed",
              details: {
                fields: {
                  prompt: ["required"],
                },
              },
            },
          },
          { status: 422, headers: { "X-Request-Id": "req-validation" } },
        ),
      ),
    );

    const client = new CordumClient({ baseUrl: "https://cordum.test", auth: state.validApiKey, tenantId: "tenant-a" });
    const error = await client.jobs.create({ topic: "job.default" } as never).catch((reason: unknown) => reason);

    expect(error).toBeInstanceOf(ValidationError);
    expect((error as ValidationError).fieldErrors).toEqual({ prompt: ["required"] });
    expect((error as ValidationError).requestId).toBe("req-validation");
  });

  it("falls back to text payloads for unknown error bodies", async () => {
    server.use(
      http.get("https://cordum.test/api/v1/jobs", () =>
        new HttpResponse("plain failure", { status: 501, headers: { "content-type": "text/plain" } }),
      ),
    );

    const client = new CordumClient({ baseUrl: "https://cordum.test", auth: state.validApiKey, tenantId: "tenant-a" });
    const error = await client.jobs.list().catch((reason: unknown) => reason);

    expect(error).toBeInstanceOf(ServerError);
    expect((error as ServerError).payload).toBe("plain failure");
  });

  it("echoes request ids into authentication errors", async () => {
    server.use(
      http.get("https://cordum.test/api/v1/jobs", () =>
        HttpResponse.json({ message: "unauthorized" }, { status: 401, headers: { "X-Request-Id": "req-auth-401" } }),
      ),
    );

    const client = new CordumClient({ baseUrl: "https://cordum.test", auth: state.validApiKey, tenantId: "tenant-a" });
    const error = await client.jobs.list().catch((reason: unknown) => reason);

    expect((error as { requestId?: string }).requestId).toBe("req-auth-401");
  });
});
