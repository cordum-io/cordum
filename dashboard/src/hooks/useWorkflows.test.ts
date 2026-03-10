import { describe, expect, it } from "vitest";
import type { WorkflowRun, WorkflowStep } from "@/api/types";
import { __workflowsInternal } from "./useWorkflows";
import {
  resolveNodeMeta,
  normalizeStepType,
} from "@/pages/WorkflowCreatePage";
import { __runStreamInternal } from "./useRunStream";

function makeRun(overrides: Partial<WorkflowRun>): WorkflowRun {
  return {
    id: "run-default",
    workflowId: "wf-default",
    status: "pending",
    steps: [],
    startedAt: "2026-01-01T00:00:00.000Z",
    createdAt: "2026-01-01T00:00:00.000Z",
    ...overrides,
  };
}

describe("useWorkflows internals", () => {
  it("builds query strings while skipping empty values", () => {
    const query = __workflowsInternal.buildQuery({
      org_id: "acme",
      status: "running",
      tags: ["critical", ""],
      empty: "",
      nil: undefined,
    });

    expect(query).toContain("org_id=acme");
    expect(query).toContain("status=running");
    expect(query).toContain("tags=critical");
    expect(query).not.toContain("empty=");
    expect(query).not.toContain("nil=");
  });

  it("normalizes string arrays and parses durations/date input", () => {
    expect(__workflowsInternal.toStringArray(["a", " b ", 3])).toEqual([
      "a",
      "b",
      "3",
    ]);
    expect(__workflowsInternal.toStringArray("x, y, z")).toEqual(["x", "y", "z"]);
    expect(__workflowsInternal.toStringArray(undefined)).toEqual([]);

    expect(__workflowsInternal.parseDurationSeconds("1500ms")).toBe(2);
    expect(__workflowsInternal.parseDurationSeconds("2m")).toBe(120);
    expect(__workflowsInternal.parseDurationSeconds("1.5h")).toBe(5400);
    expect(__workflowsInternal.parseDurationSeconds("")).toBeUndefined();
    expect(__workflowsInternal.parseDurationSeconds("bad")).toBeUndefined();

    expect(__workflowsInternal.parseDateToISO("2026-03-01T12:00:00Z")).toContain("2026-03-01T12:00:00");
    expect(__workflowsInternal.parseDateToISO("not-a-date")).toBeUndefined();
  });

  it("builds step payloads from legacy config and direct fields", () => {
    const payload = __workflowsInternal.buildStepPayload({
      id: "step-parallel",
      name: "Parallel Step",
      type: "parallel",
      config: {
        timeout: "30s",
        duration: "10m",
        parallelSteps: ["step-a", "step-b"],
        completionStrategy: "n_of_m",
        requiredCount: 1,
        capabilities: ["cap.read", "cap.write"],
        requires: ["cap.audit"],
        riskTags: ["pii"],
      },
    });

    expect(payload.type).toBe("parallel");
    expect(payload.timeout_sec).toBe(30);
    expect(payload.delay_sec).toBe(600);
    expect((payload.input as Record<string, unknown>).steps).toEqual([
      "step-a",
      "step-b",
    ]);
    expect((payload.input as Record<string, unknown>).strategy).toBe("n_of_m");
    expect((payload.input as Record<string, unknown>).required).toBe(1);
    expect((payload.meta as Record<string, unknown>).capability).toBe("cap.read");
    expect((payload.meta as Record<string, unknown>).requires).toEqual([
      "cap.write",
      "cap.audit",
    ]);
  });

  it("builds workflow upsert payloads with normalized step map", () => {
    const payload = __workflowsInternal.toWorkflowUpsertPayload({
      id: "wf-1",
      name: "Workflow 1",
      timeout: 120,
      metadata: {
        orgId: "acme",
        teamId: "platform",
      },
      steps: [
        {
          id: "step-1",
          name: "Agent Step",
          type: "agent-task",
          topic: "job.default",
        },
      ],
    });

    expect(payload.id).toBe("wf-1");
    expect(payload.timeout_sec).toBe(120);
    expect(payload.org_id).toBe("acme");
    expect(payload.team_id).toBe("platform");
    expect((payload.steps as Record<string, unknown>)["step-1"]).toBeDefined();
    expect(
      ((payload.steps as Record<string, unknown>)["step-1"] as Record<string, unknown>).type,
    ).toBe("job");
  });

  it("prioritizes and sorts active runs by attention", () => {
    const waitingRun = makeRun({
      id: "run-waiting",
      status: "waiting",
      steps: [{ id: "s1", name: "s1", type: "step", status: "waiting" }],
      startedAt: "2026-01-01T00:05:00.000Z",
    });
    const runningRun = makeRun({
      id: "run-running",
      status: "running",
      startedAt: "2026-01-01T00:00:00.000Z",
    });
    const pendingRun = makeRun({
      id: "run-pending",
      status: "pending",
      startedAt: "2026-01-01T00:01:00.000Z",
    });
    const succeededRun = makeRun({
      id: "run-succeeded",
      status: "succeeded",
    });

    expect(__workflowsInternal.getAttentionPriority(waitingRun)).toBe(0);
    expect(__workflowsInternal.getAttentionPriority(runningRun)).toBe(2);
    expect(__workflowsInternal.getAttentionPriority(pendingRun)).toBe(3);

    const sorted = __workflowsInternal.sortByAttention([
      succeededRun,
      pendingRun,
      waitingRun,
      runningRun,
    ]);
    expect(sorted.map((run) => run.id)).toEqual([
      "run-waiting",
      "run-running",
      "run-pending",
    ]);
  });

  it("computes workflow stats from run history", () => {
    const stats = __workflowsInternal.computeWorkflowStats([
      makeRun({ id: "run-1", status: "running" }),
      makeRun({ id: "run-2", status: "succeeded" }),
      makeRun({ id: "run-3", status: "failed" }),
      makeRun({ id: "run-4", status: "cancelled" }),
    ]);

    expect(stats.lastRunStatus).toBe("running");
    expect(stats.sparkline).toEqual(["running", "succeeded", "failed", "cancelled"]);
    expect(stats.successRate).toBe(33);

    expect(__workflowsInternal.computeWorkflowStats([])).toEqual({
      successRate: 0,
      lastRunStatus: null,
      lastRunTime: null,
      sparkline: [],
    });
  });
});

// ---------------------------------------------------------------------------
// Crash resilience: builder node type resolution
// ---------------------------------------------------------------------------

describe("resolveNodeMeta — crash resilience", () => {
  it("returns valid meta for all known node types", () => {
    const known = ["worker", "approval", "condition", "delay", "loop", "parallel", "subworkflow"];
    for (const type of known) {
      const meta = resolveNodeMeta(type);
      expect(meta.type).toBe(type);
      expect(meta.label).toBeTruthy();
      expect(meta.icon).toBeDefined();
    }
  });

  it("returns UNKNOWN_NODE_META for unrecognized step types (no crash)", () => {
    const unknown = resolveNodeMeta("custom-step-type-xyz");
    expect(unknown.type).toBe("unknown");
    expect(unknown.label).toBe("Unknown");
    expect(unknown.icon).toBeDefined();
    expect(unknown.desc).toContain("Unsupported");
  });

  it("handles empty string type without crash", () => {
    const meta = resolveNodeMeta("");
    expect(meta.type).toBe("unknown");
  });
});

describe("normalizeStepType — backend type preservation", () => {
  it("maps 'job' to 'worker' and preserves original", () => {
    const result = normalizeStepType("job");
    expect(result.nodeType).toBe("worker");
    expect(result.originalType).toBe("job");
  });

  it("maps 'sub-workflow' to 'subworkflow' and preserves original", () => {
    const result = normalizeStepType("sub-workflow");
    expect(result.nodeType).toBe("subworkflow");
    expect(result.originalType).toBe("sub-workflow");
  });

  it("maps unknown backend type to 'unknown' while preserving the original", () => {
    const result = normalizeStepType("custom-backend-type");
    expect(result.nodeType).toBe("unknown");
    expect(result.originalType).toBe("custom-backend-type");
  });

  it("preserves known types directly", () => {
    const result = normalizeStepType("approval");
    expect(result.nodeType).toBe("approval");
    expect(result.originalType).toBe("approval");
  });
});

// ---------------------------------------------------------------------------
// Crash resilience: useRunStream cache patcher
// ---------------------------------------------------------------------------

describe("useRunStream cache patcher — crash resilience", () => {
  it("isRunEvent correctly classifies run events", () => {
    expect(__runStreamInternal.isRunEvent({ id: "1", type: "job.result", timestamp: "", payload: {} })).toBe(true);
    expect(__runStreamInternal.isRunEvent({ id: "2", type: "run.status_changed", timestamp: "", payload: {} })).toBe(true);
    expect(__runStreamInternal.isRunEvent({ id: "3", type: "job.submit", timestamp: "", payload: {} })).toBe(true);
    expect(__runStreamInternal.isRunEvent({ id: "4", type: "unknown.event", timestamp: "", payload: {} })).toBe(false);
  });

  it("extractRunId handles missing and varied payload shapes", () => {
    expect(__runStreamInternal.extractRunId({ id: "1", type: "t", timestamp: "", payload: { runId: "r1" } })).toBe("r1");
    expect(__runStreamInternal.extractRunId({ id: "2", type: "t", timestamp: "", payload: { run_id: "r2" } })).toBe("r2");
    expect(__runStreamInternal.extractRunId({ id: "3", type: "t", timestamp: "", payload: {} })).toBeUndefined();
  });

  it("extractStatus handles varied payload shapes", () => {
    expect(__runStreamInternal.extractStatus({ id: "1", type: "t", timestamp: "", payload: { status: "running" } })).toBe("running");
    expect(__runStreamInternal.extractStatus({ id: "2", type: "t", timestamp: "", payload: { newStatus: "failed" } })).toBe("failed");
    expect(__runStreamInternal.extractStatus({ id: "3", type: "t", timestamp: "", payload: {} })).toBeUndefined();
  });
});

// ---------------------------------------------------------------------------
// Crash resilience: sortByAttention with undefined steps
// ---------------------------------------------------------------------------

describe("sortByAttention — crash resilience", () => {
  it("handles runs with undefined steps without crash", () => {
    const runNoSteps = makeRun({
      id: "run-no-steps",
      status: "running",
      steps: undefined as unknown as WorkflowStep[],
    });
    // getAttentionPriority accesses run.steps ?? []
    expect(() => __workflowsInternal.getAttentionPriority(runNoSteps)).not.toThrow();
  });

  it("handles empty steps array", () => {
    const run = makeRun({ id: "run-empty", status: "running", steps: [] });
    expect(__workflowsInternal.getAttentionPriority(run)).toBe(2);
  });
});

// ---------------------------------------------------------------------------
// Crash resilience: buildStepPayload with minimal/missing fields
// ---------------------------------------------------------------------------

describe("buildStepPayload — crash resilience with malformed input", () => {
  it("handles step with no config (undefined)", () => {
    const payload = __workflowsInternal.buildStepPayload({
      id: "step-bare",
      name: "Bare Step",
      type: "job",
    });
    expect(payload.type).toBe("job");
    expect(payload.id).toBe("step-bare");
  });

  it("handles step with empty config", () => {
    const payload = __workflowsInternal.buildStepPayload({
      id: "step-empty-cfg",
      name: "Empty Config",
      type: "delay",
      config: {},
    });
    expect(payload.type).toBe("delay");
  });

  it("handles step with empty type (defaults to job)", () => {
    const payload = __workflowsInternal.buildStepPayload({
      id: "step-no-type",
      name: "No Type",
      type: "",
    });
    expect(payload.type).toBe("job");
  });

  it("handles step with unknown type (preserved as-is)", () => {
    const payload = __workflowsInternal.buildStepPayload({
      id: "step-unknown",
      name: "Unknown Type",
      type: "custom-thing",
    });
    expect(payload.type).toBe("custom-thing");
  });
});
