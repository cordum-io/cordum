import { describe, expect, it } from "vitest";
import { RuleScopeKind } from "@/api/generated/model/ruleScopeKind";
import { RuleStatus } from "@/api/generated/model/ruleStatus";
import { RuleType } from "@/api/generated/model/ruleType";
import {
  emptyDraftRule,
  NEW_RULE_ID,
  parseCreateNewType,
  ruleHasKnownType,
} from "./useRule";
import { UNKNOWN_RULE_TYPE, type NormalizedRule } from "./useRulesList";

describe("emptyDraftRule", () => {
  it.each([
    [RuleType.input],
    [RuleType.output],
    [RuleType.velocity],
    [RuleType.edge],
  ])("returns a draft Rule for %s", (type) => {
    const draft = emptyDraftRule(type);
    expect(draft.type).toBe(type);
    expect(draft.status).toBe(RuleStatus.draft);
    expect(draft.scope.kind).toBe(RuleScopeKind.global);
    expect(draft.id).toBe("");
    expect(draft.name).toBe("");
    expect(draft.audit.created_at).toBe("");
    expect(draft.audit.created_by).toBe("");
    expect(draft.match).toEqual({});
    expect(draft.decide).toEqual({ type: "allow" });
  });
});

describe("parseCreateNewType", () => {
  it("accepts every canonical RuleType", () => {
    expect(parseCreateNewType(RuleType.input)).toBe(RuleType.input);
    expect(parseCreateNewType(RuleType.output)).toBe(RuleType.output);
    expect(parseCreateNewType(RuleType.velocity)).toBe(RuleType.velocity);
    expect(parseCreateNewType(RuleType.edge)).toBe(RuleType.edge);
  });

  it("rejects unknown / missing values", () => {
    expect(parseCreateNewType(null)).toBeUndefined();
    expect(parseCreateNewType("")).toBeUndefined();
    expect(parseCreateNewType("InputRule")).toBeUndefined(); // legacy alias not accepted on create-new path
    expect(parseCreateNewType("nonsense")).toBeUndefined();
  });
});

describe("ruleHasKnownType", () => {
  const baseRule: NormalizedRule = {
    id: "x",
    name: "x",
    type: RuleType.input,
    scope: { kind: RuleScopeKind.global },
    status: RuleStatus.draft,
    version: "v1",
    audit: { created_at: "", created_by: "" },
    match: {},
    decide: { type: "allow" },
  };

  it("recognizes a generated RuleType value", () => {
    expect(ruleHasKnownType(baseRule)).toBe(true);
  });

  it("rejects the UNKNOWN_RULE_TYPE sentinel (preserves task-15537d13 hotfix safety)", () => {
    expect(ruleHasKnownType({ ...baseRule, type: UNKNOWN_RULE_TYPE })).toBe(false);
  });
});

describe("NEW_RULE_ID", () => {
  it("is the literal sentinel the URL contract expects", () => {
    expect(NEW_RULE_ID).toBe("new");
  });
});
