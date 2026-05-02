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
} from "lucide-react";
import type { AgentActionEvent, EdgeDecision } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { Skeleton } from "@/components/ui/Skeleton";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import {
  useEdgeSession,
  useEdgeSessionEvents,
  useEdgeExecutions,
} from "@/hooks/useEdgeSessions";
import { EdgeApprovalsDrawer } from "@/components/edge/EdgeApprovalsDrawer";
import { EdgeArtifactsPanel } from "@/components/edge/EdgeArtifactsPanel";
import { EdgeEventInspector } from "@/components/edge/EdgeEventInspector";
import { cn, formatRelativeTime } from "@/lib/utils";

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
  const [approvalsOpen, setApprovalsOpen] = useState(false);
  const [filter, setFilter] = useState<Filter>({ executionId: "", decision: "", kind: "" });

  const session = sessionQuery.data;
  const events = useMemo(() => {
    const items = eventsQuery.data?.items ?? [];
    return [...items].sort(sortByOrder);
  }, [eventsQuery.data]);
  const visibleEvents = useMemo(() => applyFilter(events, filter), [events, filter]);
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
          <Button variant="outline" size="sm" onClick={() => setApprovalsOpen(true)}>
            <Inbox className="h-3.5 w-3.5" /> Approvals
          </Button>
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

        {eventsQuery.isPending ? (
          <Skeleton className="mt-4 h-48 w-full" />
        ) : visibleEvents.length === 0 ? (
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
            {visibleEvents.map((event) => (
              <TimelineRow
                key={event.eventId}
                event={event}
                selected={event.eventId === selectedEventId}
                onSelect={() => setSelectedEventId(event.eventId)}
              />
            ))}
          </ol>
        )}
      </section>

      <EdgeArtifactsPanel sessionId={session.sessionId} events={events} />

      <EdgeEventInspector
        event={selectedEvent}
        open={selectedEvent !== null}
        onClose={() => setSelectedEventId(null)}
      />
      <EdgeApprovalsDrawer
        open={approvalsOpen}
        onClose={() => setApprovalsOpen(false)}
        sessionId={session.sessionId}
        events={events}
        currentPrincipalId={session.principalId}
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

function TimelineRow({
  event,
  selected,
  onSelect,
}: {
  event: AgentActionEvent;
  selected: boolean;
  onSelect: () => void;
}) {
  return (
    <motion.li
      layout
      initial={{ opacity: 0, y: 4 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.18 }}
      className={cn(
        "rounded-2xl border border-border bg-surface-1/80 transition-shadow",
        selected ? "shadow-soft-hover ring-1 ring-cordum/40" : "shadow-soft hover:shadow-soft-hover",
      )}
    >
      <button
        type="button"
        onClick={onSelect}
        data-testid="edge-event-row"
        data-event-id={event.eventId}
        aria-pressed={selected}
        className="flex w-full flex-wrap items-center justify-between gap-3 rounded-2xl px-3 py-2 text-left"
      >
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <StatusBadge variant={decisionVariant(event.decision)}>{String(event.decision)}</StatusBadge>
          <span className="font-mono text-xs text-foreground">{event.kind}</span>
          {event.toolName ? (
            <span className="text-xs text-muted-foreground">· {event.toolName}</span>
          ) : null}
          {event.approvalRef ? (
            <span className="font-mono text-[10px] text-cordum">{event.approvalRef}</span>
          ) : null}
        </div>
        <div className="flex items-center gap-2 text-[10px] text-muted-foreground">
          <Clock className="h-3 w-3" />
          <span>{formatRelativeTime(event.ts)}</span>
          <span className="font-mono">#{event.seq}</span>
        </div>
      </button>
    </motion.li>
  );
}
