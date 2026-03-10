import { Link } from "react-router-dom";
import { ExternalLink } from "lucide-react";
import { cn } from "../../lib/utils";
import type { ApprovalWorkflowContext } from "../../api/types";

// ---------------------------------------------------------------------------
// Step indicator dot
// ---------------------------------------------------------------------------

function StepDot({
  state,
  label,
}: {
  state: "completed" | "current" | "pending";
  label?: string;
}) {
  return (
    <div className="flex flex-col items-center gap-1">
      <div
        className={cn(
          "h-2.5 w-2.5 rounded-full",
          state === "completed" && "bg-[var(--color-success)]",
          state === "current" && "bg-[var(--color-warning)] animate-pulse",
          state === "pending" && "bg-muted",
        )}
      />
      {label && (
        <span className="text-[9px] text-muted-foreground max-w-[60px] text-center truncate">
          {label}
        </span>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Step connector line
// ---------------------------------------------------------------------------

function StepLine({ completed }: { completed: boolean }) {
  return (
    <div
      className={cn(
        "h-0.5 flex-1 min-w-3",
        completed ? "bg-[var(--color-success)]" : "bg-muted",
      )}
    />
  );
}

// ---------------------------------------------------------------------------
// WorkflowContext
// ---------------------------------------------------------------------------

interface WorkflowContextProps {
  workflowContext?: ApprovalWorkflowContext;
}

export function WorkflowContext({ workflowContext }: WorkflowContextProps) {
  if (!workflowContext) {
    return <p className="text-xs text-muted-foreground">This job is not part of a workflow.</p>;
  }

  const { workflowId, runId, stepIndex, stepName, totalSteps } = workflowContext;
  const hasStepInfo = stepIndex != null && totalSteps != null;

  return (
    <div className="space-y-3">
      {/* Workflow + Run links */}
      <div className="space-y-1 text-xs">
        <p className="flex items-center gap-1.5">
          <span className="text-muted-foreground">Workflow:</span>
          <Link
            to={`/workflows/${workflowId}`}
            className="font-medium text-accent hover:underline inline-flex items-center gap-1"
          >
            {workflowId.slice(0, 16)}
            <ExternalLink className="h-3 w-3" />
          </Link>
        </p>
        <p>
          <span className="text-muted-foreground">Run: </span>
          <Link
            to={`/workflows/${workflowId}?run=${runId}`}
            className="font-mono text-accent hover:underline"
          >
            {runId.slice(0, 16)}
          </Link>
        </p>
      </div>

      {/* Step position */}
      {hasStepInfo && (
        <>
          <p className="text-xs">
            <span className="text-muted-foreground">Step: </span>
            <span className="font-medium text-ink">
              {stepIndex + 1} of {totalSteps}
              {stepName && ` — ${stepName}`}
            </span>
          </p>

          {/* Visual step indicator */}
          <div className="flex items-center gap-0.5 py-1">
            {Array.from({ length: totalSteps }, (_, i) => {
              const state =
                i < stepIndex ? "completed" : i === stepIndex ? "current" : "pending";
              return (
                <div key={i} className="contents">
                  {i > 0 && <StepLine completed={i <= stepIndex} />}
                  <StepDot
                    state={state}
                    label={i === stepIndex ? stepName ?? `Step ${i + 1}` : undefined}
                  />
                </div>
              );
            })}
          </div>

          {/* What happens next */}
          <div className="space-y-1 text-xs text-muted-foreground">
            {stepIndex + 1 < totalSteps && (
              <p>
                If approved, workflow continues to step {stepIndex + 2} of {totalSteps}.
              </p>
            )}
            {stepIndex + 1 >= totalSteps && (
              <p>
                If approved, this is the final step — workflow will complete.
              </p>
            )}
            <p>If rejected, the workflow run will be terminated.</p>
          </div>
        </>
      )}
    </div>
  );
}
