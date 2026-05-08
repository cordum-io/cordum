import dagre from "@dagrejs/dagre";
import type { Node, Edge } from "reactflow";
import type { Workflow, WorkflowRun, WorkflowStep, RunStatus } from "@/api/types";
import type { UnifiedNodeData, StudioMode, StudioGraphData } from "./types";
import { colorEdgesByStatus, markCriticalPath } from "../workflows/dag/dagStyles";
import { logger } from "@/lib/logger";

// ---------------------------------------------------------------------------
// Layout constants
// ---------------------------------------------------------------------------

const NODE_WIDTH = 200;
const NODE_HEIGHT = 80;
const RANK_SEP = 100;
const NODE_SEP = 80;

// ---------------------------------------------------------------------------
// Dagre layout
// ---------------------------------------------------------------------------

function applyDagreLayout(
  nodes: Node<UnifiedNodeData>[],
  edges: Edge[],
): Node<UnifiedNodeData>[] {
  const g = new dagre.graphlib.Graph();
  g.setDefaultEdgeLabel(() => ({}));
  g.setGraph({ rankdir: "TB", ranksep: RANK_SEP, nodesep: NODE_SEP, marginx: 40, marginy: 40 });

  for (const node of nodes) {
    g.setNode(node.id, { width: NODE_WIDTH, height: NODE_HEIGHT });
  }
  for (const edge of edges) {
    g.setEdge(edge.source, edge.target);
  }

  try {
    dagre.layout(g);
  } catch (err) {
    logger.error("workflow-studio", "Dagre layout failed, using fallback positions", {
      nodeCount: nodes.length,
      edgeCount: edges.length,
      error: err instanceof Error ? err.message : String(err),
    });
    return nodes.map((node, i) => ({
      ...node,
      position: { x: 40, y: 40 + i * (NODE_HEIGHT + RANK_SEP) },
    }));
  }

  return nodes.map((node) => {
    const pos = g.node(node.id);
    if (!pos || typeof pos.x !== "number" || typeof pos.y !== "number") {
      logger.warn("workflow-studio", "Dagre returned invalid position for node", { nodeId: node.id });
      return node;
    }
    return {
      ...node,
      position: { x: pos.x - NODE_WIDTH / 2, y: pos.y - NODE_HEIGHT / 2 },
    };
  });
}

// ---------------------------------------------------------------------------
// Build run step lookup
// ---------------------------------------------------------------------------

interface RunStepInfo {
  status?: RunStatus;
  duration?: number;
  error?: string;
  output?: Record<string, unknown>;
  /** Audit-chain event hash from cordum-core `WorkflowRunStep.audit_hash`
   *  (task-913b6c6c). Threaded through to the governance overlay. */
  auditHash?: string;
}

function buildRunStepMap(run?: WorkflowRun | null): Map<string, RunStepInfo> {
  const map = new Map<string, RunStepInfo>();
  if (!run?.steps) return map;

  for (const rs of run.steps) {
    let duration: number | undefined;
    if (rs.startedAt && rs.completedAt) {
      duration = new Date(rs.completedAt).getTime() - new Date(rs.startedAt).getTime();
    }
    map.set(rs.id, {
      status: rs.status,
      duration,
      error: rs.error,
      output: rs.output,
      auditHash: rs.auditHash,
    });
  }
  return map;
}

// ---------------------------------------------------------------------------
// Extract dependency edges from a step
// ---------------------------------------------------------------------------

function extractEdges(step: WorkflowStep, existingEdges: Edge[]): Edge[] {
  const edges: Edge[] = [];
  const deps = step.depends_on ?? [];

  for (const dep of deps) {
    edges.push({
      id: `e-${dep}-${step.id}`,
      source: dep,
      target: step.id,
      type: "smoothstep",
      animated: false,
      style: { strokeWidth: 1.5, stroke: "var(--border)" },
    });
  }

  // Branch edges from config.branches (condition true/false, switch cases)
  const branches = (step.config as Record<string, unknown> | undefined)?.branches as
    | Record<string, string>
    | undefined;

  if (branches) {
    for (const [handleId, targetId] of Object.entries(branches)) {
      const alreadyExists = existingEdges.some((e) => e.source === step.id && e.target === targetId)
        || edges.some((e) => e.source === step.id && e.target === targetId);

      if (!alreadyExists) {
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
            fill: handleId === "true" ? "var(--success, #1f7a57)"
              : handleId === "false" ? "var(--danger, #b83a3a)"
              : "var(--accent, #0f7f7a)",
          },
          style: {
            strokeWidth: 1.5,
            stroke: handleId === "true" ? "var(--success, #1f7a57)"
              : handleId === "false" ? "var(--danger, #b83a3a)"
              : "var(--accent, #0f7f7a)",
          },
        });
      } else {
        // Patch existing edge with sourceHandle
        const existing = [...existingEdges, ...edges].find(
          (e) => e.source === step.id && e.target === targetId,
        );
        if (existing && !existing.sourceHandle) {
          existing.sourceHandle = handleId;
        }
      }
    }
  }

  // Parallel child edges
  if (step.type === "parallel") {
    const input = (step.input ?? {}) as Record<string, unknown>;
    const children = Array.isArray(input.steps)
      ? (input.steps as unknown[]).map(String).filter(Boolean)
      : Array.isArray((step.config as Record<string, unknown> | undefined)?.parallelSteps)
        ? ((step.config as Record<string, unknown>).parallelSteps as unknown[]).map(String).filter(Boolean)
        : [];

    for (const childId of children) {
      if (!childId || childId === step.id) continue;
      const alreadyExists = [...existingEdges, ...edges].some(
        (e) => e.source === step.id && e.target === childId,
      );
      if (!alreadyExists) {
        edges.push({
          id: `e-parallel-${step.id}-${childId}`,
          source: step.id,
          target: childId,
          type: "smoothstep",
          animated: false,
          style: { strokeDasharray: "4 3", strokeWidth: 1.5, stroke: "var(--border)" },
        });
      }
    }
  }

  return edges;
}

// ---------------------------------------------------------------------------
// Definition → Graph (for both view and edit)
// ---------------------------------------------------------------------------

export function definitionToGraph(
  workflow: Workflow,
  mode: StudioMode,
  run?: WorkflowRun | null,
): StudioGraphData {
  const steps = workflow.steps ?? [];
  const runStepMap = buildRunStepMap(run);
  const allEdges: Edge[] = [];

  // Build nodes
  const nodes: Node<UnifiedNodeData>[] = steps.map((step) => {
    const runStep = runStepMap.get(step.id);
    const safetyDecision =
      runStep?.output?.safetyDecision
        ? (runStep.output.safetyDecision as { type: string })
        : undefined;
    // auditHash sources: prefer the run-step record's own field (cordum-core
    // task-913b6c6c populates StepRun.audit_hash directly when available);
    // fall back to a nested output.audit_hash for forward-compat with any
    // run-record builder that surfaces it via the output bag.
    const auditHash =
      runStep?.auditHash ??
      (runStep?.output as { auditHash?: string } | undefined)?.auditHash;

    const data: UnifiedNodeData = {
      label: step.name || step.id,
      stepId: step.id,
      stepType: step.type,
      mode,
      // Config fields
      topic: step.topic,
      condition: step.condition,
      worker_id: step.worker_id,
      for_each: step.for_each,
      max_parallel: step.max_parallel,
      input: step.input,
      input_schema: step.input_schema,
      input_schema_id: step.input_schema_id,
      output_path: step.output_path,
      output_schema: step.output_schema,
      output_schema_id: step.output_schema_id,
      meta: step.meta,
      on_error: step.on_error,
      retry: step.retry,
      timeout_sec: step.timeout_sec,
      delay_sec: step.delay_sec,
      delay_until: step.delay_until,
      route_labels: step.route_labels,
      config: step.config,
      // Design-time policy hint (cordum-core task-913b6c6c). Threads through
      // for both view + edit modes; UnifiedNode renders the icon when set.
      policyGate: step.policyGate,
      // Run overlay
      ...(run ? {
        runStatus: runStep?.status,
        duration: runStep?.duration,
        error: runStep?.error,
        safetyDecision,
        auditHash,
      } : {}),
    };

    return {
      id: step.id,
      type: "unified",
      position: { x: 0, y: 0 }, // dagre will set this
      data,
      draggable: mode === "edit",
      connectable: mode === "edit",
    };
  });

  // Build edges
  for (const step of steps) {
    const stepEdges = extractEdges(step, allEdges);
    allEdges.push(...stepEdges);
  }

  // Apply dagre layout
  const layoutNodes = applyDagreLayout(nodes, allEdges);

  // Apply run-state edge styling when a run is selected
  let styledEdges = allEdges;
  if (run) {
    const stepStatusMap = new Map<string, RunStatus>();
    for (const rs of run.steps ?? []) {
      if (rs.status) stepStatusMap.set(rs.id, rs.status);
    }
    styledEdges = colorEdgesByStatus(styledEdges, stepStatusMap);
    styledEdges = markCriticalPath(steps, styledEdges);
  }

  return { nodes: layoutNodes, edges: styledEdges };
}

// ---------------------------------------------------------------------------
// Graph → Definition (for saving from edit mode)
// ---------------------------------------------------------------------------

export function graphToDefinition(
  nodes: Node<UnifiedNodeData>[],
  edges: Edge[],
  baseMeta?: Partial<Workflow>,
): Partial<Workflow> {
  const steps: WorkflowStep[] = nodes.map((n) => {
    const d = n.data;
    const incoming = edges.filter((e) => e.target === n.id).map((e) => e.source);

    // Build branches map from outgoing edges with sourceHandle
    const outgoing = edges.filter((e) => e.source === n.id && e.sourceHandle);
    const branches: Record<string, string> = {};
    for (const edge of outgoing) {
      branches[edge.sourceHandle!] = edge.target;
    }

    // Preserve legacy config with branches
    const config = { ...((d.config as Record<string, unknown>) ?? {}) };
    if (Object.keys(branches).length > 0) {
      config.branches = branches;
    }

    return {
      id: n.id,
      name: d.label ?? n.id,
      type: d.stepType ?? n.type ?? "job",
      depends_on: incoming.length > 0 ? incoming : undefined,
      topic: d.topic,
      condition: d.condition,
      worker_id: d.worker_id,
      for_each: d.for_each,
      max_parallel: d.max_parallel,
      input: d.input,
      input_schema: d.input_schema,
      input_schema_id: d.input_schema_id,
      output_path: d.output_path,
      output_schema: d.output_schema,
      output_schema_id: d.output_schema_id,
      meta: d.meta,
      on_error: d.on_error,
      retry: d.retry,
      timeout_sec: d.timeout_sec,
      delay_sec: d.delay_sec,
      delay_until: d.delay_until,
      route_labels: d.route_labels,
      config: Object.keys(config).length > 0 ? config : undefined,
    };
  });

  return { ...baseMeta, steps };
}
