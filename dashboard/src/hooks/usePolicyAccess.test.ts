import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithQueryClient } from "./__tests__/test-utils";
import { usePolicyAccess } from "./usePolicyAccess";
import type { AuthConfig } from "../api/types";

const { configState, authConfigState } = vi.hoisted(() => ({
  configState: {
    user: {
      id: "u1",
      username: "alice",
      email: "alice@example.com",
      display_name: "Alice",
      roles: ["viewer"] as string[],
      tenant: "tenant-1",
    },
    principalRole: "viewer" as string,
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

describe("usePolicyAccess", () => {
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

  it("denies every privileged capability while authConfig is undefined (loading or fetch error) — fail closed, even for an admin", () => {
    // Unconditional deny regardless of role, mirroring usePermission's tested
    // fail-closed contract: we don't yet know whether auth is required, so we
    // must not paint privileged affordances we'd have to retract.
    configState.user.roles = ["admin"];
    configState.principalRole = "admin";

    const hook = renderWithQueryClient(() => usePolicyAccess());

    expect(hook.result.current).toMatchObject({
      canEdit: false,
      canPublish: false,
      canRelease: false,
      canManageOutputRules: false,
      canManageTenants: false,
    });
    hook.unmount();
  });

  it("allows all capabilities when authConfig is resolved with every mode disabled (genuine no-auth deployment)", () => {
    authConfigState.data = {
      password_enabled: false,
      saml_enabled: false,
      default_tenant: "default",
    };

    const hook = renderWithQueryClient(() => usePolicyAccess());

    expect(hook.result.current).toMatchObject({
      canEdit: true,
      canPublish: true,
      canRelease: true,
      canManageOutputRules: true,
      canManageTenants: true,
    });
    hook.unmount();
  });

  it("derives caps from role when auth is enabled and the user is an admin", () => {
    authConfigState.data = {
      password_enabled: true,
      saml_enabled: false,
      default_tenant: "default",
    };
    configState.user.roles = ["admin"];
    configState.principalRole = "admin";

    const hook = renderWithQueryClient(() => usePolicyAccess());

    expect(hook.result.current).toMatchObject({
      canEdit: true,
      canPublish: true,
      canRelease: true,
      canManageOutputRules: true,
      canManageTenants: true,
      isReadOnly: false,
      requiresAuth: true,
    });
    hook.unmount();
  });

  it("denies caps and marks read-only when auth is enabled and the user is a viewer", () => {
    authConfigState.data = {
      password_enabled: true,
      saml_enabled: false,
      default_tenant: "default",
    };
    configState.user.roles = ["viewer"];
    configState.principalRole = "viewer";

    const hook = renderWithQueryClient(() => usePolicyAccess());

    expect(hook.result.current).toMatchObject({
      canEdit: false,
      canPublish: false,
      canRelease: false,
      canManageOutputRules: false,
      canManageTenants: false,
      isReadOnly: true,
      requiresAuth: true,
    });
    hook.unmount();
  });
});
