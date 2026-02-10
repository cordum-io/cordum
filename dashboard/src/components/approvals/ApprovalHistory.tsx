import { useState, useMemo, useCallback } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { Download } from "lucide-react";
import { useApprovalHistory } from "../../hooks/useApprovals";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { Input } from "../ui/Input";
import { Select } from "../ui/Select";
import { cn } from "../../lib/utils";
import { useConfigStore } from "../../state/config";
import type { ApprovalHistoryEntry } from "../../api/types";

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

type ActionFilter = "all" | "approved" | "rejected";
type TimeRange = "1h" | "24h" | "7d" | "30d";

const TIME_LABELS: Record<TimeRange, string> = {
  "1h": "1h",
  "24h": "24h",
  "7d": "7d",
  "30d": "30d",
};

const TIME_MS: Record<TimeRange, number> = {
  "1h": 60 * 60_000,
  "24h": 24 * 60 * 60_000,
  "7d": 7 * 24 * 60 * 60_000,
  "30d": 30 * 24 * 60 * 60_000,
};

const PER_PAGE = 20;
// SLA threshold read from config store in MetricsStrip

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

function formatDuration(ms: number | undefined): string {
  if (ms == null || ms <= 0) return "—";
  const secs = Math.floor(ms / 1_000);
  if (secs < 60) return `${secs}s`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ${secs % 60}s`;
  const hrs = Math.floor(mins / 60);
  return `${hrs}h ${mins % 60}m`;
}

function actionBadge(action: string) {
  if (action.includes("approve")) return <Badge variant="success">Approved</Badge>;
  if (action.includes("reject")) return <Badge variant="danger">Rejected</Badge>;
  return <Badge variant="default">{action}</Badge>;
}

// ---------------------------------------------------------------------------
// Metrics strip
// ---------------------------------------------------------------------------

function MetricsStrip({ items }: { items: ApprovalHistoryEntry[] }) {
  const slaMs = useConfigStore((s) => s.approvalSlaMs);
  const stats = useMemo(() => {
    const total = items.length;
    if (total === 0) return null;

    const approved = items.filter((i) => i.action === "approve").length;
    const approvalRate = Math.round((approved / total) * 100);

    // Avg wait (only items with waitDurationMs)
    const withWait = items.filter((i) => i.waitDurationMs != null && i.waitDurationMs > 0);
    const avgWaitMs = withWait.length > 0
      ? Math.round(withWait.reduce((s, i) => s + (i.waitDurationMs ?? 0), 0) / withWait.length)
      : 0;

    // SLA compliance
    const slaCompliant = withWait.filter((i) => (i.waitDurationMs ?? 0) <= slaMs).length;
    const slaRate = withWait.length > 0 ? Math.round((slaCompliant / withWait.length) * 100) : 100;

    // Top approvers
    const actorCounts = new Map<string, number>();
    for (const item of items) {
      actorCounts.set(item.actor, (actorCounts.get(item.actor) ?? 0) + 1);
    }
    const topApprovers = [...actorCounts.entries()]
      .sort((a, b) => b[1] - a[1])
      .slice(0, 3)
      .map(([name, count]) => `${name} (${count})`)
      .join(", ");

    return { total, approvalRate, avgWaitMs, slaRate, topApprovers };
  }, [items, slaMs]);

  if (!stats) return null;

  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted">
      <span>
        <span className="font-semibold text-ink">{stats.total}</span> decisions
      </span>
      <span aria-hidden>&middot;</span>
      <span>
        <span className={cn("font-semibold", stats.approvalRate >= 50 ? "text-success" : "text-danger")}>
          {stats.approvalRate}%
        </span>{" "}
        approved
      </span>
      <span aria-hidden>&middot;</span>
      <span>
        avg wait{" "}
        <span className="font-semibold text-ink">{formatDuration(stats.avgWaitMs)}</span>
      </span>
      <span aria-hidden>&middot;</span>
      <span>
        SLA{" "}
        <span className={cn("font-semibold", stats.slaRate >= 90 ? "text-success" : stats.slaRate >= 70 ? "text-warning" : "text-danger")}>
          {stats.slaRate}%
        </span>
      </span>
      {stats.topApprovers && (
        <>
          <span aria-hidden>&middot;</span>
          <span className="truncate max-w-xs" title={stats.topApprovers}>
            Top: {stats.topApprovers}
          </span>
        </>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// CSV export
// ---------------------------------------------------------------------------

function exportCsv(items: ApprovalHistoryEntry[]) {
  const header = "Decision,Job ID,Topic,Actor,Wait Duration (s),Decided At,Reason/Comment";
  const rows = items.map((i) => {
    const decision = i.action === "approve" ? "Approved" : "Rejected";
    const waitSecs = i.waitDurationMs != null ? Math.round(i.waitDurationMs / 1000) : "";
    const reason = (i.reason ?? "").replace(/"/g, '""');
    const topic = (i.topic ?? "").replace(/"/g, '""');
    return `${decision},${i.jobId},"${topic}",${i.actor},${waitSecs},${i.timestamp},"${reason}"`;
  });
  const csv = [header, ...rows].join("\n");
  const blob = new Blob([csv], { type: "text/csv;charset=utf-8;" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `approvals-history-${new Date().toISOString().slice(0, 10)}.csv`;
  a.click();
  URL.revokeObjectURL(url);
}

// ---------------------------------------------------------------------------
// ApprovalHistory
// ---------------------------------------------------------------------------

export function ApprovalHistory() {
  const [actionFilter, setActionFilter] = useState<ActionFilter>("all");
  const [timeRange, setTimeRange] = useState<TimeRange>("7d");
  const [search, setSearch] = useState("");
  const [actorFilter, setActorFilter] = useState("");
  const [workflowFilter, setWorkflowFilter] = useState("");
  const [searchParams, setSearchParams] = useSearchParams();
  const page = Math.max(0, parseInt(searchParams.get("page") ?? "0", 10) || 0);
  const setPage = useCallback(
    (updater: number | ((prev: number) => number)) => {
      setSearchParams((prev) => {
        const next = new URLSearchParams(prev);
        const newPage = typeof updater === "function" ? updater(page) : updater;
        if (newPage > 0) next.set("page", String(newPage));
        else next.delete("page");
        return next;
      }, { replace: true });
    },
    [page, setSearchParams],
  );

  const { data, isLoading, isError } = useApprovalHistory();
  const allItems = data?.items ?? [];

  // Unique actors for dropdown
  const uniqueActors = useMemo(() => {
    const set = new Set(allItems.map((i) => i.actor));
    return [...set].sort();
  }, [allItems]);

  // Unique workflows for dropdown
  const uniqueWorkflows = useMemo(() => {
    const set = new Set(allItems.map((i) => i.workflowId).filter(Boolean) as string[]);
    return [...set].sort();
  }, [allItems]);

  // Client-side filtering
  const filtered = useMemo(() => {
    const cutoff = Date.now() - TIME_MS[timeRange];
    const searchLower = search.toLowerCase();

    return allItems.filter((item) => {
      if (actionFilter === "approved" && item.action !== "approve") return false;
      if (actionFilter === "rejected" && item.action !== "reject") return false;
      if (new Date(item.timestamp).getTime() < cutoff) return false;
      if (actorFilter && item.actor !== actorFilter) return false;
      if (workflowFilter && item.workflowId !== workflowFilter) return false;
      if (searchLower) {
        const haystack = [item.jobId, item.reason ?? "", item.actor, item.topic ?? ""]
          .join(" ")
          .toLowerCase();
        if (!haystack.includes(searchLower)) return false;
      }
      return true;
    });
  }, [allItems, actionFilter, timeRange, actorFilter, workflowFilter, search]);

  // Pagination
  const totalPages = Math.max(1, Math.ceil(filtered.length / PER_PAGE));
  const paged = useMemo(
    () => filtered.slice(page * PER_PAGE, (page + 1) * PER_PAGE),
    [filtered, page],
  );

  const resetFilters = useCallback(() => {
    setActionFilter("all");
    setTimeRange("7d");
    setSearch("");
    setActorFilter("");
    setWorkflowFilter("");
    setPage(0);
  }, [setPage]);

  return (
    <div className="space-y-4">
      {/* Metrics strip */}
      <MetricsStrip items={filtered} />

      {/* Filters */}
      <div className="flex flex-wrap items-center gap-3">
        {/* Action filter */}
        <div className="flex gap-0.5 rounded-lg border border-border p-0.5">
          {(["all", "approved", "rejected"] as const).map((action) => (
            <button
              key={action}
              type="button"
              className={cn(
                "rounded-md px-3 py-1.5 text-xs font-medium capitalize transition-colors",
                actionFilter === action
                  ? "bg-accent/10 text-accent"
                  : "text-muted hover:text-ink",
              )}
              onClick={() => { setActionFilter(action); setPage(0); }}
            >
              {action}
            </button>
          ))}
        </div>

        {/* Time range */}
        <div className="flex gap-0.5 rounded-lg border border-border p-0.5">
          {(["1h", "24h", "7d", "30d"] as const).map((range) => (
            <button
              key={range}
              type="button"
              className={cn(
                "rounded-md px-3 py-1.5 text-xs font-medium transition-colors",
                timeRange === range
                  ? "bg-accent/10 text-accent"
                  : "text-muted hover:text-ink",
              )}
              onClick={() => { setTimeRange(range); setPage(0); }}
            >
              {TIME_LABELS[range]}
            </button>
          ))}
        </div>

        {/* Actor filter */}
        {uniqueActors.length > 1 && (
          <Select
            className="h-8 w-36 text-xs"
            value={actorFilter}
            onChange={(e) => { setActorFilter(e.target.value); setPage(0); }}
          >
            <option value="">All actors</option>
            {uniqueActors.map((a) => (
              <option key={a} value={a}>{a}</option>
            ))}
          </Select>
        )}

        {/* Workflow filter */}
        {uniqueWorkflows.length > 0 && (
          <Select
            className="h-8 w-36 text-xs"
            value={workflowFilter}
            onChange={(e) => { setWorkflowFilter(e.target.value); setPage(0); }}
          >
            <option value="">All workflows</option>
            {uniqueWorkflows.map((w) => (
              <option key={w} value={w}>{w.slice(0, 20)}</option>
            ))}
          </Select>
        )}

        {/* Search */}
        <Input
          className="h-8 w-48 text-xs"
          placeholder="Search..."
          value={search}
          onChange={(e) => { setSearch(e.target.value); setPage(0); }}
        />

        <div className="ml-auto flex items-center gap-2">
          <span className="text-xs text-muted">
            {filtered.length} result{filtered.length !== 1 ? "s" : ""}
          </span>
          {filtered.length > 0 && (
            <Button
              variant="outline"
              size="sm"
              onClick={() => exportCsv(filtered)}
            >
              <Download className="mr-1 h-3 w-3" />
              CSV
            </Button>
          )}
        </div>
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
      {!isLoading && !isError && paged.length > 0 && (
        <div className="overflow-x-auto rounded-2xl border border-border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border bg-surface2/50 text-left">
                <th className="px-4 py-3 font-medium text-muted">Decision</th>
                <th className="px-4 py-3 font-medium text-muted">Decided by</th>
                <th className="px-4 py-3 font-medium text-muted">Wait</th>
                <th className="px-4 py-3 font-medium text-muted">Decision time</th>
                <th className="px-4 py-3 font-medium text-muted">Job</th>
                <th className="px-4 py-3 font-medium text-muted">Workflow</th>
                <th className="px-4 py-3 font-medium text-muted">Reason</th>
              </tr>
            </thead>
            <tbody>
              {paged.map((item: ApprovalHistoryEntry) => (
                <tr
                  key={item.id}
                  className="border-b border-border last:border-b-0 hover:bg-surface2/30 transition-colors"
                >
                  <td className="px-4 py-3">{actionBadge(item.action)}</td>
                  <td className="px-4 py-3 text-xs text-ink">{item.actor}</td>
                  <td className="px-4 py-3 font-mono text-xs text-muted">
                    {formatDuration(item.waitDurationMs)}
                  </td>
                  <td className="px-4 py-3 font-mono text-xs text-muted">
                    {formatTimestamp(item.timestamp)}
                  </td>
                  <td className="px-4 py-3">
                    <Link
                      to={`/jobs/${item.jobId}`}
                      className="font-mono text-xs text-accent hover:underline"
                    >
                      {item.jobId.slice(0, 8)}
                    </Link>
                  </td>
                  <td className="px-4 py-3 text-xs">
                    {item.workflowId ? (
                      <Link
                        to={`/workflows/${item.workflowId}`}
                        className="text-accent hover:underline"
                      >
                        {item.workflowId.slice(0, 8)}
                      </Link>
                    ) : (
                      <span className="text-muted">—</span>
                    )}
                  </td>
                  <td className="max-w-xs truncate px-4 py-3 text-xs text-muted" title={item.reason ?? ""}>
                    {item.reason ?? "—"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* Pagination */}
      {!isLoading && !isError && filtered.length > PER_PAGE && (
        <div className="flex items-center justify-between">
          <Button
            variant="ghost"
            size="sm"
            disabled={page === 0}
            onClick={() => setPage((p) => Math.max(0, p - 1))}
          >
            Previous
          </Button>
          <span className="text-xs text-muted">
            Page {page + 1} of {totalPages}
          </span>
          <Button
            variant="ghost"
            size="sm"
            disabled={page >= totalPages - 1}
            onClick={() => setPage((p) => p + 1)}
          >
            Next
          </Button>
        </div>
      )}
    </div>
  );
}
