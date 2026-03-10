import { describe, expect, it } from "vitest";
import { derivePolicyAccess } from "@/hooks/usePolicyAccess";

describe("QuarantinePage RBAC contract", () => {
  it("grants release access to operator and secops roles", () => {
    for (const role of ["operator", "secops", "admin", "owner"]) {
      const access = derivePolicyAccess({
        requiresAuth: true,
        roles: [role],
        principalRole: role,
      });
      expect(access.canRelease).toBe(true);
    }
  });

  it("grants release access to release_manager without edit/publish", () => {
    const access = derivePolicyAccess({
      requiresAuth: true,
      roles: ["release_manager"],
      principalRole: "release_manager",
    });
    expect(access.canRelease).toBe(true);
    expect(access.canEdit).toBe(false);
    expect(access.canPublish).toBe(false);
  });

  it("denies release access to viewer, auditor, editor, and publisher roles", () => {
    for (const role of ["viewer", "auditor", "editor", "publisher"]) {
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
