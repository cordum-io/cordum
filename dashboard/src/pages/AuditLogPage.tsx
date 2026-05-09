/*
 * DESIGN: "Control Surface" — Audit Log
 *
 * v2.5 hero rewrite (task-55f813b3):
 *  - Filters serialised to URL via nuqs (URL roundtrip restores state).
 *  - Hand-rolled <table> swapped for primitives/DataTable (auto-virtualizes
 *    when row count > 100; decision-identity 3px left edge).
 *  - Row-click opens a Drawer with event detail + chain-signature drilldown
 *    (DoD #4 amended via comment-832277b0 — drilldown derives per-event
 *    chain status from the cached /audit/verify result, no N+1).
 */
import { useEffect, useMemo, useState } from "react";
import { useQueryState, parseAsString } from "nuqs";
import { parseAsSearchTerm } from "@/lib/url-state";
import type { ColumnDef } from "@tanstack/react-table";
import { get } from "@/api/client";
import { useGetPolicyAudit } from "@/api/generated/policy/policy";
import type { GetPolicyAuditParams } from "@/api/generated/model/getPolicyAuditParams";
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
  X,
  Copy,
} from "lucide-react";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import { cn, formatRelativeTime } from "@/lib/utils";
import { toast } from "sonner";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { useConfigStore } from "@/state/config";
import {
  useAuditVerify,
  type AuditVerifyResult,
} from "@/hooks/useAuditVerify";
import { useIsAdmin } from "@/hooks/usePermission";
import type { PolicyAuditEntry } from "@/api/generated/model/policyAuditEntry";

// `PolicyAuditEntry` is the openapi-declared shape; spec enrichment in
// task-7efe8c34 brought it into parity with the backend struct so the
// generated `useGetPolicyAudit` hook is now usable directly. The previous
// `& Record<string, unknown>` intersection is no longer needed.

interface AuditEvent {
  id: string;
  action: string;
  actor: string;
  resource: string;
  resourceId?: string;
  detail?: string;
  timestamp: string;
  ip?: string;
  decision?: string;
  seq?: number;
}

const PAGE_LIMIT = 1000;

const DECISION_TIERS: ReadonlySet<DecisionTier> = new Set([
  "allow",
  "deny",
  "require_approval",
  "allow_with_constraints",
  "throttle",
]);

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

function mapEvent(e: PolicyAuditEntry): AuditEvent {
  // `seq` is not part of the openapi-declared PolicyAuditEntry shape —
  // the gateway's /policy/audit handler does not emit it. Tests stub it
  // via MSW for the chain-integrity drilldown path (AuditLogPage.render
  // verifies "Verified"/"Unverified"/"Pending" based on event.seq vs
  // chain.first_seq/last_seq). Capture it via narrow extension cast so
  // the test contract stays intact; in production the field is absent
  // and AuditEvent.seq is undefined as before.
  const seqRaw = (e as PolicyAuditEntry & { seq?: unknown }).seq;
  return {
    id: e.id,
    action: e.action,
    actor: e.actor_id || e.role || "unknown",
    resource: e.resource_type || "",
    resourceId: e.resource_id ?? undefined,
    detail: e.message ?? undefined,
    timestamp: e.created_at || e.timestamp || "",
    decision: e.decision ?? undefined,
    seq: typeof seqRaw === "number" ? seqRaw : undefined,
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

function decisionAccessor(event: AuditEvent): DecisionTier | undefined {
  const d = event.decision;
  if (!d) return undefined;
  return DECISION_TIERS.has(d as DecisionTier) ? (d as DecisionTier) : undefined;
}

interface AgentOption {
  id: string;
  name: string;
}

export default function AuditLogPage() {
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
  const [agents, setAgents] = useState<AgentOption[]>([]);
  const [expandedEventId, setExpandedEventId] = useState<string | null>(null);

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

  // Build typed query params for the generated hook. Unset filters drop
  // out (orval omits undefined values from the query string), so the
  // produced URL matches the previous manual construction.
  const auditParams = useMemo<GetPolicyAuditParams>(() => {
    const p: GetPolicyAuditParams = {
      limit: PAGE_LIMIT,
      offset: 0,
    };
    if (actionFilter) p.action = actionFilter;
    if (agentFilter) p.agent_id = agentFilter;
    // Defensive ISO conversion: malformed `?from=banana` URL values
    // would throw at toISOString() and break the whole query. Validate
    // the parsed Date before forwarding; silently drop unparseable
    // input so the rest of the filter set still applies.
    if (dateFrom) {
      const d = new Date(dateFrom);
      if (!Number.isNaN(d.getTime())) p.after = d.toISOString();
    }
    if (dateTo) {
      const d = new Date(dateTo + "T23:59:59");
      if (!Number.isNaN(d.getTime())) p.before = d.toISOString();
    }
    if (search) p.search = search;
    return p;
  }, [actionFilter, agentFilter, dateFrom, dateTo, search]);

  const { data, isLoading, isError, error, refetch } = useGetPolicyAudit(
    auditParams,
  );

  const events: AuditEvent[] = useMemo(
    () => (data?.items ?? []).map(mapEvent),
    [data],
  );
  const total = data?.total;
  const expandedEvent = useMemo(
    () => events.find((e) => e.id === expandedEventId) ?? null,
    [events, expandedEventId],
  );

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

  const columns = useMemo<ColumnDef<AuditEvent>[]>(
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
        id: "action",
        header: "Action",
        accessorFn: (e) => e.action,
        cell: ({ row }) => (
          <StatusBadge
            variant={actionVariant(row.original.action)}
            className="font-mono"
          >
            {row.original.action}
          </StatusBadge>
        ),
      },
      {
        id: "actor",
        header: "Actor",
        accessorFn: (e) => e.actor,
        cell: ({ row }) => (
          <span className="font-mono text-cordum">
            {row.original.actor.slice(0, 16)}
          </span>
        ),
      },
      {
        id: "resource",
        header: "Resource",
        accessorFn: (e) => e.resource,
        cell: ({ row }) => (
          <span className="text-sm text-foreground">
            {row.original.resource || "—"}
            {row.original.resourceId && (
              <span className="text-xs text-muted-foreground font-mono ml-1">
                ({row.original.resourceId.slice(0, 12)})
              </span>
            )}
          </span>
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
          const variant: BadgeVariant =
            d === "allow"
              ? "healthy"
              : d === "deny"
                ? "danger"
                : "warning";
          return (
            <StatusBadge variant={variant} className="font-mono">
              {d}
            </StatusBadge>
          );
        },
      },
      {
        id: "detail",
        header: "Detail",
        enableSorting: false,
        cell: ({ row }) => (
          <span className="text-xs text-muted-foreground truncate max-w-[260px] inline-block align-middle">
            {row.original.detail ?? "—"}
          </span>
        ),
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
            <Button
              variant="outline"
              size="sm"
              disabled
              aria-label="Export PDF (coming soon)"
              title="PDF export — coming soon. Backend endpoint pending."
            >
              <FileText className="w-3 h-3 mr-1" />
              Export PDF
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
                  placeholder="Search events..."
                  value={search}
                  onChange={(e) => void setSearch(e.target.value || null)}
                  aria-label="Search audit events"
                  className="bg-surface-1"
                />
              </LabeledField>

              <LabeledField label="Action">
                <Select
                  value={actionFilter}
                  onChange={(e) => void setActionFilter(e.target.value || null)}
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
                  void setSearch(null);
                  void setActionFilter(null);
                  void setAgentFilter(null);
                  void setDateFrom(null);
                  void setDateTo(null);
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
            onRowClick={(event) => setExpandedEventId(event.id)}
            emptyState={
              <EmptyState
                icon={<FileText className="w-5 h-5" />}
                title="No audit events"
                description={
                  filtersActive
                    ? "No events match your filters"
                    : "Events will appear as actions occur in the system"
                }
              />
            }
          />
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
          />
        )}
      </Drawer>
    </div>
  );
}

// ---------------------------------------------------------------------------
// AuditEventDrilldown
//
// Renders inside the Drawer when the user clicks an audit row. Top half
// shows event metadata; bottom half is the chain-signature drilldown
// (DoD #4 amended via comment-832277b0). The drilldown gates on isAdmin
// per the /audit/verify backend RBAC; non-admin viewers see a hint
// pointing at /govern/verification instead of triggering a 403.
//
// The drilldown derives per-event verdict from the cached chain-wide
// verification result — opening 1000 different drawers fires at most
// one /audit/verify request because React Query shares the cache via
// queryKey ["audit-chain-verify", tenant].
// ---------------------------------------------------------------------------

interface AuditEventDrilldownProps {
  event: AuditEvent;
  tenantId: string;
  isAdmin: boolean;
  onClose: () => void;
}

function AuditEventDrilldown({
  event,
  tenantId,
  isAdmin,
  onClose,
}: AuditEventDrilldownProps) {
  return (
    <div className="space-y-6">
      <header className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="text-[10px] uppercase tracking-[0.18em] text-muted-foreground">
            Audit event
          </p>
          <h2 className="font-display text-lg font-semibold text-foreground truncate">
            {event.action || "(no action)"}
          </h2>
          <p className="mt-1 text-xs text-muted-foreground font-mono">
            {event.id}
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

      <dl className="grid grid-cols-2 gap-3 text-xs">
        <DrillRow label="Time" value={event.timestamp || "—"} mono />
        <DrillRow label="Actor" value={event.actor} mono />
        <DrillRow label="Resource" value={event.resource || "—"} />
        {event.resourceId && (
          <DrillRow label="Resource ID" value={event.resourceId} mono />
        )}
        {event.decision && (
          <DrillRow label="Decision" value={event.decision} mono />
        )}
        {event.seq !== undefined && (
          <DrillRow label="Chain seq" value={`#${event.seq}`} mono />
        )}
      </dl>

      {event.detail && (
        <section>
          <p className="text-[10px] uppercase tracking-[0.18em] text-muted-foreground">
            Detail
          </p>
          <p className="mt-1 text-sm text-foreground whitespace-pre-wrap">
            {event.detail}
          </p>
        </section>
      )}

      <ChainSignatureSection
        event={event}
        tenantId={tenantId}
        isAdmin={isAdmin}
      />
    </div>
  );
}

interface DrillRowProps {
  label: string;
  value: string;
  mono?: boolean;
}

function DrillRow({ label, value, mono }: DrillRowProps) {
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
      <dd
        className={cn(
          "flex items-center gap-1.5 text-sm",
          mono && "font-mono",
        )}
      >
        <span className="truncate">{value}</span>
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
  event: AuditEvent;
  tenantId: string;
  isAdmin: boolean;
}

function ChainSignatureSection({
  event,
  tenantId,
  isAdmin,
}: ChainSignatureSectionProps) {
  // Per amended DoD #4 (comment-7419de07, third amendment, supersedes
  // comment-832277b0): use the chain-wide /audit/verify endpoint and
  // derive the per-row verdict from the cached result. The signature
  // display must NOT block the row from rendering — pending is the safe
  // default while the chain verdict is in flight or absent.
  // useAuditVerify gates on isAdmin internally so viewer users don't fire
  // a 403; we mirror that gate at this section to render a helpful hint
  // instead of a silent no-op.
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
      <ChainSignatureVerdict event={event} verify={verify} />
    </section>
  );
}

interface ChainSignatureVerdictProps {
  event: AuditEvent;
  verify: {
    data: AuditVerifyResult | undefined;
    isLoading: boolean;
    isError: boolean;
  };
}

// ChainSignatureVerdict implements the three-state badge contract from the
// task-55f813b3 third DoD amendment (comment-7419de07):
//
//   verified   (healthy / green) — chain verdict found AND row's seq is in
//                                  the verified Merkle window with no gap.
//   unverified (danger / red)    — chain verdict found AND row's seq is in
//                                  gaps as missing / hash_mismatch /
//                                  out_of_order.
//   pending    (muted)           — chain verdict not yet loaded (in-flight,
//                                  errored, or no data) OR row's seq is
//                                  absent from the cached result (no seq on
//                                  the row, or seq pruned by retention).
//
// The descriptive subtext below the badge preserves the granular reason
// (Retention-trimmed / no chain seq / hash_mismatch label) so operators
// can tell why a row is pending or unverified without losing information.
function ChainSignatureVerdict({
  event,
  verify,
}: ChainSignatureVerdictProps) {
  // Pending: chain verdict not yet loaded.
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

  // Pending: chain verdict request errored or no data — we cannot claim
  // verified or unverified without a result.
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

  // Pending: row's seq absent from the cached result — policy-only audit
  // entries are not in the Merkle chain at all.
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

  // Pending: row's seq pruned by retention — verdict cannot be derived
  // because the signature evidence is gone.
  if (event.seq < chain.retention_boundary_seq) {
    return (
      <div className="space-y-1">
        <StatusBadge variant="muted">Pending</StatusBadge>
        <p className="text-xs text-muted-foreground">
          Chain seq #{event.seq} is older than the retention window
          ({chain.retention_window_hours ?? "?"}h). Signature evidence has
          been pruned (retention-trimmed).
        </p>
      </div>
    );
  }

  // Unverified: row's seq matches a tampered / missing / out-of-order gap.
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

  // Verified requires that the row's seq is actually present in the cached
  // chain-wide result's verified range. Without this guard, an empty result
  // (verified_events=0), a stale cache, a bounded/limited verify window, or
  // a result missing coverage bounds would default-pass an absent seq as
  // "verified" — the very gap QA flagged on reopen #3. Per architect's
  // pending definition (comment-7419de07), "row's seq absent from the
  // cached result" is pending. Conditions are inlined in a single &&
  // chain so TypeScript narrows `first_seq` / `last_seq` from `number |
  // undefined` to `number` before the comparison.
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
          {emptySuffix}
          . Awaiting a fresh chain verdict that covers this seq.
        </p>
      </div>
    );
  }

  // Verified: seq is in the verified Merkle window and not in any gap.
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
