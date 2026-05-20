import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

type MockUser = { id: string } | null;

interface MockConfigState {
  apiBaseUrl: string;
  apiKey: string;
  authMode: "apikey" | "session" | "anonymous";
  tenantId: string;
  principalId: string;
  principalRole: string;
  user: MockUser;
  isLoggingOut: boolean;
  logout: ReturnType<typeof vi.fn>;
}

const { loggerMock, getStateMock } = vi.hoisted(() => ({
  loggerMock: {
    debug: vi.fn(),
    info: vi.fn(),
    warn: vi.fn(),
    error: vi.fn(),
  },
  getStateMock: vi.fn(),
}));

let mockConfigState: MockConfigState;
let fetchMock: ReturnType<typeof vi.fn>;
let randomUUIDSpy: ReturnType<typeof vi.spyOn>;
let performanceNowSpy: ReturnType<typeof vi.spyOn>;

vi.mock("../state/config", () => ({
  useConfigStore: {
    getState: getStateMock,
  },
}));

vi.mock("../lib/logger", () => ({
  logger: loggerMock,
}));

import { ApiError, apiClient, del, get, patch, post, put } from "./client";

function jsonResponse(status: number, body: unknown, statusText = "OK"): Response {
  return new Response(JSON.stringify(body), {
    status,
    statusText,
    headers: { "Content-Type": "application/json" },
  });
}

function textResponse(
  status: number,
  body: string,
  contentType: string,
  statusText = "OK",
): Response {
  return new Response(body, {
    status,
    statusText,
    headers: { "Content-Type": contentType },
  });
}

async function captureApiError(request: Promise<unknown>): Promise<ApiError> {
  try {
    await request;
  } catch (err) {
    if (err instanceof ApiError) {
      return err;
    }
    throw err;
  }
  throw new Error("expected ApiError");
}

describe("api client - get", () => {
  beforeEach(() => {
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    mockConfigState = {
      apiBaseUrl: "https://api.example.test/api/v1/",
      apiKey: "api-key-1",
      authMode: "apikey",
      tenantId: "tenant-1",
      principalId: "principal-1",
      principalRole: "admin",
      user: { id: "user-1" },
      isLoggingOut: false,
      logout: vi.fn(),
    };
    getStateMock.mockImplementation(() => mockConfigState);

    randomUUIDSpy = vi.spyOn(globalThis.crypto, "randomUUID").mockReturnValue("00000000-0000-0000-0000-000000000123");
    performanceNowSpy = vi.spyOn(performance, "now").mockImplementation(() => 100);
    vi.clearAllMocks();
  });

  afterEach(() => {
    randomUUIDSpy.mockRestore();
    performanceNowSpy.mockRestore();
    vi.unstubAllGlobals();
  });

  it("session-mode authMode does NOT send X-API-Key even when apiKey slot is non-empty (login-loop regression)", async () => {
    // Customer-reported bug: password/SSO login is session-based. Even if a
    // stale `apiKey` value lingers in the store, authMode="session" gates the
    // X-API-Key header off so the cookie is the only auth signal sent.
    mockConfigState.authMode = "session";
    mockConfigState.apiKey = "stale-session-token"; // would have triggered the loop
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }));

    await get("/topics");

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    const headers = init.headers as Record<string, string>;
    expect(headers["X-API-Key"]).toBeUndefined();
    // Tenant + principal headers still flow normally
    expect(headers["X-Tenant-ID"]).toBe("tenant-1");
    expect(headers["X-Principal-Id"]).toBe("principal-1");
    // credentials: "include" carries the httpOnly cookie auth
    expect(init.credentials).toBe("include");
  });

  it("anonymous authMode sends no X-API-Key regardless of apiKey contents", async () => {
    mockConfigState.authMode = "anonymous";
    mockConfigState.apiKey = "leftover-from-prior-logout";
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }));

    await get("/topics");

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    const headers = init.headers as Record<string, string>;
    expect(headers["X-API-Key"]).toBeUndefined();
  });

  it("constructs GET request URL + auth headers and parses JSON response", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { id: "job-1", ok: true }));

    const result = await get<{ id: string; ok: boolean }>("/jobs/job-1");

    expect(result).toEqual({ id: "job-1", ok: true });
    expect(fetchMock).toHaveBeenCalledTimes(1);

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("https://api.example.test/api/v1/jobs/job-1");
    expect(init.method).toBe("GET");
    expect(init.headers).toEqual({
      "Content-Type": "application/json",
      "X-Request-Id": "00000000-0000-0000-0000-000000000123",
      "X-API-Key": "api-key-1",
      "X-Tenant-ID": "tenant-1",
      "X-Principal-Id": "principal-1",
      "X-Principal-Role": "admin",
    });
  });

  it("returns undefined on 204 no-content responses", async () => {
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204, statusText: "No Content" }));

    const result = await get<undefined>("/jobs/job-2");

    expect(result).toBeUndefined();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});

describe("api client - write methods", () => {
  beforeEach(() => {
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    mockConfigState = {
      apiBaseUrl: "https://api.example.test/api/v1",
      apiKey: "api-key-1",
      authMode: "apikey",
      tenantId: "tenant-1",
      principalId: "principal-1",
      principalRole: "admin",
      user: { id: "user-1" },
      isLoggingOut: false,
      logout: vi.fn(),
    };
    getStateMock.mockImplementation(() => mockConfigState);

    randomUUIDSpy = vi.spyOn(globalThis.crypto, "randomUUID").mockReturnValue("00000000-0000-0000-0000-000000000123");
    performanceNowSpy = vi.spyOn(performance, "now").mockImplementation(() => 100);
    vi.clearAllMocks();
  });

  afterEach(() => {
    randomUUIDSpy.mockRestore();
    performanceNowSpy.mockRestore();
    vi.unstubAllGlobals();
  });

  it("post sends JSON-stringified body", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { ok: true }));
    await post<{ ok: boolean }>("/jobs", { topic: "sys.job.submit", attempt: 1 });

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("https://api.example.test/api/v1/jobs");
    expect(init.method).toBe("POST");
    expect(init.body).toBe(JSON.stringify({ topic: "sys.job.submit", attempt: 1 }));
  });

  it("post omits body when argument is undefined", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { ok: true }));
    await post<{ ok: boolean }>("/jobs");

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(init.method).toBe("POST");
    expect(init.body).toBeUndefined();
  });

  it("put sends method PUT with serialized body", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { ok: true }));
    await put<{ ok: boolean }>("/jobs/job-1", { status: "running" });

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(init.method).toBe("PUT");
    expect(init.body).toBe(JSON.stringify({ status: "running" }));
  });

  it("patch sends method PATCH with serialized body", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { ok: true }));
    await patch<{ ok: boolean }>("/jobs/job-1", { status: "failed" });

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(init.method).toBe("PATCH");
    expect(init.body).toBe(JSON.stringify({ status: "failed" }));
  });

  it("del sends method DELETE without a request body", async () => {
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }));
    await del("/jobs/job-1");

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(init.method).toBe("DELETE");
    expect(init.body).toBeUndefined();
  });
});

describe("api client - error handling", () => {
  beforeEach(() => {
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    mockConfigState = {
      apiBaseUrl: "https://api.example.test/api/v1",
      apiKey: "api-key-1",
      authMode: "apikey",
      tenantId: "tenant-1",
      principalId: "principal-1",
      principalRole: "admin",
      user: { id: "user-1" },
      isLoggingOut: false,
      logout: vi.fn(),
    };
    getStateMock.mockImplementation(() => mockConfigState);

    randomUUIDSpy = vi.spyOn(globalThis.crypto, "randomUUID").mockReturnValue("00000000-0000-0000-0000-000000000123");
    performanceNowSpy = vi.spyOn(performance, "now").mockImplementation(() => 100);
    vi.clearAllMocks();
    window.history.replaceState({}, "", "/dashboard");
  });

  afterEach(() => {
    randomUUIDSpy.mockRestore();
    performanceNowSpy.mockRestore();
    vi.unstubAllGlobals();
  });

  it("throws ApiError and logs out on 401 responses", async () => {
    vi.useFakeTimers();
    const mockedWindow = {
      location: {
        pathname: "/dashboard",
        href: "/dashboard",
      },
    } as unknown as Window & typeof globalThis;
    vi.stubGlobal("window", mockedWindow);

    fetchMock.mockResolvedValueOnce(
      jsonResponse(401, { error: "unauthorized" }, "Unauthorized"),
    );

    const err = await captureApiError(get("/protected"));
    expect(err).toBeInstanceOf(ApiError);
    expect(err.name).toBe("ApiError");
    expect(err.status).toBe(401);
    expect(err.message).toBe("Unauthorized — session expired");
    expect(mockConfigState.logout).toHaveBeenCalledTimes(1);
    // Redirect is delayed by 1.5s so users see the session-expired toast.
    expect(mockedWindow.location.href).toBe("/dashboard");
    vi.advanceTimersByTime(1500);
    expect(mockedWindow.location.href).toBe("/login");
    vi.useRealTimers();
  });

  it("coalesces simultaneous 401 responses into a single logout", async () => {
    vi.useFakeTimers();
    const mockedWindow = {
      location: {
        pathname: "/dashboard",
        href: "/dashboard",
      },
    } as unknown as Window & typeof globalThis;
    vi.stubGlobal("window", mockedWindow);

    mockConfigState.logout.mockImplementation(() => {
      mockConfigState.isLoggingOut = true;
    });

    fetchMock
      .mockResolvedValueOnce(jsonResponse(401, { error: "unauthorized" }, "Unauthorized"))
      .mockResolvedValueOnce(jsonResponse(401, { error: "unauthorized" }, "Unauthorized"));

    const [firstError, secondError] = await Promise.all([
      captureApiError(get("/protected-a")),
      captureApiError(get("/protected-b")),
    ]);

    expect(firstError.status).toBe(401);
    expect(secondError.status).toBe(401);
    expect(mockConfigState.logout).toHaveBeenCalledTimes(1);
    // Only one redirect scheduled (isLoggingOut guard).
    vi.advanceTimersByTime(1500);
    expect(mockedWindow.location.href).toBe("/login");
    vi.useRealTimers();
  });

  it("throws forbidden ApiError on 403 with parsed body", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(403, { reason: "missing role" }, "Forbidden"),
    );

    const err = await captureApiError(get("/admin"));
    expect(err).toBeInstanceOf(ApiError);
    expect(err.status).toBe(403);
    expect(err.message).toBe("Forbidden — insufficient permissions");
    expect(err.body).toEqual({ reason: "missing role" });
  });

  it("throws rate-limit ApiError on 429 responses", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(429, { retry_after_ms: 1000 }, "Too Many Requests"),
    );

    const err = await captureApiError(get("/burst"));
    expect(err).toBeInstanceOf(ApiError);
    expect(err.status).toBe(429);
    expect(err.message).toBe("Rate limit exceeded — please slow down");
    expect(err.body).toEqual({ retry_after_ms: 1000 });
  });

  it("uses body.error for generic error messages", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(500, { error: "backend exploded", detail: "trace-id" }, "Server Error"),
    );

    const err = await captureApiError(get("/jobs"));
    expect(err.status).toBe(500);
    expect(err.message).toBe("backend exploded");
    expect(err.body).toEqual({ error: "backend exploded", detail: "trace-id" });
  });

  it("uses body.message for generic error messages when error is absent", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(500, { message: "upstream timeout" }, "Server Error"),
    );

    const err = await captureApiError(get("/jobs"));
    expect(err.status).toBe(500);
    expect(err.message).toBe("upstream timeout");
    expect(err.body).toEqual({ message: "upstream timeout" });
  });

  it("falls back to statusText for non-JSON error bodies", async () => {
    fetchMock.mockResolvedValueOnce(
      new Response("not json", {
        status: 500,
        statusText: "Internal Server Error",
        headers: { "Content-Type": "text/plain" },
      }),
    );

    const err = await captureApiError(get("/jobs"));
    expect(err.status).toBe(500);
    expect(err.message).toBe("Internal Server Error");
    expect(err.body).toBeUndefined();
  });

  it("propagates network errors when fetch rejects", async () => {
    const networkError = new Error("network down");
    fetchMock.mockRejectedValueOnce(networkError);

    await expect(get("/jobs")).rejects.toBe(networkError);
  });

  it("ApiError carries status, message, and body properties", () => {
    const err = new ApiError(418, "teapot", { reason: "short and stout" });
    expect(err.name).toBe("ApiError");
    expect(err.status).toBe(418);
    expect(err.message).toBe("teapot");
    expect(err.body).toEqual({ reason: "short and stout" });
  });
});

describe("api client - baseUrl and auth header edge cases", () => {
  beforeEach(() => {
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    mockConfigState = {
      apiBaseUrl: "",
      apiKey: "",
      authMode: "anonymous",
      tenantId: "",
      principalId: "",
      principalRole: "",
      user: { id: "user-fallback" },
      isLoggingOut: false,
      logout: vi.fn(),
    };
    getStateMock.mockImplementation(() => mockConfigState);

    randomUUIDSpy = vi.spyOn(globalThis.crypto, "randomUUID").mockReturnValue("00000000-0000-0000-0000-000000000123");
    performanceNowSpy = vi.spyOn(performance, "now").mockImplementation(() => 100);
    vi.clearAllMocks();
  });

  afterEach(() => {
    randomUUIDSpy.mockRestore();
    performanceNowSpy.mockRestore();
    vi.unstubAllGlobals();
    vi.unstubAllEnvs();
  });

  it("falls back to VITE_API_URL and strips trailing slash", async () => {
    vi.stubEnv("VITE_API_URL", "https://env.example.test/root/");
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }));

    await get("/health");

    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("https://env.example.test/root/health");
  });

  it("omits API key and tenant headers and uses user.id when principalId is empty", async () => {
    mockConfigState.principalRole = "operator";
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }));

    await get("/me");

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    const headers = init.headers as Record<string, string>;
    expect(headers["Content-Type"]).toBe("application/json");
    expect(headers["X-Request-Id"]).toBe("00000000-0000-0000-0000-000000000123");
    expect(headers["X-Principal-Id"]).toBe("user-fallback");
    expect(headers["X-Principal-Role"]).toBe("operator");
    expect(headers["X-API-Key"]).toBeUndefined();
    expect(headers["X-Tenant-ID"]).toBeUndefined();
  });

  describe("request() error wrapping", () => {
    it("wraps a fetch network rejection (TypeError) in ApiError(0) with a friendly message", async () => {
      fetchMock.mockRejectedValueOnce(new TypeError("Failed to fetch"));

      const err = await captureApiError(get("/foo"));
      expect(err.status).toBe(0);
      expect(err.message).toMatch(/network/i);
    });

    it("wraps an internal-timeout AbortError in ApiError(408) when AbortSignal.timeout fires", async () => {
      const timeoutController = new AbortController();
      const timeoutSpy = vi
        .spyOn(AbortSignal, "timeout")
        .mockImplementation(() => timeoutController.signal);

      try {
        fetchMock.mockImplementation((_url: string, init: RequestInit) => {
          return new Promise((_resolve, reject) => {
            const sig = init.signal as AbortSignal;
            if (sig.aborted) {
              reject(sig.reason ?? new DOMException("Aborted", "AbortError"));
              return;
            }
            sig.addEventListener("abort", () => {
              reject(sig.reason ?? new DOMException("Aborted", "AbortError"));
            });
          });
        });

        const pending = get("/slow");
        // Fire the internal timeout signal as if 30s had elapsed.
        timeoutController.abort(new DOMException("Timeout", "TimeoutError"));

        const err = await captureApiError(pending);
        expect(err.status).toBe(408);
        expect(err.message).toMatch(/timed out/i);
      } finally {
        timeoutSpy.mockRestore();
      }
    });

    it("preserves a caller-initiated AbortError instead of wrapping it as ApiError", async () => {
      const controller = new AbortController();
      controller.abort();

      fetchMock.mockImplementation((_url: string, init: RequestInit) => {
        return new Promise((_resolve, reject) => {
          const sig = init.signal as AbortSignal;
          if (sig.aborted) {
            reject(sig.reason ?? new DOMException("Aborted", "AbortError"));
            return;
          }
          sig.addEventListener("abort", () => {
            reject(sig.reason ?? new DOMException("Aborted", "AbortError"));
          });
        });
      });

      let caught: unknown;
      try {
        await post("/foo", { hello: "world" }, { signal: controller.signal });
      } catch (e) {
        caught = e;
      }

      expect(caught).toBeDefined();
      expect(caught).not.toBeInstanceOf(ApiError);
      expect((caught as Error).name).toBe("AbortError");
    });
  });
});

describe("apiClient (orval mutator adapter)", () => {
  beforeEach(() => {
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    mockConfigState = {
      apiBaseUrl: "https://api.example.test/api/v1/",
      apiKey: "api-key-1",
      authMode: "apikey",
      tenantId: "tenant-1",
      principalId: "principal-1",
      principalRole: "admin",
      user: { id: "user-1" },
      isLoggingOut: false,
      logout: vi.fn(),
    };
    getStateMock.mockImplementation(() => mockConfigState);

    randomUUIDSpy = vi
      .spyOn(globalThis.crypto, "randomUUID")
      .mockReturnValue("00000000-0000-0000-0000-000000000123");
    performanceNowSpy = vi
      .spyOn(performance, "now")
      .mockImplementation(() => 100);
    vi.clearAllMocks();
  });

  afterEach(() => {
    randomUUIDSpy.mockRestore();
    performanceNowSpy.mockRestore();
    vi.unstubAllGlobals();
  });

  it("translates JSON data into a stringified body and forwards method + headers", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { ok: true }));

    await apiClient<{ ok: boolean }>({
      url: "/jobs",
      method: "post",
      data: { topic: "demo", payload: { n: 1 } },
    });

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("https://api.example.test/api/v1/jobs");
    expect(init.method).toBe("POST");
    expect(init.body).toBe(JSON.stringify({ topic: "demo", payload: { n: 1 } }));
    expect((init.headers as Record<string, string>)["Content-Type"]).toBe(
      "application/json",
    );
  });

  it("preserves FormData bodies and does NOT force Content-Type (lets the runtime set the multipart boundary)", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { id: "pack-1" }));

    const fd = new FormData();
    fd.append("bundle", new Blob(["zip-bytes"], { type: "application/zip" }), "pack.tgz");

    await apiClient<{ id: string }>({
      url: "/packs/install",
      method: "POST",
      data: fd,
    });

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(init.body).toBe(fd);
    const headers = init.headers as Record<string, string>;
    expect(headers["Content-Type"]).toBeUndefined();
    // Auth headers must still be applied — only Content-Type is dropped.
    expect(headers["X-API-Key"]).toBe("api-key-1");
    expect(headers["X-Tenant-ID"]).toBe("tenant-1");
  });

  it("serializes params into a URL query string, repeating array values per key", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { items: [] }));

    await apiClient({
      url: "/jobs",
      method: "GET",
      params: { status: "running", tags: ["a", "b"], skip: undefined },
    });

    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe(
      "https://api.example.test/api/v1/jobs?status=running&tags=a&tags=b",
    );
  });

  it("forwards the provided AbortSignal so React Query cancellation propagates", async () => {
    const controller = new AbortController();
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { ok: true }));

    await apiClient({
      url: "/health",
      method: "GET",
      signal: controller.signal,
    });

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    // The internal request() composes our signal with a 30s timeout signal,
    // so init.signal will be a different (composite) AbortSignal — but it
    // must be defined and follow our controller's abort.
    expect(init.signal).toBeDefined();
    expect(init.signal!.aborted).toBe(false);
    controller.abort();
    expect(init.signal!.aborted).toBe(true);
  });

  it("strips the leading /api/v1 from orval-emitted URLs so the final fetch is single-prefixed", async () => {
    // Reproduces QA reopen finding (msg-580c1422 / task-7cde446c rejectionDetails):
    // generated hooks pass `url: "/api/v1/jobs"` but request() prepends baseUrl()
    // (`/api/v1/`), so without normalization the fetch URL would be
    // `https://api.example.test/api/v1/api/v1/jobs` and 404 in production.
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { items: [] }));

    await apiClient<{ items: unknown[] }>({
      url: "/api/v1/jobs",
      method: "GET",
    });

    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("https://api.example.test/api/v1/jobs");
    expect(url).not.toMatch(/\/api\/v1\/api\/v1\//);
  });

  it("parses successful text/csv responses as text by content type", async () => {
    fetchMock.mockResolvedValueOnce(
      textResponse(200, "id,status\njob-1,succeeded\n", "text/csv"),
    );

    const result = await apiClient<string>({
      url: "/audit/export",
      method: "GET",
    });

    expect(result).toBe("id,status\njob-1,succeeded\n");
  });

  it("parses successful NDJSON responses as text by content type", async () => {
    const ndjson = "{\"id\":\"evt-1\"}\n{\"id\":\"evt-2\"}\n";
    fetchMock.mockResolvedValueOnce(
      textResponse(200, ndjson, "application/x-ndjson"),
    );

    const result = await apiClient<string>({
      url: "/audit/export",
      method: "GET",
    });

    expect(result).toBe(ndjson);
  });

  it("honors explicit responseType=text even when the server marks the body as JSON", async () => {
    fetchMock.mockResolvedValueOnce(
      textResponse(200, "{\"raw\":true}", "application/json"),
    );

    const result = await apiClient<string>({
      url: "/raw-json-text",
      method: "GET",
      responseType: "text",
    });

    expect(result).toBe("{\"raw\":true}");
  });

  it("honors explicit responseType=blob", async () => {
    fetchMock.mockResolvedValueOnce(
      textResponse(200, "binary-ish", "application/octet-stream"),
    );

    const result = await apiClient<Blob>({
      url: "/artifact",
      method: "GET",
      responseType: "blob",
    });

    // Response.blob() may return a Blob from a different JS realm in CI
    // (undici/jsdom), so avoid instanceof and assert the Blob contract instead.
    expect(Object.prototype.toString.call(result)).toBe("[object Blob]");
    expect(result.size).toBe("binary-ish".length);
    expect(result.type).toBe("application/octet-stream");
    await expect(result.text()).resolves.toBe("binary-ish");
  });

  it("honors explicit responseType=arraybuffer", async () => {
    fetchMock.mockResolvedValueOnce(
      textResponse(200, "buffer-ish", "application/octet-stream"),
    );

    const result = await apiClient<ArrayBuffer>({
      url: "/artifact",
      method: "GET",
      responseType: "arraybuffer",
    });

    expect(new TextDecoder().decode(result)).toBe("buffer-ish");
  });
});

describe("apiClient — generated hook integration", () => {
  beforeEach(() => {
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    mockConfigState = {
      apiBaseUrl: "https://api.example.test/api/v1/",
      apiKey: "api-key-1",
      authMode: "apikey",
      tenantId: "tenant-1",
      principalId: "principal-1",
      principalRole: "admin",
      user: { id: "user-1" },
      isLoggingOut: false,
      logout: vi.fn(),
    };
    getStateMock.mockImplementation(() => mockConfigState);

    randomUUIDSpy = vi
      .spyOn(globalThis.crypto, "randomUUID")
      .mockReturnValue("00000000-0000-0000-0000-000000000123");
    performanceNowSpy = vi
      .spyOn(performance, "now")
      .mockImplementation(() => 100);
    vi.clearAllMocks();
  });

  afterEach(() => {
    randomUUIDSpy.mockRestore();
    performanceNowSpy.mockRestore();
    vi.unstubAllGlobals();
  });

  it("invokes a real generated function (listJobs) without double-prefixing /api/v1", async () => {
    // Imports a real orval-emitted hook so this regression cannot be bypassed
    // by future refactors of the apiClient adapter that stop normalizing URLs.
    const { listJobs } = await import("./generated/jobs/jobs");
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { items: [] }));

    await listJobs();

    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("https://api.example.test/api/v1/jobs");
    expect(url).not.toMatch(/\/api\/v1\/api\/v1\//);
  });

  it("invokes generated audit export and returns CSV/NDJSON text instead of forcing JSON parse", async () => {
    const { exportAuditCompliance } = await import("./generated/audit-export/audit-export");
    fetchMock.mockResolvedValueOnce(
      textResponse(200, "sequence,hash\n1,abc\n", "text/csv"),
    );

    const result = await exportAuditCompliance({ format: "csv" });

    const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("https://api.example.test/api/v1/audit/export?format=csv");
    expect(result).toBe("sequence,hash\n1,abc\n");
  });
});
