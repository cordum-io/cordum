import { useState, useCallback } from "react";
import { useParams, Link, useNavigate } from "react-router-dom";
import {
  ArrowLeft,
  Loader,
  RefreshCw,
  XCircle,
  Trash2,
  Clock,
  ChevronDown,
  ChevronUp,
  AlertTriangle,
  Shield,
} from "lucide-react";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { Badge } from "../components/ui/Badge";
import { isValidResourceId } from "../lib/utils";
import { RunStatusBadge } from "../components/StatusBadge";
import { GanttTimeline } from "../components/workflow/GanttTimeline";
import { RunVisualization } from "../components/workflow/RunVisualization";
import { useRun, useRerunRun, useCancelRun, useDeleteRun } from "../hooks/useWorkflows";
import type { WorkflowStep } from "../api/types";
import { usePageTitle } from "../hooks/usePageTitle";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function formatDuration(ms: number | undefined): string {
  if (!ms) return "—";
  if (ms < 1_000) return `${Math.round(ms)}ms`;
  const secs = ms / 1_000;
  if (secs < 60) return `${secs.toFixed(1)}s`;
  const mins = Math.floor(secs / 60);
  const remSecs = Math.round(secs % 60);
  return `${mins}m ${remSecs}s`;
}

function computeDuration(run: { startedAt?: string | null; completedAt?: string | null }): number | undefined {
  if (!run.startedAt) return undefined;
  const end = run.completedAt ? new Date(run.completedAt).getTime() : Date.now();
  return end - new Date(run.startedAt).getTime();
}

const safetyVariant: Record<string, "success" | "danger" | "warning" | "info"> = {
  allow: "success",
  deny: "danger",
  require_approval: "warning",
  throttle: "info",
};

// ---------------------------------------------------------------------------
// Confirm Dialog
// ---------------------------------------------------------------------------

function ConfirmDialog({
  title,
  message,
  confirmLabel,
  isPending,
  onConfirm,
  onCancel,
  variant = "primary",
}: {
  title: string;
  message: string;
  confirmLabel: string;
  isPending: boolean;
  onConfirm: () => void;
  onCancel: () => void;
  variant?: "primary" | "danger";
}) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <Card className="relative z-10 w-full max-w-sm">
        <div className="space-y-4">
          <h3 className="font-display text-lg font-semibold text-ink">{title}</h3>
          <p className="text-sm text-muted">{message}</p>
          <div className="flex justify-end gap-2">
            <Button variant="ghost" size="sm" onClick={onCancel} disabled={isPending}>
              Cancel
            </Button>
            <Button variant={variant} size="sm" onClick={onConfirm} disabled={isPending}>
              {isPending ? "Processing…" : confirmLabel}
            </Button>
          </div>
        </div>
      </Card>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Step List Item
// ---------------------------------------------------------------------------

function StepItem({ step }: { step: WorkflowStep }) {
  const [expanded, setExpanded] = useState(false);
  const duration = computeDuration(step);
  const safetyDecision = step.output?.safetyDecision as string | undefined;

  return (
    <div className="border-b border-border last:border-b-0">
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        className="flex w-full items-center gap-3 px-4 py-3 text-left transition hover:bg-surface2/40"
      >
        <RunStatusBadge status={step.status} />
        <span className="flex-1 text-sm font-medium text-ink">
          {step.name || step.id}
        </span>
        {safetyDecision && (
          <Badge variant={safetyVariant[safetyDecision] ?? "default"} className="text-[10px]">
            <Shield className="mr-0.5 h-3 w-3" />
            {safetyDecision}
          </Badge>
        )}
        {duration != null && (
          <span className="flex items-center gap-1 text-xs text-muted">
            <Clock className="h-3 w-3" />
            {formatDuration(duration)}
          </span>
        )}
        {expanded ? (
          <ChevronUp className="h-4 w-4 text-muted" />
        ) : (
          <ChevronDown className="h-4 w-4 text-muted" />
        )}
      </button>

      {expanded && (
        <div className="space-y-2 border-t border-border bg-surface2/20 px-4 py-3">
          <div className="grid grid-cols-2 gap-2 text-xs">
            <div>
              <span className="text-muted">Type:</span>{" "}
              <span className="text-ink">{step.type}</span>
            </div>
            <div>
              <span className="text-muted">ID:</span>{" "}
              <span className="font-mono text-ink">{step.id}</span>
            </div>
            {step.startedAt && (
              <div>
                <span className="text-muted">Started:</span>{" "}
                <span className="text-ink">{new Date(step.startedAt).toLocaleString()}</span>
              </div>
            )}
            {step.completedAt && (
              <div>
                <span className="text-muted">Completed:</span>{" "}
                <span className="text-ink">{new Date(step.completedAt).toLocaleString()}</span>
              </div>
            )}
          </div>
          {(step.depends_on ?? step.dependsOn)?.length ? (
            <div className="text-xs">
              <span className="text-muted">Depends on:</span>{" "}
              <span className="font-mono text-ink">{(step.depends_on ?? step.dependsOn)!.join(", ")}</span>
            </div>
          ) : null}
          {step.error && (
            <div className="flex items-start gap-1.5 rounded-lg border border-danger/30 bg-danger/5 px-3 py-2 text-xs text-danger">
              <AlertTriangle className="mt-0.5 h-3.5 w-3.5 flex-shrink-0" />
              <span>{step.error}</span>
            </div>
          )}
          {step.output && !step.error && (
            <pre className="max-h-40 overflow-auto rounded-lg border border-border bg-surface2/30 p-2 text-[11px] text-muted">
              {JSON.stringify(step.output, null, 2)}
            </pre>
          )}
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// RunDetailPage
// ---------------------------------------------------------------------------

export default function RunDetailPage() {
  const { id: rawWorkflowId, runId: rawRunId } = useParams<{ id: string; runId: string }>();
  const workflowId = isValidResourceId(rawWorkflowId) ? rawWorkflowId : undefined;
  const runId = isValidResourceId(rawRunId) ? rawRunId : undefined;
  usePageTitle(runId ? `Run ${runId.slice(0, 8)}` : "Run");
  const { data: run, isLoading, isError } = useRun(runId);

  const navigate = useNavigate();
  const rerunRun = useRerunRun();
  const cancelRun = useCancelRun();
  const deleteRun = useDeleteRun();

  const [showRerunConfirm, setShowRerunConfirm] = useState(false);
  const [showCancelConfirm, setShowCancelConfirm] = useState(false);
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);

  const handleRerun = useCallback(() => {
    if (!runId) return;
    rerunRun.mutate({ runId }, { onSuccess: () => setShowRerunConfirm(false) });
  }, [runId, rerunRun]);

  const handleCancel = useCallback(() => {
    if (!workflowId || !runId) return;
    cancelRun.mutate(
      { workflowId, runId },
      { onSuccess: () => setShowCancelConfirm(false) },
    );
  }, [workflowId, runId, cancelRun]);

  const handleDelete = useCallback(() => {
    if (!workflowId || !runId) return;
    deleteRun.mutate(
      { workflowId, runId },
      { onSuccess: () => navigate(`/workflows/${workflowId}`) },
    );
  }, [workflowId, runId, deleteRun, navigate]);

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-16 text-sm text-muted">
        <Loader className="mr-2 h-4 w-4 animate-spin" />
        Loading run details...
      </div>
    );
  }

  if (isError || !run) {
    return (
      <div className="py-16 text-center text-sm text-danger">
        Failed to load run details. The run may not exist.
      </div>
    );
  }

  const duration = run.duration ?? computeDuration(run);
  const isRunning = run.status === "running" || run.status === "waiting" || run.status === "pending";
  const isTerminal = ["succeeded", "failed", "cancelled", "timed_out"].includes(run.status);

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="space-y-1">
          <Link
            to={`/workflows/${workflowId}`}
            className="inline-flex items-center gap-1 text-xs text-accent hover:underline"
          >
            <ArrowLeft className="h-3 w-3" />
            Back to workflow
          </Link>
          <h1 className="font-display text-xl font-bold text-ink">
            Run <span className="font-mono text-accent">{run.id.slice(0, 12)}</span>
          </h1>
          <div className="flex flex-wrap items-center gap-3 text-xs text-muted">
            <RunStatusBadge status={run.status} />
            {run.startedAt && (
              <span>Started {new Date(run.startedAt).toLocaleString()}</span>
            )}
            {duration != null && (
              <span className="flex items-center gap-1">
                <Clock className="h-3 w-3" />
                {formatDuration(duration)}
              </span>
            )}
            {run.rerunOf && (
              <span>
                Rerun of{" "}
                <Link
                  to={`/workflows/${workflowId}/runs/${run.rerunOf}`}
                  className="font-mono text-accent hover:underline"
                >
                  {run.rerunOf.slice(0, 12)}
                </Link>
              </span>
            )}
            {run.dryRun && <Badge variant="warning">Dry Run</Badge>}
          </div>
        </div>

        {/* Actions */}
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => setShowRerunConfirm(true)}
            disabled={rerunRun.isPending}
          >
            <RefreshCw className="h-3.5 w-3.5" />
            Rerun
          </Button>
          {isRunning && (
            <Button
              variant="danger"
              size="sm"
              onClick={() => setShowCancelConfirm(true)}
              disabled={cancelRun.isPending}
            >
              <XCircle className="h-3.5 w-3.5" />
              Cancel
            </Button>
          )}
          {isTerminal && (
            <Button
              variant="ghost"
              size="sm"
              className="text-danger hover:bg-danger/10"
              onClick={() => setShowDeleteConfirm(true)}
              disabled={deleteRun.isPending}
              title="Permanently removes this run and its data. Only completed/failed/cancelled runs can be deleted."
            >
              <Trash2 className="h-3.5 w-3.5" />
              Delete
            </Button>
          )}
        </div>
      </div>

      {/* DAG Visualization */}
      <Card>
        <div className="p-4">
          <h2 className="mb-3 text-xs font-semibold uppercase tracking-wide text-muted">
            DAG Visualization
          </h2>
          <div style={{ height: 320 }}>
            <RunVisualization run={run} />
          </div>
        </div>
      </Card>

      {/* Gantt Timeline */}
      {run.steps.length > 0 && <GanttTimeline steps={run.steps} />}

      {/* Step List */}
      <Card>
        <div className="px-4 py-3">
          <h2 className="text-xs font-semibold uppercase tracking-wide text-muted">
            Steps ({run.steps.length})
          </h2>
        </div>
        {run.steps.length === 0 ? (
          <div className="px-4 pb-4 text-sm text-muted">No steps in this run.</div>
        ) : (
          <div className="border-t border-border">
            {run.steps.map((step) => (
              <StepItem key={step.id} step={step} />
            ))}
          </div>
        )}
      </Card>

      {/* Run Error */}
      {run.error && (
        <div className="flex items-start gap-2 rounded-2xl border border-danger/30 bg-danger/5 px-4 py-3 text-sm text-danger">
          <AlertTriangle className="mt-0.5 h-4 w-4 flex-shrink-0" />
          <pre className="whitespace-pre-wrap">{JSON.stringify(run.error, null, 2)}</pre>
        </div>
      )}

      {/* Confirm dialogs */}
      {showRerunConfirm && (
        <ConfirmDialog
          title="Rerun this workflow?"
          message="This will create a new run with the same input. The original run is not affected."
          confirmLabel="Rerun"
          isPending={rerunRun.isPending}
          onConfirm={handleRerun}
          onCancel={() => setShowRerunConfirm(false)}
        />
      )}
      {showCancelConfirm && (
        <ConfirmDialog
          title="Cancel this run?"
          message="This will attempt to cancel the currently running workflow. Steps already completed will not be rolled back."
          confirmLabel="Cancel Run"
          isPending={cancelRun.isPending}
          onConfirm={handleCancel}
          onCancel={() => setShowCancelConfirm(false)}
          variant="danger"
        />
      )}
      {showDeleteConfirm && (
        <ConfirmDialog
          title={`Delete run ${run.id.slice(0, 8)}?`}
          message="This permanently removes the run and its timeline data. This cannot be undone."
          confirmLabel="Delete"
          isPending={deleteRun.isPending}
          onConfirm={handleDelete}
          onCancel={() => setShowDeleteConfirm(false)}
          variant="danger"
        />
      )}
    </div>
  );
}
