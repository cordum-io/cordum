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

  // EDGE-052 — five-section round-trip + 2-section legacy backward compat.

  it("round-trips all 5 sections (input + output + edge_action + mcp_tool + invariants)", () => {
    const yaml = `default_decision: deny
rules:
  - id: input-allow-test
    match:
      topics: ["job.test"]
    decision: allow
output_policy:
  enabled: true
  fail_mode: closed
output_rules:
  - id: out-secret
    severity: high
    decision: quarantine
    match:
      detectors: ["secret_leak"]
edge_action_rules:
  - id: edge-deny-secret
    match:
      topics: ["job.edge.action"]
      labels:
        path.class: secret
    decision: deny
mcp_tool_rules:
  - id: mcp-deny-fs
    match:
      mcp:
        deny_tools: ["fs.read"]
    decision: deny
invariants:
  - id: inv-deny-secret-paths
    match:
      topics: ["job.edge.action"]
    decision: deny
    reason: SecOps invariant
`;
    const parsed = parseGlobalPolicyYaml(yaml);
    expect(parsed.valid).toBe(true);
    expect(parsed.policy.edgeActionRules?.map((r) => r.id)).toEqual(["edge-deny-secret"]);
    expect(parsed.policy.mcpToolRules?.map((r) => r.id)).toEqual(["mcp-deny-fs"]);
    expect(parsed.policy.invariants?.map((r) => r.id)).toEqual(["inv-deny-secret-paths"]);
    expect(parsed.policy.mcpToolRules?.[0]?.match.mcp.denyTools).toEqual(["fs.read"]);

    const serialized = serializeGlobalPolicyYaml(parsed.policy);
    const reparsed = parseGlobalPolicyYaml(serialized);
    expect(reparsed.valid).toBe(true);
    expect(reparsed.policy.edgeActionRules?.map((r) => r.id)).toEqual(["edge-deny-secret"]);
    expect(reparsed.policy.mcpToolRules?.map((r) => r.id)).toEqual(["mcp-deny-fs"]);
    expect(reparsed.policy.invariants?.[0]?.reason).toBe("SecOps invariant");
  });

  it("backward-compat: legacy 2-section YAML (input + output only) parses + serializes without sprouting empty new sections", () => {
    const legacyYaml = `default_decision: deny
rules:
  - id: legacy-allow
    match:
      topics: ["job.test"]
    decision: allow
output_policy:
  enabled: true
  fail_mode: closed
output_rules: []
`;
    const parsed = parseGlobalPolicyYaml(legacyYaml);
    expect(parsed.valid).toBe(true);
    // New sections default to empty arrays — never undefined.
    expect(parsed.policy.edgeActionRules).toEqual([]);
    expect(parsed.policy.mcpToolRules).toEqual([]);
    expect(parsed.policy.invariants).toEqual([]);
    // No issue messages about the missing keys (they are optional).
    expect(
      parsed.issues.filter((i) => ["edge_action_rules", "mcp_tool_rules", "invariants"].includes(i.path)),
    ).toEqual([]);

    // Round-trip: serialized output MUST NOT add the empty new keys —
    // a legacy author saving the document round-trips identically.
    const serialized = serializeGlobalPolicyYaml(parsed.policy);
    expect(serialized).not.toContain("edge_action_rules");
    expect(serialized).not.toContain("mcp_tool_rules");
    expect(serialized).not.toContain("invariants:");
  });

  it("malformed new section yields a section-named warning issue but does not invalidate the document", () => {
    const yaml = `default_decision: deny
rules: []
edge_action_rules: not_an_array
`;
    const parsed = parseGlobalPolicyYaml(yaml);
    // Warning, not error — the rest of the document is still usable.
    expect(parsed.valid).toBe(true);
    expect(parsed.issues.some((i) => i.path === "edge_action_rules")).toBe(true);
    expect(parsed.policy.edgeActionRules).toEqual([]);
  });

  it("createDefaultGlobalPolicyDocument seeds new sections as empty arrays", async () => {
    const { createDefaultGlobalPolicyDocument } = await import("./globalPolicy");
    const doc = createDefaultGlobalPolicyDocument();
    expect(doc.edgeActionRules).toEqual([]);
    expect(doc.mcpToolRules).toEqual([]);
    expect(doc.invariants).toEqual([]);
  });
});
