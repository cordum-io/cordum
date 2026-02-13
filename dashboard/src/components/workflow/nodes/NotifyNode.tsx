import { memo } from "react";
import type { NodeProps } from "reactflow";
import { Bell } from "lucide-react";
import { BaseNode } from "./BaseNode";

export const NotifyNode = memo(function NotifyNode({ data, selected }: NodeProps) {
  const config = (data.config ?? {}) as Record<string, unknown>;
  return (
    <BaseNode
      icon={<Bell className="h-4 w-4 text-pink-600" />}
      label={data.label as string}
      accent="bg-pink-50"
      selected={selected}
    >
      {typeof config.channel === "string" && config.channel && (
        <span>channel: {config.channel}</span>
      )}
    </BaseNode>
  );
});
