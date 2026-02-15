import { useEffect, useState, useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { User, X } from "lucide-react";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { Textarea } from "../ui/Textarea";
import { PayloadViewer } from "./PayloadViewer";
import { SafetyExplanation } from "./SafetyExplanation";
import { WorkflowContext } from "./WorkflowContext";
import { ApprovalPatterns } from "./ApprovalPatterns";
import { cn } from "../../lib/utils";
import { ApiError } from "../../api/client";
import { computeUrgencyLevel } from "../../api/transform";
import { useEventStore } from "../../state/events";
import { useConfigStore } from "../../state/config";
import { useDialogA11y } from "../../hooks/useDialogA11y";
import type { Approval, UrgencyLevel } from "../../api/types";

// ---------------------------------------------------------------------------
// Live wait timer (shared logic with ApprovalCardV2)
// ---------------------------------------------------------------------------

function useWaitTimer(requestedAt: string) {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1_000);
    return () => clearInterval(id);
  }, []);
  const elapsed = Math.max(0, now - new Date(requestedAt).getTime());
  const urgency = computeUrgencyLevel(elapsed);
  const secs = Math.floor(elapsed / 1_000);
  const mins = Math.floor(secs / 60);
  const hrs = Math.floor(mins / 60);
  let formatted: string;
  if (mins < 1) formatted = "<1m";
  else if (hrs < 1) formatted = `${mins}m ${secs % 60}s`;
  else if (hrs < 2) formatted = `${hrs}h ${mins % 60}m`;
  else formatted = `${hrs}h+`;
  return { formatted, urgency };
}

// ---------------------------------------------------------------------------
// Urgency styling
// ---------------------------------------------------------------------------

const urgencyBadgeVariant: Record<UrgencyLevel, "success" | "warning" | "danger"> = {
  fresh: "success",
  aging: "warning",
  critical: "danger",
  breach: "danger",
};

const urgencyLabels: Record<UrgencyLevel, string> = {
  fresh: "Fresh",
  aging: "Aging",
  critical: "Critical",
  breach: "SLA Breach",
};

// ---------------------------------------------------------------------------
// Section wrapper
// ---------------------------------------------------------------------------

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="space-y-2">
      <h3 className="text-sm font-semibold text-ink">{title}</h3>
      <hr className="border-border" />
      {children}
    </div>
  );
}

// ---------------------------------------------------------------------------
// ApprovalDetailPanel
// ---------------------------------------------------------------------------

type PanelMode = "idle" | "confirming-approve" | "confirming-reject" | "confirming-conditions" | "conditions-diff";

interface ApprovalDetailPanelProps {
  approval: Approval;
  allApprovals?: Approval[];
  onClose: () => void;
  onApprove: (id: string, comment?: string) => Promise<void>;
  onReject: (id: string, reason: string) => Promise<void>;
}

export function ApprovalDetailPanel({
  approval,
  allApprovals,
  onClose,
  onApprove,
  onReject,
}: ApprovalDetailPanelProps) {
  const navigate = useNavigate();
  const { formatted, urgency } = useWaitTimer(approval.requestedAt);
  const [mode, setMode] = useState<PanelMode>("idle");
  const [comment, setComment] = useState("");
  const [rejectReason, setRejectReason] = useState("");
  const [feedback, setFeedback] = useState<string | null>(null);

  const [actionPending, setActionPending] = useState(false);

  // Conditions form state
  const [conditionText, setConditionText] = useState("");
  const [modifiedPayload, setModifiedPayload] = useState("");
  const [payloadError, setPayloadError] = useState<string | null>(null);
  const [conditionScope, setConditionScope] = useState<"this-run" | "pattern">("this-run");

  const dialogRef = useDialogA11y(onClose);

  // Presence tracking — mark as reviewing on mount, clear on unmount
  const currentUser = useConfigStore((s) => s.principalId || "operator");
  const setReviewing = useEventStore((s) => s.setReviewing);
  const clearReviewing = useEventStore((s) => s.clearReviewing);
  const assignApproval = useEventStore((s) => s.assignApproval);
  const unassignApproval = useEventStore((s) => s.unassignApproval);
  const assignee = useEventStore((s) => s.approvalAssignments.get(approval.id));

  useEffect(() => {
    setReviewing(approval.id, currentUser);
    return () => clearReviewing(approval.id);
  }, [approval.id, currentUser, setReviewing, clearReviewing]);

  const originalPayloadJson = useMemo(
    () => approval.jobContext ? JSON.stringify(approval.jobContext, null, 2) : "{}",
    [approval.jobContext],
  );

  function resetConditionsForm() {
    setConditionText("");
    setModifiedPayload("");
    setPayloadError(null);
    setConditionScope("this-run");
  }

  function validatePayload(value: string): boolean {
    if (!value.trim()) {
      setPayloadError(null);
      return true;
    }
    try {
      JSON.parse(value);
      setPayloadError(null);
      return true;
    } catch {
      setPayloadError("Invalid JSON");
      return false;
    }
  }

  async function handleConditionsSubmit() {
    const trimmed = modifiedPayload.trim();
    let parsedPayload: Record<string, unknown> | undefined;
    if (trimmed && trimmed !== originalPayloadJson) {
      try {
        parsedPayload = JSON.parse(trimmed);
      } catch {
        setPayloadError("Invalid JSON");
        return;
      }
    }
    const note = conditionText.trim() + (parsedPayload ? " [payload modified]" : "");
    setActionPending(true);
    try {
      await onApprove(approval.id, note);
      setFeedback("Approved with conditions");
      setMode("idle");
      resetConditionsForm();
      if (conditionScope === "pattern") {
        const params = new URLSearchParams();
        if (approval.topic) params.set("topic", approval.topic);
        params.set("decision", "allow");
        params.set("reason", conditionText.trim());
        setTimeout(() => navigate(`/policies?${params.toString()}`), 800);
      } else {
        setTimeout(onClose, 800);
      }
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        setFeedback("This item is no longer pending — the queue has been refreshed.");
      } else {
        setFeedback(`Action failed: ${err instanceof Error ? err.message : "Unknown error"}`);
      }
      setMode("idle");
      resetConditionsForm();
      setTimeout(onClose, 2000);
    } finally {
      setActionPending(false);
    }
  }

  async function handleApprove() {
    setActionPending(true);
    try {
      await onApprove(approval.id, comment || undefined);
      setFeedback("Approved successfully");
      setMode("idle");
      setComment("");
      setTimeout(onClose, 800);
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        setFeedback("This item is no longer pending — the queue has been refreshed.");
      } else {
        setFeedback(`Action failed: ${err instanceof Error ? err.message : "Unknown error"}`);
      }
      setMode("idle");
      setComment("");
      setTimeout(onClose, 2000);
    } finally {
      setActionPending(false);
    }
  }

  async function handleReject() {
    setActionPending(true);
    try {
      await onReject(approval.id, rejectReason.trim());
      setFeedback("Rejected");
      setMode("idle");
      setRejectReason("");
      setTimeout(onClose, 800);
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        setFeedback("This item is no longer pending — the queue has been refreshed.");
      } else {
        setFeedback(`Action failed: ${err instanceof Error ? err.message : "Unknown error"}`);
      }
      setMode("idle");
      setRejectReason("");
      setTimeout(onClose, 2000);
    } finally {
      setActionPending(false);
    }
  }

  // Clear feedback after 2s
  useEffect(() => {
    if (!feedback) return;
    const t = setTimeout(() => setFeedback(null), 2_000);
    return () => clearTimeout(t);
  }, [feedback]);

  return (
    <>
      {/* Backdrop */}
      <div
        className="fixed inset-0 z-40 bg-black/30"
        onClick={onClose}
      />

      {/* Panel */}
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-label="Approval detail"
        className={cn(
          "fixed right-0 top-0 z-50 flex h-full flex-col",
          "w-full md:w-[65%] border-l border-border bg-surface shadow-2xl",
          "translate-x-0 transition-transform duration-200 ease-out",
        )}
      >
        {/* Section 1: Sticky decision header */}
        <div className="sticky top-0 z-10 space-y-3 border-b border-border bg-surface/95 backdrop-blur px-6 py-4">
          <div className="flex items-start justify-between">
            <div className="space-y-1 min-w-0 flex-1">
              <p className="text-xl font-semibold text-ink leading-tight truncate">
                {approval.humanSummary || `Job ${approval.jobId.slice(0, 8)} requires approval`}
              </p>
              <div className="flex items-center gap-2 text-xs text-muted">
                <Badge variant={urgencyBadgeVariant[urgency]}>
                  {urgencyLabels[urgency]}
                </Badge>
                <span className="font-mono">Waiting {formatted}</span>
                {approval.policyRule && (
                  <>
                    <span>&middot;</span>
                    <span>Rule: {approval.policyRule}</span>
                  </>
                )}
              </div>
            </div>
            <button
              type="button"
              onClick={onClose}
              className="ml-3 rounded p-1.5 text-muted hover:bg-surface2 hover:text-ink transition-colors"
              aria-label="Close panel"
            >
              <X className="h-5 w-5" />
            </button>
          </div>

          {/* Feedback message */}
          {feedback && (
            <p className={cn("text-sm font-medium", feedback.startsWith("Action failed") || feedback.startsWith("This item is no longer") ? "text-warning" : "text-success")}>{feedback}</p>
          )}

          {/* Action buttons */}
          {mode === "idle" && (
            <div className="flex flex-wrap items-center gap-2">
              <Button
                size="sm"
                className="bg-emerald-600 hover:bg-emerald-700 text-white"
                disabled={actionPending}
                onClick={() => setMode("confirming-approve")}
              >
                Approve
              </Button>
              <Button
                variant="outline"
                size="sm"
                disabled={actionPending}
                onClick={() => setMode("confirming-conditions")}
              >
                Approve with Conditions
              </Button>
              <Button
                variant="danger"
                size="sm"
                disabled={actionPending}
                onClick={() => setMode("confirming-reject")}
              >
                Reject
              </Button>
              <div className="ml-auto">
                {assignee === currentUser ? (
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => unassignApproval(approval.id)}
                  >
                    <User className="h-3.5 w-3.5 mr-1" />
                    Unassign
                  </Button>
                ) : (
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => assignApproval(approval.id, currentUser)}
                  >
                    <User className="h-3.5 w-3.5 mr-1" />
                    {assignee ? `Reassign to me` : `Assign to me`}
                  </Button>
                )}
              </div>
            </div>
          )}

          {mode === "confirming-approve" && (
            <div className="space-y-2">
              <Textarea
                rows={2}
                placeholder="Add a comment (optional)..."
                value={comment}
                onChange={(e) => setComment(e.target.value)}
              />
              <div className="flex items-center gap-2">
                <Button
                  size="sm"
                  className="bg-emerald-600 hover:bg-emerald-700 text-white"
                  disabled={actionPending}
                  onClick={handleApprove}
                >
                  {actionPending ? "Approving\u2026" : "Confirm Approve"}
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={actionPending}
                  onClick={() => { setMode("idle"); setComment(""); }}
                >
                  Cancel
                </Button>
              </div>
            </div>
          )}

          {mode === "confirming-reject" && (
            <div className="space-y-2">
              <Textarea
                rows={2}
                placeholder="Reason for rejection (required)"
                value={rejectReason}
                onChange={(e) => setRejectReason(e.target.value)}
                className={cn(!rejectReason.trim() && "border-danger")}
              />
              <div className="flex items-center gap-2">
                <Button
                  variant="danger"
                  size="sm"
                  disabled={!rejectReason.trim() || actionPending}
                  onClick={handleReject}
                >
                  {actionPending ? "Rejecting\u2026" : "Confirm Reject"}
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={actionPending}
                  onClick={() => { setMode("idle"); setRejectReason(""); }}
                >
                  Cancel
                </Button>
              </div>
            </div>
          )}

          {mode === "confirming-conditions" && (
            <div className="space-y-3">
              {/* Condition text */}
              <div className="space-y-1">
                <label className="text-xs font-medium text-muted">Condition (required)</label>
                <Textarea
                  rows={3}
                  placeholder="Describe conditions, e.g. Approved for staging only"
                  value={conditionText}
                  onChange={(e) => setConditionText(e.target.value)}
                  className={cn(!conditionText.trim() && conditionText !== "" && "border-danger")}
                />
              </div>

              {/* Modified payload */}
              <div className="space-y-1">
                <label className="text-xs font-medium text-muted">Modify payload (optional)</label>
                <textarea
                  rows={6}
                  className={cn(
                    "w-full rounded-lg border bg-surface2 px-3 py-2 font-mono text-xs text-ink",
                    "focus:outline-none focus:ring-2 focus:ring-accent/50",
                    payloadError ? "border-danger" : "border-border",
                  )}
                  value={modifiedPayload || originalPayloadJson}
                  onChange={(e) => {
                    setModifiedPayload(e.target.value);
                    setPayloadError(null);
                  }}
                  onBlur={(e) => validatePayload(e.target.value)}
                />
                {payloadError && (
                  <p className="text-xs text-danger">{payloadError}</p>
                )}
              </div>

              {/* Scope radio */}
              <div className="space-y-1">
                <label className="text-xs font-medium text-muted">Scope</label>
                <div className="flex items-center gap-4">
                  <label className="flex items-center gap-1.5 text-xs text-ink cursor-pointer">
                    <input
                      type="radio"
                      name="condition-scope"
                      className="accent-accent"
                      checked={conditionScope === "this-run"}
                      onChange={() => setConditionScope("this-run")}
                    />
                    This run only
                  </label>
                  <label className="flex items-center gap-1.5 text-xs text-ink cursor-pointer">
                    <input
                      type="radio"
                      name="condition-scope"
                      className="accent-accent"
                      checked={conditionScope === "pattern"}
                      onChange={() => setConditionScope("pattern")}
                    />
                    This pattern going forward
                  </label>
                </div>
              </div>

              {/* Actions */}
              <div className="flex items-center gap-2">
                {modifiedPayload.trim() && modifiedPayload.trim() !== originalPayloadJson ? (
                  <Button
                    size="sm"
                    className="bg-emerald-600 hover:bg-emerald-700 text-white"
                    disabled={!conditionText.trim() || !!payloadError}
                    onClick={() => {
                      if (validatePayload(modifiedPayload)) setMode("conditions-diff");
                    }}
                  >
                    Review Changes
                  </Button>
                ) : (
                  <Button
                    size="sm"
                    className="bg-emerald-600 hover:bg-emerald-700 text-white"
                    disabled={!conditionText.trim() || !!payloadError}
                    onClick={handleConditionsSubmit}
                  >
                    Confirm Conditional Approval
                  </Button>
                )}
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => { setMode("idle"); resetConditionsForm(); }}
                >
                  Cancel
                </Button>
              </div>
            </div>
          )}

          {mode === "conditions-diff" && (
            <div className="space-y-3">
              <p className="text-xs font-medium text-ink">Review payload changes before confirming:</p>
              <div className="grid grid-cols-2 gap-2 text-xs">
                <div className="space-y-1">
                  <span className="font-medium text-muted">Original</span>
                  <pre className="max-h-40 overflow-auto rounded-lg border border-border bg-surface2 p-2 font-mono text-[11px] text-muted whitespace-pre-wrap">
                    {originalPayloadJson}
                  </pre>
                </div>
                <div className="space-y-1">
                  <span className="font-medium text-success">Modified</span>
                  <pre className="max-h-40 overflow-auto rounded-lg border border-success/30 bg-success/5 p-2 font-mono text-[11px] text-ink whitespace-pre-wrap">
                    {modifiedPayload}
                  </pre>
                </div>
              </div>
              <p className="text-xs text-muted">
                Condition: <span className="font-medium text-ink">{conditionText}</span>
              </p>
              <div className="flex items-center gap-2">
                <Button
                  size="sm"
                  className="bg-emerald-600 hover:bg-emerald-700 text-white"
                  onClick={handleConditionsSubmit}
                >
                  Confirm Conditional Approval
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => setMode("confirming-conditions")}
                >
                  Back
                </Button>
              </div>
            </div>
          )}
        </div>

        {/* Scrollable body */}
        <div className="flex-1 overflow-y-auto px-6 py-5 space-y-6">
          {/* Section 2: Job Context / Payload */}
          <Section title="What the Agent Wants to Do">
            <PayloadViewer
              jobContext={approval.jobContext}
              topic={approval.topic}
              capabilities={approval.capabilities}
            />
          </Section>

          {/* Section 3: Safety Explanation */}
          <Section title="Safety Kernel Decision">
            <SafetyExplanation
              policyRule={approval.policyRule}
              reason={approval.reason}
              riskTags={approval.riskTags}
              capabilities={approval.capabilities}
              safetyDecision={approval.safetyDecision}
            />
          </Section>

          {/* Section 4: Workflow Context */}
          <Section title="Workflow Context">
            <WorkflowContext workflowContext={approval.workflowContext} />
          </Section>

          {/* Section 5: Similar Past Approvals */}
          <Section title="Similar Past Approvals">
            <ApprovalPatterns approval={approval} allApprovals={allApprovals ?? []} />
          </Section>
        </div>
      </div>
    </>
  );
}
