import type { Edge } from "reactflow";
import type { WorkflowStep, RunStatus } from "../../../api/types";

// ---------------------------------------------------------------------------
// Critical path highlighting
// ---------------------------------------------------------------------------

/**
 * Walk the DAG to find the longest-duration chain among completed steps,
 * then mark those edges with a thicker accent stroke.
 */
export function markCriticalPath(
  steps: WorkflowStep[],
  edges: Edge[],
): Edge[] {
  // Build step duration map (only completed steps count)
  const durationMap = new Map<string, number>();
  const depsMap = new Map<string, string[]>();

  for (const step of steps) {
    const status = step.status;
    const isCompleted =
      status === "succeeded" || status === "failed";

    if (isCompleted && step.startedAt && step.completedAt) {
      const dur =
        new Date(step.completedAt).getTime() -
        new Date(step.startedAt).getTime();
      durationMap.set(step.id, Math.max(dur, 0));
    }

    depsMap.set(step.id, step.depends_on ?? step.dependsOn ?? []);
  }

  // Compute cumulative duration per node (longest path to each node)
  const cumulative = new Map<string, number>();
  const predecessor = new Map<string, string>(); // track which parent leads to longest path

  // Topological order via Kahn's algorithm
  const inDegree = new Map<string, number>();
  const children = new Map<string, string[]>();

  for (const step of steps) {
    inDegree.set(step.id, (step.depends_on ?? step.dependsOn ?? []).length);
    if (!children.has(step.id)) children.set(step.id, []);
    for (const dep of step.depends_on ?? step.dependsOn ?? []) {
      if (!children.has(dep)) children.set(dep, []);
      children.get(dep)!.push(step.id);
    }
  }

  const queue: string[] = [];
  for (const [id, deg] of inDegree) {
    if (deg === 0) {
      queue.push(id);
      cumulative.set(id, durationMap.get(id) ?? 0);
    }
  }

  while (queue.length > 0) {
    const current = queue.shift()!;
    const currentCum = cumulative.get(current) ?? 0;

    for (const child of children.get(current) ?? []) {
      const candidateCum = currentCum + (durationMap.get(child) ?? 0);
      if (candidateCum > (cumulative.get(child) ?? 0)) {
        cumulative.set(child, candidateCum);
        predecessor.set(child, current);
      }

      inDegree.set(child, (inDegree.get(child) ?? 1) - 1);
      if (inDegree.get(child) === 0) {
        queue.push(child);
      }
    }
  }

  // Find the terminal node with the highest cumulative duration
  let maxCum = 0;
  let maxNode = "";
  for (const [id, cum] of cumulative) {
    if (cum > maxCum) {
      maxCum = cum;
      maxNode = id;
    }
  }

  // Trace back to build the critical edge set
  const criticalEdges = new Set<string>();
  let cur = maxNode;
  while (predecessor.has(cur)) {
    const prev = predecessor.get(cur)!;
    criticalEdges.add(`e-${prev}-${cur}`);
    cur = prev;
  }

  // Apply styles
  return edges.map((edge) => {
    if (criticalEdges.has(edge.id)) {
      return {
        ...edge,
        style: { strokeWidth: 3, stroke: "var(--accent)" },
        animated: true,
      };
    }
    return {
      ...edge,
      style: {
        strokeWidth: 1.5,
        stroke: "var(--border)",
        ...(edge.style ?? {}),
      },
    };
  });
}

// ---------------------------------------------------------------------------
// Status-based edge coloring
// ---------------------------------------------------------------------------

const COMPLETED_STATUSES: Set<string> = new Set([
  "succeeded",
]);

const RUNNING_STATUSES: Set<string> = new Set([
  "running",
]);

const FAILED_STATUSES: Set<string> = new Set(["failed", "timed_out"]);

/**
 * Color edges based on the status of their source and target steps.
 */
export function colorEdgesByStatus(
  edges: Edge[],
  stepStatusMap: Map<string, RunStatus>,
): Edge[] {
  return edges.map((edge) => {
    const sourceStatus = stepStatusMap.get(edge.source) ?? "";
    const targetStatus = stepStatusMap.get(edge.target) ?? "";

    // Edge to a failed node
    if (FAILED_STATUSES.has(targetStatus)) {
      return {
        ...edge,
        style: { ...(edge.style ?? {}), stroke: "var(--danger, #b83a3a)" },
      };
    }

    // Edge from completed to running
    if (
      COMPLETED_STATUSES.has(sourceStatus) &&
      RUNNING_STATUSES.has(targetStatus)
    ) {
      return {
        ...edge,
        style: { ...(edge.style ?? {}), stroke: "var(--info, #3b82f6)" },
        animated: true,
      };
    }

    // Edge between two completed nodes
    if (
      COMPLETED_STATUSES.has(sourceStatus) &&
      COMPLETED_STATUSES.has(targetStatus)
    ) {
      return {
        ...edge,
        style: { ...(edge.style ?? {}), stroke: "var(--success, #1f7a57)" },
      };
    }

    return edge;
  });
}
