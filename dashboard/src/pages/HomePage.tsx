/*
 * DESIGN: "Control Surface" — Dashboard Overview.
 * Phase 3 wk5 (task-5101a23c): KPI tiles use StatTile primitive, recent
 * activity uses primitives/DataTable, charts use --chart-1..5 tokens (donut)
 * + decision-identity tokens (area chart series, since each series IS a
 * decision class). Heavy sections live in src/components/home/ subcomponents.
 */
import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { motion } from "framer-motion";
import {
  Area,
  AreaChart,
  CartesianGrid,
  Cell,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { ArrowRight, Radio, UserCheck, Zap } from "lucide-react";
import { get } from "@/api/client";
import {
  mapApprovalItem,
  mapHeartbeatToWorker,
  mapJobRecord,
  type BackendApprovalItem,
  type BackendHeartbeat,
  type BackendJobRecord,
} from "@/api/transform";
import type { Approval, Job, Worker } from "@/api/types";
import { AuditChainCard } from "@/components/AuditChainCard";
import { HomeKpiCards } from "@/components/home/HomeKpiCards";
import { OnboardingChecklist } from "@/components/home/OnboardingChecklist";
import { RecentActivityList } from "@/components/home/RecentActivityList";
import {
  SystemHealthCards,
  type ServiceHealth,
} from "@/components/home/SystemHealthCards";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { ChartTooltip } from "@/components/ui/ChartTooltip";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { useAuth } from "@/hooks/useAuth";
import { useStatus } from "@/hooks/useStatus";
import { safeLocalStorage } from "@/lib/storage";
import { cn } from "@/lib/utils";

const ACTIVITY_LIVE_BUCKETS = 15;
const ACTIVITY_LIVE_WINDOW_MS = 30 * 60 * 1000;
const ACTIVITY_DAY_BUCKETS = 12;

interface ActivityBucket {
  time: string;
  allowed: number;
  denied: number;
  approval: number;
  failed: number;
}

function bucketJobsForChart(jobs: Job[], liveMode: boolean): ActivityBucket[] {
  const buckets = new Map<
    string,
    Pick<ActivityBucket, "allowed" | "denied" | "approval" | "failed">
  >();
  const classify = (
    job: Job,
    bucket: { allowed: number; denied: number; approval: number; failed: number },
  ) => {
    if (job.status === "failed") bucket.failed++;
    else if (job.safetyDecision?.type === "deny") bucket.denied++;
    else if (job.safetyDecision?.type === "require_approval") bucket.approval++;
    else bucket.allowed++;
  };

  if (liveMode) {
    const now = Date.now();
    const stepMs = ACTIVITY_LIVE_WINDOW_MS / ACTIVITY_LIVE_BUCKETS;
    for (let i = 0; i < ACTIVITY_LIVE_BUCKETS; i++) {
      const t = new Date(now - ACTIVITY_LIVE_WINDOW_MS + i * stepMs);
      const label = `${String(t.getHours()).padStart(2, "0")}:${String(t.getMinutes()).padStart(2, "0")}`;
      buckets.set(label, { allowed: 0, denied: 0, approval: 0, failed: 0 });
    }
    const keys = Array.from(buckets.keys());
    for (const job of jobs) {
      const ts = new Date(job.createdAt).getTime();
      if (ts < now - ACTIVITY_LIVE_WINDOW_MS) continue;
      const idx = Math.min(
        ACTIVITY_LIVE_BUCKETS - 1,
        Math.floor((ts - (now - ACTIVITY_LIVE_WINDOW_MS)) / stepMs),
      );
      const bucket = buckets.get(keys[idx]);
      if (bucket) classify(job, bucket);
    }
  } else {
    for (let i = 0; i < ACTIVITY_DAY_BUCKETS; i++) {
      const label = `${String(i * 2).padStart(2, "0")}:00`;
      buckets.set(label, { allowed: 0, denied: 0, approval: 0, failed: 0 });
    }
    for (const job of jobs) {
      const hour = new Date(job.createdAt).getHours();
      const label = `${String(Math.floor(hour / 2) * 2).padStart(2, "0")}:00`;
      const bucket = buckets.get(label);
      if (bucket) classify(job, bucket);
    }
  }
  return Array.from(buckets, ([time, v]) => ({ time, ...v }));
}

function deriveServices(
  status: ReturnType<typeof useStatus>["data"],
): ServiceHealth[] {
  if (!status) return [];
  const services: ServiceHealth[] = [];
  const uptimeLabel =
    status.uptime_seconds != null
      ? `up ${Math.floor(status.uptime_seconds / 3600)}h ${Math.floor((status.uptime_seconds % 3600) / 60)}m`
      : "—";
  services.push({ name: "API Gateway", status: "healthy", latency: uptimeLabel });
  if (status.nats) {
    services.push({
      name: "NATS",
      status: status.nats.connected ? "healthy" : "down",
      latency: status.nats.status ?? "—",
    });
  }
  if (status.redis) {
    services.push({
      name: "Redis",
      status: status.redis.ok ? "healthy" : "down",
      latency: status.redis.error ?? (status.redis.ok ? "ok" : "—"),
    });
  }
  if (status.workers) {
    const count = status.workers.count ?? 0;
    services.push({
      name: "Workers",
      status: count > 0 ? "healthy" : "degraded",
      latency: `${count} connected`,
    });
  }
  if (status.circuit_breakers) {
    const inputState = status.circuit_breakers.input?.state ?? "unknown";
    services.push({
      name: "Safety Kernel",
      status:
        inputState === "CLOSED" ? "healthy" : inputState === "OPEN" ? "down" : "degraded",
      latency: inputState.toLowerCase(),
    });
  }
  return services;
}

// Decision-distribution donut uses neutral chart tokens (--chart-1..5). Decision
// identity (allow=success, deny=danger, etc) is reserved for the Job Activity
// area chart below where each series IS that identity class — see "one accent
// per screen" rule in dashboard/docs/design-system-audit.md.
const DECISION_PALETTE = [
  "var(--chart-5)", // Allow — teal-green
  "var(--chart-4)", // Deny — burgundy
  "var(--chart-3)", // Require Approval — amber
  "var(--chart-1)", // Constrained — teal
  "var(--chart-2)", // Throttle — orange
] as const;

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

  const jobs = jobsData?.items ?? [];
  const workersList = workers ?? [];
  const pendingApprovals =
    approvalsData?.filter((a) => a.status === "pending") ?? [];

  const services = useMemo(() => deriveServices(statusData), [statusData]);
  const activityData = useMemo(
    () => bucketJobsForChart(jobs, liveMode),
    [jobs, liveMode],
  );

  const decisionCounts = useMemo(() => {
    const counts = { allow: 0, deny: 0, require_approval: 0, allow_with_constraints: 0, throttle: 0 };
    for (const job of jobs) {
      const tier = job.safetyDecision?.type;
      if (tier && tier in counts) counts[tier as keyof typeof counts]++;
    }
    return counts;
  }, [jobs]);

  const decisionData = [
    { name: "Allow", value: decisionCounts.allow, color: DECISION_PALETTE[0] },
    { name: "Deny", value: decisionCounts.deny, color: DECISION_PALETTE[1] },
    { name: "Require Approval", value: decisionCounts.require_approval, color: DECISION_PALETTE[2] },
    { name: "Constrained", value: decisionCounts.allow_with_constraints, color: DECISION_PALETTE[3] },
    { name: "Throttle", value: decisionCounts.throttle, color: DECISION_PALETTE[4] },
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

  const showZeroStateOnboarding =
    showOnboarding &&
    !jobsLoading &&
    !workersLoading &&
    jobs.length === 0 &&
    workersList.length === 0;

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

      <HomeKpiCards
        jobs={jobs}
        workers={workersList}
        pendingApprovals={pendingApprovals}
        isLoading={isLoading}
      />

      {showZeroStateOnboarding && (
        <OnboardingChecklist
          jobs={jobs.length}
          workers={workersList.length}
          onDismiss={() => {
            safeLocalStorage.setItem("onboarding-dismissed", "true");
            setShowOnboarding(false);
          }}
        />
      )}

      <div className="grid grid-cols-1 lg:grid-cols-12 gap-6">
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
              <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
              <XAxis dataKey="time" tick={{ fontSize: 10, fill: "var(--muted-foreground)" }} axisLine={false} tickLine={false} />
              <YAxis tick={{ fontSize: 10, fill: "var(--muted-foreground)" }} axisLine={false} tickLine={false} />
              <Tooltip content={<ChartTooltip />} />
              <Area type="monotone" dataKey="allowed" stackId="1" stroke="var(--color-success)" fill="url(#gradAllowed)" strokeWidth={2} name="Allowed" />
              <Area type="monotone" dataKey="denied" stackId="1" stroke="var(--color-governance)" fill="url(#gradDenied)" strokeWidth={2} strokeDasharray="8 4" name="Denied" />
              <Area type="monotone" dataKey="approval" stackId="1" stroke="var(--color-warning)" fill="url(#gradApproval)" strokeWidth={2} strokeDasharray="4 2" name="Approval" />
              <Area type="monotone" dataKey="failed" stackId="1" stroke="var(--color-danger)" fill="url(#gradFailed)" strokeWidth={2} strokeDasharray="8 4 2 4" name="Failed" />
            </AreaChart>
          </ResponsiveContainer>
        </motion.div>

        <motion.div
          initial={{ opacity: 0, y: 12 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.3, delay: 0.15 }}
          className="instrument-card lg:col-span-4 h-full"
        >
          <h2 className="font-display font-semibold text-sm text-foreground mb-0.5">
            Decision Distribution
          </h2>
          <p className="text-xs text-muted-foreground mb-4">5 safety decision types</p>
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
                  <span className="w-2 h-2 rounded-full" style={{ backgroundColor: d.color }} />
                  <span className="text-muted-foreground">{d.name}</span>
                </span>
                <span className="font-mono text-foreground font-bold">{d.value}</span>
              </div>
            ))}
          </div>
        </motion.div>
      </div>

      <RecentActivityList jobs={jobs} />

      <SystemHealthCards
        workers={workersList}
        workersLoading={workersLoading}
        services={services}
        statusLoading={statusLoading}
      />

      <AuditChainCard tenant={tenantId ?? ""} />

      {pendingApprovals.length > 0 && (
        <div className="instrument-card flex items-center justify-between px-4 py-3 border-l-2 border-[var(--color-warning)]">
          <div className="flex items-center gap-2">
            <UserCheck className="w-4 h-4 text-[var(--color-warning)]" />
            <span className="text-sm font-semibold text-foreground">
              {pendingApprovals.length} approval
              {pendingApprovals.length > 1 ? "s" : ""} pending
            </span>
          </div>
          <Button variant="outline" size="sm" onClick={() => navigate("/approvals")}>
            Review now <ArrowRight className="w-3 h-3 ml-1" />
          </Button>
        </div>
      )}
    </div>
  );
}
