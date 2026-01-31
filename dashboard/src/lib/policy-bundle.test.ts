import { describe, expect, it } from "vitest";
import { parsePolicyBundle, serializePolicyRules, updateBundleRules } from "./policy-bundle";

describe("policy bundle parsing", () => {
  it("parses rules from yaml", () => {
    const content = `
version: v1
rules:
  - id: rule-1
    decision: deny
    reason: block prod
    match:
      topics: ["job.prod.delete"]
      tenants: ["default"]
      risk_tags: ["critical"]
    constraints:
      budgets:
        max_runtime_ms: 120000
`;
    const result = parsePolicyBundle(content);
    expect(result.error).toBeUndefined();
    expect(result.rules).toHaveLength(1);
    expect(result.rules[0].id).toBe("rule-1");
    expect(result.rules[0].decision).toBe("deny");
    expect(result.rules[0].match?.topics).toEqual(["job.prod.delete"]);
  });

  it("returns error on invalid yaml", () => {
    const result = parsePolicyBundle("rules: [");
    expect(result.error).toBeTruthy();
  });
});

describe("policy bundle serialization", () => {
  it("serializes rules back into yaml", () => {
    const rules = [
      {
        uid: "rule-1",
        id: "rule-1",
        decision: "allow",
        reason: "ok",
        match: { topics: ["job.ok"] },
      },
    ];
    const serialized = serializePolicyRules(rules);
    expect(serialized[0].id).toBe("rule-1");
    const yaml = updateBundleRules({ version: "v1" }, rules);
    expect(yaml).toMatch(/rules:/);
    expect(yaml).toMatch(/job\.ok/);
  });
});
