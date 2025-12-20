import type { Workflow, WorkflowNode } from "../types";

export type RunInputs = {
  prompt: string;
  filePath?: string;
  instruction?: string;
};

export type NodeResultLike = {
  result?: unknown;
  result_ptr?: unknown;
  context_ptr?: unknown;
  resultPtr?: unknown;
  contextPtr?: unknown;
};

export type TemplateContext = {
  inputs: RunInputs;
  nodeResults: Record<string, NodeResultLike>;
  prevResult: unknown;
  prevResultPtr?: unknown;
  prevContextPtr?: unknown;
  prevNodeId?: unknown;
};

function toTemplateString(value: unknown): string {
  if (value === null || value === undefined) {
    return "";
  }
  if (typeof value === "string") {
    return value;
  }
  if (typeof value === "number" || typeof value === "boolean" || typeof value === "bigint") {
    return String(value);
  }
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

function splitOnce(value: string, sep: string): [string, string] {
  const idx = value.indexOf(sep);
  if (idx < 0) {
    return [value, ""];
  }
  return [value.slice(0, idx), value.slice(idx + sep.length)];
}

function getPath(value: unknown, path: string): unknown {
  if (!path) {
    return value;
  }
  const parts = path.split(".").filter(Boolean);
  let cur: any = value;
  for (const p of parts) {
    if (cur === null || cur === undefined) {
      return undefined;
    }
    const isIndex = /^[0-9]+$/.test(p);
    cur = isIndex ? cur[Number(p)] : cur[p];
  }
  return cur;
}

export function resolveTemplate(expr: string, ctx: TemplateContext): unknown {
  const raw = expr.trim();

  if (raw.startsWith("input.")) {
    const key = raw.slice("input.".length);
    if (key === "prompt") return ctx.inputs.prompt ?? "";
    if (key === "filePath") return ctx.inputs.filePath ?? "";
    if (key === "instruction") return ctx.inputs.instruction ?? "";
    return "";
  }

  if (raw === "prev.result") {
    return ctx.prevResult ?? "";
  }
  if (raw.startsWith("prev.result.")) {
    const path = raw.slice("prev.result.".length);
    return getPath(ctx.prevResult, path);
  }
  if (raw === "prev.result_ptr" || raw === "prev.resultPtr") {
    return ctx.prevResultPtr ?? "";
  }
  if (raw === "prev.context_ptr" || raw === "prev.contextPtr") {
    return ctx.prevContextPtr ?? "";
  }
  if (raw === "prev.node_id" || raw === "prev.nodeId") {
    return ctx.prevNodeId ?? "";
  }

  if (raw.startsWith("node.")) {
    const rest = raw.slice("node.".length);
    const [nodeId, tail] = splitOnce(rest, ".");
    if (!nodeId || !tail) {
      return "";
    }
    const [root, path] = splitOnce(tail, ".");
    const node = ctx.nodeResults[nodeId];
    if (!node) {
      return "";
    }
    if (root === "result") {
      return getPath(node.result, path);
    }
    if (root === "result_ptr" || root === "resultPtr") {
      return node.result_ptr ?? node.resultPtr ?? "";
    }
    if (root === "context_ptr" || root === "contextPtr") {
      return node.context_ptr ?? node.contextPtr ?? "";
    }
    return "";
  }

  return "";
}

export function renderTemplate(template: string, ctx: TemplateContext): string {
  return template.replaceAll(/\$\{([^}]+)\}/g, (_match, expr) => toTemplateString(resolveTemplate(expr, ctx)));
}

function renderTemplateValue(template: string, ctx: TemplateContext): unknown {
  const m = template.match(/^\$\{([^}]+)\}$/);
  if (m) {
    return resolveTemplate(m[1], ctx);
  }
  return renderTemplate(template, ctx);
}

export function topoSortTaskNodes(workflow: Workflow): WorkflowNode[] {
  const tasks = workflow.nodes.filter((n) => n.data.kind === "task");
  if (tasks.length === 0) {
    return [];
  }
  const taskIds = new Set(tasks.map((t) => t.id));
  const indegree = new Map<string, number>();
  const out = new Map<string, string[]>();

  for (const t of tasks) {
    indegree.set(t.id, 0);
    out.set(t.id, []);
  }

  for (const e of deriveTaskEdges(workflow)) {
    if (!taskIds.has(e.source) || !taskIds.has(e.target)) {
      continue;
    }
    out.get(e.source)!.push(e.target);
    indegree.set(e.target, (indegree.get(e.target) ?? 0) + 1);
  }

  const queue = tasks
    .filter((t) => (indegree.get(t.id) ?? 0) === 0)
    .sort((a, b) => (a.position.x - b.position.x) || (a.position.y - b.position.y));

  const sorted: WorkflowNode[] = [];
  while (queue.length > 0) {
    const node = queue.shift()!;
    sorted.push(node);
    for (const to of out.get(node.id) ?? []) {
      const next = (indegree.get(to) ?? 0) - 1;
      indegree.set(to, next);
      if (next === 0) {
        const targetNode = tasks.find((t) => t.id === to);
        if (targetNode) {
          queue.push(targetNode);
          queue.sort((a, b) => (a.position.x - b.position.x) || (a.position.y - b.position.y));
        }
      }
    }
  }

  if (sorted.length !== tasks.length) {
    throw new Error("workflow graph contains a cycle");
  }

  return sorted;
}

export function deriveTaskEdges(workflow: Workflow): { source: string; target: string }[] {
  const nodes = workflow.nodes ?? [];
  const edges = workflow.edges ?? [];

  const nodeById = new Map(nodes.map((n) => [n.id, n]));
  const outgoing = new Map<string, string[]>();
  for (const e of edges) {
    if (!outgoing.has(e.source)) {
      outgoing.set(e.source, []);
    }
    outgoing.get(e.source)!.push(e.target);
  }

  const isTask = (id: string) => nodeById.get(id)?.data.kind === "task";
  const isPassThrough = (id: string) => nodeById.get(id)?.data.kind === "memory";

  const uniq = new Set<string>();
  const result: { source: string; target: string }[] = [];

  for (const n of nodes) {
    if (n.data.kind !== "task") {
      continue;
    }
    const sourceId = n.id;
    const visited = new Set<string>();

    const visit = (nextId: string) => {
      if (!nextId) return;
      if (isTask(nextId)) {
        if (nextId === sourceId) return;
        const key = `${sourceId}->${nextId}`;
        if (uniq.has(key)) return;
        uniq.add(key);
        result.push({ source: sourceId, target: nextId });
        return;
      }
      if (!isPassThrough(nextId)) {
        return;
      }
      if (visited.has(nextId)) {
        return;
      }
      visited.add(nextId);
      for (const to of outgoing.get(nextId) ?? []) {
        visit(to);
      }
    };

    for (const to of outgoing.get(sourceId) ?? []) {
      visit(to);
    }
  }

  return result;
}

export function computeWorkflowOutput(workflow: Workflow, ctx: TemplateContext): Record<string, unknown> {
  const outputNode = workflow.nodes.find((n) => n.data.kind === "output");
  if (!outputNode || outputNode.data.kind !== "output") {
    return { result: ctx.prevResult ?? null };
  }

  const out: Record<string, unknown> = {};
  for (const field of outputNode.data.outputs ?? []) {
    const key = field.key?.trim();
    if (!key) {
      continue;
    }
    out[key] = renderTemplateValue(field.template ?? "", ctx);
  }
  return out;
}
