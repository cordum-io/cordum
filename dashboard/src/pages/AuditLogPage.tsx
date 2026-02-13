import { useState, useMemo, useCallback, useEffect, useRef } from "react";
import { useSearchParams } from "react-router-dom";
import { List, BarChart3 } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { useAuditLog, useAuditCorrelation, type AuditFilters } from "../hooks/useAudit";
import { Card } from "../components/ui/Card";
import { Select } from "../components/ui/Select";
import { Button } from "../components/ui/Button";
import { AuditExport } from "../components/audit/AuditExport";
import { SavedFiltersDropdown } from "../components/audit/SavedFiltersDropdown";
import { AuditFiltersBar, type AuditFilterValues } from "../components/audit/AuditFiltersBar";
import type { SerializedFilterState } from "../lib/audit-filters";
import { AuditEventCard, classifyEvent } from "../components/audit/AuditEventCard";
import { AuditDetailPanel } from "../components/audit/AuditDetailPanel";
import { AuditTimeline } from "../components/audit/AuditTimeline";
import { AuditIntegrityPanel } from "../components/audit/AuditIntegrityPanel";
import { useEventStore } from "../state/events";
import { cn } from "../lib/utils";
import { usePageTitle } from "../hooks/usePageTitle";

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const PER_PAGE_OPTIONS = [25, 50, 100] as const;

const HIGH_SEVERITY_ACTIONS = new Set([
  "deny", "safety_deny", "require_approval",
  "safety_require_approval", "auth_failure",
]);

// ---------------------------------------------------------------------------
// Time gap formatter (for correlation view)
// ---------------------------------------------------------------------------

function formatGap(ms: number): string {
  if (ms < 1000) return "<1s";
  const secs = ms / 1000;
  if (secs < 60) return `${secs.toFixed(1)}s`;
  const mins = Math.floor(secs / 60);
  const remSecs = Math.floor(secs % 60);
  if (mins < 60) return `${mins}m ${remSecs}s`;
  const hours = Math.floor(mins / 60);
  const remMins = mins % 60;
  if (hours < 24) return `${hours}h ${remMins}m`;
  const days = Math.floor(hours / 24);
  const remHours = hours % 24;
  return `${days}d ${remHours}h`;
}

// ---------------------------------------------------------------------------
// AuditLogPage
// ---------------------------------------------------------------------------

export default function AuditLogPage() {
  usePageTitle("Audit Log");
  // Pagination state
  const [page, setPage] = useState(0);
  const [perPage, setPerPage] = useState<number>(25);

  // View mode: stream (default) or timeline
  type ViewMode = "stream" | "timeline";
  const [viewMode, setViewMode] = useState<ViewMode>("stream");

  // Live tail state
  const [liveTail, setLiveTail] = useState(false);
  const [newEventsCount, setNewEventsCount] = useState(0);
  const queryClient = useQueryClient();
  const scrollRef = useRef<HTMLDivElement>(null);
  const atTopRef = useRef(true);

  // Track WS events for live tail
  const wsEvents = useEventStore((s) => s.events);
  const prevEventsLenRef = useRef(wsEvents.length);

  // Live tail: prepend new audit-related events to cache
  useEffect(() => {
    if (!liveTail) {
      prevEventsLenRef.current = wsEvents.length;
      return;
    }
    const newCount = wsEvents.length - prevEventsLenRef.current;
    if (newCount <= 0) {
      prevEventsLenRef.current = wsEvents.length;
      return;
    }
    // New events arrived — invalidate audit query to refetch
    queryClient.invalidateQueries({ queryKey: ["audit"] });
    setNewEventsCount((c) => c + newCount);
    prevEventsLenRef.current = wsEvents.length;
  }, [liveTail, wsEvents.length, queryClient]);

  // Periodic revalidation every 30s when live tail is on
  useEffect(() => {
    if (!liveTail) return;
    const id = setInterval(() => {
      queryClient.invalidateQueries({ queryKey: ["audit"] });
    }, 30_000);
    return () => clearInterval(id);
  }, [liveTail, queryClient]);

  // Auto-pause when page is not visible
  const liveTailRef = useRef(liveTail);
  liveTailRef.current = liveTail;
  const savedLiveTailRef = useRef(false);

  useEffect(() => {
    function handleVisibility() {
      if (document.hidden && liveTailRef.current) {
        savedLiveTailRef.current = true;
        setLiveTail(false);
      } else if (!document.hidden && savedLiveTailRef.current) {
        savedLiveTailRef.current = false;
        setLiveTail(true);
        setNewEventsCount(0);
      }
    }
    document.addEventListener("visibilitychange", handleVisibility);
    return () => document.removeEventListener("visibilitychange", handleVisibility);
  }, []);

  // Scroll tracking for live tail floating button
  useEffect(() => {
    function handleScroll() {
      atTopRef.current = window.scrollY < 50;
      if (atTopRef.current && liveTail) {
        setNewEventsCount(0);
      }
    }
    window.addEventListener("scroll", handleScroll, { passive: true });
    return () => window.removeEventListener("scroll", handleScroll);
  }, [liveTail]);

  // Track which event IDs are "new" for slide-in animation
  const seenIdsRef = useRef<Set<string>>(new Set());
  const [animatingIds, setAnimatingIds] = useState<Set<string>>(new Set());

  // Toggle live tail
  function toggleLiveTail() {
    if (liveTail) {
      setLiveTail(false);
    } else {
      setLiveTail(true);
      setNewEventsCount(0);
      setPage(0);
    }
  }

  // Panel state synced with URL (?eventId=...)
  const [searchParams, setSearchParams] = useSearchParams();
  const selectedEventId = searchParams.get("eventId");

  // Correlation view mode
  const correlationView = searchParams.get("view") === "correlation";
  const correlationResource = searchParams.get("resource") ?? "";
  const [correlationResType, correlationResId] = correlationResource.includes(":")
    ? [correlationResource.split(":")[0], correlationResource.split(":").slice(1).join(":")]
    : ["", correlationResource];
  const { data: correlationEvents, isLoading: corrLoading } = useAuditCorrelation(
    correlationView && correlationResId ? correlationResId : null,
  );

  function exitCorrelation() {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev);
      next.delete("view");
      next.delete("resource");
      return next;
    });
  }

  // Filters driven by AuditFiltersBar (URL-synced)
  const [filterValues, setFilterValues] = useState<AuditFilterValues>({
    eventType: [], actor: "", resourceType: "", resourceId: "",
    severity: [], outcome: [], timeRange: "", from: "", to: "", search: "",
  });
  const [activeFilterId, setActiveFilterId] = useState<string | null>(null);

  // Convert filter values to hook format
  const filters: AuditFilters = useMemo(
    () => ({
      eventType: filterValues.eventType.length ? filterValues.eventType : undefined,
      actor: filterValues.actor || undefined,
      resourceType: filterValues.resourceType || undefined,
      resourceId: filterValues.resourceId || undefined,
      severity: filterValues.severity.length ? filterValues.severity : undefined,
      outcome: filterValues.outcome.length ? filterValues.outcome : undefined,
      timeRange: filterValues.timeRange || undefined,
      from: filterValues.from || undefined,
      to: filterValues.to || undefined,
      search: filterValues.search || undefined,
      sort: "time-desc",
    }),
    [filterValues],
  );

  const { data, isLoading, isError, filtered } = useAuditLog(filters);

  // Animate new events when live tail is active
  useEffect(() => {
    if (!liveTail || !filtered.length) return;
    const newIds = new Set<string>();
    for (const e of filtered) {
      if (!seenIdsRef.current.has(e.id)) {
        newIds.add(e.id);
        seenIdsRef.current.add(e.id);
      }
    }
    if (newIds.size > 0) {
      setAnimatingIds(newIds);
      const t = setTimeout(() => setAnimatingIds(new Set()), 2_000);
      return () => clearTimeout(t);
    }
  }, [liveTail, filtered]);

  // Stats summary
  const stats = useMemo(() => {
    const total = filtered.length;
    let highCount = 0;
    let safetyCount = 0;
    for (const e of filtered) {
      const action = (e.action || e.eventType || "").toLowerCase();
      if (HIGH_SEVERITY_ACTIONS.has(action)) highCount++;
      if (classifyEvent(e) === "safety_decision") safetyCount++;
    }
    return { total, highCount, safetyCount };
  }, [filtered]);

  // Pagination
  const totalFiltered = filtered.length;
  const totalPages = Math.max(1, Math.ceil(totalFiltered / perPage));
  const paged = useMemo(
    () => filtered.slice(page * perPage, (page + 1) * perPage),
    [filtered, page, perPage],
  );

  // Reset page when filters change; disable live tail if historical range is set
  const handleFiltersChange = useCallback((values: AuditFilterValues) => {
    setFilterValues(values);
    setPage(0);
    setActiveFilterId(null);
    if (values.timeRange && values.timeRange !== "") {
      setLiveTail(false);
    }
  }, []);

  // Saved filter handlers
  const handleLoadSavedFilter = useCallback((saved: SerializedFilterState, id: string) => {
    const next: AuditFilterValues = {
      eventType: saved.eventType ?? [],
      actor: saved.actor ?? "",
      resourceType: saved.resourceType ?? "",
      resourceId: saved.resourceId ?? "",
      severity: saved.severity ?? [],
      outcome: saved.outcome ?? [],
      timeRange: saved.timeRange ?? "",
      from: "",
      to: "",
      search: saved.search ?? "",
    };
    setFilterValues(next);
    setActiveFilterId(id);
    setPage(0);
    if (next.timeRange) setLiveTail(false);
  }, []);

  const handleClearActiveFilter = useCallback(() => {
    setActiveFilterId(null);
  }, []);

  const selectedEntry = useMemo(
    () => filtered.find((e) => e.id === selectedEventId) ?? null,
    [filtered, selectedEventId],
  );

  const openPanel = useCallback(
    (id: string) => {
      setSearchParams((prev) => {
        const next = new URLSearchParams(prev);
        next.set("eventId", id);
        return next;
      });
    },
    [setSearchParams],
  );

  const closePanel = useCallback(() => {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev);
      next.delete("eventId");
      return next;
    });
  }, [setSearchParams]);

  function handleEventClick(id: string) {
    if (id === selectedEventId) {
      closePanel();
    } else {
      openPanel(id);
    }
  }

  // -------------------------------------------------------------------------
  // Correlation view
  // -------------------------------------------------------------------------
  if (correlationView) {
    return (
      <div className="space-y-4">
        {/* Correlation header */}
        <div className="flex items-center justify-between">
          <div>
            <h1 className="font-display text-2xl font-bold text-ink">
              Lifecycle: {correlationResType} <span className="font-mono text-lg">{correlationResId.slice(0, 16)}</span>
            </h1>
            <p className="text-sm text-muted">
              All events for this resource in chronological order.
            </p>
          </div>
          <Button variant="outline" size="sm" onClick={exitCorrelation}>
            Back to stream &rarr;
          </Button>
        </div>

        {corrLoading && (
          <Card>
            <p className="py-8 text-center text-sm text-muted">Loading related events&hellip;</p>
          </Card>
        )}

        {!corrLoading && (!correlationEvents || correlationEvents.length === 0) && (
          <Card>
            <p className="py-8 text-center text-sm text-muted">No related events found.</p>
          </Card>
        )}

        {!corrLoading && correlationEvents && correlationEvents.length > 0 && (
          <div className="space-y-0">
            {correlationEvents.map((entry, i) => {
              const prevEntry = i > 0 ? correlationEvents[i - 1] : null;
              const gapMs = prevEntry
                ? new Date(entry.timestamp).getTime() - new Date(prevEntry.timestamp).getTime()
                : 0;

              return (
                <div key={entry.id}>
                  {/* Time gap indicator */}
                  {prevEntry && (
                    <div className="flex items-center gap-2 py-1 pl-8">
                      <div className="h-4 w-px bg-border" />
                      <span className="text-xs text-muted">&larr; {formatGap(gapMs)} &rarr;</span>
                    </div>
                  )}
                  <div className="border-l-2 border-border pl-4">
                    <AuditEventCard
                      entry={entry}
                      onClick={handleEventClick}
                    />
                  </div>
                </div>
              );
            })}
            <p className="pl-8 pt-2 text-xs text-muted">
              {correlationEvents.length} event{correlationEvents.length !== 1 ? "s" : ""} in lifecycle
            </p>
          </div>
        )}

        {/* Slide-out detail panel */}
        <AuditDetailPanel entry={selectedEntry} onClose={closePanel} />
      </div>
    );
  }

  // -------------------------------------------------------------------------
  // Normal stream view
  // -------------------------------------------------------------------------
  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="font-display text-2xl font-bold text-ink">Audit Trail</h1>
          <p className="text-sm text-muted">
            Policy audit events from the control plane.
          </p>
          {stats.total > 0 && (
            <p className="mt-1 text-xs text-muted">
              {stats.total} events
              {stats.highCount > 0 && <> &middot; {stats.highCount} high severity</>}
              {stats.safetyCount > 0 && <> &middot; {stats.safetyCount} safety decisions</>}
              {filterValues.timeRange
                ? <> &middot; range: {filterValues.timeRange}</>
                : <> &middot; all time</>}
            </p>
          )}
        </div>
        <div className="flex items-center gap-2">
          {/* Live tail toggle */}
          <button
            type="button"
            className={cn(
              "flex items-center gap-1.5 rounded-lg border px-3 py-1.5 text-xs font-medium transition-colors",
              liveTail
                ? "border-green-500 bg-green-50 text-green-700 dark:bg-green-950 dark:text-green-300"
                : "border-border text-muted hover:text-ink",
            )}
            onClick={toggleLiveTail}
          >
            {liveTail && (
              <span className="relative flex h-2 w-2">
                <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-green-500 opacity-75" />
                <span className="relative inline-flex h-2 w-2 rounded-full bg-green-500" />
              </span>
            )}
            {liveTail ? "Live" : "Live tail"}
          </button>

          {/* View toggle */}
          <div className="flex rounded-lg border border-border p-0.5">
            <button
              type="button"
              className={cn(
                "flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium transition-colors",
                viewMode === "stream"
                  ? "bg-accent/15 text-accent"
                  : "text-muted hover:text-ink",
              )}
              onClick={() => setViewMode("stream")}
            >
              <List className="h-3.5 w-3.5" />
              Stream
            </button>
            <button
              type="button"
              className={cn(
                "flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium transition-colors",
                viewMode === "timeline"
                  ? "bg-accent/15 text-accent"
                  : "text-muted hover:text-ink",
              )}
              onClick={() => setViewMode("timeline")}
            >
              <BarChart3 className="h-3.5 w-3.5" />
              Timeline
            </button>
          </div>
          <AuditExport filters={filters} />
        </div>
      </div>

      {/* Composable filter bar (URL-synced) + saved filters */}
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0 flex-1">
          <AuditFiltersBar onChange={handleFiltersChange} />
        </div>
        <SavedFiltersDropdown
          currentFilters={filters}
          activeFilterId={activeFilterId}
          onLoad={handleLoadSavedFilter}
          onClearActive={handleClearActiveFilter}
        />
      </div>

      {/* Loading */}
      {isLoading && (
        <Card>
          <p className="py-8 text-center text-sm text-muted">Loading audit trail\u2026</p>
        </Card>
      )}

      {/* Error */}
      {!isLoading && isError && (
        <Card>
          <p className="py-8 text-center text-sm text-danger">
            Failed to load audit trail.
          </p>
        </Card>
      )}

      {/* Empty */}
      {!isLoading && !isError && totalFiltered === 0 && (
        <Card>
          <p className="py-8 text-center text-sm text-muted">
            {(data?.items?.length ?? 0) > 0
              ? "No entries match the current filters."
              : "No audit entries."}
          </p>
        </Card>
      )}

      {/* Timeline view */}
      {!isLoading && !isError && totalFiltered > 0 && viewMode === "timeline" && (
        <AuditTimeline events={filtered} onEventClick={handleEventClick} />
      )}

      {/* Event stream */}
      {!isLoading && !isError && totalFiltered > 0 && viewMode === "stream" && (
        <>
          {/* Floating new events button (live tail, scrolled away) */}
          {liveTail && newEventsCount > 0 && !atTopRef.current && (
            <div className="sticky top-2 z-20 flex justify-center">
              <button
                type="button"
                className="rounded-full border border-accent bg-surface px-4 py-1.5 text-xs font-medium text-accent shadow-lg transition-colors hover:bg-accent hover:text-white"
                onClick={() => {
                  scrollRef.current?.scrollTo({ top: 0, behavior: "smooth" });
                  setNewEventsCount(0);
                }}
              >
                &uarr; {newEventsCount} new event{newEventsCount !== 1 ? "s" : ""}
              </button>
            </div>
          )}
          <div className="space-y-2" ref={scrollRef}>
            {filterValues.search && (
              <p className="text-xs text-muted">
                {totalFiltered} result{totalFiltered !== 1 ? "s" : ""} for &ldquo;{filterValues.search}&rdquo;
              </p>
            )}
            {paged.map((entry) => (
              <div
                key={entry.id}
                className={cn(
                  "transition-all duration-200 ease-out",
                  animatingIds.has(entry.id) && "animate-[slideIn_200ms_ease-out] bg-accent/5",
                )}
                style={animatingIds.has(entry.id) ? { animation: "slideIn 200ms ease-out, fadeHighlight 2s ease-out" } : undefined}
              >
                <AuditEventCard
                  entry={entry}
                  onClick={handleEventClick}
                  searchQuery={filterValues.search}
                />
              </div>
            ))}
          </div>

          {/* Pagination */}
          <div className="flex items-center justify-between text-sm">
            <span className="text-xs text-muted">
              Showing {page * perPage + 1}\u2013
              {Math.min((page + 1) * perPage, totalFiltered)} of{" "}
              {totalFiltered} entries
            </span>
            <div className="flex items-center gap-3">
              <Select
                className="h-8 w-20 text-xs"
                value={perPage}
                onChange={(e) => {
                  setPerPage(Number(e.target.value));
                  setPage(0);
                }}
              >
                {PER_PAGE_OPTIONS.map((n) => (
                  <option key={n} value={n}>
                    {n}
                  </option>
                ))}
              </Select>
              <div className="flex gap-1">
                <Button
                  variant="outline"
                  size="sm"
                  disabled={page === 0}
                  onClick={() => setPage((p) => p - 1)}
                >
                  Newer
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={page >= totalPages - 1}
                  onClick={() => setPage((p) => p + 1)}
                >
                  Older
                </Button>
              </div>
            </div>
          </div>
        </>
      )}

      {/* Integrity & retention info */}
      <AuditIntegrityPanel events={data?.items ?? []} />

      {/* Slide-out detail panel */}
      <AuditDetailPanel entry={selectedEntry} onClose={closePanel} />
    </div>
  );
}
