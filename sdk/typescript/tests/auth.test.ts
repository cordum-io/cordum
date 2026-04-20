import { inspect } from "node:util";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from "vitest";
import { HttpResponse, http } from "msw";
import { setupServer } from "msw/node";
import { ApiKeyAuth, BearerTokenAuth, SessionAuth } from "../src/auth.js";

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

describe("auth providers", () => {
  let loginCount = 0;
  let protectedCount = 0;

  beforeEach(() => {
    loginCount = 0;
    protectedCount = 0;
  });

  it("injects the configured API-key header", async () => {
    const headers = new Headers();
    const auth = new ApiKeyAuth("secret-api-key", { header: "X-Test-Key" });

    await auth.applyHeaders(headers, {});
    expect(headers.get("X-Test-Key")).toBe("secret-api-key");
  });

  it("injects bearer tokens into the Authorization header", async () => {
    const headers = new Headers();
    const auth = new BearerTokenAuth("secret-bearer-token");

    await auth.applyHeaders(headers, {});
    expect(headers.get("Authorization")).toBe("Bearer secret-bearer-token");
  });

  it("performs a lazy session login and applies the returned token", async () => {
    server.use(
      http.post("https://cordum.test/api/v1/auth/login", async ({ request }) => {
        loginCount += 1;
        const body = (await request.json()) as { username?: string; password?: string };
        expect(body).toEqual({ username: "operator@cordum.test", password: "correct horse battery staple" });
        return HttpResponse.json({
          token: createJwtWithExp(Math.floor(Date.now() / 1000) + 3_600),
        });
      }),
    );

    const auth = new SessionAuth({
      baseUrl: "https://cordum.test",
      email: "operator@cordum.test",
      password: "correct horse battery staple",
      fetch: globalThis.fetch,
    });

    const headers = new Headers();
    await auth.applyHeaders(headers, {});

    expect(headers.get("Authorization")).toMatch(/^Bearer /);
    expect(loginCount).toBe(1);
  });

  it("reuses a single in-flight session login across concurrent header applications", async () => {
    server.use(
      http.post("https://cordum.test/api/v1/auth/login", async () => {
        loginCount += 1;
        await new Promise((resolve) => setTimeout(resolve, 20));
        return HttpResponse.json({
          token: createJwtWithExp(Math.floor(Date.now() / 1000) + 3_600),
        });
      }),
    );

    const auth = new SessionAuth({
      baseUrl: "https://cordum.test",
      email: "operator@cordum.test",
      password: "correct horse battery staple",
      fetch: globalThis.fetch,
    });

    const headersList = Array.from({ length: 5 }, () => new Headers());
    await Promise.all(headersList.map((headers) => auth.applyHeaders(headers, {})));

    expect(loginCount).toBe(1);
    for (const headers of headersList) {
      expect(headers.get("Authorization")).toMatch(/^Bearer /);
    }
  });

  it("refreshes once on 401 and retries with a new session token", async () => {
    let issuedToken = 0;
    let firstToken: string | undefined;
    let secondToken: string | undefined;
    server.use(
      http.post("https://cordum.test/api/v1/auth/login", async () => {
        loginCount += 1;
        issuedToken += 1;
        const token = createJwtWithMarker(`token-${issuedToken}`, Math.floor(Date.now() / 1000) + 3_600);
        if (issuedToken === 1) {
          firstToken = token;
        } else if (issuedToken === 2) {
          secondToken = token;
        }
        return HttpResponse.json({
          token,
        });
      }),
      http.get("https://cordum.test/api/v1/protected", ({ request }) => {
        protectedCount += 1;
        const authorization = request.headers.get("Authorization");
        if (authorization === `Bearer ${firstToken}`) {
          return HttpResponse.json({ error: "expired" }, { status: 401 });
        }
        if (authorization === `Bearer ${secondToken}`) {
          return HttpResponse.json({ ok: true }, { status: 200 });
        }
        return HttpResponse.json({ error: "unexpected token" }, { status: 500 });
      }),
    );

    const auth = new SessionAuth({
      baseUrl: "https://cordum.test",
      email: "operator@cordum.test",
      password: "correct horse battery staple",
      fetch: globalThis.fetch,
    });

    let response = await callProtectedRoute(auth);
    if (await auth.handleUnauthorizedResponse(response)) {
      response = await callProtectedRoute(auth);
    }

    expect(response.status).toBe(200);
    expect(loginCount).toBe(2);
    expect(protectedCount).toBe(2);
  });

  it("redacts secrets from JSON.stringify across providers", () => {
    const providers = [
      [new ApiKeyAuth("api-secret"), "api-secret"],
      [new BearerTokenAuth("bearer-secret"), "bearer-secret"],
      [
        new SessionAuth({
          baseUrl: "https://cordum.test",
          email: "operator@cordum.test",
          password: "session-secret",
          fetch: globalThis.fetch,
        }),
        "session-secret",
      ],
    ] as const;

    for (const [provider, secret] of providers) {
      const serialized = JSON.stringify(provider);
      expect(serialized).not.toContain(secret);
      expect(serialized).toContain("\"redacted\":true");
    }
  });

  it("redacts secrets from util.inspect across providers", () => {
    const providers = [
      [new ApiKeyAuth("api-secret"), "api-secret"],
      [new BearerTokenAuth("bearer-secret"), "bearer-secret"],
      [
        new SessionAuth({
          baseUrl: "https://cordum.test",
          email: "operator@cordum.test",
          password: "session-secret",
          fetch: globalThis.fetch,
        }),
        "session-secret",
      ],
    ] as const;

    for (const [provider, secret] of providers) {
      const inspected = inspect(provider);
      expect(inspected).not.toContain(secret);
      expect(inspected).toContain("redacted: true");
    }
  });
});

async function callProtectedRoute(auth: SessionAuth): Promise<Response> {
  const headers = new Headers();
  await auth.applyHeaders(headers, {});
  return fetch("https://cordum.test/api/v1/protected", { headers });
}

function createJwtWithExp(exp: number): string {
  return createJwt({ exp });
}

function createJwtWithMarker(tokenId: string, exp: number): string {
  return createJwt({ exp, jti: tokenId });
}

function createJwt(payload: Record<string, unknown>): string {
  return [
    encodeBase64Url({ alg: "none", typ: "JWT" }),
    encodeBase64Url(payload),
    "signature",
  ].join(".");
}

function encodeBase64Url(value: Record<string, unknown>): string {
  return Buffer.from(JSON.stringify(value), "utf8").toString("base64url");
}
