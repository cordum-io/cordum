import { memo } from "react";
import type { NodeProps } from "reactflow";
import { GitBranch } from "lucide-react";
import { BaseNode, type OutputHandle } from "./BaseNode";

const CONDITION_OUTPUTS: OutputHandle[] = [
  { id: "true", label: "\u2713 True", position: "right" },
  { id: "false", label: "\u2717 False", position: "left" },
];

export const ConditionNode = memo(function ConditionNode({ data, selected }: NodeProps) {
  const config = (data.config ?? {}) as Record<string, unknown>;
  const expression = (data.condition as string) ?? (typeof config.expression === "string" ? config.expression : "");
  return (
    <BaseNode
      icon={<GitBranch className="h-4 w-4 text-[var(--color-info)]" />}
      label={data.label as string}
      accent="bg-[var(--color-info)]/15"
      selected={selected}
      outputs={CONDITION_OUTPUTS}
    >
      {expression && (
        <span className="truncate block max-w-[120px]">{expression}</span>
      )}
    </BaseNode>
  );
});
