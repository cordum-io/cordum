import { Handle, Position, type NodeProps } from "reactflow";
import { ArrowLeft } from "lucide-react";
import type { InputNodeData } from "../types";

export default function InputNode({ data, selected }: NodeProps<InputNodeData>) {
  return (
    <div
      className={[
        "min-w-[200px] rounded-2xl border bg-secondary-background/60 px-3 py-2 text-xs shadow-sm backdrop-blur",
        selected ? "border-sky-400/60" : "border-primary-border",
      ].join(" ")}
    >
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <div className="rounded-lg border border-sky-400/20 bg-sky-500/10 p-1 text-sky-200">
            <ArrowLeft size={14} />
          </div>
          <div>
            <div className="text-[10px] uppercase tracking-wider text-tertiary-text">Input</div>
            <div className="truncate font-semibold text-primary-text">{data.name || "Input"}</div>
          </div>
        </div>
      </div>
      <div className="mt-2 flex flex-wrap gap-1 text-[11px] text-tertiary-text">
        <span className={data.includeFilePath ? "rounded-md border border-primary-border bg-tertiary-background px-1.5 py-0.5" : "hidden"}>
          filePath
        </span>
        <span className={data.includeInstruction ? "rounded-md border border-primary-border bg-tertiary-background px-1.5 py-0.5" : "hidden"}>
          instruction
        </span>
        {!data.includeFilePath && !data.includeInstruction ? <span>prompt</span> : null}
      </div>
      <Handle type="source" position={Position.Right} className="!h-2.5 !w-2.5 !border-0 !bg-sky-300" />
    </div>
  );
}
