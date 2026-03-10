import { describe, expect, it } from "vitest";
import {
  getInputRulesAffordances,
  getInputRulesViewModeState,
  INPUT_RULES_PAGE_SECTIONS,
} from "./InputRulesPage";

describe("InputRulesPage extraction contract", () => {
  it("keeps page sections scoped to input-policy concerns", () => {
    expect(INPUT_RULES_PAGE_SECTIONS).toEqual([
      "first-match-banner",
      "default-decision",
      "ordered-rules",
      "yaml-pane",
    ]);
    expect(INPUT_RULES_PAGE_SECTIONS).not.toContain("output-rules");
    expect(INPUT_RULES_PAGE_SECTIONS).not.toContain("tenant-policy");
    expect(INPUT_RULES_PAGE_SECTIONS).not.toContain("simulator");
  });

  it("uses page-scoped view-mode visibility rules", () => {
    expect(getInputRulesViewModeState("visual")).toEqual({ showVisual: true, showYaml: false });
    expect(getInputRulesViewModeState("split")).toEqual({ showVisual: true, showYaml: true });
    expect(getInputRulesViewModeState("yaml")).toEqual({ showVisual: false, showYaml: true });
  });

  it("enforces read-only affordances for viewers and write affordances for editors", () => {
    expect(getInputRulesAffordances(false)).toEqual({
      canAddRule: false,
      canEditRule: false,
      canReorderRule: false,
      canDeleteRule: false,
      yamlEditable: false,
      drawerReadOnly: true,
    });

    expect(getInputRulesAffordances(true)).toEqual({
      canAddRule: true,
      canEditRule: true,
      canReorderRule: true,
      canDeleteRule: true,
      yamlEditable: true,
      drawerReadOnly: false,
    });
  });
});
