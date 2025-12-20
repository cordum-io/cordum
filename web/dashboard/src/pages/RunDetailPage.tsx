import { Link, useNavigate, useParams } from "react-router-dom";
import { useEffect, useMemo } from "react";
import Card from "../components/Card";
import EmptyState from "../components/EmptyState";
import Badge from "../components/Badge";
import JsonViewer from "../components/JsonViewer";
import MemoryPointerViewer from "../components/MemoryPointerViewer";
import { formatRFC3339, formatUnixMillis } from "../lib/format";
import { useRunStore } from "../state/runStore";
import { useWorkflowStore } from "../state/workflowStore";
import { topoSortTaskNodes } from "../features/workflows/runner/runWorkflow";
import { useInspectorStore } from "../state/inspectorStore";
import { useMutation, useQuery } from "@tanstack/react-query";
import {
  approveWorkflowStep,
  cancelWorkflowRun,
  fetchWorkflow,
  fetchWorkflowRun,
  type WorkflowDefinition,
  type WorkflowRun,
} from "../lib/api";

export default function RunDetailPage() {
  const { id } = useParams();
  if (!id) {
    return (
      <Card title="Run">
        <EmptyState title="Missing run id" description="Navigate back to Runs and select a run." />
      </Card>
    );
  }

  if (id.startsWith("run_")) {
    return <LocalRunDetail runId={id} />;
  }

  return <EngineRunDetail runId={id} />;
}

function isTerminalJobState(state: string): boolean {
  return state === "SUCCEEDED" || state === "FAILED" || state === "CANCELLED" || state === "TIMEOUT" || state === "DENIED";
}

function LocalRunDetail({ runId }: { runId: string }) {
  const navigate = useNavigate();
  const run = useRunStore((s) => s.runsById[runId]);
  const setActiveRun = useRunStore((s) => s.setActiveRun);
  const workflows = useWorkflowStore((s) => s.workflows);
  const showInspector = useInspectorStore((s) => s.show);

  useEffect(() => {
    setActiveRun(runId);
  }, [runId, setActiveRun]);

  const workflow = useMemo(() => {
    if (!run) return null;
    return workflows.find((w) => w.id === run.workflowId) ?? null;
  }, [run, workflows]);

  const rows = useMemo(() => {
    if (!run) {
      return [];
    }
    if (!workflow) {
      return Object.values(run.nodeResults);
    }
    try {
      const orderedTasks = topoSortTaskNodes(workflow);
      return orderedTasks
        .map((n) => {
          const existing = run.nodeResults[n.id];
          if (existing) {
            return existing;
          }
          if (n.data.kind !== "task") {
            return null;
          }
          return {
            nodeId: n.id,
            nodeName: n.data.name,
            topic: n.data.topic,
            state: "PENDING" as const,
          };
        })
        .filter(Boolean) as any[];
    } catch {
      return Object.values(run.nodeResults);
    }
  }, [run, workflow]);

  if (!run) {
    return (
      <Card title="Run">
        <EmptyState title="Run not found" description="The run may have been cleared from local history." />
      </Card>
    );
  }

  const memoryId = run.memoryId || `run:${run.id}`;

  return (
    <div className="space-y-6">
      <Card
        title={`Run: ${run.workflowName}`}
        right={
          <div className="flex items-center gap-2">
            <button
              className="rounded-md border border-white/10 bg-black/20 px-2 py-1 text-xs text-zinc-300 hover:bg-black/30"
              onClick={() => navigate("/runs")}
            >
              Back
            </button>
            <Badge state={run.status} />
          </div>
        }
      >
        <div className="grid grid-cols-2 gap-4 text-sm md:grid-cols-4">
          <div className="rounded-xl border border-white/10 bg-black/20 p-3">
            <div className="text-xs text-zinc-500">Run ID</div>
            <div className="mt-1 truncate font-mono text-xs text-zinc-200">{run.id}</div>
          </div>
          <div className="rounded-xl border border-white/10 bg-black/20 p-3">
            <div className="text-xs text-zinc-500">Memory ID</div>
            <div className="mt-1 truncate font-mono text-xs text-zinc-200" title={memoryId}>
              {memoryId}
            </div>
            <div className="mt-2 flex flex-wrap gap-2">
              <button
                type="button"
                className="rounded-md border border-white/10 bg-black/20 px-2 py-1 font-mono text-[10px] text-zinc-300 hover:bg-black/30"
                onClick={() =>
                  showInspector("Memory: Events (context engine)", <MemoryPointerViewer pointer={`redis://mem:${memoryId}:events`} />)
                }
              >
                events
              </button>
              <button
                type="button"
                className="rounded-md border border-white/10 bg-black/20 px-2 py-1 font-mono text-[10px] text-zinc-300 hover:bg-black/30"
                onClick={() =>
                  showInspector("Memory: Chunks Index (context engine)", <MemoryPointerViewer pointer={`redis://mem:${memoryId}:chunks`} />)
                }
              >
                chunks
              </button>
            </div>
          </div>
          <div className="rounded-xl border border-white/10 bg-black/20 p-3">
            <div className="text-xs text-zinc-500">Started</div>
            <div className="mt-1 text-xs text-zinc-200">{formatUnixMillis(run.startedAt)}</div>
          </div>
          <div className="rounded-xl border border-white/10 bg-black/20 p-3">
            <div className="text-xs text-zinc-500">Ended</div>
            <div className="mt-1 text-xs text-zinc-200">{formatUnixMillis(run.endedAt)}</div>
          </div>
        </div>

        {run.error ? <div className="mt-3 rounded-lg border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-200">{run.error}</div> : null}

        {workflow ? (
          <div className="mt-3 text-xs text-zinc-500">
            Workflow:{" "}
            <Link
              to="/workflows"
              className="font-mono text-zinc-300 hover:underline"
              onClick={() => useWorkflowStore.getState().selectWorkflow(workflow.id)}
            >
              {workflow.id}
            </Link>
          </div>
        ) : null}
      </Card>

      <Card title="Node Results">
        <div className="space-y-2">
          {rows.length === 0 ? (
            <EmptyState title="No nodes" description="This workflow has no task nodes." />
          ) : (
            rows.map((r) => (
              <div
                key={r.nodeId}
                className="flex items-center justify-between gap-3 rounded-xl border border-white/10 bg-black/20 px-3 py-2 text-sm"
              >
                <div className="min-w-0">
                  <div className="truncate font-semibold text-zinc-200">{r.nodeName}</div>
                  <div className="truncate font-mono text-xs text-zinc-500">{r.topic}</div>
                  {r.jobId ? (
                    <div className="mt-1 flex flex-wrap items-center gap-2 text-xs">
                      <Link
                        to={`/jobs/${encodeURIComponent(r.jobId)}`}
                        className="font-mono text-zinc-300 hover:underline"
                      >
                        job: {r.jobId}
                      </Link>
                      {r.traceId ? <span className="font-mono text-zinc-500">trace: {r.traceId}</span> : null}
                      <button
                        type="button"
                        className="rounded-md border border-white/10 bg-black/20 px-2 py-1 font-mono text-[10px] text-zinc-300 hover:bg-black/30"
                        onClick={() =>
                          showInspector("Memory: Context Pointer", <MemoryPointerViewer pointer={r.contextPtr || `redis://ctx:${r.jobId}`} />)
                        }
                      >
                        ctx
                      </button>
                      <button
                        type="button"
                        disabled={!isTerminalJobState(r.state) || !r.resultPtr}
                        className="rounded-md border border-white/10 bg-black/20 px-2 py-1 font-mono text-[10px] text-zinc-300 hover:bg-black/30 disabled:cursor-not-allowed disabled:opacity-50"
                        onClick={() =>
                          showInspector("Memory: Result Pointer", <MemoryPointerViewer pointer={r.resultPtr} />)
                        }
                      >
                        res
                      </button>
                    </div>
                  ) : null}
                  {r.error ? (
                    <div className="mt-2 whitespace-pre-wrap break-words text-xs text-red-200">{r.error}</div>
                  ) : null}
                </div>
                <div className="flex shrink-0 items-center gap-2">
                  <Badge state={r.state} />
                </div>
              </div>
            ))
          )}
        </div>
      </Card>

      <div className="grid grid-cols-2 gap-6">
        <Card title="Inputs">
          <JsonViewer value={run.inputs} />
        </Card>
        <Card title="Output">
          {run.output ? <JsonViewer value={run.output} /> : <EmptyState title="No output yet" description="Run still in progress or failed before output." />}
        </Card>
      </div>
    </div>
  );
}

function EngineRunDetail({ runId }: { runId: string }) {
  const navigate = useNavigate();
  const showInspector = useInspectorStore((s) => s.show);

  const runQ = useQuery({
    queryKey: ["wf-run", runId],
    queryFn: () => fetchWorkflowRun(runId),
    refetchInterval: (q) => {
      const data = q.state.data as WorkflowRun | undefined;
      if (!data) {
        return 2_000;
      }
      return data.status === "succeeded" || data.status === "failed" || data.status === "cancelled" || data.status === "timed_out"
        ? false
        : 2_000;
    },
  });

  const workflowQ = useQuery({
    queryKey: ["wf-def", runQ.data?.workflow_id],
    queryFn: () => fetchWorkflow(runQ.data!.workflow_id),
    enabled: Boolean(runQ.data?.workflow_id),
  });

  const approveM = useMutation({
    mutationFn: async (args: { workflowId: string; stepId: string; approved: boolean }) =>
      approveWorkflowStep(args.workflowId, runId, args.stepId, args.approved),
    onSuccess: () => {
      void runQ.refetch();
    },
  });

  const cancelM = useMutation({
    mutationFn: async (workflowId: string) => cancelWorkflowRun(workflowId, runId),
    onSuccess: () => {
      void runQ.refetch();
    },
  });

  if (runQ.isLoading) {
    return (
      <Card title="Run">
        <EmptyState title="Loading run…" />
      </Card>
    );
  }

  if (runQ.isError || !runQ.data) {
    return (
      <Card title="Run">
        <EmptyState title="Run not found" description="Verify the run id and API settings." />
      </Card>
    );
  }

  const run = runQ.data;
  const wf = workflowQ.data;
  const workflowTitle = wf?.name ? `${wf.name}` : run.workflow_id;
  const steps = buildEngineSteps(wf, run);
  const memoryId =
    typeof (run as any)?.input?.memory_id === "string" && String((run as any).input.memory_id).trim()
      ? String((run as any).input.memory_id).trim()
      : `run:${run.id}`;

  return (
    <div className="space-y-6">
      <Card
        title={`Run: ${workflowTitle}`}
        right={
          <div className="flex items-center gap-2">
            <button
              className="rounded-md border border-white/10 bg-black/20 px-2 py-1 text-xs text-zinc-300 hover:bg-black/30"
              onClick={() => navigate("/runs")}
            >
              Back
            </button>
            <button
              className="rounded-md border border-red-500/30 bg-red-500/10 px-2 py-1 text-xs text-red-200 hover:bg-red-500/20 disabled:opacity-50"
              disabled={!run.workflow_id || cancelM.isPending}
              onClick={() => cancelM.mutate(run.workflow_id)}
              title="Cancel run"
            >
              Cancel
            </button>
            <Badge state={run.status} />
          </div>
        }
      >
        <div className="grid grid-cols-2 gap-4 text-sm md:grid-cols-4">
          <div className="rounded-xl border border-white/10 bg-black/20 p-3">
            <div className="text-xs text-zinc-500">Run ID</div>
            <div className="mt-1 truncate font-mono text-xs text-zinc-200">{run.id}</div>
          </div>
          <div className="rounded-xl border border-white/10 bg-black/20 p-3">
            <div className="text-xs text-zinc-500">Memory ID</div>
            <div className="mt-1 truncate font-mono text-xs text-zinc-200" title={memoryId}>
              {memoryId}
            </div>
            <div className="mt-2 flex flex-wrap gap-2">
              <button
                type="button"
                className="rounded-md border border-white/10 bg-black/20 px-2 py-1 font-mono text-[10px] text-zinc-300 hover:bg-black/30"
                onClick={() =>
                  showInspector("Memory: Events (context engine)", <MemoryPointerViewer pointer={`redis://mem:${memoryId}:events`} />)
                }
              >
                events
              </button>
              <button
                type="button"
                className="rounded-md border border-white/10 bg-black/20 px-2 py-1 font-mono text-[10px] text-zinc-300 hover:bg-black/30"
                onClick={() =>
                  showInspector("Memory: Chunks Index (context engine)", <MemoryPointerViewer pointer={`redis://mem:${memoryId}:chunks`} />)
                }
              >
                chunks
              </button>
            </div>
          </div>
          <div className="rounded-xl border border-white/10 bg-black/20 p-3">
            <div className="text-xs text-zinc-500">Created</div>
            <div className="mt-1 text-xs text-zinc-200">{formatRFC3339(run.created_at)}</div>
          </div>
          <div className="rounded-xl border border-white/10 bg-black/20 p-3">
            <div className="text-xs text-zinc-500">Updated</div>
            <div className="mt-1 text-xs text-zinc-200">{formatRFC3339(run.updated_at)}</div>
          </div>
        </div>

        {run.error ? <div className="mt-3 rounded-lg border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-200">{JSON.stringify(run.error)}</div> : null}

        <div className="mt-3 text-xs text-zinc-500">
          Workflow: <span className="font-mono text-zinc-300">{run.workflow_id}</span>
        </div>
      </Card>

      <Card title="Steps">
        {steps.length === 0 ? (
          <EmptyState title="No steps" description="This workflow definition has no steps." />
        ) : (
          <div className="space-y-2">
            {steps.map((s) => (
              <div key={s.stepId} className="rounded-xl border border-white/10 bg-black/20 p-3 text-sm">
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0">
                    <div className="truncate font-semibold text-zinc-200">{s.name}</div>
                    <div className="mt-0.5 flex flex-wrap items-center gap-2 text-xs text-zinc-500">
                      <span className="font-mono">{s.stepId}</span>
                      {s.type ? <span className="font-mono">type:{s.type}</span> : null}
                      {s.topic ? <span className="font-mono">topic:{s.topic}</span> : null}
                    </div>
                    {s.jobId ? (
                      <div className="mt-1 text-xs">
                        <Link to={`/jobs/${encodeURIComponent(s.jobId)}`} className="font-mono text-zinc-300 hover:underline">
                          job: {s.jobId}
                        </Link>
                      </div>
                    ) : null}
                  </div>
                  <div className="flex shrink-0 items-center gap-2">
                    {s.status === "waiting" ? (
                      <>
                        <button
                          className="rounded-md border border-emerald-500/30 bg-emerald-500/10 px-2 py-1 text-xs text-emerald-200 hover:bg-emerald-500/20 disabled:opacity-50"
                          disabled={approveM.isPending || !run.workflow_id}
                          onClick={() => approveM.mutate({ workflowId: run.workflow_id, stepId: s.stepId, approved: true })}
                        >
                          Approve
                        </button>
                        <button
                          className="rounded-md border border-red-500/30 bg-red-500/10 px-2 py-1 text-xs text-red-200 hover:bg-red-500/20 disabled:opacity-50"
                          disabled={approveM.isPending || !run.workflow_id}
                          onClick={() => approveM.mutate({ workflowId: run.workflow_id, stepId: s.stepId, approved: false })}
                        >
                          Deny
                        </button>
                      </>
                    ) : null}
                    <Badge state={s.status} />
                  </div>
                </div>

                <div className="mt-2 grid grid-cols-2 gap-4 text-xs">
                  <div className="rounded-lg border border-white/10 bg-black/10 p-2">
                    <div className="text-[11px] uppercase tracking-wider text-zinc-500">Started</div>
                    <div className="mt-1 text-zinc-200">{formatRFC3339(s.startedAt)}</div>
                  </div>
                  <div className="rounded-lg border border-white/10 bg-black/10 p-2">
                    <div className="text-[11px] uppercase tracking-wider text-zinc-500">Completed</div>
                    <div className="mt-1 text-zinc-200">{formatRFC3339(s.completedAt)}</div>
                  </div>
                </div>

                {s.error ? (
                  <div className="mt-2 rounded-lg border border-red-500/30 bg-red-500/10 p-2 text-xs text-red-200">
                    {JSON.stringify(s.error)}
                  </div>
                ) : null}
              </div>
            ))}
          </div>
        )}
      </Card>

      <div className="grid grid-cols-2 gap-6">
        <Card title="Input">{run.input ? <JsonViewer value={run.input} /> : <EmptyState title="No input" />}</Card>
        <Card title="Context">{run.context ? <JsonViewer value={run.context} /> : <EmptyState title="No context yet" />}</Card>
      </div>
    </div>
  );
}

type EngineStepRow = {
  stepId: string;
  name: string;
  type?: string;
  topic?: string;
  status: string;
  jobId?: string;
  startedAt?: string | null;
  completedAt?: string | null;
  error?: Record<string, unknown>;
};

function buildEngineSteps(wf: WorkflowDefinition | undefined, run: WorkflowRun): EngineStepRow[] {
  const defs = wf?.steps ?? {};
  const runs = run.steps ?? {};
  const stepIds = Object.keys(defs);
  stepIds.sort((a, b) => a.localeCompare(b));
  return stepIds.map((id) => {
    const def = defs[id];
    const sr = runs[id];
    return {
      stepId: id,
      name: def?.name || id,
      type: def?.type,
      topic: def?.topic,
      status: sr?.status || "pending",
      jobId: sr?.job_id,
      startedAt: sr?.started_at ?? null,
      completedAt: sr?.completed_at ?? null,
      error: sr?.error,
    };
  });
}
