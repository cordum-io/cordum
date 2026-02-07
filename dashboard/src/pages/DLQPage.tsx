import { useCallback, useEffect, useMemo, useState } from "react";
import { ChevronDown, ChevronRight, RefreshCw, Trash2, X } from "lucide-react";
import { useDLQ, useRetryDLQ, useDeleteDLQ } from "../hooks/useDLQ";
import { Button } from "../components/ui/Button";
import { Badge } from "../components/ui/Badge";
import { Input } from "../components/ui/Input";
import { cn } from "../lib/utils";
import { DLQRowActions } from "../components/dlq/DLQActions";
import type { DLQEntry, RetryAttempt } from "../api/types";

// ---------------------------------------------------------------------------
// Debounce hook
// ---------------------------------------------------------------------------

function useDebouncedValue(value: string, delayMs: number): string {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const id = setTimeout(() => setDebounced(value), delayMs);
    return () => clearTimeout(id);
  }, [value, delayMs]);
  return debounced;
}

// ---------------------------------------------------------------------------
// Time range presets
// ---------------------------------------------------------------------------

const TIME_PRESETS = [
  { label: "1h", value: "1h" },
  { label: "24h", value: "24h" },
  { label: "7d", value: "7d" },
  { label: "30d", value: "30d" },
  { label: "All", value: "" },
] as const;

const SINCE_MS: Record<string, number> = {
  "1h": 60 * 60 * 1000,
  "24h": 24 * 60 * 60 * 1000,
  "7d": 7 * 24 * 60 * 60 * 1000,
  "30d": 30 * 24 * 60 * 60 * 1000,
};

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
// Skeleton rows
// ---------------------------------------------------------------------------

function SkeletonRows({ count = 8 }: { count?: number }) {
  return (
    <>
      {Array.from({ length: count }, (_, i) => (
        <tr key={i} className="animate-pulse">
          {Array.from({ length: 7 }, (_, j) => (
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
// Retry attempts panel
// ---------------------------------------------------------------------------

function RetryAttemptsPanel({ attempts }: { attempts: RetryAttempt[] }) {
  if (attempts.length === 0) {
    return (
      <p className="px-4 py-3 text-xs text-muted">No retry attempts recorded.</p>
    );
  }
  return (
    <div className="space-y-2 px-4 py-3">
      <h4 className="text-[11px] font-semibold uppercase tracking-wider text-muted">
        Retry Attempts ({attempts.length})
      </h4>
      <div className="space-y-1.5">
        {attempts.map((a, i) => (
          <div
            key={i}
            className="flex items-start gap-3 rounded-xl bg-surface2/40 px-3 py-2 text-xs"
          >
            <span className="shrink-0 font-mono text-muted">#{i + 1}</span>
            <div className="min-w-0 flex-1">
              <span className="text-danger font-medium break-words">
                {a.error}
              </span>
            </div>
            <span className="shrink-0 text-muted">{timeAgo(a.attemptedAt)}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Batch actions toolbar
// ---------------------------------------------------------------------------

function BatchToolbar({
  count,
  onRetryAll,
  onDeleteAll,
  onClear,
  isPending,
}: {
  count: number;
  onRetryAll: () => void;
  onDeleteAll: () => void;
  onClear: () => void;
  isPending: boolean;
}) {
  return (
    <div className="flex items-center gap-3 rounded-xl bg-accent/10 px-4 py-2">
      <span className="text-xs font-semibold text-accent">
        {count} selected
      </span>
      <Button
        variant="outline"
        size="sm"
        onClick={onRetryAll}
        disabled={isPending}
      >
        <RefreshCw className="h-3.5 w-3.5" />
        Retry All
      </Button>
      <Button
        variant="danger"
        size="sm"
        onClick={onDeleteAll}
        disabled={isPending}
      >
        <Trash2 className="h-3.5 w-3.5" />
        Delete All
      </Button>
      <Button variant="ghost" size="sm" onClick={onClear}>
        <X className="h-3.5 w-3.5" />
        Clear
      </Button>
    </div>
  );
}

// ---------------------------------------------------------------------------
// DLQPage
// ---------------------------------------------------------------------------

export default function DLQPage() {
  const [limit, setLimit] = useState(25);
  const [cursor, setCursor] = useState<number | undefined>(undefined);
  const [cursorStack, setCursorStack] = useState<number[]>([]);

  // Filter state
  const [topicInput, setTopicInput] = useState("");
  const [timeRange, setTimeRange] = useState("");
  const debouncedTopic = useDebouncedValue(topicInput, 400);

  // Expand + select state
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());

  // Batch mutations
  const retryDLQ = useRetryDLQ();
  const deleteDLQ = useDeleteDLQ();
  const batchPending = retryDLQ.isPending || deleteDLQ.isPending;

  // Compute since ISO from time range preset
  const sinceISO = useMemo(() => {
    if (!timeRange || !SINCE_MS[timeRange]) return undefined;
    return new Date(Date.now() - SINCE_MS[timeRange]).toISOString();
  }, [timeRange]);

  const { data, isLoading, isError } = useDLQ({
    limit,
    cursor,
    topic: debouncedTopic || undefined,
    since: sinceISO,
  });

  const entries = data?.items ?? [];
  const nextCursor = data?.next_cursor ?? null;

  // Active filter count
  const activeFilterCount =
    (debouncedTopic ? 1 : 0) + (timeRange ? 1 : 0);

  // Reset pagination when filters change
  const resetPagination = useCallback(() => {
    setCursor(undefined);
    setCursorStack([]);
  }, []);

  useEffect(() => {
    resetPagination();
  }, [debouncedTopic, sinceISO, resetPagination]);

  const clearFilters = useCallback(() => {
    setTopicInput("");
    setTimeRange("");
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

  // Checkbox handlers
  const toggleSelect = useCallback((id: string) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  const toggleSelectAll = useCallback(() => {
    if (selectedIds.size === entries.length && entries.length > 0) {
      setSelectedIds(new Set());
    } else {
      setSelectedIds(new Set(entries.map((e) => e.id)));
    }
  }, [selectedIds.size, entries]);

  const clearSelection = useCallback(() => setSelectedIds(new Set()), []);

  const handleBatchRetry = useCallback(() => {
    for (const id of selectedIds) {
      retryDLQ.mutate({ id });
    }
    clearSelection();
  }, [selectedIds, retryDLQ, clearSelection]);

  const handleBatchDelete = useCallback(() => {
    for (const id of selectedIds) {
      deleteDLQ.mutate(id);
    }
    clearSelection();
  }, [selectedIds, deleteDLQ, clearSelection]);

  const allChecked = entries.length > 0 && selectedIds.size === entries.length;

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <h1 className="font-display text-2xl font-bold text-ink">Dead Letter Queue</h1>
          {activeFilterCount > 0 && (
            <Badge variant="info">{activeFilterCount} filter{activeFilterCount > 1 ? "s" : ""}</Badge>
          )}
        </div>
      </div>

      {/* Filter bar */}
      <div className="flex flex-wrap items-end gap-3">
        <div className="w-56">
          <label className="mb-1 block text-[11px] font-semibold uppercase tracking-wider text-muted">
            Topic
          </label>
          <Input
            value={topicInput}
            onChange={(e) => setTopicInput(e.target.value)}
            placeholder="Filter by topic\u2026"
            className="h-[42px]"
          />
        </div>
        <div>
          <label className="mb-1 block text-[11px] font-semibold uppercase tracking-wider text-muted">
            Time Range
          </label>
          <div className="flex gap-1">
            {TIME_PRESETS.map((p) => (
              <button
                key={p.value}
                type="button"
                onClick={() => setTimeRange(p.value)}
                className={cn(
                  "rounded-full px-3 py-1.5 text-xs font-semibold transition",
                  timeRange === p.value
                    ? "bg-accent/15 text-accent"
                    : "text-muted hover:bg-surface2",
                )}
              >
                {p.label}
              </button>
            ))}
          </div>
        </div>
        {activeFilterCount > 0 && (
          <Button variant="ghost" size="sm" onClick={clearFilters}>
            <X className="h-3.5 w-3.5" />
            Clear
          </Button>
        )}
      </div>

      {/* Batch toolbar */}
      {selectedIds.size > 0 && (
        <BatchToolbar
          count={selectedIds.size}
          onRetryAll={handleBatchRetry}
          onDeleteAll={handleBatchDelete}
          onClear={clearSelection}
          isPending={batchPending}
        />
      )}

      <div className="surface-card overflow-hidden rounded-2xl">
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="border-b border-border">
              <tr>
                <th className="w-10 px-4 py-3">
                  <input
                    type="checkbox"
                    checked={allChecked}
                    onChange={toggleSelectAll}
                    className="h-3.5 w-3.5 rounded border-border accent-accent"
                  />
                </th>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted">
                  Job ID
                </th>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted">
                  Reason
                </th>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted">
                  Attempts
                </th>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted">
                  Topic
                </th>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted">
                  Failed At
                </th>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted">
                  Actions
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {isLoading && <SkeletonRows />}

              {!isLoading && isError && (
                <tr>
                  <td colSpan={7} className="px-4 py-12 text-center text-muted">
                    Failed to load dead letter queue. Please try again.
                  </td>
                </tr>
              )}

              {!isLoading && !isError && entries.length === 0 && (
                <tr>
                  <td colSpan={7} className="px-4 py-12 text-center text-muted">
                    Dead letter queue is empty &mdash; no failed jobs
                  </td>
                </tr>
              )}

              {!isLoading &&
                entries.map((entry: DLQEntry) => {
                  const isExpanded = expandedId === entry.id;
                  return (
                    <DLQRow
                      key={entry.id}
                      entry={entry}
                      isExpanded={isExpanded}
                      isSelected={selectedIds.has(entry.id)}
                      onToggleExpand={() =>
                        setExpandedId(isExpanded ? null : entry.id)
                      }
                      onToggleSelect={() => toggleSelect(entry.id)}
                    />
                  );
                })}
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
    </div>
  );
}

// ---------------------------------------------------------------------------
// DLQ Row (with expandable retry attempts + checkbox)
// ---------------------------------------------------------------------------

function DLQRow({
  entry,
  isExpanded,
  isSelected,
  onToggleExpand,
  onToggleSelect,
}: {
  entry: DLQEntry;
  isExpanded: boolean;
  isSelected: boolean;
  onToggleExpand: () => void;
  onToggleSelect: () => void;
}) {
  const [showFullError, setShowFullError] = useState(false);
  const errorText = entry.error || entry.reason || entry.reasonCode || "\u2014";
  const errorTruncated = errorText.length > 120;
  const displayError =
    showFullError || !errorTruncated
      ? errorText
      : errorText.slice(0, 120) + "\u2026";

  return (
    <>
      <tr
        className={cn(
          "transition-colors cursor-pointer",
          isSelected ? "bg-accent/5" : "hover:bg-surface2/60",
        )}
        onClick={onToggleExpand}
      >
        <td className="w-10 px-4 py-3" onClick={(e) => e.stopPropagation()}>
          <input
            type="checkbox"
            checked={isSelected}
            onChange={onToggleSelect}
            className="h-3.5 w-3.5 rounded border-border accent-accent"
          />
        </td>
        <td className="px-4 py-3 font-mono text-xs text-ink">
          <span className="inline-flex items-center gap-1">
            {isExpanded ? (
              <ChevronDown className="h-3 w-3 text-muted" />
            ) : (
              <ChevronRight className="h-3 w-3 text-muted" />
            )}
            {entry.jobId.slice(0, 8)}
          </span>
        </td>
        <td className="px-4 py-3 max-w-md">
          <span
            className="text-danger font-medium text-sm break-words"
            title={errorText}
          >
            {displayError}
          </span>
          {errorTruncated && (
            <button
              className="ml-1 text-xs text-accent hover:underline"
              onClick={(e) => {
                e.stopPropagation();
                setShowFullError((v) => !v);
              }}
            >
              {showFullError ? "less" : "more"}
            </button>
          )}
        </td>
        <td className="px-4 py-3 text-xs text-muted">
          {entry.retryCount ?? entry.attempts ?? 0}
          {entry.maxRetries != null ? `/${entry.maxRetries}` : ""}
        </td>
        <td className="px-4 py-3 text-xs text-muted font-mono">
          {entry.originalTopic || "\u2014"}
        </td>
        <td className="px-4 py-3 text-xs text-muted">
          {entry.failedAt ? timeAgo(entry.failedAt) : "\u2014"}
        </td>
        <td className="px-4 py-3" onClick={(e) => e.stopPropagation()}>
          <DLQRowActions entryId={entry.id} />
        </td>
      </tr>
      {isExpanded && (
        <tr>
          <td colSpan={7} className="bg-surface2/20 border-b border-border">
            <RetryAttemptsPanel attempts={entry.retryAttempts ?? []} />
          </td>
        </tr>
      )}
    </>
  );
}
