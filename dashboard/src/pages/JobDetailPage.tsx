import { useEffect, useMemo, useState } from "react";
import { useParams, Link } from "react-router-dom";
import { logger } from "../lib/logger";
import { ArrowLeft, ExternalLink, Clock, RotateCw } from "lucide-react";

import { useJob, useJobDecisions } from "../hooks/useJobs";
import { useOutputFindings, useReleaseQuarantinedJob } from "../hooks/useOutputPolicy";
import { Card, CardHeader, CardTitle } from "../components/ui/Card";
import { Badge } from "../components/ui/Badge";
import { Button } from "../components/ui/Button";
import { JobStatusBadge } from "../components/StatusBadge";
import { JobStateMachine } from "../components/jobs/JobStateMachine";
import { SafetyExplainCard } from "../components/jobs/SafetyExplainCard";
import { JobActions } from "../components/jobs/JobActions";
import { MemoryPanel } from "../components/jobs/MemoryPanel";
import { ArtifactPanel } from "../components/jobs/ArtifactPanel";
import { RemediateDrawer } from "../components/jobs/RemediateDrawer";
import { usePageTitle } from "../hooks/usePageTitle";

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
    logger.debug("job-detail", "Date formatting failed, returning raw ISO string");
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

type DetailTab = "overview" | "memory" | "artifacts";

// ---------------------------------------------------------------------------
// JobDetailPage
// ---------------------------------------------------------------------------

export default function JobDetailPage() {
  const { id } = useParams<{ id: string }>();
  usePageTitle(id ? `Job ${id.slice(0, 8)}` : "Job");
  const { data: job, isLoading, isError } = useJob(id ?? "");
  const { data: decisions } = useJobDecisions(id ?? "");
  const isQuarantined = job?.status === "output_quarantined";
  const { data: findings, isLoading: findingsLoading } = useOutputFindings(
    isQuarantined ? (id ?? "") : "",
  );
  const releaseMutation = useReleaseQuarantinedJob();

  // Use first decision or job's inline decision
  const safetyDecision = decisions?.[0] ?? job?.safetyDecision;
  const timeline = useMemo(() => (job ? deriveTimeline(job) : []), [job]);
  const hasMemory = !!job?.contextPtr;
  const hasArtifacts =
    !!job?.resultPtr ||
    !!job?.output_safety?.original_ptr ||
    !!job?.output_safety?.redacted_ptr;
  const canRemediate =
    job?.status === "failed" ||
    job?.status === "denied" ||
    job?.status === "output_quarantined";
  const availableTabs = useMemo(() => {
    const tabs: Array<{ key: DetailTab; label: string }> = [{ key: "overview", label: "Overview" }];
    if (hasMemory) {
      tabs.push({ key: "memory", label: "Memory" });
    }
    if (hasArtifacts) {
      tabs.push({ key: "artifacts", label: "Artifacts" });
    }
    return tabs;
  }, [hasArtifacts, hasMemory]);
  const [activeTab, setActiveTab] = useState<DetailTab>("overview");
  const [showRemediate, setShowRemediate] = useState(false);

  useEffect(() => {
    if (!availableTabs.some((tab) => tab.key === activeTab)) {
      setActiveTab("overview");
    }
  }, [activeTab, availableTabs]);

  useEffect(() => {
    if (!canRemediate && showRemediate) {
      setShowRemediate(false);
    }
  }, [canRemediate, showRemediate]);

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
      {canRemediate && (
        <div className="flex items-center gap-2">
          <Button
            type="button"
            size="sm"
            variant="outline"
            onClick={() => setShowRemediate(true)}
          >
            <RotateCw className="h-3.5 w-3.5" />
            Remediate
          </Button>
        </div>
      )}

      {availableTabs.length > 1 && (
        <div className="flex flex-wrap items-center gap-2">
          {availableTabs.map((tab) => (
            <button
              key={tab.key}
              type="button"
              className={
                activeTab === tab.key
                  ? "rounded-full bg-accent/15 px-4 py-1.5 text-xs font-semibold uppercase tracking-widest text-accent"
                  : "rounded-full px-4 py-1.5 text-xs font-semibold uppercase tracking-widest text-muted hover:text-ink"
              }
              onClick={() => setActiveTab(tab.key)}
            >
              {tab.label}
            </button>
          ))}
        </div>
      )}

      {activeTab === "overview" && (
        <>
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

          {/* Quarantine Findings */}
          {isQuarantined && (
            <Card>
              <CardHeader>
                <div className="flex items-center justify-between">
                  <CardTitle>Quarantine Findings</CardTitle>
                  <Button
                    type="button"
                    size="sm"
                    variant="primary"
                    disabled={releaseMutation.isPending}
                    onClick={() => releaseMutation.mutate(job.id)}
                  >
                    {releaseMutation.isPending ? "Releasing..." : "Release Job"}
                  </Button>
                </div>
              </CardHeader>
              {findingsLoading ? (
                <div className="flex items-center gap-2 py-6 justify-center">
                  <div className="h-5 w-5 animate-spin rounded-full border-2 border-accent border-t-transparent" />
                  <span className="text-xs text-muted">Loading findings...</span>
                </div>
              ) : findings && findings.length > 0 ? (
                <div className="overflow-x-auto">
                  <table className="w-full text-left text-xs">
                    <thead>
                      <tr className="border-b border-border/60 text-muted">
                        <th className="px-3 py-2 font-semibold">Severity</th>
                        <th className="px-3 py-2 font-semibold">Type</th>
                        <th className="px-3 py-2 font-semibold">Detail</th>
                        <th className="px-3 py-2 font-semibold">Scanner</th>
                        <th className="px-3 py-2 font-semibold">Confidence</th>
                      </tr>
                    </thead>
                    <tbody>
                      {findings.map((f, i) => (
                        <tr key={i} className="border-b border-border/30 last:border-b-0">
                          <td className="px-3 py-2">
                            <Badge variant={
                              f.severity === "critical" || f.severity === "high" ? "danger" :
                              f.severity === "medium" ? "warning" : "info"
                            }>
                              {f.severity}
                            </Badge>
                          </td>
                          <td className="px-3 py-2 font-mono">{f.type}</td>
                          <td className="px-3 py-2 max-w-xs truncate">{f.detail}</td>
                          <td className="px-3 py-2">{f.scanner ?? "-"}</td>
                          <td className="px-3 py-2">{f.confidence != null ? `${(f.confidence * 100).toFixed(0)}%` : "-"}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ) : (
                <p className="py-6 text-center text-xs text-muted">No findings available</p>
              )}
            </Card>
          )}

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
                      <span className="w-24 text-xs font-semibold capitalize text-ink">{entry.state}</span>
                      <span className="font-mono text-xs text-muted">{fmtTimestamp(entry.timestamp)}</span>
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
          {(job.workflowRunId || job.workflowId) && (
            <Card>
              <div className="flex items-center justify-between">
                <div>
                  <h3 className="text-sm font-semibold text-ink">Part of Workflow</h3>
                  {job.workflowId && (
                    <p className="mt-0.5 font-mono text-xs text-muted">Workflow: {job.workflowId}</p>
                  )}
                  {job.workflowRunId && (
                    <p className="mt-0.5 font-mono text-xs text-muted">Run: {job.workflowRunId}</p>
                  )}
                </div>
                <Link
                  to={
                    job.workflowId && job.workflowRunId
                      ? `/workflows/${job.workflowId}/runs/${job.workflowRunId}`
                      : job.workflowId
                        ? `/workflows/${job.workflowId}`
                        : "/workflows"
                  }
                  className="inline-flex items-center gap-1.5 rounded-full border border-border px-3 py-1.5 text-xs font-semibold text-ink transition-colors hover:border-accent hover:text-accent"
                >
                  <ExternalLink className="h-3.5 w-3.5" />
                  View Workflow
                </Link>
              </div>
            </Card>
          )}
        </>
      )}

      {activeTab === "memory" && hasMemory && (
        <MemoryPanel memoryPtr={job.contextPtr} jobId={job.id} />
      )}

      {activeTab === "artifacts" && hasArtifacts && (
        <ArtifactPanel jobId={job.id} />
      )}

      <RemediateDrawer
        open={showRemediate}
        jobId={job.id}
        originalJob={job}
        onClose={() => setShowRemediate(false)}
      />
    </div>
  );
}
