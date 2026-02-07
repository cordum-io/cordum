import { useParams, Link } from "react-router-dom";
import { ArrowLeft, ExternalLink, Clock } from "lucide-react";
import { useQuery } from "@tanstack/react-query";

import { useJob, useJobDecisions } from "../hooks/useJobs";
import { get } from "../api/client";
import { Card, CardHeader, CardTitle } from "../components/ui/Card";
import { Badge } from "../components/ui/Badge";
import { JobStatusBadge } from "../components/StatusBadge";
import { JobStateMachine } from "../components/jobs/JobStateMachine";
import { SafetyExplainCard } from "../components/jobs/SafetyExplainCard";
import { JobActions } from "../components/jobs/JobActions";

// ---------------------------------------------------------------------------
// Memory payload hook
// ---------------------------------------------------------------------------

function useMemoryPayload(ptr: string | undefined) {
  return useQuery<unknown>({
    queryKey: ["memory", ptr],
    queryFn: () => get<unknown>(`/memory?ptr=${encodeURIComponent(ptr ?? "")}`),
    enabled: !!ptr,
    staleTime: 60_000,
  });
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function fmtTimestamp(iso?: string): string {
  if (!iso) return "-";
  try {
    return new Date(iso).toLocaleString(undefined, {
      year: "numeric",
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    });
  } catch {
    return iso;
  }
}

function durationBetween(a?: string, b?: string): string | null {
  if (!a || !b) return null;
  const ms = new Date(b).getTime() - new Date(a).getTime();
  if (ms < 0 || isNaN(ms)) return null;
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`;
  return `${(ms / 60_000).toFixed(1)}m`;
}

// ---------------------------------------------------------------------------
// Timeline of state transitions (derived from job metadata)
// ---------------------------------------------------------------------------

interface TimelineEntry {
  state: string;
  timestamp: string;
}

function deriveTimeline(job: {
  createdAt: string;
  updatedAt: string;
  status: string;
  duration?: number;
}): TimelineEntry[] {
  const entries: TimelineEntry[] = [
    { state: "submitted", timestamp: job.createdAt },
  ];

  if (job.status !== "pending") {
    entries.push({ state: job.status, timestamp: job.updatedAt });
  }

  return entries;
}

// ---------------------------------------------------------------------------
// Payload viewer
// ---------------------------------------------------------------------------

function PayloadSection({
  title,
  ptr,
}: {
  title: string;
  ptr: string | undefined;
}) {
  const { data, isLoading, isError } = useMemoryPayload(ptr);

  if (!ptr) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>{title}</CardTitle>
        </CardHeader>
        <p className="text-sm text-muted">No payload pointer available.</p>
      </Card>
    );
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <span className="text-[10px] font-mono text-muted truncate max-w-[200px]" title={ptr}>
          {ptr}
        </span>
      </CardHeader>
      {isLoading && (
        <div className="animate-pulse space-y-2">
          <div className="h-4 w-3/4 rounded bg-surface2" />
          <div className="h-4 w-1/2 rounded bg-surface2" />
        </div>
      )}
      {isError && (
        <p className="text-sm text-danger">Failed to load payload.</p>
      )}
      {!isLoading && !isError && data !== undefined && (
        <pre className="max-h-64 overflow-auto rounded-xl bg-surface2 p-3 text-xs text-ink font-mono">
          {JSON.stringify(data, null, 2)}
        </pre>
      )}
    </Card>
  );
}

// ---------------------------------------------------------------------------
// JobDetailPage
// ---------------------------------------------------------------------------

export default function JobDetailPage() {
  const { id } = useParams<{ id: string }>();
  const { data: job, isLoading, isError } = useJob(id ?? "");
  const { data: decisions } = useJobDecisions(id ?? "");

  // Use first decision or job's inline decision
  const safetyDecision = decisions?.[0] ?? job?.safetyDecision;
  const timeline = job ? deriveTimeline(job) : [];

  if (isLoading) {
    return (
      <div className="space-y-6">
        <div className="flex items-center gap-3">
          <div className="h-8 w-8 animate-spin rounded-full border-4 border-accent border-t-transparent" />
          <span className="text-sm text-muted">Loading job...</span>
        </div>
      </div>
    );
  }

  if (isError || !job) {
    return (
      <div className="space-y-4">
        <Link to="/jobs" className="inline-flex items-center gap-1 text-sm text-accent hover:underline">
          <ArrowLeft className="h-4 w-4" /> Back to Jobs
        </Link>
        <Card>
          <p className="py-8 text-center text-sm text-muted">
            Job not found or failed to load.
          </p>
        </Card>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Back link */}
      <Link to="/jobs" className="inline-flex items-center gap-1 text-sm text-accent hover:underline">
        <ArrowLeft className="h-4 w-4" /> Back to Jobs
      </Link>

      {/* Header */}
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="font-display text-2xl font-bold text-ink">
            Job {job.id.slice(0, 8)}...
          </h1>
          <div className="mt-1 flex items-center gap-3 text-sm text-muted">
            <span className="font-mono">{job.topic}</span>
            <span>&middot;</span>
            <span>Pool: {job.pool || "\u2014"}</span>
            {job.duration !== undefined && (
              <>
                <span>&middot;</span>
                <span>{job.duration}ms</span>
              </>
            )}
          </div>
        </div>
        <div className="flex items-center gap-3">
          <JobStatusBadge state={job.status} />
          {safetyDecision && (
            <Badge variant={
              safetyDecision.type === "allow" ? "success" :
              safetyDecision.type === "deny" ? "danger" :
              safetyDecision.type === "require_approval" ? "warning" : "info"
            }>
              {safetyDecision.type.replace(/_/g, " ")}
            </Badge>
          )}
        </div>
      </div>

      {/* Job actions */}
      <JobActions job={job} />

      {/* State machine visualization */}
      <Card>
        <CardHeader>
          <CardTitle>Lifecycle</CardTitle>
        </CardHeader>
        <div className="flex justify-center py-2">
          <JobStateMachine status={job.status} />
        </div>
      </Card>

      {/* Safety explain */}
      {safetyDecision && (
        <section>
          <h2 className="mb-3 text-xs font-semibold uppercase tracking-wider text-muted">
            Safety Evaluation
          </h2>
          <SafetyExplainCard decision={safetyDecision} />
        </section>
      )}

      {/* Payloads */}
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <PayloadSection title="Request Payload" ptr={job.contextPtr} />
        <PayloadSection title="Result Payload" ptr={job.resultPtr} />
      </div>

      {/* Timeline */}
      {timeline.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle>Timeline</CardTitle>
          </CardHeader>
          <div className="space-y-0">
            {timeline.map((entry, i) => {
              const dur = i > 0 ? durationBetween(timeline[i - 1].timestamp, entry.timestamp) : null;
              return (
                <div key={i} className="flex items-center gap-3 border-b border-border/40 py-2 last:border-b-0">
                  <Clock className="h-3.5 w-3.5 shrink-0 text-muted" />
                  <span className="text-xs font-semibold text-ink capitalize w-24">{entry.state}</span>
                  <span className="text-xs text-muted font-mono">{fmtTimestamp(entry.timestamp)}</span>
                  {dur && (
                    <span className="ml-auto text-[10px] text-muted">+{dur}</span>
                  )}
                </div>
              );
            })}
          </div>
        </Card>
      )}

      {/* Workflow context link */}
      {job.workflowRunId && (
        <Card>
          <div className="flex items-center justify-between">
            <div>
              <h3 className="text-sm font-semibold text-ink">Part of Workflow Run</h3>
              <p className="mt-0.5 text-xs text-muted font-mono">{job.workflowRunId}</p>
            </div>
            <Link
              to="/workflows"
              className="inline-flex items-center gap-1.5 rounded-full border border-border px-3 py-1.5 text-xs font-semibold text-ink hover:border-accent hover:text-accent transition-colors"
            >
              <ExternalLink className="h-3.5 w-3.5" />
              View Workflow
            </Link>
          </div>
        </Card>
      )}
    </div>
  );
}
