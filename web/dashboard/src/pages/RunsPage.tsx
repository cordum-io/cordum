import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import Card from "../components/Card";
import EmptyState from "../components/EmptyState";
import DataTable, { type Column } from "../components/DataTable";
import Badge from "../components/Badge";
import { formatRFC3339, formatUnixMillis } from "../lib/format";
import { useRunStore, type WorkflowRun } from "../state/runStore";
import { useAuthStore } from "../state/authStore";
import { fetchWorkflowRuns, fetchWorkflows, type WorkflowDefinition, type WorkflowRun as EngineRun } from "../lib/api";
import { useQuery } from "@tanstack/react-query";
import Loading from "../components/Loading";

export default function RunsPage() {
  const [mode, setMode] = useState<"studio" | "engine">("studio");

  const order = useRunStore((s) => s.order);
  const runsById = useRunStore((s) => s.runsById);
  const clear = useRunStore((s) => s.clear);

  const authStatus = useAuthStore((s) => s.status);
  const canPoll = authStatus === "unknown" || authStatus === "authorized";

  const workflowsQ = useQuery({
    queryKey: ["wf-defs", authStatus],
    queryFn: () => fetchWorkflows(),
    enabled: mode === "engine",
    refetchInterval: canPoll ? 10_000 : false,
  });

  const [engineWorkflowId, setEngineWorkflowId] = useState<string>("");

  useEffect(() => {
    if (mode !== "engine") {
      return;
    }
    if (engineWorkflowId) {
      return;
    }
    const first = workflowsQ.data?.[0]?.id;
    if (first) {
      setEngineWorkflowId(first);
    }
  }, [engineWorkflowId, mode, workflowsQ.data]);

  const engineRunsQ = useQuery({
    queryKey: ["wf-runs", engineWorkflowId, authStatus],
    queryFn: () => fetchWorkflowRuns(engineWorkflowId),
    enabled: mode === "engine" && Boolean(engineWorkflowId),
    refetchInterval: canPoll ? 2_000 : false,
  });

  const rows = useMemo(() => order.map((id) => runsById[id]).filter(Boolean), [order, runsById]);

  const columns: Column<WorkflowRun>[] = useMemo(
    () => [
      {
        key: "id",
        header: "Run ID",
        className: "w-[320px] font-mono text-xs",
        render: (r) => (
          <Link to={`/runs/${encodeURIComponent(r.id)}`} className="hover:underline">
            {r.id}
          </Link>
        ),
      },
      { key: "workflow", header: "Workflow", render: (r) => r.workflowName },
      { key: "status", header: "Status", className: "w-[140px]", render: (r) => <Badge state={r.status} /> },
      { key: "started", header: "Started", className: "w-[220px]", render: (r) => formatUnixMillis(r.startedAt) },
      { key: "ended", header: "Ended", className: "w-[220px]", render: (r) => formatUnixMillis(r.endedAt) },
    ],
    [],
  );

  const engineColumns: Column<EngineRun>[] = useMemo(
    () => [
      {
        key: "id",
        header: "Run ID",
        className: "w-[340px] font-mono text-xs",
        render: (r) => (
          <Link to={`/runs/${encodeURIComponent(r.id)}`} className="hover:underline">
            {r.id}
          </Link>
        ),
      },
      {
        key: "workflow_id",
        header: "Workflow",
        className: "w-[260px] font-mono text-xs",
        render: (r) => r.workflow_id,
      },
      { key: "status", header: "Status", className: "w-[140px]", render: (r) => <Badge state={r.status} /> },
      { key: "created", header: "Created", className: "w-[220px]", render: (r) => formatRFC3339(r.created_at) },
      { key: "updated", header: "Updated", className: "w-[220px]", render: (r) => formatRFC3339(r.updated_at) },
    ],
    [],
  );

  const engineWorkflows = (workflowsQ.data ?? []) as WorkflowDefinition[];
  const engineRuns = (engineRunsQ.data ?? []) as EngineRun[];

  return (
    <div className="space-y-6">
      <Card
        title="Runs"
        right={
          <div className="flex items-center gap-2">
            <div className="rounded-lg border border-primary-border bg-secondary-background p-1 text-xs">
              <button
                className={[
                  "rounded-md px-2 py-1",
                  mode === "studio" ? "bg-tertiary-background text-primary-text" : "text-secondary-text hover:bg-tertiary-background",
                ].join(" ")}
                onClick={() => setMode("studio")}
              >
                Studio
              </button>
              <button
                className={[
                  "rounded-md px-2 py-1",
                  mode === "engine" ? "bg-tertiary-background text-primary-text" : "text-secondary-text hover:bg-tertiary-background",
                ].join(" ")}
                onClick={() => setMode("engine")}
              >
                Engine
              </button>
            </div>
            {mode === "studio" ? (
              <button
                className="rounded-lg border border-primary-border bg-secondary-background px-2 py-1 text-xs text-secondary-text hover:bg-tertiary-background"
                onClick={clear}
              >
                Clear
              </button>
            ) : null}
          </div>
        }
      >
        {mode === "studio" ? (
          rows.length === 0 ? (
            <EmptyState title="No runs yet" description="Create a workflow in Workflows and click Run." />
          ) : (
            <DataTable columns={columns} rows={rows} rowKey={(r) => r.id} />
          )
        ) : workflowsQ.isLoading ? (
          <Loading label="Loading workflows…" />
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
        ) : engineWorkflows.length === 0 ? (
          <EmptyState title="No engine workflows" description="Create one via the Workflow Engine API." />
        ) : (
          <div className="space-y-4">
            <div className="flex flex-wrap items-center gap-2">
              <div className="text-xs text-tertiary-text">Workflow</div>
              <select
                value={engineWorkflowId}
                onChange={(e) => setEngineWorkflowId(e.target.value)}
                className="min-w-[360px] rounded-lg border border-primary-border bg-secondary-background px-2 py-1.5 text-xs text-primary-text"
              >
                {engineWorkflows.map((wf) => (
                  <option key={wf.id} value={wf.id}>
                    {wf.name || wf.id}
                  </option>
                ))}
              </select>
              <button
                className="rounded-lg border border-primary-border bg-secondary-background px-2 py-1.5 text-xs text-secondary-text hover:bg-tertiary-background"
                onClick={() => void engineRunsQ.refetch()}
              >
                Refresh
              </button>
            </div>

            {engineRunsQ.isLoading ? (
              <Loading label="Loading runs…" />
            ) : engineRunsQ.isError ? (
              <EmptyState title="Failed to load runs" description="Verify the selected workflow and API settings." />
            ) : engineRuns.length === 0 ? (
              <EmptyState title="No runs for this workflow" description="Start a run from Workflows → Engine." />
            ) : (
              <DataTable columns={engineColumns} rows={engineRuns} rowKey={(r) => r.id} />
            )}
          </div>
        )}
      </Card>
    </div>
  );
}
