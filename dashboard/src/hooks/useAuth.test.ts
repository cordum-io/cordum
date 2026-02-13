import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithQueryClient } from "./__tests__/test-utils";
import { useAuth, useRequireAuth } from "./useAuth";

const { navigateMock, locationState, configState } = vi.hoisted(() => ({
  navigateMock: vi.fn(),
  locationState: { pathname: "/jobs", search: "?q=1" },
  configState: {
    apiKey: "token-1",
    user: {
      id: "u1",
      username: "alice",
      email: "alice@example.com",
      display_name: "Alice",
      roles: ["admin"],
      tenant: "tenant-1",
    },
    isAuthenticated: true,
    login: vi.fn(),
    logout: vi.fn(),
    tenantId: "tenant-1",
    principalId: "u1",
  },
}));

vi.mock("react-router-dom", () => ({
  useNavigate: () => navigateMock,
  useLocation: () => locationState,
}));

vi.mock("../state/config", () => ({
  useConfigStore: (selector: (state: typeof configState) => unknown) => selector(configState),
}));

describe("useAuth", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    locationState.pathname = "/jobs";
    locationState.search = "?q=1";
    configState.apiKey = "token-1";
    configState.isAuthenticated = true;
    configState.tenantId = "tenant-1";
    configState.principalId = "u1";
    configState.user = {
      id: "u1",
      username: "alice",
      email: "alice@example.com",
      display_name: "Alice",
      roles: ["admin"],
      tenant: "tenant-1",
    };
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("returns auth state and actions from config store", () => {
    const hook = renderWithQueryClient(() => useAuth());

    expect(hook.result.current).toMatchObject({
      token: "token-1",
      user: configState.user,
      isAuthenticated: true,
      tenantId: "tenant-1",
      principalId: "u1",
    });
    expect(hook.result.current?.login).toBe(configState.login);
    expect(hook.result.current?.logout).toBe(configState.logout);

    hook.unmount();
  });

  it("useRequireAuth navigates to login with encoded returnUrl when unauthenticated", async () => {
    configState.isAuthenticated = false;
    locationState.pathname = "/jobs";
    locationState.search = "?q=1";

    const hook = renderWithQueryClient(() => useRequireAuth());

    await hook.waitFor(() => {
      expect(navigateMock).toHaveBeenCalledWith("/login?returnUrl=%2Fjobs%3Fq%3D1", {
        replace: true,
      });
    });
    expect(hook.result.current).toBe(false);

    hook.unmount();
  });

  it("useRequireAuth returns true and does not navigate when authenticated", async () => {
    configState.isAuthenticated = true;

    const hook = renderWithQueryClient(() => useRequireAuth());

    await hook.waitFor(() => {
      expect(hook.result.current).toBe(true);
    });
    expect(navigateMock).not.toHaveBeenCalled();

    hook.unmount();
  });
});
