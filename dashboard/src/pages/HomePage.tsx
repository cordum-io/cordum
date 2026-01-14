import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Area, AreaChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { Link, useNavigate } from "react-router-dom";
import { Play, XCircle, Save, Trash2, RotateCcw } from "lucide-react";
import { api } from "../lib/api";
import { formatCount, formatRelative, formatShortDate, epochToMillis } from "../lib/format";
import { getDLQGuidance, getGuidanceSeverityBg } from "../lib/dlq-guidance";
import { useAllRuns, useWorkflows } from "../hooks/useWorkflows";
import { useEventStore } from "../state/events";
import { usePinStore } from "../state/pins";
import { Card, CardHeader, CardTitle } from "../components/ui/Card";
import { MetricCard } from "../components/MetricCard";
import { ProgressBar } from "../components/ProgressBar";
import { RunStatusBadge } from "../components/StatusBadge";
import { Button } from "../components/ui/Button";
import { Drawer } from "../components/ui/Drawer";
import { Textarea } from "../components/ui/Textarea";
import { Input } from "../components/ui/Input";
import type { JobRecord, StepRun, WorkflowRun, Workflow } from "../types/api";

type RunPreset = {
  id: string;
  name: string;
  workflowId: string;
  payload: string;
};

function loadPresets(): RunPreset[] {
  try {
    const stored = localStorage.getItem("cordum-run-presets");
    return stored ? JSON.parse(stored) : [];
  } catch {
    return [];
  }
}

function savePresets(presets: RunPreset[]) {
  localStorage.setItem("cordum-run-presets", JSON.stringify(presets));
}

function countFanoutChildren(step: StepRun): { total: number; completed: number } {
  const children = Object.values(step.children || {});
  if (children.length === 0) return { total: 0, completed: 0 };
  const completed = children.filter((c) =>
    ["succeeded", "failed", "cancelled", "timed_out"].includes(c.status)
  ).length;
  return { total: children.length, completed };
}

function runProgress(run: WorkflowRun) {
  const steps = Object.values(run.steps || {});
  if (steps.length === 0) {
    return { percent: 0, activeStep: "", activeStatus: "", fanout: null as { total: number; completed: number } | null };
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
  const fanout = active ? countFanoutChildren(active) : null;
  return {
    percent: Math.round((completed / steps.length) * 100),
    activeStep: active?.step_id || "",
    activeStatus: active?.status || "",
    fanout: fanout && fanout.total > 0 ? fanout : null,
  };
}

function runTimestamp(run: WorkflowRun) {
  return run.updated_at || run.started_at || run.created_at || "";
}

function buildActivity(jobs: JobRecord[]) {
  const buckets = new Map<string, { time: string; succeeded: number; failed: number; running: number; pending: number }>();
  jobs.forEach((job) => {
    const ms = epochToMillis(job.updated_at);
    if (!ms) {
      return;
    }
    const date = new Date(ms);
    const key = `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}-${date.getHours()}`;
    const bucket = buckets.get(key) || {
      time: new Date(date.getFullYear(), date.getMonth(), date.getDate(), date.getHours()).toISOString(),
      succeeded: 0,
      failed: 0,
      running: 0,
      pending: 0,
    };
    if (job.state === "SUCCEEDED") {
      bucket.succeeded += 1;
    } else if (job.state === "FAILED" || job.state === "DENIED" || job.state === "TIMEOUT") {
      bucket.failed += 1;
    } else if (job.state === "RUNNING" || job.state === "DISPATCHED") {
      bucket.running += 1;
    } else {
      bucket.pending += 1;
    }
    buckets.set(key, bucket);
  });
  return Array.from(buckets.values()).sort((a, b) => a.time.localeCompare(b.time));
}

function jobUpdatedAt(updatedAt?: number): string {
  const ms = epochToMillis(updatedAt);
  if (!ms) {
    return "";
  }
  return new Date(ms).toISOString();
}

export function HomePage() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const approvalsQuery = useQuery({
    queryKey: ["approvals", "summary"],
    queryFn: () => api.listApprovals(20),
  });
  const dlqQuery = useQuery({
    queryKey: ["dlq", "summary"],
    queryFn: () => api.listDLQPage(20),
  });
  const jobsQuery = useQuery({
    queryKey: ["jobs", "summary"],
    queryFn: () => api.listJobs({ limit: 200 }),
  });
  const policyQuery = useQuery({
    queryKey: ["policy", "snapshots"],
    queryFn: () => api.listPolicySnapshots(),
  });
  const { runs, isLoading } = useAllRuns({ limit: 120 });
  const events = useEventStore((state) => state.events.slice(0, 6));
  const pinned = usePinStore((state) => state.items);
  const removePinned = usePinStore((state) => state.removeItem);
  const workflowsQuery = useWorkflows();
  const workflowMap = useMemo(() => {
    const map = new Map<string, { id: string; name?: string; input_schema?: Record<string, unknown> }>();
    workflowsQuery.data?.forEach((w) => map.set(w.id, w));
    return map;
  }, [workflowsQuery.data]);

  const cancelMutation = useMutation({
    mutationFn: ({ workflowId, runId }: { workflowId: string; runId: string }) =>
      api.cancelRun(workflowId, runId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["runs"] }),
  });

  const rerunMutation = useMutation({
    mutationFn: (payload: { runId: string }) => api.rerunRun(payload.runId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["runs"] }),
  });

  // Run drawer state
  const [runDrawerWorkflow, setRunDrawerWorkflow] = useState<Workflow | null>(null);
  const [runPayload, setRunPayload] = useState("{}");
  const [runPayloadError, setRunPayloadError] = useState<string | null>(null);
  const [presets, setPresets] = useState<RunPreset[]>(loadPresets);
  const [newPresetName, setNewPresetName] = useState("");
  const [selectedPresetId, setSelectedPresetId] = useState("");

  const startRunMutation = useMutation({
    mutationFn: ({ workflowId, body }: { workflowId: string; body: Record<string, unknown> }) =>
      api.startRun(workflowId, body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["runs"] });
      setRunDrawerWorkflow(null);
      setRunPayload("{}");
    },
    onError: (error: Error) => setRunPayloadError(error.message),
  });

  const workflowPresets = useMemo(
    () => presets.filter((p) => p.workflowId === runDrawerWorkflow?.id),
    [presets, runDrawerWorkflow]
  );

  const handleSavePreset = () => {
    if (!newPresetName.trim() || !runDrawerWorkflow) return;
    const newPreset: RunPreset = {
      id: `${Date.now()}`,
      name: newPresetName.trim(),
      workflowId: runDrawerWorkflow.id,
      payload: runPayload,
    };
    const updated = [...presets, newPreset];
    setPresets(updated);
    savePresets(updated);
    setNewPresetName("");
  };

  const handleDeletePreset = (id: string) => {
    const updated = presets.filter((p) => p.id !== id);
    setPresets(updated);
    savePresets(updated);
    if (selectedPresetId === id) setSelectedPresetId("");
  };

  const handleLoadPreset = (preset: RunPreset) => {
    setRunPayload(preset.payload);
    setSelectedPresetId(preset.id);
  };
  const policyCount = useMemo(() => {
    const data = policyQuery.data as { snapshots?: string[] } | undefined;
    return data?.snapshots?.length ?? 0;
  }, [policyQuery.data]);

  const liveRuns = useMemo(
    () =>
      runs
        .filter((run) => ["running", "waiting", "pending"].includes(run.status))
        .sort((a, b) => runTimestamp(b).localeCompare(runTimestamp(a)))
        .slice(0, 5),
    [runs]
  );

  const failedRunsCount = useMemo(
    () => runs.filter((run) => ["failed", "timed_out"].includes(run.status)).length,
    [runs]
  );

  const approvals = approvalsQuery.data?.items || [];
  const attentionApprovals = useMemo(
    () =>
      approvals
        .slice()
        .sort((a, b) => b.job.updated_at - a.job.updated_at)
        .slice(0, 3),
    [approvals]
  );
  const attentionFailedRuns = useMemo(
    () =>
      runs
        .filter((run) => ["failed", "timed_out"].includes(run.status))
        .sort((a, b) => runTimestamp(b).localeCompare(runTimestamp(a)))
        .slice(0, 3),
    [runs]
  );
  const dlqEntries = dlqQuery.data?.items || [];
  const attentionDlq = useMemo(
    () =>
      dlqEntries
        .slice()
        .sort((a, b) => (a.created_at || "").localeCompare(b.created_at || ""))
        .slice(0, 3),
    [dlqEntries]
  );

  const oldestApproval = useMemo(() => {
    if (!approvals.length) {
      return "-";
    }
    const oldest = approvals.reduce((min, item) => (item.job.updated_at < min.job.updated_at ? item : min), approvals[0]);
    return formatRelative(jobUpdatedAt(oldest.job.updated_at));
  }, [approvals]);

  const dlqOldest = useMemo(() => {
    if (!dlqEntries.length) {
      return "-";
    }
    const sorted = dlqEntries.slice().sort((a, b) => (a.created_at || "").localeCompare(b.created_at || ""));
    return formatRelative(sorted[0]?.created_at);
  }, [dlqEntries]);

  const activityData = useMemo(() => buildActivity(jobsQuery.data?.items || []), [jobsQuery.data]);

  return (
    <div className="space-y-8">
      <section className="grid gap-4 lg:grid-cols-4">
        <MetricCard
          title="Approvals"
          value={formatCount(approvalsQuery.data?.items.length || 0)}
          detail="Pending approval requests"
        />
        <MetricCard
          title="Failed Runs"
          value={formatCount(failedRunsCount)}
          detail="Runs needing attention"
        />
        <MetricCard
          title="DLQ Items"
          value={formatCount(dlqQuery.data?.items.length || 0)}
          detail="Dead-letter queue backlog"
        />
        <MetricCard
          title="Policy Snapshots"
          value={formatCount(policyCount)}
          detail="Active policy versions"
        />
      </section>

      <section className="grid gap-4 lg:grid-cols-3">
        <div className="attention-card border-l-4 border-warning">
          <div className="flex items-center justify-between">
            <div>
              <div className="text-xs uppercase tracking-[0.2em] text-muted">Approvals</div>
              <div className="text-lg font-semibold text-ink">{formatCount(approvals.length)}</div>
            </div>
            <Button variant="outline" size="sm" type="button" onClick={() => navigate("/policy")}>
              Open inbox
            </Button>
          </div>
          <div className="mt-2 text-xs text-muted">Oldest waiting {oldestApproval}</div>
          {attentionApprovals.length ? (
            <div className="mt-3 space-y-2">
              {attentionApprovals.map((item) => (
                <div key={item.job.id} className="list-row">
                  <div className="flex items-center justify-between text-xs text-muted">
                    <span>Job {item.job.id.slice(0, 8)}</span>
                    <span>{formatRelative(jobUpdatedAt(item.job.updated_at))}</span>
                  </div>
                  <div className="text-sm font-semibold text-ink">{item.job.topic || "job"}</div>
                </div>
              ))}
            </div>
          ) : (
            <div className="mt-3 text-sm text-muted">No approvals waiting.</div>
          )}
        </div>

        <div className="attention-card border-l-4 border-danger">
          <div className="flex items-center justify-between">
            <div>
              <div className="text-xs uppercase tracking-[0.2em] text-muted">Failed runs</div>
              <div className="text-lg font-semibold text-ink">{formatCount(failedRunsCount)}</div>
            </div>
            <Button variant="outline" size="sm" type="button" onClick={() => navigate("/runs")}>
              Review
            </Button>
          </div>
          <div className="mt-2 text-xs text-muted">Last 24h failures</div>
          {attentionFailedRuns.length ? (
            <div className="mt-3 space-y-2">
              {attentionFailedRuns.map((run) => (
                <div key={run.id} className="list-row">
                  <div className="flex items-center justify-between text-xs text-muted">
                    <Link to={`/runs/${run.id}`} className="hover:underline">
                      Run {run.id.slice(0, 8)}
                    </Link>
                    <span>{formatRelative(runTimestamp(run))}</span>
                  </div>
                  <div className="flex items-center justify-between">
                    <div className="text-sm font-semibold text-ink">{run.workflow_id}</div>
                    <Button
                      variant="outline"
                      size="sm"
                      type="button"
                      onClick={(e) => {
                        e.stopPropagation();
                        rerunMutation.mutate({ runId: run.id });
                      }}
                      disabled={rerunMutation.isPending}
                      title="Rerun this workflow"
                    >
                      <RotateCcw className="h-3 w-3" />
                    </Button>
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <div className="mt-3 text-sm text-muted">No failed runs right now.</div>
          )}
        </div>

        <div className="attention-card border-l-4 border-danger">
          <div className="flex items-center justify-between">
            <div>
              <div className="text-xs uppercase tracking-[0.2em] text-muted">DLQ backlog</div>
              <div className="text-lg font-semibold text-ink">{formatCount(dlqEntries.length)}</div>
            </div>
            <Button variant="outline" size="sm" type="button" onClick={() => navigate("/system")}>
              Open DLQ
            </Button>
          </div>
          <div className="mt-2 text-xs text-muted">Oldest entry {dlqOldest}</div>
          {attentionDlq.length ? (
            <div className="mt-3 space-y-2">
              {attentionDlq.map((entry) => {
                const guidance = getDLQGuidance(entry);
                return (
                  <div key={entry.job_id} className="list-row">
                    <div className="flex items-center justify-between text-xs text-muted">
                      <Link to={`/jobs/${entry.job_id}`} className="hover:underline">
                        Job {entry.job_id.slice(0, 8)}
                      </Link>
                      <span>{formatRelative(entry.created_at)}</span>
                    </div>
                    <div className="text-sm font-semibold text-ink">
                      {entry.reason_code ? (
                        <span className="font-mono text-warning">{entry.reason_code}</span>
                      ) : (
                        entry.reason || entry.status || "Failed job"
                      )}
                    </div>
                    {guidance ? (
                      <div className={`mt-2 rounded-lg border p-2 text-xs ${getGuidanceSeverityBg(guidance.severity)}`}>
                        <div className="font-medium text-ink">{guidance.title}</div>
                        {guidance.action?.href ? (
                          <Link to={guidance.action.href} className="text-accent hover:underline">
                            {guidance.action.label}
                          </Link>
                        ) : null}
                      </div>
                    ) : null}
                  </div>
                );
              })}
            </div>
          ) : (
            <div className="mt-3 text-sm text-muted">DLQ is clear.</div>
          )}
        </div>
      </section>

      <section className="grid gap-6 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>Live Runs</CardTitle>
            <Button variant="ghost" size="sm" type="button" onClick={() => navigate("/runs")}>
              View all
            </Button>
          </CardHeader>
          {isLoading ? (
            <div className="text-sm text-muted">Loading run activity...</div>
          ) : liveRuns.length === 0 ? (
            <div className="rounded-2xl border border-dashed border-border p-6 text-sm text-muted">
              No active runs right now.
            </div>
          ) : (
            <div className="space-y-4">
              {liveRuns.map((run) => {
                const { percent, activeStep, activeStatus, fanout } = runProgress(run);
                const statusLabel = activeStatus === "waiting" ? "awaiting approval" : activeStatus === "running" ? "executing" : activeStatus === "pending" ? "queued" : activeStatus;
                return (
                  <div key={run.id} className="rounded-2xl border border-border bg-white/70 p-4">
                    <div className="flex flex-col justify-between gap-3 lg:flex-row lg:items-center">
                      <div>
                        <Link to={`/runs/${run.id}`} className="text-sm font-semibold text-ink hover:underline">
                          {run.workflow_id}
                        </Link>
                        <div className="text-xs text-muted">
                          Run {run.id.slice(0, 8)} · {formatRelative(runTimestamp(run))}
                        </div>
                      </div>
                      <div className="flex items-center gap-2">
                        <RunStatusBadge status={run.status} />
                        <Button
                          variant="outline"
                          size="sm"
                          type="button"
                          onClick={() => cancelMutation.mutate({ workflowId: run.workflow_id, runId: run.id })}
                          disabled={cancelMutation.isPending}
                          title="Cancel run"
                        >
                          <XCircle className="h-3 w-3" />
                        </Button>
                      </div>
                    </div>
                    <div className="mt-3">
                      <div className="mb-2 flex items-center justify-between text-xs text-muted">
                        <span>
                          {activeStep ? (
                            <>
                              Step: <span className="font-medium text-ink">{activeStep}</span>
                              {statusLabel ? ` · ${statusLabel}` : ""}
                            </>
                          ) : (
                            "Queued"
                          )}
                          {fanout ? (
                            <span className="ml-2 rounded bg-accent/10 px-1.5 py-0.5 text-[10px] font-medium text-accent">
                              {fanout.completed}/{fanout.total} items
                            </span>
                          ) : null}
                        </span>
                        <span>{percent}%</span>
                      </div>
                      <ProgressBar value={percent} />
                    </div>
                    <div className="mt-2 flex justify-end">
                      <Link to={`/runs/${run.id}`}>
                        <Button variant="ghost" size="sm" type="button">
                          View timeline
                        </Button>
                      </Link>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Pinned Workflows</CardTitle>
          </CardHeader>
          {pinned.length === 0 ? (
            <div className="rounded-2xl border border-dashed border-border p-6 text-sm text-muted">
              Pin workflows or pools to keep them close.
            </div>
          ) : (
            <div className="space-y-3">
              {pinned.map((item) => {
                const isWorkflow = item.type === "workflow";
                const workflow = isWorkflow ? workflowMap.get(item.id) : null;
                return (
                  <div key={item.id} className="rounded-2xl border border-border bg-white/70 p-3">
                    <div className="flex items-center justify-between gap-2">
                      <div className="min-w-0 flex-1">
                        <Link
                          to={isWorkflow ? `/workflows/${item.id}` : `/system?tab=workers`}
                          className="text-sm font-semibold text-ink hover:underline"
                        >
                          {item.label}
                        </Link>
                        <div className="text-xs text-muted">{item.detail || item.type}</div>
                      </div>
                      <div className="flex items-center gap-1">
                        {isWorkflow && workflow ? (
                          <Button
                            variant="primary"
                            size="sm"
                            type="button"
                            title="Start new run"
                            onClick={() => {
                              setRunDrawerWorkflow(workflow as Workflow);
                              setRunPayload("{}");
                              setRunPayloadError(null);
                              setSelectedPresetId("");
                            }}
                          >
                            <Play className="h-3 w-3" />
                            Run
                          </Button>
                        ) : null}
                        <Button
                          variant="ghost"
                          size="sm"
                          type="button"
                          onClick={() => removePinned(item.id)}
                          title="Unpin"
                        >
                          <XCircle className="h-3 w-3" />
                        </Button>
                      </div>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </Card>
      </section>

      <section className="grid gap-6 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>Activity Timeline</CardTitle>
            <div className="text-xs text-muted">Last 24h</div>
          </CardHeader>
          {activityData.length === 0 ? (
            <div className="rounded-2xl border border-dashed border-border p-6 text-sm text-muted">
              No recent job activity.
            </div>
          ) : (
            <div className="h-64">
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={activityData} margin={{ left: 0, right: 0, top: 10, bottom: 0 }}>
                  <defs>
                    <linearGradient id="colorSuccess" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%" stopColor="#1f7a57" stopOpacity={0.6} />
                      <stop offset="95%" stopColor="#1f7a57" stopOpacity={0.05} />
                    </linearGradient>
                    <linearGradient id="colorFail" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%" stopColor="#b83a3a" stopOpacity={0.5} />
                      <stop offset="95%" stopColor="#b83a3a" stopOpacity={0.05} />
                    </linearGradient>
                  </defs>
                  <XAxis
                    dataKey="time"
                    tickFormatter={(value) => formatShortDate(value)}
                    tick={{ fontSize: 10 }}
                    axisLine={false}
                    tickLine={false}
                  />
                  <YAxis hide />
                  <Tooltip
                    labelFormatter={(value) => formatShortDate(value as string)}
                    formatter={(value: number) => [value, "runs"]}
                  />
                  <Area type="monotone" dataKey="succeeded" stroke="#1f7a57" fill="url(#colorSuccess)" />
                  <Area type="monotone" dataKey="failed" stroke="#b83a3a" fill="url(#colorFail)" />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          )}
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Recent Events</CardTitle>
            <div className="text-xs text-muted">Live bus updates</div>
          </CardHeader>
          {events.length === 0 ? (
            <div className="rounded-2xl border border-dashed border-border p-6 text-sm text-muted">
              Waiting for live events.
            </div>
          ) : (
            <div className="space-y-3">
              {events.map((event) => (
                <div key={event.id} className="rounded-2xl border border-border bg-white/70 p-3">
                  <div className="text-sm font-semibold text-ink">{event.title}</div>
                  <div className="text-xs text-muted">{event.detail || ""}</div>
                  <div className="text-[11px] text-muted">{formatRelative(event.timestamp)}</div>
                </div>
              ))}
            </div>
          )}
        </Card>
      </section>

      {/* Run Workflow Drawer */}
      <Drawer open={Boolean(runDrawerWorkflow)} onClose={() => setRunDrawerWorkflow(null)}>
        {runDrawerWorkflow ? (
          <div className="space-y-5">
            <div>
              <div className="text-xs uppercase tracking-[0.2em] text-muted">Start Run</div>
              <div className="mt-1 text-lg font-semibold text-ink">
                {runDrawerWorkflow.name || runDrawerWorkflow.id}
              </div>
            </div>

            {/* Presets */}
            {workflowPresets.length > 0 ? (
              <div>
                <div className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Saved Presets</div>
                <div className="mt-2 space-y-2">
                  {workflowPresets.map((preset) => (
                    <div
                      key={preset.id}
                      className={`flex items-center justify-between rounded-xl border p-2 ${
                        selectedPresetId === preset.id ? "border-accent bg-accent/5" : "border-border"
                      }`}
                    >
                      <button
                        type="button"
                        onClick={() => handleLoadPreset(preset)}
                        className="flex-1 text-left text-sm font-medium text-ink hover:text-accent"
                      >
                        {preset.name}
                      </button>
                      <Button
                        variant="ghost"
                        size="sm"
                        type="button"
                        onClick={() => handleDeletePreset(preset.id)}
                        title="Delete preset"
                      >
                        <Trash2 className="h-3 w-3" />
                      </Button>
                    </div>
                  ))}
                </div>
              </div>
            ) : null}

            {/* Input Schema Info */}
            {runDrawerWorkflow.input_schema ? (
              <div className="rounded-xl border border-border bg-white/50 p-3">
                <div className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Input Schema</div>
                <pre className="mt-2 max-h-32 overflow-auto text-[10px] text-muted">
                  {JSON.stringify(runDrawerWorkflow.input_schema, null, 2)}
                </pre>
              </div>
            ) : null}

            {/* Payload Input */}
            <div>
              <div className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Input Payload (JSON)</div>
              <Textarea
                rows={6}
                value={runPayload}
                onChange={(event) => {
                  setRunPayload(event.target.value);
                  setRunPayloadError(null);
                }}
                className="mt-2 font-mono text-xs"
              />
              {runPayloadError ? (
                <div className="mt-1 text-xs text-danger">{runPayloadError}</div>
              ) : null}
            </div>

            {/* Save Preset */}
            <div className="flex gap-2">
              <Input
                value={newPresetName}
                onChange={(event) => setNewPresetName(event.target.value)}
                placeholder="Preset name"
                className="flex-1"
              />
              <Button
                variant="outline"
                size="sm"
                type="button"
                onClick={handleSavePreset}
                disabled={!newPresetName.trim()}
              >
                <Save className="h-3 w-3" />
                Save
              </Button>
            </div>

            {/* Actions */}
            <div className="flex gap-2">
              <Button
                variant="outline"
                type="button"
                onClick={() => setRunDrawerWorkflow(null)}
              >
                Cancel
              </Button>
              <Button
                variant="primary"
                type="button"
                onClick={() => {
                  setRunPayloadError(null);
                  try {
                    const body = JSON.parse(runPayload || "{}");
                    startRunMutation.mutate({ workflowId: runDrawerWorkflow.id, body });
                  } catch (error) {
                    setRunPayloadError(error instanceof Error ? error.message : "Invalid JSON");
                  }
                }}
                disabled={startRunMutation.isPending}
              >
                {startRunMutation.isPending ? "Starting..." : "Launch Run"}
              </Button>
            </div>
          </div>
        ) : null}
      </Drawer>
    </div>
  );
}
