/*
 * EDGE-023: Edge Sessions list page
 * Live table of governed Edge sessions with summary cards and filters.
 * Row click navigates to EDGE-024 detail page.
 */
import { useMemo, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { motion } from "framer-motion";
import {
  CheckCircle2,
  Activity,
  ShieldAlert,
  Inbox,
  FileText,
  Clock,
  Search,
  Terminal,
  type LucideIcon,
} from "lucide-react";
import type { EdgeSession, EdgeSessionListParams } from "@/api/types";
import { EmptyState } from "@/components/ui/EmptyState";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { Skeleton } from "@/components/ui/Skeleton";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import { useEdgeSessions } from "@/hooks/useEdgeSessions";
import { cn, formatRelativeTime } from "@/lib/utils";

const STATUS_OPTIONS = [
  "starting",
  "running",
  "waiting_for_approval",
  "degraded",
  "ended",
  "failed",
];

const POLICY_MODE_OPTIONS = ["observe", "enforce", "enterprise-strict", "local-dev-enforce"];

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

interface PageFilter {
  status: string;
  policyMode: string;
  agentProduct: string;
  search: string;
}

const EMPTY_FILTER: PageFilter = { status: "", policyMode: "", agentProduct: "", search: "" };

function applyClientFilter(items: EdgeSession[], filter: PageFilter): EdgeSession[] {
  const search = filter.search.trim().toLowerCase();
  return items.filter((session) => {
    if (filter.policyMode && session.policyMode !== filter.policyMode) return false;
    if (
      search &&
      !session.sessionId.toLowerCase().includes(search) &&
      !(session.principalId ?? "").toLowerCase().includes(search)
    ) {
      return false;
    }
    return true;
  });
}

function buildHookParams(filter: PageFilter): EdgeSessionListParams {
  const params: EdgeSessionListParams = { limit: 50 };
  if (filter.status) params.status = filter.status;
  if (filter.agentProduct) params.agentProduct = filter.agentProduct;
  return params;
}

function isActiveSession(status: string): boolean {
  return ["starting", "running", "waiting_for_approval", "degraded"].includes(status);
}

export default function EdgeSessionsPage() {
  const [filter, setFilter] = useState<PageFilter>(EMPTY_FILTER);
  const navigate = useNavigate();
  const hookParams = useMemo(() => buildHookParams(filter), [filter]);
  const sessionsQuery = useEdgeSessions(hookParams);
  const sessions = sessionsQuery.data?.items ?? [];
  const visibleSessions = useMemo(() => applyClientFilter(sessions, filter), [sessions, filter]);

  const summary = useMemo(() => {
    const active = sessions.filter((session) => isActiveSession(session.status)).length;
    const closed = sessions.length - active;
    const waiting = sessions.filter((session) => session.status === "waiting_for_approval").length;
    const denied = sessions.reduce((acc, session) => acc + (session.riskSummary?.deniedCount ?? 0), 0);
    const evidenceFiles = sessions.reduce(
      (acc, session) => acc + (session.riskSummary?.artifactCount ?? 0),
      0,
    );
    return { active, closed, waiting, denied, evidenceFiles };
  }, [sessions]);

  return (
    <div className="space-y-6 p-6">
      <header className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <p className="text-xs font-medium uppercase tracking-[0.2em] text-cordum">Cordum Edge</p>
          <h1 className="mt-1 text-2xl font-semibold font-display text-foreground">Edge sessions</h1>
          <p className="mt-1 max-w-2xl text-sm text-muted-foreground">
            Governed Claude Code hook sessions across this tenant. Review open and closed
            sessions, pending approvals, denied actions, and attached evidence files.
          </p>
        </div>
      </header>

      <section
        className="grid gap-3 sm:grid-cols-2 lg:grid-cols-5"
        data-testid="edge-sessions-summary"
      >
        <SummaryCard
          label="Active sessions"
          value={summary.active}
          description="Running or approval-paused"
          icon={Activity}
          variant="info"
        />
        <SummaryCard
          label="Closed sessions"
          value={summary.closed}
          description="Ended or failed sessions"
          icon={CheckCircle2}
          variant="healthy"
        />
        <SummaryCard
          label="Pending approvals"
          value={summary.waiting}
          description="Sessions waiting for review"
          icon={Inbox}
          variant="governance"
        />
        <SummaryCard
          label="Denied actions"
          value={summary.denied}
          description="Policy-blocked tool actions"
          icon={ShieldAlert}
          variant="danger"
        />
        <SummaryCard
          label="Evidence files"
          value={summary.evidenceFiles}
          description="Audit artifacts attached"
          icon={FileText}
          variant="healthy"
        />
      </section>

      <SessionsFilters filter={filter} setFilter={setFilter} />

      <section
        className="rounded-3xl border border-border bg-surface-1/70 p-4"
        data-testid="edge-sessions-table"
      >
        <header className="flex flex-wrap items-center justify-between gap-3">
          <p className="text-sm text-muted-foreground">
            {sessionsQuery.isPending
              ? "Loading…"
              : `${visibleSessions.length} session${visibleSessions.length === 1 ? "" : "s"}`}
            {visibleSessions.length !== sessions.length ? ` of ${sessions.length}` : ""}
          </p>
        </header>

        {sessionsQuery.error ? (
          <div className="mt-4">
            <ErrorBanner
              title="Edge sessions unavailable"
              message={sessionsQuery.error.message}
              onRetry={() => {
                void sessionsQuery.refetch();
              }}
            />
          </div>
        ) : sessionsQuery.isPending ? (
          <Skeleton className="mt-4 h-48 w-full" />
        ) : visibleSessions.length === 0 ? (
          <div className="mt-4">
            <EmptyState
              icon={<Terminal className="w-5 h-5" aria-hidden="true" />}
              title="No Edge sessions"
              description={
                sessions.length === 0
                  ? "No governed Claude Code sessions have started for this tenant yet."
                  : "Adjust the filters above to see sessions."
              }
            />
          </div>
        ) : (
          <ul className="mt-4 space-y-2" data-testid="edge-sessions-list">
            {visibleSessions.map((session) => (
              <SessionRow
                key={session.sessionId}
                session={session}
                onSelect={() => navigate(`/edge/sessions/${session.sessionId}`)}
              />
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}

function SummaryCard({
  label,
  value,
  description,
  icon: Icon,
  variant,
}: {
  label: string;
  value: number;
  description: string;
  icon: LucideIcon;
  variant: BadgeVariant;
}) {
  return (
    <div className="rounded-3xl border border-border bg-surface-1/80 p-4 shadow-soft">
      <div className="flex items-center justify-between">
        <span className="text-[10px] uppercase tracking-[0.2em] text-muted-foreground">{label}</span>
        <Icon className={cn("h-4 w-4", summaryIconClass(variant))} />
      </div>
      <div className="mt-2">
        <span className="font-display text-2xl text-foreground">{value}</span>
        <p className="mt-1 text-xs leading-snug text-muted-foreground">{description}</p>
      </div>
    </div>
  );
}

function summaryIconClass(variant: BadgeVariant): string {
  switch (variant) {
    case "danger":
      return "text-danger";
    case "governance":
      return "text-cordum";
    case "healthy":
      return "text-success";
    case "warning":
      return "text-warning";
    default:
      return "text-muted-foreground";
  }
}

function SessionsFilters({
  filter,
  setFilter,
}: {
  filter: PageFilter;
  setFilter: (next: PageFilter) => void;
}) {
  return (
    <section className="flex flex-wrap items-center gap-3" data-testid="edge-sessions-filters">
      <FilterSelect
        label="Status"
        value={filter.status}
        options={STATUS_OPTIONS}
        onChange={(value) => setFilter({ ...filter, status: value })}
        testid="edge-sessions-filter-status"
      />
      <FilterSelect
        label="Policy mode"
        value={filter.policyMode}
        options={POLICY_MODE_OPTIONS}
        onChange={(value) => setFilter({ ...filter, policyMode: value })}
        testid="edge-sessions-filter-policy"
      />
      <label className="flex items-center gap-2 text-xs text-muted-foreground">
        Agent
        <input
          data-testid="edge-sessions-filter-agent"
          type="text"
          value={filter.agentProduct}
          onChange={(event) => setFilter({ ...filter, agentProduct: event.target.value })}
          placeholder="claude-code"
          className="rounded-xl border border-border bg-background px-2 py-1 text-xs text-foreground shadow-soft"
        />
      </label>
      <label className="flex items-center gap-2 text-xs text-muted-foreground">
        <Search className="h-3 w-3" />
        <input
          data-testid="edge-sessions-filter-search"
          type="search"
          value={filter.search}
          onChange={(event) => setFilter({ ...filter, search: event.target.value })}
          placeholder="Search session id or principal"
          className="rounded-xl border border-border bg-background px-2 py-1 text-xs text-foreground shadow-soft"
        />
      </label>
    </section>
  );
}

function FilterSelect({
  label,
  value,
  options,
  onChange,
  testid,
}: {
  label: string;
  value: string;
  options: string[];
  onChange: (value: string) => void;
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

function SessionRow({
  session,
  onSelect,
}: {
  session: EdgeSession;
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
        "shadow-soft hover:shadow-soft-hover",
      )}
    >
      <button
        type="button"
        onClick={onSelect}
        data-testid="edge-sessions-row"
        data-session-id={session.sessionId}
        className="flex w-full flex-wrap items-center justify-between gap-3 rounded-2xl px-3 py-2 text-left"
      >
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <Link
            to={`/edge/sessions/${session.sessionId}`}
            onClick={(event) => event.stopPropagation()}
            className="break-all font-mono text-xs text-foreground hover:underline"
          >
            {session.sessionId}
          </Link>
          <StatusBadge variant={statusVariant(session.status)}>{session.status}</StatusBadge>
          <StatusBadge variant="info">{session.policyMode}</StatusBadge>
          {session.agentProduct ? (
            <span className="text-xs text-muted-foreground">{session.agentProduct}</span>
          ) : null}
          {session.principalId ? (
            <span className="font-mono text-[10px] text-muted-foreground">
              {session.principalId}
            </span>
          ) : null}
        </div>
        <div className="flex items-center gap-3 text-[10px] text-muted-foreground">
          {session.riskSummary?.deniedCount ? (
            <span data-testid="edge-sessions-row-denied">
              {session.riskSummary.deniedCount} denied
            </span>
          ) : null}
          {session.riskSummary?.approvalCount ? (
            <span data-testid="edge-sessions-row-approvals">
              {session.riskSummary.approvalCount} approvals
            </span>
          ) : null}
          <Clock className="h-3 w-3" />
          <span>{formatRelativeTime(session.startedAt)}</span>
        </div>
      </button>
    </motion.li>
  );
}
