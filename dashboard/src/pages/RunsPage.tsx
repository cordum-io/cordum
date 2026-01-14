import { useMemo, useState } from "react";
import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { api } from "../lib/api";
import { formatDuration, formatRelative } from "../lib/format";
import { useWorkflows } from "../hooks/useWorkflows";
import { useUiStore } from "../state/ui";
import { useViewStore } from "../state/views";
import { Card, CardHeader, CardTitle } from "../components/ui/Card";
import { Select } from "../components/ui/Select";
import { Input } from "../components/ui/Input";
import { Button } from "../components/ui/Button";
import { ProgressBar } from "../components/ProgressBar";
import { RunStatusBadge } from "../components/StatusBadge";
import { Drawer } from "../components/ui/Drawer";
import type { WorkflowRun } from "../types/api";

const statusOptions = ["all", "running", "waiting", "pending", "succeeded", "failed", "cancelled", "timed_out"];
const timeOptions = [
  { label: "All", value: "all" },
  { label: "24h", value: "24h" },
  { label: "7d", value: "7d" },
  { label: "30d", value: "30d" },
];

function runProgress(run: WorkflowRun) {
  const steps = Object.values(run.steps || {});
  if (steps.length === 0) {
    return { percent: 0, activeStep: "", activeStatus: "" };
  }
  const completed = steps.filter((step) =>
    ["succeeded", "failed", "cancelled", "timed_out"].includes(step.status)
  ).length;
  // First look for running/waiting steps
  let active = steps.find((step) => ["running", "waiting"].includes(step.status));
  // If none, look for pending steps (queued but not started)
  if (!active) {
    active = steps.find((step) => step.status === "pending");
  }
  return {
    percent: Math.round((completed / steps.length) * 100),
    activeStep: active?.step_id || "",
    activeStatus: active?.status || "",
  };
}

function runUpdatedAt(run: WorkflowRun) {
  return run.updated_at || run.started_at || run.created_at || "";
}

function withinRange(run: WorkflowRun, range: string) {
  if (range === "all") {
    return true;
  }
  const timestamp = runUpdatedAt(run);
  if (!timestamp) {
    return true;
  }
  const date = new Date(timestamp);
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  if (range === "24h") {
    return diffMs <= 24 * 60 * 60 * 1000;
  }
  if (range === "7d") {
    return diffMs <= 7 * 24 * 60 * 60 * 1000;
  }
  if (range === "30d") {
    return diffMs <= 30 * 24 * 60 * 60 * 1000;
  }
  return true;
}

export function RunsPage() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [statusFilter, setStatusFilter] = useState("all");
  const [workflowFilter, setWorkflowFilter] = useState("all");
  const [timeFilter, setTimeFilter] = useState("24h");
  const serverParams = useMemo(() => {
    const params: {
      limit?: number;
      status?: string;
      workflow_id?: string;
      updated_after?: number;
    } = { limit: 200 };
    if (statusFilter !== "all") {
      params.status = statusFilter;
    }
    if (workflowFilter !== "all") {
      params.workflow_id = workflowFilter;
    }
    if (timeFilter !== "all") {
      const now = Date.now();
      const deltaMs =
        timeFilter === "24h"
          ? 24 * 60 * 60 * 1000
          : timeFilter === "7d"
          ? 7 * 24 * 60 * 60 * 1000
          : 30 * 24 * 60 * 60 * 1000;
      params.updated_after = Math.floor((now - deltaMs) / 1000);
    }
    return params;
  }, [statusFilter, workflowFilter, timeFilter]);
  const runsQuery = useInfiniteQuery({
    queryKey: ["runs", serverParams],
    queryFn: ({ pageParam }) => api.listWorkflowRuns({ ...serverParams, cursor: pageParam as number | undefined }),
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
    initialPageParam: undefined as number | undefined,
  });
  const runs = runsQuery.data?.pages.flatMap((page) => page.items) ?? [];
  const isLoading = runsQuery.isLoading;
  const workflowsQuery = useWorkflows();
  const workflowMap = useMemo(() => {
    const map = new Map<string, string>();
    workflowsQuery.data?.forEach((workflow) => map.set(workflow.id, workflow.name || workflow.id));
    return map;
  }, [workflowsQuery.data]);

  const globalSearch = useUiStore((state) => state.globalSearch);
  const views = useViewStore((state) => state.views);
  const addView = useViewStore((state) => state.addView);

  const [searchQuery, setSearchQuery] = useState("");
  const [selectedViewId, setSelectedViewId] = useState("all");
  const [selectedRun, setSelectedRun] = useState<WorkflowRun | null>(null);

  const cancelMutation = useMutation({
    mutationFn: ({ workflowId, runId }: { workflowId: string; runId: string }) =>
      api.cancelRun(workflowId, runId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["runs"] }),
  });

  const rerunMutation = useMutation({
    mutationFn: (payload: { runId: string }) => api.rerunRun(payload.runId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["runs"] }),
  });

  const filteredRuns = useMemo(() => {
    const query = (searchQuery || globalSearch).toLowerCase();
    return runs
      .filter((run) => (statusFilter === "all" ? true : run.status === statusFilter))
      .filter((run) => (workflowFilter === "all" ? true : run.workflow_id === workflowFilter))
      .filter((run) => withinRange(run, timeFilter))
      .filter((run) => {
        if (!query) {
          return true;
        }
        return (
          run.id.toLowerCase().includes(query) ||
          run.workflow_id.toLowerCase().includes(query) ||
          (workflowMap.get(run.workflow_id) || "").toLowerCase().includes(query)
        );
      })
      .sort((a, b) => runUpdatedAt(b).localeCompare(runUpdatedAt(a)));
  }, [runs, statusFilter, workflowFilter, timeFilter, searchQuery, globalSearch, workflowMap]);

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Runs</CardTitle>
          <Button variant="subtle" size="sm" type="button" onClick={() => navigate("/workflows")}
          >
            Start new run
          </Button>
        </CardHeader>
        <div className="flex flex-col gap-4 lg:flex-row lg:items-end">
          <div className="flex flex-1 flex-col gap-2">
            <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Saved views</label>
            <div className="flex flex-col gap-2 lg:flex-row">
              <Select
                value={selectedViewId}
                onChange={(event) => {
                  const next = event.target.value;
                  setSelectedViewId(next);
                  if (next === "all") {
                    setStatusFilter("all");
                    setWorkflowFilter("all");
                    setSearchQuery("");
                    return;
                  }
                  const view = views.find((item) => item.id === next);
                  if (view) {
                    setStatusFilter(view.filters.status || "all");
                    setWorkflowFilter(view.filters.workflowId || "all");
                    setSearchQuery(view.filters.query || "");
                  }
                }}
              >
                <option value="all">All runs</option>
                {views.map((view) => (
                  <option key={view.id} value={view.id}>
                    {view.name}
                  </option>
                ))}
              </Select>
              <Button
                variant="outline"
                size="sm"
                type="button"
                onClick={() => {
                  const viewName = `View ${new Date().toLocaleTimeString()}`;
                  addView({
                    id: `${Date.now()}`,
                    name: viewName,
                    filters: {
                      status: statusFilter === "all" ? undefined : statusFilter,
                      workflowId: workflowFilter === "all" ? undefined : workflowFilter,
                      query: searchQuery || undefined,
                    },
                  });
                  setSelectedViewId("all");
                }}
              >
                Save view
              </Button>
            </div>
          </div>
        </div>
        <div className="mt-6 grid gap-3 lg:grid-cols-4">
          <div>
            <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Status</label>
            <Select value={statusFilter} onChange={(event) => setStatusFilter(event.target.value)}>
              {statusOptions.map((status) => (
                <option key={status} value={status}>
                  {status === "all" ? "Any" : status}
                </option>
              ))}
            </Select>
          </div>
                      <div>
                                      <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Workflow</label>
                                      <Select value={workflowFilter} onChange={(event) => setWorkflowFilter(event.target.value)}>
                                        <option value="all">All workflows</option>              {workflowsQuery.data?.map((workflow) => (                <option key={workflow.id} value={workflow.id}>
                  {workflow.name || workflow.id}
                </option>
              ))}
            </Select>
          </div>
          <div>
            <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Time range</label>
            <Select value={timeFilter} onChange={(event) => setTimeFilter(event.target.value)}>
              {timeOptions.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </Select>
          </div>
          <div className="flex-1 lg:max-w-xs">
            <Input value={searchQuery} onChange={(event) => setSearchQuery(event.target.value)} placeholder="Run id or workflow" />
          </div>
        </div>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Run List</CardTitle>
          <div className="text-xs text-muted">Showing {filteredRuns.length} runs</div>
        </CardHeader>
        <div className="space-y-3">
          {isLoading ? (
            <div className="text-sm text-muted">Loading runs...</div>
          ) : filteredRuns.length === 0 ? (
            <div className="rounded-2xl border border-dashed border-border p-6 text-sm text-muted">No runs match these filters.</div>
          ) : (
            filteredRuns.map((run, index) => {
              const progress = runProgress(run);
              return (
                <button
                  key={run.id}
                  type="button"
                  onClick={() => setSelectedRun(run)}
                  className="list-row animate-rise text-left"
                  style={{ animationDelay: `${index * 40}ms` }}
                >
                  <div className="flex flex-col gap-3">
                    <div className="grid gap-3 lg:grid-cols-[minmax(0,2fr)_auto_auto] lg:items-center">
                      <div>
                        <div className="text-sm font-semibold text-ink">
                          {workflowMap.get(run.workflow_id) || run.workflow_id}
                        </div>
                        <div className="text-xs text-muted">
                          Run {run.id.slice(0, 8)} · {formatRelative(runUpdatedAt(run))}
                        </div>
                      </div>
                      <div className="text-xs text-muted">{formatDuration(run.started_at || run.created_at, run.completed_at)}</div>
                      <RunStatusBadge status={run.status} />
                    </div>
                    <div>
                      <div className="mb-2 flex items-center justify-between text-xs text-muted">
                        <span>
                          {progress.activeStep ? (
                            <>
                              Step: {progress.activeStep}
                              {progress.activeStatus ? ` · ${progress.activeStatus === "waiting" ? "awaiting approval" : progress.activeStatus}` : ""}
                            </>
                          ) : (
                            "Queued"
                          )}
                        </span>
                        <span>{progress.percent}%</span>
                      </div>
                      <ProgressBar value={progress.percent} />
                    </div>
                  </div>
                </button>
              );
            })
          )}
        </div>
        {runsQuery.hasNextPage ? (
          <div className="mt-4">
            <Button
              variant="outline"
              size="sm"
              type="button"
              onClick={() => runsQuery.fetchNextPage()}
              disabled={runsQuery.isFetchingNextPage}
            >
              {runsQuery.isFetchingNextPage ? "Loading..." : "Load more"}
            </Button>
          </div>
        ) : null}
      </Card>

      <Drawer open={Boolean(selectedRun)} onClose={() => setSelectedRun(null)}>
        {selectedRun ? (
          <div className="space-y-6">
            <div className="flex items-center justify-between">
              <div>
                <div className="text-xs uppercase tracking-[0.2em] text-muted">Run</div>
                <h3 className="text-xl font-semibold text-ink">{selectedRun.id.slice(0, 12)}</h3>
              </div>
              <RunStatusBadge status={selectedRun.status} />
            </div>
            <div className="rounded-2xl border border-border bg-white/70 p-4 text-sm text-muted">
              <div>Workflow: {workflowMap.get(selectedRun.workflow_id) || selectedRun.workflow_id}</div>
              <div>Started: {formatRelative(selectedRun.started_at || selectedRun.created_at)}</div>
              <div>Org: {selectedRun.org_id || "default"}</div>
            </div>
            <div>
              <h4 className="text-sm font-semibold text-ink">Steps</h4>
              <div className="mt-3 space-y-2">
                {Object.values(selectedRun.steps || {}).length === 0 ? (
                  <div className="text-sm text-muted">No steps reported yet.</div>
                ) : (
                  Object.values(selectedRun.steps || {}).map((step) => (
                    <div key={step.step_id} className="rounded-xl border border-border bg-white/70 px-3 py-2 text-sm">
                      <div className="flex items-center justify-between">
                        <span className="font-semibold text-ink">{step.step_id}</span>
                        <span className="text-xs text-muted">{step.status}</span>
                      </div>
                    </div>
                  ))
                )}
              </div>
            </div>
            <div className="flex flex-wrap gap-3">
              <Button variant="primary" type="button" onClick={() => navigate(`/runs/${selectedRun.id}`)}>
                Open full view
              </Button>
              <Button
                variant="outline"
                type="button"
                onClick={() => rerunMutation.mutate({ runId: selectedRun.id })}
                disabled={rerunMutation.isPending}
              >
                Rerun
              </Button>
              <Button
                variant="danger"
                type="button"
                onClick={() => cancelMutation.mutate({ workflowId: selectedRun.workflow_id, runId: selectedRun.id })}
                disabled={cancelMutation.isPending}
              >
                Cancel
              </Button>
            </div>
          </div>
        ) : null}
      </Drawer>
    </div>
  );
}
