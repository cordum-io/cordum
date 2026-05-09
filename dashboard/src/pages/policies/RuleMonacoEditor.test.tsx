import { describe, expect, it, vi } from "vitest";
import { renderWithProviders, waitFor } from "@/test-utils/render";
import { RuleScopeKind } from "@/api/generated/model/ruleScopeKind";
import { RuleStatus } from "@/api/generated/model/ruleStatus";
import { RuleType } from "@/api/generated/model/ruleType";
import RuleMonacoEditor from "./RuleMonacoEditor";
import type { NormalizedRule } from "@/hooks/useRulesList";

const sampleRule: NormalizedRule = {
  id: "rule-1",
  name: "Block secrets",
  type: RuleType.input,
  scope: { kind: RuleScopeKind.global },
  status: RuleStatus.draft,
  version: "v1",
  audit: { created_at: "", created_by: "" },
  match: {},
  decide: { type: "deny" },
};

describe("RuleMonacoEditor (lazy scaffold)", () => {
  it("mounts without crashing under the test stub for @monaco-editor/react", async () => {
    const onChange = vi.fn();
    const { container } = renderWithProviders(
      <RuleMonacoEditor rule={sampleRule} onChange={onChange} />,
    );
    // The vitest alias replaces @monaco-editor/react with a stub that
    // renders null, so we cannot assert against a textarea here. The
    // important contract is that the lazy boundary resolves and our
    // wrapper renders its outer container without throwing.
    await waitFor(() => expect(container.firstChild).not.toBeNull());
  });

  it("does not call onChange synchronously on mount", async () => {
    const onChange = vi.fn();
    renderWithProviders(<RuleMonacoEditor rule={sampleRule} onChange={onChange} />);
    // Debounce window is 300ms; mount alone should never emit a draft.
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(onChange).not.toHaveBeenCalled();
  });

  it("re-serializes when the parent rule reference changes (draft sync)", async () => {
    const onChange = vi.fn();
    const { rerender, container } = renderWithProviders(
      <RuleMonacoEditor rule={sampleRule} onChange={onChange} />,
    );
    rerender(
      <RuleMonacoEditor
        rule={{ ...sampleRule, name: "Updated by Form view" }}
        onChange={onChange}
      />,
    );
    // No assertion against Monaco internals — the stub renders null. The
    // contract is that re-render does not throw and onChange is not
    // called as a side effect of the parent update (the lastEmittedRef
    // guard suppresses the round-trip echo).
    await waitFor(() => expect(container.firstChild).not.toBeNull());
    expect(onChange).not.toHaveBeenCalled();
  });
});
