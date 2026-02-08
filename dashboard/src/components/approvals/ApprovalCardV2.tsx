import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Eye, Lock, User } from "lucide-react";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { Textarea } from "../ui/Textarea";
import { cn } from "../../lib/utils";
import { computeUrgencyLevel } from "../../api/transform";
import { useEventStore } from "../../state/events";
import { useConfigStore, isSlaBreach, slaRemainingMs } from "../../state/config";
import type { Approval, UrgencyLevel } from "../../api/types";

// ---------------------------------------------------------------------------
// Live wait timer hook
// ---------------------------------------------------------------------------

function formatDuration(ms: number): string {
  const secs = Math.floor(ms / 1_000);
  const mins = Math.floor(secs / 60);
  const hrs = Math.floor(mins / 60);
  if (mins < 1) return "<1m";
  if (hrs < 1) return `${mins}m ${secs % 60}s`;
  if (hrs < 2) return `${hrs}h ${mins % 60}m`;
  return `${hrs}h+`;
}

function useWaitTimer(requestedAt: string) {
  const [now, setNow] = useState(Date.now);
  const slaMs = useConfigStore((s) => s.approvalSlaMs);

  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1_000);
    return () => clearInterval(id);
  }, []);

  const elapsed = Math.max(0, now - new Date(requestedAt).getTime());
  const urgency = computeUrgencyLevel(elapsed);
  const formatted = formatDuration(elapsed);

  const breach = isSlaBreach(elapsed, slaMs);
  const remaining = slaRemainingMs(elapsed, slaMs);
  const slaText = breach
    ? `Breached ${formatDuration(Math.abs(remaining))} ago`
    : `${formatDuration(remaining)} remaining`;

  return { formatted, elapsed, urgency, breach, slaText };
}

// ---------------------------------------------------------------------------
// Urgency styling
// ---------------------------------------------------------------------------

const urgencyBorderColor: Record<UrgencyLevel, string> = {
  fresh: "border-l-emerald-500",
  aging: "border-l-yellow-500",
  critical: "border-l-red-500",
  breach: "border-l-red-600 animate-pulse",
};

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
// Summary fallback — topic + capabilities when humanSummary is absent
// ---------------------------------------------------------------------------

function buildFallbackSummary(approval: Approval): string {
  const caps = approval.capabilities ?? [];
  const topic = approval.topic ?? (approval.jobContext?.topic as string | undefined);
  const parts: string[] = [];
  if (caps.length > 0) parts.push(caps.join(", "));
  if (topic) parts.push(`on ${topic}`);
  if (parts.length > 0) return parts.join(" ");
  return `Job ${approval.jobId.slice(0, 8)} requires approval`;
}

// ---------------------------------------------------------------------------
// High-risk detection
// ---------------------------------------------------------------------------

const HIGH_RISK_TAGS = new Set(["financial", "destructive", "compliance", "production"]);

export function isHighRisk(approval: Approval): boolean {
  return (approval.riskTags ?? []).some((t) => HIGH_RISK_TAGS.has(t));
}

// ---------------------------------------------------------------------------
// ApprovalCardV2
// ---------------------------------------------------------------------------

type CardMode = "idle" | "confirming-approve" | "confirming-reject";

interface ApprovalCardV2Props {
  approval: Approval;
  onApprove: (id: string, comment?: string) => void;
  onReject: (id: string, reason: string) => void;
  onReview: (id: string) => void;
  selected?: boolean;
  onToggleSelect?: (id: string) => void;
}

export function ApprovalCardV2({
  approval,
  onApprove,
  onReject,
  onReview,
  selected,
  onToggleSelect,
}: ApprovalCardV2Props) {
  const { formatted, urgency, breach, slaText } = useWaitTimer(approval.requestedAt);
  const [mode, setMode] = useState<CardMode>("idle");
  const [comment, setComment] = useState("");
  const [rejectReason, setRejectReason] = useState("");

  const wfCtx = approval.workflowContext;

  // Build human-readable summary with fallback
  const summary = approval.humanSummary || buildFallbackSummary(approval);

  const highRisk = isHighRisk(approval);

  // Presence & assignment from store
  const presence = useEventStore((s) => s.approvalPresence.get(approval.id));
  const assignee = useEventStore((s) => s.approvalAssignments.get(approval.id));

  return (
    <Card
      className={cn(
        "border-l-4 transition-shadow hover:shadow-lift",
        urgencyBorderColor[urgency],
        selected && "ring-2 ring-accent/40",
      )}
    >
      <div className="flex gap-3">
        {/* Checkbox column */}
        {onToggleSelect && (
          <div className="flex items-start pt-0.5">
            <div className="relative">
              <input
                type="checkbox"
                checked={selected ?? false}
                onChange={() => onToggleSelect(approval.id)}
                className="h-4 w-4 rounded border-border text-accent focus:ring-accent cursor-pointer"
                title={highRisk ? "High-risk — must be reviewed individually" : undefined}
              />
              {highRisk && (
                <Lock className="absolute -right-1.5 -top-1.5 h-3 w-3 text-warning" />
              )}
            </div>
          </div>
        )}

        {/* Card content */}
        <div className="min-w-0 flex-1 space-y-2">
        {/* ROW 1: Wait timer + urgency badge + SLA */}
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <Badge variant={urgencyBadgeVariant[urgency]}>
              {urgencyLabels[urgency]}
            </Badge>
            {breach && (
              <span className="rounded bg-red-600 px-1.5 py-0.5 text-[10px] font-bold uppercase text-white">
                SLA Breach
              </span>
            )}
          </div>
          <div className="text-right">
            <span className="font-mono text-xs text-muted">
              Waiting {formatted}
            </span>
            <p className={cn("text-[10px]", breach ? "font-medium text-danger" : "text-muted")}>
              SLA: {slaText}
            </p>
          </div>
        </div>

        {/* Presence / Assignment indicators */}
        {(presence || assignee) && (
          <div className="flex flex-wrap items-center gap-2">
            {presence && (
              <span className="flex items-center gap-1 rounded-full bg-accent/10 px-2 py-0.5 text-[10px] font-medium text-accent">
                <Eye className="h-3 w-3" />
                Being reviewed by @{presence.actor}
              </span>
            )}
            {assignee && (
              <span className="flex items-center gap-1 rounded-full bg-ink/5 px-2 py-0.5 text-[10px] font-medium text-ink">
                <User className="h-3 w-3" />
                Assigned to @{assignee}
              </span>
            )}
          </div>
        )}

        {/* ROW 2: Human-readable summary */}
        <p className="text-lg font-semibold text-ink leading-tight">
          {summary}
        </p>

        {/* ROW 3: Context lines */}
        <div className="space-y-0.5 text-xs text-muted">
          {approval.policyRule && (
            <p>
              Rule:{" "}
              <span className="font-medium text-ink">{approval.policyRule}</span>
              {approval.reason && <> — {approval.reason}</>}
            </p>
          )}
          {wfCtx && (
            <p>
              Workflow:{" "}
              <Link
                to={`/workflows/${wfCtx.workflowId}`}
                className="text-accent hover:underline"
              >
                {wfCtx.workflowId.slice(0, 12)}
              </Link>
              {wfCtx.stepIndex != null && wfCtx.totalSteps != null && (
                <> &rarr; step {wfCtx.stepIndex + 1}/{wfCtx.totalSteps}</>
              )}
              {wfCtx.stepName && <> ({wfCtx.stepName})</>}
            </p>
          )}
        </div>

        {/* ROW 4: Risk tags + capabilities */}
        {((approval.riskTags?.length ?? 0) > 0 || (approval.capabilities?.length ?? 0) > 0) && (
          <div className="flex flex-wrap gap-1.5">
            {approval.riskTags?.map((t) => (
              <Badge key={t} variant="warning">{t}</Badge>
            ))}
            {approval.capabilities?.map((c) => (
              <Badge key={c} variant="info">{c}</Badge>
            ))}
          </div>
        )}

        {/* ROW 5: Action buttons / inline confirm */}
        {mode === "idle" && (
          <div className="flex flex-wrap items-center justify-end gap-2 pt-1">
            <Button
              size="sm"
              className="bg-emerald-600 hover:bg-emerald-700 text-white"
              onClick={() => setMode("confirming-approve")}
            >
              Approve
            </Button>
            <Button
              variant="danger"
              size="sm"
              onClick={() => setMode("confirming-reject")}
            >
              Reject
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => onReview(approval.id)}
            >
              Review
            </Button>
          </div>
        )}

        {mode === "confirming-approve" && (
          <div className="space-y-2 rounded-xl border border-border bg-surface2/30 p-3">
            <Textarea
              rows={2}
              placeholder="Add a comment (optional)..."
              value={comment}
              onChange={(e) => setComment(e.target.value)}
            />
            <div className="flex items-center justify-end gap-2">
              <Button
                variant="ghost"
                size="sm"
                onClick={() => { setMode("idle"); setComment(""); }}
              >
                Cancel
              </Button>
              <Button
                size="sm"
                className="bg-emerald-600 hover:bg-emerald-700 text-white"
                onClick={() => {
                  onApprove(approval.id, comment || undefined);
                  setMode("idle");
                  setComment("");
                }}
              >
                Confirm Approve
              </Button>
            </div>
          </div>
        )}

        {mode === "confirming-reject" && (
          <div className="space-y-2 rounded-xl border border-border bg-surface2/30 p-3">
            <Textarea
              rows={2}
              placeholder="Reason for rejection (required)"
              value={rejectReason}
              onChange={(e) => setRejectReason(e.target.value)}
              className={cn(!rejectReason.trim() && "border-danger")}
            />
            {!rejectReason.trim() && (
              <p className="text-xs text-danger">A reason is required to reject.</p>
            )}
            <div className="flex items-center justify-end gap-2">
              <Button
                variant="ghost"
                size="sm"
                onClick={() => { setMode("idle"); setRejectReason(""); }}
              >
                Cancel
              </Button>
              <Button
                variant="danger"
                size="sm"
                disabled={!rejectReason.trim()}
                onClick={() => {
                  onReject(approval.id, rejectReason.trim());
                  setMode("idle");
                  setRejectReason("");
                }}
              >
                Confirm Reject
              </Button>
            </div>
          </div>
        )}
      </div>
      </div>
    </Card>
  );
}
