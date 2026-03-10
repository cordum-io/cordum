import { describe, expect, it } from "vitest";
import type { PolicyRule } from "../../api/types";
import {
  createCondition,
  createConditionGroup,
  fromRule,
  isConditionGroup,
  toMatchCriteria,
  toYaml,
} from "./conditionTypes";
import type { Condition, ConditionGroup } from "./conditionTypes";

// ---------------------------------------------------------------------------
// isConditionGroup
// ---------------------------------------------------------------------------

describe("isConditionGroup", () => {
  it("returns true for objects with logic + conditions", () => {
    const group: ConditionGroup = {
      id: "g1",
      logic: "AND",
      conditions: [],
    };
    expect(isConditionGroup(group)).toBe(true);
  });

  it("returns false for plain conditions", () => {
    const cond: Condition = {
      id: "c1",
      field: "capability",
      operator: "in",
      value: [],
    };
    expect(isConditionGroup(cond)).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// createCondition
// ---------------------------------------------------------------------------

describe("createCondition", () => {
  it("creates default condition (capability, in, [])", () => {
    const cond = createCondition();
    expect(cond.field).toBe("capability");
    expect(cond.operator).toBe("in");
    expect(cond.value).toEqual([]);
    expect(cond.id).toMatch(/^cond-/);
  });

  it("preserves custom arguments", () => {
    const cond = createCondition("riskTag", "contains", "pii");
    expect(cond.field).toBe("riskTag");
    expect(cond.operator).toBe("contains");
    expect(cond.value).toBe("pii");
  });

  it("generates unique ids across calls", () => {
    const a = createCondition();
    const b = createCondition();
    expect(a.id).not.toBe(b.id);
  });
});

// ---------------------------------------------------------------------------
// createConditionGroup
// ---------------------------------------------------------------------------

describe("createConditionGroup", () => {
  it("creates default group (AND, empty conditions)", () => {
    const group = createConditionGroup();
    expect(group.logic).toBe("AND");
    expect(group.conditions).toEqual([]);
    expect(group.id).toMatch(/^cond-/);
  });

  it("preserves custom logic and conditions", () => {
    const child = createCondition("riskTag", "in", ["pii"]);
    const group = createConditionGroup("OR", [child]);
    expect(group.logic).toBe("OR");
    expect(group.conditions).toHaveLength(1);
    expect(group.conditions[0]).toBe(child);
  });
});

// ---------------------------------------------------------------------------
// fromRule
// ---------------------------------------------------------------------------

describe("fromRule", () => {
  it("converts rule with capabilities into condition group", () => {
    const rule = {
      id: "r1",
      matchCriteria: { capabilities: ["code.write", "code.exec"] },
      decisionType: "deny",
    } as any as PolicyRule;
    const group = fromRule(rule);
    expect(group.logic).toBe("AND");
    expect(group.conditions).toHaveLength(1);

    const cond = group.conditions[0] as Condition;
    expect(cond.field).toBe("capability");
    expect(cond.operator).toBe("in");
    expect(cond.value).toEqual(["code.write", "code.exec"]);
  });

  it("converts rule with riskTags into condition group", () => {
    const rule = {
      id: "r2",
      matchCriteria: { riskTags: ["pii", "secrets"] },
      decisionType: "require_approval",
    } as any as PolicyRule;
    const group = fromRule(rule);
    expect(group.conditions).toHaveLength(1);

    const cond = group.conditions[0] as Condition;
    expect(cond.field).toBe("riskTag");
    expect(cond.value).toEqual(["pii", "secrets"]);
  });

  it("converts rule with both capabilities and riskTags", () => {
    const rule = {
      id: "r3",
      matchCriteria: {
        capabilities: ["code.write"],
        riskTags: ["pii"],
      },
      decisionType: "deny",
    } as any as PolicyRule;
    const group = fromRule(rule);
    expect(group.conditions).toHaveLength(2);
    expect((group.conditions[0] as Condition).field).toBe("capability");
    expect((group.conditions[1] as Condition).field).toBe("riskTag");
  });

  it("returns empty group for rule with no match criteria", () => {
    const rule = {
      id: "r4",
      matchCriteria: {},
      decisionType: "allow",
    } as any as PolicyRule;
    const group = fromRule(rule);
    expect(group.conditions).toHaveLength(0);
  });

  it("preserves OR logic from rule", () => {
    const rule = {
      id: "r5",
      matchCriteria: { capabilities: ["a"] },
      decisionType: "deny",
      logic: "OR",
    } as any as PolicyRule;
    const group = fromRule(rule);
    expect(group.logic).toBe("OR");
  });

  it("defaults to AND logic when rule.logic is absent", () => {
    const rule = {
      id: "r6",
      matchCriteria: { capabilities: ["a"] },
      decisionType: "deny",
    } as any as PolicyRule;
    const group = fromRule(rule);
    expect(group.logic).toBe("AND");
  });
});

// ---------------------------------------------------------------------------
// toMatchCriteria
// ---------------------------------------------------------------------------

describe("toMatchCriteria", () => {
  it("extracts capabilities from flat conditions", () => {
    const group = createConditionGroup("AND", [
      createCondition("capability", "in", ["code.write", "code.exec"]),
    ]);
    const result = toMatchCriteria(group);
    expect(result.capabilities).toEqual(["code.write", "code.exec"]);
    expect(result.riskTags).toEqual([]);
    expect(result.logic).toBe("AND");
  });

  it("extracts riskTags from flat conditions", () => {
    const group = createConditionGroup("OR", [
      createCondition("riskTag", "in", ["pii", "secrets"]),
    ]);
    const result = toMatchCriteria(group);
    expect(result.riskTags).toEqual(["pii", "secrets"]);
    expect(result.logic).toBe("OR");
  });

  it("handles mixed capabilities and riskTags", () => {
    const group = createConditionGroup("AND", [
      createCondition("capability", "in", ["code.write"]),
      createCondition("riskTag", "in", ["pii"]),
    ]);
    const result = toMatchCriteria(group);
    expect(result.capabilities).toEqual(["code.write"]);
    expect(result.riskTags).toEqual(["pii"]);
  });

  it("includes groups field when nested groups exist", () => {
    const nested = createConditionGroup("OR", [
      createCondition("riskTag", "in", ["pii"]),
    ]);
    const group = createConditionGroup("AND", [
      createCondition("capability", "in", ["a"]),
      nested,
    ]);
    const result = toMatchCriteria(group);
    expect(result.groups).toHaveLength(1);
    expect(result.groups![0]).toBe(nested);
  });

  it("omits groups field when no nested groups", () => {
    const group = createConditionGroup("AND", [
      createCondition("capability", "in", ["a"]),
    ]);
    const result = toMatchCriteria(group);
    expect(result.groups).toBeUndefined();
  });

  it("handles string value (non-array)", () => {
    const group = createConditionGroup("AND", [
      createCondition("capability", "equals", "code.write"),
    ]);
    const result = toMatchCriteria(group);
    expect(result.capabilities).toEqual(["code.write"]);
  });
});

// ---------------------------------------------------------------------------
// toYaml
// ---------------------------------------------------------------------------

describe("toYaml", () => {
  it("produces correct YAML for single condition", () => {
    const group = createConditionGroup("AND", [
      createCondition("capability", "in", ["code.write"]),
    ]);
    const yaml = toYaml(group, "deny", "Too risky");
    expect(yaml).toContain("match:");
    expect(yaml).toContain("logic: AND");
    expect(yaml).toContain("conditions:");
    expect(yaml).toContain("field: capability");
    expect(yaml).toContain("operator: in");
    expect(yaml).toContain("value: [code.write]");
    expect(yaml).toContain('decision: deny');
    expect(yaml).toContain('reason: "Too risky"');
  });

  it("omits reason line when reason is empty", () => {
    const group = createConditionGroup("AND", []);
    const yaml = toYaml(group, "allow", "");
    expect(yaml).toContain("decision: allow");
    expect(yaml).not.toContain("reason:");
  });

  it("handles nested groups with proper indentation", () => {
    const nested = createConditionGroup("OR", [
      createCondition("riskTag", "in", ["pii"]),
    ]);
    const group = createConditionGroup("AND", [
      createCondition("capability", "in", ["a"]),
      nested,
    ]);
    const yaml = toYaml(group, "deny", "nested test");
    expect(yaml).toContain("- group:");
    expect(yaml).toContain("logic: OR");
    expect(yaml).toContain("field: riskTag");
  });

  it("handles string value in condition", () => {
    const group = createConditionGroup("AND", [
      createCondition("topic", "equals", "job.default"),
    ]);
    const yaml = toYaml(group, "allow", "topic match");
    expect(yaml).toContain("value: job.default");
  });
});
