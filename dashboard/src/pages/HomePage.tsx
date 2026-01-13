import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { Area, AreaChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { useNavigate } from "react-router-dom";
import { api } from "../lib/api";
import { formatCount, formatRelative, formatShortDate, epochToMillis } from "../lib/format";
import { useAllRuns } from "../hooks/useWorkflows";
import { useEventStore } from "../state/events";
import { usePinStore } from "../state/pins";
import { Card, CardHeader, CardTitle } from "../components/ui/Card";
import { MetricCard } from "../components/MetricCard";
import { ProgressBar } from "../components/ProgressBar";
import { RunStatusBadge } from "../components/StatusBadge";
import { Button } from "../components/ui/Button";
import type { JobRecord, WorkflowRun } from "../types/api";

function runProgress(run: WorkflowRun) {
  const steps = Object.values(run.steps || {});
  if (steps.length === 0) {
    return { percent: 0, activeStep: "" };
  }
  const completed = steps.filter((step) =>
    ["succeeded", "failed", "cancelled", "timed_out"].includes(step.status)
  ).length;
  const active = steps.find((step) => ["running", "waiting"].includes(step.status));
  return {
    percent: Math.round((completed / steps.length) * 100),
    activeStep: active?.step_id || "",
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
                    <span>Run {run.id.slice(0, 8)}</span>
                    <span>{formatRelative(runTimestamp(run))}</span>
                  </div>
                  <div className="text-sm font-semibold text-ink">{run.workflow_id}</div>
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
              {attentionDlq.map((entry) => (
                <div key={entry.job_id} className="list-row">
                  <div className="flex items-center justify-between text-xs text-muted">
                    <span>Job {entry.job_id.slice(0, 8)}</span>
                    <span>{formatRelative(entry.created_at)}</span>
                  </div>
                  <div className="text-sm font-semibold text-ink">{entry.reason || entry.status || "Failed job"}</div>
                </div>
              ))}
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
                const { percent, activeStep } = runProgress(run);
                return (
                  <div key={run.id} className="rounded-2xl border border-border bg-white/70 p-4">
                    <div className="flex flex-col justify-between gap-3 lg:flex-row lg:items-center">
                      <div>
                        <div className="text-sm font-semibold text-ink">{run.workflow_id}</div>
                        <div className="text-xs text-muted">Run {run.id.slice(0, 8)}</div>
                      </div>
                      <RunStatusBadge status={run.status} />
                    </div>
                    <div className="mt-3">
                      <div className="mb-2 flex items-center justify-between text-xs text-muted">
                        <span>{activeStep ? `Step: ${activeStep}` : "Starting"}</span>
                        <span>{percent}%</span>
                      </div>
                      <ProgressBar value={percent} />
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
              {pinned.map((item) => (
                <div key={item.id} className="rounded-2xl border border-border bg-white/70 p-3">
                  <div className="text-sm font-semibold text-ink">{item.label}</div>
                  <div className="text-xs text-muted">{item.detail || item.type}</div>
                </div>
              ))}
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
    </div>
  );
}
