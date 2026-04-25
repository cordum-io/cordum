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
import { Input } from "@/components/ui/Input";
import { Select } from "@/components/ui/Select";
import { LabeledField } from "@/components/ui/LabeledField";
import { InstrumentCard, InstrumentCardBody } from "@/components/ui/InstrumentCard";
import {
  Search,
  RefreshCw,
  FileText,
  Download,
  Calendar,
  Bot,
  X,
} from "lucide-react";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
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

const tableBodyVariants = {
  hidden: {},
  visible: {
    transition: {
      staggerChildren: 0.04,
    },
  },
};

const tableRowVariants = {
  hidden: { opacity: 0, y: 8 },
  visible: { opacity: 1, y: 0 },
};

export function parseSeqParam(raw?: string | null): number | undefined {
  if (typeof raw !== "string") return undefined;
  const trimmed = raw.trim();
  if (!trimmed) return undefined;
  if (!/^\d+$/.test(trimmed)) return undefined;
  const parsed = Number.parseInt(trimmed, 10);
  return Number.isFinite(parsed) ? parsed : undefined;
}

export function filterEventsBySeq<T extends { seq?: number }>(
  events: T[],
  fromSeq?: number,
  toSeq?: number,
): T[] {
  if (fromSeq === undefined && toSeq === undefined) {
    return events;
  }
  return events.filter((event) => {
    if (typeof event.seq !== "number") return false;
    if (fromSeq !== undefined && event.seq < fromSeq) return false;
    if (toSeq !== undefined && event.seq > toSeq) return false;
    return true;
  });
}

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

function actionVariant(action: string): BadgeVariant {
  if (action.includes("created") || action.includes("registered")) {
    return "healthy";
  }
  if (action.includes("failed") || action.includes("deleted")) {
    return "danger";
  }
  if (action.includes("updated") || action.includes("decided")) {
    return "warning";
  }
  return "cordum";
}

interface AgentOption {
  id: string;
  name: string;
}

export default function AuditLogPage() {
  const [search, setSearch] = useState("");
  const [actionFilter, setActionFilter] = useState("");
  const [agentFilter, setAgentFilter] = useState("");
  const [dateFrom, setDateFrom] = useState("");
  const [dateTo, setDateTo] = useState("");
  const [agents, setAgents] = useState<AgentOption[]>([]);

  useEffect(() => {
    get<{ items?: Array<{ id: string; name: string }> }>("/agents")
      .then((res) => {
        if (res.items) {
          setAgents(res.items.map((a) => ({ id: a.id, name: a.name })));
        }
      })
      .catch(() => {
        /* agent list not available — filter hidden */
      });
  }, []);
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
    queryKey: ["audit", actionFilter, agentFilter, dateFrom, dateTo, search],
    queryFn: async ({ pageParam = 0 }) => {
      const params = new URLSearchParams({
        limit: String(PAGE_SIZE),
        offset: String(pageParam),
      });
      if (actionFilter) params.set("action", actionFilter);
      if (agentFilter) params.set("agent_id", agentFilter);
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

  const filtersActive =
    !!actionFilter || !!agentFilter || !!dateFrom || !!dateTo || !!search;
  const activeFilterCount = [
    actionFilter,
    agentFilter,
    dateFrom,
    dateTo,
    search,
  ].filter(Boolean).length;

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

      <InstrumentCard className="p-4">
        <InstrumentCardBody className="space-y-4">
          <div className="flex flex-col gap-4 xl:flex-row xl:items-end xl:justify-between">
            <div
              className={cn(
                "grid flex-1 gap-4",
                agents.length > 0
                  ? "md:grid-cols-2 xl:grid-cols-4"
                  : "md:grid-cols-2 xl:grid-cols-3",
              )}
            >
              <LabeledField label="Search">
                <Input
                  type="text"
                  icon={<Search className="h-3.5 w-3.5" />}
                  placeholder="Search events..."
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                  aria-label="Search audit events"
                  className="bg-surface-1"
                />
              </LabeledField>

              <LabeledField label="Action">
                <Select
                  value={actionFilter}
                  onChange={(e) => setActionFilter(e.target.value)}
                  aria-label="Filter by action"
                  className="bg-surface-1"
                >
                  <option value="">All Actions</option>
                  <option value="job.created">Job Created</option>
                  <option value="job.completed">Job Completed</option>
                  <option value="job.failed">Job Failed</option>
                  <option value="approval.decided">Approval Decided</option>
                  <option value="policy.updated">Policy Updated</option>
                  <option value="worker.registered">Worker Registered</option>
                </Select>
              </LabeledField>

              {agents.length > 0 && (
                <LabeledField
                  label="Agent"
                  description="Filter by actor"
                  action={<Bot className="h-3.5 w-3.5 text-muted-foreground" />}
                >
                  <Select
                    value={agentFilter}
                    onChange={(e) => setAgentFilter(e.target.value)}
                    aria-label="Filter by agent"
                    className="bg-surface-1"
                  >
                    <option value="">All Agents</option>
                    {agents.map((a) => (
                      <option key={a.id} value={a.id}>
                        {a.name}
                      </option>
                    ))}
                  </Select>
                </LabeledField>
              )}

              <LabeledField
                label="Date range"
                description="Inclusive start and end dates"
                action={<Calendar className="h-3.5 w-3.5 text-muted-foreground" />}
              >
                <div className="grid grid-cols-[1fr_auto_1fr] items-center gap-2">
                  <Input
                    type="date"
                    value={dateFrom}
                    onChange={(e) => setDateFrom(e.target.value)}
                    aria-label="From date"
                    className="bg-surface-1"
                  />
                  <span className="text-center text-xs text-muted-foreground">
                    to
                  </span>
                  <Input
                    type="date"
                    value={dateTo}
                    onChange={(e) => setDateTo(e.target.value)}
                    aria-label="To date"
                    className="bg-surface-1"
                  />
                </div>
              </LabeledField>
            </div>

            <div className="flex flex-wrap items-center gap-2 xl:justify-end">
              {filtersActive && (
                <StatusBadge variant="info">
                  {activeFilterCount} filter{activeFilterCount > 1 ? "s" : ""}{" "}
                  active
                </StatusBadge>
              )}
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  setSearch("");
                  setActionFilter("");
                  setAgentFilter("");
                  setDateFrom("");
                  setDateTo("");
                }}
                disabled={!filtersActive}
              >
                <X className="h-3 w-3" />
                Clear filters
              </Button>
            </div>
          </div>

          <div className="flex flex-wrap items-center gap-3 text-xs text-muted-foreground">
            {total != null ? (
              <span>
                Showing {events.length} of {total} events
                {filtersActive && " (filtered)"}
              </span>
            ) : (
              <span>Showing {events.length} events</span>
            )}
            {filtersActive && (
              <span>
                Narrowed by search, action, agent, or date range filters.
              </span>
            )}
          </div>
        </InstrumentCardBody>
      </InstrumentCard>

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
              <motion.tbody initial="hidden" animate="visible" variants={tableBodyVariants}>
                {events.map((e) => (
                  <motion.tr
                    key={e.id}
                    variants={tableRowVariants}
                    className="border-b border-border hover:bg-surface-1 transition-colors"
                  >
                    <td className="px-5 py-3 font-mono text-xs text-muted-foreground whitespace-nowrap">
                      {formatRelativeTime(e.timestamp)}
                    </td>
                    <td className="px-5 py-3">
                      <StatusBadge
                        variant={actionVariant(e.action)}
                        className="font-mono"
                      >
                        {e.action}
                      </StatusBadge>
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
                  </motion.tr>
                ))}
              </motion.tbody>
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
