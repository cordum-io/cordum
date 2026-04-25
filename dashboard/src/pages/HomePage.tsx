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
import {
  mapJobRecord,
  mapHeartbeatToWorker,
  mapApprovalItem,
  type BackendJobRecord,
  type BackendHeartbeat,
  type BackendApprovalItem,
} from "@/api/transform";
import type { Job, Worker, Approval } from "@/api/types";
import { PageHeader } from "@/components/layout/PageHeader";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { Button } from "@/components/ui/Button";
import { SkeletonCard } from "@/components/ui/Skeleton";
import {
  AreaChart,
  Area,
  PieChart,
  Pie,
  Cell,
  ResponsiveContainer,
  XAxis,
  YAxis,
  Tooltip,
  CartesianGrid,
} from "recharts";
import {
  Activity,
  Cpu,
  UserCheck,
  ArrowRight,
  Zap,
  ShieldCheck,
  Radio,
} from "lucide-react";
import { cn, formatRelativeTime } from "@/lib/utils";
import { useStatus } from "@/hooks/useStatus";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { ChartTooltip } from "@/components/ui/ChartTooltip";
import { CollapsibleSection } from "@/components/ui/CollapsibleSection";
import { OnboardingChecklist } from "@/components/home/OnboardingChecklist";
import { MetricValue } from "@/components/ui/MetricValue";
import { InstrumentCard } from "@/components/ui/InstrumentCard";
import { SafetyDecisionBadge } from "@/components/ui/SafetyDecisionBadge";
import { safeLocalStorage } from "@/lib/storage";
import { AuditChainCard } from "@/components/AuditChainCard";
import { useAuth } from "@/hooks/useAuth";
import { SafetyDecisionFeed } from "@/components/home/SafetyDecisionFeed";

export default function HomePage() {
  const navigate = useNavigate();
  const { tenantId } = useAuth();
  const [showOnboarding, setShowOnboarding] = useState(
    () => !safeLocalStorage.getItem("onboarding-dismissed"),
  );
  const [liveMode, setLiveMode] = useState(false);

  const {
    data: jobsData,
    isLoading: jobsLoading,
    isError: jobsError,
    error: jobsErr,
    refetch: refetchJobs,
  } = useQuery({
    queryKey: ["jobs", "home"],
    queryFn: async () => {
      const res = await get<{ items: BackendJobRecord[]; total?: number }>(
        "/jobs?limit=200",
      );
      const items = (res.items ?? [])
        .map(mapJobRecord)
        .filter((j): j is Job => !!j);
      return { items, total: res.total ?? items.length };
    },
    refetchInterval: 10_000,
  });

  const {
    data: workers,
    isLoading: workersLoading,
    isError: workersError,
    error: workersErr,
    refetch: refetchWorkers,
  } = useQuery({
    queryKey: ["workers", "home"],
    queryFn: async () => {
      const res = await get<{ items: BackendHeartbeat[] }>("/workers");
      return (res.items ?? [])
        .map(mapHeartbeatToWorker)
        .filter((w): w is Worker => !!w);
    },
    refetchInterval: 15_000,
  });

  const {
    data: approvalsData,
    isLoading: approvalsLoading,
    isError: approvalsError,
    error: approvalsErr,
    refetch: refetchApprovals,
  } = useQuery({
    queryKey: ["approvals", "home"],
    queryFn: async () => {
      const res = await get<{ items: BackendApprovalItem[] }>(
        "/approvals?limit=100",
      );
      return (res.items ?? [])
        .map(mapApprovalItem)
        .filter((a): a is Approval => !!a);
    },
    refetchInterval: 5_000,
  });

  const { data: statusData, isLoading: statusLoading } = useStatus();

  const derivedServices = useMemo(() => {
    if (!statusData) return [];
    const svc: { name: string; status: string; latency: string }[] = [];
    // API Gateway — if we got a response, it's healthy
    const uptimeLabel =
      statusData.uptime_seconds != null
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
        status:
          inputState === "CLOSED"
            ? "healthy"
            : inputState === "OPEN"
              ? "down"
              : "degraded",
        latency: inputState.toLowerCase(),
      });
    }
    return svc;
  }, [statusData]);

  const jobs = jobsData?.items ?? [];
  const activeWorkers =
    workers?.filter((w) => w.status === "idle" || w.status === "busy") ?? [];
  const pendingApprovals =
    approvalsData?.filter((a) => a.status === "pending") ?? [];

  const runningJobs = jobs.filter((j) => j.status === "running").length;
  const failedJobs = jobs.filter((j) => j.status === "failed").length;
  const completedJobs = jobs.filter((j) => j.status === "succeeded").length;
  const totalJobs = jobs.length;

  const {
    safetyAllowed,
    safetyDenied,
    safetyApproval,
    safetyConstrained,
    safetyThrottled,
    safetyAllowRate,
  } = useMemo(() => {
    const allowed = jobs.filter(
      (j) => j.safetyDecision?.type === "allow",
    ).length;
    const denied = jobs.filter((j) => j.safetyDecision?.type === "deny").length;
    const approval = jobs.filter(
      (j) => j.safetyDecision?.type === "require_approval",
    ).length;
    const constrained = jobs.filter(
      (j) => j.safetyDecision?.type === "allow_with_constraints",
    ).length;
    const throttled = jobs.filter(
      (j) => j.safetyDecision?.type === "throttle",
    ).length;
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
    type Bucket = {
      allowed: number;
      denied: number;
      approval: number;
      failed: number;
    };
    const buckets = new Map<string, Bucket>();
    const classify = (j: Job, b: Bucket) => {
      if (j.status === "failed") b.failed++;
      else if (j.safetyDecision?.type === "deny") b.denied++;
      else if (j.safetyDecision?.type === "require_approval") b.approval++;
      else b.allowed++;
    };

    if (liveMode) {
      // Live: last 30 min, 2-minute buckets
      const now = Date.now();
      const windowMs = 30 * 60 * 1000;
      for (let i = 0; i < 15; i++) {
        const t = new Date(now - windowMs + i * 2 * 60 * 1000);
        const label =
          String(t.getHours()).padStart(2, "0") +
          ":" +
          String(t.getMinutes()).padStart(2, "0");
        buckets.set(label, { allowed: 0, denied: 0, approval: 0, failed: 0 });
      }
      for (const j of jobs) {
        const ts = new Date(j.createdAt).getTime();
        if (ts < now - windowMs) continue;
        const offset = ts - (now - windowMs);
        const idx = Math.min(14, Math.floor(offset / (2 * 60 * 1000)));
        const keys = Array.from(buckets.keys());
        const b = buckets.get(keys[idx]);
        if (b) classify(j, b);
      }
    } else {
      // Default: 24h, 2-hour buckets
      for (let i = 0; i < 12; i++) {
        const label = String(i * 2).padStart(2, "0") + ":00";
        buckets.set(label, { allowed: 0, denied: 0, approval: 0, failed: 0 });
      }
      for (const j of jobs) {
        const hour = new Date(j.createdAt).getHours();
        const bucket =
          String(Math.floor(hour / 2) * 2).padStart(2, "0") + ":00";
        const b = buckets.get(bucket);
        if (b) classify(j, b);
      }
    }
    return Array.from(buckets, ([time, v]) => ({ time, ...v }));
  }, [jobs, liveMode]);

  // Decision Distribution donut — 5 safety decisions
  const decisionData = [
    { name: "Allow", value: safetyAllowed, color: "#1f7a57" },
    { name: "Deny", value: safetyDenied, color: "#7c3aed" },
    { name: "Require Approval", value: safetyApproval, color: "#c58a1c" },
    { name: "Constrained", value: safetyConstrained, color: "#0f7f7a" },
    { name: "Throttle", value: safetyThrottled, color: "#d4833a" },
  ];

  const isLoading = jobsLoading || workersLoading || approvalsLoading;

  const hasError = jobsError || workersError || approvalsError;
  if (hasError) {
    const errorMessage =
      jobsErr?.message ||
      workersErr?.message ||
      approvalsErr?.message ||
      "Failed to load dashboard data";
    return (
      <ErrorBanner
        message={errorMessage}
        onRetry={() => {
          void refetchJobs();
          void refetchWorkers();
          void refetchApprovals();
        }}
      />
    );
  }

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
        initial="hidden"
        animate="visible"
        variants={{
          visible: { transition: { staggerChildren: 0.05 } },
        }}
        className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4"
      >
        {isLoading ? (
          Array.from({ length: 4 }).map((_, i) => <SkeletonCard key={i} />)
        ) : (
          <>
            {/* KPI 1: Recent Jobs */}
            <motion.div variants={{ hidden: { opacity: 0, y: 10 }, visible: { opacity: 1, y: 0 } }}>
              <InstrumentCard>
                <MetricValue
                  label="Recent Jobs"
                  value={totalJobs.toLocaleString()}
                  icon={<Activity className="w-4 h-4" />}
                >
                  <div className="flex gap-3 mt-3 text-xs font-mono text-muted-foreground">
                    <span>{runningJobs} running</span>
                    <span className="text-[var(--color-success)]">
                      {completedJobs} done
                    </span>
                    <span className="text-destructive">{failedJobs} failed</span>
                  </div>
                </MetricValue>
              </InstrumentCard>
            </motion.div>

            {/* KPI 2: Active Agents */}
            <motion.div variants={{ hidden: { opacity: 0, y: 10 }, visible: { opacity: 1, y: 0 } }}>
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
                          w.status === "idle"
                            ? "bg-[var(--color-success)]"
                            : w.status === "busy"
                              ? "bg-cordum"
                              : "bg-muted-foreground",
                        )}
                      />
                    ))}
                  </div>
                </MetricValue>
              </InstrumentCard>
            </motion.div>

            {/* KPI 3: Safety Decisions */}
            <motion.div variants={{ hidden: { opacity: 0, y: 10 }, visible: { opacity: 1, y: 0 } }}>
              <InstrumentCard>
                <MetricValue
                  label="Safety Decisions"
                  value={`${safetyAllowRate}%`}
                  unit="allowed"
                  icon={<ShieldCheck className="w-4 h-4" />}
                >
                  <div className="flex gap-3 mt-3 text-xs font-mono">
                    <span className="text-[var(--color-success)]">
                      {safetyAllowed} allow
                    </span>
                    <span className="text-[var(--color-governance)]">
                      {safetyDenied} deny
                    </span>
                    <span className="text-[var(--color-warning)]">
                      {safetyApproval} review
                    </span>
                  </div>
                </MetricValue>
              </InstrumentCard>
            </motion.div>

            {/* KPI 4: Pending Approvals */}
            <motion.div variants={{ hidden: { opacity: 0, y: 10 }, visible: { opacity: 1, y: 0 } }}>
              <InstrumentCard
                accent={pendingApprovals.length > 0 ? "warning" : "cordum"}
              >
                <MetricValue
                  label="Pending Approvals"
                  value={pendingApprovals.length}
                  unit="awaiting"
                  icon={
                    <UserCheck
                      className={cn(
                        "w-4 h-4",
                        pendingApprovals.length > 0
                          ? "text-[var(--color-warning)]"
                          : "text-cordum",
                      )}
                    />
                  }
                >
                  {pendingApprovals.length > 0 && (
                    <Button
                      variant="ghost"
                      size="sm"
                      className="mt-2.5 text-[var(--color-warning)] hover:text-[var(--color-warning)] p-0 h-auto font-mono text-xs uppercase tracking-widest"
                      onClick={() => navigate("/approvals")}
                    >
                      Review now <ArrowRight className="w-3 h-3 ml-1" />
                    </Button>
                  )}
                </MetricValue>
              </InstrumentCard>
            </motion.div>
          </>
        )}
      </motion.div>

      {/* Onboarding checklist — shown for new users with zero data */}
      {showOnboarding &&
        !jobsLoading &&
        !workersLoading &&
        jobs.length === 0 &&
        (workers ?? []).length === 0 && (
          <OnboardingChecklist
            jobs={jobs.length}
            workers={(workers ?? []).length}
            onDismiss={() => {
              safeLocalStorage.setItem("onboarding-dismissed", "true");
              setShowOnboarding(false);
            }}
          />
        )}

      {/* Charts Row — Job Activity with Safety Overlay + Decision Distribution */}
      <div className="grid grid-cols-1 lg:grid-cols-12 gap-6">
        {/* Job Activity with Safety Overlay — 8 cols */}
        <motion.div
          initial={{ opacity: 0, y: 12 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.3, delay: 0.1 }}
          className="instrument-card lg:col-span-8 h-full"
        >
          <div className="flex items-start justify-between mb-5">
            <div className="min-w-0">
              <h2 className="font-display font-semibold text-sm text-foreground tracking-tight">
                Job Activity
              </h2>
              <p className="text-xs text-muted-foreground mt-1 leading-none">
                {liveMode
                  ? "Live — last 30 min, 2-min resolution"
                  : "Safety overlay — allowed vs denied vs approval"}
              </p>
            </div>
            <div className="flex items-center gap-3 shrink-0">
              <button
                type="button"
                aria-pressed={liveMode}
                aria-label={liveMode ? "Live mode on" : "Live mode off"}
                onClick={() => setLiveMode((v) => !v)}
                className={cn(
                  "flex items-center gap-1.5 rounded-xl px-2.5 py-1.5 text-xs font-mono font-medium transition-all",
                  liveMode
                    ? "bg-[var(--color-success)]/15 text-[var(--color-success)] ring-1 ring-[var(--color-success)]/30"
                    : "bg-surface-2 text-muted-foreground hover:text-foreground",
                )}
              >
                <Radio className={cn("w-3 h-3", liveMode && "animate-pulse")} />
                Live
              </button>
            </div>
          </div>
          <ResponsiveContainer width="100%" height={320}>
            <AreaChart data={activityData}>
              <defs>
                <linearGradient id="gradAllowed" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="var(--color-success)" stopOpacity={0.25} />
                  <stop offset="95%" stopColor="var(--color-success)" stopOpacity={0} />
                </linearGradient>
                <linearGradient id="gradDenied" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="var(--color-governance)" stopOpacity={0.25} />
                  <stop offset="95%" stopColor="var(--color-governance)" stopOpacity={0} />
                </linearGradient>
                <linearGradient id="gradApproval" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="var(--color-warning)" stopOpacity={0.25} />
                  <stop offset="95%" stopColor="var(--color-warning)" stopOpacity={0} />
                </linearGradient>
                <linearGradient id="gradFailed" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="var(--color-danger)" stopOpacity={0.25} />
                  <stop offset="95%" stopColor="var(--color-danger)" stopOpacity={0} />
                </linearGradient>
              </defs>
              <CartesianGrid
                strokeDasharray="3 3"
                stroke="rgba(255,255,255,0.04)"
                vertical={false}
              />
              <XAxis
                dataKey="time"
                tick={{ fontSize: 10, fill: "var(--muted-foreground)" }}
                axisLine={false}
                tickLine={false}
              />
              <YAxis
                tick={{ fontSize: 10, fill: "var(--muted-foreground)" }}
                axisLine={false}
                tickLine={false}
              />
              <Tooltip content={<ChartTooltip />} />
              <Area
                type="monotone"
                dataKey="allowed"
                stackId="1"
                stroke="var(--color-success)"
                fill="url(#gradAllowed)"
                strokeWidth={2}
                name="Allowed"
              />
              <Area
                type="monotone"
                dataKey="denied"
                stackId="1"
                stroke="var(--color-governance)"
                fill="url(#gradDenied)"
                strokeWidth={2}
                strokeDasharray="8 4"
                name="Denied"
              />
              <Area
                type="monotone"
                dataKey="approval"
                stackId="1"
                stroke="var(--color-warning)"
                fill="url(#gradApproval)"
                strokeWidth={2}
                strokeDasharray="4 2"
                name="Approval"
              />
              <Area
                type="monotone"
                dataKey="failed"
                stackId="1"
                stroke="var(--color-danger)"
                fill="url(#gradFailed)"
                strokeWidth={2}
                strokeDasharray="8 4 2 4"
                name="Failed"
              />
            </AreaChart>
          </ResponsiveContainer>
        </motion.div>

        {/* Decision Distribution Donut — 4 cols */}
        <motion.div
          initial={{ opacity: 0, y: 12 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.3, delay: 0.15 }}
          className="instrument-card lg:col-span-4 h-full"
        >
          <h2 className="font-display font-semibold text-sm text-foreground mb-0.5">
            Decision Distribution
          </h2>
          <p className="text-xs text-muted-foreground mb-4">
            5 safety decision types
          </p>
          <ResponsiveContainer width="100%" height={240}>
            <PieChart>
              <Pie
                data={decisionData}
                cx="50%"
                cy="50%"
                innerRadius={60}
                outerRadius={90}
                paddingAngle={4}
                dataKey="value"
                stroke="none"
              >
                {decisionData.map((entry) => (
                  <Cell key={entry.name} fill={entry.color} />
                ))}
              </Pie>
              <Tooltip content={<ChartTooltip />} />
            </PieChart>
          </ResponsiveContainer>
          <div className="space-y-2 mt-4">
            {decisionData.map((d) => (
              <div
                key={d.name}
                className="flex items-center justify-between text-xs p-2 rounded-xl bg-surface-0/40 border border-border/5"
              >
                <span className="flex items-center gap-2">
                  <span
                    className="w-2 h-2 rounded-full"
                    style={{ backgroundColor: d.color }}
                  />
                  <span className="text-muted-foreground">{d.name}</span>
                </span>
                <span className="font-mono text-foreground font-bold">{d.value}</span>
              </div>
            ))}
          </div>
        </motion.div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-12 gap-6 mt-6">
        {/* Recent Activity Table — 8 cols */}
        <motion.div
          initial={{ opacity: 0, y: 12 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.3, delay: 0.2 }}
          className="instrument-card lg:col-span-8 overflow-hidden"
        >
          <div className="flex items-center justify-between px-5 py-3 border-b border-border/50">
            <h2 className="font-display font-semibold text-sm text-foreground">
              Recent Activity
            </h2>
            <Button variant="ghost" size="sm" onClick={() => navigate("/jobs")}>
              View all <ArrowRight className="w-3 h-3 ml-1" />
            </Button>
          </div>
          <table className="w-full">
            <thead>
              <tr className="border-b border-border/50 bg-surface-0/50">
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">
                  Job ID
                </th>
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">
                  Topic
                </th>
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">
                  Status
                </th>
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">
                  Safety
                </th>
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">
                  Time
                </th>
              </tr>
            </thead>
            <tbody>
              {jobs.slice(0, 5).map((job) => {
                const safetyDecision = job.safetyDecision?.type;
                return (
                  <tr
                    key={job.id}
                    onClick={() => navigate(`/jobs/${job.id}`)}
                    className="border-b border-border/40 hover:bg-surface-1/50 transition-all cursor-pointer group"
                  >
                    <td className="px-5 py-3 font-mono text-sm text-cordum group-hover:underline">
                      {job.id.slice(0, 12)}
                    </td>
                    <td className="px-5 py-3 text-sm text-foreground">
                      {job.topic || "—"}
                    </td>
                    <td className="px-5 py-3">
                      <StatusBadge
                        variant={
                          job.status === "running"
                            ? "healthy"
                            : job.status === "succeeded"
                              ? "healthy"
                              : job.status === "failed" ||
                                  job.status === "timeout"
                                ? "danger"
                                : job.status === "denied" ||
                                    job.status === "output_quarantined"
                                  ? "governance"
                                  : job.status === "pending" ||
                                      job.status === "scheduled" ||
                                      job.status === "approval_required"
                                    ? "warning"
                                    : "muted"
                        }
                      >
                        {job.status === "output_quarantined"
                          ? "quarantined"
                          : job.status}
                      </StatusBadge>
                    </td>
                    <td className="px-5 py-3">
                      <SafetyDecisionBadge decision={safetyDecision} />
                    </td>
                    <td className="px-5 py-3 text-sm text-muted-foreground">
                      {job.updatedAt
                        ? formatRelativeTime(
                            new Date(job.updatedAt).toISOString(),
                          )
                        : "—"}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </motion.div>

        {/* Safety Decision Feed — 4 cols */}
        <motion.div
          initial={{ opacity: 0, y: 12 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.3, delay: 0.25 }}
          className="lg:col-span-4"
        >
          <SafetyDecisionFeed />
        </motion.div>
      </div>

      {/* Worker Pool Health — collapsed by default to reduce above-fold density */}
      <CollapsibleSection title="Worker Pool Health" defaultOpen={false}>
        <div className="flex items-center justify-between mb-4">
          <p className="text-xs text-muted-foreground">
            Real-time agent status
          </p>
          <Button variant="ghost" size="sm" onClick={() => navigate("/agents")}>
            View fleet <ArrowRight className="w-3 h-3 ml-1" />
          </Button>
        </div>
        <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-6 gap-3">
          {(workers ?? []).slice(0, 12).map((w, _idx) => {
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
                  <div
                    className={cn(
                      "w-2 h-2 rounded-full",
                      isOnline
                        ? "bg-[var(--color-success)] animate-pulse"
                        : "bg-muted-foreground",
                    )}
                  />
                  <span className="font-mono text-xs text-foreground truncate">
                    {w.name || w.id.slice(0, 10)}
                  </span>
                </div>
                <div className="space-y-1.5">
                  <div className="flex justify-between text-xs uppercase tracking-wider font-mono">
                    <span className="text-muted-foreground">CPU</span>
                    <span className="text-foreground">{w.cpuLoad ?? 0}%</span>
                  </div>
                  <div className="w-full h-1 rounded-full bg-surface-2 overflow-hidden">
                    <div
                      className="h-full rounded-full bg-cordum transition-all"
                      style={{ width: `${w.cpuLoad ?? 0}%` }}
                    />
                  </div>
                  <div className="flex justify-between text-xs uppercase tracking-wider font-mono">
                    <span className="text-muted-foreground">MEM</span>
                    <span className="text-foreground">
                      {w.memoryLoad ?? 0}%
                    </span>
                  </div>
                  <div className="w-full h-1 rounded-full bg-surface-2 overflow-hidden">
                    <div
                      className="h-full rounded-full bg-[var(--color-info)] transition-all"
                      style={{ width: `${w.memoryLoad ?? 0}%` }}
                    />
                  </div>
                </div>
                {/* Last policy eval line */}
                <div className="mt-2 pt-1.5 border-t border-border/40 text-xs font-mono text-muted-foreground">
                  Jobs: {w.activeJobs ?? 0} / {w.capacity ?? 0}
                </div>
              </InstrumentCard>
            );
          })}
          {(!workers || workers.length === 0) && !workersLoading && (
            <div className="col-span-full flex flex-col items-center gap-2 py-8">
              <p className="text-sm text-muted-foreground">
                No agents connected — start an agent with your API key
              </p>
              <Button
                variant="outline"
                size="sm"
                onClick={() => navigate("/agents")}
              >
                Agent setup
              </Button>
            </div>
          )}
        </div>
      </CollapsibleSection>

      {/* System Health — collapsed by default */}
      <CollapsibleSection title="Service Health" defaultOpen={false}>
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-5 gap-3">
          {statusLoading ? (
            Array.from({ length: 5 }).map((_, i) => (
              <div
                key={i}
                className="flex items-center gap-3 rounded-2xl border border-border bg-surface-0 p-3 animate-pulse"
              >
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
                accent={
                  svc.status === "healthy"
                    ? "healthy"
                    : svc.status === "degraded"
                      ? "warning"
                      : "danger"
                }
                className="p-3"
              >
                <div className="flex items-center gap-3">
                  <div
                    className={cn(
                      "w-2 h-2 rounded-full shrink-0",
                      svc.status === "healthy"
                        ? "bg-[var(--color-success)]"
                        : svc.status === "degraded"
                          ? "bg-[var(--color-warning)]"
                          : "bg-destructive",
                    )}
                  />
                  <div className="flex-1 min-w-0">
                    <p className="text-xs text-foreground font-semibold truncate">
                      {svc.name}
                    </p>
                    <p className="text-xs text-muted-foreground font-mono">
                      {svc.latency || "—"}
                    </p>
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
      </CollapsibleSection>

      {/* Audit chain tamper evidence — governance health at a glance. */}
      <AuditChainCard tenant={tenantId ?? ""} />

      {/* Approval Queue — compact banner linking to ApprovalsPage */}
      {pendingApprovals.length > 0 && (
        <div className="instrument-card flex items-center justify-between px-4 py-3 border-l-2 border-[var(--color-warning)]">
          <div className="flex items-center gap-2">
            <UserCheck className="w-4 h-4 text-[var(--color-warning)]" />
            <span className="text-sm font-semibold text-foreground">
              {pendingApprovals.length} approval
              {pendingApprovals.length > 1 ? "s" : ""} pending
            </span>
          </div>
          <Button
            variant="outline"
            size="sm"
            onClick={() => navigate("/approvals")}
          >
            Review now <ArrowRight className="w-3 h-3 ml-1" />
          </Button>
        </div>
      )}
    </div>
  );
}

