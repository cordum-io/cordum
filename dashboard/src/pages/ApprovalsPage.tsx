import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { Clock, History } from "lucide-react";
import { useApprovals } from "../hooks/useApprovals";
import { Badge } from "../components/ui/Badge";
import { Card } from "../components/ui/Card";
import { ApprovalDetailModal } from "../components/approvals/ApprovalDetailModal";
import { ApprovalHistory } from "../components/approvals/ApprovalHistory";
import { cn } from "../lib/utils";
import type { Approval } from "../api/types";

type ApprovalsTab = "pending" | "history";

// ---------------------------------------------------------------------------
// Wait time helpers
// ---------------------------------------------------------------------------

function waitTimeMs(requestedAt: string): number {
  return Date.now() - new Date(requestedAt).getTime();
}

function formatWait(ms: number): string {
  const secs = Math.floor(ms / 1_000);
  if (secs < 60) return `${secs}s`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m`;
  const hrs = Math.floor(mins / 60);
  const rem = mins % 60;
  return `${hrs}h ${rem}m`;
}

type Urgency = "default" | "warning" | "danger";

function urgencyLevel(ms: number): Urgency {
  if (ms > 15 * 60_000) return "danger";
  if (ms > 5 * 60_000) return "warning";
  return "default";
}

const urgencyLabel: Record<Urgency, string> = {
  default: "Normal",
  warning: "Aging",
  danger: "Critical",
};

// ---------------------------------------------------------------------------
// Urgency badge
// ---------------------------------------------------------------------------

function UrgencyBadge({ urgency }: { urgency: Urgency }) {
  return (
    <Badge
      variant={urgency}
      className={cn(urgency === "danger" && "animate-pulse")}
    >
      {urgencyLabel[urgency]}
    </Badge>
  );
}

// ---------------------------------------------------------------------------
// Approval card
// ---------------------------------------------------------------------------

function ApprovalCard({
  approval,
  onClick,
}: {
  approval: Approval;
  onClick: () => void;
}) {
  const ms = waitTimeMs(approval.requestedAt);
  const urgency = urgencyLevel(ms);

  return (
    <Card
      className={cn(
        "cursor-pointer transition-shadow hover:shadow-lift",
        urgency === "danger" && "border-l-4 border-l-danger",
        urgency === "warning" && "border-l-4 border-l-warning",
      )}
      onClick={onClick}
    >
      <div className="space-y-3">
        {/* Header: urgency + wait time */}
        <div className="flex items-center justify-between">
          <UrgencyBadge urgency={urgency} />
          <span className="text-xs font-mono text-muted">
            Waiting {formatWait(ms)}
          </span>
        </div>

        {/* What the agent wants to do */}
        <div>
          <p className="text-sm font-semibold text-ink">
            Job{" "}
            <Link
              to={`/jobs/${approval.jobId}`}
              className="font-mono text-accent hover:underline"
              onClick={(e) => e.stopPropagation()}
            >
              {approval.jobId.slice(0, 8)}
            </Link>
            {" "}requires approval
          </p>
          {approval.reason && (
            <p className="mt-1 text-xs text-muted">{approval.reason}</p>
          )}
        </div>

        {/* Policy rule */}
        {approval.policyRule && (
          <div className="text-xs">
            <span className="text-muted">Policy rule: </span>
            <span className="font-medium text-ink">{approval.policyRule}</span>
          </div>
        )}

        {/* Workflow context */}
        {typeof approval.jobContext?.workflowRunId === "string" && (
          <div className="text-xs">
            <span className="text-muted">Workflow: </span>
            <Link
              to={`/workflows/${approval.jobContext.workflowId as string}`}
              className="text-accent hover:underline"
            >
              {(approval.jobContext.workflowName as string) ??
                (approval.jobContext.workflowId as string)}
            </Link>
          </div>
        )}

        {/* Job context summary */}
        {approval.jobContext && (
          <div className="flex flex-wrap gap-2">
            {typeof approval.jobContext.type === "string" && (
              <Badge variant="default">
                {approval.jobContext.type}
              </Badge>
            )}
            {Array.isArray(approval.jobContext.capabilities) &&
              (approval.jobContext.capabilities as string[]).map((c) => (
                <Badge key={c} variant="info">
                  {c}
                </Badge>
              ))}
          </div>
        )}
      </div>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// ApprovalsPage
// ---------------------------------------------------------------------------

export default function ApprovalsPage() {
  const { data, isLoading, isError } = useApprovals("pending");
  const [selected, setSelected] = useState<Approval | null>(null);
  const [activeTab, setActiveTab] = useState<ApprovalsTab>("pending");

  const approvals = data?.items ?? [];

  // Sort by requested time ascending (longest waiting first)
  const sorted = useMemo(
    () =>
      [...approvals].sort(
        (a, b) =>
          new Date(a.requestedAt).getTime() -
          new Date(b.requestedAt).getTime(),
      ),
    [approvals],
  );

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <h1 className="font-display text-2xl font-bold text-ink">
            Approvals
          </h1>
          {sorted.length > 0 && (
            <Badge variant="warning">{sorted.length} pending</Badge>
          )}
        </div>
      </div>

      {/* Tabs */}
      <div className="flex gap-1 rounded-full border border-border p-1 w-fit">
        <button
          type="button"
          className={cn(
            "flex items-center gap-2 rounded-full px-5 py-2 text-xs font-semibold uppercase tracking-widest transition",
            activeTab === "pending"
              ? "bg-accent/15 text-accent"
              : "text-muted hover:text-ink",
          )}
          onClick={() => setActiveTab("pending")}
        >
          <Clock className="h-3.5 w-3.5" />
          Pending
        </button>
        <button
          type="button"
          className={cn(
            "flex items-center gap-2 rounded-full px-5 py-2 text-xs font-semibold uppercase tracking-widest transition",
            activeTab === "history"
              ? "bg-accent/15 text-accent"
              : "text-muted hover:text-ink",
          )}
          onClick={() => setActiveTab("history")}
        >
          <History className="h-3.5 w-3.5" />
          History
        </button>
      </div>

      {/* Pending tab */}
      {activeTab === "pending" && (
        <>
          {isLoading && (
            <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
              {Array.from({ length: 4 }, (_, i) => (
                <Card key={i} className="animate-pulse">
                  <div className="space-y-3">
                    <div className="h-5 w-1/3 rounded bg-surface2" />
                    <div className="h-4 w-2/3 rounded bg-surface2" />
                    <div className="h-4 w-1/2 rounded bg-surface2" />
                  </div>
                </Card>
              ))}
            </div>
          )}

          {!isLoading && isError && (
            <Card>
              <p className="py-8 text-center text-muted">
                Failed to load approvals. Please try again.
              </p>
            </Card>
          )}

          {!isLoading && !isError && sorted.length === 0 && (
            <Card>
              <div className="py-12 text-center">
                <div className="mx-auto mb-3 flex h-12 w-12 items-center justify-center rounded-full bg-[color:rgba(31,122,87,0.12)]">
                  <span className="text-xl text-success">&#10003;</span>
                </div>
                <p className="text-sm font-semibold text-ink">
                  No pending approvals
                </p>
                <p className="mt-1 text-xs text-muted">All clear.</p>
              </div>
            </Card>
          )}

          {!isLoading && !isError && sorted.length > 0 && (
            <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
              {sorted.map((approval: Approval) => (
                <ApprovalCard
                  key={approval.id}
                  approval={approval}
                  onClick={() => setSelected(approval)}
                />
              ))}
            </div>
          )}

          {selected && (
            <ApprovalDetailModal
              approval={selected}
              onClose={() => setSelected(null)}
            />
          )}
        </>
      )}

      {/* History tab */}
      {activeTab === "history" && <ApprovalHistory />}
    </div>
  );
}
