import { Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { AlertTriangle, ArrowUpRight, Bot, Clock, ShieldAlert } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { InfoBanner } from "@/components/ui/InfoBanner";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import type {
  AgentExecution,
  EdgeExecutionListParams,
  EdgeExecutionMetrics,
  EdgeSession,
} from "@/api/types";
import { ApiError } from "@/api/client";
import { cn, formatRelativeTime } from "@/lib/utils";
import { queryKeys } from "@/lib/queryKeys";
import { fetchEdgeExecutions, fetchEdgeSession, edgeErrorFromApiError } from "@/hooks/useEdgeSessions";

interface AgentExecutionEvidenceRow {
  execution: AgentExecution;
  maxRisk?: string;
}

interface AgentExecutionEvidencePage {
  items: AgentExecutionEvidenceRow[];
  nextCursor: string | null;
}

interface AgentExecutionsPanelProps {
  jobId?: string | null;
  workflowRunId?: string | null;
  limit?: number;
  className?: string;
}

const DEFAULT_LIMIT = 8;

function clean(value?: string | null): string | undefined {
  const trimmed = value?.trim();
  return trimmed ? trimmed : undefined;
}

export function edgeSessionDetailPath(sessionId: string): string {
  return `/edge/sessions/${encodeURIComponent(sessionId)}`;
}

export function executionMatchesScope(
  execution: AgentExecution,
  params: Pick<EdgeExecutionListParams, "jobId" | "workflowRunId">,
): boolean {
  if (params.jobId && execution.jobId !== params.jobId) return false;
  if (params.workflowRunId && execution.workflowRunId !== params.workflowRunId) return false;
  return true;
}

function riskFromExecutionLabels(execution: AgentExecution): string | undefined {
  return clean(
    execution.labels?.max_risk ??
      execution.labels?.risk ??
      execution.labels?.risk_level,
  );
}

function riskFromSession(session: EdgeSession | undefined): string | undefined {
  return clean(session?.riskSummary?.maxRisk);
}

async function riskBySessionID(executions: AgentExecution[]): Promise<Map<string, string>> {
  const ids = Array.from(new Set(executions.map((item) => item.sessionId).filter(Boolean)));
  const settled = await Promise.allSettled(ids.map((id) => fetchEdgeSession(id)));
  const risks = new Map<string, string>();
  settled.forEach((result, index) => {
    if (result.status !== "fulfilled") return;
    const id = ids[index];
    const risk = riskFromSession(result.value);
    if (id && risk) risks.set(id, risk);
  });
  return risks;
}

async function fetchExecutionEvidence(params: EdgeExecutionListParams): Promise<AgentExecutionEvidencePage> {
  const page = await fetchEdgeExecutions(params);
  const scoped = page.items.filter((execution) => executionMatchesScope(execution, params));
  const risks = await riskBySessionID(scoped);
  return {
    nextCursor: page.nextCursor,
    items: scoped.map((execution) => ({
      execution,
      maxRisk: risks.get(execution.sessionId) ?? riskFromExecutionLabels(execution),
    })),
  };
}

function statusVariant(status: string): BadgeVariant {
  switch (status.toLowerCase()) {
    case "succeeded":
      return "healthy";
    case "failed":
    case "timeout":
    case "timed_out":
      return "danger";
    case "cancelled":
      return "muted";
    case "running":
      return "info";
    default:
      return "warning";
  }
}

function riskVariant(risk?: string): BadgeVariant {
  switch ((risk ?? "").toLowerCase()) {
    case "critical":
      return "danger";
    case "high":
      return "warning";
    case "medium":
      return "info";
    case "low":
      return "healthy";
    default:
      return "muted";
  }
}

function DecisionCounts({ metrics }: { metrics?: EdgeExecutionMetrics }) {
  const allow = metrics?.allow ?? 0;
  const deny = metrics?.deny ?? 0;
  const requireApproval = metrics?.requireApproval ?? 0;
  return (
    <div className="flex flex-wrap items-center gap-1.5 text-[11px] font-mono text-muted-foreground">
      <span className="rounded-full bg-[var(--color-success)]/10 px-2 py-0.5 text-[var(--color-success)]">
        allow {allow}
      </span>
      <span className="rounded-full bg-destructive/10 px-2 py-0.5 text-destructive">deny {deny}</span>
      <span className="rounded-full bg-[var(--color-warning)]/10 px-2 py-0.5 text-[var(--color-warning)]">
        approval {requireApproval}
      </span>
    </div>
  );
}

function errorMessage(error: unknown): string {
  if (error instanceof ApiError) {
    return edgeErrorFromApiError(error)?.message ?? error.message;
  }
  return error instanceof Error ? error.message : "Unable to load linked Edge executions.";
}

function AgentExecutionRow({ row }: { row: AgentExecutionEvidenceRow }) {
  const { execution, maxRisk } = row;
  const riskLabel = maxRisk ? maxRisk.toLowerCase() : "not recorded";
  return (
    <div className="grid gap-3 border-t border-border px-4 py-3 md:grid-cols-[minmax(0,1fr)_auto]">
      <div className="min-w-0 space-y-2">
        <div className="flex flex-wrap items-center gap-2">
          <StatusBadge variant={statusVariant(execution.status)} dot>
            {execution.status}
          </StatusBadge>
          <span className="truncate text-sm font-medium text-foreground">{execution.adapter}</span>
          <span className="text-xs font-mono text-muted-foreground">{formatRelativeTime(execution.startedAt)}</span>
        </div>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
          {execution.stepId && <span className="font-mono">step {execution.stepId}</span>}
          {execution.workerId && <span className="font-mono">worker {execution.workerId}</span>}
          <span className="font-mono">exec {execution.executionId.slice(0, 12)}</span>
        </div>
        <DecisionCounts metrics={execution.metrics} />
      </div>
      <div className="flex items-center gap-3 md:justify-end">
        <div className="space-y-1 text-right">
          <p className="text-[10px] font-mono uppercase tracking-wider text-muted-foreground">Max risk</p>
          <StatusBadge variant={riskVariant(maxRisk)}>{riskLabel}</StatusBadge>
        </div>
        <Link
          to={edgeSessionDetailPath(execution.sessionId)}
          className={cn(
            "inline-flex h-8 shrink-0 items-center justify-center gap-1.5 rounded-xl border border-border px-3 text-xs font-medium",
            "text-foreground transition-all duration-[var(--duration-soft)] ease-out hover:-translate-y-[1px] hover:bg-secondary hover:shadow-soft-hover",
            "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-cordum/40 focus-visible:ring-offset-2 focus-visible:ring-offset-background",
          )}
        >
          Session <ArrowUpRight className="h-3 w-3" />
        </Link>
      </div>
    </div>
  );
}

export function AgentExecutionsPanel({
  jobId,
  workflowRunId,
  limit = DEFAULT_LIMIT,
  className,
}: AgentExecutionsPanelProps) {
  const params: EdgeExecutionListParams = {
    jobId: clean(jobId),
    workflowRunId: clean(workflowRunId),
    limit,
  };
  const enabled = Boolean(params.jobId || params.workflowRunId);
  const query = useQuery<AgentExecutionEvidencePage, Error>({
    queryKey: queryKeys.edge.executions.list(params),
    queryFn: () => fetchExecutionEvidence(params),
    enabled,
    retry: false,
    staleTime: 5_000,
  });

  if (!enabled || (query.isLoading && !query.data)) return null;
  if (query.isError) {
    return (
      <InfoBanner variant="warning" title="Edge evidence unavailable" icon={<AlertTriangle className="h-3.5 w-3.5" />} className={className}>
        <div className="flex flex-wrap items-center gap-3">
          <span>{errorMessage(query.error)}</span>
          <Button type="button" variant="outline" size="sm" onClick={() => void query.refetch()}>
            Retry
          </Button>
        </div>
      </InfoBanner>
    );
  }

  const rows = query.data?.items ?? [];
  if (rows.length === 0) return null;

  return (
    <section className={cn("instrument-card overflow-hidden !p-0", className)} aria-label="Linked Agent Executions">
      <div className="flex flex-col gap-3 px-4 py-4 md:flex-row md:items-start md:justify-between">
        <div className="flex items-start gap-3">
          <div className="rounded-xl border border-cordum/20 bg-cordum/10 p-2 text-cordum">
            <Bot className="h-4 w-4" />
          </div>
          <div>
            <h2 className="font-display text-sm font-semibold text-foreground">Agent Executions</h2>
            <p className="mt-1 max-w-2xl text-xs text-muted-foreground">
              Edge evidence linked to this production work unit. Full event details stay in the Edge session view.
            </p>
          </div>
        </div>
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <ShieldAlert className="h-3.5 w-3.5" />
          <span>{rows.length} linked</span>
          <Clock className="h-3.5 w-3.5" />
          <span>latest first</span>
        </div>
      </div>
      <div role="list">
        {rows.map((row) => (
          <div key={row.execution.executionId} role="listitem">
            <AgentExecutionRow row={row} />
          </div>
        ))}
      </div>
    </section>
  );
}
