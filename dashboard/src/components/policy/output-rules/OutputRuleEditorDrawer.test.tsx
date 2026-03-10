import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { createEmptyGlobalOutputRule } from "@/components/policy/global/GlobalOutputRuleEditorDrawer";
import { OutputRuleEditorDrawer } from "./OutputRuleEditorDrawer";

describe("OutputRuleEditorDrawer", () => {
  it("renders viewer read-only drawer content without save affordance", () => {
    const rule = createEmptyGlobalOutputRule(1);
    rule.id = "quarantine-secrets";
    rule.decision = "quarantine";
    rule.severity = "high";
    rule.reason = "Protect outbound channels";
    rule.match.detectors = ["pii"];
    rule.match.contentPatterns = ["ssn"];

    const markup = renderToStaticMarkup(
      <OutputRuleEditorDrawer
        open
        readOnly
        rule={rule}
        nextRuleIndex={2}
        onClose={() => {}}
        onSave={() => {}}
      />,
    );

    expect(markup).toContain("Viewer mode: output rule editor is read-only.");
    expect(markup).toContain("quarantine-secrets");
    expect(markup).toContain("View Output Rule");
    expect(markup).not.toContain("Save rule");
  });

  it("delegates to editable GlobalOutputRuleEditorDrawer when readOnly is false", () => {
    const markup = renderToStaticMarkup(
      <OutputRuleEditorDrawer
        open
        readOnly={false}
        rule={createEmptyGlobalOutputRule(1)}
        nextRuleIndex={2}
        onClose={() => {}}
        onSave={() => {}}
      />,
    );

    expect(markup).toContain("Edit Output Rule");
    expect(markup).toContain("Save rule");
  });
});
