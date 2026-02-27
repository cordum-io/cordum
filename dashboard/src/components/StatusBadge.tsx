import type { HTMLAttributes } from "react";
import { cn } from "../lib/utils";
import { approvalStatusMeta, jobStatusMeta, runStatusMeta, type StatusMeta } from "../lib/status";

const toneStyles: Record<string, string> = {
  success: "bg-success/12 text-success",
  warning: "bg-warning/15 text-warning",
  danger: "bg-danger/12 text-danger",
  info: "bg-accent/12 text-accent",
  muted: "bg-muted/10 text-muted",
  accent: "bg-accent/12 text-accent",
};

const shapeStyles: Record<string, string> = {
  circle: "rounded-full",
  diamond: "rounded rotate-45",
  square: "rounded-lg",
  shield: "rounded-lg",
  triangle: "clip-triangle",
};

function StatusGlyph({ meta }: { meta: StatusMeta }) {
  const Icon = meta.icon;
  return (
    <span
      className={cn(
        "inline-flex h-7 w-7 items-center justify-center",
        toneStyles[meta.tone],
        shapeStyles[meta.shape]
      )}
    >
      <span className={meta.shape === "diamond" ? "-rotate-45" : ""}>
        <Icon className="h-3.5 w-3.5" />
      </span>
    </span>
  );
}

export function StatusBadge({
  meta,
  className,
}: HTMLAttributes<HTMLDivElement> & { meta: StatusMeta }) {
  return (
    <div className={cn("inline-flex items-center gap-2", className)}>
      <StatusGlyph meta={meta} />
      <span className="text-xs font-semibold uppercase tracking-wide text-ink">
        {meta.label}
      </span>
    </div>
  );
}

export function RunStatusBadge({ status }: { status?: string }) {
  return <StatusBadge meta={runStatusMeta(status)} />;
}

export function JobStatusBadge({ state }: { state?: string }) {
  return <StatusBadge meta={jobStatusMeta(state)} />;
}

export function ApprovalStatusBadge({ required }: { required?: boolean }) {
  return <StatusBadge meta={approvalStatusMeta(required)} />;
}
