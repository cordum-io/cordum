import { act } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createTestQueryClient, mockFetch, renderWithQueryClient } from "./__tests__/test-utils";
import {
  useInstallPack,
  useMarketplacePacks,
  usePack,
  usePacks,
  useUninstallPack,
} from "./usePacks";

const { addToastMock, loggerMock } = vi.hoisted(() => ({
  addToastMock: vi.fn(),
  loggerMock: {
    debug: vi.fn(),
    info: vi.fn(),
    warn: vi.fn(),
    error: vi.fn(),
  },
}));

const { mockConfigState } = vi.hoisted(() => ({
  mockConfigState: {
    apiBaseUrl: "/api/v1",
    apiKey: "",
    tenantId: "",
    principalId: "",
    principalRole: "",
    user: null,
    logout: vi.fn(),
  },
}));

vi.mock("../state/config", () => ({
  useConfigStore: {
    getState: () => mockConfigState,
  },
}));

vi.mock("../state/toast", () => ({
  useToastStore: {
    getState: () => ({ addToast: addToastMock }),
  },
}));

vi.mock("../lib/logger", () => ({
  logger: loggerMock,
}));

describe("usePacks hooks", () => {
  beforeEach(() => {
    window.localStorage.clear();
    vi.clearAllMocks();
    mockConfigState.apiBaseUrl = "/api/v1";
    mockConfigState.apiKey = "";
    mockConfigState.tenantId = "";
    mockConfigState.principalId = "";
    mockConfigState.principalRole = "";
    mockConfigState.user = null;
    vi.spyOn(globalThis.crypto, "randomUUID").mockReturnValue("00000000-0000-0000-0000-000000000123");
    vi.spyOn(performance, "now").mockReturnValue(100);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("usePacks starts in loading state", () => {
    mockFetch([{ match: "/packs", method: "GET", body: [] }]);
    const hook = renderWithQueryClient(() => usePacks());
    expect(hook.result.current?.isLoading).toBe(true);
    expect(hook.result.current?.data).toBeUndefined();
    hook.unmount();
  });

  it("usePacks returns error state on fetch failure", async () => {
    mockFetch([{ match: "/packs", method: "GET", status: 500, body: { error: "server error" } }]);
    const hook = renderWithQueryClient(() => usePacks());
    await hook.waitFor(() => {
      expect(hook.result.current?.isError).toBe(true);
    });
    hook.unmount();
  });

  it("usePack returns error state on fetch failure", async () => {
    mockFetch([{ match: "/packs/p1", method: "GET", status: 404, body: { error: "not found" } }]);
    const hook = renderWithQueryClient(() => usePack("p1"));
    await hook.waitFor(() => {
      expect(hook.result.current?.isError).toBe(true);
    });
    hook.unmount();
  });

  it("usePacks fetches /packs and maps records", async () => {
    mockFetch([
      {
        match: "/packs",
        method: "GET",
        body: {
          items: [
            {
              id: "pack-1",
              version: "1.2.3",
              status: "installed",
              manifest: {
                metadata: { id: "pack-1", title: "Pack One", description: "Desc" },
                topics: [{ capability: "cap.alpha" }],
              },
            },
          ],
        },
      },
    ]);

    const hook = renderWithQueryClient(() => usePacks());
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    expect(hook.result.current?.data?.items[0]).toMatchObject({
      id: "pack-1",
      name: "Pack One",
      version: "1.2.3",
      capabilities: ["cap.alpha"],
    });
    hook.unmount();
  });

  it("usePack fetches /packs/{id} and maps single record", async () => {
    mockFetch([
      {
        match: "/packs/pack-9",
        method: "GET",
        body: {
          id: "pack-9",
          version: "9.0.0",
          status: "installed",
          manifest: {
            metadata: { id: "pack-9", title: "Pack Nine" },
            topics: [{ capability: "cap.beta" }],
          },
        },
      },
    ]);

    const hook = renderWithQueryClient(() => usePack("pack-9"));
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    expect(hook.result.current?.data).toMatchObject({ id: "pack-9", name: "Pack Nine" });
    hook.unmount();
  });

  it("useMarketplacePacks fetches catalogs and items", async () => {
    mockFetch([
      {
        match: "/marketplace/packs",
        method: "GET",
        body: {
          catalogs: [{ id: "cat-1", title: "Main Catalog", url: "https://example.org/catalog" }],
          items: [
            {
              id: "pack-1",
              version: "1.0.0",
              title: "Pack One",
              catalog_id: "cat-1",
              installed_status: "installed",
            },
          ],
          fetched_at: "2026-02-13T10:00:00.000Z",
          cached: true,
        },
      },
    ]);

    const hook = renderWithQueryClient(() => useMarketplacePacks());
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    expect(hook.result.current?.data?.catalogs[0]).toMatchObject({ id: "cat-1", title: "Main Catalog" });
    expect(hook.result.current?.data?.items[0]).toMatchObject({ id: "pack-1", catalogId: "cat-1" });
    expect(hook.result.current?.data?.cached).toBe(true);
    hook.unmount();
  });

  it("useInstallPack posts backend field names, maps response, and invalidates caches", async () => {
    const fetchSpy = mockFetch([
      {
        match: "/marketplace/install",
        method: "POST",
        body: {
          id: "pack-installed",
          version: "1.0.0",
          status: "installed",
          manifest: {
            metadata: { id: "pack-installed", title: "Installed Pack" },
            topics: [{ capability: "cap.install" }],
          },
        },
      },
    ]);

    const queryClient = createTestQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    const hook = renderWithQueryClient(() => useInstallPack(), queryClient);

    let result;
    await act(async () => {
      result = await hook.result.current?.mutateAsync({
        catalogId: "cat-1",
        packId: "pack-1",
        version: "2.0.0",
        url: "https://example.org/pack.tgz",
        sha256: "abc123",
        force: true,
        upgrade: true,
        inactive: false,
      });
    });

    const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit];
    expect(JSON.parse(String(init.body))).toEqual({
      catalog_id: "cat-1",
      pack_id: "pack-1",
      version: "2.0.0",
      url: "https://example.org/pack.tgz",
      sha256: "abc123",
      force: true,
      upgrade: true,
      inactive: false,
    });
    expect(result).toMatchObject({ id: "pack-installed", name: "Installed Pack" });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["packs"] });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["marketplace-packs"] });
    expect(addToastMock).toHaveBeenCalledWith({ type: "success", title: "Pack installed" });

    hook.unmount();
  });

  it("useUninstallPack posts endpoint and invalidates related caches", async () => {
    const fetchSpy = mockFetch([
      {
        match: "/packs/pack-1/uninstall",
        method: "POST",
        body: null,
      },
    ]);

    const queryClient = createTestQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    const hook = renderWithQueryClient(() => useUninstallPack(), queryClient);

    await act(async () => {
      await hook.result.current?.mutateAsync("pack-1");
    });

    expect(fetchSpy).toHaveBeenCalledTimes(1);
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["packs"] });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["pack", "pack-1"] });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["marketplace-packs"] });
    expect(addToastMock).toHaveBeenCalledWith({ type: "success", title: "Pack uninstalled" });

    hook.unmount();
  });
});

