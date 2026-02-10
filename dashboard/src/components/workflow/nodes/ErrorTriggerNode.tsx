import { memo } from "react";
import type { NodeProps } from "reactflow";
import { AlertTriangle } from "lucide-react";
import { BaseNode } from "./BaseNode";

export const ErrorTriggerNode = memo(function ErrorTriggerNode({ data, selected }: NodeProps) {
  const config = (data.config ?? {}) as Record<string, unknown>;
  const catchFrom = (data.on_error as string) || (typeof config.catchFrom === "string" ? config.catchFrom : "");
  return (
    <BaseNode
      icon={<AlertTriangle className="h-4 w-4 text-red-600" />}
      label={data.label as string}
      accent="bg-red-50"
      selected={selected}
      hideInput
    >
      {catchFrom && <span className="block truncate max-w-[140px]">catches: {catchFrom}</span>}
    </BaseNode>
  );
});
