import { describe, expect, it } from "vitest";
import { createEmptyGlobalInputRule } from "@/components/policy/global/GlobalRuleEditorDrawer";
import {
  filterTenantScopedRules,
  getTenantDetailAffordances,
  TENANT_DETAIL_SECTIONS,
} from "./TenantDetailPage";

describe("TenantDetailPage contract", () => {
  it("defines the expected tenant detail section structure", () => {
    expect(TENANT_DETAIL_SECTIONS).toEqual([
      "topic-access-control",
      "mcp-governance",
      "limits",
      "tenant-scoped-rules",
    ]);
  });

  it("maps tenant detail read/write affordances by RBAC capability", () => {
    expect(getTenantDetailAffordances(false)).toEqual({
      canEdit: false,
      showSave: false,
      showReadOnlyBanner: true,
    });

    expect(getTenantDetailAffordances(true)).toEqual({
      canEdit: true,
      showSave: true,
      showReadOnlyBanner: false,
    });
  });

  it("filters tenant-scoped rules from merged rule context", () => {
    const acmeRule = createEmptyGlobalInputRule(1);
    acmeRule.id = "acme-deny-admin";
    acmeRule.match.tenants = ["acme-corp"];

    const globalRule = createEmptyGlobalInputRule(2);
    globalRule.id = "global-default";
    globalRule.match.tenants = [];

    const caseInsensitiveRule = createEmptyGlobalInputRule(3);
    caseInsensitiveRule.id = "acme-allow-read";
    caseInsensitiveRule.match.tenants = [" ACME-CORP "];

    const scoped = filterTenantScopedRules(
      [acmeRule, globalRule, caseInsensitiveRule],
      "acme-corp",
    );
    expect(scoped.map((rule) => rule.id)).toEqual([
      "acme-deny-admin",
      "acme-allow-read",
    ]);
  });
});
