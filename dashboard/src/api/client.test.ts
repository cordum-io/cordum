import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

type MockUser = { id: string } | null;

interface MockConfigState {
  apiBaseUrl: string;
  apiKey: string;
  tenantId: string;
  principalId: string;
  principalRole: string;
  user: MockUser;
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

import { ApiError, del, get, patch, post, put } from "./client";

function jsonResponse(status: number, body: unknown, statusText = "OK"): Response {
  return new Response(JSON.stringify(body), {
    status,
    statusText,
    headers: { "Content-Type": "application/json" },
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
      tenantId: "tenant-1",
      principalId: "principal-1",
      principalRole: "admin",
      user: { id: "user-1" },
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
      tenantId: "tenant-1",
      principalId: "principal-1",
      principalRole: "admin",
      user: { id: "user-1" },
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
      tenantId: "tenant-1",
      principalId: "principal-1",
      principalRole: "admin",
      user: { id: "user-1" },
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
    expect(mockedWindow.location.href).toBe("/login");
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
      tenantId: "",
      principalId: "",
      principalRole: "",
      user: { id: "user-fallback" },
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
});

