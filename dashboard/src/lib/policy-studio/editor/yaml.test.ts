import { describe, expect, it } from "vitest";
import { RuleScopeKind } from "@/api/generated/model/ruleScopeKind";
import { RuleStatus } from "@/api/generated/model/ruleStatus";
import { RuleType } from "@/api/generated/model/ruleType";
import { ruleToYaml, yamlToPartialRule } from "./yaml";
import type { NormalizedRule } from "@/hooks/useRulesList";

const baseRule: NormalizedRule = {
  id: "block-secrets",
  name: "Block secrets",
  type: RuleType.input,
  scope: { kind: RuleScopeKind.workflow, value: "wf-claims" },
  status: RuleStatus.published,
  version: "v3",
  audit: {
    created_at: "2026-04-01T00:00:00Z",
    created_by: "alice",
    updated_at: "2026-05-01T00:00:00Z",
    updated_by: "bob",
  },
  match: { tenants: ["acme"], topics: ["job.acme.*"] },
  decide: { type: "deny", reason: "secret_leak" },
  description: "Block PII / secrets in inputs",
};

describe("ruleToYaml", () => {
  it("emits canonical envelope ordering and round-trips through yamlToPartialRule", () => {
    const yaml = ruleToYaml(baseRule);
    expect(yaml).toMatch(/^id:/m);
    expect(yaml).toContain("type: input");
    expect(yaml).toContain("status: published");
    const parsed = yamlToPartialRule(yaml, baseRule);
    expect(parsed.error).toBeNull();
    expect(parsed.rule).toEqual(baseRule);
  });

  it("omits description when empty", () => {
    const minimal: NormalizedRule = {
      ...baseRule,
      description: undefined,
    };
    delete (minimal as Partial<NormalizedRule>).description;
    const yaml = ruleToYaml(minimal);
    expect(yaml).not.toContain("description:");
  });
});

describe("yamlToPartialRule", () => {
  it("returns base rule unchanged for an empty document", () => {
    const result = yamlToPartialRule("", baseRule);
    expect(result.error).toBeNull();
    expect(result.rule).toEqual(baseRule);
  });

  it("rejects non-object top level YAML", () => {
    const result = yamlToPartialRule("- one\n- two", baseRule);
    expect(result.rule).toBeNull();
    expect(result.error).toMatch(/Top-level/i);
  });

  it("rejects unsupported rule type", () => {
    const result = yamlToPartialRule("type: not-a-real-type", baseRule);
    expect(result.rule).toBeNull();
    expect(result.error).toMatch(/rule type/i);
  });

  it("rejects unsupported scope kind in object form", () => {
    const result = yamlToPartialRule("scope:\n  kind: bogus\n  value: x", baseRule);
    expect(result.rule).toBeNull();
    expect(result.error).toMatch(/scope/i);
  });

  it("accepts string scope shorthand for known kinds", () => {
    const result = yamlToPartialRule("scope: global", baseRule);
    expect(result.error).toBeNull();
    expect(result.rule?.scope).toEqual({ kind: RuleScopeKind.global });
  });

  it("returns a parse error for malformed YAML without crashing", () => {
    const result = yamlToPartialRule("id: [oops:", baseRule);
    expect(result.rule).toBeNull();
    expect(result.error).toBeTruthy();
  });

  it("preserves description when set, removes it when explicitly empty", () => {
    const withDesc = yamlToPartialRule("description: new desc", baseRule);
    expect(withDesc.rule?.description).toBe("new desc");
    const withoutDesc = yamlToPartialRule("description: ''", baseRule);
    expect(withoutDesc.rule?.description).toBeUndefined();
  });
});
