import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createTestQueryClient, mockFetch, renderWithQueryClient } from "./__tests__/test-utils";
import { useAuthConfig } from "./useAuthConfig";

const { loggerMock } = vi.hoisted(() => ({
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

vi.mock("../lib/logger", () => ({
  logger: loggerMock,
}));

describe("useAuthConfig", () => {
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

  it("fetches /auth/config and returns auth config", async () => {
    mockFetch([
      {
        match: "/auth/config",
        method: "GET",
        body: {
          password_enabled: true,
          saml_enabled: false,
          default_tenant: "default",
          require_rbac: true,
        },
      },
    ]);

    const queryClient = createTestQueryClient();
    const hook = renderWithQueryClient(() => useAuthConfig(), queryClient);

    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    expect(hook.result.current?.data).toMatchObject({
      password_enabled: true,
      saml_enabled: false,
      default_tenant: "default",
    });

    const query = queryClient.getQueryCache().find({ queryKey: ["auth-config"] });
    const options = query?.options as { staleTime?: number; retry?: number } | undefined;
    expect(options?.staleTime).toBe(5 * 60_000);
    expect(options?.retry).toBe(1);

    hook.unmount();
  });
});

