import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createTestQueryClient, mockFetch, renderWithQueryClient } from "./__tests__/test-utils";
import { useSCIMConfig, useRotateSCIMToken } from "./useSCIMConfig";

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

describe("useSCIMConfig", () => {
  beforeEach(() => {
    window.localStorage.clear();
    vi.clearAllMocks();
    mockConfigState.apiBaseUrl = "/api/v1";
    mockConfigState.apiKey = "";
    mockConfigState.tenantId = "";
    mockConfigState.principalId = "";
    mockConfigState.principalRole = "";
    mockConfigState.user = null;
    vi.spyOn(globalThis.crypto, "randomUUID").mockReturnValue("00000000-0000-0000-0000-000000000789");
    vi.spyOn(performance, "now").mockReturnValue(100);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("loads SCIM settings from the gateway", async () => {
    mockFetch([
      {
        match: "/scim/settings",
        method: "GET",
        body: {
          entitled: true,
          configured: true,
          endpointUrl: "https://gateway.cordum.test/api/v1/scim/v2/Users",
          bearerToken: "scim-secret-token",
          bearerTokenMasked: "scim-sec********oken",
          tokenManagedBy: "redis",
          users: [
            {
              id: "user-1",
              userName: "alice@example.com",
              displayName: "Alice Example",
              email: "alice@example.com",
              source: "scim",
              active: true,
              syncedAt: "2026-04-11T12:00:00Z",
            },
          ],
        },
      },
    ]);

    const hook = renderWithQueryClient(() => useSCIMConfig(), createTestQueryClient());

    try {
      await hook.waitFor(() => {
        expect(hook.result.current?.isSuccess).toBe(true);
      });

      expect(hook.result.current?.data).toMatchObject({
        entitled: true,
        configured: true,
        endpointUrl: "https://gateway.cordum.test/api/v1/scim/v2/Users",
        bearerToken: "scim-secret-token",
        bearerTokenMasked: "scim-sec********oken",
        tokenManagedBy: "redis",
        users: [
          {
            id: "user-1",
            userName: "alice@example.com",
            source: "scim",
            active: true,
          },
        ],
      });
    } finally {
      hook.unmount();
    }
  });

  it("posts to rotate the SCIM token and invalidates settings", async () => {
    const fetchSpy = mockFetch([
      {
        match: "/scim/settings/token",
        method: "POST",
        body: {
          bearerToken: "scim-rotated-token",
          bearerTokenMasked: "scim-rot********oken",
          tokenManagedBy: "redis",
        },
      },
    ]);

    const queryClient = createTestQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    const hook = renderWithQueryClient(() => useRotateSCIMToken(), queryClient);

    try {
      hook.result.current?.mutate();

      await hook.waitFor(() => {
        expect(hook.result.current?.isSuccess).toBe(true);
      });

      expect(fetchSpy).toHaveBeenCalledWith(
        expect.stringContaining("/api/v1/scim/settings/token"),
        expect.objectContaining({ method: "POST" }),
      );
      expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["scim-settings"] });
    } finally {
      hook.unmount();
    }
  });
});
