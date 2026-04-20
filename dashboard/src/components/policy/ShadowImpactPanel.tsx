import { useMemo, useState } from "react";
import { BarChart as BarChartIcon, RefreshCw } from "lucide-react";
import {
  Bar,
  BarChart,
  CartesianGrid,
  Legend,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import {
  useShadowResultsSummary,
  useShadowResultsTimeseries,
} from "@/hooks/useShadowPolicy";
import { ShadowComparisonsTable } from "./ShadowComparisonsTable";

const WINDOWS: Record<string, { ms: number; bucket: string }> = {
  "1h": { ms: 60 * 60 * 1000, bucket: "1m" },
  "24h": { ms: 24 * 60 * 60 * 1000, bucket: "15m" },
  "7d": { ms: 7 * 24 * 60 * 60 * 1000, bucket: "1h" },
  "30d": { ms: 30 * 24 * 60 * 60 * 1000, bucket: "1d" },
};

type WindowKey = keyof typeof WINDOWS;

const COLORS = {
  escalated: "#f97316", // orange
  relaxed: "#10b981", // emerald
  approval_differ: "#f59e0b", // amber
  unchanged: "#64748b", // slate
};

export interface ShadowImpactPanelProps {
  bundleID: string;
}

/**
 * ShadowImpactPanel renders the three-section impact dashboard:
 *   (1) summary callout (would-have-been-blocked count + secondaries),
 *   (2) stacked bar chart over time,
 *   (3) drill-down comparisons table.
 *
 * Time-range picker is local state (no need to cross tabs). Empty
 * state acknowledges that shadow results accrue over time — activation
 * alone doesn't populate the panel instantly.
 */
export function ShadowImpactPanel({ bundleID }: ShadowImpactPanelProps) {
  const [windowKey, setWindowKey] = useState<WindowKey>("24h");

  const range = useMemo(() => {
    const untilMs = Date.now();
    const fromMs = untilMs - WINDOWS[windowKey].ms;
    return { bundleID, fromMs, untilMs };
  }, [bundleID, windowKey]);

  const summary = useShadowResultsSummary(range);
  const timeseries = useShadowResultsTimeseries({
    ...range,
    bucket: WINDOWS[windowKey].bucket,
  });

  return (
    <div className="space-y-4" data-testid="shadow-impact-panel">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <BarChartIcon className="w-4 h-4 text-cordum" />
          <span className="text-xs font-mono uppercase tracking-widest text-muted-foreground">
            Shadow impact
          </span>
        </div>
        <div className="flex items-center gap-1" role="radiogroup" aria-label="Time window">
          {(Object.keys(WINDOWS) as WindowKey[]).map((k) => (
            <Button
              key={k}
              variant={windowKey === k ? "outline" : "ghost"}
              size="sm"
              onClick={() => setWindowKey(k)}
              role="radio"
              aria-checked={windowKey === k}
            >
              {k}
            </Button>
          ))}
          <Button
            variant="ghost"
            size="sm"
            onClick={() => {
              void summary.refetch();
              void timeseries.refetch();
            }}
            aria-label="Refresh"
          >
            <RefreshCw className="w-3.5 h-3.5" />
          </Button>
        </div>
      </div>

      <SummaryCallout
        loading={summary.isLoading}
        error={summary.isError ? (summary.error instanceof Error ? summary.error.message : "summary error") : null}
        totalEvaluated={summary.data?.total_evaluated ?? 0}
        escalated={summary.data?.escalated_count ?? 0}
        relaxed={summary.data?.relaxed_count ?? 0}
        approvalDiffer={summary.data?.approval_differ_count ?? 0}
        unchanged={summary.data?.unchanged_count ?? 0}
      />

      <div className="instrument-card p-4">
        <p className="text-xs font-mono uppercase tracking-widest text-muted-foreground mb-3">
          Outcomes over time
        </p>
        {timeseries.isLoading ? (
          <SkeletonCard />
        ) : timeseries.isError ? (
          <ErrorBanner
            message={timeseries.error instanceof Error ? timeseries.error.message : "timeseries error"}
            onRetry={() => void timeseries.refetch()}
          />
        ) : (timeseries.data?.buckets?.length ?? 0) === 0 ? (
          <EmptyState
            title="No shadow evaluations yet"
            description="Results will appear as jobs flow through the kernel. Try refreshing or widen the window."
          />
        ) : (
          <div
            style={{ width: "100%", height: 280 }}
            role="img"
            aria-label={`Shadow outcomes over ${timeseries.data!.buckets.length} buckets: escalated, relaxed, approval_differ, unchanged`}
          >
            <ResponsiveContainer>
              <BarChart data={timeseries.data!.buckets}>
                <CartesianGrid strokeDasharray="3 3" stroke="var(--color-border)" />
                <XAxis
                  dataKey="ts_ms"
                  tickFormatter={(v: number) => new Date(v).toLocaleTimeString()}
                  tick={{ fontSize: 11 }}
                />
                <YAxis tick={{ fontSize: 11 }} />
                <Tooltip
                  labelFormatter={(v) => new Date(v as number).toLocaleString()}
                  contentStyle={{ background: "var(--color-surface-1)", border: "1px solid var(--color-border)" }}
                />
                <Legend wrapperStyle={{ fontSize: 12 }} />
                <Bar dataKey="escalated" stackId="a" fill={COLORS.escalated} />
                <Bar dataKey="relaxed" stackId="a" fill={COLORS.relaxed} />
                <Bar dataKey="approval_differ" stackId="a" fill={COLORS.approval_differ} />
                <Bar dataKey="unchanged" stackId="a" fill={COLORS.unchanged} />
              </BarChart>
            </ResponsiveContainer>
          </div>
        )}
      </div>

      <ShadowComparisonsTable bundleID={bundleID} fromMs={range.fromMs} untilMs={range.untilMs} />
    </div>
  );
}

function SummaryCallout({
  loading,
  error,
  totalEvaluated,
  escalated,
  relaxed,
  approvalDiffer,
  unchanged,
}: {
  loading: boolean;
  error: string | null;
  totalEvaluated: number;
  escalated: number;
  relaxed: number;
  approvalDiffer: number;
  unchanged: number;
}) {
  if (loading) return <SkeletonCard />;
  if (error) return <ErrorBanner message={error} />;

  // Would-have-been-blocked = escalated + approval_differ (the shadow
  // tightened the outcome). Relaxed = shadow would have allowed what
  // the active blocked. Unchanged = agreement.
  const wouldBlock = escalated + approvalDiffer;

  return (
    <div
      className="instrument-card p-4 grid grid-cols-2 gap-4 sm:grid-cols-4"
      data-testid="shadow-summary-callout"
    >
      <div>
        <p className="text-[10px] font-mono uppercase tracking-widest text-muted-foreground">
          Would have been blocked
        </p>
        <p className="mt-1 text-3xl font-semibold text-[var(--color-warning)]">
          {wouldBlock.toLocaleString()}
        </p>
        <p className="text-xs text-muted-foreground">
          of {totalEvaluated.toLocaleString()} evaluated
        </p>
      </div>
      <SecondaryMetric label="Escalated" value={escalated} tone="warn" />
      <SecondaryMetric label="Relaxed" value={relaxed} tone="ok" />
      <SecondaryMetric label="Unchanged" value={unchanged} tone="muted" />
    </div>
  );
}

function SecondaryMetric({
  label,
  value,
  tone,
}: {
  label: string;
  value: number;
  tone: "ok" | "warn" | "muted";
}) {
  const color =
    tone === "warn"
      ? "text-orange-400"
      : tone === "ok"
        ? "text-emerald-400"
        : "text-muted-foreground";
  return (
    <div>
      <p className="text-[10px] font-mono uppercase tracking-widest text-muted-foreground">
        {label}
      </p>
      <p className={`mt-1 text-2xl font-semibold ${color}`}>{value.toLocaleString()}</p>
    </div>
  );
}
