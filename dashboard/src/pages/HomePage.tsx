/*
 * DESIGN: "Control Surface" — Dashboard Overview
 * Revision v2: Balanced KPIs (2 ops + 2 governance)
 * "Orchestration sells. Governance seals. Both are Cordum."
 */
import { useState, useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { motion } from "framer-motion";
import { get } from "@/api/client";
import { mapJobRecord, mapHeartbeatToWorker, mapApprovalItem, type BackendJobRecord, type BackendHeartbeat, type BackendApprovalItem } from "@/api/transform";
import type { Job, Worker, Approval } from "@/api/types";
import { PageHeader } from "@/components/layout/PageHeader";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { Button } from "@/components/ui/Button";
import { SkeletonCard } from "@/components/ui/Skeleton";
import {
  AreaChart, Area, PieChart, Pie, Cell,
  ResponsiveContainer, XAxis, YAxis, Tooltip, CartesianGrid,
} from "recharts";
import {
  Activity, Cpu, UserCheck, ArrowRight,
  CheckCircle2, XCircle, Zap, ShieldCheck,
} from "lucide-react";
import { cn, formatRelativeTime } from "@/lib/utils";
import { useApproveJob, useRejectJob } from "@/hooks/useApprovals";
import { useStatus } from "@/hooks/useStatus";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { ChartTooltip } from "@/components/ui/ChartTooltip";
import { MetricValue } from "@/components/ui/MetricValue";
import { InstrumentCard } from "@/components/ui/InstrumentCard";
import { SafetyDecisionBadge } from "@/components/ui/SafetyDecisionBadge";

export default function HomePage() {
  const navigate = useNavigate();
  const [denyTarget, setDenyTarget] = useState<string | null>(null);
  const approveMut = useApproveJob();
  const rejectMut = useRejectJob();

  const { data: jobsData, isLoading: jobsLoading } = useQuery({
    queryKey: ["jobs", "home"],
    queryFn: async () => {
      const res = await get<{ items: BackendJobRecord[]; total?: number }>("/jobs?limit=200");
      const items = (res.items ?? []).map(mapJobRecord).filter((j): j is Job => !!j);
      return { items, total: res.total ?? items.length };
    },
    refetchInterval: 10_000,
  });

  const { data: workers, isLoading: workersLoading } = useQuery({
    queryKey: ["workers", "home"],
    queryFn: async () => {
      const res = await get<{ items: BackendHeartbeat[] }>("/workers");
      return (res.items ?? []).map(mapHeartbeatToWorker).filter((w): w is Worker => !!w);
    },
    refetchInterval: 15_000,
  });

  const { data: approvalsData, isLoading: approvalsLoading } = useQuery({
    queryKey: ["approvals", "home"],
    queryFn: async () => {
      const res = await get<{ items: BackendApprovalItem[] }>("/approvals?limit=100");
      return (res.items ?? []).map(mapApprovalItem).filter((a): a is Approval => !!a);
    },
    refetchInterval: 5_000,
  });

  const { data: statusData, isLoading: statusLoading } = useStatus();

  const derivedServices = useMemo(() => {
    if (!statusData) return [];
    const svc: { name: string; status: string; latency: string }[] = [];
    // API Gateway — if we got a response, it's healthy
    const uptimeLabel = statusData.uptime_seconds != null
      ? `up ${Math.floor(statusData.uptime_seconds / 3600)}h ${Math.floor((statusData.uptime_seconds % 3600) / 60)}m`
      : "—";
    svc.push({ name: "API Gateway", status: "healthy", latency: uptimeLabel });
    // NATS
    if (statusData.nats) {
      svc.push({
        name: "NATS",
        status: statusData.nats.connected ? "healthy" : "down",
        latency: statusData.nats.status ?? "—",
      });
    }
    // Redis
    if (statusData.redis) {
      svc.push({
        name: "Redis",
        status: statusData.redis.ok ? "healthy" : "down",
        latency: statusData.redis.error ?? (statusData.redis.ok ? "ok" : "—"),
      });
    }
    // Workers
    if (statusData.workers) {
      const count = statusData.workers.count ?? 0;
      svc.push({
        name: "Workers",
        status: count > 0 ? "healthy" : "degraded",
        latency: `${count} connected`,
      });
    }
    // Safety Kernel — derive from circuit breaker if available
    if (statusData.circuit_breakers) {
      const inputState = statusData.circuit_breakers.input?.state ?? "unknown";
      svc.push({
        name: "Safety Kernel",
        status: inputState === "CLOSED" ? "healthy" : inputState === "OPEN" ? "down" : "degraded",
        latency: inputState.toLowerCase(),
      });
    }
    return svc;
  }, [statusData]);

  const jobs = jobsData?.items ?? [];
  const activeWorkers = workers?.filter((w) => w.status === "idle" || w.status === "busy") ?? [];
  const pendingApprovals = approvalsData?.filter((a) => a.status === "pending") ?? [];

  const runningJobs = jobs.filter((j) => j.status === "running").length;
  const failedJobs = jobs.filter((j) => j.status === "failed").length;
  const completedJobs = jobs.filter((j) => j.status === "succeeded").length;
  const totalJobs = jobs.length;

  const { safetyAllowed, safetyDenied, safetyApproval, safetyConstrained, safetyThrottled, safetyAllowRate } = useMemo(() => {
    const allowed = jobs.filter(j => j.safetyDecision?.type === "allow").length;
    const denied = jobs.filter(j => j.safetyDecision?.type === "deny").length;
    const approval = jobs.filter(j => j.safetyDecision?.type === "require_approval").length;
    const constrained = jobs.filter(j => j.safetyDecision?.type === "allow_with_constraints").length;
    const throttled = jobs.filter(j => j.safetyDecision?.type === "throttle").length;
    const total = allowed + denied + approval + constrained + throttled;
    return {
      safetyAllowed: allowed,
      safetyDenied: denied,
      safetyApproval: approval,
      safetyConstrained: constrained,
      safetyThrottled: throttled,
      safetyTotal: total,
      safetyAllowRate: total > 0 ? Math.round((allowed / total) * 100) : 0,
    };
  }, [jobs]);

  const activityData = useMemo(() => {
    const buckets = new Map<string, { allowed: number; denied: number; approval: number }>();
    for (let i = 0; i < 12; i++) {
      const label = String(i * 2).padStart(2, "0") + ":00";
      buckets.set(label, { allowed: 0, denied: 0, approval: 0 });
    }
    for (const j of jobs) {
      const hour = new Date(j.createdAt).getHours();
      const bucket = String(Math.floor(hour / 2) * 2).padStart(2, "0") + ":00";
      const b = buckets.get(bucket);
      if (b) {
        if (j.safetyDecision?.type === "deny") b.denied++;
        else if (j.safetyDecision?.type === "require_approval") b.approval++;
        else b.allowed++;
      }
    }
    return Array.from(buckets, ([time, v]) => ({ time, ...v }));
  }, [jobs]);

  // Decision Distribution donut — 5 safety decisions
  const decisionData = [
    { name: "Allow", value: safetyAllowed, color: "#1f7a57" },
    { name: "Deny", value: safetyDenied, color: "#b83a3a" },
    { name: "Require Approval", value: safetyApproval, color: "#c58a1c" },
    { name: "Constrained", value: safetyConstrained, color: "#0f7f7a" },
    { name: "Throttle", value: safetyThrottled, color: "#d4833a" },
  ];

  const isLoading = jobsLoading || workersLoading || approvalsLoading;

  return (
    <div className="space-y-6">
      <PageHeader
        label="Control Plane"
        title="Dashboard"
        subtitle="Real-time overview of your agent orchestration and governance"
        actions={
          <Button variant="primary" size="sm" onClick={() => navigate("/jobs")}>
            <Zap className="w-3.5 h-3.5" />
            Submit Job
          </Button>
        }
      />

      <motion.div
        initial={{ opacity: 0, y: 12 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.3 }}
        className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4"
      >
        {isLoading ? (
          Array.from({ length: 4 }).map((_, i) => <SkeletonCard key={i} />)
        ) : (
          <>
            {/* KPI 1: Recent Jobs */}
            <InstrumentCard>
              <MetricValue label="Recent Jobs" value={totalJobs.toLocaleString()} icon={<Activity className="w-4 h-4" />}>
                <div className="flex gap-3 mt-3 text-[10px] font-mono text-muted-foreground">
                  <span>{runningJobs} running</span>
                  <span className="text-[var(--color-success)]">{completedJobs} done</span>
                  <span className="text-destructive">{failedJobs} failed</span>
                </div>
              </MetricValue>
            </InstrumentCard>

            {/* KPI 2: Active Agents */}
            <InstrumentCard>
              <MetricValue 
                label="Active Agents" 
                value={activeWorkers.length} 
                unit={`/ ${workers?.length ?? 0}`}
                icon={<Cpu className="w-4 h-4" />}
              >
                <div className="flex gap-1 mt-3.5">
                  {(workers ?? []).slice(0, 20).map((w) => (
                    <div
                      key={w.id}
                      className={cn(
                        "w-2 h-2 rounded-sm",
                        w.status === "idle" ? "bg-[var(--color-success)]" :
                        w.status === "busy" ? "bg-cordum" :
                        "bg-muted-foreground",
                      )}
                    />
                  ))}
                </div>
              </MetricValue>
            </InstrumentCard>

            {/* KPI 3: Safety Decisions */}
            <InstrumentCard>
              <MetricValue 
                label="Safety Decisions" 
                value={`${safetyAllowRate}%`} 
                unit="allowed"
                icon={<ShieldCheck className="w-4 h-4" />}
              >
                <div className="flex gap-3 mt-3 text-[10px] font-mono">
                  <span className="text-[var(--color-success)]">{safetyAllowed} allow</span>
                  <span className="text-destructive">{safetyDenied} deny</span>
                  <span className="text-[var(--color-warning)]">{safetyApproval} review</span>
                </div>
              </MetricValue>
            </InstrumentCard>

            {/* KPI 4: Pending Approvals */}
            <InstrumentCard accent={pendingApprovals.length > 0 ? "warning" : "cordum"}>
              <MetricValue 
                label="Pending Approvals" 
                value={pendingApprovals.length} 
                unit="awaiting"
                icon={<UserCheck className={cn("w-4 h-4", pendingApprovals.length > 0 ? "text-[var(--color-warning)]" : "text-cordum")} />}
              >
                {pendingApprovals.length > 0 && (
                  <Button variant="ghost" size="sm" className="mt-2.5 text-[var(--color-warning)] hover:text-[var(--color-warning)] p-0 h-auto font-mono text-[10px] uppercase tracking-widest" onClick={() => navigate("/approvals")}>
                    Review now <ArrowRight className="w-3 h-3 ml-1" />
                  </Button>
                )}
              </MetricValue>
            </InstrumentCard>
          </>
        )}
      </motion.div>

      {/* Charts Row — Job Activity with Safety Overlay + Decision Distribution */}
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
        {/* Job Activity with Safety Overlay — 2 cols */}
        <motion.div
          initial={{ opacity: 0, y: 12 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.3, delay: 0.1 }}
          className="instrument-card lg:col-span-2"
        >
          <div className="flex items-start justify-between mb-5">
            <div className="min-w-0">
              <h3 className="font-display font-semibold text-sm text-foreground tracking-tight">Job Activity</h3>
              <p className="text-[11px] text-muted-foreground mt-1 leading-none">Safety overlay — allowed vs denied vs approval</p>
            </div>
            <div className="flex items-center gap-4 text-[10px] font-mono shrink-0">
              <span className="flex items-center gap-1.5"><span className="w-2 h-2 rounded-full bg-[var(--color-success)]" />Allowed</span>
              <span className="flex items-center gap-1.5"><span className="w-2 h-2 rounded-full bg-destructive" />Denied</span>
              <span className="flex items-center gap-1.5"><span className="w-2 h-2 rounded-full bg-[var(--color-warning)]" />Approval</span>
            </div>
          </div>
          <ResponsiveContainer width="100%" height={260}>
            <AreaChart data={activityData}>
              <defs>
                <linearGradient id="gradAllowed" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="#1f7a57" stopOpacity={0.25} />
                  <stop offset="95%" stopColor="#1f7a57" stopOpacity={0} />
                </linearGradient>
                <linearGradient id="gradDenied" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="#b83a3a" stopOpacity={0.25} />
                  <stop offset="95%" stopColor="#b83a3a" stopOpacity={0} />
                </linearGradient>
                <linearGradient id="gradApproval" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="#c58a1c" stopOpacity={0.25} />
                  <stop offset="95%" stopColor="#c58a1c" stopOpacity={0} />
                </linearGradient>
              </defs>
              <CartesianGrid strokeDasharray="3 3" stroke="rgba(255,255,255,0.04)" />
              <XAxis dataKey="time" tick={{ fontSize: 10, fill: "#5a6a70" }} axisLine={false} tickLine={false} />
              <YAxis tick={{ fontSize: 10, fill: "#5a6a70" }} axisLine={false} tickLine={false} />
              <Tooltip content={<ChartTooltip />} />
              <Area type="monotone" dataKey="allowed" stackId="1" stroke="#1f7a57" fill="url(#gradAllowed)" strokeWidth={2} name="Allowed" />
              <Area type="monotone" dataKey="denied" stackId="1" stroke="#b83a3a" fill="url(#gradDenied)" strokeWidth={2} name="Denied" />
              <Area type="monotone" dataKey="approval" stackId="1" stroke="#c58a1c" fill="url(#gradApproval)" strokeWidth={2} name="Approval" />
            </AreaChart>
          </ResponsiveContainer>
        </motion.div>

        {/* Decision Distribution Donut — 1 col */}
        <motion.div
          initial={{ opacity: 0, y: 12 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.3, delay: 0.15 }}
          className="instrument-card"
        >
          <h3 className="font-display font-semibold text-sm text-foreground mb-0.5">Decision Distribution</h3>
          <p className="text-xs text-muted-foreground mb-4">5 safety decision types</p>
          <ResponsiveContainer width="100%" height={180}>
            <PieChart>
              <Pie
                data={decisionData}
                cx="50%"
                cy="50%"
                innerRadius={48}
                outerRadius={72}
                paddingAngle={3}
                dataKey="value"
              >
                {decisionData.map((entry) => (
                  <Cell key={entry.name} fill={entry.color} />
                ))}
              </Pie>
              <Tooltip content={<ChartTooltip />} />
            </PieChart>
          </ResponsiveContainer>
          <div className="space-y-1.5 mt-2">
            {decisionData.map((d) => (
              <div key={d.name} className="flex items-center justify-between text-xs">
                <span className="flex items-center gap-2">
                  <span className="w-2 h-2 rounded-full" style={{ backgroundColor: d.color }} />
                  <span className="text-muted-foreground">{d.name}</span>
                </span>
                <span className="font-mono text-foreground">{d.value}</span>
              </div>
            ))}
          </div>
        </motion.div>
      </div>

      {/* Recent Activity Table — with Safety Decision column */}
      <motion.div
        initial={{ opacity: 0, y: 12 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.3, delay: 0.2 }}
        className="instrument-card overflow-hidden"
      >
        <div className="flex items-center justify-between px-5 py-3 border-b border-border">
          <h3 className="font-display font-semibold text-sm text-foreground">Recent Activity</h3>
          <Button variant="ghost" size="sm" onClick={() => navigate("/jobs")}>
            View all <ArrowRight className="w-3 h-3 ml-1" />
          </Button>
        </div>
        <table className="w-full">
          <thead>
            <tr className="border-b border-border bg-surface-0">
              <th className="text-left px-5 py-2.5 text-[10px] font-mono font-medium text-muted-foreground uppercase tracking-widest">Job ID</th>
              <th className="text-left px-5 py-2.5 text-[10px] font-mono font-medium text-muted-foreground uppercase tracking-widest">Topic</th>
              <th className="text-left px-5 py-2.5 text-[10px] font-mono font-medium text-muted-foreground uppercase tracking-widest">Status</th>
              <th className="text-left px-5 py-2.5 text-[10px] font-mono font-medium text-muted-foreground uppercase tracking-widest">Safety</th>
              <th className="text-left px-5 py-2.5 text-[10px] font-mono font-medium text-muted-foreground uppercase tracking-widest">Duration</th>
              <th className="text-left px-5 py-2.5 text-[10px] font-mono font-medium text-muted-foreground uppercase tracking-widest">Time</th>
            </tr>
          </thead>
          <tbody>
            {jobs.slice(0, 8).map((job) => {
              const safetyDecision = job.safetyDecision?.type;
              return (
                <tr
                  key={job.id}
                  onClick={() => navigate(`/jobs/${job.id}`)}
                  className="border-b border-border hover:bg-surface-1 transition-colors cursor-pointer group"
                >
                  <td className="px-5 py-2.5 font-mono text-sm text-cordum group-hover:underline">{job.id.slice(0, 12)}</td>
                  <td className="px-5 py-2.5 text-sm text-foreground">{job.topic || "—"}</td>
                  <td className="px-5 py-2.5">
                    <StatusBadge
                      variant={
                        job.status === "running" ? "healthy" :
                        job.status === "failed" ? "danger" :
                        job.status === "succeeded" ? "healthy" :
                        job.status === "pending" || job.status === "scheduled" ? "warning" :
                        "muted"
                      }
                    >
                      {job.status}
                    </StatusBadge>
                  </td>
                  <td className="px-5 py-2.5">
                    <SafetyDecisionBadge decision={safetyDecision} />
                  </td>
                  <td className="px-5 py-2.5 text-sm text-muted-foreground font-mono">
                    {job.duration
                      ? `${Math.round(job.duration / 1000)}s`
                      : job.status === "running" ? "running..." : "—"}
                  </td>
                  <td className="px-5 py-2.5 text-sm text-muted-foreground">
                    {job.updatedAt ? formatRelativeTime(new Date(job.updatedAt).toISOString()) : "—"}
                  </td>
                </tr>
              );
            })}
            {jobs.length === 0 && !jobsLoading && (
              <tr>
                <td colSpan={6} className="text-center text-sm text-muted-foreground py-12">
                  No jobs yet — submit your first job to get started
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </motion.div>

      {/* Worker Pool Health */}
      <motion.div
        initial={{ opacity: 0, y: 12 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.3, delay: 0.25 }}
        className="instrument-card"
      >
        <div className="flex items-center justify-between mb-5">
          <div>
            <h3 className="font-display font-semibold text-sm text-foreground">Worker Pool Health</h3>
            <p className="text-xs text-muted-foreground mt-0.5">Real-time agent status</p>
          </div>
          <Button variant="ghost" size="sm" onClick={() => navigate("/agents")}>
            View fleet <ArrowRight className="w-3 h-3 ml-1" />
          </Button>
        </div>
        <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-6 gap-3">
          {(workers ?? []).slice(0, 12).map((w, idx) => {
            const isOnline = w.status === "idle" || w.status === "busy";
            return (
              <InstrumentCard
                key={w.id}
                onClick={() => navigate(`/agents/${w.id}`)}
                hoverable
                accent={isOnline ? "healthy" : "muted"}
                className="p-3" // dense padding for high density grid
              >
                <div className="flex items-center gap-2 mb-2">
                  <div className={cn("w-2 h-2 rounded-full", isOnline ? "bg-[var(--color-success)] animate-pulse" : "bg-muted-foreground")} />
                  <span className="font-mono text-[11px] text-foreground truncate">{w.name || w.id.slice(0, 10)}</span>
                </div>
                <div className="space-y-1.5">
                  <div className="flex justify-between text-[9px] uppercase tracking-wider font-mono">
                    <span className="text-muted-foreground">CPU</span>
                    <span className="text-foreground">{w.cpuLoad ?? 0}%</span>
                  </div>
                  <div className="w-full h-1 rounded-full bg-surface-2 overflow-hidden">
                    <div className="h-full rounded-full bg-cordum transition-all" style={{ width: `${w.cpuLoad ?? 0}%` }} />
                  </div>
                  <div className="flex justify-between text-[9px] uppercase tracking-wider font-mono">
                    <span className="text-muted-foreground">MEM</span>
                    <span className="text-foreground">{w.memoryLoad ?? 0}%</span>
                  </div>
                  <div className="w-full h-1 rounded-full bg-surface-2 overflow-hidden">
                    <div className="h-full rounded-full bg-[var(--color-info)] transition-all" style={{ width: `${w.memoryLoad ?? 0}%` }} />
                  </div>
                </div>
                {/* Last policy eval line */}
                <div className="mt-2 pt-1.5 border-t border-border/40 text-[9px] font-mono text-muted-foreground">
                  Jobs: {w.activeJobs ?? 0} / {w.capacity ?? 0}
                </div>
              </InstrumentCard>
            );
          })}
          {(!workers || workers.length === 0) && !workersLoading && (
            <div className="col-span-full text-center py-8 text-sm text-muted-foreground">
              No workers registered yet
            </div>
          )}
        </div>
      </motion.div>

      {/* System Health — with Safety Kernel row */}
      <motion.div
        initial={{ opacity: 0, y: 12 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.3, delay: 0.3 }}
        className="instrument-card"
      >
        <h3 className="font-display font-semibold text-sm text-foreground mb-5">Service Health</h3>
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-5 gap-3">
          {statusLoading ? (
            Array.from({ length: 5 }).map((_, i) => (
              <div key={i} className="flex items-center gap-3 rounded-2xl border border-border bg-surface-0 p-3 animate-pulse">
                <div className="w-2 h-2 rounded-full shrink-0 bg-surface-2" />
                <div className="flex-1 min-w-0 space-y-1">
                  <div className="h-3 bg-surface-2 rounded w-20" />
                  <div className="h-2.5 bg-surface-2 rounded w-10" />
                </div>
              </div>
            ))
          ) : derivedServices.length > 0 ? (
            derivedServices.map((svc) => (
              <InstrumentCard
                key={svc.name}
                accent={svc.status === "healthy" ? "healthy" : svc.status === "degraded" ? "warning" : "danger"}
                className="p-3"
              >
                <div className="flex items-center gap-3">
                  <div className={cn(
                    "w-2 h-2 rounded-full shrink-0",
                    svc.status === "healthy" ? "bg-[var(--color-success)]" : svc.status === "degraded" ? "bg-[var(--color-warning)]" : "bg-destructive"
                  )} />
                  <div className="flex-1 min-w-0">
                    <p className="text-xs text-foreground font-semibold truncate">{svc.name}</p>
                    <p className="text-[10px] text-muted-foreground font-mono">{svc.latency || "—"}</p>
                  </div>
                </div>
              </InstrumentCard>
            ))
          ) : (
            <div className="col-span-full text-center py-4 text-sm text-muted-foreground">
              Health data unavailable
            </div>
          )}
        </div>
      </motion.div>

      {/* Approval Queue */}
      {pendingApprovals.length > 0 && (
        <motion.div
          initial={{ opacity: 0, y: 12 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.3, delay: 0.35 }}
          className="space-y-3"
        >
          <div className="flex items-center justify-between">
            <h3 className="font-display font-semibold text-sm text-foreground">Approval Queue</h3>
            <Button variant="ghost" size="sm" onClick={() => navigate("/approvals")}>
              View all <ArrowRight className="w-3 h-3 ml-1" />
            </Button>
          </div>
          {pendingApprovals.slice(0, 3).map((approval) => (
            <InstrumentCard
              key={approval.id}
              accent="warning"
              onClick={() => navigate("/approvals")}
              hoverable
            >
              <div className="flex items-start justify-between gap-4">
                <div className="min-w-0">
                  <div className="flex items-center gap-3 mb-2">
                    <span className="font-mono text-sm text-cordum">{approval.id.slice(0, 12)}</span>
                    <span className="text-[10px] text-muted-foreground font-mono">
                      {approval.requestedAt ? formatRelativeTime(approval.requestedAt) : "—"}
                    </span>
                  </div>
                  <h4 className="text-sm font-semibold font-display text-foreground leading-snug">
                    {approval.topic || "Pending Approval"}
                  </h4>
                </div>
                <div className="flex gap-2 shrink-0">
                  <Button size="sm" variant="danger" onClick={(e) => { e.stopPropagation(); setDenyTarget(approval.id); }}>
                    <XCircle className="w-3.5 h-3.5 mr-1" />
                    Deny
                  </Button>
                  <Button
                    size="sm"
                    variant="primary"
                    loading={approveMut.isPending}
                    onClick={(e) => { e.stopPropagation(); approveMut.mutate({ id: approval.id }); }}
                  >
                    <CheckCircle2 className="w-3.5 h-3.5 mr-1" />
                    Approve
                  </Button>
                </div>
              </div>
            </InstrumentCard>
          ))}
        </motion.div>
      )}

      <ConfirmDialog
        open={!!denyTarget}
        onClose={() => setDenyTarget(null)}
        onConfirm={() => {
          if (denyTarget) {
            rejectMut.mutate({ id: denyTarget, reason: "Denied from dashboard" });
          }
          setDenyTarget(null);
        }}
        title="Deny Approval"
        description="Are you sure you want to deny this approval request? This action cannot be undone."
        confirmLabel="Deny"
        variant="destructive"
        loading={rejectMut.isPending}
      />
    </div>
  );
}
