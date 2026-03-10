import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { createEmptyGlobalInputRule } from "@/components/policy/global/GlobalRuleEditorDrawer";
import { InputRuleEditorDrawer } from "./InputRuleEditorDrawer";

describe("InputRuleEditorDrawer", () => {
  it("renders viewer read-only drawer content without save affordance", () => {
    const rule = createEmptyGlobalInputRule(1);
    rule.id = "deny-admin-tools";
    rule.decision = "deny";
    rule.reason = "Block privileged tools";

    const markup = renderToStaticMarkup(
      <InputRuleEditorDrawer
        open
        readOnly
        rule={rule}
        nextRuleIndex={2}
        existingRuleIds={[]}
        onClose={() => {}}
        onSave={() => {}}
      />,
    );

    expect(markup).toContain("Viewer mode: this drawer is read-only");
    expect(markup).toContain("deny-admin-tools");
    expect(markup).toContain("View Rule");
    expect(markup).not.toContain("Save rule");
  });

  it("delegates to editable GlobalRuleEditorDrawer when readOnly is false", () => {
    const markup = renderToStaticMarkup(
      <InputRuleEditorDrawer
        open
        readOnly={false}
        rule={createEmptyGlobalInputRule(1)}
        nextRuleIndex={2}
        existingRuleIds={[]}
        onClose={() => {}}
        onSave={() => {}}
      />,
    );

    expect(markup).toContain("Edit Rule");
    expect(markup).toContain("Save rule");
  });
});
