/*
 * DESIGN: "Control Surface" — Audit Log (human-readable rewrite, task-c8d4b056)
 *
 * The page now renders the backend's human-readable attribution: a primary
 * Summary column ("who/what did what, and why"), named Agent/Event/Severity/
 * Decision columns, a Hide system/routine toggle (server category=governance),
 * and a drawer that explains who/what acted, where, why, and surfaces redacted
 * input/output previews + trace/artifact pointers — never a raw payload dump.
 * Download uses the shared RFC-4180 CSV helper (lib/export) with a partial-slice
 * warning so a filtered/visible export is never mistaken for the audit-complete
 * compliance bundle. Rows source from the SIEM feed GET /api/v1/audit/events via
 * useInfiniteAuditEvents (mapped to the shared AuditEntry shape).
 */
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useQueryState, parseAsString } from "nuqs";
import { parseAsSearchTerm } from "@/lib/url-state";
import type { ColumnDef } from "@tanstack/react-table";
import { get } from "@/api/client";
import {
  useInfiniteAuditEvents,
  type AuditEventsFilters,
} from "@/hooks/useAuditEvents";
import type { AuditEntry } from "@/api/types";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonTable } from "@/components/ui/Skeleton";
import { Input } from "@/components/ui/Input";
import { Select } from "@/components/ui/Select";
import { LabeledField } from "@/components/ui/LabeledField";
import { InstrumentCard, InstrumentCardBody } from "@/components/ui/InstrumentCard";
import { Drawer } from "@/components/ui/Drawer";
import { ChainIntegrityWidget } from "@/components/ChainIntegrityWidget";
import {
  DataTable,
  type DecisionTier,
} from "@/components/primitives/DataTable";
import {
  Search,
  RefreshCw,
  FileText,
  Download,
  Calendar,
  Bot,
  EyeOff,
  Eye,
  X,
  Copy,
} from "lucide-react";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import { cn, formatRelativeTime } from "@/lib/utils";
import { toCsv, downloadFile } from "@/lib/export";
import { toast } from "sonner";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { useConfigStore } from "@/state/config";
import {
  useAuditVerify,
  type AuditVerifyResult,
} from "@/hooks/useAuditVerify";
import { useIsAdmin } from "@/hooks/usePermission";

const PAGE_LIMIT = 200;

const DECISION_TIERS: ReadonlySet<DecisionTier> = new Set([
  "allow",
  "deny",
  "require_approval",
  "allow_with_constraints",
  "throttle",
]);

// AUDIT_EXPORT_HEADERS is the human-readable CSV column order for the
// dashboard "visible rows" export. This is the dashboard download (the loaded
// slice) — distinct from the backend audit-complete compliance bundle, whose
// column contract lives in core/audit/export_compliance.go.
export const AUDIT_EXPORT_HEADERS = [
  "Summary",
  "Timestamp",
  "Category",
  "Severity",
  "Event Type",
  "Actor",
  "Agent",
  "Resource",
  "Action",
  "Decision",
  "Rule",
  "Reason",
  "Job ID",
  "Session ID",
  "Execution ID",
  "Chain Seq",
  "Event Hash",
  "Prev Hash",
  "Input Preview",
  "Output Preview",
  "Trace ID",
  "Artifact ID",
] as const;

// csvCell neutralises spreadsheet formula-injection at the cell source: a cell
// beginning with =,+,-,@,TAB,CR is prefixed with a single quote (OWASP). RFC
// comma/quote/newline escaping is handled by toCsv.
function csvCell(value: string | number | undefined | null): string {
  const s = value === undefined || value === null ? "" : String(value);
  if (s && /^[=+\-@\t\r]/.test(s)) return "'" + s;
  return s;
}

// buildAuditExportRows projects the loaded AuditEntry rows onto
// AUDIT_EXPORT_HEADERS, formula-guarding every cell. Exported + pure so the
// CSV shape is unit-testable without rendering the page.
export function buildAuditExportRows(entries: AuditEntry[]): string[][] {
  return entries.map((e) =>
    [
      e.humanSummary ?? "",
      e.timestamp ?? "",
      e.governanceCategory ?? "",
      e.severity ?? "",
      e.eventType ?? "",
      e.actorLabel ?? e.actor ?? "",
      e.agentLabel ?? e.agentName ?? "",
      e.resourceLabel ?? e.resourceType ?? "",
      e.action ?? "",
      e.decision ?? "",
      e.matchedRule ?? "",
      e.reason ?? e.message ?? "",
      e.jobId ?? "",
      e.sessionId ?? "",
      e.executionId ?? "",
      e.seq !== undefined ? String(e.seq) : "",
      e.eventHash ?? "",
      e.prevHash ?? "",
      e.inputPreview ?? "",
      e.outputPreview ?? "",
      e.traceId ?? "",
      e.artifactId ?? "",
    ].map(csvCell),
  );
}

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

function severityVariant(severity?: string): BadgeVariant {
  switch (severity) {
    case "high":
      return "danger";
    case "medium":
      return "warning";
    default:
      return "muted";
  }
}

function decisionVariant(decision?: string): BadgeVariant {
  if (!decision) return "muted";
  if (decision === "allow" || decision === "approved") return "healthy";
  if (decision === "deny" || decision === "rejected") return "danger";
  return "warning";
}

function decisionAccessor(event: AuditEntry): DecisionTier | undefined {
  const d = event.decision;
  if (!d) return undefined;
  return DECISION_TIERS.has(d as DecisionTier) ? (d as DecisionTier) : undefined;
}

interface AgentOption {
  id: string;
  name: string;
}

export default function AuditLogPage() {
  const nextPageSentinelRef = useRef<HTMLDivElement | null>(null);
  const nextPageFetchPendingRef = useRef(false);
  const tenantId = useConfigStore((s) => s.tenantId);
  const isAdmin = useIsAdmin();

  const [search, setSearch] = useQueryState(
    "search",
    parseAsSearchTerm.withOptions({ clearOnDefault: true }),
  );
  const [actionFilter, setActionFilter] = useQueryState(
    "action",
    parseAsString.withDefault("").withOptions({ clearOnDefault: true }),
  );
  const [agentFilter, setAgentFilter] = useQueryState(
    "agent",
    parseAsString.withDefault("").withOptions({ clearOnDefault: true }),
  );
  const [dateFrom, setDateFrom] = useQueryState(
    "from",
    parseAsString.withDefault("").withOptions({ clearOnDefault: true }),
  );
  const [dateTo, setDateTo] = useQueryState(
    "to",
    parseAsString.withDefault("").withOptions({ clearOnDefault: true }),
  );
  // Hide system/routine audit — backed by the server governance/routine
  // taxonomy: hide_system=1 narrows the feed to category=governance so an
  // investigator sees only consequential rows (denials, approvals, policy
  // decisions, auth changes) and not the high-volume routine telemetry.
  const [hideSystem, setHideSystem] = useQueryState(
    "hide_system",
    parseAsString.withDefault("").withOptions({ clearOnDefault: true }),
  );
  const [eventTypePrefix] = useQueryState(
    "event_type_prefix",
    parseAsString.withDefault("").withOptions({ clearOnDefault: true }),
  );
  const [agents, setAgents] = useState<AgentOption[]>([]);
  const [expandedEventId, setExpandedEventId] = useState<string | null>(null);

  const systemHidden = hideSystem === "1";

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

  // Server-side filters. event_type/from/to/search/category/agent_id are
  // narrowed by the gateway; eventTypePrefix is the only client-side residue
  // (a redirect convenience param).
  const auditFilters = useMemo<AuditEventsFilters>(() => {
    const p: AuditEventsFilters = { limit: PAGE_LIMIT };
    if (actionFilter) p.eventType = [actionFilter];
    if (dateFrom) {
      const d = new Date(dateFrom);
      if (!Number.isNaN(d.getTime())) p.from = d.toISOString();
    }
    if (dateTo) {
      const d = new Date(dateTo + "T23:59:59");
      if (!Number.isNaN(d.getTime())) p.to = d.toISOString();
    }
    if (search) p.search = search;
    if (systemHidden) p.category = "governance";
    if (agentFilter) p.agentId = agentFilter;
    return p;
  }, [actionFilter, dateFrom, dateTo, search, systemHidden, agentFilter]);

  const {
    items,
    isLoading,
    isError,
    error,
    refetch,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
  } = useInfiniteAuditEvents(auditFilters);

  useEffect(() => {
    if (!isFetchingNextPage) {
      nextPageFetchPendingRef.current = false;
    }
  }, [isFetchingNextPage]);

  useEffect(() => {
    const node = nextPageSentinelRef.current;
    if (!node || typeof IntersectionObserver === "undefined") return;
    const observer = new IntersectionObserver(
      (entries) => {
        if (
          shouldFetchNextAuditPage(entries, !!hasNextPage, isFetchingNextPage) &&
          !nextPageFetchPendingRef.current
        ) {
          nextPageFetchPendingRef.current = true;
          void fetchNextPage().finally(() => {
            nextPageFetchPendingRef.current = false;
          });
        }
      },
      { rootMargin: "320px 0px" },
    );
    observer.observe(node);
    return () => observer.disconnect();
  }, [fetchNextPage, hasNextPage, isFetchingNextPage]);

  const events: AuditEntry[] = useMemo(() => {
    if (!eventTypePrefix) return items;
    const prefix = eventTypePrefix.toLowerCase();
    return items.filter((e) => e.eventType.toLowerCase().startsWith(prefix));
  }, [items, eventTypePrefix]);

  const expandedEvent = useMemo(
    () => events.find((e) => e.id === expandedEventId) ?? null,
    [events, expandedEventId],
  );

  const filtersActive =
    !!actionFilter ||
    !!agentFilter ||
    !!dateFrom ||
    !!dateTo ||
    !!search ||
    systemHidden;
  const activeFilterCount = [
    actionFilter,
    agentFilter,
    dateFrom,
    dateTo,
    search,
    systemHidden ? "1" : "",
  ].filter(Boolean).length;

  const clearFilters = () => {
    void setSearch(null);
    void setActionFilter(null);
    void setAgentFilter(null);
    void setDateFrom(null);
    void setDateTo(null);
    void setHideSystem(null);
  };

  const exportCSV = () => {
    const rows = buildAuditExportRows(events);
    const csv = toCsv([...AUDIT_EXPORT_HEADERS], rows);
    const dateSuffix =
      dateFrom || dateTo ? `-${dateFrom || "start"}-${dateTo || "now"}` : "";
    downloadFile(
      csv,
      `audit-visible-${new Date().toISOString().slice(0, 10)}${dateSuffix}.csv`,
      "text/csv;charset=utf-8",
    );
    if (hasNextPage) {
      toast.info(
        `Exported ${events.length} visible rows. More rows exist — scroll to load them, or use the compliance export for an audit-complete bundle.`,
      );
    } else if (filtersActive) {
      toast.success(
        `Exported ${events.length} filtered rows (the currently visible slice).`,
      );
    } else {
      toast.success(`Exported ${events.length} visible rows.`);
    }
  };

  const columns = useMemo<ColumnDef<AuditEntry>[]>(
    () => [
      {
        id: "time",
        header: "Time",
        accessorFn: (e) => e.timestamp,
        cell: ({ row }) => (
          <span className="font-mono text-xs text-muted-foreground whitespace-nowrap">
            {formatRelativeTime(row.original.timestamp)}
          </span>
        ),
      },
      {
        id: "summary",
        header: "Summary",
        enableSorting: false,
        cell: ({ row }) => {
          const e = row.original;
          return (
            <div className="min-w-0 max-w-[520px]">
              <p className="truncate text-sm text-foreground">
                {e.humanSummary || e.action || e.eventType}
              </p>
              <p className="mt-0.5 truncate font-mono text-[11px] text-muted-foreground">
                {e.eventType}
                {e.resourceLabel ? ` · ${e.resourceLabel}` : ""}
              </p>
            </div>
          );
        },
      },
      {
        id: "agent",
        header: "Agent",
        accessorFn: (e) => e.agentLabel ?? "",
        cell: ({ row }) => {
          const e = row.original;
          const label = e.agentLabel || e.agentName;
          if (!label) {
            return <span className="text-xs text-muted-foreground">—</span>;
          }
          return (
            <div className="min-w-0">
              <span className="block truncate text-sm text-cordum">{label}</span>
              {e.agentId && (
                <span className="block truncate font-mono text-[11px] text-muted-foreground">
                  {e.agentId}
                </span>
              )}
            </div>
          );
        },
      },
      {
        id: "severity",
        header: "Severity",
        accessorFn: (e) => e.severity ?? "",
        cell: ({ row }) =>
          row.original.severity ? (
            <StatusBadge variant={severityVariant(row.original.severity)}>
              {row.original.severity}
            </StatusBadge>
          ) : (
            <span className="text-xs text-muted-foreground">—</span>
          ),
      },
      {
        id: "decision",
        header: "Decision",
        accessorFn: (e) => e.decision ?? "",
        cell: ({ row }) => {
          const d = row.original.decision;
          if (!d) {
            return <span className="text-xs text-muted-foreground">—</span>;
          }
          return (
            <StatusBadge variant={decisionVariant(d)} className="font-mono">
              {d}
            </StatusBadge>
          );
        },
      },
    ],
    [],
  );

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
        subtitle="Who did what, when, where, and why — across the whole platform"
        actions={
          <div className="flex gap-2">
            <Button variant="outline" size="sm" onClick={() => refetch()}>
              <RefreshCw className="w-3 h-3 mr-1" />
              Refresh
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={exportCSV}
              title="Download the currently loaded rows. For an audit-complete, chain-verified bundle use the compliance export."
            >
              <Download className="w-3 h-3 mr-1" />
              Export visible (CSV)
            </Button>
          </div>
        }
      />

      <div className="sticky top-0 z-10 -mx-4 px-4 pt-2 pb-1 bg-background/80 backdrop-blur-sm sm:mx-0 sm:px-0">
        <ChainIntegrityWidget tenant={tenantId} compact />
      </div>

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
                  placeholder="Summary, agent, session, rule…"
                  value={search}
                  onChange={(e) => void setSearch(e.target.value || null)}
                  aria-label="Search audit events"
                  className="bg-surface-1"
                />
              </LabeledField>

              <LabeledField label="Event type">
                <Select
                  value={actionFilter}
                  onChange={(e) => void setActionFilter(e.target.value || null)}
                  aria-label="Filter by event type"
                  className="bg-surface-1"
                >
                  <option value="">All event types</option>
                  <optgroup label="Safety / Policy">
                    <option value="safety.decision">Safety decision</option>
                    <option value="safety.approval">Safety approval</option>
                    <option value="safety.policy_change">Policy change</option>
                  </optgroup>
                  <optgroup label="MCP">
                    <option value="mcp.tool_invocation">MCP tool invocation</option>
                    <option value="mcp.tool_approval">MCP tool approval</option>
                    <option value="mcp.tool_denied">MCP tool denied</option>
                    <option value="mcp.signature_invalid">MCP signature invalid</option>
                  </optgroup>
                  <optgroup label="Edge (Claude Code)">
                    <option value="edge.session_started">Edge session started</option>
                    <option value="edge.action_attempted">Edge action attempted</option>
                    <option value="edge.policy_decision">Edge policy decision</option>
                    <option value="edge.action_denied">Edge action denied</option>
                    <option value="edge.approval_requested">Edge approval requested</option>
                    <option value="edge.fail_closed">Edge fail closed</option>
                  </optgroup>
                  <optgroup label="Worker / Topic">
                    <option value="worker_trust_change">Worker trust change</option>
                    <option value="topic_registered">Topic registered</option>
                    <option value="topic_unregistered">Topic unregistered</option>
                  </optgroup>
                  <optgroup label="Auth">
                    <option value="system.auth">System auth</option>
                    <option value="auth.api_key_created">API key created</option>
                    <option value="auth.api_key_revoked">API key revoked</option>
                    <option value="auth.role_upserted">Role upserted</option>
                    <option value="auth.role_deleted">Role deleted</option>
                  </optgroup>
                  <optgroup label="Delegation">
                    <option value="delegation.lineage">Delegation lineage</option>
                    <option value="delegation.rejected">Delegation rejected</option>
                  </optgroup>
                  <optgroup label="License">
                    <option value="license.legacy_format_rejected">License legacy rejected</option>
                    <option value="license.breakglass_activated">License breakglass</option>
                  </optgroup>
                  <optgroup label="Action gates">
                    <option value="actiongate.denied">Action gate denied</option>
                  </optgroup>
                </Select>
              </LabeledField>

              {agents.length > 0 && (
                <LabeledField
                  label="Agent"
                  description="Filter by governed agent"
                  action={<Bot className="h-3.5 w-3.5 text-muted-foreground" />}
                >
                  <Select
                    value={agentFilter}
                    onChange={(e) => void setAgentFilter(e.target.value || null)}
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
                    onChange={(e) => void setDateFrom(e.target.value || null)}
                    aria-label="From date"
                    className="bg-surface-1"
                  />
                  <span className="text-center text-xs text-muted-foreground">
                    to
                  </span>
                  <Input
                    type="date"
                    value={dateTo}
                    onChange={(e) => void setDateTo(e.target.value || null)}
                    aria-label="To date"
                    className="bg-surface-1"
                  />
                </div>
              </LabeledField>
            </div>

            <div className="flex flex-wrap items-center gap-2 xl:justify-end">
              <Button
                variant={systemHidden ? "default" : "outline"}
                size="sm"
                onClick={() => void setHideSystem(systemHidden ? null : "1")}
                aria-pressed={systemHidden}
                aria-label={
                  systemHidden
                    ? "Show system/routine audit"
                    : "Hide system/routine audit"
                }
              >
                {systemHidden ? (
                  <Eye className="h-3 w-3" />
                ) : (
                  <EyeOff className="h-3 w-3" />
                )}
                {systemHidden
                  ? "Show system/routine audit"
                  : "Hide system/routine audit"}
              </Button>
              {filtersActive && (
                <StatusBadge variant="info">
                  {activeFilterCount} filter{activeFilterCount > 1 ? "s" : ""}{" "}
                  active
                </StatusBadge>
              )}
              <Button
                variant="ghost"
                size="sm"
                onClick={clearFilters}
                disabled={!filtersActive}
              >
                <X className="h-3 w-3" />
                Clear filters
              </Button>
            </div>
          </div>

          <div className="flex flex-wrap items-center gap-3 text-xs text-muted-foreground">
            <span>
              Showing {events.length} event{events.length === 1 ? "" : "s"}
              {filtersActive && " (filtered)"}
              {hasNextPage && " · more available"}
            </span>
            {systemHidden && (
              <span>System/routine rows are hidden (governance-only view).</span>
            )}
          </div>
        </InstrumentCardBody>
      </InstrumentCard>

      {isLoading ? (
        <div className="instrument-card">
          <SkeletonTable rows={10} />
        </div>
      ) : (
        <div className="instrument-card overflow-hidden">
          <DataTable
            columns={columns}
            data={events}
            decisionAccessor={decisionAccessor}
            disableVirtualization
            onRowClick={(event) => setExpandedEventId(event.id)}
            emptyState={
              systemHidden ? (
                <EmptyState
                  icon={<EyeOff className="w-5 h-5" />}
                  title="No governance events match"
                  description="Only governance events are shown — system/routine rows are hidden. Clear filters to see the full trail."
                  action={
                    <Button variant="outline" size="sm" onClick={clearFilters}>
                      Clear filters
                    </Button>
                  }
                />
              ) : (
                <EmptyState
                  icon={<FileText className="w-5 h-5" />}
                  title="No audit events"
                  description={
                    filtersActive
                      ? "No events match your filters"
                      : "Events will appear as actions occur in the system"
                  }
                  action={
                    filtersActive ? (
                      <Button variant="outline" size="sm" onClick={clearFilters}>
                        Clear filters
                      </Button>
                    ) : undefined
                  }
                />
              )
            }
          />
          <div ref={nextPageSentinelRef} aria-hidden="true" className="h-px" />
          <div
            className="flex flex-wrap items-center justify-center gap-3 border-t border-border bg-surface-1 p-4 text-sm text-muted-foreground"
            aria-live="polite"
          >
            {isFetchingNextPage ? (
              <span>Loading more audit events…</span>
            ) : hasNextPage ? (
              <>
                <span>Scroll for older audit events.</span>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => void fetchNextPage()}
                  disabled={isFetchingNextPage}
                  aria-label="Load more audit events"
                >
                  Load more
                </Button>
              </>
            ) : events.length > 0 ? (
              <span>End of audit trail.</span>
            ) : null}
          </div>
        </div>
      )}

      <Drawer
        open={expandedEvent !== null}
        onClose={() => setExpandedEventId(null)}
        size="lg"
        label="Audit event detail"
      >
        {expandedEvent && (
          <AuditEventDrilldown
            event={expandedEvent}
            tenantId={tenantId}
            isAdmin={isAdmin}
            onClose={() => setExpandedEventId(null)}
            onPivotAgent={(id) => {
              void setAgentFilter(id);
              setExpandedEventId(null);
            }}
          />
        )}
      </Drawer>
    </div>
  );
}

// ---------------------------------------------------------------------------
// AuditEventDrilldown — explains who/what acted, when, where, why, and surfaces
// redacted input/output previews + trace/artifact pointers. NEVER dumps the raw
// extra payload as a JSON blob (the previews are the only content surfaced, and
// they are already bounded/redacted server-side).
// ---------------------------------------------------------------------------

interface AuditEventDrilldownProps {
  event: AuditEntry;
  tenantId: string;
  isAdmin: boolean;
  onClose: () => void;
  onPivotAgent: (agentId: string) => void;
}

function AuditEventDrilldown({
  event,
  tenantId,
  isAdmin,
  onClose,
  onPivotAgent,
}: AuditEventDrilldownProps) {
  const hasEvidence =
    !!event.inputPreview ||
    !!event.outputPreview ||
    !!event.traceId ||
    !!event.artifactId;

  return (
    <div className="space-y-6">
      <header className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="text-[10px] uppercase tracking-[0.18em] text-muted-foreground">
            Audit event
          </p>
          <h2 className="font-display text-lg font-semibold text-foreground">
            {event.humanSummary || event.action || event.eventType}
          </h2>
          <p className="mt-1 font-mono text-xs text-muted-foreground">
            {event.eventType} · {event.id}
          </p>
        </div>
        <button
          type="button"
          aria-label="Close drilldown"
          onClick={onClose}
          className="rounded-full border border-border p-1.5 text-muted-foreground hover:bg-surface-1"
        >
          <X className="h-3.5 w-3.5" />
        </button>
      </header>

      <DrillSection title="Who / what">
        <dl className="grid grid-cols-2 gap-3 text-xs">
          <DrillRow label="Actor" value={event.actorLabel || event.actor} />
          {(event.agentLabel || event.agentName) && (
            <DrillRow label="Agent" value={event.agentLabel || event.agentName || ""} />
          )}
          {event.agentId && (
            <DrillRow
              label="Agent ID"
              value={event.agentId}
              mono
              onPivot={() => onPivotAgent(event.agentId as string)}
            />
          )}
          {event.agentProduct && (
            <DrillRow label="Product" value={event.agentProduct} />
          )}
        </dl>
      </DrillSection>

      <DrillSection title="When / where">
        <dl className="grid grid-cols-2 gap-3 text-xs">
          <DrillRow label="Time" value={event.timestamp || "—"} mono />
          <DrillRow
            label="Resource"
            value={event.resourceLabel || event.resourceType || "—"}
          />
          {event.sessionId && (
            <DrillRow label="Session" value={event.sessionId} mono />
          )}
          {event.executionId && (
            <DrillRow label="Execution" value={event.executionId} mono />
          )}
          {event.jobId && <DrillRow label="Job" value={event.jobId} mono />}
        </dl>
      </DrillSection>

      <DrillSection title="Why">
        <dl className="grid grid-cols-2 gap-3 text-xs">
          <DrillRow label="Decision" value={event.decision || "—"} mono />
          {event.matchedRule && (
            <DrillRow label="Matched rule" value={event.matchedRule} mono />
          )}
          {event.severity && (
            <DrillRow label="Severity" value={event.severity} />
          )}
        </dl>
        {event.reason && (
          <p className="mt-2 whitespace-pre-wrap text-sm text-foreground">
            {event.reason}
          </p>
        )}
      </DrillSection>

      <DrillSection title="Evidence">
        {hasEvidence ? (
          <div className="space-y-2 text-xs">
            {event.inputPreview && (
              <PreviewBlock label="Input preview" value={event.inputPreview} />
            )}
            {event.outputPreview && (
              <PreviewBlock label="Output preview" value={event.outputPreview} />
            )}
            <dl className="grid grid-cols-2 gap-3">
              {event.traceId && (
                <DrillRow label="Trace" value={event.traceId} mono />
              )}
              {event.artifactId && (
                <DrillRow label="Artifact" value={event.artifactId} mono />
              )}
            </dl>
          </div>
        ) : (
          <p className="text-xs text-muted-foreground">
            No redacted preview captured for this event. Raw input/output is
            never dumped here — see the compliance export for permissioned
            artifact pointers.
          </p>
        )}
      </DrillSection>

      <ChainSignatureSection
        event={event}
        tenantId={tenantId}
        isAdmin={isAdmin}
      />
    </div>
  );
}

interface DrillSectionProps {
  title: string;
  children: ReactNode;
}

function DrillSection({ title, children }: DrillSectionProps) {
  return (
    <section>
      <p className="mb-2 text-[10px] uppercase tracking-[0.18em] text-muted-foreground">
        {title}
      </p>
      {children}
    </section>
  );
}

interface PreviewBlockProps {
  label: string;
  value: string;
}

function PreviewBlock({ label, value }: PreviewBlockProps) {
  return (
    <div className="rounded-lg border border-border bg-surface-1 p-2">
      <p className="text-[10px] uppercase tracking-[0.18em] text-muted-foreground">
        {label}
      </p>
      <p className="mt-1 whitespace-pre-wrap break-words font-mono text-xs text-foreground">
        {value}
      </p>
    </div>
  );
}

interface DrillRowProps {
  label: string;
  value: string;
  mono?: boolean;
  onPivot?: () => void;
}

function DrillRow({ label, value, mono, onPivot }: DrillRowProps) {
  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      toast.success(`Copied ${label.toLowerCase()}`);
    } catch {
      toast.error("Copy failed");
    }
  };

  return (
    <div className="flex flex-col gap-0.5">
      <dt className="text-[10px] uppercase tracking-[0.18em] text-muted-foreground">
        {label}
      </dt>
      <dd className={cn("flex items-center gap-1.5 text-sm", mono && "font-mono")}>
        <span className="truncate">{value}</span>
        {onPivot && value && value !== "—" && (
          <button
            type="button"
            aria-label={`Filter by ${label}`}
            onClick={onPivot}
            className="shrink-0 rounded p-0.5 text-muted-foreground hover:bg-surface-1 hover:text-foreground"
            data-row-action
          >
            <Search className="h-3 w-3" />
          </button>
        )}
        {value && value !== "—" && (
          <button
            type="button"
            aria-label={`Copy ${label}`}
            onClick={handleCopy}
            className="shrink-0 rounded p-0.5 text-muted-foreground hover:bg-surface-1 hover:text-foreground"
            data-row-action
          >
            <Copy className="h-3 w-3" />
          </button>
        )}
      </dd>
    </div>
  );
}

interface ChainSignatureSectionProps {
  event: AuditEntry;
  tenantId: string;
  isAdmin: boolean;
}

function ChainSignatureSection({
  event,
  tenantId,
  isAdmin,
}: ChainSignatureSectionProps) {
  const verify = useAuditVerify({ tenant: tenantId, enabled: isAdmin });

  if (!isAdmin) {
    return (
      <section className="rounded-2xl border border-border bg-surface-1 p-4">
        <p className="text-[10px] uppercase tracking-[0.18em] text-muted-foreground">
          Chain signature
        </p>
        <p className="mt-2 text-sm text-foreground">
          Chain integrity verification requires admin role.
        </p>
        <p className="mt-1 text-xs text-muted-foreground">
          See <span className="font-mono">/govern/verification</span> for
          read-only chain status.
        </p>
      </section>
    );
  }

  return (
    <section className="rounded-2xl border border-border bg-surface-1 p-4 space-y-2">
      <p className="text-[10px] uppercase tracking-[0.18em] text-muted-foreground">
        Chain signature
      </p>
      {event.seq !== undefined && (
        <p className="font-mono text-xs text-muted-foreground">
          seq #{event.seq}
          {event.eventHash ? ` · ${event.eventHash.slice(0, 16)}…` : ""}
        </p>
      )}
      <ChainSignatureVerdict event={event} verify={verify} />
    </section>
  );
}

interface ChainSignatureVerdictProps {
  event: AuditEntry;
  verify: {
    data: AuditVerifyResult | undefined;
    isLoading: boolean;
    isError: boolean;
  };
}

// Three-state badge: verified / unverified / pending. Pending is the safe
// default while the chain verdict is in flight, errored, absent, or when the
// row's seq is outside the cached verified range / pruned by retention.
function ChainSignatureVerdict({
  event,
  verify,
}: ChainSignatureVerdictProps) {
  if (verify.isLoading) {
    return (
      <div className="space-y-1">
        <StatusBadge variant="muted">Pending</StatusBadge>
        <p className="text-xs text-muted-foreground">
          Loading chain verification for event{" "}
          <span className="font-mono">{event.id}</span>…
        </p>
      </div>
    );
  }

  if (verify.isError || !verify.data) {
    return (
      <div className="space-y-1">
        <StatusBadge variant="muted">Pending</StatusBadge>
        <p className="text-xs text-muted-foreground">
          Chain verification result unavailable for event{" "}
          <span className="font-mono">{event.id}</span>. Try again or check{" "}
          <span className="font-mono">/govern/verification</span>.
        </p>
      </div>
    );
  }

  const chain = verify.data;

  if (event.seq === undefined) {
    return (
      <div className="space-y-1">
        <StatusBadge variant="muted">Pending</StatusBadge>
        <p className="text-xs text-muted-foreground">
          This entry has no chain seq — policy-only audit entries are not
          included in the Merkle chain.
        </p>
      </div>
    );
  }

  if (event.seq < chain.retention_boundary_seq) {
    return (
      <div className="space-y-1">
        <StatusBadge variant="muted">Pending</StatusBadge>
        <p className="text-xs text-muted-foreground">
          Chain seq #{event.seq} is older than the retention window
          ({chain.retention_window_hours ?? "?"}h). Signature evidence has been
          pruned (retention-trimmed).
        </p>
      </div>
    );
  }

  const tamperGap = chain.gaps.find(
    (g) =>
      g.at_seq === event.seq &&
      (g.type === "missing" ||
        g.type === "hash_mismatch" ||
        g.type === "out_of_order"),
  );

  if (tamperGap) {
    return (
      <div className="space-y-1">
        <StatusBadge variant="danger" dot>
          Unverified
        </StatusBadge>
        <p className="text-xs text-muted-foreground">
          Chain seq #{event.seq} failed verification — tamper detected
          ({tamperGap.type.replace(/_/g, " ")}). Investigate via
          /govern/verification before relying on this entry for compliance
          export.
        </p>
      </div>
    );
  }

  const inVerifiedRange =
    chain.verified_events > 0 &&
    chain.first_seq !== undefined &&
    chain.last_seq !== undefined &&
    event.seq >= chain.first_seq &&
    event.seq <= chain.last_seq;

  if (!inVerifiedRange) {
    const rangeSuffix =
      chain.first_seq !== undefined && chain.last_seq !== undefined
        ? ` (${chain.first_seq}…${chain.last_seq})`
        : "";
    const emptySuffix =
      chain.verified_events === 0 ? " (no events verified yet)" : "";
    return (
      <div className="space-y-1">
        <StatusBadge variant="muted">Pending</StatusBadge>
        <p className="text-xs text-muted-foreground">
          Chain seq #{event.seq} is outside the cached verification range
          {rangeSuffix}
          {emptySuffix}. Awaiting a fresh chain verdict that covers this seq.
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-1">
      <StatusBadge variant="healthy" dot>
        Verified
      </StatusBadge>
      <p className="text-xs text-muted-foreground">
        Chain seq #{event.seq} is signed and present in the verified Merkle
        window ({chain.verified_events} of {chain.total_events} events).
      </p>
    </div>
  );
}
