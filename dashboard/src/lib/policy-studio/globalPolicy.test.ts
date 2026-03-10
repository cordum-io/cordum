import { describe, expect, it } from "vitest";
import {
  parseGlobalPolicyYaml,
  serializeGlobalPolicyYaml,
} from "./globalPolicy";

const SAMPLE_POLICY = `version: "1"
default_decision: deny
custom_root: keep-me
rules:
  - id: first
    match:
      topics: ["job.*"]
      capabilities: ["code.generate"]
    decision: require_approval
    reason: review first
    constraints:
      budgets:
        max_runtime_ms: 5000
    remediations:
      - id: reroute
        title: safer route
        replacement_topic: job.safe
  - id: second
    match:
      risk_tags: ["high"]
    decision: deny
output_policy:
  enabled: true
  fail_mode: closed
output_rules:
  - id: out-secret
    severity: high
    decision: quarantine
    match:
      detectors: ["secret_leak"]
`;

describe("globalPolicy parser", () => {
  it("round-trips global policy while preserving order and custom root fields", () => {
    const parsed = parseGlobalPolicyYaml(SAMPLE_POLICY);
    expect(parsed.valid).toBe(true);
    expect(parsed.policy.rules.map((rule) => rule.id)).toEqual(["first", "second"]);
    expect(parsed.policy.sourceRoot.custom_root).toBe("keep-me");

    const serialized = serializeGlobalPolicyYaml(parsed.policy);
    const reparsed = parseGlobalPolicyYaml(serialized);

    expect(reparsed.policy.sourceRoot.custom_root).toBe("keep-me");
    expect(reparsed.policy.rules.map((rule) => rule.id)).toEqual(["first", "second"]);
    expect(reparsed.policy.outputRules[0]?.id).toBe("out-secret");
    expect(reparsed.policy.outputPolicy.enabled).toBe(true);
    expect(reparsed.policy.outputPolicy.failMode).toBe("closed");
  });

  it("falls back to deny-safe defaults on invalid YAML", () => {
    const parsed = parseGlobalPolicyYaml("rules:\n  - id: bad\n    match: [");
    expect(parsed.valid).toBe(false);
    expect(parsed.policy.defaultDecision).toBe("deny");
    expect(parsed.policy.rules).toEqual([]);
    expect(parsed.issues.some((issue) => issue.severity === "error")).toBe(true);
  });

  it("normalizes invalid default_decision to deny with warning", () => {
    const parsed = parseGlobalPolicyYaml("default_decision: maybe\nrules: []");
    expect(parsed.policy.defaultDecision).toBe("deny");
    expect(parsed.issues.some((issue) => issue.path === "default_decision")).toBe(true);
  });

  it("preserves advanced fields through YAML↔Visual style round-trip edits", () => {
    const yaml = `default_decision: deny
rules:
  - id: keep-advanced
    decision: allow
    reason: baseline
    match:
      capabilities: ["code.generate"]
      actor_ids: ["worker-1"]
      labels:
        env: prod
      mcp:
        allow_servers: ["github"]
    constraints:
      budgets:
        max_runtime_ms: 2500
      toolchain:
        allowed_tools: ["search_issues"]
    remediations:
      - id: safer-path
        title: Use safer topic
        replacement_topic: job.safe
`;

    const parsed = parseGlobalPolicyYaml(yaml);
    expect(parsed.valid).toBe(true);
    parsed.policy.rules[0].reason = "edited in visual";

    const serialized = serializeGlobalPolicyYaml(parsed.policy);
    const reparsed = parseGlobalPolicyYaml(serialized);
    const rule = reparsed.policy.rules[0];

    expect(rule.reason).toBe("edited in visual");
    expect(rule.match.actorIds).toEqual(["worker-1"]);
    expect(rule.match.labels).toEqual({ env: "prod" });
    expect(rule.match.mcp.allowServers).toEqual(["github"]);
    expect(rule.constraints.budgets.maxRuntimeMs).toBe(2500);
    expect(rule.constraints.toolchain.allowedTools).toEqual(["search_issues"]);
    expect(rule.remediations[0]?.id).toBe("safer-path");
  });
});
