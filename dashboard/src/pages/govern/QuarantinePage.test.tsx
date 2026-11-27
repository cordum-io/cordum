import { describe, expect, it } from "vitest";
import { derivePolicyAccess } from "@/hooks/usePolicyAccess";

describe("QuarantinePage RBAC contract", () => {
  it("grants release access to the admin role", () => {
    const access = derivePolicyAccess({
      requiresAuth: true,
      roles: ["admin"],
      principalRole: "admin",
    });
    expect(access.canRelease).toBe(true);
  });

  it("denies release access to every role other than admin", () => {
    for (const role of [
      "operator",
      "secops",
      "owner",
      "release_manager",
      "viewer",
      "auditor",
      "editor",
      "publisher",
    ]) {
      const access = derivePolicyAccess({
        requiresAuth: true,
        roles: [role],
        principalRole: role,
      });
      expect(access.canRelease).toBe(false);
    }
  });

  it("grants full release access when auth is disabled", () => {
    const access = derivePolicyAccess({
      requiresAuth: false,
      roles: [],
    });
    expect(access.canRelease).toBe(true);
  });
});
