import { useMemo, useState } from "react";
import { Clock } from "lucide-react";
import { InstrumentCard, InstrumentCardBody, InstrumentCardHeader } from "@/components/ui/InstrumentCard";
import { Tabs } from "@/components/ui/Tabs";
import { StatTile } from "@/components/ui/StatTile";
import { EmptyState } from "@/components/ui/EmptyState";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { Skeleton } from "@/components/ui/Skeleton";
import { useApprovalAnalytics } from "@/hooks/useApprovalAnalytics";
import type {
  ApprovalAnalyticsGroupBy,
  ApprovalAnalyticsWindow,
} from "@/api/types";
import { BottleneckTable } from "./BottleneckTable";
import { cn } from "@/lib/utils";

export interface ApprovalAnalyticsWidgetProps {
  /**
   * Where this instance of the widget is mounted. Changes only the
   * default window (Command Center → live 24h ops; governance page
   * → week-over-week trends) and some minor copy. Data source is
   * identical.
   */
  context?: "command-center" | "governance";
  className?: string;
  defaultWindow?: ApprovalAnalyticsWindow;
}

type BreakdownTab = "rule" | "agent" | "topic";

const WINDOW_TABS: { id: ApprovalAnalyticsWindow; label: string }[] = [
  { id: "24h", label: "24h" },
  { id: "7d", label: "7d" },
  { id: "30d", label: "30d" },
];

const BREAKDOWN_TABS: { id: BreakdownTab; label: string }[] = [
  { id: "rule", label: "By rule" },
  { id: "agent", label: "By agent" },
  { id: "topic", label: "By topic" },
];

function formatSeconds(value: number | null): string {
  if (value === null) return "—";
  if (value < 60) return `${Math.round(value)} s`;
  if (value < 3600) return `${Math.round(value / 60)} m`;
  return `${(value / 3600).toFixed(1)} h`;
}

function formatRatio(auto: number, manual: number): string {
  const total = auto + manual;
  if (total === 0) return "—";
  const autoPct = Math.round((auto / total) * 100);
  return `${autoPct}% auto · ${100 - autoPct}% manual`;
}

// formatApprovalRate renders approved / total as an integer-rounded
// percentage, or "—" when total is 0. The null distinction matters:
// "0%" means "nothing approved in a non-empty window"; "—" means
// "no sample set yet to reason about". Callers pass total, not
// total-of-resolved, so pending approvals drag the rate down — which
// is the right signal for a reviewer backlog.
function formatApprovalRate(approved: number, total: number): string {
  if (total <= 0) return "—";
  return `${Math.round((approved / total) * 100)}%`;
}

export function ApprovalAnalyticsWidget({
  context = "command-center",
  className,
  defaultWindow = "24h",
}: ApprovalAnalyticsWidgetProps) {
  const [window, setWindow] = useState<ApprovalAnalyticsWindow>(defaultWindow);
  const [breakdown, setBreakdown] = useState<BreakdownTab>("rule");

  const overall = useApprovalAnalytics({ window, groupBy: "overall" });
  const grouped = useApprovalAnalytics({ window, groupBy: breakdown as ApprovalAnalyticsGroupBy, limit: 10 });

  const summary = overall.data?.summary;
  const groups = grouped.data?.groups ?? [];

  const isLoading = overall.isLoading || grouped.isLoading;
  const isError = overall.isError || grouped.isError;
  const error = overall.error ?? grouped.error;

  const subtitle = useMemo(() => {
    switch (context) {
      case "governance":
        return "Weekly bottlenecks, auto vs manual, and time-to-approve trend.";
      default:
        return "Time-to-approve, auto vs manual, bottlenecks.";
    }
  }, [context]);

  return (
    <InstrumentCard accent="governance" className={cn("w-full", className)}>
      <InstrumentCardHeader
        title="Approval analytics"
        subtitle={subtitle}
        action={
          <Tabs
            ariaLabel="Approval analytics window"
            variant="segmented"
            tabs={WINDOW_TABS}
            activeTab={window}
            onChange={(id) => setWindow(id as ApprovalAnalyticsWindow)}
          />
        }
      />
      <InstrumentCardBody className="space-y-4">
        {isLoading ? (
          <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
            <Skeleton className="h-20 w-full" />
            <Skeleton className="h-20 w-full" />
            <Skeleton className="h-20 w-full" />
            <Skeleton className="h-20 w-full" />
          </div>
        ) : isError ? (
          <ErrorBanner
            title="Approval analytics unavailable"
            message={error instanceof Error ? error.message : undefined}
            onRetry={() => {
              overall.refetch();
              grouped.refetch();
            }}
          />
        ) : !summary || summary.total === 0 ? (
          <EmptyState
            icon={<Clock className="h-5 w-5" />}
            title="No approvals in this window"
            description="When safety rules require human review, analytics appear here."
          />
        ) : (
          <>
            <div
              className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-5"
              role="group"
              aria-label="Approval analytics KPIs"
            >
              <StatTile
                label="Total approvals"
                value={summary.total.toLocaleString()}
                helperText={`${summary.approved} approved · ${summary.rejected} rejected`}
              />
              <StatTile
                label="Approval rate"
                value={formatApprovalRate(summary.approved, summary.total)}
                helperText={
                  summary.total === 0
                    ? "no data"
                    : `${summary.approved} of ${summary.total} approved`
                }
              />
              <StatTile
                label="Avg time to approve"
                value={formatSeconds(summary.avgTimeToApproveSeconds)}
                helperText={summary.avgTimeToApproveSeconds === null ? "no data" : "window average"}
              />
              <StatTile
                label="Auto vs manual"
                value={formatRatio(summary.autoResolved, summary.manualResolved)}
              />
              <StatTile
                label="p90 latency"
                value={formatSeconds(summary.p90)}
                helperText={summary.p99 === null ? undefined : `p99 ${formatSeconds(summary.p99)}`}
              />
            </div>

            <div className="space-y-3">
              <Tabs
                ariaLabel="Approval breakdown"
                tabs={BREAKDOWN_TABS}
                activeTab={breakdown}
                onChange={(id) => setBreakdown(id as BreakdownTab)}
              />
              <BottleneckTable
                groups={groups}
                summaryAvgSeconds={summary.avgTimeToApproveSeconds}
                variant={breakdown}
              />
            </div>
          </>
        )}
      </InstrumentCardBody>
    </InstrumentCard>
  );
}

export default ApprovalAnalyticsWidget;
