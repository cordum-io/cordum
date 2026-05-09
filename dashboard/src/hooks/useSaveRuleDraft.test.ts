import { renderHook, act } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { RuleScopeKind } from "@/api/generated/model/ruleScopeKind";
import { RuleStatus } from "@/api/generated/model/ruleStatus";
import { RuleType } from "@/api/generated/model/ruleType";
import { useSaveRuleDraft } from "./useSaveRuleDraft";
import type { NormalizedRule } from "./useRulesList";

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

describe("useSaveRuleDraft (Phase 3A boundary)", () => {
  it("reports isAvailable=false in Phase 3A so the drawer disables the Save button", () => {
    const { result } = renderHook(() => useSaveRuleDraft());
    expect(result.current.isAvailable).toBe(false);
    expect(result.current.isPending).toBe(false);
  });

  it("returns a typed error result when mutateAsync is called while disabled (defense-in-depth)", async () => {
    const { result } = renderHook(() => useSaveRuleDraft());
    let outcome: Awaited<ReturnType<typeof result.current.mutateAsync>> | null = null;
    await act(async () => {
      outcome = await result.current.mutateAsync(sampleRule);
    });
    expect(outcome).not.toBeNull();
    const saveResult = outcome!;
    expect(saveResult.ok).toBe(false);
    if (!saveResult.ok) {
      expect(saveResult.error).toMatch(/Phase 3A|not enabled|Phase 3E/i);
    }
  });

  it("does not flip isPending when the disabled gate short-circuits", async () => {
    const { result } = renderHook(() => useSaveRuleDraft());
    await act(async () => {
      await result.current.mutateAsync(sampleRule);
    });
    expect(result.current.isPending).toBe(false);
  });
});
