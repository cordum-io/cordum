import { Check, Circle } from "lucide-react";
import { cn } from "../../lib/utils";
import type { JobStatus } from "../../api/types";

// ---------------------------------------------------------------------------
// Lifecycle steps in order
// ---------------------------------------------------------------------------

const STEPS: { key: string; label: string }[] = [
  { key: "pending", label: "Submitted" },
  { key: "safety", label: "Safety Check" },
  { key: "dispatched", label: "Dispatched" },
  { key: "running", label: "Running" },
  { key: "terminal", label: "Complete" },
];

const STATUS_INDEX: Record<string, number> = {
  pending: 0,
  scheduled: 0,
  approval_required: 1,
  dispatched: 2,
  running: 3,
  succeeded: 4,
  failed: 4,
  cancelled: 4,
  denied: 4,
  timeout: 4,
  output_quarantined: 4,
};

const TERMINAL_LABELS: Record<string, string> = {
  succeeded: "Succeeded",
  failed: "Failed",
  cancelled: "Cancelled",
  denied: "Denied",
  timeout: "Timed Out",
  output_quarantined: "Quarantined",
};

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function JobStateMachine({ status }: { status?: JobStatus }) {
  const current = status ?? "pending";
  const currentIdx = STATUS_INDEX[current] ?? 0;
  const isFailed = current === "failed" || current === "cancelled" || current === "denied" || current === "timeout";

  return (
    <div className="flex items-center gap-0">
      {STEPS.map((step, i) => {
        const isCompleted = i < currentIdx;
        const isCurrent = i === currentIdx;
        const isTerminalFailed = isCurrent && isFailed;
        const label = i === 4 && TERMINAL_LABELS[current] ? TERMINAL_LABELS[current] : step.label;

        return (
          <div key={step.key} className="flex items-center">
            {/* Node */}
            <div className="flex flex-col items-center gap-1.5">
              <div
                className={cn(
                  "flex h-8 w-8 items-center justify-center rounded-full border-2 transition-all",
                  isCompleted && "border-success bg-success/10",
                  isCurrent && !isTerminalFailed && "border-accent bg-accent/10 ring-2 ring-accent/20",
                  isTerminalFailed && "border-danger bg-danger/10 ring-2 ring-danger/20",
                  !isCompleted && !isCurrent && "border-border bg-surface2",
                )}
              >
                {isCompleted ? (
                  <Check className="h-4 w-4 text-success" />
                ) : isCurrent ? (
                  <Circle
                    className={cn(
                      "h-3 w-3",
                      isTerminalFailed ? "text-danger fill-danger" : "text-accent fill-accent",
                    )}
                  />
                ) : (
                  <Circle className="h-3 w-3 text-muted" />
                )}
              </div>
              <span
                className={cn(
                  "text-[10px] font-medium whitespace-nowrap",
                  isCompleted && "text-success",
                  isCurrent && !isTerminalFailed && "text-accent font-semibold",
                  isTerminalFailed && "text-danger font-semibold",
                  !isCompleted && !isCurrent && "text-muted",
                )}
              >
                {label}
              </span>
            </div>

            {/* Connector line */}
            {i < STEPS.length - 1 && (
              <div
                className={cn(
                  "mx-1.5 h-0.5 w-8 rounded-full transition-all",
                  i < currentIdx ? "bg-success" : "bg-border",
                )}
              />
            )}
          </div>
        );
      })}
    </div>
  );
}
