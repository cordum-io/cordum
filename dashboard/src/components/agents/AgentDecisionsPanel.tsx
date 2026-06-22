import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { ScrollText } from "lucide-react";
import { useInfiniteAuditEvents } from "@/hooks/useAuditEvents";
import type { AuditEntry } from "@/api/types";
import {
  buildAgentDecisionDeepLink,
  decisionEvidence,
} from "@/lib/agent-decisions";
import { SafetyBadge } from "./SafetyBadge";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { SkeletonTable } from "@/components/ui/Skeleton";
import { formatRelativeTime } from "@/lib/utils";

interface AgentDecisionsPanelProps {
  agentId: string;
  /** Opt into ~2s live polling. Default OFF to avoid extra prod request load. */
  live?: boolean;
}

/**
 * AgentDecisionsPanel surfaces one agent's GOVERNED-ACTION history — MCP tool
 * calls (mcp.tool_invocation), Edge actions and job decisions — by REUSING the
 * audit feed (GET /api/v1/audit/events?agent_id=<id>) via useInfiniteAuditEvents.
 * It does not introduce a parallel decision store. Each row shows the decision
 * badge, the governed tool/action, the human reason, the cited evidence
 * (prompt-injection taint snippet + source tool, firing rule, Edge approver),
 * the timestamp, and a deep-link into the global Audit Log filtered to exactly
 * that event.
 *
 * Attacker-controlled taint snippets are rendered as plain escaped React text
 * (never dangerouslySetInnerHTML); they are already bounded/redacted server-side.
 */
export function AgentDecisionsPanel({
  agentId,
  live = false,
}: AgentDecisionsPanelProps) {
  const {
    items,
    isLoading,
    isError,
    error,
    userMessage,
    hasNextPage,
    isFetchingNextPage,
    fetchNextPage,
    refetch,
  } = useInfiniteAuditEvents({ agentId, limit: 100 }, { live });

  const [decisionsOnly, setDecisionsOnly] = useState(false);

  const visible = useMemo(
    () =>
      decisionsOnly
        ? items.filter((e) => (e.decision ?? "").trim() !== "")
        : items,
    [decisionsOnly, items],
  );

  if (isLoading) {
    return (
      <section className="instrument-card" aria-busy="true">
        <PanelHeader />
        <div className="mt-4">
          <SkeletonTable rows={5} />
        </div>
      </section>
    );
  }

  if (isError) {
    return (
      <section className="instrument-card">
        <PanelHeader />
        <ErrorBanner
          title="Failed to load governance decisions"
          message={
            userMessage ??
            (error instanceof Error ? error.message : "An unexpected error occurred")
          }
          onRetry={() => void refetch()}
        />
      </section>
    );
  }

  return (
    <section className="instrument-card">
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <PanelHeader />
        <label className="flex items-center gap-2 text-xs text-muted-foreground">
          <input
            type="checkbox"
            checked={decisionsOnly}
            onChange={(e) => setDecisionsOnly(e.target.checked)}
            className="h-3.5 w-3.5 rounded border-border"
          />
          Decisions only
        </label>
      </div>

      {visible.length === 0 ? (
        decisionsOnly ? (
          <EmptyState
            title="No decisions on the loaded pages"
            description="Toggle off ‘Decisions only’ to see all governed activity recorded for this agent."
          />
        ) : (
          <EmptyState
            icon={<ScrollText className="h-5 w-5" />}
            title="No governance decisions recorded for this agent yet"
            description="MCP tool calls, Edge actions, and job decisions attributed to this agent will appear here."
          />
        )
      ) : (
        <ul role="list" className="space-y-3">
          {visible.map((entry) => (
            <AgentDecisionRow key={entry.id} entry={entry} />
          ))}
        </ul>
      )}

      {hasNextPage && (
        <div className="mt-4 flex justify-center">
          <Button
            variant="outline"
            size="sm"
            disabled={isFetchingNextPage}
            onClick={() => void fetchNextPage()}
          >
            {isFetchingNextPage ? "Loading…" : "Load more"}
          </Button>
        </div>
      )}
    </section>
  );
}

function PanelHeader() {
  return (
    <div>
      <h2 className="font-display text-sm font-semibold text-foreground">
        Governance Decisions
      </h2>
      <p className="mt-0.5 text-xs text-muted-foreground">
        MCP tool calls, Edge actions, and job decisions attributed to this agent.
      </p>
    </div>
  );
}

function AgentDecisionRow({ entry }: { entry: AuditEntry }) {
  const ev = decisionEvidence(entry);
  const decision = (entry.decision ?? "").trim();
  const actionLabel = entry.action || entry.eventType;
  const href = buildAgentDecisionDeepLink(entry);
  const hasEvidence =
    ev.taintSnippet || ev.taintSourceTool || ev.matchedRule || ev.approver || ev.subReason;

  return (
    <li className="rounded-2xl border border-border bg-surface-0/50 p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <SafetyBadge decision={decision} />
          <span className="font-mono text-xs text-foreground">{actionLabel}</span>
        </div>
        <div className="flex items-center gap-3">
          <time className="text-xs text-muted-foreground" dateTime={entry.timestamp}>
            {formatRelativeTime(entry.timestamp)}
          </time>
          <Link
            to={href}
            aria-label={`Open the ${actionLabel} decision in the Audit Log`}
            className="text-xs font-medium text-cordum hover:underline"
          >
            Audit log →
          </Link>
        </div>
      </div>

      {ev.reason && <p className="mt-2 text-xs text-muted-foreground">{ev.reason}</p>}

      {hasEvidence && (
        <div className="mt-2 space-y-1.5 border-l-2 border-border pl-3">
          {ev.taintSnippet && (
            <div className="text-[11px] text-muted-foreground">
              <span className="uppercase tracking-wide">Cited injected content</span>
              {ev.taintSourceTool && (
                <>
                  {" · source tool "}
                  <code className="font-mono text-foreground">
                    {ev.taintSourceTool}
                  </code>
                </>
              )}
              {ev.taintPattern && (
                <>
                  {" · pattern "}
                  <code className="font-mono text-foreground">
                    {ev.taintPattern}
                  </code>
                </>
              )}
              <code className="mt-1 block whitespace-pre-wrap break-words rounded bg-surface-2 px-2 py-1 font-mono text-foreground">
                {ev.taintSnippet}
              </code>
            </div>
          )}
          {!ev.taintSnippet && ev.taintSourceTool && (
            <p className="text-[11px] text-muted-foreground">
              Source tool:{" "}
              <code className="font-mono text-foreground">{ev.taintSourceTool}</code>
            </p>
          )}
          {ev.matchedRule && (
            <p className="text-[11px] text-muted-foreground">
              Firing rule:{" "}
              <code className="font-mono text-foreground">{ev.matchedRule}</code>
            </p>
          )}
          {ev.approver && (
            <p className="text-[11px] text-muted-foreground">
              Approved by <span className="text-foreground">{ev.approver}</span>
            </p>
          )}
          {!ev.taintSnippet && ev.subReason && (
            <p className="text-[11px] text-muted-foreground">
              Sub-reason:{" "}
              <code className="font-mono text-foreground">{ev.subReason}</code>
            </p>
          )}
        </div>
      )}
    </li>
  );
}
