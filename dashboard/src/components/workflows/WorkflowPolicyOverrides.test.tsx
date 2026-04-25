import { describe, expect, it } from "vitest";
import { extractConstraints, hasAnyConstraints } from "./WorkflowPolicyOverrides";
import { extractWorkflowRules } from "./WorkflowPolicyOverrideRules";

describe("extractConstraints", () => {
  it("returns null when config and metadata are both null", () => {
    expect(extractConstraints(null, null)).toBeNull();
  });

  it("returns null when neither has constraints", () => {
    expect(extractConstraints({ foo: 1 }, { bar: 2 })).toBeNull();
  });

  it("extracts from config.constraints", () => {
    const c = extractConstraints({ constraints: { budgets: { max_retries: 3 } } }, null);
    expect(c).toEqual({ budgets: { max_retries: 3 } });
  });

  it("extracts from config.policy_constraints", () => {
    const c = extractConstraints({ policy_constraints: { sandbox: { isolated: true } } }, null);
    expect(c).toEqual({ sandbox: { isolated: true } });
  });

  it("falls back to metadata.constraints", () => {
    const c = extractConstraints(null, { constraints: { diff: { max_files: 10 } } });
    expect(c).toEqual({ diff: { max_files: 10 } });
  });

  it("prefers config over metadata", () => {
    const c = extractConstraints(
      { constraints: { budgets: { max_retries: 5 } } },
      { constraints: { budgets: { max_retries: 2 } } },
    );
    expect(c).toEqual({ budgets: { max_retries: 5 } });
  });
});

describe("hasAnyConstraints", () => {
  it("returns false for null", () => {
    expect(hasAnyConstraints(null)).toBe(false);
  });

  it("returns false for empty object", () => {
    expect(hasAnyConstraints({})).toBe(false);
  });

  it("returns false for empty nested objects", () => {
    expect(hasAnyConstraints({ budgets: {}, sandbox: {}, toolchain: {}, diff: {} })).toBe(false);
  });

  it("returns true when budgets has values", () => {
    expect(hasAnyConstraints({ budgets: { max_retries: 3 } })).toBe(true);
  });

  it("returns true when sandbox has values", () => {
    expect(hasAnyConstraints({ sandbox: { isolated: true } })).toBe(true);
  });

  it("returns true when toolchain has values", () => {
    expect(hasAnyConstraints({ toolchain: { allowed_tools: ["grep"] } })).toBe(true);
  });

  it("returns true when diff has values", () => {
    expect(hasAnyConstraints({ diff: { max_files: 10 } })).toBe(true);
  });

  it("returns true for redaction_level", () => {
    expect(hasAnyConstraints({ redaction_level: "full" })).toBe(true);
  });
});

describe("extractWorkflowRules", () => {
  it("returns empty array when no rules present", () => {
    expect(extractWorkflowRules(null, null)).toEqual([]);
    expect(extractWorkflowRules({ foo: 1 }, { bar: 2 })).toEqual([]);
  });

  it("extracts from config.policy_rules", () => {
    const rules = extractWorkflowRules(
      { policy_rules: [{ id: "r1", decision: "deny" }] },
      null,
    );
    expect(rules).toHaveLength(1);
    expect(rules[0].id).toBe("r1");
    expect(rules[0].decision).toBe("deny");
  });

  it("extracts from config.rules", () => {
    const rules = extractWorkflowRules(
      { rules: [{ id: "r2", decision: "allow", description: "Default allow" }] },
      null,
    );
    expect(rules).toHaveLength(1);
    expect(rules[0].id).toBe("r2");
    expect(rules[0].description).toBe("Default allow");
  });

  it("falls back to metadata.policy_rules", () => {
    const rules = extractWorkflowRules(null, {
      policy_rules: [{ id: "r3", decision: "require_approval", topics: ["job.deploy"] }],
    });
    expect(rules).toHaveLength(1);
    expect(rules[0].topics).toEqual(["job.deploy"]);
  });

  it("filters out entries without id", () => {
    const rules = extractWorkflowRules(
      { policy_rules: [{ decision: "deny" }, { id: "valid", decision: "allow" }] },
      null,
    );
    expect(rules).toHaveLength(1);
    expect(rules[0].id).toBe("valid");
  });

  it("handles non-array values gracefully", () => {
    expect(extractWorkflowRules({ policy_rules: "not-an-array" }, null)).toEqual([]);
    expect(extractWorkflowRules({ policy_rules: 42 }, null)).toEqual([]);
  });
});
