import { describe, expect, it } from "vitest";
import { derivePolicyAccess } from "@/hooks/usePolicyAccess";
import { APP_SHELL_NAV_SECTIONS } from "@/components/layout/AppShell";

describe("GOVERN navigation shape", () => {
  it("keeps the dark-launched Delegations nav hidden until the feature flag is enabled", () => {
    const governSection = APP_SHELL_NAV_SECTIONS.find((section) => section.label === "Govern");
    expect(governSection?.items.map((item) => item.label)).toEqual([
      "Policy Studio",
      "Quarantine",
      "Verification",
    ]);
  });
});

describe("policy access role boundaries for GOVERN shells", () => {
  it("hides edit/publish/release affordances for viewer roles", () => {
    const access = derivePolicyAccess({
      requiresAuth: true,
      roles: ["viewer"],
      principalRole: "viewer",
    });
    expect(access.isReadOnly).toBe(true);
    expect(access.canEdit).toBe(false);
    expect(access.canPublish).toBe(false);
    expect(access.canRelease).toBe(false);
  });

  it("enables edit/publish/release affordances for the admin role", () => {
    const access = derivePolicyAccess({
      requiresAuth: true,
      roles: ["admin"],
      principalRole: "admin",
    });
    expect(access.isReadOnly).toBe(false);
    expect(access.canEdit).toBe(true);
    expect(access.canPublish).toBe(true);
    expect(access.canRelease).toBe(true);
  });

  it("gracefully allows affordances when auth is disabled", () => {
    const access = derivePolicyAccess({
      requiresAuth: false,
      roles: [],
      principalRole: "",
    });
    expect(access.canEdit).toBe(true);
    expect(access.canPublish).toBe(true);
    expect(access.canRelease).toBe(true);
    expect(access.isReadOnly).toBe(false);
  });

  it.each(["publisher", "editor", "release_manager"])(
    "gives the %s role none of edit, publish or release",
    (role) => {
      const access = derivePolicyAccess({
        requiresAuth: true,
        roles: [role],
        principalRole: role,
      });
      expect(access.canEdit).toBe(false);
      expect(access.canPublish).toBe(false);
      expect(access.canRelease).toBe(false);
      expect(access.isReadOnly).toBe(false);
    },
  );

  it("shares output-rule and tenant management scope with edit capability, for the admin role only", () => {
    const admin = derivePolicyAccess({
      requiresAuth: true,
      roles: ["admin"],
      principalRole: "admin",
    });
    expect(admin.canManageOutputRules).toBe(true);
    expect(admin.canManageTenants).toBe(true);

    const editor = derivePolicyAccess({
      requiresAuth: true,
      roles: ["editor"],
      principalRole: "editor",
    });
    expect(editor.canManageOutputRules).toBe(false);
    expect(editor.canManageTenants).toBe(false);
  });
});
