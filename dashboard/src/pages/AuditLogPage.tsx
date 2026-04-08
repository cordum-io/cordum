/*
 * DESIGN: "Control Surface" — Audit Log
 * Matches cordumds-gj5mw4zm.manus.space showcase patterns
 */
import { useState, useRef, useEffect } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { motion } from "framer-motion";
import { get } from "@/api/client";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonTable } from "@/components/ui/Skeleton";
import {
  Search,
  RefreshCw,
  FileText,
  Download,
  Calendar,
  X,
} from "lucide-react";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { cn, formatRelativeTime } from "@/lib/utils";
import { toast } from "sonner";
import { ErrorBanner } from "@/components/ui/ErrorBanner";

interface AuditEvent {
  id: string;
  action: string;
  actor: string;
  resource: string;
  resourceId?: string;
  detail?: string;
  timestamp: string;
  ip?: string;
}

interface AuditResponse {
  items: Record<string, unknown>[];
  total?: number;
  has_more?: boolean;
  offset?: number;
}

interface AuditObserverState {
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  fetchNextPage: () => Promise<unknown>;
}

const PAGE_SIZE = 50;

export function shouldFetchNextAuditPage(
  entries: Pick<IntersectionObserverEntry, "isIntersecting">[],
  hasNextPage: boolean,
  isFetchingNextPage: boolean,
): boolean {
  return !!entries[0]?.isIntersecting && hasNextPage && !isFetchingNextPage;
}

function mapEvent(e: Record<string, unknown>): AuditEvent {
  return {
    id: (e.id as string) ?? "",
    action: (e.action as string) ?? "",
    actor:
      (e.actor_id as string) ||
      (e.role as string) ||
      (e.actor as string) ||
      "unknown",
    resource: (e.resource_type as string) || (e.resource as string) || "",
    resourceId:
      (e.resource_id as string) || (e.resourceId as string) || undefined,
    detail: (e.message as string) || (e.detail as string) || undefined,
    timestamp: (e.created_at as string) || (e.timestamp as string) || "",
  };
}

function actionColor(action: string) {
  if (action.includes("created") || action.includes("registered"))
    return "text-[var(--color-success)] bg-[var(--color-success)]/10 border-[var(--color-success)]/20";
  if (action.includes("failed") || action.includes("deleted"))
    return "text-destructive bg-destructive/10 border-destructive/20";
  if (action.includes("updated") || action.includes("decided"))
    return "text-[var(--color-warning)] bg-[var(--color-warning)]/10 border-[var(--color-warning)]/20";
  return "text-cordum bg-cordum/10 border-cordum/20";
}

export default function AuditLogPage() {
  const [search, setSearch] = useState("");
  const [actionFilter, setActionFilter] = useState("");
  const [dateFrom, setDateFrom] = useState("");
  const [dateTo, setDateTo] = useState("");
  const loadMoreRef = useRef<HTMLDivElement>(null);
  const observerStateRef = useRef<AuditObserverState>({
    hasNextPage: false,
    isFetchingNextPage: false,
    fetchNextPage: () => Promise.resolve(),
  });
  const handleObserverRef = useRef<IntersectionObserverCallback | null>(null);

  const {
    data,
    isLoading,
    isError,
    error,
    refetch,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
  } = useInfiniteQuery({
    queryKey: ["audit", actionFilter, dateFrom, dateTo, search],
    queryFn: async ({ pageParam = 0 }) => {
      const params = new URLSearchParams({
        limit: String(PAGE_SIZE),
        offset: String(pageParam),
      });
      if (actionFilter) params.set("action", actionFilter);
      if (dateFrom) params.set("after", new Date(dateFrom).toISOString());
      if (dateTo)
        params.set("before", new Date(dateTo + "T23:59:59").toISOString());
      if (search) params.set("search", search);
      return get<AuditResponse>(`/policy/audit?${params}`);
    },
    getNextPageParam: (lastPage, allPages) => {
      if (!lastPage.has_more) return undefined;
      return allPages.reduce((sum, p) => sum + (p.items?.length ?? 0), 0);
    },
    initialPageParam: 0,
  });

  const events: AuditEvent[] = (data?.pages ?? []).flatMap((p) =>
    (p.items ?? []).map(mapEvent),
  );
  const total = data?.pages?.[0]?.total;

  observerStateRef.current = {
    hasNextPage: !!hasNextPage,
    isFetchingNextPage,
    fetchNextPage,
  };
  if (!handleObserverRef.current) {
    handleObserverRef.current = (entries) => {
      const {
        hasNextPage: canFetchNextPage,
        isFetchingNextPage: fetchingNextPage,
        fetchNextPage: fetchNextPagePage,
      } = observerStateRef.current;
      if (
        shouldFetchNextAuditPage(entries, canFetchNextPage, fetchingNextPage)
      ) {
        void fetchNextPagePage();
      }
    };
  }

  useEffect(() => {
    const el = loadMoreRef.current;
    if (!el) return;
    const observer = new IntersectionObserver(
      (entries, currentObserver) => {
        handleObserverRef.current?.(entries, currentObserver);
      },
      { threshold: 0.1 },
    );
    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  const filtersActive = !!actionFilter || !!dateFrom || !!dateTo || !!search;
  const activeFilterCount = [actionFilter, dateFrom, dateTo, search].filter(
    Boolean,
  ).length;

  const exportCSV = () => {
    if (filtersActive) {
      toast.info(
        `Exporting ${events.length} filtered events. Clear filters to export all.`,
      );
    }
    const rows = events.map((e) =>
      [
        e.timestamp,
        e.action,
        e.actor,
        e.resource,
        e.resourceId ?? "",
        (e.detail ?? "").replace(/,/g, ";"),
      ].join(","),
    );
    const csv = [
      "timestamp,action,actor,resource,resourceId,detail",
      ...rows,
    ].join("\n");
    const blob = new Blob([csv], { type: "text/csv" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    const dateSuffix =
      dateFrom || dateTo ? `-${dateFrom || "start"}-${dateTo || "now"}` : "";
    a.download = `audit-export-${new Date().toISOString().slice(0, 10)}${dateSuffix}.csv`;
    a.click();
    URL.revokeObjectURL(url);
    toast.success(`Exported ${events.length} events`);
  };

  if (isError) {
    return (
      <ErrorBanner
        message={
          error instanceof Error ? error.message : "Failed to load audit log"
        }
        onRetry={() => void refetch()}
      />
    );
  }

  return (
    <div className="space-y-6">
      <PageHeader
        label="Platform"
        title="Audit Log"
        subtitle="System-wide activity trail"
        actions={
          <div className="flex gap-2">
            <Button variant="outline" size="sm" onClick={() => refetch()}>
              <RefreshCw className="w-3 h-3 mr-1" />
              Refresh
            </Button>
            <Button variant="outline" size="sm" onClick={exportCSV}>
              <Download className="w-3 h-3 mr-1" />
              Export CSV
            </Button>
          </div>
        }
      />

      {/* Filters */}
      <div
        className={cn(
          "flex items-center gap-3 flex-wrap",
          filtersActive && "border-l-[3px] border-l-[var(--color-info)] pl-3",
        )}
      >
        <div className="relative flex-1 max-w-sm">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted-foreground" />
          <input
            type="text"
            placeholder="Search events..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="h-8 w-full pl-8 pr-3 text-xs bg-surface-1 border border-border rounded-2xl text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-cordum"
            aria-label="Search audit events"
          />
        </div>
        <select
          value={actionFilter}
          onChange={(e) => setActionFilter(e.target.value)}
          className="h-8 px-3 text-xs bg-surface-1 border border-border rounded-2xl text-foreground focus:outline-none focus:ring-1 focus:ring-cordum"
          aria-label="Filter by action"
        >
          <option value="">All Actions</option>
          <option value="job.created">Job Created</option>
          <option value="job.completed">Job Completed</option>
          <option value="job.failed">Job Failed</option>
          <option value="approval.decided">Approval Decided</option>
          <option value="policy.updated">Policy Updated</option>
          <option value="worker.registered">Worker Registered</option>
        </select>
        <div className="flex items-center gap-1.5">
          <Calendar className="w-3.5 h-3.5 text-muted-foreground" />
          <input
            type="date"
            value={dateFrom}
            onChange={(e) => setDateFrom(e.target.value)}
            className="h-8 px-2 text-xs bg-surface-1 border border-border rounded-2xl text-foreground focus:outline-none focus:ring-1 focus:ring-cordum"
            aria-label="From date"
          />
          <span className="text-xs text-muted-foreground">to</span>
          <input
            type="date"
            value={dateTo}
            onChange={(e) => setDateTo(e.target.value)}
            className="h-8 px-2 text-xs bg-surface-1 border border-border rounded-2xl text-foreground focus:outline-none focus:ring-1 focus:ring-cordum"
            aria-label="To date"
          />
        </div>
        {filtersActive && (
          <StatusBadge variant="info">
            {activeFilterCount} filter{activeFilterCount > 1 ? "s" : ""} active
          </StatusBadge>
        )}
        <button
          onClick={() => {
            setSearch("");
            setActionFilter("");
            setDateFrom("");
            setDateTo("");
          }}
          disabled={!filtersActive}
          className={cn(
            "h-8 px-3 text-xs transition-colors flex items-center gap-1",
            filtersActive
              ? "text-muted-foreground hover:text-foreground"
              : "text-muted-foreground/40 cursor-not-allowed",
          )}
        >
          <X className="w-3 h-3" />
          Clear filters
        </button>
      </div>

      {/* Result count when filtered */}
      {filtersActive && (
        <p className="text-xs text-muted-foreground">
          Showing {events.length} of {data?.pages?.[0]?.total ?? events.length}+
          events
        </p>
      )}

      {/* Total count */}
      {total != null && (
        <p className="text-xs text-muted-foreground">
          Showing {events.length} of {total} events
          {filtersActive && " (filtered)"}
        </p>
      )}

      {/* Table */}
      {isLoading ? (
        <div className="instrument-card">
          <SkeletonTable rows={10} />
        </div>
      ) : events.length === 0 ? (
        <EmptyState
          icon={<FileText className="w-5 h-5" />}
          title="No audit events"
          description={
            filtersActive
              ? "No events match your filters"
              : "Events will appear as actions occur in the system"
          }
        />
      ) : (
        <motion.div
          initial={{ opacity: 0, y: 12 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.3 }}
          className="instrument-card overflow-hidden"
        >
          <div className="overflow-x-auto">
            <table className="w-full min-w-[700px]">
              <thead>
                <tr className="border-b border-border bg-surface-0">
                  <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">
                    Time
                  </th>
                  <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">
                    Action
                  </th>
                  <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">
                    Actor
                  </th>
                  <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">
                    Resource
                  </th>
                  <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">
                    Detail
                  </th>
                </tr>
              </thead>
              <tbody>
                {events.map((e) => (
                  <tr
                    key={e.id}
                    className="border-b border-border hover:bg-surface-1 transition-colors"
                  >
                    <td className="px-5 py-3 font-mono text-xs text-muted-foreground whitespace-nowrap">
                      {formatRelativeTime(e.timestamp)}
                    </td>
                    <td className="px-5 py-3">
                      <span
                        className={cn(
                          "text-xs font-mono px-2 py-0.5 rounded-full border",
                          actionColor(e.action),
                        )}
                      >
                        {e.action}
                      </span>
                    </td>
                    <td className="px-5 py-3 text-sm text-foreground">
                      {e.actor}
                    </td>
                    <td className="px-5 py-3">
                      <span className="text-sm text-foreground">
                        {e.resource}
                      </span>
                      {e.resourceId && (
                        <span className="text-xs text-muted-foreground font-mono ml-1">
                          ({e.resourceId.slice(0, 12)})
                        </span>
                      )}
                    </td>
                    <td className="px-5 py-3 text-xs text-muted-foreground truncate max-w-[200px]">
                      {e.detail ?? "\u2014"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          {/* Load More / Infinite scroll trigger */}
          <div ref={loadMoreRef} className="px-5 py-3 text-center">
            {isFetchingNextPage ? (
              <span className="text-xs text-muted-foreground">
                Loading more...
              </span>
            ) : hasNextPage ? (
              <Button
                variant="ghost"
                size="sm"
                onClick={() => void fetchNextPage()}
              >
                Load more
                {total != null ? ` (${events.length} of ${total})` : ""}
              </Button>
            ) : events.length > PAGE_SIZE ? (
              <span className="text-xs text-muted-foreground">
                All events loaded
              </span>
            ) : null}
          </div>
        </motion.div>
      )}
    </div>
  );
}
