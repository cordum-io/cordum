import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useMutation, useQuery } from "@tanstack/react-query";
import Panel from "../components/Panel";
import WorkflowCanvas from "../features/workflows/WorkflowCanvas";
import { useWorkflowStore } from "../state/workflowStore";
import { useInspectorStore } from "../state/inspectorStore";
import type { WorkflowNode, WorkflowNodeData } from "../features/workflows/types";
import { defaultTopics } from "../features/workflows/types";
import JsonViewer from "../components/JsonViewer";
import { useRunStore } from "../state/runStore";
import { useAuthStore } from "../state/authStore";
import Loading from "../components/Loading";
import EmptyState from "../components/EmptyState";
import {
  fetchWorkflows,
  startWorkflowRun,
  upsertWorkflow,
  type WorkflowDefinition,
  type WorkflowStepDefinition,
} from "../lib/api";
import {
  Cylinder,
  Database,
  Waypoints,
  Plus,
  ArrowRight,
  ArrowLeft,
} from "lucide-react";

export default function WorkflowsPage() {
  const navigate = useNavigate();
  const [mode, setMode] = useState<"studio" | "engine">("studio");
  const authStatus = useAuthStore((s) => s.status);

  const workflows = useWorkflowStore((s) => s.workflows);
  const selectedWorkflowId = useWorkflowStore((s) => s.selectedWorkflowId);
  const selectedNodeId = useWorkflowStore((s) => s.selectedNodeId);
  const selectWorkflow = useWorkflowStore((s) => s.selectWorkflow);
  const selectNode = useWorkflowStore((s) => s.selectNode);
  const createWorkflow = useWorkflowStore((s) => s.createWorkflow);
  const duplicateWorkflow = useWorkflowStore((s) => s.duplicateWorkflow);
  const deleteWorkflow = useWorkflowStore((s) => s.deleteWorkflow);
  const renameWorkflow = useWorkflowStore((s) => s.renameWorkflow);
  const setWorkflowGraph = useWorkflowStore((s) => s.setWorkflowGraph);
  const addNode = useWorkflowStore((s) => s.addNode);
  const deleteNode = useWorkflowStore((s) => s.deleteNode);

  const showInspector = useInspectorStore((s) => s.show);
  const closeInspector = useInspectorStore((s) => s.close);

  const startRun = useRunStore((s) => s.startRun);
  const [runModalOpen, setRunModalOpen] = useState(false);
  const [runPrompt, setRunPrompt] = useState("");
  const [runFilePath, setRunFilePath] = useState("");
  const [runInstruction, setRunInstruction] = useState("");

  const workflow = useMemo(
    () => workflows.find((w) => w.id === selectedWorkflowId) ?? workflows[0] ?? null,
    [selectedWorkflowId, workflows],
  );

  const [nameDraft, setNameDraft] = useState("");

  useEffect(() => {
    setNameDraft(workflow?.name ?? "");
  }, [workflow?.id]);

  useEffect(() => {
    if (mode !== "studio") {
      return;
    }
    if (!workflow || !selectedNodeId) {
      return;
    }
    const node = workflow.nodes.find((n) => n.id === selectedNodeId);
    if (!node) {
      return;
    }
    const kind = node.data.kind;
    const title =
      kind === "task"
        ? `Task: ${node.data.name}`
        : kind === "input"
          ? "Input"
          : kind === "memory"
            ? "Memory"
            : "Output";
    showInspector(
      title,
      <WorkflowNodeInspector
        workflowId={workflow.id}
        nodeId={node.id}
        data={node.data}
        onDelete={() => {
          deleteNode(workflow.id, node.id);
          selectNode(null);
          closeInspector();
        }}
      />,
    );
  }, [closeInspector, deleteNode, mode, selectNode, selectedNodeId, showInspector, workflow]);

  if (mode === "engine") {
    return <EngineWorkflowsPage onModeChange={setMode} />;
  }

  if (!workflow) {
    return (
      <div className="space-y-6">
        <Panel title="Workflows">
          <div className="text-sm text-tertiary-text">No workflows found.</div>
        </Panel>
      </div>
    );
  }

  const inputNode = workflow.nodes.find((n) => n.data.kind === "input");
  const includeFilePath = inputNode?.data.kind === "input" ? inputNode.data.includeFilePath : false;
  const includeInstruction = inputNode?.data.kind === "input" ? inputNode.data.includeInstruction : false;
  const hasTasks = workflow.nodes.some((n) => n.data.kind === "task");
  const canRun = hasTasks && !(authStatus === "missing_api_key" || authStatus === "invalid_api_key");

  return (
    <div className="grid grid-cols-[320px_1fr] gap-6">
      <div className="space-y-6">
        <Panel
          title="Workflows"
          right={
            <div className="flex items-center gap-2">
              <div className="rounded-lg border border-primary-border bg-secondary-background p-1 text-xs">
                <button
                  className="rounded-md bg-tertiary-background px-2 py-1 text-primary-text"
                  onClick={() => setMode("studio")}
                >
                  Studio
                </button>
                <button
                  className="rounded-md px-2 py-1 text-secondary-text hover:bg-tertiary-background"
                  onClick={() => setMode("engine")}
                >
                  Engine
                </button>
              </div>
              <button
                className="flex items-center gap-2 rounded-lg border border-primary-border bg-secondary-background px-2 py-1 text-xs text-secondary-text hover:bg-tertiary-background"
                onClick={() => createWorkflow()}
              >
                <Plus size={14} />
                New
              </button>
            </div>
          }
        >
          <div className="space-y-2">
            {workflows.map((w) => (
              <button
                key={w.id}
                type="button"
                onClick={() => selectWorkflow(w.id)}
                className={[
                  "w-full rounded-lg border px-3 py-2 text-left text-xs",
                  w.id === workflow.id
                    ? "border-secondary-border bg-tertiary-background text-primary-text"
                    : "border-primary-border bg-secondary-background text-secondary-text hover:bg-tertiary-background",
                ].join(" ")}
              >
                <div className="truncate font-semibold">{w.name}</div>
                <div className="mt-1 truncate font-mono text-[11px] text-tertiary-text">{w.id}</div>
              </button>
            ))}
          </div>
        </Panel>

        <Panel title="Node Library">
          <div className="space-y-3 text-xs">
            <div
              draggable
              onDragStart={(e) => e.dataTransfer.setData("application/coretex-node-type", "input")}
              className="flex cursor-grab items-center gap-3 rounded-lg border border-primary-border bg-secondary-background px-3 py-2 text-primary-text hover:bg-tertiary-background"
            >
              <div className="rounded-lg border border-primary-border bg-tertiary-background p-2">
                <ArrowLeft size={16} />
              </div>
              <div>
                <div className="font-semibold">Input</div>
                <div className="text-tertiary-text">Receives data</div>
              </div>
            </div>
            <div
              draggable
              onDragStart={(e) => e.dataTransfer.setData("application/coretex-node-type", "task")}
              className="flex cursor-grab items-center gap-3 rounded-lg border border-primary-border bg-secondary-background px-3 py-2 text-primary-text hover:bg-tertiary-background"
            >
              <div className="rounded-lg border border-primary-border bg-tertiary-background p-2">
                <Cylinder size={16} />
              </div>
              <div>
                <div className="font-semibold">Task</div>
                <div className="text-tertiary-text">Runs a job</div>
              </div>
            </div>
            <div
              draggable
              onDragStart={(e) => e.dataTransfer.setData("application/coretex-node-type", "memory")}
              className="flex cursor-grab items-center gap-3 rounded-lg border border-primary-border bg-secondary-background px-3 py-2 text-primary-text hover:bg-tertiary-background"
            >
              <div className="rounded-lg border border-primary-border bg-tertiary-background p-2">
                <Database size={16} />
              </div>
              <div>
                <div className="font-semibold">Memory</div>
                <div className="text-tertiary-text">Manage memory_id + inspect mem:*</div>
              </div>
            </div>
            <div
              draggable
              onDragStart={(e) => e.dataTransfer.setData("application/coretex-node-type", "output")}
              className="flex cursor-grab items-center gap-3 rounded-lg border border-primary-border bg-secondary-background px-3 py-2 text-primary-text hover:bg-tertiary-background"
            >
              <div className="rounded-lg border border-primary-border bg-tertiary-background p-2">
                <ArrowRight size={16} />
              </div>
              <div>
                <div className="font-semibold">Output</div>
                <div className="text-tertiary-text">Returns data</div>
              </div>
            </div>
          </div>
          <div className="mt-3 text-center text-xs text-tertiary-text">
            Drag and drop nodes to the canvas to build your workflow.
          </div>
        </Panel>
      </div>

      <div className="space-y-4">
        <Panel
          title={workflow.name}
          right={
            <div className="flex items-center gap-2">
              <button
                className={[
                  "rounded-lg border border-primary-border px-2 py-1 text-xs",
                  canRun
                    ? "bg-secondary-background text-secondary-text hover:bg-tertiary-background"
                    : "cursor-not-allowed bg-secondary-background/50 text-tertiary-text",
                ].join(" ")}
                disabled={!canRun}
                onClick={() => {
                  if (authStatus === "missing_api_key" || authStatus === "invalid_api_key") {
                    navigate("/settings");
                    return;
                  }
                  if (inputNode?.data.kind === "input") {
                    setRunPrompt(inputNode.data.promptDefault ?? "");
                    setRunFilePath(inputNode.data.filePathDefault ?? "");
                    setRunInstruction(inputNode.data.instructionDefault ?? "");
                  } else {
                    setRunPrompt("");
                    setRunFilePath("");
                    setRunInstruction("");
                  }
                  setRunModalOpen(true);
                }}
              >
                Run
              </button>
              <button
                className="rounded-lg border border-primary-border bg-secondary-background px-2 py-1 text-xs text-secondary-text hover:bg-tertiary-background"
                onClick={() => duplicateWorkflow(workflow.id)}
              >
                Duplicate
              </button>
              <button
                className="rounded-lg border border-primary-border bg-secondary-background px-2 py-1 text-xs text-secondary-text hover:bg-tertiary-background"
                onClick={() => deleteWorkflow(workflow.id)}
              >
                Delete
              </button>
            </div>
          }
        >
          <div className="mb-3 flex items-center gap-2">
            <input
              value={nameDraft}
              onChange={(e) => setNameDraft(e.target.value)}
              onBlur={() => renameWorkflow(workflow.id, nameDraft)}
              className="w-[420px] rounded-lg border border-primary-border bg-secondary-background px-3 py-1.5 text-sm text-primary-text placeholder:text-tertiary-text"
            />
            <div className="text-xs text-tertiary-text">Space + drag to pan · Delete to remove</div>
          </div>
          <div className="relative h-[70vh] overflow-hidden rounded-xl border border-primary-border bg-secondary-background">
            {workflow.nodes.length === 0 && (
              <div className="absolute inset-0 flex items-center justify-center text-center text-sm text-tertiary-text">
                Drag a node from the library to get started.
              </div>
            )}
            {workflow.nodes.length > 0 && !hasTasks ? (
              <div className="absolute inset-0 flex items-center justify-center p-6 text-center text-sm">
                <div className="max-w-[520px] rounded-2xl border border-primary-border bg-secondary-background/70 p-5 text-primary-text shadow-sm backdrop-blur">
                  <div className="text-sm font-semibold">Add your first Task</div>
                  <div className="mt-2 text-sm text-tertiary-text">
                    Studio runs Task nodes. Use the <span className="font-mono">+ Task</span> button on the canvas or drag a Task from the
                    library.
                  </div>
                </div>
              </div>
            ) : null}
            <WorkflowCanvas
              workflow={workflow}
              onChange={(nodes, edges) => setWorkflowGraph(workflow.id, nodes, edges)}
              onSelectNode={(nodeId) => selectNode(nodeId)}
              onCreateNode={(type, position) => {
                const id = addNode(workflow.id, type, position);
                if (id) {
                  selectNode(id);
                }
              }}
            />
          </div>
        </Panel>
      </div>

      {runModalOpen ? (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-6">
          <div className="glass w-full max-w-[720px] rounded-2xl border border-primary-border p-5 shadow-xl">
            <div className="flex items-center justify-between">
              <div className="text-sm font-semibold text-primary-text">Run: {workflow.name}</div>
              <button
                className="rounded-lg border border-primary-border bg-secondary-background px-2 py-1 text-xs text-secondary-text hover:bg-tertiary-background"
                onClick={() => setRunModalOpen(false)}
              >
                Close
              </button>
            </div>

            <div className="mt-4 space-y-4">
              <label className="block">
                <div className="text-xs text-tertiary-text">input.prompt</div>
                <textarea
                  value={runPrompt}
                  onChange={(e) => setRunPrompt(e.target.value)}
                  className="mt-1 h-40 w-full rounded-xl border border-primary-border bg-secondary-background px-3 py-2 font-mono text-xs text-primary-text"
                />
              </label>

              {includeFilePath ? (
                <label className="block">
                  <div className="text-xs text-tertiary-text">input.filePath</div>
                  <input
                    value={runFilePath}
                    onChange={(e) => setRunFilePath(e.target.value)}
                    className="mt-1 w-full rounded-xl border border-primary-border bg-secondary-background px-3 py-2 text-sm text-primary-text"
                  />
                </label>
              ) : null}

              {includeInstruction ? (
                <label className="block">
                  <div className="text-xs text-tertiary-text">input.instruction</div>
                  <textarea
                    value={runInstruction}
                    onChange={(e) => setRunInstruction(e.target.value)}
                    className="mt-1 h-24 w-full rounded-xl border border-primary-border bg-secondary-background px-3 py-2 text-sm text-primary-text"
                  />
                </label>
              ) : null}
            </div>

            <div className="mt-5 flex items-center justify-end gap-2">
              <button
                className="rounded-lg border border-primary-border bg-secondary-background px-3 py-1.5 text-xs text-secondary-text hover:bg-tertiary-background"
                onClick={() => setRunModalOpen(false)}
              >
                Cancel
              </button>
              <button
                className="rounded-lg border border-emerald-500/30 bg-emerald-500/10 px-3 py-1.5 text-xs text-emerald-200 hover:bg-emerald-500/20"
                onClick={() => {
                  const runId = startRun(workflow, {
                    prompt: runPrompt,
                    filePath: includeFilePath ? runFilePath : undefined,
                    instruction: includeInstruction ? runInstruction : undefined,
                  });
                  setRunModalOpen(false);
                  navigate(`/runs/${encodeURIComponent(runId)}`);
                }}
              >
                Start Run
              </button>
            </div>
          </div>
        </div>
      ) : null}
    </div>
  );
}

function EngineWorkflowsPage({ onModeChange }: { onModeChange: (mode: "studio" | "engine") => void }) {
  const navigate = useNavigate();
  const authStatus = useAuthStore((s) => s.status);
  const canPoll = authStatus === "unknown" || authStatus === "authorized";

  const [selectedWorkflowId, setSelectedWorkflowId] = useState<string>("");
  const [runModalOpen, setRunModalOpen] = useState(false);
  const [runInputDraft, setRunInputDraft] = useState<string>(`{
  "prompt": "hello from workflow run"
}`);
  const [editModalOpen, setEditModalOpen] = useState(false);
  const [workflowJsonDraft, setWorkflowJsonDraft] = useState<string>("");

  const workflowsQ = useQuery({
    queryKey: ["wf-defs", authStatus],
    queryFn: () => fetchWorkflows(),
    refetchInterval: canPoll ? 10_000 : false,
  });

  useEffect(() => {
    if (selectedWorkflowId) {
      return;
    }
    const first = workflowsQ.data?.[0]?.id;
    if (first) {
      setSelectedWorkflowId(first);
    }
  }, [selectedWorkflowId, workflowsQ.data]);

  const selected = useMemo(
    () => workflowsQ.data?.find((w) => w.id === selectedWorkflowId) ?? workflowsQ.data?.[0] ?? null,
    [selectedWorkflowId, workflowsQ.data],
  );

  useEffect(() => {
    if (!selected) {
      return;
    }
    setWorkflowJsonDraft(JSON.stringify(selected, null, 2));
  }, [selected?.id]);

  const startRunM = useMutation({
    mutationFn: async () => {
      if (!selected) {
        throw new Error("select a workflow");
      }
      let input: Record<string, unknown> = {};
      try {
        input = JSON.parse(runInputDraft) as Record<string, unknown>;
      } catch {
        throw new Error("input must be valid JSON");
      }
      return startWorkflowRun(selected.id, input);
    },
    onSuccess: (data) => {
      setRunModalOpen(false);
      navigate(`/runs/${encodeURIComponent(data.run_id)}`);
    },
  });

  const upsertM = useMutation({
    mutationFn: async () => {
      let def: Record<string, unknown>;
      try {
        def = JSON.parse(workflowJsonDraft) as Record<string, unknown>;
      } catch {
        throw new Error("workflow JSON must be valid");
      }
      return upsertWorkflow(def as any);
    },
    onSuccess: async (data) => {
      setEditModalOpen(false);
      await workflowsQ.refetch();
      if (data?.id) {
        setSelectedWorkflowId(data.id);
      }
    },
  });

  const workflows = workflowsQ.data ?? [];

  return (
    <div className="grid grid-cols-[320px_1fr] gap-6">
      <div className="space-y-6">
        <Panel
          title="Workflows"
          right={
            <div className="flex items-center gap-2">
              <div className="rounded-lg border border-primary-border bg-secondary-background p-1 text-xs">
                <button
                  className="rounded-md px-2 py-1 text-secondary-text hover:bg-tertiary-background"
                  onClick={() => onModeChange("studio")}
                >
                  Studio
                </button>
                <button
                  className="rounded-md bg-tertiary-background px-2 py-1 text-primary-text"
                  onClick={() => onModeChange("engine")}
                >
                  Engine
                </button>
              </div>
              <button
                className="rounded-lg border border-primary-border bg-secondary-background px-2 py-1 text-xs text-secondary-text hover:bg-tertiary-background"
                onClick={() => setEditModalOpen(true)}
              >
                Upsert JSON
              </button>
            </div>
          }
        >
          {workflowsQ.isLoading ? (
            <Loading label="Loading workflows..." />
          ) : workflowsQ.isError ? (
            <EmptyState
              title={authStatus === "missing_api_key" || authStatus === "invalid_api_key" ? "Unauthorized" : "Failed to load workflows"}
              description={
                authStatus === "missing_api_key"
                  ? "Gateway requires an API key. Set it in Settings."
                  : authStatus === "invalid_api_key"
                    ? "API key was rejected. Update it in Settings."
                    : "Check API base/key in Settings."
              }
            />
          ) : workflows.length === 0 ? (
            <EmptyState title="No workflows" description="Use Upsert JSON to create one." />
          ) : (
            <div className="space-y-2">
              {workflows.map((w) => (
                <button
                  key={w.id}
                  type="button"
                  onClick={() => setSelectedWorkflowId(w.id)}
                  className={[
                    "w-full rounded-lg border px-3 py-2 text-left text-xs",
                    w.id === selected?.id
                      ? "border-secondary-border bg-tertiary-background text-primary-text"
                      : "border-primary-border bg-secondary-background text-secondary-text hover:bg-tertiary-background",
                  ].join(" ")}
                >
                  <div className="truncate font-semibold">{w.name || w.id}</div>
                  <div className="mt-1 flex items-center justify-between gap-2">
                    <div className="truncate font-mono text-[11px] text-tertiary-text">{w.id}</div>
                    <div className="text-[11px] text-tertiary-text">{Object.keys(w.steps || {}).length} steps</div>
                  </div>
                </button>
              ))}
            </div>
          )}
        </Panel>
      </div>

      <div className="space-y-6">
        <Panel
          title={selected ? selected.name || selected.id : "Workflow"}
          right={
            <div className="flex items-center gap-2">
              <button
                className="rounded-lg border border-primary-border bg-secondary-background px-3 py-1.5 text-xs text-secondary-text hover:bg-tertiary-background disabled:opacity-50"
                disabled={!selected || authStatus === "missing_api_key" || authStatus === "invalid_api_key"}
                onClick={() => setRunModalOpen(true)}
              >
                Start Run
              </button>
              <button
                className="rounded-lg border border-primary-border bg-secondary-background px-3 py-1.5 text-xs text-secondary-text hover:bg-tertiary-background disabled:opacity-50"
                disabled={!selected}
                onClick={() => setEditModalOpen(true)}
              >
                Edit JSON
              </button>
            </div>
          }
        >
          {!selected ? (
            <EmptyState title="Select a workflow" />
          ) : (
            <div className="grid grid-cols-2 gap-6">
              <Panel title="Steps">
                <EngineStepsTable steps={selected.steps} />
              </Panel>
              <Panel title="Definition">
                <JsonViewer value={selected} />
              </Panel>
            </div>
          )}
        </Panel>
      </div>

      {runModalOpen ? (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-6">
          <div className="glass w-full max-w-[720px] rounded-2xl border border-primary-border p-5 shadow-xl">
            <div className="flex items-center justify-between">
              <div className="text-sm font-semibold text-primary-text">Start workflow run</div>
              <button
                className="rounded-lg border border-primary-border bg-secondary-background px-2 py-1 text-xs text-secondary-text hover:bg-tertiary-background"
                onClick={() => setRunModalOpen(false)}
              >
                Close
              </button>
            </div>

            <div className="mt-4 space-y-2">
              <div className="text-xs text-tertiary-text">Input JSON</div>
              <textarea
                value={runInputDraft}
                onChange={(e) => setRunInputDraft(e.target.value)}
                className="h-56 w-full rounded-xl border border-primary-border bg-secondary-background px-3 py-2 font-mono text-xs text-primary-text"
              />
              {startRunM.isError ? (
                <div className="rounded-lg border border-red-500/30 bg-red-500/10 p-2 text-xs text-red-200">
                  {startRunM.error instanceof Error ? startRunM.error.message : "failed"}
                </div>
              ) : null}
            </div>

            <div className="mt-5 flex items-center justify-end gap-2">
              <button
                className="rounded-lg border border-primary-border bg-secondary-background px-3 py-1.5 text-xs text-secondary-text hover:bg-tertiary-background"
                onClick={() => setRunModalOpen(false)}
              >
                Cancel
              </button>
              <button
                className="rounded-lg border border-emerald-500/30 bg-emerald-500/10 px-3 py-1.5 text-xs text-emerald-200 hover:bg-emerald-500/20 disabled:opacity-50"
                disabled={startRunM.isPending}
                onClick={() => startRunM.mutate()}
              >
                {startRunM.isPending ? "Starting…" : "Start"}
              </button>
            </div>
          </div>
        </div>
      ) : null}

      {editModalOpen ? (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-6">
          <div className="glass w-full max-w-[900px] rounded-2xl border border-primary-border p-5 shadow-xl">
            <div className="flex items-center justify-between">
              <div className="text-sm font-semibold text-primary-text">Upsert workflow JSON</div>
              <button
                className="rounded-lg border border-primary-border bg-secondary-background px-2 py-1 text-xs text-secondary-text hover:bg-tertiary-background"
                onClick={() => setEditModalOpen(false)}
              >
                Close
              </button>
            </div>

            <div className="mt-4 space-y-2">
              <div className="text-xs text-tertiary-text">
                Payload must match the Workflow Engine schema (`/api/v1/workflows`). Steps should be a map keyed by step id.
              </div>
              <textarea
                value={workflowJsonDraft}
                onChange={(e) => setWorkflowJsonDraft(e.target.value)}
                className="h-[460px] w-full rounded-xl border border-primary-border bg-secondary-background px-3 py-2 font-mono text-xs text-primary-text"
              />
              {upsertM.isError ? (
                <div className="rounded-lg border border-red-500/30 bg-red-500/10 p-2 text-xs text-red-200">
                  {upsertM.error instanceof Error ? upsertM.error.message : "failed"}
                </div>
              ) : null}
            </div>

            <div className="mt-5 flex items-center justify-end gap-2">
              <button
                className="rounded-lg border border-primary-border bg-secondary-background px-3 py-1.5 text-xs text-secondary-text hover:bg-tertiary-background"
                onClick={() => setEditModalOpen(false)}
              >
                Cancel
              </button>
              <button
                className="rounded-lg border border-emerald-500/30 bg-emerald-500/10 px-3 py-1.5 text-xs text-emerald-200 hover:bg-emerald-500/20 disabled:opacity-50"
                disabled={upsertM.isPending}
                onClick={() => upsertM.mutate()}
              >
                {upsertM.isPending ? "Saving…" : "Save"}
              </button>
            </div>
          </div>
        </div>
      ) : null}
    </div>
  );
}

function EngineStepsTable({ steps }: { steps: Record<string, WorkflowStepDefinition> }) {
  const ids = Object.keys(steps || {});
  ids.sort((a, b) => a.localeCompare(b));
  if (ids.length === 0) {
    return <EmptyState title="No steps" />;
  }
  return (
    <div className="space-y-2">
      {ids.map((id) => {
        const s = steps[id];
        return (
          <div key={id} className="rounded-lg border border-primary-border bg-secondary-background px-3 py-2 text-xs">
            <div className="flex items-center justify-between gap-3">
              <div className="truncate font-semibold text-primary-text">{s?.name || id}</div>
              <div className="font-mono text-[11px] text-tertiary-text">{s?.type || "-"}</div>
            </div>
            <div className="mt-1 flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-tertiary-text">
              <span className="font-mono">id:{id}</span>
              {s?.topic ? <span className="font-mono">topic:{s.topic}</span> : null}
              {s?.depends_on?.length ? <span className="font-mono">deps:{s.depends_on.join(",")}</span> : null}
            </div>
          </div>
        );
      })}
    </div>
  );
}

function WorkflowNodeInspector({
  workflowId,
  nodeId,
  data,
  onDelete,
}: {
  workflowId: string;
  nodeId: string;
  data: WorkflowNodeData;
  onDelete: () => void;
}) {
  const updateNodeData = useWorkflowStore((s) => s.updateNodeData);
  const workflow = useWorkflowStore((s) => s.workflows.find((w) => w.id === workflowId) ?? null);
  const promptRef = useRef<HTMLTextAreaElement | null>(null);

  const nodeById = useMemo(() => {
    const map = new Map<string, WorkflowNode>();
    for (const n of workflow?.nodes ?? []) {
      map.set(n.id, n as WorkflowNode);
    }
    return map;
  }, [workflow?.nodes]);

  const upstreamIds = useMemo(() => {
    const ids = (workflow?.edges ?? []).filter((e) => e.target === nodeId).map((e) => e.source);
    ids.sort((a, b) => a.localeCompare(b));
    return ids;
  }, [nodeId, workflow?.edges]);

  const downstreamIds = useMemo(() => {
    const ids = (workflow?.edges ?? []).filter((e) => e.source === nodeId).map((e) => e.target);
    ids.sort((a, b) => a.localeCompare(b));
    return ids;
  }, [nodeId, workflow?.edges]);

  const insertIntoTaskTemplate = useCallback(
    (token: string) => {
      const el = promptRef.current;
      const fallbackValue = data.kind === "task" ? data.promptTemplate ?? "" : "";
      const start = el?.selectionStart ?? fallbackValue.length;
      const end = el?.selectionEnd ?? start;
      updateNodeData(workflowId, nodeId, (cur) => {
        const curVal = (cur as any)?.promptTemplate ?? "";
        const safeStart = Math.max(0, Math.min(start, curVal.length));
        const safeEnd = Math.max(safeStart, Math.min(end, curVal.length));
        return { ...(cur as any), promptTemplate: curVal.slice(0, safeStart) + token + curVal.slice(safeEnd) } as any;
      });
      requestAnimationFrame(() => {
        const nextEl = promptRef.current;
        if (!nextEl) return;
        const pos = start + token.length;
        nextEl.focus();
        try {
          nextEl.setSelectionRange(pos, pos);
        } catch {
          // ignore
        }
      });
    },
    [data.kind, data.kind === "task" ? data.promptTemplate : "", nodeId, updateNodeData, workflowId],
  );

  if (data.kind === "input") {
    return (
      <div className="space-y-4">
        <label className="block">
          <div className="text-xs text-tertiary-text">Name</div>
          <input
            value={data.name}
            onChange={(e) => updateNodeData(workflowId, nodeId, (cur) => ({ ...cur, name: e.target.value } as any))}
            className="mt-1 w-full rounded-lg border border-primary-border bg-secondary-background px-3 py-2 text-sm text-primary-text"
          />
        </label>

        <label className="block">
          <div className="text-xs text-tertiary-text">Default Prompt</div>
          <textarea
            value={data.promptDefault}
            onChange={(e) =>
              updateNodeData(workflowId, nodeId, (cur) => ({ ...cur, promptDefault: e.target.value } as any))
            }
            className="mt-1 h-24 w-full rounded-lg border border-primary-border bg-secondary-background px-3 py-2 font-mono text-xs text-primary-text"
          />
        </label>

        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={data.includeFilePath}
            onChange={(e) =>
              updateNodeData(workflowId, nodeId, (cur) => ({ ...cur, includeFilePath: e.target.checked } as any))
            }
          />
          <span>Include `input.filePath`</span>
        </label>
        {data.includeFilePath ? (
          <label className="block">
            <div className="text-xs text-tertiary-text">Default filePath</div>
            <input
              value={data.filePathDefault}
              onChange={(e) =>
                updateNodeData(workflowId, nodeId, (cur) => ({ ...cur, filePathDefault: e.target.value } as any))
              }
              className="mt-1 w-full rounded-lg border border-primary-border bg-secondary-background px-3 py-2 text-sm text-primary-text"
            />
          </label>
        ) : null}

        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={data.includeInstruction}
            onChange={(e) =>
              updateNodeData(workflowId, nodeId, (cur) => ({ ...cur, includeInstruction: e.target.checked } as any))
            }
          />
          <span>Include `input.instruction`</span>
        </label>
        {data.includeInstruction ? (
          <label className="block">
            <div className="text-xs text-tertiary-text">Default instruction</div>
            <textarea
              value={data.instructionDefault}
              onChange={(e) =>
                updateNodeData(workflowId, nodeId, (cur) => ({ ...cur, instructionDefault: e.target.value } as any))
              }
              className="mt-1 h-20 w-full rounded-lg border border-primary-border bg-secondary-background px-3 py-2 text-sm text-primary-text"
            />
          </label>
        ) : null}

        <div className="flex items-center justify-between pt-2">
          <button
            className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-1 text-xs text-red-200 hover:bg-red-500/20"
            onClick={onDelete}
          >
            Delete Node
          </button>
          <button
            className="rounded-lg border border-primary-border bg-secondary-background px-3 py-1 text-xs text-secondary-text hover:bg-tertiary-background"
            onClick={() => navigator.clipboard?.writeText(nodeId)}
          >
            Copy ID
          </button>
        </div>
      </div>
    );
  }

  if (data.kind === "task") {
    const upstreamTasks = upstreamIds
      .map((id) => nodeById.get(id))
      .filter((n) => n && n.data.kind === "task") as any[];
    const downstreamTasks = downstreamIds
      .map((id) => nodeById.get(id))
      .filter((n) => n && n.data.kind === "task") as any[];

    return (
      <div className="space-y-4">
        <label className="block">
          <div className="text-xs text-tertiary-text">Name</div>
          <input
            value={data.name}
            onChange={(e) => updateNodeData(workflowId, nodeId, (cur) => ({ ...cur, name: e.target.value } as any))}
            className="mt-1 w-full rounded-lg border border-primary-border bg-secondary-background px-3 py-2 text-sm text-primary-text"
          />
        </label>

        <label className="block">
          <div className="text-xs text-tertiary-text">Topic</div>
          <select
            value={data.topic}
            onChange={(e) => updateNodeData(workflowId, nodeId, (cur) => ({ ...cur, topic: e.target.value } as any))}
            className="mt-1 w-full rounded-lg border border-primary-border bg-secondary-background px-3 py-2 text-sm text-primary-text"
          >
            {defaultTopics.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </select>
        </label>

        <label className="block">
          <div className="text-xs text-tertiary-text">Prompt Template</div>
          <div className="mb-2 flex flex-wrap gap-2">
            <button
              type="button"
              className="rounded-lg border border-primary-border bg-secondary-background px-2 py-1 text-[11px] text-secondary-text hover:bg-tertiary-background"
              onClick={() => insertIntoTaskTemplate("${input.prompt}")}
              title="Insert input.prompt"
            >
              + input.prompt
            </button>
            <button
              type="button"
              className="rounded-lg border border-primary-border bg-secondary-background px-2 py-1 text-[11px] text-secondary-text hover:bg-tertiary-background"
              onClick={() => insertIntoTaskTemplate("${input.instruction}")}
              title="Insert input.instruction"
            >
              + input.instruction
            </button>
            <button
              type="button"
              className="rounded-lg border border-primary-border bg-secondary-background px-2 py-1 text-[11px] text-secondary-text hover:bg-tertiary-background"
              onClick={() => insertIntoTaskTemplate("${prev.result}")}
              title="Insert prev.result (upstream result)"
            >
              + prev.result
            </button>
            <button
              type="button"
              className="rounded-lg border border-primary-border bg-secondary-background px-2 py-1 text-[11px] text-secondary-text hover:bg-tertiary-background"
              onClick={() => insertIntoTaskTemplate("${prev.result.response}")}
              title="Insert prev.result.response (chat jobs)"
            >
              + prev.result.response
            </button>
            <button
              type="button"
              className="rounded-lg border border-primary-border bg-secondary-background px-2 py-1 text-[11px] text-secondary-text hover:bg-tertiary-background"
              onClick={() => insertIntoTaskTemplate("${prev.result_ptr}")}
              title="Insert prev.result_ptr"
            >
              + prev.result_ptr
            </button>
            <button
              type="button"
              className="rounded-lg border border-primary-border bg-secondary-background px-2 py-1 text-[11px] text-secondary-text hover:bg-tertiary-background"
              onClick={() => insertIntoTaskTemplate("${prev.context_ptr}")}
              title="Insert prev.context_ptr"
            >
              + prev.context_ptr
            </button>
          </div>
          <textarea
            ref={promptRef}
            value={data.promptTemplate}
            onChange={(e) =>
              updateNodeData(workflowId, nodeId, (cur) => ({ ...cur, promptTemplate: e.target.value } as any))
            }
            className="mt-1 h-44 w-full rounded-lg border border-primary-border bg-secondary-background px-3 py-2 font-mono text-xs text-primary-text"
          />
          <div className="mt-2 space-y-2 text-[11px] text-tertiary-text">
            <div>
              Edges define what <span className="font-mono">prev.*</span> refers to.{" "}
              {upstreamTasks.length > 1 ? (
                <span>
                  This node has <span className="font-semibold text-secondary-text">{upstreamTasks.length}</span> upstream tasks, so{" "}
                  <span className="font-mono">prev.result</span> becomes a map keyed by node id.
                </span>
              ) : upstreamTasks.length === 1 ? (
                <span>
                  This node has one upstream task, so <span className="font-mono">prev.result</span> is that task’s result.
                </span>
              ) : (
                <span>No upstream tasks found; this will use the last completed task as <span className="font-mono">prev</span>.</span>
              )}
            </div>
            {upstreamTasks.length > 0 ? (
              <div className="rounded-xl border border-primary-border bg-secondary-background p-3">
                <div className="text-xs font-semibold text-primary-text">Upstream</div>
                <div className="mt-2 space-y-2">
                  {upstreamTasks.map((n: any) => (
                    <div key={n.id} className="rounded-lg border border-secondary-border bg-tertiary-background p-2">
                      <div className="flex items-center justify-between gap-2">
                        <div className="truncate text-xs font-semibold text-primary-text">{n.data.name || n.id}</div>
                        <div className="truncate font-mono text-[11px] text-tertiary-text">{n.id}</div>
                      </div>
                      <div className="mt-1 truncate font-mono text-[11px] text-tertiary-text">{n.data.topic || "-"}</div>
                      <div className="mt-2 flex flex-wrap gap-2">
                        <button
                          type="button"
                          className="rounded-lg border border-primary-border bg-secondary-background px-2 py-1 text-[11px] text-secondary-text hover:bg-tertiary-background"
                          onClick={() => insertIntoTaskTemplate(`\${node.${n.id}.result}`)}
                          title="Insert full upstream result"
                        >
                          + node.result
                        </button>
                        <button
                          type="button"
                          className="rounded-lg border border-primary-border bg-secondary-background px-2 py-1 text-[11px] text-secondary-text hover:bg-tertiary-background"
                          onClick={() => insertIntoTaskTemplate(`\${node.${n.id}.result.response}`)}
                          title="Insert upstream response (chat jobs)"
                        >
                          + node.result.response
                        </button>
                        <button
                          type="button"
                          className="rounded-lg border border-primary-border bg-secondary-background px-2 py-1 text-[11px] text-secondary-text hover:bg-tertiary-background"
                          onClick={() => insertIntoTaskTemplate(`\${node.${n.id}.result_ptr}`)}
                          title="Insert upstream result_ptr"
                        >
                          + node.result_ptr
                        </button>
                        <button
                          type="button"
                          className="rounded-lg border border-primary-border bg-secondary-background px-2 py-1 text-[11px] text-secondary-text hover:bg-tertiary-background"
                          onClick={() => insertIntoTaskTemplate(`\${node.${n.id}.context_ptr}`)}
                          title="Insert upstream context_ptr"
                        >
                          + node.context_ptr
                        </button>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            ) : null}
            {downstreamTasks.length > 0 ? (
              <div className="rounded-xl border border-primary-border bg-secondary-background p-3">
                <div className="text-xs font-semibold text-primary-text">Downstream</div>
                <div className="mt-2 space-y-1">
                  {downstreamTasks.map((n: any) => (
                    <div key={n.id} className="flex items-center justify-between gap-2 text-[11px]">
                      <div className="truncate text-secondary-text">{n.data.name || n.id}</div>
                      <div className="truncate font-mono text-tertiary-text">{n.id}</div>
                    </div>
                  ))}
                </div>
              </div>
            ) : null}
          </div>
        </label>

        <div className="grid grid-cols-2 gap-3">
          <label className="block">
            <div className="text-xs text-tertiary-text">Timeout (ms)</div>
            <input
              value={String(data.timeoutMs)}
              inputMode="numeric"
              onChange={(e) =>
                updateNodeData(workflowId, nodeId, (cur) => ({ ...cur, timeoutMs: Number(e.target.value || 0) } as any))
              }
              className="mt-1 w-full rounded-lg border border-primary-border bg-secondary-background px-3 py-2 text-sm text-primary-text"
            />
          </label>
          <label className="block">
            <div className="text-xs text-tertiary-text">Retries</div>
            <input
              value={String(data.retries)}
              inputMode="numeric"
              onChange={(e) =>
                updateNodeData(workflowId, nodeId, (cur) => ({ ...cur, retries: Number(e.target.value || 0) } as any))
              }
              className="mt-1 w-full rounded-lg border border-primary-border bg-secondary-background px-3 py-2 text-sm text-primary-text"
            />
          </label>
        </div>

        <div className="rounded-xl border border-primary-border bg-secondary-background p-3">
          <div className="text-xs font-semibold text-primary-text">Current Node JSON</div>
          <div className="mt-2">
            <JsonViewer value={data} />
          </div>
        </div>

        <div className="flex items-center justify-between pt-2">
          <button
            className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-1 text-xs text-red-200 hover:bg-red-500/20"
            onClick={onDelete}
          >
            Delete Node
          </button>
          <button
            className="rounded-lg border border-primary-border bg-secondary-background px-3 py-1 text-xs text-secondary-text hover:bg-tertiary-background"
            onClick={() => navigator.clipboard?.writeText(nodeId)}
          >
            Copy ID
          </button>
        </div>
      </div>
    );
  }

  if (data.kind === "memory") {
    const computedMemoryId =
      data.strategy === "workflow"
        ? `wf:${workflowId}`
        : data.strategy === "custom"
          ? data.customMemoryId.trim()
          : "run:<run_id>";

    const generateSessionMemoryId = () => {
      const uuid =
        typeof crypto !== "undefined" && typeof crypto.randomUUID === "function"
          ? crypto.randomUUID()
          : `${Date.now()}-${Math.random().toString(16).slice(2)}`;
      return `session:${uuid}`;
    };

    return (
      <div className="space-y-4">
        <label className="block">
          <div className="text-xs text-tertiary-text">Name</div>
          <input
            value={data.name}
            onChange={(e) => updateNodeData(workflowId, nodeId, (cur) => ({ ...(cur as any), name: e.target.value } as any))}
            className="mt-1 w-full rounded-lg border border-primary-border bg-secondary-background px-3 py-2 text-sm text-primary-text"
          />
        </label>

        <label className="block">
          <div className="text-xs text-tertiary-text">Strategy</div>
          <select
            value={data.strategy}
            onChange={(e) =>
              updateNodeData(workflowId, nodeId, (cur) => ({ ...(cur as any), strategy: e.target.value } as any))
            }
            className="mt-1 w-full rounded-lg border border-primary-border bg-secondary-background px-3 py-2 text-sm text-primary-text"
          >
            <option value="run">Per run (run:&lt;run_id&gt;)</option>
            <option value="workflow">Per workflow (wf:&lt;workflow_id&gt;)</option>
            <option value="custom">Custom</option>
          </select>
          <div className="mt-2 text-[11px] text-tertiary-text">
            Memory is used by the context engine to store chat history and RAG chunks. It’s inspectable via{" "}
            <span className="font-mono">GET /api/v1/memory</span> using keys like{" "}
            <span className="font-mono">mem:&lt;memory_id&gt;:events</span>.
          </div>
        </label>

        {data.strategy === "custom" ? (
          <div className="space-y-2">
            <label className="block">
              <div className="text-xs text-tertiary-text">Custom memory_id</div>
              <input
                value={data.customMemoryId}
                onChange={(e) =>
                  updateNodeData(workflowId, nodeId, (cur) => ({ ...(cur as any), customMemoryId: e.target.value } as any))
                }
                placeholder="e.g. session:my-chat-1"
                className="mt-1 w-full rounded-lg border border-primary-border bg-secondary-background px-3 py-2 text-sm text-primary-text"
              />
            </label>
            <button
              type="button"
              className="w-full rounded-lg border border-primary-border bg-secondary-background px-3 py-1.5 text-xs text-secondary-text hover:bg-tertiary-background"
              onClick={() =>
                updateNodeData(workflowId, nodeId, (cur) => ({ ...(cur as any), customMemoryId: generateSessionMemoryId() } as any))
              }
            >
              Generate session id
            </button>
          </div>
        ) : null}

        <div className="rounded-xl border border-primary-border bg-secondary-background p-3">
          <div className="text-xs font-semibold text-primary-text">Effective memory_id</div>
          <div className="mt-2 truncate font-mono text-[11px] text-tertiary-text" title={computedMemoryId}>
            {computedMemoryId}
          </div>
          <div className="mt-3 grid grid-cols-2 gap-2">
            <button
              type="button"
              className="rounded-lg border border-primary-border bg-tertiary-background px-3 py-1.5 text-xs text-secondary-text hover:bg-secondary-background"
              onClick={() => navigator.clipboard?.writeText(computedMemoryId)}
            >
              Copy memory_id
            </button>
            <button
              type="button"
              className="rounded-lg border border-primary-border bg-tertiary-background px-3 py-1.5 text-xs text-secondary-text hover:bg-secondary-background"
              onClick={() => navigator.clipboard?.writeText(`redis://mem:${computedMemoryId}:events`)}
            >
              Copy events ptr
            </button>
          </div>
        </div>

        <div className="flex items-center justify-between pt-2">
          <button
            className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-1 text-xs text-red-200 hover:bg-red-500/20"
            onClick={onDelete}
          >
            Delete Node
          </button>
          <button
            className="rounded-lg border border-primary-border bg-secondary-background px-3 py-1 text-xs text-secondary-text hover:bg-tertiary-background"
            onClick={() => navigator.clipboard?.writeText(nodeId)}
          >
            Copy ID
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <label className="block">
        <div className="text-xs text-tertiary-text">Name</div>
        <input
          value={data.name}
          onChange={(e) => updateNodeData(workflowId, nodeId, (cur) => ({ ...cur, name: e.target.value } as any))}
          className="mt-1 w-full rounded-lg border border-primary-border bg-secondary-background px-3 py-2 text-sm text-primary-text"
        />
      </label>

      <div className="rounded-xl border border-primary-border bg-secondary-background p-3">
        <div className="text-xs font-semibold text-primary-text">Outputs</div>
        <div className="mt-2 space-y-2">
          {data.outputs.map((o, idx) => (
            <div key={idx} className="rounded-lg border border-secondary-border bg-tertiary-background p-2">
              <div className="text-[11px] text-tertiary-text">Key</div>
              <input
                value={o.key}
                onChange={(e) =>
                  updateNodeData(workflowId, nodeId, (cur) => {
                    const next = { ...(cur as any) };
                    next.outputs = [...next.outputs];
                    next.outputs[idx] = { ...next.outputs[idx], key: e.target.value };
                    return next;
                  })
                }
                className="mt-1 w-full rounded-md border border-primary-border bg-secondary-background px-2 py-1 text-xs text-primary-text"
              />
              <div className="mt-2 text-[11px] text-tertiary-text">Template</div>
              <textarea
                value={o.template}
                onChange={(e) =>
                  updateNodeData(workflowId, nodeId, (cur) => {
                    const next = { ...(cur as any) };
                    next.outputs = [...next.outputs];
                    next.outputs[idx] = { ...next.outputs[idx], template: e.target.value };
                    return next;
                  })
                }
                className="mt-1 h-20 w-full rounded-md border border-primary-border bg-secondary-background px-2 py-1 font-mono text-xs text-primary-text"
              />
            </div>
          ))}
          <button
            className="w-full rounded-lg border border-primary-border bg-secondary-background px-3 py-1 text-xs text-secondary-text hover:bg-tertiary-background"
            onClick={() =>
              updateNodeData(workflowId, nodeId, (cur) => ({
                ...(cur as any),
                outputs: [...(cur as any).outputs, { key: `out_${(cur as any).outputs.length + 1}`, template: "${prev.result}" }],
              }))
            }
          >
            Add output field
          </button>
        </div>
      </div>

      <div className="flex items-center justify-between pt-2">
        <button
          className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-1 text-xs text-red-200 hover:bg-red-500/20"
          onClick={onDelete}
        >
          Delete Node
        </button>
        <button
          className="rounded-lg border border-primary-border bg-secondary-background px-3 py-1 text-xs text-secondary-text hover:bg-tertiary-background"
          onClick={() => navigator.clipboard?.writeText(nodeId)}
        >
          Copy ID
        </button>
      </div>
    </div>
  );
}
