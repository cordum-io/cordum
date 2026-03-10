import { describe, expect, it } from "vitest";
import { derivePolicyAccess } from "@/hooks/usePolicyAccess";
import {
  BUNDLE_DETAIL_TABS,
  decodeBundleId,
  validateBundleYaml,
} from "./BundleDetailPage";

describe("BundleDetailPage extraction contract", () => {
  it("provides four dedicated tabs for bundle lifecycle", () => {
    expect(BUNDLE_DETAIL_TABS).toEqual(["yaml", "preview", "diff", "history"]);
    expect(BUNDLE_DETAIL_TABS).not.toContain("simulator");
    expect(BUNDLE_DETAIL_TABS).not.toContain("rules");
  });

  it("decodes tilde-encoded bundle IDs back to slash form", () => {
    expect(decodeBundleId("default")).toBe("default");
    expect(decodeBundleId("secops~prod-bundle")).toBe("secops/prod-bundle");
    expect(decodeBundleId("a~b~c")).toBe("a/b/c");
    expect(decodeBundleId("already%2Fencoded")).toBe("already/encoded");
  });

  it("returns no issues for valid YAML", () => {
    const issues = validateBundleYaml("rules:\n  - id: test\n    decision: allow\n");
    expect(issues).toHaveLength(0);
  });

  it("returns no issues for empty content", () => {
    expect(validateBundleYaml("")).toHaveLength(0);
    expect(validateBundleYaml("   ")).toHaveLength(0);
  });

  it("returns error for malformed YAML", () => {
    const issues = validateBundleYaml("rules:\n  - id: test\n  bad indent");
    expect(issues.length).toBeGreaterThan(0);
    expect(issues[0].severity).toBe("error");
  });

  it("returns error for deeply malformed YAML", () => {
    const issues = validateBundleYaml("key: [\n  unclosed bracket");
    expect(issues.length).toBeGreaterThan(0);
    expect(issues.every((i) => i.severity === "error")).toBe(true);
  });

  it("enforces viewer read-only behavior via RBAC", () => {
    const viewer = derivePolicyAccess({
      requiresAuth: true,
      roles: ["viewer"],
      principalRole: "viewer",
    });
    expect(viewer.canEdit).toBe(false);
    expect(viewer.canPublish).toBe(false);
    expect(viewer.isReadOnly).toBe(true);
  });

  it("grants edit and publish to authorized roles", () => {
    const admin = derivePolicyAccess({
      requiresAuth: true,
      roles: ["admin"],
      principalRole: "admin",
    });
    expect(admin.canEdit).toBe(true);
    expect(admin.canPublish).toBe(true);
    expect(admin.isReadOnly).toBe(false);
  });

  it("enforces publish restriction for editor-only role", () => {
    const editor = derivePolicyAccess({
      requiresAuth: true,
      roles: ["editor"],
      principalRole: "editor",
    });
    expect(editor.canEdit).toBe(true);
    expect(editor.canPublish).toBe(false);
    expect(editor.isReadOnly).toBe(false);
  });
});
