import type { HTMLAttributes } from "react";
import { cn } from "../lib/utils";
import { approvalStatusMeta, jobStatusMeta, runStatusMeta, type StatusMeta } from "../lib/status";

const toneStyles: Record<string, string> = {
  success: "bg-[color:rgba(31,122,87,0.16)] text-success",
  warning: "bg-[color:rgba(197,138,28,0.18)] text-warning",
  danger: "bg-[color:rgba(184,58,58,0.18)] text-danger",
  info: "bg-[color:rgba(15,127,122,0.14)] text-accent",
  muted: "bg-[color:rgba(90,106,112,0.15)] text-muted",
  accent: "bg-[color:rgba(15,127,122,0.18)] text-accent",
};

const shapeStyles: Record<string, string> = {
  circle: "rounded-full",
  diamond: "rounded-md rotate-45",
  square: "rounded-xl",
  shield: "rounded-[18px]",
  triangle: "clip-triangle",
};

function StatusGlyph({ meta }: { meta: StatusMeta }) {
  const Icon = meta.icon;
  return (
    <span
      className={cn(
        "inline-flex h-8 w-8 items-center justify-center",
        toneStyles[meta.tone],
        shapeStyles[meta.shape]
      )}
    >
      <span className={meta.shape === "diamond" ? "-rotate-45" : ""}>
        <Icon className="h-4 w-4" />
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
