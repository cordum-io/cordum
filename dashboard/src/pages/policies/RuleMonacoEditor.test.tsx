import { createRef } from "react";
import { describe, expect, it, vi } from "vitest";
import { renderWithProviders, waitFor } from "@/test-utils/render";
import { RuleScopeKind } from "@/api/generated/model/ruleScopeKind";
import { RuleStatus } from "@/api/generated/model/ruleStatus";
import { RuleType } from "@/api/generated/model/ruleType";
import { RULE_TEMPLATES } from "@/lib/policy-studio/templates";
import RuleMonacoEditor, {
  type RuleMonacoEditorHandle,
} from "./RuleMonacoEditor";
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

  it("exposes the insertText imperative API via ref", async () => {
    const onChange = vi.fn();
    const ref = createRef<RuleMonacoEditorHandle>();
    renderWithProviders(
      <RuleMonacoEditor ref={ref} rule={sampleRule} onChange={onChange} />,
    );
    // useImperativeHandle effects run after render, so wait for the
    // forwardRef installation before invoking the imperative API.
    await waitFor(() => expect(ref.current).not.toBeNull());
    expect(typeof ref.current!.insertText).toBe("function");
  });

  it("propagates an insertText payload through the parser to onChange (fallback path)", async () => {
    // Vitest aliases @monaco-editor/react to a () => null stub, so the
    // Monaco editor instance ref stays null. insertText therefore takes
    // the fallback branch: append to local YAML + synchronously parse +
    // call onChange. The live Monaco executeEdits path is exercised at
    // runtime in browser. We use a non-envelope `description:` insert so
    // the appended snippet doesn't duplicate any envelope key (which the
    // strict YAML parser would reject).
    const onChange = vi.fn();
    const ref = createRef<RuleMonacoEditorHandle>();
    renderWithProviders(
      <RuleMonacoEditor ref={ref} rule={sampleRule} onChange={onChange} />,
    );

    await waitFor(() => expect(ref.current).not.toBeNull());
    ref.current!.insertText("description: Updated by template");

    await waitFor(() => expect(onChange).toHaveBeenCalledTimes(1));
    const next = onChange.mock.calls[0]?.[0] as NormalizedRule;
    expect(next.description).toBe("Updated by template");
    // Envelope fields stay intact — append doesn't replace them.
    expect(next.id).toBe(sampleRule.id);
    expect(next.name).toBe(sampleRule.name);
    expect(next.type).toBe(RuleType.input);
  });

  it("replaceDocument loads a real PII Redact template into the editor and propagates the parsed Rule", async () => {
    // QA reopen #1 fix (msg-d41b3a8d): full-envelope template insertion must
    // produce a valid editor document. We use the actual committed template
    // from RULE_TEMPLATES — not a synthetic single-key snippet — so this
    // test exercises the same payload an end-user would click.
    const onChange = vi.fn();
    const ref = createRef<RuleMonacoEditorHandle>();
    renderWithProviders(
      <RuleMonacoEditor ref={ref} rule={sampleRule} onChange={onChange} />,
    );
    await waitFor(() => expect(ref.current).not.toBeNull());

    const piiTemplate = RULE_TEMPLATES.find((t) => t.id === "pii-redact");
    expect(piiTemplate).toBeDefined();
    expect(piiTemplate!.yaml).toMatch(/Template: PII Redact/);
    ref.current!.replaceDocument(piiTemplate!.yaml);

    // The template must parse cleanly — no "Map keys must be unique" — and
    // the parsed Rule must reflect the template's envelope (replace, not
    // append).
    await waitFor(() => expect(onChange).toHaveBeenCalledTimes(1));
    const next = onChange.mock.calls[0]?.[0] as NormalizedRule;
    expect(next.name).toBe("PII redact");
    expect(next.type).toBe(RuleType.input);
    expect(next.scope).toEqual({ kind: RuleScopeKind.tenant, value: "default" });
    expect(next.status).toBe(RuleStatus.draft);
    expect((next.decide as { type: string }).type).toBe("redact");
  });

  it.each(RULE_TEMPLATES.map((t) => [t.id, t]))(
    "replaceDocument loads each committed RULE_TEMPLATES entry without YAML duplicate-key parse errors (%s)",
    async (_id, template) => {
      // Iterates the actual seven templates so a future template addition
      // is automatically covered. QA's specific failure mode was
      // "Map keys must be unique at line 15" when appending a full
      // envelope; replaceDocument's contract is "swap the document
      // wholesale", so each template must parse on its own as a
      // standalone Rule envelope.
      const onChange = vi.fn();
      const ref = createRef<RuleMonacoEditorHandle>();
      renderWithProviders(
        <RuleMonacoEditor ref={ref} rule={sampleRule} onChange={onChange} />,
      );
      await waitFor(() => expect(ref.current).not.toBeNull());
      ref.current!.replaceDocument(template.yaml);
      await waitFor(() => expect(onChange).toHaveBeenCalledTimes(1));
      const next = onChange.mock.calls[0]?.[0] as NormalizedRule;
      // The parsed rule's `type` must match the template's declared type;
      // if the parser had errored on duplicate keys, onChange wouldn't
      // have been called at all.
      expect(next.type).toBe(template.ruleType);
    },
  );
});
