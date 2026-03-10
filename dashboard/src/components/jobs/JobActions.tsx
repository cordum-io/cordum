import { useState, useCallback } from "react";
import { useNavigate } from "react-router-dom";
import { Ban, RotateCcw, ShieldCheck } from "lucide-react";
import { Button } from "../ui/Button";
import { ConfirmDialog } from "../ui/ConfirmDialog";
import { useCancelJob, useRetryJob } from "../../hooks/useJobs";
import { logger } from "../../lib/logger";
import type { Job, JobStatus } from "../../api/types";

// ---------------------------------------------------------------------------
// State helpers
// ---------------------------------------------------------------------------

const TERMINAL_STATES: Set<JobStatus> = new Set([
  "succeeded",
  "failed",
  "cancelled",
  "denied",
  "timeout",
  "output_quarantined",
]);

const RETRYABLE_STATES: Set<JobStatus> = new Set([
  "failed",
  "timeout",
  "denied",
]);

const REMEDIABLE_STATES: Set<JobStatus> = new Set([
  "approval_required",
]);

type DialogAction = "cancel" | "retry" | "remediate" | null;

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

interface JobActionsProps {
  job: Job;
}

export function JobActions({ job }: JobActionsProps) {
  const navigate = useNavigate();
  const [activeDialog, setActiveDialog] = useState<DialogAction>(null);
  const [feedback, setFeedback] = useState<{
    type: "success" | "error";
    message: string;
  } | null>(null);

  const cancelMutation = useCancelJob();
  const retryMutation = useRetryJob();

  const canCancel = !TERMINAL_STATES.has(job.status);
  const canRetry = RETRYABLE_STATES.has(job.status);
  const canRemediate = REMEDIABLE_STATES.has(job.status);

  const close = useCallback(() => {
    setActiveDialog(null);
  }, []);

  const handleCancel = useCallback(() => {
    logger.info("job-actions", "Cancel clicked", { jobId: job.id });
    cancelMutation.mutate(job.id, {
      onSuccess: () => {
        setFeedback({ type: "success", message: "Job cancelled successfully." });
        close();
      },
      onError: (err) => {
        setFeedback({
          type: "error",
          message: err.message || "Failed to cancel job.",
        });
        close();
      },
    });
  }, [job.id, cancelMutation, close]);

  const handleRetry = useCallback(() => {
    logger.info("job-actions", "Retry clicked", { jobId: job.id });
    retryMutation.mutate({ id: job.id, topic: job.topic || "" }, {
      onSuccess: () => {
        setFeedback({ type: "success", message: "Job resubmitted for retry." });
        close();
      },
      onError: (err) => {
        setFeedback({
          type: "error",
          message: err.message || "Failed to retry job.",
        });
        close();
      },
    });
  }, [job.id, retryMutation, close]);

  const handleRemediate = useCallback(() => {
    logger.info("job-actions", "Remediate clicked", { jobId: job.id });
    close();
    navigate("/approvals");
  }, [close, navigate, job.id]);

  return (
    <>
      {/* Feedback banner */}
      {feedback && (
        <div
          className={`flex items-center justify-between rounded-xl px-4 py-2 text-sm ${
            feedback.type === "success"
              ? "bg-success/10 text-success"
              : "bg-danger/10 text-danger"
          }`}
        >
          <span>{feedback.message}</span>
          <button
            type="button"
            className="text-xs underline"
            onClick={() => setFeedback(null)}
          >
            Dismiss
          </button>
        </div>
      )}

      {/* Action buttons */}
      <div className="flex items-center gap-2">
        <Button
          variant="danger"
          size="sm"
          type="button"
          disabled={!canCancel}
          onClick={() => setActiveDialog("cancel")}
        >
          <Ban className="h-3.5 w-3.5" />
          Cancel
        </Button>
        <Button
          variant="outline"
          size="sm"
          type="button"
          disabled={!canRetry}
          onClick={() => setActiveDialog("retry")}
        >
          <RotateCcw className="h-3.5 w-3.5" />
          Retry
        </Button>
        {canRemediate && (
          <Button
            variant="ghost"
            size="sm"
            type="button"
            onClick={() => setActiveDialog("remediate")}
          >
            <ShieldCheck className="h-3.5 w-3.5" />
            Remediate
          </Button>
        )}
      </div>

      {/* Confirmation dialogs */}
      <ConfirmDialog
        open={activeDialog === "cancel"}
        title="Cancel Job"
        message={`Are you sure you want to cancel job ${job.id.slice(0, 8)}...? This will terminate the job immediately.`}
        confirmLabel="Cancel Job"
        confirmVariant="danger"
        isPending={cancelMutation.isPending}
        onConfirm={handleCancel}
        onCancel={close}
      />
      <ConfirmDialog
        open={activeDialog === "retry"}
        title="Retry Job"
        message={`Resubmit job ${job.id.slice(0, 8)}... with the same parameters? It will be re-queued for processing.`}
        confirmLabel="Retry Job"
        confirmVariant="primary"
        isPending={retryMutation.isPending}
        onConfirm={handleRetry}
        onCancel={close}
      />
      <ConfirmDialog
        open={activeDialog === "remediate"}
        title="Remediate Job"
        message={`This will navigate you to the approvals page where you can approve or override the safety decision for job ${job.id.slice(0, 8)}...`}
        confirmLabel="Go to Approvals"
        confirmVariant="primary"
        onConfirm={handleRemediate}
        onCancel={close}
      />
    </>
  );
}
