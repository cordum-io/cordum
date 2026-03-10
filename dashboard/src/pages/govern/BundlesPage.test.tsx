import { describe, expect, it } from "vitest";
import { derivePolicyAccess } from "@/hooks/usePolicyAccess";
import { encodePolicyBundleId } from "@/hooks/usePolicies";
import {
  BUNDLES_PAGE_SECTIONS,
  getBundleStatusVariant,
  getBundleAffordances,
} from "./BundlesPage";

describe("BundlesPage extraction contract", () => {
  it("keeps page sections focused on bundle list responsibilities", () => {
    expect(BUNDLES_PAGE_SECTIONS).toEqual([
      "bundle-summary-cards",
      "bundle-list",
    ]);
    expect(BUNDLES_PAGE_SECTIONS).not.toContain("simulator");
    expect(BUNDLES_PAGE_SECTIONS).not.toContain("tenant-policy");
    expect(BUNDLES_PAGE_SECTIONS).not.toContain("output-rules");
  });

  it("maps bundle status to correct badge variant", () => {
    expect(getBundleStatusVariant("published")).toBe("healthy");
    expect(getBundleStatusVariant("Published")).toBe("healthy");
    expect(getBundleStatusVariant("draft")).toBe("warning");
    expect(getBundleStatusVariant("Draft")).toBe("warning");
    expect(getBundleStatusVariant("archived")).toBe("muted");
    expect(getBundleStatusVariant(undefined)).toBe("muted");
    expect(getBundleStatusVariant("")).toBe("muted");
  });

  it("provides correct affordances for publisher vs viewer", () => {
    const publisherAffordances = getBundleAffordances(true);
    expect(publisherAffordances).toEqual({
      canManageBundle: true,
      canViewBundle: true,
      actionLabel: "Manage bundle",
    });

    const viewerAffordances = getBundleAffordances(false);
    expect(viewerAffordances).toEqual({
      canManageBundle: false,
      canViewBundle: true,
      actionLabel: "View bundle",
    });
  });

  it("encodes bundle IDs with slashes for URL safety", () => {
    expect(encodePolicyBundleId("default")).toBe("default");
    expect(encodePolicyBundleId("secops/prod-bundle")).toBe("secops~prod-bundle");
    expect(encodePolicyBundleId("a/b/c")).toBe("a~b~c");
  });

  it("enforces publish affordances by role through usePolicyAccess", () => {
    const viewer = derivePolicyAccess({
      requiresAuth: true,
      roles: ["viewer"],
      principalRole: "viewer",
    });
    expect(viewer.canPublish).toBe(false);
    expect(viewer.canEdit).toBe(false);
    expect(viewer.isReadOnly).toBe(true);

    const operator = derivePolicyAccess({
      requiresAuth: true,
      roles: ["operator"],
      principalRole: "operator",
    });
    expect(operator.canPublish).toBe(true);
    expect(operator.canEdit).toBe(true);
    expect(operator.isReadOnly).toBe(false);

    const secops = derivePolicyAccess({
      requiresAuth: true,
      roles: ["secops"],
      principalRole: "secops",
    });
    expect(secops.canPublish).toBe(true);
    expect(secops.canEdit).toBe(true);
  });

  it("grants full access when auth is not required", () => {
    const noAuth = derivePolicyAccess({
      requiresAuth: false,
      roles: [],
    });
    expect(noAuth.canPublish).toBe(true);
    expect(noAuth.canEdit).toBe(true);
    expect(noAuth.isReadOnly).toBe(false);
  });
});
