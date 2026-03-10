import { useNavigate } from "react-router-dom";
import { CheckCircle2 } from "lucide-react";
import { useActiveRuns } from "../../hooks/useWorkflows";
import { useRunStream } from "../../hooks/useRunStream";
import { cn } from "../../lib/utils";
import type { WorkflowRun, WorkflowStep } from "../../api/types";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function elapsed(iso?: string): string {
  if (!iso) return "\u2014";
  const diff = Date.now() - new Date(iso).getTime();
  const secs = Math.floor(diff / 1_000);
  if (secs < 60) return `${secs}s`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ${mins % 60}m`;
  return `${Math.floor(hrs / 24)}d`;
}

const STEP_BAR_COLORS: Record<string, string> = {
  succeeded: "bg-[var(--color-success)]",
  completed: "bg-[var(--color-success)]",
  running: "bg-[var(--color-info)]",
  in_progress: "bg-[var(--color-info)]",
  failed: "bg-destructive",
  timed_out: "bg-destructive",
  waiting: "bg-[var(--color-warning)]",
  blocked: "bg-[var(--color-warning)]",
  pending: "bg-muted",
  queued: "bg-muted",
  cancelled: "bg-muted-foreground",
};

function statusDotClass(run: WorkflowRun): string {
  const steps = run.steps ?? [];
  if (steps.some((s) => s.status === "waiting"))
    return "bg-[var(--color-warning)] animate-pulse";
  if (steps.some((s) => s.status === "failed" || s.status === "timed_out"))
    return "bg-destructive";
  if (run.status === "running")
    return "bg-[var(--color-info)]";
  return "bg-muted-foreground";
}

// ---------------------------------------------------------------------------
// MiniProgressBar
// ---------------------------------------------------------------------------

function MiniProgressBar({ steps }: { steps: WorkflowStep[] }) {
  if (steps.length === 0) return null;
  const total = steps.length;
  const done = steps.filter(
    (s) => s.status === "succeeded",
  ).length;

  return (
    <div className="flex items-center gap-1.5">
      <div className="flex h-1 w-full overflow-hidden rounded-full">
        {steps.map((s, i) => (
          <div
            key={s.id ?? i}
            className={cn("h-full", STEP_BAR_COLORS[s.status ?? ""] ?? "bg-muted")}
            style={{ width: `${100 / total}%` }}
          />
        ))}
      </div>
      <span className="flex-shrink-0 text-[10px] text-muted-foreground">
        {done}/{total}
      </span>
    </div>
  );
}

// ---------------------------------------------------------------------------
// RunCard
// ---------------------------------------------------------------------------

function RunCard({ run, onClick }: { run: WorkflowRun; onClick: () => void }) {
  const workflowName =
    (run as unknown as Record<string, unknown>).workflowName ??
    run.workflowId?.slice(0, 8) ??
    "Workflow";

  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "snap-start flex-shrink-0 w-56 surface-card rounded-xl px-4 py-3",
        "text-left transition-all duration-200",
        "hover:shadow-lg hover:-translate-y-0.5",
        "animate-in fade-in duration-300",
      )}
    >
      <div className="mb-2 flex items-center justify-between">
        <span className="max-w-[160px] truncate text-sm font-semibold text-ink">
          {String(workflowName)}
        </span>
        <span className={cn("h-2 w-2 flex-shrink-0 rounded-full", statusDotClass(run))} />
      </div>

      <MiniProgressBar steps={run.steps ?? []} />

      <div className="mt-2 text-xs text-muted-foreground">
        {elapsed(run.startedAt ?? run.createdAt)}
      </div>
    </button>
  );
}

// ---------------------------------------------------------------------------
// Skeleton loader
// ---------------------------------------------------------------------------

function SkeletonCard() {
  return (
    <div className="snap-start flex-shrink-0 w-56 surface-card rounded-xl px-4 py-3 animate-pulse">
      <div className="mb-2 flex items-center justify-between">
        <div className="h-4 w-28 rounded bg-muted" />
        <div className="h-2 w-2 rounded-full bg-muted" />
      </div>
      <div className="h-1 w-full rounded-full bg-muted" />
      <div className="mt-2 h-3 w-12 rounded bg-muted" />
    </div>
  );
}

// ---------------------------------------------------------------------------
// ActiveRunsStrip
// ---------------------------------------------------------------------------

export function ActiveRunsStrip({ className }: { className?: string }) {
  const { data: runs, isLoading } = useActiveRuns();

  // Live updates for all active runs
  useRunStream(null);

  if (isLoading) {
    return (
      <div className={cn("flex gap-3 overflow-x-auto pb-2", className)}>
        {Array.from({ length: 4 }, (_, i) => (
          <SkeletonCard key={i} />
        ))}
      </div>
    );
  }

  if (!runs || runs.length === 0) {
    return (
      <div className={cn("flex items-center gap-2 rounded-xl bg-surface2/50 px-4 py-2.5", className)}>
        <CheckCircle2 className="h-4 w-4 text-success" />
        <span className="text-sm text-muted-foreground">All clear — no active runs</span>
      </div>
    );
  }

  return (
    <ActiveRunsStripInner runs={runs} className={className} />
  );
}

function ActiveRunsStripInner({
  runs,
  className,
}: {
  runs: WorkflowRun[];
  className?: string;
}) {
  const navigate = useNavigate();

  return (
    <div
      className={cn(
        "flex gap-3 overflow-x-auto snap-x snap-mandatory pb-2",
        className,
      )}
    >
      {runs.map((run) => (
        <RunCard
          key={run.id}
          run={run}
          onClick={() => navigate(`/workflows/${run.workflowId}/runs/${run.id}`)}
        />
      ))}
    </div>
  );
}
