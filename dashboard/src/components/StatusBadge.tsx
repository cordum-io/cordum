import type { HTMLAttributes } from "react";
import { cn } from "../lib/utils";
import { approvalStatusMeta, jobDecisionMeta, jobStatusMeta, runStatusMeta, type StatusMeta } from "../lib/status";

const toneStyles: Record<string, string> = {
  success: "border-status-success-border bg-status-success-bg text-success",
  warning: "border-status-warning-border bg-status-warning-bg text-warning",
  danger: "border-status-danger-border bg-status-danger-bg text-danger",
  info: "border-status-info-border bg-status-info-bg text-info",
  muted: "border-status-muted-border bg-status-muted-bg text-muted-foreground",
};

const shapeStyles: Record<string, string> = {
  circle: "rounded-full",
  diamond: "rounded-md rotate-45",
  square: "rounded-lg",
  shield: "rounded-[18px]",
  triangle: "clip-triangle",
};

function StatusGlyph({ meta, compact }: { meta: StatusMeta; compact?: boolean }) {
  const Icon = meta.icon;
  return (
    <span
      className={cn(
        "inline-flex items-center justify-center",
        compact ? "h-5 w-5" : "h-8 w-8",
        toneStyles[meta.tone],
        shapeStyles[meta.shape]
      )}
    >
      <span className={meta.shape === "diamond" ? "-rotate-45" : ""}>
        <Icon className={compact ? "h-3 w-3" : "h-4 w-4"} />
      </span>
    </span>
  );
}

export function StatusBadge({
  meta,
  compact,
  className,
}: HTMLAttributes<HTMLDivElement> & { meta: StatusMeta; compact?: boolean }) {
  return (
    <div className={cn("inline-flex items-center gap-2", className)}>
      <StatusGlyph meta={meta} compact={compact} />
      {!compact && (
        <span className="text-xs font-semibold uppercase tracking-wide text-ink">
          {meta.label}
        </span>
      )}
    </div>
  );
}

export function RunStatusBadge({ status }: { status?: string }) {
  return <StatusBadge meta={runStatusMeta(status)} />;
}

export function JobStatusBadge({ state }: { state?: string }) {
  return <StatusBadge meta={jobStatusMeta(state)} compact />;
}

export function JobDecisionBadge({ decision }: { decision?: string }) {
  return <StatusBadge meta={jobDecisionMeta(decision)} compact />;
}

export function JobDecisionBadge({ decision }: { decision?: string }) {
  return <StatusBadge meta={jobDecisionMeta(decision)} compact />;
}
