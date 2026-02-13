import { useState } from "react";
import { CheckCircle, X, XCircle } from "lucide-react";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { Textarea } from "../ui/Textarea";
import { useApproveApproval, useRejectApproval } from "../../hooks/useApprovals";
import { logger } from "../../lib/logger";
import type { Approval } from "../../api/types";

type ActionMode = "idle" | "approve" | "reject";

export function ApprovalDetailModal({
  approval,
  onClose,
}: {
  approval: Approval;
  onClose: () => void;
}) {
  const approve = useApproveApproval();
  const reject = useRejectApproval();

  const [mode, setMode] = useState<ActionMode>("idle");
  const [comment, setComment] = useState("");
  const [rejectReason, setRejectReason] = useState("");
  const [feedback, setFeedback] = useState<{
    type: "success" | "error";
    message: string;
  } | null>(null);

  const isPending = approve.isPending || reject.isPending;

  function handleApprove() {
    logger.info("approval-modal", "Approving", { id: approval.id });
    approve.mutate(
      { id: approval.id, comment: comment.trim() || undefined },
      {
        onSuccess: () => {
          setFeedback({ type: "success", message: "Approved successfully." });
          setTimeout(onClose, 800);
        },
        onError: (err) => {
          setFeedback({
            type: "error",
            message: err.message || "Failed to approve.",
          });
        },
      },
    );
  }

  function handleReject() {
    if (!rejectReason.trim()) return;
    logger.info("approval-modal", "Rejecting", { id: approval.id });
    reject.mutate(
      { id: approval.id, reason: rejectReason.trim() },
      {
        onSuccess: () => {
          setFeedback({ type: "success", message: "Rejected." });
          setTimeout(onClose, 800);
        },
        onError: (err) => {
          setFeedback({
            type: "error",
            message: err.message || "Failed to reject.",
          });
        },
      },
    );
  }

  const ctx = approval.jobContext ?? {};

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
      onClick={onClose}
    >
      <div
        className="surface-card w-full max-w-lg max-h-[85vh] overflow-y-auto rounded-3xl p-6 shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="mb-5 flex items-center justify-between">
          <h2 className="font-display text-lg font-semibold text-ink">
            Approval Detail
          </h2>
          <button
            onClick={onClose}
            className="rounded-full p-1 hover:bg-surface2"
          >
            <X className="h-4 w-4 text-muted" />
          </button>
        </div>

        {/* Job context */}
        <section className="mb-4">
          <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted">
            Job Context
          </h3>
          <div className="space-y-2">
            <div className="flex flex-wrap gap-2">
              {typeof ctx.type === "string" && (
                <Badge variant="default">{ctx.type}</Badge>
              )}
              {typeof ctx.topic === "string" && (
                <Badge variant="info">{ctx.topic}</Badge>
              )}
              {Array.isArray(ctx.capabilities) &&
                (ctx.capabilities as string[]).map((c) => (
                  <Badge key={c} variant="info">
                    {c}
                  </Badge>
                ))}
            </div>
            {Object.keys(ctx).length > 0 && (
              <pre className="mt-2 max-h-40 overflow-auto rounded-xl bg-surface2 p-3 text-xs text-ink">
                {JSON.stringify(ctx, null, 2)}
              </pre>
            )}
          </div>
        </section>

        {/* Safety explain */}
        <section className="mb-4">
          <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted">
            Safety Decision
          </h3>
          <div className="space-y-1 text-sm">
            {approval.policyRule && (
              <p>
                <span className="text-muted">Matched rule: </span>
                <span className="font-medium text-ink">
                  {approval.policyRule}
                </span>
              </p>
            )}
            {approval.reason && (
              <p>
                <span className="text-muted">Reason: </span>
                <span className="text-ink">{approval.reason}</span>
              </p>
            )}
          </div>
        </section>

        {/* Similar past approvals */}
        <section className="mb-5">
          <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted">
            History
          </h3>
          {approval.resolvedAt && approval.actor ? (
            <p className="text-sm text-muted">
              Last resolved by{" "}
              <span className="font-medium text-ink">{approval.actor}</span> at{" "}
              {new Date(approval.resolvedAt).toLocaleString()}
            </p>
          ) : (
            <p className="text-sm text-muted">No prior resolution on record.</p>
          )}
        </section>

        {/* Feedback */}
        {feedback && (
          <div
            className={`mb-4 rounded-xl px-4 py-2 text-sm font-medium ${
              feedback.type === "success"
                ? "bg-[color:rgba(31,122,87,0.12)] text-success"
                : "bg-[color:rgba(184,58,58,0.14)] text-danger"
            }`}
          >
            {feedback.message}
          </div>
        )}

        {/* Actions */}
        {mode === "idle" && (
          <div className="flex gap-3">
            <Button
              variant="primary"
              size="sm"
              onClick={() => setMode("approve")}
              disabled={isPending}
            >
              <CheckCircle className="h-4 w-4" />
              Approve
            </Button>
            <Button
              variant="danger"
              size="sm"
              onClick={() => setMode("reject")}
              disabled={isPending}
            >
              <XCircle className="h-4 w-4" />
              Reject
            </Button>
          </div>
        )}

        {mode === "approve" && (
          <div className="space-y-3">
            <Textarea
              placeholder="Optional comment..."
              rows={2}
              value={comment}
              onChange={(e) => setComment(e.target.value)}
            />
            <div className="flex gap-3">
              <Button
                variant="primary"
                size="sm"
                onClick={handleApprove}
                disabled={isPending}
              >
                {approve.isPending ? "Approving..." : "Confirm Approve"}
              </Button>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => setMode("idle")}
                disabled={isPending}
              >
                Cancel
              </Button>
            </div>
          </div>
        )}

        {mode === "reject" && (
          <div className="space-y-3">
            <Textarea
              placeholder="Reason for rejection (required)"
              rows={2}
              value={rejectReason}
              onChange={(e) => setRejectReason(e.target.value)}
            />
            {rejectReason.trim().length === 0 && (
              <p className="text-xs text-danger">
                A reason is required to reject.
              </p>
            )}
            <div className="flex gap-3">
              <Button
                variant="danger"
                size="sm"
                onClick={handleReject}
                disabled={isPending || rejectReason.trim().length === 0}
              >
                {reject.isPending ? "Rejecting..." : "Confirm Reject"}
              </Button>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => setMode("idle")}
                disabled={isPending}
              >
                Cancel
              </Button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
