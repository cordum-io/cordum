import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithQueryClient } from "./__tests__/test-utils";
import { useIsAdmin, usePermission } from "./usePermission";
import type { AuthConfig } from "../api/types";

const { configState, authConfigState } = vi.hoisted(() => ({
  configState: {
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

vi.mock("../state/config", () => ({
  useConfigStore: (selector: (state: typeof configState) => unknown) => selector(configState),
}));

vi.mock("./useAuthConfig", () => ({
  useAuthConfig: () => ({ data: authConfigState.data }),
}));

describe("usePermission", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    configState.user = {
      id: "u1",
      username: "alice",
      email: "alice@example.com",
      display_name: "Alice",
      roles: ["viewer"],
      tenant: "tenant-1",
    };
    configState.principalRole = "viewer";
    authConfigState.data = undefined;
  });

  it("allows all when auth is not configured", () => {
    const hook = renderWithQueryClient(() => usePermission(["admin"]));

    expect(hook.result.current).toEqual({ allowed: true, userRoles: [] });
    hook.unmount();
  });

  it("allows when auth is enabled and user has required role", () => {
    authConfigState.data = {
      password_enabled: true,
      saml_enabled: false,
      default_tenant: "default",
    };
    configState.user.roles = ["admin", "viewer"];

    const hook = renderWithQueryClient(() => usePermission(["admin"]));

    expect(hook.result.current).toEqual({ allowed: true, userRoles: ["admin", "viewer"] });
    hook.unmount();
  });

  it("denies when auth is enabled and user lacks required role", () => {
    authConfigState.data = {
      password_enabled: true,
      saml_enabled: false,
      default_tenant: "default",
    };
    configState.user.roles = ["viewer"];
    configState.principalRole = "viewer";

    const hook = renderWithQueryClient(() => usePermission(["admin"]));

    expect(hook.result.current).toEqual({ allowed: false, userRoles: ["viewer"] });
    hook.unmount();
  });

  it("uses principalRole as fallback role source", () => {
    authConfigState.data = {
      password_enabled: true,
      saml_enabled: false,
      default_tenant: "default",
    };
    configState.user.roles = ["viewer"];
    configState.principalRole = "admin";

    const hook = renderWithQueryClient(() => usePermission(["admin"]));

    expect(hook.result.current).toEqual({ allowed: true, userRoles: ["viewer"] });
    hook.unmount();
  });

  it("useIsAdmin delegates to usePermission(['admin'])", () => {
    authConfigState.data = {
      password_enabled: true,
      saml_enabled: false,
      default_tenant: "default",
    };
    configState.user.roles = ["admin"];

    const hook = renderWithQueryClient(() => useIsAdmin());

    expect(hook.result.current).toBe(true);
    hook.unmount();
  });
});
