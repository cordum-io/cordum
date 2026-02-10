import { useState, useCallback, useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { useRuns } from "../../hooks/useWorkflows";
import { RunStatusBadge } from "../StatusBadge";
import { Button } from "../ui/Button";
import type { RunStatus, WorkflowRun } from "../../api/types";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function timeAgo(iso?: string): string {
  if (!iso) return "\u2014";
  const diff = Date.now() - new Date(iso).getTime();
  const secs = Math.floor(diff / 1_000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(hrs / 24);
  return `${days}d ago`;
}

function formatDuration(ms?: number): string {
  if (ms == null) return "\u2014";
  if (ms < 1_000) return `${ms}ms`;
  const secs = Math.floor(ms / 1_000);
  if (secs < 60) return `${secs}s`;
  const mins = Math.floor(secs / 60);
  const remSecs = secs % 60;
  return `${mins}m ${remSecs}s`;
}

function stepsProgress(run: WorkflowRun): string {
  const completed = run.steps.filter(
    (s) => s.status === "succeeded" || s.status === "completed",
  ).length;
  return `${completed}/${run.steps.length}`;
}

// ---------------------------------------------------------------------------
// Status filter options
// ---------------------------------------------------------------------------

const STATUS_OPTIONS: { label: string; value: string }[] = [
  { label: "All", value: "" },
  { label: "Running", value: "running" },
  { label: "Succeeded", value: "succeeded" },
  { label: "Completed", value: "completed" },
  { label: "Failed", value: "failed" },
  { label: "Cancelled", value: "cancelled" },
  { label: "Pending", value: "pending" },
];

// ---------------------------------------------------------------------------
// Skeleton rows
// ---------------------------------------------------------------------------

function SkeletonRows({ count = 5 }: { count?: number }) {
  return (
    <>
      {Array.from({ length: count }, (_, i) => (
        <tr key={i} className="animate-pulse">
          {Array.from({ length: 5 }, (_, j) => (
            <td key={j} className="px-4 py-3">
              <div className="h-4 rounded bg-surface2 w-3/4" />
            </td>
          ))}
        </tr>
      ))}
    </>
  );
}

// ---------------------------------------------------------------------------
// Pagination
// ---------------------------------------------------------------------------

function Pagination({
  page,
  perPage,
  total,
  onPage,
}: {
  page: number;
  perPage: number;
  total: number;
  onPage: (p: number) => void;
}) {
  const totalPages = Math.max(1, Math.ceil(total / perPage));
  return (
    <div className="flex items-center justify-between border-t border-border px-4 py-3">
      <span className="text-xs text-muted">{total} runs</span>
      <div className="flex items-center gap-1">
        <Button variant="ghost" size="sm" disabled={page <= 1} onClick={() => onPage(page - 1)}>
          Prev
        </Button>
        <span className="px-2 text-xs text-muted">
          {page} / {totalPages}
        </span>
        <Button variant="ghost" size="sm" disabled={page >= totalPages} onClick={() => onPage(page + 1)}>
          Next
        </Button>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// RunHistoryTable
// ---------------------------------------------------------------------------

const PER_PAGE = 10;

export function RunHistoryTable({ workflowId }: { workflowId: string }) {
  const navigate = useNavigate();
  const [statusFilter, setStatusFilter] = useState("");
  const [page, setPage] = useState(1);

  const { data: runs, isLoading, isError } = useRuns(workflowId, { limit: 200 });

  // Client-side filter + paginate
  const filtered = useMemo(() => {
    if (!runs) return [];
    if (!statusFilter) return runs;
    return runs.filter((r) => r.status === statusFilter);
  }, [runs, statusFilter]);

  const paged = useMemo(() => {
    const start = (page - 1) * PER_PAGE;
    return filtered.slice(start, start + PER_PAGE);
  }, [filtered, page]);

  const handleStatusChange = useCallback((value: string) => {
    setStatusFilter(value);
    setPage(1);
  }, []);

  return (
    <div className="space-y-3">
      {/* Filter */}
      <div className="flex items-center gap-2">
        <label className="text-xs font-semibold text-muted">Status:</label>
        <select
          value={statusFilter}
          onChange={(e) => handleStatusChange(e.target.value)}
          className="rounded-lg border border-border bg-transparent px-3 py-1.5 text-xs text-ink"
        >
          {STATUS_OPTIONS.map((opt) => (
            <option key={opt.value} value={opt.value}>
              {opt.label}
            </option>
          ))}
        </select>
      </div>

      {/* Table */}
      <div className="surface-card overflow-hidden rounded-2xl">
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="border-b border-border">
              <tr>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted">
                  Run ID
                </th>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted">
                  Status
                </th>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted">
                  Started
                </th>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted">
                  Duration
                </th>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted">
                  Steps
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {isLoading && <SkeletonRows />}

              {!isLoading && isError && (
                <tr>
                  <td colSpan={5} className="px-4 py-12 text-center text-muted">
                    Failed to load runs.
                  </td>
                </tr>
              )}

              {!isLoading && !isError && filtered.length === 0 && (
                <tr>
                  <td colSpan={5} className="px-4 py-12 text-center text-muted">
                    {statusFilter ? "No runs match this filter." : "No runs yet."}
                  </td>
                </tr>
              )}

              {!isLoading &&
                paged.map((run: WorkflowRun) => (
                  <tr
                    key={run.id}
                    className="cursor-pointer transition-colors hover:bg-surface2/60"
                    onClick={() => navigate(`/workflows/${workflowId}/runs/${run.id}`)}
                  >
                    <td className="px-4 py-3 font-mono text-xs text-ink">
                      {run.id.slice(0, 12)}
                    </td>
                    <td className="px-4 py-3">
                      <RunStatusBadge status={run.status as RunStatus} />
                    </td>
                    <td className="px-4 py-3 text-xs text-muted">
                      {timeAgo(run.startedAt)}
                    </td>
                    <td className="px-4 py-3 text-xs text-muted">
                      {formatDuration(run.duration)}
                    </td>
                    <td className="px-4 py-3 text-xs text-muted">
                      {stepsProgress(run)}
                    </td>
                  </tr>
                ))}
            </tbody>
          </table>
        </div>

        {!isLoading && !isError && filtered.length > PER_PAGE && (
          <Pagination
            page={page}
            perPage={PER_PAGE}
            total={filtered.length}
            onPage={setPage}
          />
        )}
      </div>
    </div>
  );
}
