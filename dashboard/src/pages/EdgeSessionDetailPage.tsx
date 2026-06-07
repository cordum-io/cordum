/*
 * EDGE-024: Edge Session Detail
 * Chronological timeline of governed agent actions for one Edge session.
 * Renders only redacted/summary fields — raw payloads stay server-side.
 */
import { useMemo, useState } from "react";
import { useParams, Link } from "react-router-dom";
import { motion } from "framer-motion";
import {
  ArrowLeft,
  Shield,
  ShieldCheck,
  ShieldAlert,
  Inbox,
  Layers,
  Workflow,
  GitBranch,
  Clock,
  ChevronRight,
  ChevronDown,
  AlertTriangle,
} from "lucide-react";
import type { AgentActionEvent, EdgeDecision } from "@/api/types";
import { EmptyState } from "@/components/ui/EmptyState";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { Skeleton } from "@/components/ui/Skeleton";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import {
  useEdgeSession,
  useEdgeSessionEvents,
  useEdgeExecutions,
} from "@/hooks/useEdgeSessions";
import { EdgeArtifactsPanel } from "@/components/edge/EdgeArtifactsPanel";
import { EdgeEventInspector } from "@/components/edge/EdgeEventInspector";
import { MCPLane } from "@/components/timeline/lanes/MCPLane";
import { cn, formatRelativeTime } from "@/lib/utils";
import { groupEdgeEvents, type EdgeEventGroup } from "@/lib/edge-event-groups";

type Filter = { executionId: string; decision: string; kind: string };

const decisionTone: Record<string, BadgeVariant> = {
  ALLOW: "healthy",
  DENY: "danger",
  REQUIRE_APPROVAL: "warning",
  REDACT: "warning",
  RECORDED: "info",
};

function decisionVariant(decision: EdgeDecision | string): BadgeVariant {
  return decisionTone[String(decision).toUpperCase()] ?? "info";
}

function statusVariant(status: string): BadgeVariant {
  switch (status) {
    case "running":
    case "starting":
      return "info";
    case "ended":
      return "healthy";
    case "failed":
      return "danger";
    case "degraded":
      return "warning";
    case "waiting_for_approval":
      return "governance";
    default:
      return "info";
  }
}

function applyFilter(events: AgentActionEvent[], filter: Filter): AgentActionEvent[] {
  return events.filter((event) => {
    if (filter.executionId && event.executionId !== filter.executionId) return false;
    if (filter.decision && String(event.decision) !== filter.decision) return false;
    if (filter.kind && event.kind !== filter.kind) return false;
    return true;
  });
}

function sortByOrder(a: AgentActionEvent, b: AgentActionEvent): number {
  if (a.executionId !== b.executionId) return a.executionId.localeCompare(b.executionId);
  if (a.seq !== b.seq) return a.seq - b.seq;
  return a.ts.localeCompare(b.ts);
}

export default function EdgeSessionDetailPage() {
  const { sessionId = "" } = useParams<{ sessionId: string }>();
  const sessionQuery = useEdgeSession(sessionId);
  const eventsQuery = useEdgeSessionEvents(sessionId, { limit: 500 });
  const executionsQuery = useEdgeExecutions({ sessionId });
  const [selectedEventId, setSelectedEventId] = useState<string | null>(null);
  const [filter, setFilter] = useState<Filter>({ executionId: "", decision: "", kind: "" });
  // EDGE-050 — UI surface for the 3-events-per-hook collapse. Default
  // hides pre-evaluation receipts (status=degraded agentd-receipt rows)
  // because they're plumbing the operator doesn't normally want to see;
  // toggle exposes them as standalone rows for audit-trail inspection.
  const [showReceipts, setShowReceipts] = useState(false);
  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(() => new Set());

  const session = sessionQuery.data;
  const events = useMemo(() => {
    const items = eventsQuery.data?.items ?? [];
    return [...items].sort(sortByOrder);
  }, [eventsQuery.data]);
  const visibleEvents = useMemo(() => applyFilter(events, filter), [events, filter]);
  // EDGE-050 — collapse 3-events-per-hook (receipt + gateway-decision +
  // agentd-evidence) into one logical row per hook fire. Underlying
  // events remain accessible via the expand caret per audit-verifiability
  // requirement (see docs/edge/identity-contract.md §1 dual-witness).
  const groups = useMemo(() => groupEdgeEvents(visibleEvents), [visibleEvents]);
  const decisions = useMemo(
    () => Array.from(new Set(events.map((event) => String(event.decision)))).sort(),
    [events],
  );
  const kinds = useMemo(
    () => Array.from(new Set(events.map((event) => event.kind))).sort(),
    [events],
  );
  const executions = executionsQuery.data?.items ?? [];
  const selectedEvent = useMemo(
    () => events.find((event) => event.eventId === selectedEventId) ?? null,
    [events, selectedEventId],
  );

  if (sessionQuery.isPending) {
    return (
      <div className="space-y-4 p-6">
        <Skeleton className="h-12 w-full max-w-2xl" />
        <Skeleton className="h-72 w-full" />
      </div>
    );
  }

  if (sessionQuery.error || !session) {
    return (
      <div className="space-y-4 p-6">
        <ErrorBanner
          title="Edge session unavailable"
          message={sessionQuery.error?.message ?? "Session not found"}
          onRetry={() => {
            void sessionQuery.refetch();
          }}
        />
      </div>
    );
  }

  return (
    <div className="space-y-6 p-6">
      <header className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0 space-y-2">
          <Link
            to="/edge/sessions"
            className="inline-flex items-center gap-1 text-xs uppercase tracking-[0.18em] text-muted-foreground hover:text-foreground"
          >
            <ArrowLeft className="h-3 w-3" /> Edge sessions
          </Link>
          <h1 className="break-all font-mono text-xl font-semibold text-foreground">{session.sessionId}</h1>
          <p className="text-sm text-muted-foreground">
            Tenant <span className="font-mono text-foreground">{session.tenantId}</span> · started{" "}
            {formatRelativeTime(session.startedAt)}
            {session.endedAt ? ` · ended ${formatRelativeTime(session.endedAt)}` : ""}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <StatusBadge variant={statusVariant(session.status)}>{session.status}</StatusBadge>
          <StatusBadge variant="info">{session.policyMode}</StatusBadge>
          <Link
            to="/approvals?lane=edge"
            className="inline-flex items-center gap-1.5 rounded-lg border border-border bg-surface-1 px-3 py-1.5 text-sm font-medium text-foreground hover:bg-surface-2 transition-colors"
          >
            <Inbox className="h-3.5 w-3.5" /> Approvals
          </Link>
        </div>
      </header>

      <SessionFacts session={session} />

      <section className="rounded-3xl border border-border bg-surface-1/70 p-4">
        <header className="flex flex-wrap items-end justify-between gap-3">
          <div>
            <p className="text-xs font-medium uppercase tracking-[0.2em] text-cordum">Timeline</p>
            <h2 className="mt-1 text-lg font-semibold text-foreground">Agent action events</h2>
            <p className="mt-1 text-sm text-muted-foreground">
              {events.length} event{events.length === 1 ? "" : "s"}
              {visibleEvents.length !== events.length ? ` · ${visibleEvents.length} after filter` : ""}
            </p>
          </div>
          <TimelineFilters
            filter={filter}
            setFilter={setFilter}
            executions={executions.map((execution) => execution.executionId)}
            decisions={decisions}
            kinds={kinds}
          />
        </header>

        <div className="mt-3 flex items-center gap-2">
          <label className="inline-flex cursor-pointer items-center gap-2 text-xs text-muted-foreground">
            <input
              type="checkbox"
              data-testid="edge-toggle-receipts"
              checked={showReceipts}
              onChange={(e) => setShowReceipts(e.target.checked)}
              className="h-3.5 w-3.5 rounded border-border accent-cordum"
            />
            <span>Show pre-evaluation receipts</span>
          </label>
        </div>

        {eventsQuery.isPending ? (
          <Skeleton className="mt-4 h-48 w-full" />
        ) : groups.length === 0 ? (
          <div className="mt-4">
            <EmptyState
              title="No events match"
              description={
                events.length === 0
                  ? "This Edge session has not emitted any agent action events yet."
                  : "Adjust the filters above to see events."
              }
            />
          </div>
        ) : (
          <ol className="mt-4 space-y-2" data-testid="edge-event-timeline">
            {groups.map((group) => (
              <TimelineGroupRow
                key={group.id}
                group={group}
                expanded={expandedGroups.has(group.id)}
                showReceipts={showReceipts}
                selectedEventId={selectedEventId}
                onToggleExpanded={() =>
                  setExpandedGroups((prev) => {
                    const next = new Set(prev);
                    if (next.has(group.id)) next.delete(group.id);
                    else next.add(group.id);
                    return next;
                  })
                }
                onSelectEvent={(eventId) => setSelectedEventId(eventId)}
              />
            ))}
          </ol>
        )}
      </section>

      <MCPLane events={events} />

      <EdgeArtifactsPanel sessionId={session.sessionId} events={events} />

      <EdgeEventInspector
        event={selectedEvent}
        open={selectedEvent !== null}
        onClose={() => setSelectedEventId(null)}
      />
    </div>
  );
}

function SessionFacts({ session }: { session: ReturnType<typeof useEdgeSession>["data"] }) {
  if (!session) return null;
  return (
    <section
      className="grid gap-3 rounded-3xl border border-border bg-surface-1/70 p-4 sm:grid-cols-2 lg:grid-cols-4"
      data-testid="edge-session-facts"
    >
      <Fact icon={Shield} label="Principal" value={session.principalId ?? "—"} mono />
      <Fact icon={ShieldCheck} label="Agent" value={session.agentProduct ?? "—"} sub={session.agentVersion} />
      <Fact icon={Layers} label="Mode" value={session.mode} />
      <Fact icon={ShieldAlert} label="Policy snapshot" value={session.policySnapshot ?? "—"} mono />
      {session.repo ? <Fact icon={GitBranch} label="Repo" value={session.repo} sub={session.gitBranch} /> : null}
      {session.cwd ? <Fact icon={Workflow} label="Cwd" value={session.cwd} mono /> : null}
      {session.jobId ? <Fact icon={Workflow} label="Job" value={session.jobId} mono /> : null}
      {session.workflowRunId ? (
        <Fact icon={Workflow} label="Workflow run" value={session.workflowRunId} mono />
      ) : null}
      {session.traceId ? <Fact icon={Workflow} label="Trace" value={session.traceId} mono /> : null}
    </section>
  );
}

function Fact({
  icon: Icon,
  label,
  value,
  sub,
  mono,
}: {
  icon: typeof Shield;
  label: string;
  value: string;
  sub?: string;
  mono?: boolean;
}) {
  return (
    <div className="min-w-0">
      <div className="flex items-center gap-1 text-[10px] uppercase tracking-[0.18em] text-muted-foreground">
        <Icon className="h-3 w-3" /> {label}
      </div>
      <div className={cn("mt-1 break-all text-sm text-foreground", mono && "font-mono text-xs")}>{value}</div>
      {sub ? <div className="text-[10px] text-muted-foreground">{sub}</div> : null}
    </div>
  );
}

function TimelineFilters({
  filter,
  setFilter,
  executions,
  decisions,
  kinds,
}: {
  filter: Filter;
  setFilter: (next: Filter) => void;
  executions: string[];
  decisions: string[];
  kinds: string[];
}) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      <FilterSelect
        label="Execution"
        value={filter.executionId}
        onChange={(value) => setFilter({ ...filter, executionId: value })}
        options={executions}
        testid="edge-filter-execution"
      />
      <FilterSelect
        label="Decision"
        value={filter.decision}
        onChange={(value) => setFilter({ ...filter, decision: value })}
        options={decisions}
        testid="edge-filter-decision"
      />
      <FilterSelect
        label="Kind"
        value={filter.kind}
        onChange={(value) => setFilter({ ...filter, kind: value })}
        options={kinds}
        testid="edge-filter-kind"
      />
    </div>
  );
}

function FilterSelect({
  label,
  value,
  onChange,
  options,
  testid,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  options: string[];
  testid: string;
}) {
  return (
    <label className="flex items-center gap-2 text-xs text-muted-foreground">
      {label}
      <select
        data-testid={testid}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        className="rounded-xl border border-border bg-background px-2 py-1 text-xs text-foreground shadow-soft"
      >
        <option value="">All</option>
        {options.map((option) => (
          <option key={option} value={option}>
            {option}
          </option>
        ))}
      </select>
    </label>
  );
}

// EDGE-050 — TimelineRow superseded by TimelineGroupRow which renders
// per-hook-fire collapsed groups. The single-event row pattern is now
// the leaf inside an expanded group's witness list.

// EDGE-050 — collapsed timeline row that wraps an EdgeEventGroup.
// Headline binds to the gateway authoritative decision when available;
// caret expands to reveal the underlying 1–3 witnesses (receipt /
// gatewayDecision / agentdEvidence). When showReceipts is on AND the
// group has a receipt, the receipt also surfaces as a standalone
// nested row so audit operators can scan pre-evaluation timestamps
// without expanding every row.
function TimelineGroupRow({
  group,
  expanded,
  showReceipts,
  selectedEventId,
  onToggleExpanded,
  onSelectEvent,
}: {
  group: EdgeEventGroup;
  expanded: boolean;
  showReceipts: boolean;
  selectedEventId: string | null;
  onToggleExpanded: () => void;
  onSelectEvent: (eventId: string) => void;
}) {
  const headline = group.headline;
  const hasMultiple = group.events.length > 1;
  const showDivergence = Boolean(group.divergence);
  return (
    <motion.li
      layout
      initial={{ opacity: 0, y: 4 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.18 }}
      className={cn(
        "rounded-2xl border bg-surface-1/80 transition-shadow",
        showDivergence
          ? "border-amber-300 shadow-soft-hover ring-1 ring-amber-300/50"
          : "border-border shadow-soft hover:shadow-soft-hover",
      )}
      data-testid="edge-event-group"
      data-group-id={group.id}
      data-divergent={showDivergence ? "true" : "false"}
    >
      <button
        type="button"
        onClick={hasMultiple ? onToggleExpanded : () => onSelectEvent(headline.eventId)}
        aria-expanded={hasMultiple ? expanded : undefined}
        data-testid="edge-event-group-headline"
        className="flex w-full flex-wrap items-center justify-between gap-3 rounded-2xl px-3 py-2 text-left"
      >
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          {hasMultiple ? (
            expanded ? (
              <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" aria-hidden />
            ) : (
              <ChevronRight className="h-3.5 w-3.5 text-muted-foreground" aria-hidden />
            )
          ) : (
            <span className="inline-block h-3.5 w-3.5" aria-hidden />
          )}
          <StatusBadge variant={decisionVariant(headline.decision)}>{String(headline.decision)}</StatusBadge>
          <span className="font-mono text-xs text-foreground">{headline.kind}</span>
          {headline.toolName ? (
            <span className="text-xs text-muted-foreground">· {headline.toolName}</span>
          ) : null}
          {headline.approvalRef ? (
            <span className="font-mono text-[10px] text-cordum">{headline.approvalRef}</span>
          ) : null}
          {showDivergence ? (
            <span
              className="inline-flex items-center gap-1 rounded-full border border-amber-300 bg-amber-50 px-2 py-0.5 text-[10px] font-medium text-amber-800"
              data-testid="edge-event-divergence-chip"
              title="Gateway decision and agentd evidence disagree on a watched field"
            >
              <AlertTriangle className="h-3 w-3" aria-hidden />
              audit divergence
            </span>
          ) : null}
        </div>
        <div className="flex items-center gap-2 text-[10px] text-muted-foreground">
          <Clock className="h-3 w-3" />
          <span>{formatRelativeTime(headline.ts)}</span>
          {hasMultiple ? (
            <span className="font-mono" data-testid="edge-event-group-count">
              {group.events.length} events
            </span>
          ) : (
            <span className="font-mono">#{headline.seq}</span>
          )}
        </div>
      </button>
      {expanded && hasMultiple ? (
        <ol
          className="space-y-1 border-t border-border/60 px-3 py-2"
          data-testid="edge-event-group-expanded"
        >
          {group.events.map((event) => (
            <li key={event.eventId}>
              <button
                type="button"
                onClick={() => onSelectEvent(event.eventId)}
                aria-pressed={event.eventId === selectedEventId}
                data-testid="edge-event-group-witness"
                data-event-id={event.eventId}
                className={cn(
                  "flex w-full flex-wrap items-center justify-between gap-2 rounded-xl px-2 py-1 text-left text-[11px]",
                  event.eventId === selectedEventId
                    ? "bg-surface-2 ring-1 ring-cordum/40"
                    : "hover:bg-surface-2",
                )}
              >
                <div className="flex min-w-0 flex-wrap items-center gap-2">
                  <span className="rounded-full border border-border px-1.5 py-0.5 font-mono text-[9px] uppercase tracking-[0.12em] text-muted-foreground">
                    {witnessRoleLabel(event, group)}
                  </span>
                  <span className="font-mono text-[10px] text-foreground">{event.eventId}</span>
                  <span className="text-muted-foreground">{event.kind}</span>
                </div>
                <span className="font-mono text-[10px] text-muted-foreground">
                  {formatRelativeTime(event.ts)} · #{event.seq}
                </span>
              </button>
            </li>
          ))}
        </ol>
      ) : null}
      {showReceipts && group.receipt && !expanded ? (
        <div
          className="border-t border-border/60 px-3 py-2 text-[10px] text-muted-foreground"
          data-testid="edge-event-group-receipt-summary"
        >
          <span className="font-mono text-[9px] uppercase tracking-[0.12em] text-muted-foreground">
            Receipt
          </span>{" "}
          <span className="font-mono text-[10px] text-foreground">{group.receipt.eventId}</span>{" "}
          · {formatRelativeTime(group.receipt.ts)}
        </div>
      ) : null}
    </motion.li>
  );
}

function witnessRoleLabel(event: AgentActionEvent, group: EdgeEventGroup): string {
  if (group.receipt && event.eventId === group.receipt.eventId) return "receipt";
  if (group.gatewayDecision && event.eventId === group.gatewayDecision.eventId) return "gateway";
  if (group.agentdEvidence && event.eventId === group.agentdEvidence.eventId) return "agentd";
  return "event";
}
