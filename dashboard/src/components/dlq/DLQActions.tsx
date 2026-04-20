import { useState, useCallback } from "react";
import { RefreshCw, Trash2 } from "lucide-react";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { useRetryDLQ, useDeleteDLQ } from "../../hooks/useDLQ";
import { logger } from "../../lib/logger";

// ---------------------------------------------------------------------------
// Confirm dialog
// ---------------------------------------------------------------------------

function ConfirmDialog({
  title,
  message,
  confirmLabel,
  variant,
  isPending,
  onConfirm,
  onCancel,
}: {
  title: string;
  message: string;
  confirmLabel: string;
  variant: "primary" | "danger";
  isPending: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <Card className="relative z-10 w-full max-w-sm">
        <div className="space-y-4">
          <h3 className="font-display text-lg font-semibold text-ink">
            {title}
          </h3>
          <p className="text-sm text-muted-foreground">{message}</p>
          <div className="flex justify-end gap-2">
            <Button
              variant="ghost"
              size="sm"
              onClick={onCancel}
              disabled={isPending}
            >
              Cancel
            </Button>
            <Button
              variant={variant}
              size="sm"
              onClick={onConfirm}
              disabled={isPending}
            >
              {isPending ? "Processing..." : confirmLabel}
            </Button>
          </div>
        </div>
      </Card>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Single-row action buttons
// ---------------------------------------------------------------------------

export function DLQRowActions({
  entryId,
  onSuccess,
}: {
  entryId: string;
  onSuccess?: () => void;
}) {
  const retryDLQ = useRetryDLQ();
  const deleteDLQ = useDeleteDLQ();
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);
  const [feedback, setFeedback] = useState<{
    type: "success" | "error";
    msg: string;
  } | null>(null);

  const clearFeedback = useCallback(() => {
    setTimeout(() => setFeedback(null), 2000);
  }, []);

  const handleRetry = useCallback(() => {
    logger.info("dlq-actions", "Retry clicked", { entryId });
    retryDLQ.mutate(
      { id: entryId },
      {
        onSuccess: () => {
          setFeedback({ type: "success", msg: "Retried" });
          clearFeedback();
          onSuccess?.();
        },
        onError: (err) => {
          setFeedback({ type: "error", msg: err.message });
          clearFeedback();
        },
      },
    );
  }, [entryId, retryDLQ, clearFeedback, onSuccess]);

  const handleDelete = useCallback(() => {
    logger.info("dlq-actions", "Delete clicked", { entryId });
    deleteDLQ.mutate(entryId, {
      onSuccess: () => {
        setShowDeleteConfirm(false);
        setFeedback({ type: "success", msg: "Deleted" });
        clearFeedback();
        onSuccess?.();
      },
      onError: (err) => {
        setFeedback({ type: "error", msg: err.message });
        clearFeedback();
      },
    });
  }, [entryId, deleteDLQ, clearFeedback, onSuccess]);

  const isPending = retryDLQ.isPending || deleteDLQ.isPending;

  return (
    <>
      <div className="flex items-center gap-1">
        {feedback && (
          <span
            className={`text-xs font-semibold ${
              feedback.type === "success" ? "text-success" : "text-danger"
            }`}
          >
            {feedback.msg}
          </span>
        )}
        <Button
          variant="outline"
          size="sm"
          onClick={handleRetry}
          disabled={isPending}
          title="Retry"
        >
          <RefreshCw className="h-3.5 w-3.5" />
        </Button>
        <Button
          variant="ghost"
          size="sm"
          className="text-danger hover:bg-danger/10"
          onClick={() => setShowDeleteConfirm(true)}
          disabled={isPending}
          title="Delete / Acknowledge"
        >
          <Trash2 className="h-3.5 w-3.5" />
        </Button>
      </div>

      {showDeleteConfirm && (
        <ConfirmDialog
          title="Delete DLQ Entry"
          message="Are you sure you want to delete this entry? This action cannot be undone."
          confirmLabel="Delete"
          variant="danger"
          isPending={deleteDLQ.isPending}
          onConfirm={handleDelete}
          onCancel={() => setShowDeleteConfirm(false)}
        />
      )}
    </>
  );
}
