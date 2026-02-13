import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { createTestQueryClient, mockFetch, renderWithQueryClient } from "./__tests__/test-utils";
import {
  __settingsInternal,
  useChangePassword,
  useCreateApiKey,
  useCreateUser,
  useEnvironments,
  useGeneralConfig,
  useMcpConfig,
  useMcpResources,
  useMcpStatus,
  useMcpTools,
  useSetMcpConfig,
  useNotificationChannels,
  useSetConfig,
  useResetUserPassword,
  useTopics,
} from "./useSettings";

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

describe("useSettings internals", () => {
  beforeEach(() => {
    window.localStorage.clear();
    vi.clearAllMocks();
  });

  it("readLocalStorage returns fallback when missing or corrupt, and parses valid JSON", () => {
    expect(__settingsInternal.readLocalStorage("missing", { ok: true })).toEqual({ ok: true });

    window.localStorage.setItem("key", JSON.stringify({ value: 123 }));
    expect(__settingsInternal.readLocalStorage("key", { value: 0 })).toEqual({ value: 123 });

    window.localStorage.setItem("bad", "{not-json");
    expect(__settingsInternal.readLocalStorage("bad", ["fallback"])).toEqual(["fallback"]);
  });

  it("writeLocalStorage writes JSON and tolerates setItem failures", () => {
    __settingsInternal.writeLocalStorage("w-key", { a: 1 });
    expect(window.localStorage.getItem("w-key")).toBe("{\"a\":1}");

    const setItemSpy = vi
      .spyOn(Storage.prototype, "setItem")
      .mockImplementation(() => {
        throw new Error("quota exceeded");
      });
    expect(() => __settingsInternal.writeLocalStorage("w-key", { b: 2 })).not.toThrow();
    setItemSpy.mockRestore();
  });

  it("resolveMcpConfig supports nested and flat mcp values", () => {
    const nested = __settingsInternal.resolveMcpConfig({
      mcp: {
        enabled: true,
        transport: "both",
        port: 9100,
        requireAuth: false,
        allowedOrigins: ["https://example.com"],
        tools: { cordum_submit_job: { enabled: false } },
      },
    });
    expect(nested).toMatchObject({
      enabled: true,
      transport: "both",
      port: 9100,
      requireAuth: false,
      allowedOrigins: ["https://example.com"],
    });
    expect(nested.tools.cordum_submit_job?.enabled).toBe(false);

    const flat = __settingsInternal.resolveMcpConfig({
      "mcp.enabled": "true",
      "mcp.transport": "stdio",
      "mcp.port": "7001",
      "mcp.require_auth": "false",
      "mcp.allowed_origins": ["*"],
    });
    expect(flat).toMatchObject({
      enabled: true,
      transport: "stdio",
      port: 7001,
      requireAuth: false,
      allowedOrigins: ["*"],
    });
  });
});

describe("useSettings hooks", () => {
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

  it("useTopics merges flat topics and pools.topics with dedupe", async () => {
    mockFetch([
      {
        match: "/config",
        method: "GET",
        body: {
          topics: { "job.default": "pool-a", "job.other": "pool-b" },
          pools: {
            "pool-a": { topics: ["job.default", "job.extra"] },
          },
        },
      },
    ]);
    const hook = renderWithQueryClient(() => useTopics());

    await hook.waitFor(() => {
      expect(hook.result.current?.length).toBe(3);
    });
    const topics = hook.result.current ?? [];
    expect(topics.map((s) => s.value)).toEqual([
      "job.default",
      "job.other",
      "job.extra",
    ]);
    hook.unmount();
  });

  it("useGeneralConfig returns defaults when config is null", async () => {
    mockFetch([{ match: "/config", method: "GET", body: null }]);

    const hook = renderWithQueryClient(() => useGeneralConfig());
    await hook.waitFor(() => {
      expect(hook.result.current?.data).toEqual(__settingsInternal.GENERAL_CONFIG_DEFAULTS);
    });
    hook.unmount();
  });

  it("useGeneralConfig merges partial config with defaults", async () => {
    mockFetch([
      { match: "/config", method: "GET", body: { safetyStance: "strict", rateLimitPerKey: 42 } },
    ]);

    const hook = renderWithQueryClient(() => useGeneralConfig());
    await hook.waitFor(() => {
      expect(hook.result.current?.data?.safetyStance).toBe("strict");
    });
    expect(hook.result.current?.data?.rateLimitPerKey).toBe(42);
    expect(hook.result.current?.data?.approvalTimeoutMs).toBe(
      __settingsInternal.GENERAL_CONFIG_DEFAULTS.approvalTimeoutMs,
    );
    hook.unmount();
  });

  it("useEnvironments returns default when config is absent", async () => {
    mockFetch([{ match: "/config", method: "GET", body: null }]);

    const hook = renderWithQueryClient(() => useEnvironments());
    await hook.waitFor(() => {
      expect(hook.result.current?.data).toEqual([__settingsInternal.DEFAULT_ENVIRONMENT]);
    });
    hook.unmount();
  });

  it("useEnvironments returns configured environments from config", async () => {
    mockFetch([
      {
        match: "/config",
        method: "GET",
        body: { environments: [{ id: "staging", name: "staging", status: "active", config: {} }] },
      },
    ]);

    const hook = renderWithQueryClient(() => useEnvironments());
    await hook.waitFor(() => {
      expect(hook.result.current?.data?.[0].id).toBe("staging");
    });
    hook.unmount();
  });

  it("useNotificationChannels prefers config channels, falls back to localStorage", async () => {
    window.localStorage.setItem(
      "cordum-notification-channels",
      JSON.stringify([{ id: "ls-1", name: "Local", type: "email", config: {}, enabled: true }]),
    );
    mockFetch([
      {
        match: "/config",
        method: "GET",
        body: { notifications: { channels: [{ id: "cfg-1", name: "Config", type: "slack", config: {}, enabled: true }] } },
      },
      {
        match: "/config",
        method: "GET",
        body: { notifications: { channels: [] } },
      },
    ]);

    const fromConfig = renderWithQueryClient(() => useNotificationChannels());
    await fromConfig.waitFor(() => {
      expect(fromConfig.result.current?.data?.[0].id).toBe("cfg-1");
    });
    fromConfig.unmount();

    const fromLocalStorage = renderWithQueryClient(() => useNotificationChannels());
    await fromLocalStorage.waitFor(() => {
      expect(fromLocalStorage.result.current?.data?.[0].id).toBe("ls-1");
    });
    fromLocalStorage.unmount();
  });

  it("useSetConfig posts /config and invalidates query on success", async () => {
    const fetchSpy = mockFetch([{ match: "/config", method: "POST", body: {} }]);
    const queryClient = createTestQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    const hook = renderWithQueryClient(() => useSetConfig(), queryClient);

    await act(async () => {
      await hook.result.current?.mutateAsync({ rateLimitPerKey: 700 });
    });

    expect(fetchSpy).toHaveBeenCalledTimes(1);
    const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit];
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({ rateLimitPerKey: 700 });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["config"] });
    expect(addToastMock).toHaveBeenCalledWith({ type: "success", title: "Settings saved" });
    hook.unmount();
  });

  it("useCreateApiKey and useCreateUser call expected endpoints and show toasts", async () => {
    const fetchSpy = mockFetch([
      { match: "/auth/keys", method: "POST", body: { key: { id: "k1" }, secret: "secret" } },
      { match: "/users", method: "POST", body: { id: "u1", username: "alice" } },
    ]);

    const keyHook = renderWithQueryClient(() => useCreateApiKey());
    await act(async () => {
      await keyHook.result.current?.mutateAsync({
        name: "CI",
        scopes: ["jobs:read"],
      });
    });
    keyHook.unmount();

    const userHook = renderWithQueryClient(() => useCreateUser());
    await act(async () => {
      await userHook.result.current?.mutateAsync({
        username: "alice",
        password: "secret",
        role: "admin",
      });
    });
    userHook.unmount();

    expect(fetchSpy).toHaveBeenCalledTimes(2);
    expect(addToastMock).toHaveBeenCalledWith({ type: "success", title: "API key created" });
    expect(addToastMock).toHaveBeenCalledWith({ type: "success", title: "User created" });
  });

  it("useChangePassword and useResetUserPassword call expected endpoints", async () => {
    const fetchSpy = mockFetch([
      { match: "/auth/password", method: "POST", body: {} },
      { match: "/users/u1/password", method: "POST", body: {} },
    ]);

    const changeHook = renderWithQueryClient(() => useChangePassword());
    await act(async () => {
      await changeHook.result.current?.mutateAsync({
        current_password: "old-password",
        new_password: "N3w-Password-Value!",
      });
    });
    changeHook.unmount();

    const resetHook = renderWithQueryClient(() => useResetUserPassword());
    await act(async () => {
      await resetHook.result.current?.mutateAsync({
        userId: "u1",
        password: "R3set-Password-Value!",
      });
    });
    resetHook.unmount();

    expect(fetchSpy).toHaveBeenCalledTimes(2);

    const [, changeInit] = fetchSpy.mock.calls[0] as [string, RequestInit];
    expect(changeInit.method).toBe("POST");
    expect(JSON.parse(String(changeInit.body))).toEqual({
      current_password: "old-password",
      new_password: "N3w-Password-Value!",
    });

    const [, resetInit] = fetchSpy.mock.calls[1] as [string, RequestInit];
    expect(resetInit.method).toBe("POST");
    expect(JSON.parse(String(resetInit.body))).toEqual({
      password: "R3set-Password-Value!",
    });
  });

  it("mutation hooks show error toasts on API errors", async () => {
    mockFetch([
      { match: "/auth/keys", method: "POST", rejectWith: new Error("create key failed") },
      { match: "/users", method: "POST", rejectWith: new Error("create user failed") },
    ]);

    const keyHook = renderWithQueryClient(() => useCreateApiKey());
    await expect(
      keyHook.result.current?.mutateAsync({ name: "CI", scopes: ["jobs:read"] }),
    ).rejects.toThrow("create key failed");
    keyHook.unmount();

    const userHook = renderWithQueryClient(() => useCreateUser());
    await expect(
      userHook.result.current?.mutateAsync({
        username: "alice",
        password: "secret",
        role: "admin",
      }),
    ).rejects.toThrow("create user failed");
    userHook.unmount();

    expect(addToastMock).toHaveBeenCalledWith({
      type: "error",
      title: "Failed to create API key",
      description: "create key failed",
    });
    expect(addToastMock).toHaveBeenCalledWith({
      type: "error",
      title: "Failed to create user",
      description: "create user failed",
    });
  });

  it("useMcpConfig maps mcp defaults and overrides", async () => {
    mockFetch([
      {
        match: "/config",
        method: "GET",
        body: {
          mcp: {
            enabled: true,
            transport: "both",
            port: 4000,
            requireAuth: false,
            allowedOrigins: ["https://claude.ai"],
            tools: { cordum_submit_job: { enabled: false } },
          },
        },
      },
    ]);

    const hook = renderWithQueryClient(() => useMcpConfig());
    await hook.waitFor(() => {
      expect(hook.result.current?.data.enabled).toBe(true);
    });
    expect(hook.result.current?.data).toMatchObject({
      enabled: true,
      transport: "both",
      port: 4000,
      requireAuth: false,
      allowedOrigins: ["https://claude.ai"],
    });
    expect(hook.result.current?.data.tools.cordum_submit_job?.enabled).toBe(false);
    hook.unmount();
  });

  it("useSetMcpConfig writes merged mcp payload through /config", async () => {
    const fetchSpy = mockFetch([
      {
        match: "/config",
        method: "GET",
        body: {
          mcp: {
            enabled: true,
            transport: "http",
            port: 3001,
            requireAuth: true,
            allowedOrigins: ["*"],
            tools: { cordum_submit_job: { enabled: true } },
            resources: { jobs: { enabled: true } },
          },
        },
      },
      { match: "/config", method: "POST", body: {} },
    ]);

    const hook = renderWithQueryClient(() => useSetMcpConfig());
    await hook.waitFor(() => {
      expect(hook.result.current).toBeDefined();
    });
    await act(async () => {
      await hook.result.current?.mutateAsync({
        transport: "both",
        tools: { cordum_submit_job: { enabled: false } },
      });
    });

    const postCall = fetchSpy.mock.calls.find((call) => {
      const [, init] = call as [string, RequestInit];
      return (init.method ?? "").toUpperCase() === "POST";
    });
    expect(postCall).toBeTruthy();
    const [, init] = postCall as [string, RequestInit];
    const payload = JSON.parse(String(init.body)) as Record<string, unknown>;
    const mcp = payload.mcp as Record<string, unknown>;
    expect(mcp.transport).toBe("both");
    expect(mcp.port).toBe(3001);
    expect((mcp.tools as Record<string, unknown>).cordum_submit_job).toEqual({ enabled: false });
    hook.unmount();
  });

  it("useMcpStatus maps success response", async () => {
    mockFetch([
      {
        match: "/config",
        method: "GET",
        body: {
          mcp: {
            enabled: true,
          },
        },
      },
      {
        match: "/mcp/status",
        method: "GET",
        body: {
          running: true,
          connected_clients: 4,
          uptime_seconds: 120,
          transport: "http",
          enabled_tools: 6,
          enabled_resources: 5,
        },
      },
    ]);

    const hook = renderWithQueryClient(() => useMcpStatus());
    await hook.waitFor(() => {
      expect(hook.result.current?.data?.running).toBe(true);
    });
    expect(hook.result.current?.data).toMatchObject({
      running: true,
      connectedClients: 4,
      uptime: 120,
      transport: "http",
      enabledTools: 6,
      enabledResources: 5,
    });
    hook.unmount();
  });

  it("useMcpStatus falls back to stopped state on 404", async () => {
    mockFetch([
      {
        match: "/config",
        method: "GET",
        body: {
          mcp: {
            enabled: true,
          },
        },
      },
      {
        match: "/mcp/status",
        method: "GET",
        status: 404,
        body: { error: "not found" },
      },
    ]);

    const fallbackHook = renderWithQueryClient(() => useMcpStatus());
    await fallbackHook.waitFor(() => {
      expect(fallbackHook.result.current?.data?.running).toBe(false);
    });
    expect(fallbackHook.result.current?.data).toMatchObject({
      running: false,
      connectedClients: 0,
      uptime: 0,
    });
    fallbackHook.unmount();
  });

  it("useMcpStatus skips MCP status call when MCP is disabled", async () => {
    const fetchSpy = mockFetch([
      {
        match: "/config",
        method: "GET",
        body: {
          mcp: {
            enabled: false,
          },
        },
      },
    ]);

    const hook = renderWithQueryClient(() => useMcpStatus());
    await hook.waitFor(() => {
      expect(hook.result.current?.data?.running).toBe(false);
    });
    expect(fetchSpy.mock.calls.some(([url]) => String(url).includes("/mcp/status"))).toBe(false);
    hook.unmount();
  });

  it("useMcpTools and useMcpResources derive toggle state from config", async () => {
    mockFetch([
      {
        match: "/config",
        method: "GET",
        body: {
          mcp: {
            tools: {
              cordum_submit_job: { enabled: false },
              cordum_cancel_job: { enabled: true },
            },
            resources: {
              jobs: { enabled: false },
              policies: { enabled: true },
            },
          },
        },
      },
      {
        match: "/config",
        method: "GET",
        body: {
          mcp: {
            tools: {
              cordum_submit_job: { enabled: false },
              cordum_cancel_job: { enabled: true },
            },
            resources: {
              jobs: { enabled: false },
              policies: { enabled: true },
            },
          },
        },
      },
    ]);

    const toolsHook = renderWithQueryClient(() => useMcpTools());
    await toolsHook.waitFor(() => {
      const submitTool = toolsHook.result.current?.data?.find((item) => item.name === "cordum_submit_job");
      expect(submitTool?.enabled).toBe(false);
    });
    toolsHook.unmount();

    const resourcesHook = renderWithQueryClient(() => useMcpResources());
    await resourcesHook.waitFor(() => {
      const jobsResource = resourcesHook.result.current?.data?.find((item) => item.name === "Jobs");
      expect(jobsResource?.enabled).toBe(false);
    });
    resourcesHook.unmount();
  });
});

