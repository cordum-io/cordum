import { memo } from "react";
import type { NodeProps } from "reactflow";
import { ShieldCheck } from "lucide-react";
import { BaseNode } from "./BaseNode";

export const ApprovalNode = memo(function ApprovalNode({ data, selected }: NodeProps) {
  const config = (data.config ?? {}) as Record<string, unknown>;
  return (
    <BaseNode
      icon={<ShieldCheck className="h-4 w-4 text-amber-600" />}
      label={data.label as string}
      accent="bg-amber-50"
      selected={selected}
    >
      {typeof config.timeout === "string" && config.timeout && (
        <span>timeout: {config.timeout}</span>
      )}
    </BaseNode>
  );
});
