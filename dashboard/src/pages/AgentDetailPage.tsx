/*
 * DESIGN: "Control Surface" — Agent Detail
 * OPERATE / Agents / :id
 * Agent-specific view: metrics, safety breakdown, policy bindings, recent jobs
 */
import { useMemo } from "react";
import { useParams, useNavigate } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { PageHeader } from "@/components/layout/PageHeader";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { Button } from "@/components/ui/Button";
import { SkeletonCard, SkeletonTable } from "@/components/ui/Skeleton";
import {
  Cpu, ArrowLeft, RefreshCw, AlertTriangle,
} from "lucide-react";
import { cn, formatRelativeTime, formatDuration } from "@/lib/utils";
import { Progress } from "@/components/ui/progress";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import {
  BarChart, Bar, ResponsiveContainer, XAxis, YAxis, Tooltip, CartesianGrid,
} from "recharts";
import { useWorker, useWorkerJobs } from "@/hooks/useWorkers";
import { usePolicyBundles } from "@/hooks/usePolicies";
import { ChartTooltipCompact as ChartTooltip } from "@/components/ui/ChartTooltip";
import type { Job } from "@/api/types";

function SafetyBadge({ decision }: { decision: string }) {
  const config: Record<string, { color: string; bg: string; label: string }> = {
    allow: { color: "text-[var(--color-success)]", bg: "bg-[var(--color-success)]/10", label: "ALLOW" },
    deny: { color: "text-[var(--color-governance)]", bg: "bg-[var(--color-governance)]/10", label: "DENY" },
    require_approval: { color: "text-[var(--color-warning)]", bg: "bg-[var(--color-warning)]/10", label: "APPROVAL" },
    allow_with_constraints: { color: "text-[var(--color-info)]", bg: "bg-[var(--color-info)]/10", label: "CONSTRAINED" },
    throttle: { color: "text-[var(--color-warning)]", bg: "bg-[var(--color-warning)]/10", label: "THROTTLE" },
  };
  const c = config[decision] ?? { color: "text-muted-foreground", bg: "bg-surface-2", label: decision.toUpperCase() };
  return <span className={cn("px-1.5 py-0.5 rounded font-mono text-xs font-semibold", c.color, c.bg)}>{c.label}</span>;
}

function deriveSafetyBreakdown(jobs: Job[]) {
  const breakdown = { allow: 0, deny: 0, require_approval: 0, allow_with_constraints: 0, throttle: 0 };
  for (const job of jobs) {
    const t = job.safetyDecision?.type;
    if (t && t in breakdown) {
      breakdown[t as keyof typeof breakdown]++;
    }
  }
  return breakdown;
}

function deriveHourlyActivity(jobs: Job[]) {
  const now = Date.now();
  const buckets: Record<number, { jobs: number; denied: number }> = {};
  for (let h = 0; h < 24; h++) buckets[h] = { jobs: 0, denied: 0 };

  for (const job of jobs) {
    if (!job.createdAt) continue;
    const created = new Date(job.createdAt).getTime();
    const hoursAgo = (now - created) / 3_600_000;
    if (hoursAgo < 0 || hoursAgo >= 24) continue;
    const bucket = 23 - Math.floor(hoursAgo);
    buckets[bucket].jobs++;
    if (job.safetyDecision?.type === "deny") buckets[bucket].denied++;
  }

  return Array.from({ length: 24 }, (_, i) => ({
    hour: `${String(i).padStart(2, "0")}:00`,
    jobs: buckets[i].jobs,
    denied: buckets[i].denied,
  }));
}

function jobStatusVariant(status: string): "healthy" | "danger" | "warning" | "muted" | "governance" {
  switch (status) {
    case "succeeded": return "healthy";
    case "denied": return "governance";
    case "failed":
    case "timeout":
    case "output_quarantined": return "danger";
    case "running":
    case "dispatched":
    case "approval_required": return "warning";
    default: return "muted";
  }
}

export default function AgentDetailPage() {
  const { id } = useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  const { data: agent, isLoading: agentLoading, error: agentError } = useWorker(id);
  const { data: jobs, isLoading: jobsLoading, isError: jobsError, error: jobsErr, refetch: refetchJobs } = useWorkerJobs(id);
  const { data: bundlesData } = usePolicyBundles();
  const bundles = bundlesData?.items ?? [];

  const safetyBreakdown = useMemo(() => deriveSafetyBreakdown(jobs ?? []), [jobs]);
  const hourlyActivity = useMemo(() => deriveHourlyActivity(jobs ?? []), [jobs]);
  const totalDecisions = Object.values(safetyBreakdown).reduce((a, b) => a + b, 0);
  const allowRate = totalDecisions > 0 ? Math.round((safetyBreakdown.allow / totalDecisions) * 100) : 0;

  const handleRefresh = () => {
    queryClient.invalidateQueries({ queryKey: ["worker", id] });
    queryClient.invalidateQueries({ queryKey: ["worker-jobs", id] });
  };

  const isOnline = agent
    ? ["online", "active", "idle", "busy"].includes(agent.status)
    : false;

  if (agentError) {
    return (
      <div className="space-y-6">
        <PageHeader
          label="Operate · Agents"
          title="Agent Detail"
          actions={
            <Button variant="ghost" size="sm" onClick={() => navigate("/agents")}>
              <ArrowLeft className="w-3 h-3 mr-1" />
              Back
            </Button>
          }
        />
        <div className="instrument-card p-8 text-center">
          <AlertTriangle className="w-8 h-8 text-destructive mx-auto mb-3" />
          <p className="text-sm text-foreground font-medium mb-1">Failed to load agent</p>
          <p className="text-xs text-muted-foreground mb-4">
            {agentError instanceof Error ? agentError.message : "An unexpected error occurred"}
          </p>
          <Button variant="outline" size="sm" onClick={handleRefresh}>
            <RefreshCw className="w-3 h-3 mr-1" />
            Retry
          </Button>
        </div>
      </div>
    );
  }

  if (agentLoading) {
    return (
      <div className="space-y-6">
        <PageHeader
          label="Operate · Agents"
          title="Loading..."
          actions={
            <Button variant="ghost" size="sm" onClick={() => navigate("/agents")}>
              <ArrowLeft className="w-3 h-3 mr-1" />
              Back
            </Button>
          }
        />
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
          <SkeletonCard />
          <SkeletonCard />
          <SkeletonCard />
        </div>
        <SkeletonCard />
        <SkeletonTable rows={5} />
      </div>
    );
  }

  if (jobsError) {
    return <ErrorBanner message={jobsErr instanceof Error ? jobsErr.message : "Failed to load agent jobs"} onRetry={() => void refetchJobs()} />;
  }

  return (
    <div className="space-y-6">
      <PageHeader
        label="Operate · Agents"
        title={agent?.name || id || "Agent Detail"}
        subtitle={`${agent?.pool ?? "unknown"} pool · ${agent?.capabilities?.join(", ") || "no capabilities"}`}
        actions={
          <div className="flex items-center gap-2">
            <Button variant="ghost" size="sm" onClick={() => navigate("/agents")}>
              <ArrowLeft className="w-3 h-3 mr-1" />
              Back
            </Button>
            <Button variant="outline" size="sm" onClick={handleRefresh}>
              <RefreshCw className="w-3 h-3 mr-1" />
              Refresh
            </Button>
          </div>
        }
      />

      {/* Agent Status + Metrics */}
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
        {/* Agent Info Card */}
        <div className="instrument-card">
          <div className="flex items-center gap-3 mb-4">
            <div className={cn(
              "w-10 h-10 rounded-2xl flex items-center justify-center",
              isOnline ? "bg-[var(--color-success)]/10" : "bg-destructive/10"
            )}>
              <Cpu className={cn("w-5 h-5", isOnline ? "text-[var(--color-success)]" : "text-destructive")} />
            </div>
            <div>
              <p className="font-mono text-sm text-foreground font-medium">{agent?.id}</p>
              <StatusBadge variant={isOnline ? "healthy" : "danger"}>
                {agent?.status ?? "unknown"}
              </StatusBadge>
            </div>
          </div>

          <div className="space-y-3">
            <div className="flex justify-between text-xs">
              <span className="text-muted-foreground">CPU</span>
              <span className="font-mono text-foreground">{agent?.cpuLoad ?? 0}%</span>
            </div>
            <Progress value={agent?.cpuLoad ?? 0} className="h-1.5" />

            <div className="flex justify-between text-xs">
              <span className="text-muted-foreground">Memory</span>
              <span className="font-mono text-foreground">{agent?.memoryLoad ?? 0}%</span>
            </div>
            <Progress value={agent?.memoryLoad ?? 0} className="h-1.5" />

            <div className="grid grid-cols-2 gap-3 pt-2 border-t border-border">
              <div>
                <p className="text-xs text-muted-foreground">Version</p>
                <p className="font-mono text-xs text-foreground">{agent?.version ?? "N/A"}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">Last Heartbeat</p>
                <p className="font-mono text-xs text-foreground">
                  {agent?.lastHeartbeat ? formatRelativeTime(agent.lastHeartbeat) : "N/A"}
                </p>
              </div>
            </div>

            {/* Metadata */}
            <div className="pt-2 border-t border-border space-y-1">
              <p className="text-xs font-mono text-muted-foreground uppercase tracking-widest">Info</p>
              <div className="flex justify-between text-xs">
                <span className="text-muted-foreground">Pool</span>
                <span className="font-mono text-foreground">{agent?.pool ?? "N/A"}</span>
              </div>
              <div className="flex justify-between text-xs">
                <span className="text-muted-foreground">Region</span>
                <span className="font-mono text-foreground">{agent?.region ?? "N/A"}</span>
              </div>
              <div className="flex justify-between text-xs">
                <span className="text-muted-foreground">Type</span>
                <span className="font-mono text-foreground">{agent?.type ?? "N/A"}</span>
              </div>
              <div className="flex justify-between text-xs">
                <span className="text-muted-foreground">Active Jobs</span>
                <span className="font-mono text-foreground">{agent?.activeJobs ?? 0} / {agent?.capacity ?? 0}</span>
              </div>
            </div>
          </div>
        </div>

        {/* Safety Breakdown */}
        <div className="instrument-card">
          <div className="flex items-center justify-between mb-4">
            <h2 className="font-display font-semibold text-sm text-foreground">Safety Decisions</h2>
            <span className="font-mono text-xs text-muted-foreground">{totalDecisions.toLocaleString()} total</span>
          </div>
          <div className="text-center mb-4">
            <span className="font-mono text-3xl font-bold text-foreground">{allowRate}%</span>
            <span className="text-xs text-muted-foreground ml-2">allow rate</span>
          </div>
          {totalDecisions === 0 ? (
            <p className="text-xs text-muted-foreground text-center py-4">
              {jobsLoading ? "Loading safety data..." : "No safety decision data available"}
            </p>
          ) : (
            <div className="space-y-2">
              {Object.entries(safetyBreakdown).map(([key, value]) => {
                const pct = totalDecisions > 0 ? (value / totalDecisions) * 100 : 0;
                const colors: Record<string, string> = {
                  allow: "bg-[var(--color-success)]",
                  deny: "bg-[var(--color-governance)]",
                  require_approval: "bg-[var(--color-warning)]",
                  allow_with_constraints: "bg-[var(--color-info)]",
                  throttle: "bg-[var(--color-warning)]",
                };
                return (
                  <div key={key}>
                    <div className="flex justify-between text-xs mb-1">
                      <span className="text-muted-foreground capitalize">{key.replace(/_/g, " ")}</span>
                      <span className="font-mono text-foreground">{value.toLocaleString()} ({pct.toFixed(1)}%)</span>
                    </div>
                    <div className="w-full h-1.5 rounded-full bg-surface-2 overflow-hidden">
                      <div className={cn("h-full rounded-full transition-all", colors[key] ?? "bg-muted-foreground")} style={{ width: `${pct}%` }} />
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </div>

        {/* Policy Bindings */}
        <div className="instrument-card">
          <h2 className="font-display font-semibold text-sm text-foreground mb-4">Active Policy Bindings</h2>
          {bundles.length === 0 ? (
            <div className="py-6 text-center">
              <p className="text-xs text-muted-foreground">No policy bundles bound to this agent's pool</p>
            </div>
          ) : (
            <div className="space-y-2">
              {bundles.map((b) => (
                <div key={b.id} className="flex items-center justify-between rounded-2xl bg-surface-0 border border-border p-3">
                  <div className="flex items-center gap-2">
                    <AlertTriangle className="w-3.5 h-3.5 text-cordum" />
                    <span className="text-sm font-medium text-foreground">{b.name || b.id}</span>
                  </div>
                  <div className="flex items-center gap-2">
                    <span className="text-xs font-mono text-muted-foreground">{b.rule_count ?? b.rules?.length ?? 0} rules</span>
                    <StatusBadge variant={b.status === "published" ? "healthy" : "muted"}>{b.status ?? "published"}</StatusBadge>
                  </div>
                </div>
              ))}
            </div>
          )}

          {/* Capabilities */}
          <div className="mt-4 pt-3 border-t border-border">
            <p className="text-xs font-mono text-muted-foreground uppercase tracking-widest mb-2">Capabilities</p>
            <div className="flex flex-wrap gap-1.5">
              {agent?.capabilities && agent.capabilities.length > 0 ? (
                agent.capabilities.map((cap) => (
                  <span key={cap} className="px-2 py-0.5 rounded bg-surface-2 text-xs font-mono text-muted-foreground">
                    {cap}
                  </span>
                ))
              ) : (
                <span className="text-xs text-muted-foreground">None</span>
              )}
            </div>
          </div>
        </div>
      </div>

      {/* Hourly Activity Chart */}
      <div className="instrument-card">
        <div className="flex items-center justify-between mb-4">
          <div>
            <h2 className="font-display font-semibold text-sm text-foreground">Hourly Activity</h2>
            <p className="text-xs text-muted-foreground mt-0.5">Jobs processed per hour (last 24h)</p>
          </div>
        </div>
        {jobsLoading ? (
          <div className="h-[200px] flex items-center justify-center">
            <SkeletonCard />
          </div>
        ) : (
          <ResponsiveContainer width="100%" height={200}>
            <BarChart data={hourlyActivity}>
              <CartesianGrid strokeDasharray="3 3" stroke="rgba(255,255,255,0.04)" />
              <XAxis dataKey="hour" tick={{ fontSize: 9, fill: "#5a6a70" }} axisLine={false} tickLine={false} interval={3} />
              <YAxis tick={{ fontSize: 9, fill: "#5a6a70" }} axisLine={false} tickLine={false} />
              <Tooltip content={<ChartTooltip />} cursor={{ fill: "var(--surface-2)" }} />
              <Bar dataKey="jobs" fill="#0f7f7a" radius={[2, 2, 0, 0]} name="Jobs" />
              <Bar dataKey="denied" fill="#7c3aed" radius={[2, 2, 0, 0]} name="Denied" />
            </BarChart>
          </ResponsiveContainer>
        )}
      </div>

      {/* Recent Jobs */}
      <div className="instrument-card overflow-hidden">
        <div className="flex items-center justify-between px-5 py-3 border-b border-border">
          <h2 className="font-display font-semibold text-sm text-foreground">Recent Jobs</h2>
          <Button variant="ghost" size="sm" onClick={() => navigate("/jobs")}>
            View all <ArrowLeft className="w-3 h-3 ml-1 rotate-180" />
          </Button>
        </div>
        {jobsLoading ? (
          <div className="p-5">
            <SkeletonTable rows={5} />
          </div>
        ) : !jobs || jobs.length === 0 ? (
          <div className="py-8 text-center">
            <p className="text-xs text-muted-foreground">No recent jobs for this agent</p>
          </div>
        ) : (
          <table className="w-full">
            <thead>
              <tr className="border-b border-border bg-surface-0">
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Job ID</th>
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Topic</th>
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Status</th>
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Safety</th>
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Duration</th>
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Time</th>
              </tr>
            </thead>
            <tbody>
              {jobs.map((job) => (
                <tr
                  key={job.id}
                  onClick={() => navigate(`/jobs/${job.id}`)}
                  className="border-b border-border hover:bg-surface-1 transition-colors cursor-pointer"
                >
                  <td className="px-5 py-3 font-mono text-sm text-cordum">{job.id}</td>
                  <td className="px-5 py-3 text-sm text-foreground">{job.topic}</td>
                  <td className="px-5 py-3">
                    <StatusBadge variant={jobStatusVariant(job.status)}>
                      {job.status}
                    </StatusBadge>
                  </td>
                  <td className="px-5 py-3">
                    <SafetyBadge decision={job.safetyDecision?.type ?? "unknown"} />
                  </td>
                  <td className="px-5 py-3 text-sm text-muted-foreground font-mono">
                    {job.duration != null ? formatDuration(job.duration) : "—"}
                  </td>
                  <td className="px-5 py-3 text-sm text-muted-foreground">
                    {job.createdAt ? formatRelativeTime(job.createdAt) : "—"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
