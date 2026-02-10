import { memo } from "react";
import type { NodeProps } from "reactflow";
import { Wrench } from "lucide-react";
import { BaseNode } from "./BaseNode";

function truncate(str: string, max: number): string {
  return str.length > max ? str.slice(0, max) + "\u2026" : str;
}

export const ToolCallNode = memo(function ToolCallNode({ data, selected }: NodeProps) {
  const config = (data.config ?? {}) as Record<string, unknown>;
  const meta = (data.meta ?? {}) as Record<string, unknown>;
  const capability = (typeof meta.capability === "string" ? meta.capability : "") || (typeof config.capability === "string" ? config.capability : "");
  return (
    <BaseNode
      icon={<Wrench className="h-4 w-4 text-amber-600" />}
      label={data.label as string}
      accent="bg-amber-50"
      selected={selected}
    >
      {capability && (
        <span className="block truncate max-w-[160px]">capability: {truncate(capability, 40)}</span>
      )}
    </BaseNode>
  );
});
