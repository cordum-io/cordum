import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { getAdvancedConfiguredSummary } from "@/lib/policy-studio/globalRuleEditorState";
import {
  __globalRuleEditorDrawerInternal,
  createEmptyGlobalInputRule,
  GlobalRuleEditorDrawer,
} from "./GlobalRuleEditorDrawer";

function hasValidationErrors(errors: object): boolean {
  return Object.values(errors as Record<string, string | undefined>).some(Boolean);
}

describe("GlobalRuleEditorDrawer", () => {
  it("renders field help tooltip content for key inputs", () => {
    const markup = renderToStaticMarkup(
      <GlobalRuleEditorDrawer
        open
        nextRuleIndex={1}
        existingRuleIds={[]}
        onClose={() => {}}
        onSave={() => {}}
      />,
    );

    expect(markup).toContain("Help for Rule ID");
    expect(markup).toContain("Unique identifier for this input rule");
    expect(markup).toContain("0 configured");
  });

  it("parses labels with strict key=value validation and keeps valid entries", () => {
    const parsed = __globalRuleEditorDrawerInternal.parseLabelsText(
      "env=prod\ninvalid\nowner=secops\nbad key=value",
    );
    expect(parsed.labels).toEqual({ env: "prod", owner: "secops" });
    expect(parsed.errors).toHaveLength(2);
  });

  it("blocks save when rule id is duplicate and required fields are invalid", () => {
    const draft = createEmptyGlobalInputRule(1);
    draft.id = "rule-1";
    draft.decision = "deny";
    draft.reason = "";

    const errors = __globalRuleEditorDrawerInternal.validateGlobalRuleDraft({
      draft,
      existingRuleIds: ["rule-1"],
      labelLineErrors: [],
      maxRuntimeMsInput: "0",
      maxConcurrentJobsInput: "",
    });

    expect(errors.ruleId).toContain("unique");
    expect(errors.reason).toContain("required");
    expect(errors.maxRuntimeMs).toContain("greater than 0");
    expect(hasValidationErrors(errors)).toBe(true);
  });

  it("passes validation for a fully valid constraints-aware rule", () => {
    const draft = createEmptyGlobalInputRule(2);
    draft.id = "allow-constrained-topic";
    draft.decision = "allow_with_constraints";
    draft.reason = "Allow constrained execution for vetted flow.";
    draft.match.topics = ["job.vetted.*"];
    draft.constraints.toolchain.allowedTools = ["search_issues"];
    draft.remediations = [
      {
        id: "remediate-1",
        title: "Use approved topic",
        summary: "",
        replacementTopic: "job.vetted.default",
        replacementCapability: "",
        addLabels: {},
        removeLabels: [],
        source: {},
      },
    ];

    const errors = __globalRuleEditorDrawerInternal.validateGlobalRuleDraft({
      draft,
      existingRuleIds: ["rule-1"],
      labelLineErrors: [],
      maxRuntimeMsInput: "1500",
      maxConcurrentJobsInput: "2",
    });

    expect(hasValidationErrors(errors)).toBe(false);
  });

  it("detects remediation mismatch for non-deny decisions", () => {
    expect(__globalRuleEditorDrawerInternal.hasRemediationDecisionMismatch("allow", [{
      id: "r-1",
      title: "Use safer workflow",
      summary: "",
      replacementTopic: "job.safe.*",
      replacementCapability: "",
      addLabels: {},
      removeLabels: [],
      source: {},
    }])).toBe(true);

    expect(__globalRuleEditorDrawerInternal.hasRemediationDecisionMismatch("deny", [{
      id: "r-1",
      title: "Use safer workflow",
      summary: "",
      replacementTopic: "job.safe.*",
      replacementCapability: "",
      addLabels: {},
      removeLabels: [],
      source: {},
    }])).toBe(false);
  });

  it("counts configured advanced groups deterministically", () => {
    const rule = createEmptyGlobalInputRule(3);
    rule.match.labels = { env: "prod" };
    rule.match.mcp.allowServers = ["github"];
    rule.match.actorIds = [];

    const summary = getAdvancedConfiguredSummary(rule);
    expect(summary.count).toBe(2);
    expect(summary.groups.labels).toBe(true);
    expect(summary.groups.mcp).toBe(true);
    expect(summary.groups.actorIds).toBe(false);
  });

  it("renders decision-specific guidance for throttle and require_approval", () => {
    const throttleRule = createEmptyGlobalInputRule(4);
    throttleRule.decision = "throttle";
    const throttleMarkup = renderToStaticMarkup(
      <GlobalRuleEditorDrawer
        open
        rule={throttleRule}
        existingRuleIds={[]}
        onClose={() => {}}
        onSave={() => {}}
      />,
    );
    expect(throttleMarkup).toContain("optional constraints are available in Advanced");

    const approvalRule = createEmptyGlobalInputRule(5);
    approvalRule.decision = "require_approval";
    const approvalMarkup = renderToStaticMarkup(
      <GlobalRuleEditorDrawer
        open
        rule={approvalRule}
        existingRuleIds={[]}
        onClose={() => {}}
        onSave={() => {}}
      />,
    );
    expect(approvalMarkup).toContain("Remediations for require_approval are available in Advanced");
  });

  it("preserves hidden constraints on non-primary decisions without save-blocking validation", () => {
    const rule = createEmptyGlobalInputRule(6);
    rule.decision = "allow";
    rule.constraints.budgets.maxRuntimeMs = 2000;

    const errors = __globalRuleEditorDrawerInternal.validateGlobalRuleDraft({
      draft: rule,
      existingRuleIds: [],
      labelLineErrors: [],
      maxRuntimeMsInput: "2000",
      maxConcurrentJobsInput: "",
    });

    expect(errors.maxRuntimeMs).toBeUndefined();
    expect(errors.reason).toBeUndefined();
    expect(hasValidationErrors(errors)).toBe(false);
  });
});
