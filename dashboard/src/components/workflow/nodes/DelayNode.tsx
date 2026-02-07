import { memo } from "react";
import type { NodeProps } from "reactflow";
import { Clock } from "lucide-react";
import { BaseNode } from "./BaseNode";

export const DelayNode = memo(function DelayNode({ data, selected }: NodeProps) {
  const config = (data.config ?? {}) as Record<string, unknown>;
  return (
    <BaseNode
      icon={<Clock className="h-4 w-4 text-purple-600" />}
      label={data.label as string}
      accent="bg-purple-50"
      selected={selected}
    >
      {typeof config.duration === "string" && config.duration && (
        <span>duration: {config.duration}</span>
      )}
    </BaseNode>
  );
});
