import { Link, useNavigate, useParams } from "react-router-dom";
import { motion } from "framer-motion";
import { ArrowLeft } from "lucide-react";
import { ApiError } from "@/api/client";
import type {
  CopilotSessionDecision,
  CopilotSessionDetailResponse,
  CopilotSessionJob,
} from "@/api/types";
import { Button } from "@/components/ui/Button";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import { EmptyState } from "@/components/ui/EmptyState";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { SkeletonTable } from "@/components/ui/Skeleton";
import { PageHeader } from "@/components/layout/PageHeader";
import { useCopilotSession } from "@/hooks/useCopilotSession";
import { clickableRowProps, formatRelativeTime } from "@/lib/utils";

function jobStatusVariant(status: string) {
  switch (status) {
    case "running":
    case "succeeded":
      return "healthy" as const;
    case "failed":
    case "failed_fatal":
      return "danger" as const;
    case "denied":
      return "governance" as const;
    case "failed_retryable":
    case "pending":
    case "scheduled":
      return "warning" as const;
    case "dispatched":
      return "info" as const;
    default:
      return "muted" as const;
  }
}

function roleVariant(role: string): BadgeVariant {
  switch (role) {
    case "user":
      return "cordum";
    case "assistant":
      return "info";
    case "system":
      return "governance";
    default:
      return "muted";
  }
}

function verdictVariant(verdict: string): BadgeVariant {
  switch (verdict) {
    case "allow":
    case "allow_with_constraints":
      return "healthy";
    case "deny":
      return "danger";
    case "require_approval":
      return "governance";
    case "throttle":
      return "warning";
    default:
      return "muted";
  }
}

function safeRelativeTime(value?: string) {
  if (!value) return "—";
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return "—";
  return formatRelativeTime(parsed.toISOString());
}

export default function CopilotSessionPage() {
  const navigate = useNavigate();
  const { sessionId } = useParams<{ sessionId: string }>();
  const trimmed = (sessionId ?? "").trim();
  const { data, isLoading, isError, error, refetch } = useCopilotSession(trimmed);

  if (!trimmed) {
    return (
      <motion.div
        initial={{ opacity: 0, y: 8 }}
        animate={{ opacity: 1, y: 0 }}
        className="space-y-6"
      >
        <EmptyState
          title="Missing session id"
          description="The URL did not include a Copilot Session id. Open a session from the Jobs page to view its detail."
        />
        <div className="flex justify-center">
          <Button variant="outline" size="sm" onClick={() => navigate("/jobs")}>
            <ArrowLeft className="w-3 h-3 mr-1" />
            Back to Jobs
          </Button>
        </div>
      </motion.div>
    );
  }

  const backendPending = isError && error instanceof ApiError && error.status === 501;
  const messages = data?.session.messages ?? [];
  const decisions = data?.decisions ?? [];
  const jobs = data?.jobs ?? [];
  const knownJobIds = new Set(jobs.map((job) => job.id));

  return (
    <motion.div
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      className="space-y-6"
    >
      <PageHeader
        label="Copilot Session"
        title={trimmed}
        subtitle="Messages, governance decisions, and linked jobs for this Copilot session."
        actions={
          <Button variant="outline" size="sm" onClick={() => navigate("/jobs")}>
            <ArrowLeft className="w-3 h-3 mr-1" />
            Back to Jobs
          </Button>
        }
      />

      {isLoading ? (
        <SkeletonTable rows={5} />
      ) : backendPending ? (
        <>
          <div className="instrument-card border-dashed border-cordum/30 bg-cordum/5">
            <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-widest mb-1">
              Backend pending
            </p>
            <p className="text-sm text-foreground">
              Copilot session details are not available yet. Linked jobs and
              governance decisions will appear once the backend store is
              wired up.
            </p>
          </div>
          <LinkedJobsTable jobs={jobs} navigate={navigate} />
        </>
      ) : isError ? (
        <ErrorBanner
          message={error instanceof Error ? error.message : "Failed to load Copilot session"}
          onRetry={() => void refetch()}
        />
      ) : (
        <>
          {data?.truncated && (
            <div className="instrument-card border-dashed border-warning/30 bg-warning/5">
              <p className="text-sm text-foreground">
                Showing first 500 entries; older items truncated.
              </p>
            </div>
          )}

          <div data-testid="copilot-session-timeline" className="space-y-6">
            <MessageTimeline messages={messages} knownJobIds={knownJobIds} />
            <GovernanceDecisionsPanel decisions={decisions} />
            <LinkedJobsTable jobs={jobs} navigate={navigate} />
          </div>
        </>
      )}
    </motion.div>
  );
}

function MessageTimeline({
  messages,
  knownJobIds,
}: {
  messages: CopilotSessionDetailResponse["session"]["messages"];
  knownJobIds: Set<string>;
}) {
  return (
    <section className="instrument-card space-y-4">
      <div>
        <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-widest">
          Timeline
        </p>
        <h2 className="text-base font-display font-semibold text-foreground">
          Messages
        </h2>
      </div>
      {messages.length === 0 ? (
        <EmptyState
          title="No messages yet"
          description="This Copilot session has not recorded any chat messages yet."
          className="py-10"
        />
      ) : (
        <ol className="space-y-3">
          {messages.map((message) => (
            <li
              key={message.id}
              className="border-l border-border pl-4 py-1"
            >
              <div className="flex flex-wrap items-center gap-2 mb-2">
                <StatusBadge variant={roleVariant(message.role)}>
                  {message.role}
                </StatusBadge>
                <span className="font-mono text-xs text-muted-foreground">
                  {safeRelativeTime(message.timestamp)}
                </span>
              </div>
              <p className="text-sm text-foreground whitespace-pre-wrap">
                {message.content}
              </p>
              {message.jobIds && message.jobIds.length > 0 && (
                <div className="mt-3 flex flex-wrap gap-2">
                  {/*
                   * Render every job id the message references. The API
                   * exposes message.jobIds independently of the enriched
                   * jobs array (which can be capped by
                   * copilotSessionAggregateLimit) and `/jobs/:jobId` only
                   * needs the id, so a chip remains useful even when the
                   * enriched view was truncated. Use knownJobIds purely
                   * for styling so users can distinguish jobs whose
                   * metadata loaded from those that did not.
                   */}
                  {message.jobIds.map((jobId) => (
                    <Link key={jobId} to={`/jobs/${jobId}`} className="inline-flex">
                      <StatusBadge variant={knownJobIds.has(jobId) ? "info" : "muted"}>
                        {jobId}
                      </StatusBadge>
                    </Link>
                  ))}
                </div>
              )}
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}

function GovernanceDecisionsPanel({ decisions }: { decisions: CopilotSessionDecision[] }) {
  return (
    <section className="instrument-card space-y-4">
      <div>
        <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-widest">
          Governance
        </p>
        <h2 className="text-base font-display font-semibold text-foreground">
          Governance decisions
        </h2>
      </div>
      {decisions.length === 0 ? (
        <EmptyState
          title="No governance decisions for this session"
          description="No policy decisions have been linked to this Copilot session yet."
          className="py-10"
        />
      ) : (
        <div className="divide-y divide-border">
          {decisions.map((decision) => (
            <div key={`${decision.jobId}-${decision.timestamp}-${decision.matchedRule}`} className="py-3 first:pt-0 last:pb-0">
              <div className="flex flex-wrap items-center gap-2">
                <StatusBadge variant={verdictVariant(decision.verdict)} dot>
                  {decision.verdict}
                </StatusBadge>
                <span className="font-mono text-xs text-muted-foreground">
                  {safeRelativeTime(decision.timestamp)}
                </span>
              </div>
              <div className="mt-2 grid gap-2 text-sm md:grid-cols-3">
                <DecisionField label="Rule" value={decision.matchedRule || decision.ruleName || "—"} />
                <DecisionField label="Agent" value={decision.agentId || "—"} />
                <DecisionField label="Job" value={decision.jobId} />
              </div>
              {decision.reason && (
                <p className="mt-2 text-xs text-muted-foreground">
                  {decision.reason}
                </p>
              )}
            </div>
          ))}
        </div>
      )}
    </section>
  );
}

function DecisionField({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-widest">
        {label}
      </p>
      <p className="font-mono text-xs text-foreground break-all">{value}</p>
    </div>
  );
}

function LinkedJobsTable({ jobs, navigate }: { jobs: CopilotSessionJob[]; navigate: ReturnType<typeof useNavigate> }) {
  return (
    <section className="space-y-3">
      <div>
        <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-widest">
          Jobs
        </p>
        <h2 className="text-base font-display font-semibold text-foreground">
          Linked jobs
        </h2>
      </div>
      {jobs.length === 0 ? (
        <EmptyState
          title="No jobs yet"
          description="No jobs linked to this session yet."
          className="instrument-card py-10"
        />
      ) : (
        <div className="instrument-card overflow-hidden p-0">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border">
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">
                  Job ID
                </th>
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">
                  Topic
                </th>
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">
                  Status
                </th>
                <th className="text-right px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">
                  Updated
                </th>
              </tr>
            </thead>
            <tbody>
              {jobs.map((job) => (
                <tr
                  key={job.id}
                  {...clickableRowProps(() => navigate(`/jobs/${job.id}`))}
                  className="border-b border-border last:border-0 hover:bg-surface-1 transition-colors cursor-pointer"
                >
                  <td className="px-5 py-3 font-mono text-sm text-cordum">
                    {job.id.slice(0, 16)}
                  </td>
                  <td className="px-5 py-3 text-foreground">{job.topic ?? "—"}</td>
                  <td className="px-5 py-3">
                    <StatusBadge variant={jobStatusVariant(job.status)} dot>
                      {job.status}
                    </StatusBadge>
                  </td>
                  <td className="px-5 py-3 text-right text-xs text-muted-foreground font-mono">
                    {safeRelativeTime(job.updatedAt)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
