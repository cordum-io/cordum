import { useMemo, useState } from "react";
import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { ArrowUpRight, Filter, Play, Sparkles } from "lucide-react";
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

function runDurationMs(run: WorkflowRun): number {
  const startIso = run.started_at || run.created_at;
  if (!startIso) {
    return 0;
  }
  const start = new Date(startIso).getTime();
  if (!Number.isFinite(start)) {
    return 0;
  }
  const end = run.completed_at ? new Date(run.completed_at).getTime() : Date.now();
  if (!Number.isFinite(end)) {
    return 0;
  }
  return Math.max(0, end - start);
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

export default function RunsPage() {
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

  const activeCount = useMemo(
    () => runs.filter((run) => ["running", "waiting", "pending"].includes(run.status)).length,
    [runs]
  );
  const waitingCount = useMemo(() => runs.filter((run) => run.status === "waiting").length, [runs]);
  const failedCount = useMemo(
    () => runs.filter((run) => ["failed", "timed_out", "cancelled"].includes(run.status)).length,
    [runs]
  );
  const hasFilters =
    statusFilter !== "all" || workflowFilter !== "all" || timeFilter !== "24h" || searchQuery.length > 0;
  const activeFilters = useMemo(() => {
    const filters: string[] = [];
    if (statusFilter !== "all") {
      filters.push(`Status: ${statusFilter}`);
    }
    if (workflowFilter !== "all") {
      const label = workflowMap.get(workflowFilter) || workflowFilter;
      filters.push(`Workflow: ${label}`);
    }
    if (timeFilter !== "24h") {
      filters.push(`Range: ${timeFilter}`);
    }
    if (searchQuery.trim()) {
      filters.push(`Search: ${searchQuery.trim()}`);
    }
    return filters;
  }, [statusFilter, workflowFilter, timeFilter, searchQuery, workflowMap]);

  const resetFilters = () => {
    setStatusFilter("all");
    setWorkflowFilter("all");
    setTimeFilter("24h");
    setSearchQuery("");
    setSelectedViewId("all");
  };

  return (
    <div className="space-y-6">
      <section className="relative overflow-hidden rounded-3xl border border-border bg-[color:var(--surface-glass)] p-6 lg:p-8">
        <div className="pointer-events-none absolute -right-16 top-0 h-56 w-56 rounded-full bg-[color:rgba(15,127,122,0.2)] blur-3xl" />
        <div className="pointer-events-none absolute -left-20 bottom-0 h-56 w-56 rounded-full bg-[color:rgba(212,131,58,0.18)] blur-3xl" />
        <div className="relative grid gap-6 lg:grid-cols-[2fr,1fr]">
          <div>
            <div className="inline-flex items-center gap-2 rounded-full border border-border bg-card/80 px-3 py-1 text-[10px] font-semibold uppercase tracking-[0.2em] text-muted-foreground">
              <Sparkles className="h-3 w-3 text-accent" />
              Runs
            </div>
            <h2 className="mt-4 font-display text-3xl font-semibold text-ink">Find the run you need in seconds.</h2>
            <p className="mt-2 text-sm text-muted-foreground">Jump to active work, approvals, or failures with one click.</p>
            <div className="mt-5 flex flex-wrap gap-3">
              <Button variant="primary" type="button" onClick={() => navigate("/workflows/new")}>
                <Play className="h-4 w-4" />
                Start a run
              </Button>
              <Button variant="outline" type="button" onClick={() => navigate("/policy")}>
                Review approvals
              </Button>
              <Button variant="ghost" type="button" onClick={() => navigate("/workflows")}>
                Browse workflows
              </Button>
            </div>
            <div className="mt-6 flex flex-wrap gap-3 text-xs text-muted-foreground">
              <div className="rounded-full border border-border bg-card/80 px-3 py-1">
                <span className="font-semibold text-ink">{filteredRuns.length}</span> shown
              </div>
              <div className="rounded-full border border-border bg-card/80 px-3 py-1">
                <span className="font-semibold text-ink">{activeCount}</span> active
              </div>
              <div className="rounded-full border border-border bg-card/80 px-3 py-1">
                <span className="font-semibold text-ink">{waitingCount}</span> awaiting approval
              </div>
              <div className="rounded-full border border-border bg-card/80 px-3 py-1">
                <span className="font-semibold text-ink">{failedCount}</span> failed
              </div>
            </div>
          </div>
          <div className="space-y-4">
            <div className="rounded-2xl border border-border bg-card/70 p-4">
              <div className="text-xs font-semibold uppercase tracking-[0.2em] text-muted-foreground">Quick actions</div>
              <div className="mt-3 space-y-2">
                <button
                  type="button"
                  onClick={() => navigate("/workflows/new")}
                  className="flex w-full items-center justify-between rounded-xl border border-border bg-card/80 px-3 py-2 text-left transition hover:border-accent"
                >
                  <div>
                    <div className="font-semibold text-ink">Start a run</div>
                    <div className="text-xs text-muted-foreground">Launch a workflow</div>
                  </div>
                  <ArrowUpRight className="h-4 w-4 text-muted-foreground" />
                </button>
                <button
                  type="button"
                  onClick={() => navigate("/policy")}
                  className="flex w-full items-center justify-between rounded-xl border border-border bg-card/80 px-3 py-2 text-left transition hover:border-accent"
                >
                  <div>
                    <div className="font-semibold text-ink">Review approvals</div>
                    <div className="text-xs text-muted-foreground">{waitingCount} waiting</div>
                  </div>
                  <ArrowUpRight className="h-4 w-4 text-muted-foreground" />
                </button>
                <button
                  type="button"
                  onClick={() => {
                    setStatusFilter("failed");
                    setSelectedViewId("all");
                  }}
                  className="flex w-full items-center justify-between rounded-xl border border-border bg-card/80 px-3 py-2 text-left transition hover:border-accent"
                >
                  <div>
                    <div className="font-semibold text-ink">Investigate failures</div>
                    <div className="text-xs text-muted-foreground">{failedCount} runs</div>
                  </div>
                  <ArrowUpRight className="h-4 w-4 text-muted-foreground" />
                </button>
              </div>
            </div>
            <div className="rounded-2xl border border-border bg-card/70 p-4">
              <div className="flex items-center justify-between">
                <div className="text-xs font-semibold uppercase tracking-[0.2em] text-muted-foreground">Focus filters</div>
                {hasFilters ? (
                  <Button variant="ghost" size="sm" type="button" onClick={resetFilters}>
                    Clear
                  </Button>
                ) : null}
              </div>
              <div className="mt-3 space-y-2">
                {[
                  { label: "All runs", value: "all", count: runs.length },
                  { label: "Running", value: "running", count: runs.filter((run) => run.status === "running").length },
                  { label: "Awaiting approval", value: "waiting", count: waitingCount },
                  { label: "Failed / timed out", value: "failed", count: failedCount },
                ].map((item) => (
                  <button
                    key={item.value}
                    type="button"
                    onClick={() => {
                      setStatusFilter(item.value);
                      setSelectedViewId("all");
                    }}
                    className="flex w-full items-center justify-between rounded-xl border border-border bg-card/80 px-3 py-2 text-left transition hover:border-accent"
                  >
                    <div>
                      <div className="font-semibold text-ink">{item.label}</div>
                      <div className="text-xs text-muted-foreground">{item.count} runs</div>
                    </div>
                    <ArrowUpRight className="h-4 w-4 text-muted-foreground" />
                  </button>
                ))}
              </div>
            </div>
            <div className="rounded-2xl border border-border bg-card/70 p-4">
              <div className="flex items-center justify-between text-xs font-semibold uppercase tracking-[0.2em] text-muted-foreground">
                Active filters
                <Filter className="h-4 w-4 text-muted-foreground" />
              </div>
              {activeFilters.length ? (
                <div className="mt-3 flex flex-wrap gap-2 text-xs">
                  {activeFilters.map((filter) => (
                    <span key={filter} className="rounded-full border border-border bg-card/80 px-3 py-1 text-ink">
                      {filter}
                    </span>
                  ))}
                </div>
              ) : (
                <div className="mt-3 text-sm text-muted-foreground">No filters applied. Showing last 24h.</div>
              )}
            </div>
          </div>
        </div>
      </section>

      <Card>
        <CardHeader>
          <CardTitle>Run filters</CardTitle>
          {hasFilters ? (
            <Button variant="outline" size="sm" type="button" onClick={resetFilters}>
              Clear filters
            </Button>
          ) : null}
        </CardHeader>
        <div className="grid gap-4 lg:grid-cols-[1.2fr_2fr] lg:items-end">
          <div className="flex flex-col gap-2">
            <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted-foreground">Saved views</label>
            <div className="flex flex-col gap-2 lg:flex-row">
              <Select
                value={selectedViewId}
                onChange={(event) => {
                  const next = event.target.value;
                  setSelectedViewId(next);
                  if (next === "all") {
                    resetFilters();
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
          <div className="rounded-2xl border border-border bg-card/70 p-3 text-xs text-muted-foreground">
            Save the filters you use most so you can jump back with one click.
          </div>
        </div>
        <div className="mt-6 grid gap-3 lg:grid-cols-4">
          <div>
            <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted-foreground">Status</label>
            <Select value={statusFilter} onChange={(event) => setStatusFilter(event.target.value)}>
              {statusOptions.map((status) => (
                <option key={status} value={status}>
                  {status === "all" ? "Any" : status}
                </option>
              ))}
            </Select>
          </div>
          <div>
            <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted-foreground">Workflow</label>
            <Select value={workflowFilter} onChange={(event) => setWorkflowFilter(event.target.value)}>
              <option value="all">All workflows</option>
              {workflowsQuery.data?.map((workflow) => (
                <option key={workflow.id} value={workflow.id}>
                  {workflow.name || workflow.id}
                </option>
              ))}
            </Select>
          </div>
          <div>
            <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted-foreground">Time range</label>
            <Select value={timeFilter} onChange={(event) => setTimeFilter(event.target.value)}>
              {timeOptions.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </Select>
          </div>
          <div className="flex-1 lg:max-w-xs">
            <Input
              value={searchQuery}
              onChange={(event) => setSearchQuery(event.target.value)}
              placeholder="Search run id or workflow"
            />
          </div>
        </div>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Run list</CardTitle>
          <div className="text-xs text-muted-foreground">
            Showing {filteredRuns.length} of {runs.length}
          </div>
        </CardHeader>
        <div className="space-y-3">
          {isLoading ? (
            <div className="text-sm text-muted-foreground">Loading runs...</div>
          ) : filteredRuns.length === 0 ? (
            <div className="rounded-2xl border border-dashed border-border p-6 text-sm text-muted-foreground">
              No runs match these filters.
              <div className="mt-3 flex flex-wrap gap-2">
                <Button variant="outline" size="sm" type="button" onClick={resetFilters}>
                  Clear filters
                </Button>
                <Button variant="primary" size="sm" type="button" onClick={() => navigate("/workflows/new")}>
                  Start a run
                </Button>
              </div>
            </div>
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
                        <div className="text-xs text-muted-foreground">
                          Run {run.id.slice(0, 8)} · {formatRelative(runUpdatedAt(run))}
                        </div>
                      </div>
                      <div className="text-xs text-muted-foreground">
                        {formatDuration(runDurationMs(run))}
                      </div>
                      <RunStatusBadge status={run.status} />
                    </div>
                    <div>
                      <div className="mb-2 flex items-center justify-between text-xs text-muted-foreground">
                        <span>
                          {progress.activeStep ? (
                            <>
                              Step: {progress.activeStep}
                              {progress.activeStatus ? ` · ${progress.activeStatus === "waiting" ? "awaiting approval" : progress.activeStatus}` : ""}
                            </>
                          ) : (
                            <span className="capitalize">{run.status || "pending"}</span>
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
                <div className="text-xs uppercase tracking-[0.2em] text-muted-foreground">Run</div>
                <h3 className="text-xl font-semibold text-ink">{selectedRun.id.slice(0, 12)}</h3>
              </div>
              <RunStatusBadge status={selectedRun.status} />
            </div>
            <div className="rounded-2xl border border-border bg-card/70 p-4 text-sm text-muted-foreground">
              <div>Workflow: {workflowMap.get(selectedRun.workflow_id) || selectedRun.workflow_id}</div>
              <div>Started: {formatRelative(runUpdatedAt(selectedRun))}</div>
              <div>Org: {selectedRun.org_id || "default"}</div>
            </div>
            <div>
              <h4 className="text-sm font-semibold text-ink">Steps</h4>
              <div className="mt-3 space-y-2">
                {Object.values(selectedRun.steps || {}).length === 0 ? (
                  <div className="text-sm text-muted-foreground">No steps reported yet.</div>
                ) : (
                  Object.values(selectedRun.steps || {}).map((step) => (
                    <div key={step.step_id} className="rounded-xl border border-border bg-card/70 px-3 py-2 text-sm">
                      <div className="flex items-center justify-between">
                        <span className="font-semibold text-ink">{step.step_id}</span>
                        <span className="text-xs text-muted-foreground">{step.status}</span>
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
