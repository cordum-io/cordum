import dagre from "@dagrejs/dagre";
import type { Node, Edge } from "reactflow";
import type { Workflow, WorkflowRun, RunStatus } from "../../../api/types";
import type { RunOverlayNodeData } from "./RunOverlayNode";
import { markCriticalPath, colorEdgesByStatus } from "./dagStyles";

// ---------------------------------------------------------------------------
// Layout constants
// ---------------------------------------------------------------------------

const NODE_WIDTH = 180;
const NODE_HEIGHT = 72;

// ---------------------------------------------------------------------------
// Dagre layout helper
// ---------------------------------------------------------------------------

function applyDagreLayout(
  nodes: Node<RunOverlayNodeData>[],
  edges: Edge[],
): Node<RunOverlayNodeData>[] {
  const g = new dagre.graphlib.Graph();
  g.setDefaultEdgeLabel(() => ({}));
  g.setGraph({ rankdir: "TB", ranksep: 100, nodesep: 80, marginx: 40, marginy: 40 });

  for (const node of nodes) {
    g.setNode(node.id, { width: NODE_WIDTH, height: NODE_HEIGHT });
  }
  for (const edge of edges) {
    g.setEdge(edge.source, edge.target);
  }

  dagre.layout(g);

  return nodes.map((node) => {
    const pos = g.node(node.id);
    return {
      ...node,
      position: {
        x: pos.x - NODE_WIDTH / 2,
        y: pos.y - NODE_HEIGHT / 2,
      },
    };
  });
}

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

  // Build nodes (position will be set by dagre)
  let nodes: Node<RunOverlayNodeData>[] = steps.map((step) => {
    const runStep = runStepMap.get(step.id);

    // Safety decision from step config (job steps store it)
    const safetyDecision =
      step.type === "job" && runStep?.output?.safetyDecision
        ? (runStep.output.safetyDecision as { type: string })
        : undefined;

    const data: RunOverlayNodeData = {
      label: step.name || step.id,
      stepType: step.type,
      condition: step.condition,
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
      position: { x: 0, y: 0 }, // dagre will set this
      data,
    };
  });

  // Build edges
  let edges: Edge[] = [];
  for (const step of steps) {
    for (const dep of step.depends_on ?? step.dependsOn ?? []) {
      edges.push({
        id: `e-${dep}-${step.id}`,
        source: dep,
        target: step.id,
        type: "smoothstep",
        animated: false,
        style: { strokeWidth: 1.5, stroke: "var(--border)" },
      });
    }

    // Condition/switch branch edges from config.branches
    const branches = (step.config as Record<string, unknown>)?.branches as
      | Record<string, string>
      | undefined;
    if (branches) {
      for (const [handleId, targetId] of Object.entries(branches)) {
        if (!edges.some((e) => e.source === step.id && e.target === targetId)) {
          edges.push({
            id: `e-${step.id}-${targetId}-${handleId}`,
            source: step.id,
            sourceHandle: handleId,
            target: targetId,
            type: "smoothstep",
            animated: false,
            label: handleId,
            labelStyle: {
              fontSize: 10,
              fontWeight: 600,
              fill: handleId === "true" ? "var(--success, #1f7a57)" : "var(--danger, #b83a3a)",
            },
            style: {
              strokeWidth: 1.5,
              stroke: handleId === "true" ? "var(--success, #1f7a57)" : "var(--danger, #b83a3a)",
            },
          });
        }
      }
    }
  }

  // Apply dagre layout
  nodes = applyDagreLayout(nodes, edges);

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
