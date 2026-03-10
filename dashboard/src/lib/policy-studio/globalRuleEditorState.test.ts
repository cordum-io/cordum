import { describe, expect, it } from "vitest";
import { createEmptyGlobalInputRule } from "@/components/policy/global/GlobalRuleEditorDrawer";
import {
  getAdvancedConfiguredSummary,
  __globalRuleEditorStateInternal,
} from "./globalRuleEditorState";

const { hasMcpConfiguration, hasConstraintsConfiguration } =
  __globalRuleEditorStateInternal;

describe("hasMcpConfiguration", () => {
  it("returns false for empty MCP config", () => {
    const rule = createEmptyGlobalInputRule(1);
    expect(hasMcpConfiguration(rule)).toBe(false);
  });

  it("returns true when any MCP list is populated", () => {
    const rule = createEmptyGlobalInputRule(1);
    rule.match.mcp.allowServers = ["github"];
    expect(hasMcpConfiguration(rule)).toBe(true);
  });
});

describe("hasConstraintsConfiguration", () => {
  it("returns false for empty constraints", () => {
    const rule = createEmptyGlobalInputRule(1);
    expect(hasConstraintsConfiguration(rule)).toBe(false);
  });

  it("returns true when budget is set", () => {
    const rule = createEmptyGlobalInputRule(1);
    rule.constraints.budgets.maxRuntimeMs = 5000;
    expect(hasConstraintsConfiguration(rule)).toBe(true);
  });

  it("returns true when sandbox isolation is enabled", () => {
    const rule = createEmptyGlobalInputRule(1);
    rule.constraints.sandbox.isolated = true;
    expect(hasConstraintsConfiguration(rule)).toBe(true);
  });

  it("returns true when toolchain allowlist is populated", () => {
    const rule = createEmptyGlobalInputRule(1);
    rule.constraints.toolchain.allowedTools = ["search_issues"];
    expect(hasConstraintsConfiguration(rule)).toBe(true);
  });
});

describe("getAdvancedConfiguredSummary", () => {
  it("returns zero count for a fresh empty rule", () => {
    const rule = createEmptyGlobalInputRule(1);
    const summary = getAdvancedConfiguredSummary(rule);
    expect(summary.count).toBe(0);
    expect(Object.values(summary.groups).every((v) => v === false)).toBe(true);
  });

  it("counts actor_ids as one configured group", () => {
    const rule = createEmptyGlobalInputRule(1);
    rule.match.actorIds = ["worker-1"];
    const summary = getAdvancedConfiguredSummary(rule);
    expect(summary.count).toBe(1);
    expect(summary.groups.actorIds).toBe(true);
  });

  it("counts labels as one configured group", () => {
    const rule = createEmptyGlobalInputRule(1);
    rule.match.labels = { env: "prod" };
    const summary = getAdvancedConfiguredSummary(rule);
    expect(summary.count).toBe(1);
    expect(summary.groups.labels).toBe(true);
  });

  it("treats all MCP subfields as a single aggregate group", () => {
    const rule = createEmptyGlobalInputRule(1);
    rule.match.mcp.allowServers = ["github"];
    rule.match.mcp.denyTools = ["admin_tool"];
    const summary = getAdvancedConfiguredSummary(rule);
    expect(summary.count).toBe(1);
    expect(summary.groups.mcp).toBe(true);
  });

  it("excludes constraints from advanced count when decision is allow_with_constraints", () => {
    const rule = createEmptyGlobalInputRule(1);
    rule.decision = "allow_with_constraints";
    rule.constraints.budgets.maxRuntimeMs = 5000;
    const summary = getAdvancedConfiguredSummary(rule);
    expect(summary.groups.constraints).toBe(false);
  });

  it("includes constraints in advanced count for non-allow_with_constraints decisions", () => {
    const rule = createEmptyGlobalInputRule(1);
    rule.decision = "deny";
    rule.constraints.budgets.maxRuntimeMs = 5000;
    const summary = getAdvancedConfiguredSummary(rule);
    expect(summary.groups.constraints).toBe(true);
  });

  it("excludes remediations from advanced count when decision is deny", () => {
    const rule = createEmptyGlobalInputRule(1);
    rule.decision = "deny";
    rule.remediations = [{ id: "reroute", title: "reroute", summary: "", replacementTopic: "safe", replacementCapability: "", addLabels: {}, removeLabels: [] }];
    const summary = getAdvancedConfiguredSummary(rule);
    expect(summary.groups.remediations).toBe(false);
  });

  it("includes remediations in advanced count for non-deny decisions", () => {
    const rule = createEmptyGlobalInputRule(1);
    rule.decision = "allow";
    rule.remediations = [{ id: "reroute", title: "reroute", summary: "", replacementTopic: "safe", replacementCapability: "", addLabels: {}, removeLabels: [] }];
    const summary = getAdvancedConfiguredSummary(rule);
    expect(summary.groups.remediations).toBe(true);
  });

  it("counts all five groups when fully configured", () => {
    const rule = createEmptyGlobalInputRule(1);
    rule.decision = "throttle";
    rule.match.actorIds = ["worker-1"];
    rule.match.labels = { env: "prod" };
    rule.match.mcp.allowServers = ["github"];
    rule.constraints.budgets.maxRuntimeMs = 5000;
    rule.remediations = [{ id: "reroute", title: "reroute", summary: "", replacementTopic: "safe", replacementCapability: "", addLabels: {}, removeLabels: [] }];
    const summary = getAdvancedConfiguredSummary(rule);
    expect(summary.count).toBe(5);
    expect(Object.values(summary.groups).every((v) => v === true)).toBe(true);
  });
});
