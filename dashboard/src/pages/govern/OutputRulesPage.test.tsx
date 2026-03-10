import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { OutputPolicyControls } from "@/components/policy/output-rules/OutputPolicyControls";
import { derivePolicyAccess } from "@/hooks/usePolicyAccess";
import {
  getOutputRulesAffordances,
  OUTPUT_RULES_PAGE_SECTIONS,
} from "./OutputRulesPage";

describe("OutputRulesPage extraction contract", () => {
  it("keeps page sections scoped to output-policy concerns", () => {
    expect(OUTPUT_RULES_PAGE_SECTIONS).toEqual([
      "output-policy-controls",
      "output-policy-status",
      "output-rules-list",
    ]);
    expect(OUTPUT_RULES_PAGE_SECTIONS).not.toContain("input-rules");
    expect(OUTPUT_RULES_PAGE_SECTIONS).not.toContain("tenant-policy");
    expect(OUTPUT_RULES_PAGE_SECTIONS).not.toContain("simulator");
  });

  it("enforces read-only affordances for viewers and write affordances for editors", () => {
    expect(getOutputRulesAffordances(false)).toEqual({
      canSave: false,
      canAddRule: false,
      canEditRule: false,
      canDeleteRule: false,
      canReorderRule: false,
      canToggleRule: false,
      drawerReadOnly: true,
    });

    expect(getOutputRulesAffordances(true)).toEqual({
      canSave: true,
      canAddRule: true,
      canEditRule: true,
      canDeleteRule: true,
      canReorderRule: true,
      canToggleRule: true,
      drawerReadOnly: false,
    });
  });

  it("maps policy roles into output-rule management affordances", () => {
    const viewerAccess = derivePolicyAccess({
      requiresAuth: true,
      roles: ["viewer"],
      principalRole: "viewer",
    });
    expect(viewerAccess.canManageOutputRules).toBe(false);

    const operatorAccess = derivePolicyAccess({
      requiresAuth: true,
      roles: ["operator"],
      principalRole: "operator",
    });
    expect(operatorAccess.canManageOutputRules).toBe(true);
  });

  it("renders explicit fail-mode guidance for operators", () => {
    const markup = renderToStaticMarkup(
      <OutputPolicyControls
        outputPolicy={{ enabled: true, failMode: "closed" }}
        readOnly
        onChange={() => {}}
      />,
    );

    expect(markup).toContain("closed (recommended)");
    expect(markup).toContain("fail-closed:");
    expect(markup).toContain("fail-open:");
  });
});
