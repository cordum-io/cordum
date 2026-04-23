import { useCallback, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { ChevronDown, ChevronUp, ListChecks, Plus } from "lucide-react";
import { useJobs, type JobFilters } from "../hooks/useJobs";
import { JobStatusBadge } from "../components/StatusBadge";
import { JobFiltersBar } from "../components/jobs/JobFiltersBar";
import { JobDecisionBadge } from "../components/jobs/JobDecisionBadge";
import { JobSubmitDrawer } from "../components/jobs/JobSubmitDrawer";
import { Badge } from "../components/ui/Badge";
import { Button } from "../components/ui/Button";
import { cn } from "../lib/utils";
import { TableEmptyState } from "../components/ui/EmptyState";
import { SkeletonRow } from "../components/ui/Skeleton";
import type { Job } from "../api/types";
import { DataFreshness } from "../components/ui/DataFreshness";
import { usePageTitle } from "../hooks/usePageTitle";
import { useToastStore } from "../state/toast";

// ---------------------------------------------------------------------------
// Duration formatter
// ---------------------------------------------------------------------------

function formatDuration(ms?: number): string {
  if (ms == null) return "\u2014";
  if (ms < 1_000) return `${ms}ms`;
  const s = ms / 1_000;
  if (s < 60) return `${s.toFixed(1)}s`;
  const m = Math.floor(s / 60);
  const rem = Math.round(s % 60);
  return `${m}m ${rem}s`;
}

// ---------------------------------------------------------------------------
// Relative time
// ---------------------------------------------------------------------------

function timeAgo(iso: string): string {
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

// ---------------------------------------------------------------------------
// Sortable header
// ---------------------------------------------------------------------------

type SortKey = "topic" | "state" | "pool" | "duration" | "updatedAt";
type SortDir = "asc" | "desc";

function SortableHeader({
  label,
  sortKey,
  activeKey,
  activeDir,
  onSort,
}: {
  label: string;
  sortKey: SortKey;
  activeKey: SortKey;
  activeDir: SortDir;
  onSort: (key: SortKey) => void;
}) {
  const isActive = activeKey === sortKey;
  const ariaSort = isActive ? (activeDir === "asc" ? "ascending" : "descending") : "none";
  return (
    <th
      className="px-4 py-2.5 text-left text-[10px] font-semibold uppercase tracking-[0.14em] text-muted"
      aria-sort={ariaSort as "ascending" | "descending" | "none"}
    >
      <button
        type="button"
        className="inline-flex items-center gap-1 select-none transition-colors hover:text-ink focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/35 focus-visible:ring-offset-1 focus-visible:ring-offset-surface-0"
        onClick={() => onSort(sortKey)}
      >
        {label}
        {isActive ? (
          activeDir === "asc" ? (
            <ChevronUp className="h-3 w-3" />
          ) : (
            <ChevronDown className="h-3 w-3" />
          )
        ) : (
          <ChevronDown className="h-3 w-3 opacity-0 group-hover:opacity-30" />
        )}
      </button>
    </th>
  );
}

const statusOrder: Record<string, number> = {
  pending: 0,
  dispatched: 1,
  running: 2,
  succeeded: 3,
  failed: 4,
  denied: 5,
  cancelled: 6,
};

function sortJobs(jobs: Job[], key: SortKey, dir: SortDir): Job[] {
  const sorted = [...jobs].sort((a, b) => {
    let cmp = 0;
    switch (key) {
      case "topic":
        cmp = (a.topic || a.type || "").localeCompare(b.topic || b.type || "");
        break;
      case "state":
        cmp = (statusOrder[a.status] ?? 99) - (statusOrder[b.status] ?? 99);
        break;
      case "pool":
        cmp = (a.pool || "").localeCompare(b.pool || "");
        break;
      case "duration":
        cmp = (a.duration ?? 0) - (b.duration ?? 0);
        break;
      case "updatedAt":
        cmp =
          new Date(a.updatedAt || 0).getTime() -
          new Date(b.updatedAt || 0).getTime();
        break;
    }
    return cmp;
  });
  return dir === "desc" ? sorted.reverse() : sorted;
}

// ---------------------------------------------------------------------------
// Pagination
// ---------------------------------------------------------------------------

function Pagination({
  canPrev,
  canNext,
  onPrev,
  onNext,
  limit,
  onLimit,
  visibleCount,
  isRefreshing,
}: {
  canPrev: boolean;
  canNext: boolean;
  onPrev: () => void;
  onNext: () => void;
  limit: number;
  onLimit: (limit: number) => void;
  visibleCount: number;
  isRefreshing: boolean;
}) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-2 border-t border-border/80 bg-surface-1/40 px-4 py-2.5">
      <div className="flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
        <span className="font-medium text-foreground">Showing {visibleCount}</span>
        <span aria-hidden>&middot;</span>
        <span>Rows</span>
        <select
          value={limit}
          onChange={(e) => onLimit(Number(e.target.value))}
          aria-label="Rows per page"
          className="h-7 rounded-md border border-border bg-surface-1 px-2 font-mono text-[11px] text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/35 focus-visible:ring-offset-1 focus-visible:ring-offset-surface-0"
        >
          <div className="overflow-x-auto">
          <table className="w-full min-w-[800px]">
            <thead>
              <tr className="border-b border-border bg-surface-0">
                <th
                  className="text-left px-5 py-2.5 text-[10px] font-mono font-medium text-muted-foreground uppercase tracking-widest cursor-pointer select-none hover:text-foreground transition-colors"
                  onClick={() => toggleSort("status")}
                >
                  <span className="inline-flex items-center">Status <SortIcon col="status" /></span>
                </th>
                <th
                  className="text-left px-5 py-2.5 text-[10px] font-mono font-medium text-muted-foreground uppercase tracking-widest cursor-pointer select-none hover:text-foreground transition-colors"
                  onClick={() => toggleSort("id")}
                >
                  <span className="inline-flex items-center">Job ID <SortIcon col="id" /></span>
                </th>
                <th
                  className="text-left px-5 py-2.5 text-[10px] font-mono font-medium text-muted-foreground uppercase tracking-widest cursor-pointer select-none hover:text-foreground transition-colors"
                  onClick={() => toggleSort("topic")}
                >
                  <span className="inline-flex items-center">Topic <SortIcon col="topic" /></span>
                </th>
                <th
                  className="text-left px-5 py-2.5 text-[10px] font-mono font-medium text-muted-foreground uppercase tracking-widest cursor-pointer select-none hover:text-foreground transition-colors"
                  onClick={() => toggleSort("safety")}
                >
                  <span className="inline-flex items-center">Safety Decision <SortIcon col="safety" /></span>
                </th>
                <th
                  className="text-center px-5 py-2.5 text-[10px] font-mono font-medium text-muted-foreground uppercase tracking-widest cursor-pointer select-none hover:text-foreground transition-colors"
                  onClick={() => toggleSort("attempts")}
                >
                  <span className="inline-flex items-center justify-center">Attempts <SortIcon col="attempts" /></span>
                </th>
                <th
                  className="text-right px-5 py-2.5 text-[10px] font-mono font-medium text-muted-foreground uppercase tracking-widest cursor-pointer select-none hover:text-foreground transition-colors"
                  onClick={() => toggleSort("updatedAt")}
                >
                  <span className="inline-flex items-center justify-end">Updated <SortIcon col="updatedAt" /></span>
                </th>
                <th className="px-5 py-2.5"></th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((job) => (
                <tr
                  key={job.id}
                  {...clickableRowProps(() => navigate(`/jobs/${job.id}`))}
                  className="border-b border-border hover:bg-surface-1 transition-colors cursor-pointer group"
                >
                  <td className="px-5 py-2.5">
                    <StatusBadge variant={jobStatusVariant(job.status)} dot pulse={job.status === "running"}>
                      {job.status}
                    </StatusBadge>
                  </td>
                  <td className="px-5 py-2.5 font-mono text-sm text-cordum group-hover:underline">{job.id.slice(0, 16)}</td>
                  <td className="px-5 py-2.5 text-sm text-foreground">{job.topic || "—"}</td>
                  <td className="px-5 py-2.5">
                    <SafetyDecisionBadge decision={job._safetyDecision} matchedRules={job._matchedRules} />
                  </td>
                  <td className="px-5 py-2.5 text-center font-mono text-xs text-muted-foreground">{job.attempts ?? 0}</td>
                  <td className="px-5 py-2.5 text-right text-xs text-muted-foreground font-mono">
                    {job.updatedAt ? formatRelativeTime(new Date(job.updatedAt).toISOString()) : "—"}
                  </td>
                  <td className="px-5 py-2.5">
                    <button className="p-1 rounded hover:bg-surface-2 transition-colors" aria-label="View details">
                      <Eye className="w-3.5 h-3.5 text-muted-foreground" />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          </div>
          <div className="flex items-center justify-between px-5 py-2.5 border-t border-border bg-surface-0">
            <span className="text-xs font-mono text-muted-foreground">
              Showing {filtered.length} of {enrichedJobs.length} jobs
            </span>
            <span className="text-[10px] font-mono text-muted-foreground">
              Sorted by {sortKey} ({sortDir})
            </span>
          </div>
        </motion.div>
      )}

      <SubmitJobDialog open={showSubmit} onClose={() => setShowSubmit(false)} />
    </div>
  );
}

// ---------------------------------------------------------------------------
// JobsPage
// ---------------------------------------------------------------------------

export default function JobsPage() {
  usePageTitle("Jobs");
  const navigate = useNavigate();
  const addToast = useToastStore((s) => s.addToast);
  const [limit, setLimit] = useState(25);
  const [cursor, setCursor] = useState<number | undefined>(undefined);
  const [cursorStack, setCursorStack] = useState<number[]>([]);
  const [filters, setFilters] = useState<JobFilters>({ limit });
  const [showSubmitDrawer, setShowSubmitDrawer] = useState(false);

  const [sortKey, setSortKey] = useState<SortKey>("updatedAt");
  const [sortDir, setSortDir] = useState<SortDir>("desc");

  const { data, isLoading, isError, error, dataUpdatedAt, refetch, isRefetching } = useJobs({ ...filters, limit, cursor });

  const rawJobs = data?.items ?? [];
  const jobs = useMemo(() => sortJobs(rawJobs, sortKey, sortDir), [rawJobs, sortKey, sortDir]);
  const nextCursor = data?.next_cursor ?? null;
  const jobsErrorMessage = useMemo(() => {
    if (!isError) return "";
    const message = String((error as { message?: string } | null)?.message ?? "").toLowerCase();
    if (message.includes("timeout")) return "Jobs API timed out. Retry to refresh data.";
    if (message.includes("network")) return "Unable to reach jobs API. Check connectivity and retry.";
    return "Failed to load jobs. Retry to refresh data.";
  }, [error, isError]);

  const handleSort = useCallback((key: SortKey) => {
    setSortKey((prev) => {
      if (prev === key) {
        setSortDir((d) => (d === "asc" ? "desc" : "asc"));
        return key;
      }
      setSortDir(key === "updatedAt" || key === "duration" ? "desc" : "asc");
      return key;
    });
  }, []);

  const handleNext = useCallback(() => {
    if (!nextCursor) return;
    setCursorStack((prev) => [...prev, cursor ?? 0]);
    setCursor(nextCursor);
  }, [nextCursor, cursor]);

  const handlePrev = useCallback(() => {
    setCursorStack((prev) => {
      if (prev.length === 0) return prev;
      const next = [...prev];
      const last = next.pop();
      setCursor(last && last > 0 ? last : undefined);
      return next;
    });
  }, []);

  const handleLimit = useCallback((value: number) => {
    setLimit(value);
    setCursor(undefined);
    setCursorStack([]);
  }, []);

  const handleSubmitSuccess = useCallback((result: { job_id: string }) => {
    addToast({
      type: "success",
      title: "Job submitted",
      description: result.job_id,
    });
    setShowSubmitDrawer(false);
    navigate(`/jobs/${result.job_id}`);
  }, [addToast, navigate]);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-border bg-surface-1/60 px-4 py-3">
        <div className="flex items-center gap-2">
          <h1 className="font-display text-2xl font-semibold text-foreground">Jobs</h1>
          {!isLoading && !isError && (
            <Badge variant="info" className="px-2 py-0.5 text-[10px]">
              {jobs.length} visible
            </Badge>
          )}
        </div>
        <div className="flex items-center gap-2">
          <DataFreshness dataUpdatedAt={dataUpdatedAt} onRefresh={refetch} isRefetching={isRefetching} />
          <Button size="sm" onClick={() => setShowSubmitDrawer(true)}>
            <Plus className="h-3.5 w-3.5" />
            New Job
          </Button>
        </div>
      </div>

      <JobFiltersBar
        onChange={(vals) => {
          const { updatedAfter, updatedBefore, ...rest } = vals;
          setFilters((prev) => ({
            ...prev,
            ...rest,
            updatedAfter: updatedAfter ? new Date(updatedAfter).getTime() : undefined,
            updatedBefore: updatedBefore ? new Date(updatedBefore).getTime() : undefined,
          }));
          setCursor(undefined);
          setCursorStack([]);
        }}
      />

      <div className="surface-card overflow-hidden rounded-lg border border-border/80 bg-surface-1/55">
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="border-b border-border">
              <tr>
                <th className="px-4 py-2.5 text-left text-[10px] font-semibold uppercase tracking-[0.14em] text-muted">
                  ID
                </th>
                <SortableHeader label="Topic" sortKey="topic" activeKey={sortKey} activeDir={sortDir} onSort={handleSort} />
                <SortableHeader label="State" sortKey="state" activeKey={sortKey} activeDir={sortDir} onSort={handleSort} />
                <th className="px-4 py-2.5 text-left text-[10px] font-semibold uppercase tracking-[0.14em] text-muted">
                  Safety Decision
                </th>
                <SortableHeader label="Pool" sortKey="pool" activeKey={sortKey} activeDir={sortDir} onSort={handleSort} />
                <SortableHeader label="Duration" sortKey="duration" activeKey={sortKey} activeDir={sortDir} onSort={handleSort} />
                <SortableHeader label="Updated" sortKey="updatedAt" activeKey={sortKey} activeDir={sortDir} onSort={handleSort} />
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {isLoading && Array.from({ length: 8 }, (_, i) => <SkeletonRow key={i} columns={7} />)}

              {!isLoading && isError && (
                <tr>
                  <td colSpan={7} className="px-4 py-8 text-center">
                    <div className="flex flex-col items-center gap-2">
                      <p className="text-sm text-warning">{jobsErrorMessage}</p>
                      <Button variant="outline" size="sm" onClick={() => refetch()}>
                        Retry
                      </Button>
                    </div>
                  </td>
                </tr>
              )}

              {!isLoading && !isError && jobs.length === 0 && (
                <TableEmptyState
                  colSpan={7}
                  icon={ListChecks}
                  title="No jobs found"
                  description="Try adjusting your filters or check back later."
                />
              )}

              {!isLoading &&
                !isError &&
                jobs.map((job: Job) => (
                  <tr
                    key={job.id}
                    className={cn(
                      "h-12 cursor-pointer border-l border-transparent transition-colors hover:bg-surface2/60 focus-visible:bg-surface2/65 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/35",
                    )}
                    onClick={() => navigate(`/jobs/${job.id}`)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter" || event.key === " ") {
                        event.preventDefault();
                        navigate(`/jobs/${job.id}`);
                      }
                    }}
                    tabIndex={0}
                    role="button"
                    aria-label={`View job ${job.id}`}
                  >
                    <td className="px-4 py-3 font-mono text-[11px] text-foreground" title={job.id}>
                      {job.id.slice(0, 12)}
                    </td>
                    <td className="px-4 py-3 text-[13px] text-foreground">
                      {job.topic || job.type}
                    </td>
                    <td className="px-4 py-3">
                      <JobStatusBadge state={job.status} />
                    </td>
                    <td className="px-4 py-3">
                      <JobDecisionBadge decision={job.safetyDecision?.type} />
                    </td>
                    <td className="px-4 py-3 text-[11px] text-muted-foreground">
                      {job.pool || "\u2014"}
                    </td>
                    <td className="px-4 py-3 font-mono text-[11px] text-muted-foreground">
                      {formatDuration(job.duration)}
                    </td>
                    <td className="px-4 py-3 font-mono text-[11px] text-muted-foreground">
                      {job.updatedAt ? timeAgo(job.updatedAt) : "\u2014"}
                    </td>
                  </tr>
                ))}
            </tbody>
          </table>
        </div>

        {!isLoading && !isError && (
          <Pagination
            canPrev={cursorStack.length > 0}
            canNext={!!nextCursor}
            onPrev={handlePrev}
            onNext={handleNext}
            limit={limit}
            onLimit={handleLimit}
            visibleCount={jobs.length}
            isRefreshing={isRefetching}
          />
        )}
      </div>

      <JobSubmitDrawer
        open={showSubmitDrawer}
        onClose={() => setShowSubmitDrawer(false)}
        onSuccess={handleSubmitSuccess}
      />
    </div>
  );
}
