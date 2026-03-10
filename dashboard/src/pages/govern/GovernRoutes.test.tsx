import { describe, expect, it } from "vitest";
import { LEGACY_POLICY_ROUTE_REDIRECTS } from "@/App";
import { derivePolicyAccess } from "@/hooks/usePolicyAccess";

describe("GOVERN legacy redirect contract", () => {
  it("maps major legacy policy paths to the expected GOVERN destinations", () => {
    expect(LEGACY_POLICY_ROUTE_REDIRECTS.input).toBe("/govern/input-rules");
    expect(LEGACY_POLICY_ROUTE_REDIRECTS.output).toBe("/govern/output-rules");
    expect(LEGACY_POLICY_ROUTE_REDIRECTS.tenants).toBe("/govern/tenants");
    expect(LEGACY_POLICY_ROUTE_REDIRECTS.bundles).toBe("/govern/bundles");
    expect(LEGACY_POLICY_ROUTE_REDIRECTS.simulator).toBe("/govern/simulator");
    expect(LEGACY_POLICY_ROUTE_REDIRECTS.quarantine).toBe("/govern/quarantine");
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

  it("enables edit/publish/release affordances for operator roles", () => {
    const access = derivePolicyAccess({
      requiresAuth: true,
      roles: ["operator"],
      principalRole: "operator",
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

  it("enforces publish/release boundary: publisher can publish but not edit or release", () => {
    const access = derivePolicyAccess({
      requiresAuth: true,
      roles: ["publisher"],
      principalRole: "publisher",
    });
    expect(access.canPublish).toBe(true);
    expect(access.canEdit).toBe(false);
    expect(access.canRelease).toBe(false);
    expect(access.isReadOnly).toBe(false);
  });

  it("enforces publish/release boundary: editor can edit but not publish or release", () => {
    const access = derivePolicyAccess({
      requiresAuth: true,
      roles: ["editor"],
      principalRole: "editor",
    });
    expect(access.canEdit).toBe(true);
    expect(access.canPublish).toBe(false);
    expect(access.canRelease).toBe(false);
  });

  it("enforces publish/release boundary: release_manager can release but not edit or publish", () => {
    const access = derivePolicyAccess({
      requiresAuth: true,
      roles: ["release_manager"],
      principalRole: "release_manager",
    });
    expect(access.canRelease).toBe(true);
    expect(access.canEdit).toBe(false);
    expect(access.canPublish).toBe(false);
  });

  it("shares output-rule and tenant management scope with edit capability", () => {
    const editor = derivePolicyAccess({
      requiresAuth: true,
      roles: ["editor"],
      principalRole: "editor",
    });
    expect(editor.canManageOutputRules).toBe(true);
    expect(editor.canManageTenants).toBe(true);

    const publisher = derivePolicyAccess({
      requiresAuth: true,
      roles: ["publisher"],
      principalRole: "publisher",
    });
    expect(publisher.canManageOutputRules).toBe(false);
    expect(publisher.canManageTenants).toBe(false);
  });
});
