import { describe, expect, it } from "vitest";
import { derivePolicyAccess } from "@/hooks/usePolicyAccess";

describe("BundlePublishDialog contract", () => {
  it("opens the publish flow only to the admin role", () => {
    const viewer = derivePolicyAccess({
      requiresAuth: true,
      roles: ["viewer"],
      principalRole: "viewer",
    });
    expect(viewer.canPublish).toBe(false);

    const admin = derivePolicyAccess({
      requiresAuth: true,
      roles: ["admin"],
      principalRole: "admin",
    });
    expect(admin.canPublish).toBe(true);
  });

  it("gives edit and publish to the admin role and neither to the editor role", () => {
    const editor = derivePolicyAccess({
      requiresAuth: true,
      roles: ["editor"],
      principalRole: "editor",
    });
    expect(editor.canEdit).toBe(false);
    expect(editor.canPublish).toBe(false);

    const admin = derivePolicyAccess({
      requiresAuth: true,
      roles: ["admin"],
      principalRole: "admin",
    });
    expect(admin.canEdit).toBe(true);
    expect(admin.canPublish).toBe(true);
  });

  it("allows rollback only for the admin role", () => {
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
    expect(secops.canPublish).toBe(false);
    expect(secops.isReadOnly).toBe(false);
  });
});
