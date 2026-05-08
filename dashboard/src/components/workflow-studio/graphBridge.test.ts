import { describe, expect, it } from "vitest";
import { definitionToGraph } from "./graphBridge";
import type { Workflow, WorkflowRun, WorkflowStep } from "@/api/types";

// Smoke coverage for the design-time + run-time governance fields the
// WorkflowNodeGovernanceOverlay consumes. graphBridge is the single
// thread-through point between WorkflowRun records and UnifiedNode data;
// regressions here would silently mute every overlay node.

function makeStep(overrides: Partial<WorkflowStep> = {}): WorkflowStep {
  return {
    id: "step-1",
    name: "step-1",
    type: "worker",
    ...overrides,
  };
}

const baseWorkflow: Workflow = {
  id: "wf-1",
  name: "wf-1",
  steps: [
    makeStep({ id: "s1", policyGate: "deny" }),
    makeStep({ id: "s2", policyGate: "allow" }),
    makeStep({ id: "s3" /* no policyGate */ }),
  ],
};

describe("graphBridge.definitionToGraph governance threading", () => {
  it("threads design-time policyGate from each WorkflowStep into UnifiedNodeData", () => {
    const graph = definitionToGraph(baseWorkflow, "view");
    expect(graph.nodes).toHaveLength(3);
    expect(graph.nodes[0]?.data.policyGate).toBe("deny");
    expect(graph.nodes[1]?.data.policyGate).toBe("allow");
    expect(graph.nodes[2]?.data.policyGate).toBeUndefined();
  });

  it("threads runtime safetyDecision + auditHash from the run-step record into UnifiedNodeData", () => {
    const run: WorkflowRun = {
      id: "run-1",
      workflowId: "wf-1",
      status: "succeeded",
      steps: [
        makeStep({
          id: "s1",
          status: "succeeded",
          output: { safetyDecision: { type: "deny" } },
          auditHash: "a".repeat(64),
        }),
      ],
      startedAt: "2026-05-08T10:00:00.000Z",
      completedAt: "2026-05-08T10:00:01.000Z",
    };
    const graph = definitionToGraph(baseWorkflow, "view", run);
    const s1 = graph.nodes[0]?.data;
    expect(s1?.runStatus).toBe("succeeded");
    expect(s1?.safetyDecision).toEqual({ type: "deny" });
    expect(s1?.auditHash).toBe("a".repeat(64));
  });

  it("falls back to runStep.output.auditHash when the top-level auditHash field is absent", () => {
    const run: WorkflowRun = {
      id: "run-1",
      workflowId: "wf-1",
      status: "succeeded",
      steps: [
        makeStep({
          id: "s1",
          status: "succeeded",
          // No top-level auditHash — surfaced via the output bag instead.
          output: { auditHash: "b".repeat(64), safetyDecision: { type: "allow" } },
        }),
      ],
      startedAt: "2026-05-08T10:00:00.000Z",
      completedAt: "2026-05-08T10:00:01.000Z",
    };
    const graph = definitionToGraph(baseWorkflow, "view", run);
    expect(graph.nodes[0]?.data.auditHash).toBe("b".repeat(64));
  });

  it("leaves runtime fields undefined when no run is supplied (design-time view)", () => {
    const graph = definitionToGraph(baseWorkflow, "view");
    const s1 = graph.nodes[0]?.data;
    expect(s1?.runStatus).toBeUndefined();
    expect(s1?.safetyDecision).toBeUndefined();
    expect(s1?.auditHash).toBeUndefined();
    // policyGate IS still present from the design-time WorkflowStep.
    expect(s1?.policyGate).toBe("deny");
  });

  it("leaves auditHash undefined for steps with no run-step or no audit data", () => {
    const run: WorkflowRun = {
      id: "run-1",
      workflowId: "wf-1",
      status: "succeeded",
      steps: [
        makeStep({ id: "s1", status: "succeeded" /* no output, no auditHash */ }),
      ],
      startedAt: "2026-05-08T10:00:00.000Z",
      completedAt: "2026-05-08T10:00:01.000Z",
    };
    const graph = definitionToGraph(baseWorkflow, "view", run);
    expect(graph.nodes[0]?.data.auditHash).toBeUndefined();
    // s2/s3 have no matching run-step → all run-time fields undefined.
    expect(graph.nodes[1]?.data.auditHash).toBeUndefined();
    expect(graph.nodes[2]?.data.auditHash).toBeUndefined();
  });
});
