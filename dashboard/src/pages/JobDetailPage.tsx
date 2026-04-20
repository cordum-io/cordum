import { useMemo, useState } from "react";
import { useParams, Link } from "react-router-dom";
import { ArrowLeft, ExternalLink, Loader2 } from "lucide-react";

import { useJob, useJobDecisions, useJobEvents, useRemediateJob } from "../hooks/useJobs";
import { useOutputFindings } from "../hooks/useOutputPolicy";
import { useApproveJob, useRejectJob } from "../hooks/useApprovals";
import { useJobStream } from "../hooks/useJobStream";
import { usePermission } from "../hooks/usePermission";
import { usePageTitle } from "../hooks/usePageTitle";
import { isValidResourceId, cn } from "../lib/utils";
import { queryKeys } from "../lib/queryKeys";
import { buildUnifiedTimeline } from "../api/transform";

import { Badge } from "../components/ui/Badge";
import { Card } from "../components/ui/Card";
import { JobStatusBadge } from "../components/StatusBadge";
import { JobActions } from "../components/jobs/JobActions";
import { UnifiedTimeline } from "../components/jobs/UnifiedTimeline";
import { GovernanceTrace } from "../components/jobs/GovernanceTrace";
import { ContextSidebar } from "../components/jobs/ContextSidebar";
import { RemediateDrawer } from "../components/jobs/RemediateDrawer";
import { useQueryClient } from "@tanstack/react-query";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function formatDuration(ms?: number): string | null {
  if (ms === undefined || ms === null) return null;
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`;
  return `${(ms / 60_000).toFixed(1)}m`;
}

const DECISION_VARIANT: Record<string, "success" | "danger" | "warning" | "info"> = {
  allow: "success",
  deny: "danger",
  require_approval: "warning",
  throttle: "info",
};

const TERMINAL_STATES = new Set([
  "succeeded", "failed", "cancelled", "denied", "timeout", "output_quarantined",
]);

// ---------------------------------------------------------------------------
// JobDetailPage
// ---------------------------------------------------------------------------

export default function JobDetailPage() {
  const { id: rawId } = useParams<{ id: string }>();
  const id = isValidResourceId(rawId) ? rawId : undefined;
  const queryClient = useQueryClient();

  // ── Data fetching ──────────────────────────────────────────────────
  // Two-pass: first render uses no polling, subsequent renders adjust based on status.
  const [pollingEnabled, setPollingEnabled] = useState(false);
  const { data: job, isLoading, isError } = useJob(id ?? "", {
    refetchInterval: pollingEnabled ? 3000 : false,
  });
  const { data: decisions } = useJobDecisions(id ?? "");
  const { data: rawEvents } = useJobEvents(id ?? "");
  const isQuarantined = job?.status === "output_quarantined";
  useOutputFindings(isQuarantined ? (id ?? "") : "");

  const jobIsRunning = job?.status === "running";
  const jobIsTerminal = job ? TERMINAL_STATES.has(job.status) : false;

  // Sync polling state with job status
  if (jobIsRunning && !pollingEnabled) setPollingEnabled(true);
  if (jobIsTerminal && pollingEnabled) setPollingEnabled(false);

  // Live progress stream
  const { progressEvents, latestPercent, isConnected, lastHeartbeat } = useJobStream(
    id ?? "",
    jobIsRunning,
  );

  // Permissions
  const { allowed: canApprove } = usePermission(["admin", "operator"]);

  // Mutations
  const approveMutation = useApproveJob();
  const rejectMutation = useRejectJob();
  const remediateMutation = useRemediateJob();

  // Drawer state
  const [drawerOpen, setDrawerOpen] = useState(false);

  // ── Derived state ──────────────────────────────────────────────────
  const primaryDecision = decisions?.[0] ?? job?.safetyDecision;
  const timelineEvents = useMemo(
    () => (job ? buildUnifiedTimeline(rawEvents ?? [], job) : []),
    [rawEvents, job],
  );

  usePageTitle(id ? `Job ${id.slice(0, 8)}` : "Job");

  // ── Action handlers ────────────────────────────────────────────────
  function handleApprove(note?: string) {
    if (!job?.approvalRef && !id) return;
    const approvalId = job?.approvalRef ?? id ?? "";
    approveMutation.mutate(
      { id: approvalId, comment: note },
      {
        onSuccess: () => {
          queryClient.invalidateQueries({ queryKey: queryKeys.jobs.detail(id ?? "") });
          queryClient.invalidateQueries({ queryKey: queryKeys.jobs.events(id ?? "") });
        },
      },
    );
  }

  function handleReject(reason: string) {
    if (!job?.approvalRef && !id) return;
    const approvalId = job?.approvalRef ?? id ?? "";
    rejectMutation.mutate(
      { id: approvalId, reason },
      {
        onSuccess: () => {
          queryClient.invalidateQueries({ queryKey: queryKeys.jobs.detail(id ?? "") });
          queryClient.invalidateQueries({ queryKey: queryKeys.jobs.events(id ?? "") });
        },
      },
    );
  }

  function handleQuickRemediate() {
    if (!id || !job) return;
    remediateMutation.mutate({
      jobId: id,
      input: {
        topic: job.topic,
        prompt: "",
        reason: "Quick remediation from job detail",
        risk_tags: job.riskTags,
      },
    });
  }

  // ── Loading state ──────────────────────────────────────────────────
  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-20">
        <Loader2 className="h-8 w-8 animate-spin text-[hsl(var(--accent))]" />
        <span className="ml-3 text-sm text-muted-foreground">Loading job...</span>
      </div>
    );
  }

  // ── Error state ────────────────────────────────────────────────────
  if (isError || !job) {
    return (
      <div className="space-y-4">
        <Link
          to="/jobs"
          className="inline-flex items-center gap-1 text-sm text-[hsl(var(--accent))] hover:underline"
        >
          <ArrowLeft className="h-4 w-4" /> Back to Jobs
        </Link>
        <Card>
          <p className="py-8 text-center text-sm text-muted-foreground">
            Job not found or failed to load.
          </p>
        </Card>
      </div>
    );
  }

  // ── Main layout ────────────────────────────────────────────────────
  return (
    <div className="space-y-6">
      {/* Header strip */}
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="space-y-1">
          <Link
            to="/jobs"
            className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-ink transition-colors"
          >
            <ArrowLeft className="h-3.5 w-3.5" /> Back to Jobs
          </Link>
          <div className="flex items-center gap-3">
            <h1 className="font-mono text-lg font-bold text-ink">
              {job.id.slice(0, 8)}...
            </h1>
            <span className="rounded bg-surface2 px-2 py-0.5 font-mono text-xs text-muted-foreground">
              {job.topic}
            </span>
            {job.pool && (
              <span className="text-xs text-muted-foreground">
                {job.pool}
              </span>
            )}
            {job.duration !== undefined && (
              <span className="font-mono text-xs text-muted-foreground">
                {formatDuration(job.duration)}
              </span>
            )}
          </div>
          <div className="flex items-center gap-2">
            <JobStatusBadge state={job.status} />
            {primaryDecision && (
              <Badge variant={DECISION_VARIANT[primaryDecision.type] ?? "info"}>
                {primaryDecision.type.replace(/_/g, " ")}
              </Badge>
            )}
          </div>
        </div>
        <JobActions job={job} />
      </div>

      {/* Governance trace breadcrumb */}
      <GovernanceTrace job={job} decision={primaryDecision} />

      {/* Main content: 2-column responsive layout */}
      <div className="lg:grid lg:grid-cols-[3fr_2fr] lg:gap-6">
        {/* Left: Timeline */}
        <UnifiedTimeline
          events={timelineEvents}
          job={job}
          progressEvents={progressEvents}
          latestPercent={latestPercent}
          isStreaming={isConnected}
          lastHeartbeat={lastHeartbeat}
          onApprove={handleApprove}
          onReject={handleReject}
          onRemediate={() => setDrawerOpen(true)}
          onQuickRemediate={handleQuickRemediate}
          canApprove={canApprove && job.status === "approval_required"}
        />

        {/* Right: Context sidebar */}
        <ContextSidebar job={job} decisions={decisions} />
      </div>

      {/* Bottom: Related context */}
      {(job.workflowRunId || job.workflowId) && (
        <Card>
          <div className="flex items-center justify-between p-4">
            <div>
              <h3 className="text-sm font-semibold text-ink">Part of Workflow</h3>
              {job.workflowId && (
                <p className="mt-0.5 font-mono text-xs text-muted-foreground">
                  Workflow: {job.workflowId}
                </p>
              )}
              {job.workflowRunId && (
                <p className="mt-0.5 font-mono text-xs text-muted-foreground">
                  Run: {job.workflowRunId}
                </p>
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
              className={cn(
                "inline-flex items-center gap-1.5 rounded-full border border-border",
                "px-3 py-1.5 text-xs font-semibold text-ink",
                "transition-colors hover:border-[hsl(var(--accent))] hover:text-[hsl(var(--accent))]",
              )}
            >
              <ExternalLink className="h-3.5 w-3.5" />
              View Workflow
            </Link>
          </div>
        </Card>
      )}

      {job.traceId && (
        <Link
          to={`/audit?correlationId=${job.traceId}`}
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-ink transition-colors"
        >
          <ExternalLink className="h-3.5 w-3.5" />
          View audit trail &rarr;
        </Link>
      )}

      {/* Remediate drawer (hidden until triggered) */}
      <RemediateDrawer
        open={drawerOpen}
        jobId={job.id}
        originalJob={job}
        onClose={() => setDrawerOpen(false)}
      />
    </div>
  );
}
