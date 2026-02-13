import { memo } from "react";
import type { NodeProps } from "reactflow";
import { Code } from "lucide-react";
import { BaseNode } from "./BaseNode";

function truncate(str: string, max: number): string {
  return str.length > max ? str.slice(0, max) + "\u2026" : str;
}

export const TransformNode = memo(function TransformNode({ data, selected }: NodeProps) {
  const config = (data.config ?? {}) as Record<string, unknown>;
  const expression = (data.condition as string) ?? (typeof config.expression === "string" ? config.expression : "");
  return (
    <BaseNode
      icon={<Code className="h-4 w-4 text-indigo-600" />}
      label={data.label as string}
      accent="bg-indigo-50"
      selected={selected}
    >
      {expression && (
        <span className="block truncate max-w-[180px] font-mono">{truncate(expression, 80)}</span>
      )}
    </BaseNode>
  );
});
