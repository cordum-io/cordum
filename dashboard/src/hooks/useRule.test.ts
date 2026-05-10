import { describe, expect, it } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import { RuleScopeKind } from "@/api/generated/model/ruleScopeKind";
import { RuleStatus } from "@/api/generated/model/ruleStatus";
import { RuleType } from "@/api/generated/model/ruleType";
import { queryKeys } from "../lib/queryKeys";
import {
  emptyDraftRule,
  findRuleInListCaches,
  NEW_RULE_ID,
  parseCreateNewType,
  ruleHasKnownType,
} from "./useRule";
import {
  UNKNOWN_RULE_TYPE,
  type NormalizedRule,
  type RulesListResult,
} from "./useRulesList";

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

describe("findRuleInListCaches", () => {
  function makeRule(id: string, name = id): NormalizedRule {
    return {
      id,
      name,
      type: RuleType.input,
      scope: { kind: RuleScopeKind.global },
      status: RuleStatus.published,
      version: "v1",
      audit: { created_at: "2026-04-01T00:00:00Z", created_by: "alice" },
      match: {},
      decide: { type: "allow" },
    };
  }

  it("returns null when no list query is cached", () => {
    const queryClient = new QueryClient();
    expect(findRuleInListCaches(queryClient, "rule-1")).toBeNull();
  });

  it("finds a rule in a cached list query (default filters)", () => {
    const queryClient = new QueryClient();
    const data: RulesListResult = {
      rules: [makeRule("rule-1", "Block secrets"), makeRule("rule-2")],
      total: 2,
    };
    queryClient.setQueryData(queryKeys.policyStudioRules.list(), data);
    const found = findRuleInListCaches(queryClient, "rule-1");
    expect(found?.name).toBe("Block secrets");
  });

  it("finds a rule across multiple list queries with different filters", () => {
    const queryClient = new QueryClient();
    queryClient.setQueryData<RulesListResult>(
      queryKeys.policyStudioRules.list({ type: RuleType.input }),
      { rules: [makeRule("rule-input")], total: 1 },
    );
    queryClient.setQueryData<RulesListResult>(
      queryKeys.policyStudioRules.list({ scope: "tenant:acme" }),
      { rules: [makeRule("rule-acme", "Tenant guard")], total: 1 },
    );
    expect(findRuleInListCaches(queryClient, "rule-input")?.id).toBe("rule-input");
    expect(findRuleInListCaches(queryClient, "rule-acme")?.name).toBe(
      "Tenant guard",
    );
  });

  it("returns null when the requested id is not in any cached list", () => {
    const queryClient = new QueryClient();
    queryClient.setQueryData<RulesListResult>(
      queryKeys.policyStudioRules.list(),
      { rules: [makeRule("other-rule")], total: 1 },
    );
    expect(findRuleInListCaches(queryClient, "missing")).toBeNull();
  });

  it("ignores cached queries with no data (e.g. errored fetches)", () => {
    const queryClient = new QueryClient();
    queryClient.setQueryData(
      queryKeys.policyStudioRules.list(),
      undefined as unknown as RulesListResult,
    );
    expect(findRuleInListCaches(queryClient, "rule-1")).toBeNull();
  });

  it("ignores cached detail-query data when iterating list caches (regression: QA reopen #2)", () => {
    // The umbrella .all() key matches BOTH list and detail entries. Detail
    // data is a NormalizedRule with NO `.rules` array; iterating naïvely
    // crashes with "Cannot read properties of undefined (reading 'find')"
    // once a rule has been opened. Filter must be by query-key shape, not
    // just truthy `data`.
    const queryClient = new QueryClient();
    queryClient.setQueryData(
      queryKeys.policyStudioRules.detail("rule-x"),
      makeRule("rule-x", "Cached detail row"),
    );
    queryClient.setQueryData<RulesListResult>(queryKeys.policyStudioRules.list(), {
      rules: [makeRule("rule-1", "Block secrets"), makeRule("rule-2")],
      total: 2,
    });
    expect(() => findRuleInListCaches(queryClient, "rule-1")).not.toThrow();
    expect(findRuleInListCaches(queryClient, "rule-1")?.name).toBe("Block secrets");
    // Detail-only id must NOT be returned via the list-cache path; the
    // helper's contract is "find in list caches", not "find anywhere".
    expect(findRuleInListCaches(queryClient, "rule-x")).toBeNull();
  });

  it("does not crash when only a detail-query entry is cached (no list query yet)", () => {
    const queryClient = new QueryClient();
    queryClient.setQueryData(
      queryKeys.policyStudioRules.detail("rule-only-detail"),
      makeRule("rule-only-detail"),
    );
    expect(() => findRuleInListCaches(queryClient, "rule-only-detail")).not.toThrow();
    expect(findRuleInListCaches(queryClient, "rule-only-detail")).toBeNull();
  });
});
