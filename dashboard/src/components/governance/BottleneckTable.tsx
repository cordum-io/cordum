import { DataTable } from "@/components/ui/DataTable";
import { EmptyState } from "@/components/ui/EmptyState";
import { isBottleneckGroup } from "@/api/transform";
import type { ApprovalAnalyticsGroup } from "@/api/types";
import { cn } from "@/lib/utils";

export interface BottleneckTableProps {
  groups: ApprovalAnalyticsGroup[];
  summaryAvgSeconds: number | null;
  /**
   * Passed through to DataTable for keying. Widget supplies
   * "rule" | "agent" | "topic" so the test harness can assert
   * tab-scoped re-renders.
   */
  variant: "rule" | "agent" | "topic";
}

function formatSeconds(value: number | null): string {
  if (value === null) return "—";
  if (value < 60) return `${Math.round(value)} s`;
  if (value < 3600) return `${Math.round(value / 60)} m`;
  return `${(value / 3600).toFixed(1)} h`;
}

function formatCount(value: number): string {
  return value.toLocaleString();
}

// formatGroupApprovalRate mirrors the widget-level formatApprovalRate.
// Returns "—" when total is 0 to keep the "no data" vs "0%" distinction
// consistent across the KPI strip and the per-group table.
function formatGroupApprovalRate(approved: number, total: number): string {
  if (total <= 0) return "—";
  return `${Math.round((approved / total) * 100)}%`;
}

const headerLabel: Record<BottleneckTableProps["variant"], string> = {
  rule: "Rule",
  agent: "Agent",
  topic: "Topic",
};

export function BottleneckTable({ groups, summaryAvgSeconds, variant }: BottleneckTableProps) {
  if (groups.length === 0) {
    return (
      <EmptyState
        title="No approvals in this breakdown"
        description="Approvals that require human review appear here once the window fills in."
      />
    );
  }

  const columns = [
    {
      key: "key",
      header: headerLabel[variant],
      render: (row: ApprovalAnalyticsGroup) => {
        const bottleneck = isBottleneckGroup(row, summaryAvgSeconds);
        return (
          <div className="flex items-center gap-2">
            <span className="font-mono text-xs text-foreground">{row.label || row.key}</span>
            {bottleneck && (
              <span
                role="status"
                aria-label="bottleneck"
                className="inline-flex items-center rounded-full bg-[color:var(--color-governance)]/15 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-[color:var(--color-governance)]"
              >
                bottleneck
              </span>
            )}
          </div>
        );
      },
    },
    {
      key: "avgTtar",
      header: "Avg time to approve",
      width: "10rem",
      render: (row: ApprovalAnalyticsGroup) => {
        const bottleneck = isBottleneckGroup(row, summaryAvgSeconds);
        return (
          <span
            className={cn(
              "font-mono text-xs",
              bottleneck ? "text-[color:var(--color-governance)] font-semibold" : "text-foreground",
            )}
            aria-label={`average time to approve ${formatSeconds(row.avgTtarSeconds)}`}
          >
            {formatSeconds(row.avgTtarSeconds)}
          </span>
        );
      },
    },
    {
      key: "p90",
      header: "p90",
      width: "6rem",
      align: "right" as const,
      render: (row: ApprovalAnalyticsGroup) => (
        <span className="font-mono text-xs text-muted-foreground">
          {formatSeconds(row.p90Seconds)}
        </span>
      ),
    },
    {
      key: "approvalRate",
      header: "Approval rate",
      width: "7rem",
      align: "right" as const,
      render: (row: ApprovalAnalyticsGroup) => (
        <span
          className="font-mono text-xs"
          aria-label={`approval rate ${formatGroupApprovalRate(row.approved, row.total)}`}
        >
          {formatGroupApprovalRate(row.approved, row.total)}
        </span>
      ),
    },
    {
      key: "total",
      header: "Total",
      width: "5rem",
      align: "right" as const,
      render: (row: ApprovalAnalyticsGroup) => (
        <span className="font-mono text-xs">{formatCount(row.total)}</span>
      ),
    },
    {
      key: "autoManual",
      header: "Auto / Manual",
      width: "9rem",
      align: "right" as const,
      render: (row: ApprovalAnalyticsGroup) => (
        <span className="font-mono text-xs text-muted-foreground">
          {formatCount(row.autoCount)} / {formatCount(row.manualCount)}
        </span>
      ),
    },
  ];

  return (
    <DataTable
      columns={columns}
      data={groups}
      keyExtractor={(row) => `${variant}:${row.key}`}
      emptyMessage="No approvals in this breakdown."
    />
  );
}
