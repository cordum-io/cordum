import type { ReactNode } from "react";
import { Handle, Position } from "reactflow";
import { cn } from "../../../lib/utils";

export interface OutputHandle {
  id: string;
  label: string;
  position: "left" | "right" | "bottom";
}

const POSITION_MAP: Record<OutputHandle["position"], Position> = {
  left: Position.Left,
  right: Position.Right,
  bottom: Position.Bottom,
};

export interface BaseNodeProps {
  icon: ReactNode;
  label: string;
  accent: string;
  selected?: boolean;
  children?: ReactNode;
  /** When provided, renders multiple named source handles instead of the single bottom handle. */
  outputs?: OutputHandle[];
  /** When true, hides the top input handle (e.g. for error-trigger nodes). */
  hideInput?: boolean;
}

export function BaseNode({ icon, label, accent, selected, children, outputs, hideInput }: BaseNodeProps) {
  return (
    <div
      className={cn(
        "relative min-w-[140px] rounded-xl border bg-white px-3 py-2.5 shadow-sm transition-all",
        selected ? "border-accent ring-2 ring-accent/30" : "border-border",
      )}
    >
      {!hideInput && <Handle type="target" position={Position.Top} className="!bg-accent !w-2.5 !h-2.5" />}
      <div className="flex items-center gap-2">
        <div className={cn("flex h-7 w-7 items-center justify-center rounded-lg", accent)}>
          {icon}
        </div>
        <span className="text-xs font-semibold text-ink truncate">{label}</span>
      </div>
      {children && <div className="mt-2 border-t border-border/50 pt-2 text-[10px] text-muted">{children}</div>}
      {outputs ? (
        outputs.map((out) => (
          <div key={out.id} className="relative">
            <Handle
              type="source"
              id={out.id}
              position={POSITION_MAP[out.position]}
              className="!bg-accent !w-2.5 !h-2.5"
            />
            <span
              className={cn(
                "absolute text-[10px] text-muted pointer-events-none whitespace-nowrap",
                out.position === "left" && "left-0 -translate-x-full pr-1 top-1/2 -translate-y-1/2",
                out.position === "right" && "right-0 translate-x-full pl-1 top-1/2 -translate-y-1/2",
                out.position === "bottom" && "bottom-0 translate-y-full left-1/2 -translate-x-1/2 pt-0.5",
              )}
            >
              {out.label}
            </span>
          </div>
        ))
      ) : (
        <Handle type="source" position={Position.Bottom} className="!bg-accent !w-2.5 !h-2.5" />
      )}
    </div>
  );
}
