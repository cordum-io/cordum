import { useCallback, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { ChevronDown, ChevronUp, ListChecks, Plus } from "lucide-react";
import { useJobs, type JobFilters } from "../hooks/useJobs";
import { JobStatusBadge } from "../components/StatusBadge";
import { JobFiltersBar } from "../components/jobs/JobFiltersBar";
import { JobSubmitDrawer } from "../components/jobs/JobSubmitDrawer";
import { Badge } from "../components/ui/Badge";
import { Button } from "../components/ui/Button";
import { cn } from "../lib/utils";
import { TableEmptyState } from "../components/ui/EmptyState";
import { SkeletonRow } from "../components/ui/Skeleton";
import type { Job, SafetyDecision } from "../api/types";
import { DataFreshness } from "../components/ui/DataFreshness";
import { usePageTitle } from "../hooks/usePageTitle";
import { useToastStore } from "../state/toast";

// ---------------------------------------------------------------------------
// Safety decision badge
// ---------------------------------------------------------------------------

const decisionVariant: Record<string, "success" | "danger" | "warning" | "info" | "default"> = {
  allow: "success",
  deny: "danger",
  require_approval: "warning",
  throttle: "info",
};

const decisionLabel: Record<string, string> = {
  allow: "Allow",
  deny: "Deny",
  require_approval: "Approval",
  throttle: "Throttle",
};

function SafetyBadge({ decision }: { decision?: SafetyDecision }) {
  if (!decision) return <span className="text-xs text-muted">&mdash;</span>;
  return (
    <Badge variant={decisionVariant[decision.type] ?? "default"}>
      {decisionLabel[decision.type] ?? decision.type}
    </Badge>
  );
}

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
      className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted"
      aria-sort={ariaSort as "ascending" | "descending" | "none"}
    >
      <button
        type="button"
        className="inline-flex items-center gap-1 select-none hover:text-ink transition-colors"
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
}: {
  canPrev: boolean;
  canNext: boolean;
  onPrev: () => void;
  onNext: () => void;
  limit: number;
  onLimit: (limit: number) => void;
}) {
  return (
    <div className="flex items-center justify-between border-t border-border px-4 py-3">
      <div className="flex items-center gap-2 text-xs text-muted">
        <span>Rows:</span>
        <select
          value={limit}
          onChange={(e) => onLimit(Number(e.target.value))}
          className="rounded border border-border bg-transparent px-2 py-1 text-xs text-ink"
        >
          {[10, 25, 50, 100].map((v) => (
            <option key={v} value={v}>
              {v}
            </option>
          ))}
        </select>
      </div>
      <div className="flex items-center gap-1">
        <Button variant="ghost" size="sm" disabled={!canPrev} onClick={onPrev}>
          Newer
        </Button>
        <Button variant="ghost" size="sm" disabled={!canNext} onClick={onNext}>
          Older
        </Button>
      </div>
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

  const { data, isLoading, isError, dataUpdatedAt, refetch, isRefetching } = useJobs({ ...filters, limit, cursor });

  const rawJobs = data?.items ?? [];
  const jobs = useMemo(() => sortJobs(rawJobs, sortKey, sortDir), [rawJobs, sortKey, sortDir]);
  const nextCursor = data?.next_cursor ?? null;

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
      <div className="flex items-center justify-between">
        <h1 className="font-display text-2xl font-bold text-ink">Jobs</h1>
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

      <div className="surface-card overflow-hidden rounded-2xl">
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="border-b border-border">
              <tr>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted">
                  ID
                </th>
                <SortableHeader label="Topic" sortKey="topic" activeKey={sortKey} activeDir={sortDir} onSort={handleSort} />
                <SortableHeader label="State" sortKey="state" activeKey={sortKey} activeDir={sortDir} onSort={handleSort} />
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted">
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
                  <td colSpan={7} className="px-4 py-12 text-center text-muted">
                    Failed to load jobs. Please try again.
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
                jobs.map((job: Job) => (
                  <tr
                    key={job.id}
                    className={cn(
                      "cursor-pointer transition-colors hover:bg-surface2/60",
                    )}
                    onClick={() => navigate(`/jobs/${job.id}`)}
                  >
                    <td className="px-4 py-3 font-mono text-xs text-ink">
                      {job.id.slice(0, 8)}
                    </td>
                    <td className="px-4 py-3 text-ink">
                      {job.topic || job.type}
                    </td>
                    <td className="px-4 py-3">
                      <JobStatusBadge state={job.status} />
                    </td>
                    <td className="px-4 py-3">
                      <SafetyBadge decision={job.safetyDecision} />
                    </td>
                    <td className="px-4 py-3 text-xs text-muted">
                      {job.pool || "\u2014"}
                    </td>
                    <td className="px-4 py-3 font-mono text-xs text-muted">
                      {formatDuration(job.duration)}
                    </td>
                    <td className="px-4 py-3 text-xs text-muted">
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
