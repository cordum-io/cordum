import { memo } from "react";
import type { NodeProps } from "reactflow";
import { Repeat } from "lucide-react";
import { BaseNode, type OutputHandle } from "./BaseNode";

const LOOP_OUTPUTS: OutputHandle[] = [
  { id: "loop-body", label: "Body", position: "right" },
  { id: "done", label: "Done", position: "bottom" },
];

export const LoopNode = memo(function LoopNode({ data, selected }: NodeProps) {
  const config = (data.config ?? {}) as Record<string, unknown>;
  const forEach = (data.for_each as string) ?? (typeof config.forEach === "string" ? config.forEach : "");
  return (
    <BaseNode
      icon={<Repeat className="h-4 w-4 text-orange-600" />}
      label={data.label as string}
      accent="bg-orange-50"
      selected={selected}
      outputs={LOOP_OUTPUTS}
    >
      {forEach && <span className="block truncate max-w-[140px]">each: {forEach}</span>}
    </BaseNode>
  );
});
