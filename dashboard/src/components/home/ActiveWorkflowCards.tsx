import { Link } from "react-router-dom";
import { Card } from "../ui/Card";
import { ProgressBar } from "../ProgressBar";
import { RunStatusBadge } from "../StatusBadge";
import { useRecentRuns } from "../../hooks/useStatus";
import type { WorkflowRun } from "../../api/types";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function relativeTime(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  const secs = Math.floor(diff / 1_000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  return `${hrs}h ago`;
}

const ACTIVE_STATUSES = new Set(["running", "pending", "waiting"]);

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function ActiveWorkflowCards() {
  const { data, isLoading } = useRecentRuns(20);
  const runs = (data?.items ?? []).filter((r: WorkflowRun) => ACTIVE_STATUSES.has(r.status));

  if (isLoading) {
    return (
      <Card>
        <div className="space-y-3">
          <div className="h-4 w-1/3 rounded bg-surface2 animate-pulse" />
          <div className="h-20 rounded bg-surface2 animate-pulse" />
        </div>
      </Card>
    );
  }

  return (
    <Card>
      <div className="space-y-3">
        <h3 className="text-sm font-semibold text-ink">Active Workflows</h3>

        {runs.length === 0 ? (
          <p className="py-6 text-center text-xs text-muted">
            No active workflow runs.
          </p>
        ) : (
          <div className="space-y-3">
            {runs.map((run: WorkflowRun) => {
              const completedSteps = run.steps.filter(
                (s) => s.status === "succeeded",
              ).length;
              const totalSteps = run.steps.length;
              const pct = totalSteps > 0 ? Math.round((completedSteps / totalSteps) * 100) : 0;

              return (
                <Link
                  key={run.id}
                  to={`/workflows/${run.workflowId}/runs/${run.id}`}
                  className="block rounded-xl border border-border p-3 transition-colors hover:bg-surface2/40"
                >
                  <div className="flex items-center justify-between mb-2">
                    <div className="flex items-center gap-2">
                      <span className="font-mono text-xs text-ink">
                        {run.id.slice(0, 8)}
                      </span>
                      <RunStatusBadge status={run.status} />
                    </div>
                    <span className="text-[10px] text-muted">
                      {run.startedAt ? relativeTime(run.startedAt) : "pending"}
                    </span>
                  </div>
                  <div className="flex items-center gap-2">
                    <ProgressBar value={pct} className="flex-1" />
                    <span className="text-[10px] text-muted shrink-0">
                      {completedSteps}/{totalSteps} steps
                    </span>
                  </div>
                </Link>
              );
            })}
          </div>
        )}
      </div>
    </Card>
  );
}
