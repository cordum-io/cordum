import type { ReactNode } from "react";
import { Handle, Position } from "reactflow";
import { cn } from "../../../lib/utils";

export interface BaseNodeProps {
  icon: ReactNode;
  label: string;
  accent: string;
  selected?: boolean;
  children?: ReactNode;
}

export function BaseNode({ icon, label, accent, selected, children }: BaseNodeProps) {
  return (
    <div
      className={cn(
        "min-w-[140px] rounded-xl border bg-white px-3 py-2.5 shadow-sm transition-all",
        selected ? "border-accent ring-2 ring-accent/30" : "border-border",
      )}
    >
      <Handle type="target" position={Position.Top} className="!bg-accent !w-2.5 !h-2.5" />
      <div className="flex items-center gap-2">
        <div className={cn("flex h-7 w-7 items-center justify-center rounded-lg", accent)}>
          {icon}
        </div>
        <span className="text-xs font-semibold text-ink truncate">{label}</span>
      </div>
      {children && <div className="mt-2 border-t border-border/50 pt-2 text-[10px] text-muted">{children}</div>}
      <Handle type="source" position={Position.Bottom} className="!bg-accent !w-2.5 !h-2.5" />
    </div>
  );
}
