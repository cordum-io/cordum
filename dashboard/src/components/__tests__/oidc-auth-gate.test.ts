import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { QueryClientProvider, type QueryClient } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AuthConfig } from "../../api/types";
import { createTestQueryClient, renderWithQueryClient } from "../../hooks/__tests__/test-utils";
import { usePermission } from "../../hooks/usePermission";
import { ProtectedRoute } from "../ProtectedRoute";

const { navigateMock, locationState, configState, authConfigState } = vi.hoisted(() => ({
  navigateMock: vi.fn(),
  locationState: { pathname: "/secure", search: "" },
  configState: {
    isAuthenticated: false,
    logout: vi.fn(),
    user: {
      id: "u1",
      username: "alice",
      email: "alice@example.com",
      display_name: "Alice",
      roles: ["viewer"],
      tenant: "tenant-1",
    },
    principalRole: "viewer",
  },
  authConfigState: {
    data: undefined as AuthConfig | undefined,
  },
}));

vi.mock("react-router-dom", async () => {
  const actual = await vi.importActual<typeof import("react-router-dom")>("react-router-dom");
  return {
    ...actual,
    useNavigate: () => navigateMock,
    useLocation: () => locationState,
  };
});

vi.mock("../../state/config", () => ({
  useConfigStore: (selector: (state: typeof configState) => unknown) => selector(configState),
}));

vi.mock("../../hooks/useAuthConfig", () => ({
  useAuthConfig: () => ({ data: authConfigState.data, isLoading: false }),
}));

vi.mock("../../hooks/useEventStream", () => ({
  useEventStream: () => undefined,
}));

vi.mock("../../hooks/useKeyboardShortcuts", () => ({
  useKeyboardShortcuts: () => undefined,
}));

vi.mock("../../hooks/useCrossTabSync", () => ({
  useCrossTabSync: () => undefined,
}));

vi.mock("../layout/AppShell", () => ({
  AppShell: ({ children }: { children: React.ReactNode }) =>
    React.createElement(React.Fragment, null, children),
}));

vi.mock("../CommandPalette", () => ({
  CommandPalette: () => null,
}));

vi.mock("../KeyboardShortcutsHelp", () => ({
  KeyboardShortcutsHelp: () => null,
}));

vi.mock("../SessionTimeoutWarning", () => ({
  SessionTimeoutWarning: () => null,
}));

const oidcOnlyAuthConfig: AuthConfig = {
  password_enabled: false,
  user_auth_enabled: false,
  saml_enabled: false,
  oidc_enabled: true,
  default_tenant: "tenant-1",
};

let container: HTMLDivElement;
let root: Root;
let queryClient: QueryClient;

async function waitFor(assertion: () => void, timeoutMs = 2000): Promise<void> {
  const start = Date.now();
  while (true) {
    try {
      assertion();
      return;
    } catch (error) {
      if (Date.now() - start >= timeoutMs) {
        throw error;
      }
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 10));
      });
    }
  }
}

function renderProtectedRoute() {
  act(() => {
    root.render(
      React.createElement(
        QueryClientProvider,
        { client: queryClient },
        React.createElement(
          ProtectedRoute,
          { children: React.createElement("div", null, "Secret content") },
        ),
      ),
    );
  });
}

describe("OIDC auth gating", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
    queryClient = createTestQueryClient();

    locationState.pathname = "/secure";
    locationState.search = "";
    configState.isAuthenticated = false;
    configState.user.roles = ["viewer"];
    configState.principalRole = "viewer";
    authConfigState.data = undefined;
  });

  afterEach(() => {
    act(() => {
      root.unmount();
    });
    container.remove();
    queryClient.clear();
  });

  it("redirects unauthenticated users to login when only OIDC is enabled", async () => {
    authConfigState.data = { ...oidcOnlyAuthConfig };
    locationState.search = "?tab=events";

    renderProtectedRoute();

    await waitFor(() => {
      expect(navigateMock).toHaveBeenCalledWith("/login?returnUrl=%2Fsecure%3Ftab%3Devents", {
        replace: true,
      });
    });
  });

  it("denies permission checks when only OIDC is enabled and role is missing", () => {
    authConfigState.data = { ...oidcOnlyAuthConfig };

    const hook = renderWithQueryClient(() => usePermission(["admin"]));

    expect(hook.result.current).toEqual({ allowed: false, userRoles: ["viewer"] });
    hook.unmount();
  });
});
