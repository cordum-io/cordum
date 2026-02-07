import { memo } from "react";
import type { NodeProps } from "reactflow";
import { GitBranch } from "lucide-react";
import { BaseNode } from "./BaseNode";

export const ConditionNode = memo(function ConditionNode({ data, selected }: NodeProps) {
  const config = (data.config ?? {}) as Record<string, unknown>;
  return (
    <BaseNode
      icon={<GitBranch className="h-4 w-4 text-teal-600" />}
      label={data.label as string}
      accent="bg-teal-50"
      selected={selected}
    >
      {typeof config.expression === "string" && config.expression && (
        <span className="truncate block max-w-[120px]">{config.expression}</span>
      )}
    </BaseNode>
  );
});
