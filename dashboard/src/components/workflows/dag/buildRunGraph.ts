import type { Node, Edge } from "reactflow";
import type { Workflow, WorkflowRun, RunStatus } from "../../../api/types";
import type { RunOverlayNodeData } from "./RunOverlayNode";
import { markCriticalPath, colorEdgesByStatus } from "./dagStyles";

// ---------------------------------------------------------------------------
// Layout constants (match WorkflowCanvas)
// ---------------------------------------------------------------------------

const Y_STEP = 140;
const GRID = 200;

// ---------------------------------------------------------------------------
// buildRunGraph
// ---------------------------------------------------------------------------

/**
 * Convert a Workflow definition + optional WorkflowRun into ReactFlow
 * nodes and edges with run state overlaid on each node.
 */
export function buildRunGraph(
  workflow: Workflow,
  run?: WorkflowRun | null,
): { nodes: Node<RunOverlayNodeData>[]; edges: Edge[] } {
  const steps = workflow.steps ?? [];

  // Build lookup for run step data by step ID
  const runStepMap = new Map<
    string,
    {
      status?: RunStatus;
      duration?: number;
      error?: string;
      output?: Record<string, unknown>;
    }
  >();

  if (run?.steps) {
    for (const rs of run.steps) {
      let duration: number | undefined;
      if (rs.startedAt && rs.completedAt) {
        duration =
          new Date(rs.completedAt).getTime() -
          new Date(rs.startedAt).getTime();
      }
      runStepMap.set(rs.id, {
        status: rs.status,
        duration,
        error: rs.error,
        output: rs.output,
      });
    }
  }

  // Index for positioning
  const idxMap = new Map<string, number>();
  steps.forEach((step, i) => idxMap.set(step.id, i));

  // Build nodes
  const nodes: Node<RunOverlayNodeData>[] = steps.map((step, i) => {
    const deps = step.dependsOn ?? [];
    let x = 300;
    let y = i * Y_STEP + 40;

    // Position below first dependency
    if (deps.length > 0) {
      const parentIdx = idxMap.get(deps[0]);
      if (parentIdx !== undefined) {
        y = parentIdx * Y_STEP + Y_STEP + 40;
      }
    }

    // Spread siblings horizontally
    const siblings = steps.filter(
      (s) =>
        s.id !== step.id &&
        JSON.stringify(s.dependsOn) === JSON.stringify(deps),
    );
    const sibIdx = siblings.findIndex((s) => s.id === step.id);
    if (sibIdx > 0) {
      x += sibIdx * GRID;
    }

    // Run data overlay
    const runStep = runStepMap.get(step.id);

    // Safety decision from step config (job steps store it)
    const safetyDecision =
      step.type === "job" && runStep?.output?.safetyDecision
        ? (runStep.output.safetyDecision as { type: string })
        : undefined;

    const data: RunOverlayNodeData = {
      label: step.name || step.id,
      stepType: step.type,
      ...(run
        ? {
            runStatus: runStep?.status,
            duration: runStep?.duration,
            error: runStep?.error,
            safetyDecision,
          }
        : {}),
    };

    return {
      id: step.id,
      type: "runOverlay",
      position: { x, y },
      data,
    };
  });

  // Build edges
  let edges: Edge[] = [];
  for (const step of steps) {
    for (const dep of step.dependsOn ?? []) {
      edges.push({
        id: `e-${dep}-${step.id}`,
        source: dep,
        target: step.id,
        type: "smoothstep",
        animated: false,
        style: { strokeWidth: 1.5, stroke: "var(--border)" },
      });
    }
  }

  // Apply run-state edge styling
  if (run) {
    const stepStatusMap = new Map<string, RunStatus>();
    for (const rs of run.steps ?? []) {
      if (rs.status) stepStatusMap.set(rs.id, rs.status);
    }
    edges = colorEdgesByStatus(edges, stepStatusMap);
    edges = markCriticalPath(steps, edges);
  }

  return { nodes, edges };
}
