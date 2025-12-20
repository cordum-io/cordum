import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import ReactFlow, {
  Background,
  Controls,
  MiniMap,
  MarkerType,
  addEdge,
  applyEdgeChanges,
  applyNodeChanges,
  type Connection,
  type Edge,
  type EdgeChange,
  type Node,
  type NodeChange,
} from "reactflow";
import { HelpCircle, Layers, ListChecks, Maximize2, Plus } from "lucide-react";
import InputNode from "./nodeTypes/InputNode";
import TaskNode from "./nodeTypes/TaskNode";
import OutputNode from "./nodeTypes/OutputNode";
import MemoryNode from "./nodeTypes/MemoryNode";
import type { TaskNodeData, Workflow, WorkflowEdge, WorkflowNode } from "./types";
import { useInspectorStore } from "../../state/inspectorStore";

export default function WorkflowCanvas({
  workflow,
  onChange,
  onSelectNode,
  onCreateNode,
}: {
  workflow: Workflow;
  onChange: (nodes: WorkflowNode[], edges: WorkflowEdge[]) => void;
  onSelectNode: (nodeId: string | null) => void;
  onCreateNode: (type: "input" | "task" | "output" | "memory", position: { x: number; y: number }) => void;
}) {
  const [spaceDown, setSpaceDown] = useState(false);
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);
  const rfWrapper = useRef<HTMLDivElement | null>(null);
  const rfInstance = useRef<any>(null);
  const showInspector = useInspectorStore((s) => s.show);

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.code === "Space" && !(e.target instanceof HTMLInputElement) && !(e.target instanceof HTMLTextAreaElement)) {
        setSpaceDown(true);
        e.preventDefault();
      }
    };
    const onKeyUp = (e: KeyboardEvent) => {
      if (e.code === "Space") {
        setSpaceDown(false);
        e.preventDefault();
      }
    };
    window.addEventListener("keydown", onKeyDown, { passive: false });
    window.addEventListener("keyup", onKeyUp, { passive: false });
    return () => {
      window.removeEventListener("keydown", onKeyDown);
      window.removeEventListener("keyup", onKeyUp);
    };
  }, []);

  const nodeTypes = useMemo(
    () => ({
      input: InputNode,
      task: TaskNode,
      output: OutputNode,
      memory: MemoryNode,
    }),
    [],
  );

  const commit = useCallback(
    (nextNodes: WorkflowNode[], nextEdges: WorkflowEdge[]) => {
      onChange(nextNodes, nextEdges);
    },
    [onChange],
  );

  const onNodesChange = useCallback(
    (changes: NodeChange[]) => {
      const next = applyNodeChanges(changes, workflow.nodes) as WorkflowNode[];
      commit(next, workflow.edges);
    },
    [commit, workflow.edges, workflow.nodes],
  );

  const onEdgesChange = useCallback(
    (changes: EdgeChange[]) => {
      const next = applyEdgeChanges(changes, workflow.edges) as WorkflowEdge[];
      commit(workflow.nodes, next);
    },
    [commit, workflow.edges, workflow.nodes],
  );

  const onConnect = useCallback(
    (conn: Connection) => {
      if (!conn.source || !conn.target) {
        return;
      }
      if (conn.source === conn.target) {
        return;
      }
      const nodeById = new Map(workflow.nodes.map((n) => [n.id, n]));
      const source = nodeById.get(conn.source);
      const target = nodeById.get(conn.target);
      if (!source || !target) {
        return;
      }
      // Basic guardrails: input has no inbound edges, output has no outbound edges.
      if (target.data.kind === "input" || source.data.kind === "output") {
        return;
      }
      if (workflow.edges.some((e) => e.source === conn.source && e.target === conn.target)) {
        return;
      }
      const edge: Edge = {
        id: `e_${conn.source}-${conn.target}-${Date.now()}`,
        source: conn.source,
        target: conn.target,
        type: "smoothstep",
        animated: false,
        markerEnd: { type: MarkerType.ArrowClosed, width: 16, height: 16, color: "rgba(255,255,255,0.45)" },
        style: { stroke: "rgba(255,255,255,0.25)", strokeWidth: 1.5 },
      };
      const next = addEdge(edge, workflow.edges) as WorkflowEdge[];
      commit(workflow.nodes, next);
    },
    [commit, workflow.edges, workflow.nodes],
  );

  const onSelectionChange = useCallback(
    ({ nodes: selectedNodes }: { nodes: Node[] }) => {
      const nodeId = selectedNodes.length === 1 ? selectedNodes[0].id : null;
      setSelectedNodeId(nodeId);
      onSelectNode(nodeId);
    },
    [onSelectNode],
  );

  const [isDragging, setIsDragging] = useState(false);

  const onDragOver = useCallback((evt: React.DragEvent) => {
    evt.preventDefault();
    evt.dataTransfer.dropEffect = "move";
  }, []);

  const onDragEnter = useCallback(() => {
    setIsDragging(true);
  }, []);

  const onDragLeave = useCallback(() => {
    setIsDragging(false);
  }, []);

  const onDrop = useCallback(
    (evt: React.DragEvent) => {
      evt.preventDefault();
      setIsDragging(false);
      const raw = evt.dataTransfer.getData("application/coretex-node-type");
      if (!raw || (raw !== "input" && raw !== "task" && raw !== "output" && raw !== "memory")) {
        return;
      }
      const bounds = rfWrapper.current?.getBoundingClientRect();
      if (!bounds) {
        return;
      }
      const pos = { x: evt.clientX - bounds.left, y: evt.clientY - bounds.top };
      const projected = rfInstance.current?.screenToFlowPosition?.(pos) ?? pos;
      onCreateNode(raw, projected);
    },
    [onCreateNode],
  );

  const styledEdges = useMemo(() => {
    const selected = selectedNodeId;
    return workflow.edges.map((e) => {
      const connected = selected ? (e.source === selected || e.target === selected) : false;
      const dim = selected ? !connected : false;
      const stroke = connected
        ? "rgba(102,102,245,0.85)"
        : dim
          ? "rgba(255,255,255,0.08)"
          : "rgba(255,255,255,0.25)";
      return {
        ...e,
        type: e.type ?? "smoothstep",
        markerEnd: e.markerEnd ?? { type: MarkerType.ArrowClosed, width: 16, height: 16, color: stroke },
        style: {
          ...(e.style ?? {}),
          stroke,
          strokeWidth: connected ? 2 : 1.5,
        },
        animated: Boolean(e.animated) || connected,
      } satisfies WorkflowEdge;
    });
  }, [selectedNodeId, workflow.edges]);

  const centerToFlowPosition = useCallback((): { x: number; y: number } | null => {
    const bounds = rfWrapper.current?.getBoundingClientRect();
    if (!bounds) {
      return null;
    }
    const pos = { x: bounds.width / 2, y: bounds.height / 2 };
    return rfInstance.current?.screenToFlowPosition?.(pos) ?? pos;
  }, []);

  const addNodeAtCenter = useCallback(
    (type: "input" | "task" | "output" | "memory") => {
      const projected = centerToFlowPosition();
      if (!projected) {
        return;
      }
      onCreateNode(type, projected);
    },
    [centerToFlowPosition, onCreateNode],
  );

  const autoLayout = useCallback(() => {
    const nodes = workflow.nodes ?? [];
    const edges = workflow.edges ?? [];
    if (nodes.length === 0) {
      return;
    }

    const nodeById = new Map(nodes.map((n) => [n.id, n]));
    const indegree = new Map<string, number>();
    const outgoing = new Map<string, string[]>();
    const rank = new Map<string, number>();

    for (const n of nodes) {
      indegree.set(n.id, 0);
      outgoing.set(n.id, []);
      rank.set(n.id, 0);
    }

    for (const e of edges) {
      if (!nodeById.has(e.source) || !nodeById.has(e.target)) {
        continue;
      }
      if (e.source === e.target) {
        continue;
      }
      outgoing.get(e.source)!.push(e.target);
      indegree.set(e.target, (indegree.get(e.target) ?? 0) + 1);
    }

    const byTypeThenPos = (a: WorkflowNode, b: WorkflowNode) => {
      const typeOrder = (n: WorkflowNode) => (n.data.kind === "input" ? 0 : n.data.kind === "task" ? 1 : 2);
      return (typeOrder(a) - typeOrder(b)) || (a.position.x - b.position.x) || (a.position.y - b.position.y) || a.id.localeCompare(b.id);
    };

    const queue = nodes
      .filter((n) => (indegree.get(n.id) ?? 0) === 0)
      .slice()
      .sort(byTypeThenPos);

    const ordered: string[] = [];
    while (queue.length > 0) {
      const n = queue.shift()!;
      ordered.push(n.id);
      for (const to of outgoing.get(n.id) ?? []) {
        rank.set(to, Math.max(rank.get(to) ?? 0, (rank.get(n.id) ?? 0) + 1));
        const next = (indegree.get(to) ?? 0) - 1;
        indegree.set(to, next);
        if (next === 0) {
          const targetNode = nodeById.get(to);
          if (targetNode) {
            queue.push(targetNode);
            queue.sort(byTypeThenPos);
          }
        }
      }
    }

    if (ordered.length !== nodes.length) {
      showInspector(
        "Workflow Issue",
        <div className="space-y-2 text-sm">
          <div className="rounded-lg border border-red-500/30 bg-red-500/10 p-3 text-red-200">
            Graph contains a cycle, so auto-layout can’t determine ordering. Remove cyclic edges and try again.
          </div>
        </div>,
      );
      return;
    }

    const maxRank = Math.max(...Array.from(rank.values()));
    const groups = new Map<number, WorkflowNode[]>();
    for (const n of nodes) {
      const r = rank.get(n.id) ?? 0;
      if (!groups.has(r)) {
        groups.set(r, []);
      }
      groups.get(r)!.push(n);
    }

    for (const [r, group] of groups) {
      group.sort((a, b) => (a.position.y - b.position.y) || byTypeThenPos(a, b));
      // Keep output nodes last even if they share rank.
      if (r === maxRank) {
        group.sort((a, b) => {
          const ao = a.data.kind === "output" ? 1 : 0;
          const bo = b.data.kind === "output" ? 1 : 0;
          return (ao - bo) || (a.position.y - b.position.y);
        });
      }
    }

    const x0 = 80;
    const y0 = 80;
    const xGap = 320;
    const yGap = 140;

    const nextNodes = nodes.map((n) => {
      const r = rank.get(n.id) ?? 0;
      const group = groups.get(r) ?? [];
      const idx = Math.max(0, group.findIndex((g) => g.id === n.id));
      return {
        ...n,
        position: { x: x0 + r * xGap, y: y0 + idx * yGap },
      };
    });

    commit(nextNodes, edges);
    setTimeout(() => rfInstance.current?.fitView?.({ padding: 0.2, duration: 300 }), 0);
  }, [commit, showInspector, workflow.edges, workflow.nodes]);

  const validateWorkflow = useCallback(() => {
    const nodes = workflow.nodes ?? [];
    const edges = workflow.edges ?? [];

    const inputCount = nodes.filter((n) => n.data.kind === "input").length;
    const outputCount = nodes.filter((n) => n.data.kind === "output").length;
    const memoryCount = nodes.filter((n) => n.data.kind === "memory").length;
    const tasks = nodes.filter((n): n is WorkflowNode & { data: TaskNodeData } => n.data.kind === "task");

    const issues: { severity: "error" | "warn"; message: string }[] = [];

    if (tasks.length === 0) {
      issues.push({ severity: "error", message: "No task nodes. Add at least one Task to run a workflow." });
    }
    if (inputCount === 0) {
      issues.push({ severity: "warn", message: "No input node. Templates can still use prev.* but input.* will be empty." });
    } else if (inputCount > 1) {
      issues.push({ severity: "warn", message: `Multiple input nodes (${inputCount}). Studio expects a single input.` });
    }
    if (outputCount === 0) {
      issues.push({ severity: "warn", message: "No output node. Run output will default to the last task result." });
    } else if (outputCount > 1) {
      issues.push({ severity: "warn", message: `Multiple output nodes (${outputCount}). Studio uses the first output node only.` });
    }
    if (memoryCount > 1) {
      issues.push({ severity: "warn", message: `Multiple memory nodes (${memoryCount}). Studio only uses one memory configuration.` });
    }

    const nodeById = new Map(nodes.map((n) => [n.id, n]));
    const taskIds = new Set(tasks.map((t) => t.id));
    const indegree = new Map<string, number>();
    const out = new Map<string, string[]>();
    for (const t of tasks) {
      indegree.set(t.id, 0);
      out.set(t.id, []);
      if (!t.data.topic?.trim()) {
        issues.push({ severity: "error", message: `Task "${t.data.name || t.id}" is missing a topic.` });
      }
      if (!t.data.promptTemplate?.trim()) {
        issues.push({ severity: "warn", message: `Task "${t.data.name || t.id}" has an empty prompt template.` });
      }
    }

    for (const e of edges) {
      const source = nodeById.get(e.source);
      const target = nodeById.get(e.target);
      if (!source || !target) {
        continue;
      }
      if (target.data.kind === "input") {
        issues.push({ severity: "warn", message: `Edge into an Input node (${target.id}) is ignored.` });
      }
      if (source.data.kind === "output") {
        issues.push({ severity: "warn", message: `Edge out of an Output node (${source.id}) is ignored.` });
      }

      if (!taskIds.has(e.source) || !taskIds.has(e.target)) {
        continue;
      }
      out.get(e.source)!.push(e.target);
      indegree.set(e.target, (indegree.get(e.target) ?? 0) + 1);
    }

    const queue = tasks.filter((t) => (indegree.get(t.id) ?? 0) === 0);
    const ordered: string[] = [];
    while (queue.length > 0) {
      const n = queue.shift()!;
      ordered.push(n.id);
      for (const to of out.get(n.id) ?? []) {
        const next = (indegree.get(to) ?? 0) - 1;
        indegree.set(to, next);
        if (next === 0) {
          const node = tasks.find((t) => t.id === to);
          if (node) queue.push(node);
        }
      }
    }
    if (ordered.length !== tasks.length) {
      issues.push({ severity: "error", message: "Task graph contains a cycle. Remove cyclic Task → Task edges." });
    }

    // Show a helpful hint when tasks aren’t connected at all.
    const connectedTaskIds = new Set<string>();
    for (const e of edges) {
      if (taskIds.has(e.source) && taskIds.has(e.target)) {
        connectedTaskIds.add(e.source);
        connectedTaskIds.add(e.target);
      }
    }
    const isolated = tasks.filter((t) => !connectedTaskIds.has(t.id));
    if (isolated.length > 0 && tasks.length > 1) {
      issues.push({
        severity: "warn",
        message: `${isolated.length} task(s) have no Task → Task edges. Studio will still run them; add edges to define ordering/dataflow.`,
      });
    }

    showInspector(
      "Workflow Check",
      issues.length === 0 ? (
        <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/10 p-3 text-sm text-emerald-200">
          Looks good.
        </div>
      ) : (
        <div className="space-y-2 text-sm">
          {issues.map((it, idx) => (
            <div
              key={idx}
              className={[
                "rounded-lg border p-3",
                it.severity === "error"
                  ? "border-red-500/30 bg-red-500/10 text-red-200"
                  : "border-amber-500/30 bg-amber-500/10 text-amber-200",
              ].join(" ")}
            >
              {it.message}
            </div>
          ))}
        </div>
      ),
    );
  }, [showInspector, workflow.edges, workflow.nodes]);

  const hasInput = useMemo(() => workflow.nodes.some((n) => n.data.kind === "input"), [workflow.nodes]);
  const hasOutput = useMemo(() => workflow.nodes.some((n) => n.data.kind === "output"), [workflow.nodes]);
  const hasMemory = useMemo(() => workflow.nodes.some((n) => n.data.kind === "memory"), [workflow.nodes]);

  return (
    <div
      ref={rfWrapper}
      className={`relative h-full w-full ${isDragging ? "border-2 border-dashed border-primary" : ""}`}
      onDrop={onDrop}
      onDragOver={onDragOver}
      onDragEnter={onDragEnter}
      onDragLeave={onDragLeave}
    >
      <div className="pointer-events-none absolute left-3 top-3 z-10 flex flex-wrap gap-2">
        <div className="pointer-events-auto flex items-center gap-1 rounded-xl border border-primary-border bg-secondary-background/70 p-1 shadow-sm backdrop-blur">
          <button
            type="button"
            className="rounded-lg px-2 py-1 text-xs text-secondary-text hover:bg-tertiary-background"
            onClick={() => rfInstance.current?.fitView?.({ padding: 0.2, duration: 250 })}
            title="Fit to view"
          >
            <Maximize2 size={14} className="inline-block" /> <span className="ml-1 hidden sm:inline">Fit</span>
          </button>
          <button
            type="button"
            className="rounded-lg px-2 py-1 text-xs text-secondary-text hover:bg-tertiary-background"
            onClick={autoLayout}
            title="Auto layout"
          >
            <Layers size={14} className="inline-block" /> <span className="ml-1 hidden sm:inline">Layout</span>
          </button>
          <button
            type="button"
            className="rounded-lg px-2 py-1 text-xs text-secondary-text hover:bg-tertiary-background"
            onClick={validateWorkflow}
            title="Validate workflow"
          >
            <ListChecks size={14} className="inline-block" /> <span className="ml-1 hidden sm:inline">Check</span>
          </button>
        </div>

        <div className="pointer-events-auto flex items-center gap-1 rounded-xl border border-primary-border bg-secondary-background/70 p-1 shadow-sm backdrop-blur">
          <button
            type="button"
            disabled={hasInput}
            className={[
              "rounded-lg px-2 py-1 text-xs text-secondary-text",
              hasInput ? "cursor-not-allowed opacity-50" : "hover:bg-tertiary-background",
            ].join(" ")}
            onClick={() => addNodeAtCenter("input")}
            title={hasInput ? "Input already exists" : "Add an input node"}
          >
            <Plus size={14} className="inline-block" /> <span className="ml-1 hidden sm:inline">Input</span>
          </button>
          <button
            type="button"
            className="rounded-lg px-2 py-1 text-xs text-secondary-text hover:bg-tertiary-background"
            onClick={() => addNodeAtCenter("task")}
            title="Add a task node"
          >
            <Plus size={14} className="inline-block" /> <span className="ml-1 hidden sm:inline">Task</span>
          </button>
          <button
            type="button"
            disabled={hasMemory}
            className={[
              "rounded-lg px-2 py-1 text-xs text-secondary-text",
              hasMemory ? "cursor-not-allowed opacity-50" : "hover:bg-tertiary-background",
            ].join(" ")}
            onClick={() => addNodeAtCenter("memory")}
            title={hasMemory ? "Memory already exists" : "Add a memory node"}
          >
            <Plus size={14} className="inline-block" /> <span className="ml-1 hidden sm:inline">Memory</span>
          </button>
          <button
            type="button"
            disabled={hasOutput}
            className={[
              "rounded-lg px-2 py-1 text-xs text-secondary-text",
              hasOutput ? "cursor-not-allowed opacity-50" : "hover:bg-tertiary-background",
            ].join(" ")}
            onClick={() => addNodeAtCenter("output")}
            title={hasOutput ? "Output already exists" : "Add an output node"}
          >
            <Plus size={14} className="inline-block" /> <span className="ml-1 hidden sm:inline">Output</span>
          </button>
        </div>

        <button
          type="button"
          className="pointer-events-auto flex items-center gap-1 rounded-xl border border-amber-500/30 bg-amber-500/10 px-2 py-1 text-xs text-amber-200 hover:bg-amber-500/20"
          onClick={() =>
            showInspector(
              "Studio Help",
              <div className="space-y-2 text-sm">
                <div className="text-primary-text">Tips:</div>
                <ul className="list-disc space-y-1 pl-5 text-secondary-text">
                  <li>Connect Task → Task edges to define dependencies and what `${"{prev.*}"}` refers to.</li>
                  <li>Use the node inspector to insert template variables like `${"{prev.result.response}"}`.</li>
                  <li>Use “Layout” to auto-arrange nodes after editing.</li>
                </ul>
              </div>,
            )
          }
          title="Quick help"
        >
          <HelpCircle size={14} /> <span className="hidden sm:inline">Help</span>
        </button>
      </div>

      <ReactFlow
        nodes={workflow.nodes}
        edges={styledEdges}
        onInit={(inst) => (rfInstance.current = inst)}
        nodeTypes={nodeTypes}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onConnect={onConnect}
        onSelectionChange={onSelectionChange}
        fitView
        panOnDrag={spaceDown}
        selectionOnDrag={!spaceDown}
        deleteKeyCode={["Backspace", "Delete"]}
        proOptions={{ hideAttribution: true }}
      >
        <MiniMap
          pannable
          zoomable
          nodeColor={() => "rgba(255,255,255,0.25)"}
          maskColor="rgba(0,0,0,0.35)"
        />
        <Controls />
        <Background gap={16} size={1} color="rgba(255,255,255,0.08)" />
      </ReactFlow>
    </div>
  );
}
