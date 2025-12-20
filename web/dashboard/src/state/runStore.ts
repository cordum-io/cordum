import { create } from "zustand";
import { readJSON, writeJSON } from "../lib/storage";
import { newID } from "../lib/id";
import { fetchJob, submitJob, type JobState } from "../lib/api";
import { computeWorkflowOutput, deriveTaskEdges, renderTemplate, topoSortTaskNodes } from "../features/workflows/runner/runWorkflow";
import type { RunInputs } from "../features/workflows/runner/runWorkflow";
import type { MemoryNodeData, TaskNodeData, Workflow } from "../features/workflows/types";

function extractJobErrorMessage(result: unknown): string | null {
  if (!result || typeof result !== "object") {
    return null;
  }
  const r = result as any;
  if (typeof r.error?.message === "string" && r.error.message.trim()) {
    return r.error.message.trim();
  }
  if (typeof r.error === "string" && r.error.trim()) {
    return r.error.trim();
  }
  return null;
}

function isTerminalJobState(state: JobState | undefined): boolean {
  return state === "SUCCEEDED" || state === "FAILED" || state === "CANCELLED" || state === "TIMEOUT" || state === "DENIED";
}

function normalizeMemoryID(value: string): string {
  const trimmed = value.trim();
  if (!trimmed) {
    return "";
  }
  // Keep it reasonably pointer-friendly: collapse whitespace and limit length.
  const collapsed = trimmed.replaceAll(/\s+/g, "_").replaceAll(/[\u0000-\u001F]/g, "");
  return collapsed.length > 200 ? collapsed.slice(0, 200) : collapsed;
}

function deriveRunMemoryID(workflow: Workflow, runId: string, inputs: RunInputs): string {
  const fallback = `run:${runId}`;
  const memNode = workflow.nodes.find((n) => n.data.kind === "memory");
  if (!memNode || memNode.data.kind !== "memory") {
    return fallback;
  }
  const cfg = memNode.data as MemoryNodeData;
  if (cfg.strategy === "workflow") {
    return `wf:${workflow.id}`;
  }
  if (cfg.strategy === "custom") {
    const rendered = renderTemplate(cfg.customMemoryId ?? "", {
      inputs,
      nodeResults: {},
      prevResult: null,
    });
    const normalized = normalizeMemoryID(rendered);
    return normalized || fallback;
  }
  return fallback;
}

export type NodeRunResult = {
  nodeId: string;
  nodeName: string;
  topic: string;
  jobId?: string;
  traceId?: string;
  state: JobState;
  startedAt?: number;
  endedAt?: number;
  contextPtr?: string;
  resultPtr?: string;
  result?: unknown;
  error?: string;
};

export type WorkflowRun = {
  id: string;
  workflowId: string;
  workflowName: string;
  startedAt: number;
  endedAt?: number;
  status: JobState;
  inputs: RunInputs;
  memoryId?: string;
  output?: Record<string, unknown>;
  nodeResults: Record<string, NodeRunResult>;
  error?: string;
};

const runsKey = "cortexos.runs.v1";
const maxRuns = 50;

type RunsSnapshot = {
  order: string[];
  runsById: Record<string, WorkflowRun>;
};

type RunState = RunsSnapshot & {
  activeRunId: string | null;
  startRun: (workflow: Workflow, inputs: RunInputs) => string;
  setActiveRun: (runId: string | null) => void;
  deleteRun: (runId: string) => void;
  clear: () => void;
};

function loadInitial(): RunsSnapshot {
  const stored = readJSON<RunsSnapshot>(runsKey);
  if (stored?.order && stored?.runsById) {
    return stored;
  }
  return { order: [], runsById: {} };
}

let persistTimer: ReturnType<typeof setTimeout> | null = null;

function persist(snapshot: RunsSnapshot) {
  if (persistTimer) {
    clearTimeout(persistTimer);
  }
  persistTimer = setTimeout(() => {
    writeJSON(runsKey, snapshot);
    persistTimer = null;
  }, 250);
}

function trimSnapshot(snapshot: RunsSnapshot): RunsSnapshot {
  if (snapshot.order.length <= maxRuns) {
    return snapshot;
  }
  const keep = snapshot.order.slice(0, maxRuns);
  const nextById: Record<string, WorkflowRun> = {};
  for (const id of keep) {
    const r = snapshot.runsById[id];
    if (r) {
      nextById[id] = r;
    }
  }
  return { order: keep, runsById: nextById };
}

export const useRunStore = create<RunState>((set, get) => {
  const initial = loadInitial();
  return {
    ...initial,
    activeRunId: null,

    setActiveRun: (runId) => set({ activeRunId: runId }),

    clear: () => {
      persist({ order: [], runsById: {} });
      set({ order: [], runsById: {}, activeRunId: null });
    },

    deleteRun: (runId) => {
      set((cur) => {
        const nextOrder = cur.order.filter((id) => id !== runId);
        const nextById = { ...cur.runsById };
        delete nextById[runId];
        const snapshot = trimSnapshot({ order: nextOrder, runsById: nextById });
        persist(snapshot);
        return { ...snapshot, activeRunId: cur.activeRunId === runId ? null : cur.activeRunId };
      });
    },

    startRun: (workflow, inputs) => {
      const runId = newID("run_");
      const memoryId = deriveRunMemoryID(workflow, runId, inputs);
      const run: WorkflowRun = {
        id: runId,
        workflowId: workflow.id,
        workflowName: workflow.name,
        startedAt: Date.now(),
        status: "RUNNING",
        inputs,
        memoryId,
        nodeResults: {},
      };

      set((cur) => {
        const snapshot = trimSnapshot({
          order: [runId, ...cur.order],
          runsById: { ...cur.runsById, [runId]: run },
        });
        persist(snapshot);
        return { ...snapshot, activeRunId: runId };
      });

      void (async () => {
        const updateRun = (updater: (cur: WorkflowRun) => WorkflowRun) => {
          set((cur) => {
            const existing = cur.runsById[runId];
            if (!existing) {
              return cur;
            }
            const nextRun = updater(existing);
            const snapshot = { order: cur.order, runsById: { ...cur.runsById, [runId]: nextRun } };
            persist(snapshot);
            return { ...cur, runsById: snapshot.runsById };
          });
        };

        const setNode = (nodeId: string, updater: (cur: NodeRunResult | undefined) => NodeRunResult) => {
          updateRun((curRun) => {
            const prev = curRun.nodeResults[nodeId];
            return {
              ...curRun,
              nodeResults: { ...curRun.nodeResults, [nodeId]: updater(prev) },
            };
          });
        };

        const tasks = topoSortTaskNodes(workflow);
        const taskIds = new Set(tasks.map((t) => t.id));
        const taskById = new Map(tasks.map((t) => [t.id, t]));
        const incomingTaskIds: Record<string, string[]> = {};
        for (const e of deriveTaskEdges(workflow)) {
          if (!taskIds.has(e.source) || !taskIds.has(e.target)) {
            continue;
          }
          if (!incomingTaskIds[e.target]) {
            incomingTaskIds[e.target] = [];
          }
          incomingTaskIds[e.target].push(e.source);
        }
        for (const [targetId, sources] of Object.entries(incomingTaskIds)) {
          sources.sort((a, b) => {
            const na = taskById.get(a);
            const nb = taskById.get(b);
            if (!na || !nb) return a.localeCompare(b);
            return (na.position.x - nb.position.x) || (na.position.y - nb.position.y) || a.localeCompare(b);
          });
        }

        let prevResult: unknown = undefined;
        let prevResultPtr: unknown = undefined;
        let prevContextPtr: unknown = undefined;
        let prevNodeId: unknown = undefined;

        for (const taskNode of tasks) {
          const data = taskNode.data as TaskNodeData;
          const currentNodeResults = get().runsById[runId]?.nodeResults ?? {};
          const upstream = incomingTaskIds[taskNode.id] ?? [];
          const edgePrevResult =
            upstream.length === 0
              ? prevResult
              : upstream.length === 1
                ? currentNodeResults[upstream[0]]?.result
                : Object.fromEntries(upstream.map((id) => [id, currentNodeResults[id]?.result]));
          const edgePrevResultPtr =
            upstream.length === 0
              ? prevResultPtr
              : upstream.length === 1
                ? currentNodeResults[upstream[0]]?.resultPtr
                : Object.fromEntries(upstream.map((id) => [id, currentNodeResults[id]?.resultPtr]));
          const edgePrevContextPtr =
            upstream.length === 0
              ? prevContextPtr
              : upstream.length === 1
                ? currentNodeResults[upstream[0]]?.contextPtr
                : Object.fromEntries(upstream.map((id) => [id, currentNodeResults[id]?.contextPtr]));
          const edgePrevNodeId =
            upstream.length === 0
              ? prevNodeId
              : upstream.length === 1
                ? upstream[0]
                : upstream;
          const renderCtx = {
            inputs,
            nodeResults: currentNodeResults,
            prevResult: edgePrevResult,
            prevResultPtr: edgePrevResultPtr,
            prevContextPtr: edgePrevContextPtr,
            prevNodeId: edgePrevNodeId,
          };
          const prompt = renderTemplate(data.promptTemplate, renderCtx);

          setNode(taskNode.id, (cur) => ({
            nodeId: taskNode.id,
            nodeName: data.name,
            topic: data.topic,
            state: cur?.state ?? "PENDING",
            startedAt: Date.now(),
          }));

          let attempt = 0;
          let lastError = "";
          const maxAttempts = Math.max(1, (data.retries ?? 0) + 1);
          while (attempt < maxAttempts) {
            attempt += 1;
            try {
              const contextMode = data.topic.startsWith("job.chat.") ? "chat" : data.topic.startsWith("job.code.") ? "rag" : undefined;
              const submitted = await submitJob({
                topic: data.topic,
                prompt,
                priority: "interactive",
                memory_id: memoryId,
                context_mode: contextMode,
                labels: {
                  workflow_id: workflow.id,
                  run_id: runId,
                  node_id: taskNode.id,
                },
              });
              setNode(taskNode.id, (cur) => ({
                ...(cur as NodeRunResult),
                jobId: submitted.job_id,
                traceId: submitted.trace_id,
                state: "PENDING",
              }));

              const start = Date.now();
              while (true) {
                const details = await fetchJob(submitted.job_id);
                setNode(taskNode.id, (cur) => ({
                  ...(cur as NodeRunResult),
                  state: details.state,
                  result: details.result,
                  contextPtr: details.context_ptr,
                  resultPtr: details.result_ptr,
                }));

                if (
                  details.state === "SUCCEEDED" ||
                  details.state === "FAILED" ||
                  details.state === "CANCELLED" ||
                  details.state === "TIMEOUT" ||
                  details.state === "DENIED"
                ) {
	                  setNode(taskNode.id, (cur) => ({
	                    ...(cur as NodeRunResult),
	                    endedAt: Date.now(),
	                    state: details.state,
	                    result: details.result,
	                    contextPtr: details.context_ptr,
	                    resultPtr: details.result_ptr,
	                  }));
	                  if (details.state !== "SUCCEEDED") {
	                    const errMsgFromResult = extractJobErrorMessage(details.result);
	                    const errMsgFromDLQ = typeof details.error_message === "string" ? details.error_message.trim() : "";
	                    const errMsgFromSafety = typeof details.safety_reason === "string" ? details.safety_reason.trim() : "";
	                    const errMsg = errMsgFromResult || errMsgFromDLQ || errMsgFromSafety;
	                    throw new Error(`node failed state=${details.state}${errMsg ? `: ${errMsg}` : ""}`);
	                  }
                  prevResult = details.result;
                  prevResultPtr = details.result_ptr;
                  prevContextPtr = details.context_ptr;
                  prevNodeId = taskNode.id;
                  break;
                }

                if (data.timeoutMs > 0 && Date.now() - start > data.timeoutMs) {
                  setNode(taskNode.id, (cur) => ({ ...(cur as NodeRunResult), endedAt: Date.now(), state: "TIMEOUT" }));
                  throw new Error("node timed out");
                }

                await new Promise((r) => setTimeout(r, 500));
              }

              lastError = "";
              break;
            } catch (err) {
              lastError = err instanceof Error ? err.message : String(err);
              setNode(taskNode.id, (cur) => ({
                ...(cur as NodeRunResult),
                error: lastError,
                state: attempt < maxAttempts ? "PENDING" : isTerminalJobState(cur?.state) ? cur!.state : "FAILED",
              }));
              if (attempt >= maxAttempts) {
                throw err;
              }
              await new Promise((r) => setTimeout(r, 500));
            }
          }

          if (lastError) {
            throw new Error(lastError);
          }
        }

        const finalNodeResults = get().runsById[runId]?.nodeResults ?? {};
        const outputNode = workflow.nodes.find((n) => n.data.kind === "output");
        let outputPrevResult: unknown = prevResult;
        let outputPrevResultPtr: unknown = prevResultPtr;
        let outputPrevContextPtr: unknown = prevContextPtr;
        let outputPrevNodeId: unknown = prevNodeId;
        if (outputNode) {
          const upstream = (workflow.edges ?? [])
            .filter((e) => e.target === outputNode.id && taskIds.has(e.source))
            .map((e) => e.source)
            .sort((a, b) => {
              const na = taskById.get(a);
              const nb = taskById.get(b);
              if (!na || !nb) return a.localeCompare(b);
              return (na.position.x - nb.position.x) || (na.position.y - nb.position.y) || a.localeCompare(b);
            });
          outputPrevResult =
            upstream.length === 0
              ? prevResult
              : upstream.length === 1
                ? finalNodeResults[upstream[0]]?.result
                : Object.fromEntries(upstream.map((id) => [id, finalNodeResults[id]?.result]));
          outputPrevResultPtr =
            upstream.length === 0
              ? prevResultPtr
              : upstream.length === 1
                ? finalNodeResults[upstream[0]]?.resultPtr
                : Object.fromEntries(upstream.map((id) => [id, finalNodeResults[id]?.resultPtr]));
          outputPrevContextPtr =
            upstream.length === 0
              ? prevContextPtr
              : upstream.length === 1
                ? finalNodeResults[upstream[0]]?.contextPtr
                : Object.fromEntries(upstream.map((id) => [id, finalNodeResults[id]?.contextPtr]));
          outputPrevNodeId = upstream.length === 0 ? prevNodeId : upstream.length === 1 ? upstream[0] : upstream;
        }

        const output = computeWorkflowOutput(workflow, {
          inputs,
          nodeResults: finalNodeResults,
          prevResult: outputPrevResult,
          prevResultPtr: outputPrevResultPtr,
          prevContextPtr: outputPrevContextPtr,
          prevNodeId: outputPrevNodeId,
        });

        updateRun((curRun) => ({
          ...curRun,
          status: "SUCCEEDED",
          endedAt: Date.now(),
          output,
        }));
      })().catch((err) => {
        const msg = err instanceof Error ? err.message : String(err);
        const cancelledAt = Date.now();
        let cancelledResults: Record<string, NodeRunResult> | null = null;
        try {
          const tasks = topoSortTaskNodes(workflow);
          cancelledResults = {};
          for (const t of tasks) {
            if (t.data.kind !== "task") continue;
            cancelledResults[t.id] = {
              nodeId: t.id,
              nodeName: t.data.name,
              topic: t.data.topic,
              state: "CANCELLED",
              endedAt: cancelledAt,
              error: "skipped (run failed)",
            };
          }
        } catch {
          cancelledResults = null;
        }
        set((cur) => {
          const existing = cur.runsById[runId];
          if (!existing) {
            return cur;
          }
          const nextNodeResults: Record<string, NodeRunResult> =
            cancelledResults === null
              ? existing.nodeResults
              : { ...cancelledResults, ...existing.nodeResults };
          const next = {
            ...existing,
            status: "FAILED" as JobState,
            endedAt: cancelledAt,
            error: msg,
            nodeResults: nextNodeResults,
          };
          const snapshot = { order: cur.order, runsById: { ...cur.runsById, [runId]: next } };
          persist(snapshot);
          return { ...cur, runsById: snapshot.runsById };
        });
      });

      return runId;
    },
  };
});
