import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { SimulatorResultsChain } from "./SimulatorResultsChain";
import { SimulatorDecisionSummary, getDecisionDisplayVariant } from "./SimulatorDecisionSummary";
import type { ExplainRuleStep } from "@/hooks/usePolicies";

describe("getDecisionDisplayVariant", () => {
  it("maps allow to healthy", () => {
    expect(getDecisionDisplayVariant("allow")).toBe("healthy");
  });

  it("maps deny to danger", () => {
    expect(getDecisionDisplayVariant("deny")).toBe("danger");
  });

  it("maps quarantine to warning", () => {
    expect(getDecisionDisplayVariant("quarantine")).toBe("warning");
  });

  it("maps require_approval to info", () => {
    expect(getDecisionDisplayVariant("require_approval")).toBe("info");
  });

  it("maps unknown decisions to muted", () => {
    expect(getDecisionDisplayVariant("something_else")).toBe("muted");
    expect(getDecisionDisplayVariant("")).toBe("muted");
  });
});

describe("SimulatorDecisionSummary", () => {
  it("renders decision, matched rule, and timing", () => {
    const markup = renderToStaticMarkup(
      <SimulatorDecisionSummary
        decision="deny"
        matchedRule="block-admin-tools"
        reason="Admin tools blocked by policy"
        evaluationTimeMs={12}
      />,
    );
    expect(markup).toContain("deny");
    expect(markup).toContain("block-admin-tools");
    expect(markup).toContain("Admin tools blocked by policy");
    expect(markup).toContain("12ms");
  });

  it("renders without optional fields", () => {
    const markup = renderToStaticMarkup(
      <SimulatorDecisionSummary decision="allow" />,
    );
    expect(markup).toContain("allow");
    expect(markup).toContain("Final decision");
  });
});

describe("SimulatorResultsChain", () => {
  it("renders empty-chain message when no steps", () => {
    const markup = renderToStaticMarkup(
      <SimulatorResultsChain chain={[]} />,
    );
    expect(markup).toContain("No evaluation chain returned");
  });

  it("renders chain steps with rule IDs and match status", () => {
    const chain: ExplainRuleStep[] = [
      {
        ruleId: "allow-default",
        decision: "allow",
        reason: "Default allow",
        matched: false,
        conditions: [
          { field: "topic", operator: "match", expected: "job.*", actual: "job.deploy", passed: true },
          { field: "capabilities", operator: "contains", expected: "code.admin", actual: "code.generate", passed: false },
        ],
      },
      {
        ruleId: "block-admin",
        decision: "deny",
        reason: "Admin tools blocked",
        matched: true,
        conditions: [
          { field: "capabilities", operator: "contains", expected: "code.admin", actual: "code.admin", passed: true },
        ],
      },
    ];

    const markup = renderToStaticMarkup(
      <SimulatorResultsChain chain={chain} />,
    );
    expect(markup).toContain("allow-default");
    expect(markup).toContain("block-admin");
    expect(markup).toContain("skipped");
    expect(markup).toContain("deny");
    expect(markup).toContain("Evaluation chain");
  });
});
