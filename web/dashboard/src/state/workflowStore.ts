import { create } from "zustand";
import { readJSON, writeJSON } from "../lib/storage";
import { defaultWorkflows } from "../features/workflows/templates";
import { newID } from "../lib/id";
import type { Workflow, WorkflowEdge, WorkflowNode, WorkflowNodeData, WorkflowNodeType } from "../features/workflows/types";

const workflowsKey = "cortexos.workflows.v1";

type WorkflowState = {
  workflows: Workflow[];
  selectedWorkflowId: string | null;
  selectedNodeId: string | null;

  selectWorkflow: (workflowId: string) => void;
  selectNode: (nodeId: string | null) => void;

  createWorkflow: (name?: string) => string;
  duplicateWorkflow: (workflowId: string) => string | null;
  deleteWorkflow: (workflowId: string) => void;
  renameWorkflow: (workflowId: string, name: string) => void;

  setWorkflowGraph: (workflowId: string, nodes: WorkflowNode[], edges: WorkflowEdge[]) => void;
  addNode: (workflowId: string, type: WorkflowNodeType, position: { x: number; y: number }) => string | null;
  addEdge: (workflowId: string, edge: WorkflowEdge) => void;
  updateNodeData: (workflowId: string, nodeId: string, updater: (cur: WorkflowNodeData) => WorkflowNodeData) => void;
  deleteNode: (workflowId: string, nodeId: string) => void;
};

function loadInitial(): Workflow[] {
  const stored = readJSON<Workflow[]>(workflowsKey);
  const seeded = defaultWorkflows();
  if (stored && Array.isArray(stored) && stored.length > 0) {
    const existingNames = new Set(stored.map((w) => w.name));
    const missing = seeded.filter((w) => !existingNames.has(w.name));
    if (missing.length === 0) {
      return stored;
    }
    const merged = [...stored, ...missing];
    writeJSON(workflowsKey, merged);
    return merged;
  }
  writeJSON(workflowsKey, seeded);
  return seeded;
}

let persistTimer: ReturnType<typeof setTimeout> | null = null;

function persist(workflows: Workflow[]) {
  if (persistTimer) {
    clearTimeout(persistTimer);
  }
  persistTimer = setTimeout(() => {
    writeJSON(workflowsKey, workflows);
    persistTimer = null;
  }, 250);
}

function clone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

export const useWorkflowStore = create<WorkflowState>((set, get) => {
  const workflows = loadInitial();
  const firstId = workflows[0]?.id ?? null;

  const updateWorkflow = (workflowId: string, updater: (wf: Workflow) => Workflow) => {
    set((cur) => {
      const idx = cur.workflows.findIndex((w) => w.id === workflowId);
      if (idx < 0) {
        return cur;
      }
      const next = [...cur.workflows];
      next[idx] = updater(next[idx]);
      persist(next);
      return { ...cur, workflows: next };
    });
  };

  return {
    workflows,
    selectedWorkflowId: firstId,
    selectedNodeId: null,

    selectWorkflow: (workflowId) => set({ selectedWorkflowId: workflowId, selectedNodeId: null }),
    selectNode: (nodeId) => set({ selectedNodeId: nodeId }),

    createWorkflow: (name) => {
      const id = newID("wf_");
      const inputId = newID("n_");
      const taskId = newID("n_");
      const outputId = newID("n_");
      const wf: Workflow = {
        id,
        name: name || "New Workflow",
        updatedAt: Date.now(),
        nodes: [
          {
            id: inputId,
            type: "input",
            position: { x: 80, y: 140 },
            data: {
              kind: "input",
              name: "Input",
              promptDefault: "",
              includeFilePath: false,
              filePathDefault: "",
              includeInstruction: false,
              instructionDefault: "",
            },
          },
          {
            id: taskId,
            type: "task",
            position: { x: 380, y: 140 },
            data: {
              kind: "task",
              name: "Task",
              topic: "job.echo",
              promptTemplate: "${input.prompt}",
              timeoutMs: 60_000,
              retries: 0,
            },
          },
          {
            id: outputId,
            type: "output",
            position: { x: 680, y: 140 },
            data: {
              kind: "output",
              name: "Output",
              outputs: [{ key: "result", template: "${prev.result}" }],
            },
          },
        ],
        edges: [
          { id: newID("e_"), source: inputId, target: taskId },
          { id: newID("e_"), source: taskId, target: outputId },
        ],
        variablesSchema: {},
      };
      set((cur) => {
        const next = [wf, ...cur.workflows];
        persist(next);
        return { workflows: next, selectedWorkflowId: id, selectedNodeId: null };
      });
      return id;
    },

    duplicateWorkflow: (workflowId) => {
      const wf = get().workflows.find((w) => w.id === workflowId);
      if (!wf) {
        return null;
      }
      const copy = clone(wf);
      copy.id = newID("wf_");
      copy.name = `${wf.name} (copy)`;
      copy.updatedAt = Date.now();
      const idMap = new Map<string, string>();
      copy.nodes = copy.nodes.map((n) => {
        const nextId = newID("n_");
        idMap.set(n.id, nextId);
        return { ...n, id: nextId };
      });
      copy.edges = copy.edges.map((e) => ({
        ...e,
        id: newID("e_"),
        source: idMap.get(e.source) ?? e.source,
        target: idMap.get(e.target) ?? e.target,
      }));
      set((cur) => {
        const next = [copy, ...cur.workflows];
        persist(next);
        return { workflows: next, selectedWorkflowId: copy.id, selectedNodeId: null };
      });
      return copy.id;
    },

    deleteWorkflow: (workflowId) => {
      set((cur) => {
        const next = cur.workflows.filter((w) => w.id !== workflowId);
        persist(next);
        const selectedWorkflowId =
          cur.selectedWorkflowId === workflowId ? next[0]?.id ?? null : cur.selectedWorkflowId;
        return { workflows: next, selectedWorkflowId, selectedNodeId: null };
      });
    },

    renameWorkflow: (workflowId, name) =>
      updateWorkflow(workflowId, (wf) => ({ ...wf, name: name.trim() || wf.name, updatedAt: Date.now() })),

    setWorkflowGraph: (workflowId, nodes, edges) =>
      updateWorkflow(workflowId, (wf) => ({ ...wf, nodes, edges, updatedAt: Date.now() })),

    addNode: (workflowId, type, position) => {
      const wf = get().workflows.find((w) => w.id === workflowId);
      if (!wf) {
        return null;
      }
      if (type === "memory" && wf.nodes.some((n) => n.data.kind === "memory")) {
        return null;
      }
      const id = newID("n_");
      const node: WorkflowNode = {
        id,
        type,
        position,
        data:
          type === "input"
            ? {
                kind: "input",
                name: "Input",
                promptDefault: "",
                includeFilePath: false,
                filePathDefault: "",
                includeInstruction: false,
                instructionDefault: "",
              }
            : type === "output"
              ? { kind: "output", name: "Output", outputs: [{ key: "result", template: "${prev.result}" }] }
              : type === "memory"
                ? { kind: "memory", name: "Memory", strategy: "run", customMemoryId: "" }
                : {
                  kind: "task",
                  name: "Task",
                  topic: "job.echo",
                  promptTemplate: "${input.prompt}",
                  timeoutMs: 60_000,
                  retries: 0,
                },
      };
      updateWorkflow(workflowId, (cur) => ({ ...cur, nodes: [...cur.nodes, node], updatedAt: Date.now() }));
      return id;
    },

    addEdge: (workflowId, edge) =>
      updateWorkflow(workflowId, (wf) => ({ ...wf, edges: [...wf.edges, edge], updatedAt: Date.now() })),

    updateNodeData: (workflowId, nodeId, updater) =>
      updateWorkflow(workflowId, (wf) => ({
        ...wf,
        nodes: wf.nodes.map((n) => (n.id === nodeId ? { ...n, data: updater(n.data) } : n)),
        updatedAt: Date.now(),
      })),

    deleteNode: (workflowId, nodeId) =>
      updateWorkflow(workflowId, (wf) => ({
        ...wf,
        nodes: wf.nodes.filter((n) => n.id !== nodeId),
        edges: wf.edges.filter((e) => e.source !== nodeId && e.target !== nodeId),
        updatedAt: Date.now(),
      })),
  };
});
