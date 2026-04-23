/*
 * DESIGN: "Control Surface" — Security Overview
 * KPI strip, safety decision feed, attention panel, activity chart, decision distribution.
 * All query-backed blocks have explicit loading/empty/error/success states.
 */
import { useState, useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { motion } from "framer-motion";
import {
  AreaChart, Area, PieChart, Pie, Cell,
  ResponsiveContainer, XAxis, YAxis, Tooltip, CartesianGrid,
} from "recharts";
import {
  ShieldCheck, UserCheck, AlertTriangle, ArrowRight,
  CheckCircle2, XCircle, RefreshCw, Eye,
} from "lucide-react";
import { PageHeader } from "@/components/layout/PageHeader";
import { InstrumentCard } from "@/components/ui/InstrumentCard";
import { MetricValue } from "@/components/ui/MetricValue";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { SafetyDecisionFeed } from "@/components/home/SafetyDecisionFeed";
import { useApprovals, useApproveJob, useRejectJob } from "@/hooks/useApprovals";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { toast } from "sonner";
import { useSafetyDecisions } from "@/hooks/useSafetyDecisions";
import { usePipelineMetrics } from "@/hooks/useStatus";
import { cn, formatRelativeTime } from "@/lib/utils";
import { ChartTooltip } from "@/components/ui/ChartTooltip";
import {
  chartColors, axisTickStyle, gridProps,
  gradientId, gradientFill, getDecisionLabel,
} from "@/lib/chart-theme";

/* ------------------------------------------------------------------ */
/* Page                                                               */
/* ------------------------------------------------------------------ */

export default function SecurityOverviewPage() {
  const navigate = useNavigate();
  const [denyTarget, setDenyTarget] = useState<string | null>(null);
  const approveMut = useApproveJob();
  const rejectMut = useRejectJob();

  // Data hooks
  const { decisions, isLoading: decisionsLoading, isError: decisionsError } = useSafetyDecisions(200);
  const { data: approvalsData, isLoading: approvalsLoading } = useApprovals("pending");
  const pendingApprovals = approvalsData?.items ?? [];
  const { data: pipeline, isLoading: pipelineLoading, isError: pipelineError } = usePipelineMetrics();

  // Compute safety stats from decisions
  const safetyStats = useMemo(() => {
    const counts = { allow: 0, deny: 0, require_approval: 0, allow_with_constraints: 0, throttle: 0 };
    for (const d of decisions) {
      if (d.decision in counts) {
        counts[d.decision as keyof typeof counts] += 1;
      }
    }
    const total = Object.values(counts).reduce((s, v) => s + v, 0);
    const allowRate = total > 0 ? Math.round((counts.allow / total) * 100) : 0;
    return { ...counts, total, allowRate };
  }, [decisions]);

  // Activity chart — 2-hour buckets
  const activityData = useMemo(() => {
    const buckets = new Map<string, { allowed: number; denied: number; approval: number }>();
    for (let i = 0; i < 12; i++) {
      const label = String(i * 2).padStart(2, "0") + ":00";
      buckets.set(label, { allowed: 0, denied: 0, approval: 0 });
    }
    for (const d of decisions) {
      const hour = new Date(d.timestamp).getHours();
      const bucket = String(Math.floor(hour / 2) * 2).padStart(2, "0") + ":00";
      const b = buckets.get(bucket);
      if (b) {
        if (d.decision === "deny") b.denied++;
        else if (d.decision === "require_approval") b.approval++;
        else b.allowed++;
      }
    }
    return Array.from(buckets, ([time, v]) => ({ time, ...v }));
  }, [decisions]);

  // Decision distribution donut
  const decisionData = [
    { name: "Allow", value: safetyStats.allow, color: chartColors.allow },
    { name: "Deny", value: safetyStats.deny, color: chartColors.deny },
    { name: "Approval", value: safetyStats.require_approval, color: chartColors.require_approval },
    { name: "Constrained", value: safetyStats.allow_with_constraints, color: chartColors.allow_with_constraints },
    { name: "Throttle", value: safetyStats.throttle, color: chartColors.throttle },
  ];

  const isLoading = decisionsLoading || approvalsLoading || pipelineLoading;

  return (
    <div className="space-y-6">
      <PageHeader
        label="Security"
        title="Security Overview"
        subtitle="Real-time governance posture and safety decision monitoring"
        actions={
          <Button variant="outline" size="sm" onClick={() => navigate("/policies")}>
            <Eye className="w-3.5 h-3.5 mr-1" />
            Policy Studio
          </Button>
        }
      />

      {/* KPI Row — instrument-card anatomy */}
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
            {/* Safety Allow Rate */}
            <InstrumentCard accent={safetyStats.allowRate >= 80 ? "healthy" : safetyStats.allowRate >= 50 ? "warning" : "danger"}>
              <div className="p-5">
                <MetricValue
                  value={`${safetyStats.allowRate}%`}
                  label="Allow Rate"
                  icon={<ShieldCheck className="w-4 h-4" />}
                />
                <div className="flex gap-3 mt-2 text-[10px] font-mono">
                  <span className="text-emerald-400">{safetyStats.allow} allow</span>
                  <span className="text-red-400">{safetyStats.deny} deny</span>
                  <span className="text-amber-400">{safetyStats.require_approval} review</span>
                </div>
              </div>
            </InstrumentCard>

            {/* Total Decisions */}
            <InstrumentCard>
              <div className="p-5">
                <MetricValue
                  value={safetyStats.total}
                  label="Total Decisions"
                  icon={<ShieldCheck className="w-4 h-4" />}
                />
                <p className="text-xs text-muted-foreground mt-2">
                  From recent jobs and live stream
                </p>
              </div>
            </InstrumentCard>

            {/* Pending Approvals */}
            <InstrumentCard accent={pendingApprovals.length > 0 ? "warning" : "cordum"}>
              <div className="p-5">
                <MetricValue
                  value={pendingApprovals.length}
                  label="Pending Approvals"
                  icon={<UserCheck className={cn("w-4 h-4", pendingApprovals.length > 0 && "text-amber-400")} />}
                />
                {pendingApprovals.length > 0 && (
                  <Button
                    variant="ghost"
                    size="sm"
                    className="mt-2 text-amber-400 hover:text-amber-300 p-0 h-auto"
                    onClick={() => navigate("/approvals")}
                  >
                    Review now <ArrowRight className="w-3 h-3 ml-1" />
                  </Button>
                )}
                {pendingApprovals.length === 0 && (
                  <p className="text-xs text-muted-foreground mt-2">All clear</p>
                )}
              </div>
            </InstrumentCard>

            {/* Pipeline */}
            <InstrumentCard accent={pipelineError ? "danger" : "cordum"}>
              <div className="p-5">
                <MetricValue
                  value={pipeline ? (pipeline.running ?? 0) : "—"}
                  label="Running Jobs"
                  icon={<AlertTriangle className="w-4 h-4" />}
                />
                {pipeline ? (
                  <div className="flex gap-3 mt-2 text-[10px] font-mono text-muted-foreground">
                    <span>{pipeline.pending ?? 0} pending</span>
                    <span>{pipeline.dispatched ?? 0} dispatched</span>
                    <span className={cn((pipeline.failed ?? 0) > 0 && "text-red-400")}>
                      {pipeline.failed ?? 0} failed
                    </span>
                  </div>
                ) : (
                  <p className="text-xs text-muted-foreground mt-2">
                    {pipelineError ? "Unable to load" : "Loading..."}
                  </p>
                )}
              </div>
            </InstrumentCard>
          </>
        )}
      </motion.div>

      {/* Charts Row + Feed */}
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
        {/* Activity Chart — 2 cols */}
        <motion.div
          initial={{ opacity: 0, y: 12 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.3, delay: 0.1 }}
          className="instrument-card p-5 lg:col-span-2"
        >
          <div className="flex items-center justify-between mb-4">
            <div>
              <h3 className="font-display font-semibold text-sm text-foreground">Safety Activity</h3>
              <p className="text-xs text-muted-foreground mt-0.5">Decision overlay — allowed vs denied vs approval</p>
            </div>
            <div className="flex items-center gap-4 text-[10px] font-mono">
              <span className="flex items-center gap-1.5">
                <span className="w-2 h-2 rounded-full bg-emerald-400" />Allowed
              </span>
              <span className="flex items-center gap-1.5">
                <span className="w-2 h-2 rounded-full bg-red-400" />Denied
              </span>
              <span className="flex items-center gap-1.5">
                <span className="w-2 h-2 rounded-full bg-amber-400" />Approval
              </span>
            </div>
          </div>
          {decisionsError && decisions.length === 0 ? (
            <EmptyState
              icon={<AlertTriangle className="w-5 h-5" />}
              title="Unable to load activity data"
              description="Check gateway connectivity."
              action={
                <Button variant="outline" size="sm" onClick={() => window.location.reload()}>
                  <RefreshCw className="w-3 h-3 mr-1" />Retry
                </Button>
              }
            />
          ) : decisions.length === 0 && !decisionsLoading ? (
            <EmptyState
              icon={<ShieldCheck className="w-5 h-5" />}
              title="No activity yet"
              description="Safety decisions will appear here when jobs are processed."
            />
          ) : (
            <ResponsiveContainer width="100%" height={260}>
              <AreaChart data={activityData}>
                <defs>
                  <linearGradient id={gradientId("allowed")} x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor={chartColors.allow} stopOpacity={0.25} />
                    <stop offset="95%" stopColor={chartColors.allow} stopOpacity={0} />
                  </linearGradient>
                  <linearGradient id={gradientId("denied")} x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor={chartColors.deny} stopOpacity={0.25} />
                    <stop offset="95%" stopColor={chartColors.deny} stopOpacity={0} />
                  </linearGradient>
                  <linearGradient id={gradientId("approval")} x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor={chartColors.require_approval} stopOpacity={0.25} />
                    <stop offset="95%" stopColor={chartColors.require_approval} stopOpacity={0} />
                  </linearGradient>
                </defs>
                <CartesianGrid {...gridProps} />
                <XAxis dataKey="time" tick={axisTickStyle} axisLine={false} tickLine={false} />
                <YAxis tick={axisTickStyle} axisLine={false} tickLine={false} />
                <Tooltip content={<ChartTooltip />} />
                <Area type="monotone" dataKey="allowed" stackId="1" stroke={chartColors.allow} fill={gradientFill("allowed")} strokeWidth={2} name="Allowed" />
                <Area type="monotone" dataKey="denied" stackId="1" stroke={chartColors.deny} fill={gradientFill("denied")} strokeWidth={2} name="Denied" />
                <Area type="monotone" dataKey="approval" stackId="1" stroke={chartColors.require_approval} fill={gradientFill("approval")} strokeWidth={2} name="Approval" />
              </AreaChart>
            </ResponsiveContainer>
          )}
        </motion.div>

        {/* Decision Distribution Donut — 1 col */}
        <motion.div
          initial={{ opacity: 0, y: 12 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.3, delay: 0.15 }}
          className="instrument-card p-5"
        >
          <h3 className="font-display font-semibold text-sm text-foreground mb-0.5">Decision Distribution</h3>
          <p className="text-xs text-muted-foreground mb-4">5 safety decision types</p>
          {safetyStats.total === 0 && !decisionsLoading ? (
            <EmptyState
              icon={<ShieldCheck className="w-5 h-5" />}
              title="No decisions"
              description="Awaiting safety evaluations."
              className="py-8"
            />
          ) : (
            <>
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
            </>
          )}
        </motion.div>
      </div>

      {/* Safety Decision Feed */}
      <motion.div
        initial={{ opacity: 0, y: 12 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.3, delay: 0.2 }}
      >
        <SafetyDecisionFeed />
      </motion.div>

      {/* Attention: Pending Approvals */}
      {pendingApprovals.length > 0 && (
        <motion.div
          initial={{ opacity: 0, y: 12 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.3, delay: 0.25 }}
          className="space-y-3"
        >
          <div className="flex items-center justify-between">
            <h3 className="font-display font-semibold text-sm text-foreground">
              Needs Attention
            </h3>
            <Button variant="ghost" size="sm" onClick={() => navigate("/approvals")}>
              View all <ArrowRight className="w-3 h-3 ml-1" />
            </Button>
          </div>
          {pendingApprovals.slice(0, 3).map((approval) => (
            <div
              key={approval.id}
              role="button"
              tabIndex={0}
              aria-label={`Review approval ${approval.id}`}
              onClick={() => navigate("/approvals")}
              onKeyDown={(e) => {
                if (e.key === "Enter" || e.key === " ") {
                  e.preventDefault();
                  navigate("/approvals");
                }
              }}
              className="instrument-card status-warning p-4 cursor-pointer hover:bg-surface-1/50 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-cordum transition-colors"
            >
              <div className="flex items-start justify-between">
                <div className="flex-1">
                  <div className="flex items-center gap-3 mb-1">
                    <span className="font-mono text-sm text-cordum">{approval.id.slice(0, 12)}</span>
                    <StatusBadge variant="warning" dot pulse>pending</StatusBadge>
                    <span className="text-[10px] text-muted-foreground font-mono">
                      {approval.requestedAt ? formatRelativeTime(approval.requestedAt) : "—"}
                    </span>
                  </div>
                  <p className="text-sm font-medium text-foreground">
                    {approval.topic || "Pending Approval"}
                  </p>
                </div>
                <div className="flex gap-2 ml-4 shrink-0">
                  <Button size="sm" variant="danger" onClick={(e) => { e.stopPropagation(); setDenyTarget(approval.id); }}>
                    <XCircle className="w-3.5 h-3.5 mr-1" />Deny
                  </Button>
                  <Button
                    size="sm"
                    variant="primary"
                    loading={approveMut.isPending}
                    onClick={(e) => {
                      e.stopPropagation();
                      approveMut.mutate({ id: approval.id }, {
                        onSuccess: () => toast.success("Approval granted"),
                        onError: () => toast.error("Failed to approve"),
                      });
                    }}
                  >
                    <CheckCircle2 className="w-3.5 h-3.5 mr-1" />Approve
                  </Button>
                </div>
              </div>
            </div>
          ))}
        </motion.div>
      )}

      <ConfirmDialog
        open={!!denyTarget}
        onClose={() => setDenyTarget(null)}
        onConfirm={() => {
          if (denyTarget) {
            rejectMut.mutate({ id: denyTarget, reason: "Denied from security overview" }, {
              onSuccess: () => toast.success("Approval denied"),
              onError: () => toast.error("Failed to deny"),
            });
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
