import { useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { CheckCircle, XCircle, Lightbulb } from "lucide-react";
import { Button } from "../ui/Button";
import type { Approval } from "../../api/types";

// ---------------------------------------------------------------------------
// Relative time formatter
// ---------------------------------------------------------------------------

function formatRelativeTime(iso: string): string {
  const ms = Date.now() - new Date(iso).getTime();
  if (ms < 0) return "just now";
  const secs = Math.floor(ms / 1000);
  if (secs < 60) return "<1m ago";
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(hrs / 24);
  return `${days}d ago`;
}

// ---------------------------------------------------------------------------
// Similarity matching
// ---------------------------------------------------------------------------

function isSimilar(current: Approval, candidate: Approval): boolean {
  if (candidate.id === current.id) return false;
  if (candidate.status === "pending" || candidate.status === "approval_required") return false;
  // Match by topic
  if (current.topic && candidate.topic && current.topic === candidate.topic) return true;
  // Match by overlapping capabilities
  if (
    current.capabilities?.length &&
    candidate.capabilities?.length &&
    current.capabilities.some((c) => candidate.capabilities!.includes(c))
  ) return true;
  return false;
}

// ---------------------------------------------------------------------------
// ApprovalPatterns
// ---------------------------------------------------------------------------

interface ApprovalPatternsProps {
  approval: Approval;
  allApprovals: Approval[];
}

export function ApprovalPatterns({ approval, allApprovals }: ApprovalPatternsProps) {
  const navigate = useNavigate();

  const similar = useMemo(() => {
    return allApprovals
      .filter((a) => isSimilar(approval, a))
      .sort((a, b) => new Date(b.resolvedAt ?? b.requestedAt).getTime() - new Date(a.resolvedAt ?? a.requestedAt).getTime())
      .slice(0, 10);
  }, [approval, allApprovals]);

  // Stats: last 30 days
  const stats = useMemo(() => {
    const cutoff = Date.now() - 30 * 24 * 60 * 60 * 1000;
    const recent = similar.filter(
      (a) => new Date(a.resolvedAt ?? a.requestedAt).getTime() >= cutoff,
    );
    const approved = recent.filter((a) => a.status === "approved" || a.status === "approve").length;
    const rejected = recent.filter((a) => a.status === "rejected" || a.status === "reject").length;
    const total = approved + rejected;
    const rate = total > 0 ? Math.round((approved / total) * 100) : 0;
    return { approved, rejected, total, rate };
  }, [similar]);

  // Auto-approve suggestion: 3+ approvals in last 7 days
  const recentApprovals7d = useMemo(() => {
    const cutoff = Date.now() - 7 * 24 * 60 * 60 * 1000;
    return similar.filter(
      (a) =>
        (a.status === "approved" || a.status === "approve") &&
        new Date(a.resolvedAt ?? a.requestedAt).getTime() >= cutoff,
    );
  }, [similar]);

  const showSuggestion = recentApprovals7d.length >= 3;

  function handleCreateRule() {
    const params = new URLSearchParams();
    if (approval.topic) params.set("topic", approval.topic);
    params.set("decision", "allow");
    params.set("reason", "Auto-approved: pattern consistently approved by operators");
    navigate(`/policies/rules/new?${params.toString()}`);
  }

  // Empty state
  if (similar.length === 0) {
    return <p className="text-xs text-muted">No similar past approvals found.</p>;
  }

  const display = similar.slice(0, 5);

  return (
    <div className="space-y-3">
      {/* Frequency counter */}
      {stats.total > 0 && (
        <p className="text-xs text-muted">
          This pattern: {stats.approved} approved, {stats.rejected} rejected in last 30 days
          {stats.total > 0 && ` (${stats.rate}% approval rate)`}
        </p>
      )}

      {/* History list */}
      <div className="space-y-1.5">
        {display.map((a) => {
          const isApproved = a.status === "approved" || a.status === "approve";
          const ts = a.resolvedAt ?? a.requestedAt;
          const actor = a.actor || a.actorId || "unknown";
          const commentText = a.comment || a.reason || "";

          return (
            <div key={a.id} className="flex items-start gap-2 text-xs">
              {isApproved ? (
                <CheckCircle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-success" />
              ) : (
                <XCircle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-danger" />
              )}
              <div className="min-w-0">
                <span className="text-muted">{formatRelativeTime(ts)}</span>
                {" — "}
                <span className="font-medium text-ink">@{actor}</span>
                {" "}
                <span className="text-muted">{isApproved ? "approved" : "rejected"}</span>
                {commentText && (
                  <>
                    {" — "}
                    <span className="italic text-muted">
                      &ldquo;{commentText.length > 60 ? `${commentText.slice(0, 60)}…` : commentText}&rdquo;
                    </span>
                  </>
                )}
              </div>
            </div>
          );
        })}
      </div>

      {/* Auto-approve suggestion */}
      {showSuggestion && (
        <div className="rounded-lg border border-blue-200 bg-blue-50 p-3 dark:border-blue-800 dark:bg-blue-950">
          <div className="flex items-start gap-2">
            <Lightbulb className="mt-0.5 h-4 w-4 shrink-0 text-blue-600 dark:text-blue-400" />
            <div className="space-y-2">
              <p className="text-xs text-blue-800 dark:text-blue-200">
                <span className="font-semibold">Consider creating an auto-approve rule.</span>{" "}
                You&apos;ve approved {recentApprovals7d.length} similar jobs in the last 7 days.
              </p>
              <Button size="sm" variant="outline" onClick={handleCreateRule}>
                Create Auto-Approve Rule
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
