import { describe, expect, it } from "vitest";
import { RuleType } from "@/api/generated/model/ruleType";
import { getRuleSchema } from "@/lib/policy-studio/schemas";
import {
  hoverDocumentationFor,
  propertyCompletionsFromSchema,
  validateAgainstSchema,
} from "./yaml-diagnostics";

function schemaFor(type: RuleType) {
  const schema = getRuleSchema(type);
  if (!schema) throw new Error(`Schema for ${type} missing`);
  return schema;
}

describe("validateAgainstSchema", () => {
  const baseInputRule = {
    id: "rule-1",
    name: "Block secrets",
    type: "input",
    scope: { kind: "global" },
    status: "draft",
    version: "v1",
    audit: { created_at: "", created_by: "" },
    match: { topics: ["email.draft"] },
    decide: { type: "deny" },
  };

  it("returns no issues for a fully-valid input rule", () => {
    const issues = validateAgainstSchema(baseInputRule, schemaFor(RuleType.input));
    expect(issues).toEqual([]);
  });

  it("flags an out-of-enum decide.type", () => {
    const rule = { ...baseInputRule, decide: { type: "shrug" } };
    const issues = validateAgainstSchema(rule, schemaFor(RuleType.input));
    expect(issues.length).toBeGreaterThan(0);
    expect(issues[0].path).toBe("decide.type");
    expect(issues[0].message).toMatch(/Expected one of/);
  });

  it("flags a wrong type literal on a velocity rule's decide.type", () => {
    const velocityRule = {
      ...baseInputRule,
      type: "velocity",
      match: { tenants: ["acme"] },
      decide: { type: "allow", max_per_minute: 10 },
    };
    const issues = validateAgainstSchema(velocityRule, schemaFor(RuleType.velocity));
    const decideIssue = issues.find((i) => i.path === "decide.type");
    expect(decideIssue).toBeDefined();
    expect(decideIssue?.message).toMatch(/throttle/);
  });

  it("flags a velocity rule that has no per-window threshold (anyOf branch)", () => {
    const velocityRule = {
      ...baseInputRule,
      type: "velocity",
      match: {},
      decide: { type: "throttle" },
    };
    const issues = validateAgainstSchema(velocityRule, schemaFor(RuleType.velocity));
    const branchIssue = issues.find((i) => i.path === "decide" && /anyOf/i.test(i.message));
    expect(branchIssue).toBeDefined();
  });

  it("flags an unknown property under match (additionalProperties: false)", () => {
    const rule = {
      ...baseInputRule,
      match: { topics: ["x"], not_a_real_field: "boom" },
    };
    const issues = validateAgainstSchema(rule, schemaFor(RuleType.input));
    const unknown = issues.find((i) => i.path === "match.not_a_real_field");
    expect(unknown).toBeDefined();
    expect(unknown?.message).toMatch(/Unknown property/);
  });

  it("flags missing required envelope fields", () => {
    const rule = { ...baseInputRule, name: undefined } as unknown as Record<string, unknown>;
    delete rule.name;
    const issues = validateAgainstSchema(rule, schemaFor(RuleType.input));
    expect(issues.find((i) => i.message.includes("name"))).toBeDefined();
  });

  it("accepts an output rule with finding_types restricted to the closed enum", () => {
    const outputRule = {
      ...baseInputRule,
      type: "output",
      match: { finding_types: ["secret_leak", "pii"] },
      decide: { type: "redact", reason: "PII redaction" },
    };
    expect(validateAgainstSchema(outputRule, schemaFor(RuleType.output))).toEqual([]);
  });

  it("rejects an output rule with a finding_type outside the enum", () => {
    const outputRule = {
      ...baseInputRule,
      type: "output",
      match: { finding_types: ["spam"] },
      decide: { type: "redact" },
    };
    const issues = validateAgainstSchema(outputRule, schemaFor(RuleType.output));
    const findingIssue = issues.find((i) => i.path === "match.finding_types.0");
    expect(findingIssue).toBeDefined();
    expect(findingIssue?.message).toMatch(/Expected one of/);
  });
});

describe("propertyCompletionsFromSchema", () => {
  it("yields completion items for the input rule's match selectors", () => {
    const items = propertyCompletionsFromSchema(schemaFor(RuleType.input));
    const fields = items.filter((i) => i.kind === "field").map((i) => i.label);
    expect(fields).toContain("topics");
    expect(fields).toContain("tools");
    expect(fields).toContain("risk_tags");
    expect(fields).toContain("content_pattern");
  });

  it("yields completion items for decide.type enum values on input rules", () => {
    const items = propertyCompletionsFromSchema(schemaFor(RuleType.input));
    const enumLabels = items.filter((i) => i.kind === "value").map((i) => i.label);
    expect(enumLabels).toContain("allow");
    expect(enumLabels).toContain("deny");
    expect(enumLabels).toContain("throttle");
  });

  it("yields the throttle const for velocity decide.type", () => {
    const items = propertyCompletionsFromSchema(schemaFor(RuleType.velocity));
    expect(items.find((i) => i.kind === "value" && i.label === "throttle")).toBeDefined();
  });

  it("yields finding_types enum values for output rules", () => {
    const items = propertyCompletionsFromSchema(schemaFor(RuleType.output));
    const enumLabels = items.filter((i) => i.kind === "value").map((i) => i.label);
    expect(enumLabels).toContain("secret_leak");
    expect(enumLabels).toContain("pii");
    expect(enumLabels).toContain("injection");
  });
});

describe("hoverDocumentationFor", () => {
  it("returns description text for a known property", () => {
    const doc = hoverDocumentationFor(schemaFor(RuleType.input), "topics");
    expect(doc).toBeTruthy();
    expect(doc).toMatch(/topics/i);
  });

  it("returns description for nested properties", () => {
    const doc = hoverDocumentationFor(schemaFor(RuleType.velocity), "max_per_minute");
    expect(doc).toBeTruthy();
    expect(doc).toMatch(/per-minute/i);
  });

  it("returns null for an unknown word", () => {
    const doc = hoverDocumentationFor(schemaFor(RuleType.input), "definitelyNotARealField");
    expect(doc).toBeNull();
  });

  it("returns null for an empty word", () => {
    expect(hoverDocumentationFor(schemaFor(RuleType.input), "")).toBeNull();
    expect(hoverDocumentationFor(schemaFor(RuleType.input), "   ")).toBeNull();
  });
});
