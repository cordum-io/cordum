import { Handle, Position, type NodeProps } from "reactflow";
import { ArrowRight } from "lucide-react";
import type { OutputNodeData } from "../types";

export default function OutputNode({ data, selected }: NodeProps<OutputNodeData>) {
  return (
    <div
      className={[
        "min-w-[200px] rounded-2xl border bg-secondary-background/60 px-3 py-2 text-xs shadow-sm backdrop-blur",
        selected ? "border-emerald-400/60" : "border-primary-border",
      ].join(" ")}
    >
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <div className="rounded-lg border border-emerald-400/20 bg-emerald-500/10 p-1 text-emerald-200">
            <ArrowRight size={14} />
          </div>
          <div>
            <div className="text-[10px] uppercase tracking-wider text-tertiary-text">Output</div>
            <div className="truncate font-semibold text-primary-text">{data.name || "Output"}</div>
          </div>
        </div>
        <div className="rounded-md border border-primary-border bg-tertiary-background px-2 py-0.5 text-[11px] text-tertiary-text">
          {data.outputs?.length ?? 0} fields
        </div>
      </div>
      <Handle type="target" position={Position.Left} className="!h-2.5 !w-2.5 !border-0 !bg-emerald-300" />
    </div>
  );
}
