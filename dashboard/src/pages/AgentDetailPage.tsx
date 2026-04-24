/*
 * DESIGN: "Control Surface" — Agent Detail
 * OPERATE / Agents / :id
 * Agent-specific view: metrics, safety breakdown, policy bindings, recent jobs
 */
import { useMemo, useState } from "react";
import { useParams, useNavigate } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { motion, AnimatePresence } from "framer-motion";
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
import { Tabs } from "@/components/ui/Tabs";
import {
  BarChart, Bar, ResponsiveContainer, XAxis, YAxis, Tooltip, CartesianGrid,
} from "recharts";
import { useWorker, useWorkerJobs } from "@/hooks/useWorkers";
import { usePolicyBundles } from "@/hooks/usePolicies";
import { ChartTooltipCompact as ChartTooltip } from "@/components/ui/ChartTooltip";
import type { Job } from "@/api/types";
import { AgentDelegationsPanel } from "@/components/delegations/AgentDelegationsPanel";
import { FEATURE_FLAGS } from "@/config/flags";

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

const container = {
  hidden: { opacity: 0 },
  visible: {
    opacity: 1,
    transition: {
      staggerChildren: 0.05,
    },
  },
};

const item = {
  hidden: { opacity: 0, y: 12 },
  visible: { opacity: 1, y: 0, transition: { duration: 0.3 } },
};

export default function AgentDetailPage() {
  const { id } = useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [activeTab, setActiveTab] = useState("overview");

  const { data: agent, isLoading: agentLoading, error: agentError } = useWorker(id);
  const { data: jobs, isLoading: jobsLoading, isError: jobsError, error: jobsErr, refetch: refetchJobs } = useWorkerJobs(id);
  const { data: bundlesData } = usePolicyBundles();
  const bundles = bundlesData?.items ?? [];

  const safetyBreakdown = useMemo(() => deriveSafetyBreakdown(jobs ?? []), [jobs]);
  const hourlyActivity = useMemo(() => deriveHourlyActivity(jobs ?? []), [jobs]);
  const totalDecisions = Object.values(safetyBreakdown).reduce((a, b) => a + b, 0);
  const allowRate = totalDecisions > 0 ? Math.round((safetyBreakdown.allow / totalDecisions) * 100) : 0;
  const tabs = [
    { id: "overview", label: "Overview" },
    { id: "activity", label: "Activity", count: jobs?.length ?? 0 },
    ...(FEATURE_FLAGS.delegationDashboard
      ? [{ id: "delegations", label: "Delegations" }]
      : []),
  ];

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
        <div className="grid grid-cols-1 lg:grid-cols-12 gap-6">
          <div className="lg:col-span-4"><SkeletonCard /></div>
          <div className="lg:col-span-4"><SkeletonCard /></div>
          <div className="lg:col-span-4"><SkeletonCard /></div>
          <div className="lg:col-span-12"><SkeletonCard /></div>
        </div>
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

      <Tabs
        tabs={tabs}
        activeTab={activeTab}
        onChange={setActiveTab}
        ariaLabel="Agent detail sections"
      />

      <AnimatePresence mode="wait">
        {activeTab === "overview" && (
          <motion.div
            key="overview"
            variants={container}
            initial="hidden"
            animate="visible"
            exit={{ opacity: 0, y: -8 }}
            className="grid grid-cols-1 gap-6 lg:grid-cols-12"
          >
            {/* Agent Info Card */}
            <motion.div variants={item} className="lg:col-span-4">
              <div className="instrument-card h-full">
                <div className="mb-4 flex items-center gap-3">
                  <div className={cn(
                    "flex h-10 w-10 items-center justify-center rounded-2xl",
                    isOnline ? "bg-[var(--color-success)]/10" : "bg-destructive/10",
                  )}>
                    <Cpu className={cn("h-5 w-5", isOnline ? "text-[var(--color-success)]" : "text-destructive")} />
                  </div>
                  <div>
                    <p className="font-mono text-sm font-medium text-foreground">{agent?.id}</p>
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

                  <div className="grid grid-cols-2 gap-3 border-t border-border pt-3 mt-3">
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

                  <div className="space-y-1 border-t border-border pt-3">
                    <p className="text-[10px] font-mono uppercase tracking-widest text-muted-foreground mb-1">Infrastructure</p>
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
            </motion.div>

            {/* Safety Breakdown */}
            <motion.div variants={item} className="lg:col-span-4">
              <div className="instrument-card h-full">
                <div className="mb-4 flex items-center justify-between">
                  <h2 className="font-display text-sm font-semibold text-foreground">Safety Decisions</h2>
                  <span className="font-mono text-xs text-muted-foreground">{totalDecisions.toLocaleString()} total</span>
                </div>
                <div className="mb-6 text-center">
                  <p className="text-3xl font-display font-bold text-foreground tracking-tight">{allowRate}%</p>
                  <p className="text-xs text-muted-foreground mt-1">aggregate allow rate</p>
                </div>
                {totalDecisions === 0 ? (
                  <p className="py-12 text-center text-xs text-muted-foreground italic">
                    {jobsLoading ? "Loading safety data..." : "No safety decision data available"}
                  </p>
                ) : (
                  <div className="space-y-4">
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
                          <div className="mb-1.5 flex justify-between text-xs">
                            <span className="capitalize text-muted-foreground">{key.replace(/_/g, " ")}</span>
                            <span className="font-mono text-foreground font-medium">{value.toLocaleString()} ({pct.toFixed(1)}%)</span>
                          </div>
                          <div className="h-1.5 w-full overflow-hidden rounded-full bg-surface-2">
                            <motion.div
                              initial={{ width: 0 }}
                              animate={{ width: `${pct}%` }}
                              transition={{ duration: 1, ease: "easeOut", delay: 0.2 }}
                              className={cn("h-full rounded-full", colors[key] ?? "bg-muted-foreground")}
                            />
                          </div>
                        </div>
                      );
                    })}
                  </div>
                )}
              </div>
            </motion.div>

            {/* Policy Bindings */}
            <motion.div variants={item} className="lg:col-span-4">
              <div className="instrument-card h-full">
                <h2 className="mb-4 font-display text-sm font-semibold text-foreground">Active Policy Bindings</h2>
                {bundles.length === 0 ? (
                  <div className="py-12 text-center border-2 border-dashed border-border/40 rounded-3xl">
                    <p className="text-xs text-muted-foreground">No policy bundles bound to this agent's pool</p>
                  </div>
                ) : (
                  <div className="space-y-2">
                    {bundles.map((b) => (
                      <div key={b.id} className="flex items-center justify-between rounded-2xl border border-border bg-surface-0/50 p-3 hover:bg-surface-0 transition-colors">
                        <div className="flex items-center gap-2">
                          <AlertTriangle className="h-3.5 w-3.5 text-cordum" />
                          <span className="text-sm font-medium text-foreground">{b.name || b.id}</span>
                        </div>
                        <div className="flex items-center gap-2">
                          <span className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider">{b.rule_count ?? b.rules?.length ?? 0} rules</span>
                          <StatusBadge variant={b.status === "published" ? "healthy" : "muted"}>{b.status ?? "published"}</StatusBadge>
                        </div>
                      </div>
                    ))}
                  </div>
                )}

                <div className="mt-6 border-t border-border pt-4">
                  <p className="mb-2 text-[10px] font-mono uppercase tracking-widest text-muted-foreground">Capabilities</p>
                  <div className="flex flex-wrap gap-1.5">
                    {agent?.capabilities && agent.capabilities.length > 0 ? (
                      agent.capabilities.map((cap) => (
                        <span key={cap} className="rounded-lg bg-surface-2 px-2 py-1 text-[10px] font-mono text-foreground border border-border/40">
                          {cap}
                        </span>
                      ))
                    ) : (
                      <span className="text-xs text-muted-foreground">None</span>
                    )}
                  </div>
                </div>
              </div>
            </motion.div>
          </motion.div>
        )}

        {activeTab === "activity" && (
          <motion.div
            key="activity"
            variants={container}
            initial="hidden"
            animate="visible"
            exit={{ opacity: 0, y: -8 }}
            className="grid grid-cols-1 gap-6 lg:grid-cols-12"
          >
            <motion.div variants={item} className="lg:col-span-12">
              <div className="instrument-card">
                <div className="mb-6 flex items-center justify-between">
                  <div>
                    <h2 className="font-display text-sm font-semibold text-foreground">Hourly Activity</h2>
                    <p className="mt-0.5 text-xs text-muted-foreground">Jobs processed per hour (last 24h)</p>
                  </div>
                </div>
                {jobsLoading ? (
                  <div className="flex h-[240px] items-center justify-center">
                    <SkeletonCard />
                  </div>
                ) : (
                  <div className="h-[240px] w-full">
                    <ResponsiveContainer width="100%" height="100%">
                      <BarChart data={hourlyActivity}>
                        <CartesianGrid strokeDasharray="3 3" stroke="rgba(255,255,255,0.04)" vertical={false} />
                        <XAxis
                          dataKey="hour"
                          tick={{ fontSize: 10, fill: "var(--text-muted)", fontFamily: "var(--font-mono)" }}
                          axisLine={false}
                          tickLine={false}
                          interval={3}
                        />
                        <YAxis
                          tick={{ fontSize: 10, fill: "var(--text-muted)", fontFamily: "var(--font-mono)" }}
                          axisLine={false}
                          tickLine={false}
                        />
                        <Tooltip content={<ChartTooltip />} cursor={{ fill: "var(--surface-2)", radius: 4 }} />
                        <Bar dataKey="jobs" fill="var(--color-cordum)" radius={[4, 4, 0, 0]} name="Jobs" />
                        <Bar dataKey="denied" fill="var(--color-governance)" radius={[4, 4, 0, 0]} name="Denied" />
                      </BarChart>
                    </ResponsiveContainer>
                  </div>
                )}
              </div>
            </motion.div>

            <motion.div variants={item} className="lg:col-span-12">
              <div className="instrument-card overflow-hidden !p-0">
                <div className="flex items-center justify-between border-b border-border px-6 py-4 bg-surface-0/30">
                  <h2 className="font-display text-sm font-semibold text-foreground">Recent Jobs</h2>
                  <Button variant="ghost" size="sm" onClick={() => navigate("/jobs")}>
                    View all <ArrowLeft className="ml-1 h-3 w-3 rotate-180" />
                  </Button>
                </div>
                {jobsLoading ? (
                  <div className="p-6">
                    <SkeletonTable rows={5} />
                  </div>
                ) : !jobs || jobs.length === 0 ? (
                  <div className="py-12 text-center">
                    <p className="text-sm text-muted-foreground">No recent jobs for this agent</p>
                  </div>
                ) : (
                  <div className="overflow-x-auto">
                    <table className="w-full">
                      <thead>
                        <tr className="border-b border-border bg-surface-0/50">
                          <th className="px-6 py-3 text-left text-[10px] font-mono font-semibold uppercase tracking-widest text-muted-foreground">Job ID</th>
                          <th className="px-6 py-3 text-left text-[10px] font-mono font-semibold uppercase tracking-widest text-muted-foreground">Topic</th>
                          <th className="px-6 py-3 text-left text-[10px] font-mono font-semibold uppercase tracking-widest text-muted-foreground">Status</th>
                          <th className="px-6 py-3 text-left text-[10px] font-mono font-semibold uppercase tracking-widest text-muted-foreground">Safety</th>
                          <th className="px-6 py-3 text-left text-[10px] font-mono font-semibold uppercase tracking-widest text-muted-foreground">Duration</th>
                          <th className="px-6 py-3 text-left text-[10px] font-mono font-semibold uppercase tracking-widest text-muted-foreground">Time</th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-border">
                        {jobs.slice(0, 10).map((job) => (
                          <tr
                            key={job.id}
                            onClick={() => navigate(`/jobs/${job.id}`)}
                            className="group cursor-pointer transition-colors hover:bg-surface-1"
                          >
                            <td className="px-6 py-4 font-mono text-xs text-cordum group-hover:underline">
                              {job.id.slice(0, 8)}
                            </td>
                            <td className="px-6 py-4 text-xs font-medium text-foreground">{job.topic}</td>
                            <td className="px-6 py-4">
                              <StatusBadge variant={jobStatusVariant(job.status)}>
                                {job.status}
                              </StatusBadge>
                            </td>
                            <td className="px-6 py-4">
                              <SafetyBadge decision={job.safetyDecision?.type ?? "unknown"} />
                            </td>
                            <td className="px-6 py-4 font-mono text-xs text-muted-foreground">
                              {job.duration != null ? formatDuration(job.duration) : "—"}
                            </td>
                            <td className="px-6 py-4 text-xs text-muted-foreground">
                              {job.createdAt ? formatRelativeTime(job.createdAt) : "—"}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
              </div>
            </motion.div>
          </motion.div>
        )}
      </AnimatePresence>

      {FEATURE_FLAGS.delegationDashboard && activeTab === "delegations" && id && (
        <AgentDelegationsPanel agentId={id} />
      )}
    </div>
  );
}
