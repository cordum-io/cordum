import { describe, expect, it } from "vitest";
import { derivePolicyAccess } from "@/hooks/usePolicyAccess";

describe("BundlePublishDialog contract", () => {
  it("requires publish role to access publish flow", () => {
    const viewer = derivePolicyAccess({
      requiresAuth: true,
      roles: ["viewer"],
      principalRole: "viewer",
    });
    expect(viewer.canPublish).toBe(false);

    const publisher = derivePolicyAccess({
      requiresAuth: true,
      roles: ["publisher"],
      principalRole: "publisher",
    });
    expect(publisher.canPublish).toBe(true);
  });

  it("distinguishes edit from publish roles", () => {
    const editor = derivePolicyAccess({
      requiresAuth: true,
      roles: ["editor"],
      principalRole: "editor",
    });
    expect(editor.canEdit).toBe(true);
    expect(editor.canPublish).toBe(false);

    const admin = derivePolicyAccess({
      requiresAuth: true,
      roles: ["admin"],
      principalRole: "admin",
    });
    expect(admin.canEdit).toBe(true);
    expect(admin.canPublish).toBe(true);
  });

  it("allows rollback only for publish-capable roles", () => {
    const auditor = derivePolicyAccess({
      requiresAuth: true,
      roles: ["auditor"],
      principalRole: "auditor",
    });
    expect(auditor.canPublish).toBe(false);
    expect(auditor.isReadOnly).toBe(true);

    const secops = derivePolicyAccess({
      requiresAuth: true,
      roles: ["secops"],
      principalRole: "secops",
    });
    expect(secops.canPublish).toBe(true);
    expect(secops.isReadOnly).toBe(false);
  });
});
