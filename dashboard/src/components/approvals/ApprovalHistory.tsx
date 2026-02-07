import { useState, useMemo } from "react";
import { Link } from "react-router-dom";
import { useApprovalHistory } from "../../hooks/useApprovals";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { cn } from "../../lib/utils";
import type { Approval } from "../../api/types";

// ---------------------------------------------------------------------------
// Filters
// ---------------------------------------------------------------------------

type ActionFilter = "all" | "approved" | "rejected";
type TimeRange = "1h" | "24h" | "7d" | "30d";

const TIME_LABELS: Record<TimeRange, string> = {
  "1h": "1 hour",
  "24h": "24 hours",
  "7d": "7 days",
  "30d": "30 days",
};

const PER_PAGE = 20;

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function formatTimestamp(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

function isWithinRange(iso: string, range: TimeRange): boolean {
  const ms: Record<TimeRange, number> = {
    "1h": 60 * 60_000,
    "24h": 24 * 60 * 60_000,
    "7d": 7 * 24 * 60 * 60_000,
    "30d": 30 * 24 * 60 * 60_000,
  };
  return Date.now() - new Date(iso).getTime() <= ms[range];
}

function actionBadge(status: string) {
  const lower = status.toLowerCase();
  if (lower.includes("approve")) {
    return <Badge variant="success">Approved</Badge>;
  }
  if (lower.includes("reject")) {
    return <Badge variant="danger">Rejected</Badge>;
  }
  return <Badge variant="default">{status}</Badge>;
}

// ---------------------------------------------------------------------------
// ApprovalHistory
// ---------------------------------------------------------------------------

export function ApprovalHistory() {
  const [actionFilter, setActionFilter] = useState<ActionFilter>("all");
  const [timeRange, setTimeRange] = useState<TimeRange>("7d");
  const [page, setPage] = useState(1);

  const { data, isLoading, isError } = useApprovalHistory({
    page,
    perPage: PER_PAGE,
    sort: "-resolvedAt",
  });

  const allItems = data?.items ?? [];

  // Client-side filters (action type + time range)
  const filtered = useMemo(() => {
    return allItems.filter((item) => {
      // Action filter
      if (actionFilter !== "all") {
        const lower = item.status.toLowerCase();
        if (actionFilter === "approved" && !lower.includes("approve")) return false;
        if (actionFilter === "rejected" && !lower.includes("reject")) return false;
      }
      // Time range filter
      const ts = item.resolvedAt ?? item.requestedAt;
      if (!isWithinRange(ts, timeRange)) return false;
      return true;
    });
  }, [allItems, actionFilter, timeRange]);

  return (
    <div className="space-y-4">
      {/* Filter bar */}
      <div className="flex flex-wrap items-center gap-3">
        {/* Action filter */}
        <div className="flex gap-1 rounded-lg border border-border p-0.5">
          {(["all", "approved", "rejected"] as const).map((action) => (
            <button
              key={action}
              className={cn(
                "rounded-md px-3 py-1.5 text-xs font-medium capitalize transition-colors",
                actionFilter === action
                  ? "bg-accent/10 text-accent"
                  : "text-muted hover:text-ink",
              )}
              onClick={() => { setActionFilter(action); setPage(1); }}
            >
              {action}
            </button>
          ))}
        </div>

        {/* Time range */}
        <div className="flex gap-1 rounded-lg border border-border p-0.5">
          {(["1h", "24h", "7d", "30d"] as const).map((range) => (
            <button
              key={range}
              className={cn(
                "rounded-md px-3 py-1.5 text-xs font-medium transition-colors",
                timeRange === range
                  ? "bg-accent/10 text-accent"
                  : "text-muted hover:text-ink",
              )}
              onClick={() => { setTimeRange(range); setPage(1); }}
            >
              {TIME_LABELS[range]}
            </button>
          ))}
        </div>

        <span className="ml-auto text-xs text-muted">
          {filtered.length} result{filtered.length !== 1 ? "s" : ""}
        </span>
      </div>

      {/* Loading */}
      {isLoading && (
        <div className="space-y-2">
          {Array.from({ length: 5 }, (_, i) => (
            <div key={i} className="h-12 animate-pulse rounded-xl bg-surface2" />
          ))}
        </div>
      )}

      {/* Error */}
      {!isLoading && isError && (
        <p className="py-8 text-center text-sm text-danger">
          Failed to load approval history.
        </p>
      )}

      {/* Empty */}
      {!isLoading && !isError && filtered.length === 0 && (
        <p className="py-12 text-center text-sm text-muted">
          No approval history for the selected filters.
        </p>
      )}

      {/* Table */}
      {!isLoading && !isError && filtered.length > 0 && (
        <div className="overflow-x-auto rounded-2xl border border-border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border bg-surface2/50 text-left">
                <th className="px-4 py-3 font-medium text-muted">Action</th>
                <th className="px-4 py-3 font-medium text-muted">Actor</th>
                <th className="px-4 py-3 font-medium text-muted">Timestamp</th>
                <th className="px-4 py-3 font-medium text-muted">Job</th>
                <th className="px-4 py-3 font-medium text-muted">Reason / Comment</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((item: Approval) => (
                <tr
                  key={item.id}
                  className="border-b border-border last:border-b-0 hover:bg-surface2/30 transition-colors"
                >
                  <td className="px-4 py-3">
                    {actionBadge(item.status)}
                  </td>
                  <td className="px-4 py-3 text-ink">
                    {item.actor ?? <span className="text-muted">—</span>}
                  </td>
                  <td className="px-4 py-3 font-mono text-xs text-muted">
                    {formatTimestamp(item.resolvedAt ?? item.requestedAt)}
                  </td>
                  <td className="px-4 py-3">
                    <Link
                      to={`/jobs/${item.jobId}`}
                      className="font-mono text-accent hover:underline"
                    >
                      {item.jobId.slice(0, 8)}
                    </Link>
                  </td>
                  <td className="max-w-xs truncate px-4 py-3 text-muted">
                    {item.comment ?? item.reason ?? <span className="text-muted">—</span>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* Pagination */}
      {!isLoading && !isError && (
        <div className="flex items-center justify-between">
          <Button
            variant="ghost"
            size="sm"
            disabled={page <= 1}
            onClick={() => setPage((p) => Math.max(1, p - 1))}
          >
            Previous
          </Button>
          <span className="text-xs text-muted">Page {page}</span>
          <Button
            variant="ghost"
            size="sm"
            disabled={allItems.length < PER_PAGE}
            onClick={() => setPage((p) => p + 1)}
          >
            Next
          </Button>
        </div>
      )}
    </div>
  );
}
