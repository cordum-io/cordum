import { memo } from "react";
import type { NodeProps } from "reactflow";
import { Briefcase } from "lucide-react";
import { BaseNode } from "./BaseNode";

export const JobNode = memo(function JobNode({ data, selected }: NodeProps) {
  const config = (data.config ?? {}) as Record<string, unknown>;
  return (
    <BaseNode
      icon={<Briefcase className="h-4 w-4 text-blue-600" />}
      label={data.label as string}
      accent="bg-blue-50"
      selected={selected}
    >
      {typeof config.topic === "string" && config.topic && (
        <span>topic: {config.topic}</span>
      )}
    </BaseNode>
  );
});
