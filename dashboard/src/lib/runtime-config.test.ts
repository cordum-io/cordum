import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { updateMock, getStateMock } = vi.hoisted(() => {
  const update = vi.fn();
  return {
    updateMock: update,
    getStateMock: vi.fn(() => ({ update })),
  };
});

vi.mock("../state/config", () => ({
  useConfigStore: {
    getState: getStateMock,
  },
}));

describe("loadRuntimeConfig", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.stubGlobal("fetch", vi.fn());
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("fetches /config.json and updates store with normalized values", async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        apiBaseUrl: " https://api.example.com/v1/ ",
        apiKey: "  key-1  ",
        tenantId: "  tenant-1 ",
        principalId: " principal-1 ",
        principalRole: " admin ",
        traceUrlTemplate: " https://trace.example/{{traceId}} ",
      }),
    } as unknown as Response);

    const { loadRuntimeConfig } = await import("./runtime-config");
    await loadRuntimeConfig();

    expect(fetchMock).toHaveBeenCalledWith(
      "/config.json",
      expect.objectContaining({
        cache: "no-store",
        headers: { Accept: "application/json" },
      }),
    );
    expect(updateMock).toHaveBeenCalledWith({
      apiBaseUrl: "https://api.example.com/v1",
      apiKey: "key-1",
      tenantId: "tenant-1",
      principalId: "principal-1",
      principalRole: "admin",
      traceUrlTemplate: "https://trace.example/{{traceId}}",
    });
  });

  it("does not update when fetch fails or response is non-ok", async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce({ ok: false } as unknown as Response);
    fetchMock.mockRejectedValueOnce(new Error("network error"));

    const { loadRuntimeConfig } = await import("./runtime-config");
    await loadRuntimeConfig();
    await loadRuntimeConfig();

    expect(updateMock).not.toHaveBeenCalled();
  });

  it("does not update on invalid JSON or non-object payload", async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce({
      ok: true,
      json: async () => {
        throw new Error("bad json");
      },
    } as unknown as Response);
    fetchMock.mockResolvedValueOnce({
      ok: true,
      json: async () => "not-object",
    } as unknown as Response);

    const { loadRuntimeConfig } = await import("./runtime-config");
    await loadRuntimeConfig();
    await loadRuntimeConfig();

    expect(updateMock).not.toHaveBeenCalled();
  });

  it("does not update when all fields are invalid after normalization", async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        apiBaseUrl: "ftp://invalid.example",
        apiKey: " ",
        tenantId: "",
        principalId: "   ",
        principalRole: null,
        traceUrlTemplate: "javascript:alert(1)",
      }),
    } as unknown as Response);

    const { loadRuntimeConfig } = await import("./runtime-config");
    await loadRuntimeConfig();

    expect(updateMock).not.toHaveBeenCalled();
  });

  it("normalizes relative base URL and allows safe template", async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        apiBaseUrl: "/api/v1///",
        traceUrlTemplate: "/trace/{{traceId}}",
      }),
    } as unknown as Response);

    const { loadRuntimeConfig } = await import("./runtime-config");
    await loadRuntimeConfig();

    expect(updateMock).toHaveBeenCalledWith({
      apiBaseUrl: "/api/v1",
      traceUrlTemplate: "/trace/{{traceId}}",
    });
  });
});
