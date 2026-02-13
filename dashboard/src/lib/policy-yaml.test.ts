import { describe, it, expect } from "vitest";
import { validatePolicyYaml, countRulesFromYaml } from "./policy-yaml";

describe("validatePolicyYaml", () => {
  it("accepts valid YAML", () => {
    const result = validatePolicyYaml("version: '1'\nrules:\n  - id: r1\n    decision: allow");
    expect(result.valid).toBe(true);
    expect(result.errors).toHaveLength(0);
    expect(result.parsed).toBeDefined();
  });

  it("rejects invalid YAML syntax", () => {
    const result = validatePolicyYaml("bad: [unclosed");
    expect(result.valid).toBe(false);
    expect(result.errors.length).toBeGreaterThan(0);
    expect(result.errors[0].message).toBeTruthy();
  });

  it("accepts empty string as valid YAML (parses to null)", () => {
    const result = validatePolicyYaml("");
    expect(result.valid).toBe(true);
  });

  it("returns line number for errors when available", () => {
    const result = validatePolicyYaml("foo:\n  bar:\n    - bad: [unclosed");
    expect(result.valid).toBe(false);
    expect(result.errors[0].line).toBeGreaterThanOrEqual(1);
  });
});

describe("countRulesFromYaml", () => {
  it("counts rules array", () => {
    const yaml = "rules:\n  - id: r1\n  - id: r2\n  - id: r3";
    expect(countRulesFromYaml(yaml)).toBe(3);
  });

  it("counts tenant topics as rules", () => {
    const yaml = "tenants:\n  default:\n    deny_topics:\n      - a\n      - b\n    allow_topics:\n      - c";
    expect(countRulesFromYaml(yaml)).toBe(3);
  });

  it("returns 0 for empty YAML", () => {
    expect(countRulesFromYaml("")).toBe(0);
  });

  it("returns 0 for invalid YAML", () => {
    expect(countRulesFromYaml("bad: [unclosed")).toBe(0);
  });

  it("returns 0 when no rules or tenants present", () => {
    expect(countRulesFromYaml("version: '1'")).toBe(0);
  });
});
