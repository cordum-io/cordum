import { memo } from "react";
import type { NodeProps } from "reactflow";
import { Split } from "lucide-react";
import { BaseNode } from "./BaseNode";

export const FanOutNode = memo(function FanOutNode({ data, selected }: NodeProps) {
  const config = (data.config ?? {}) as Record<string, unknown>;
  const parallelism = (typeof data.max_parallel === "number" ? data.max_parallel : null) ?? (typeof config.parallelism === "number" ? config.parallelism : null);
  const forEach = (typeof data.for_each === "string" && data.for_each.trim() ? data.for_each : null) ?? (typeof config.forEach === "string" && config.forEach.trim() ? config.forEach : null);
  return (
    <BaseNode
      icon={<Split className="h-4 w-4 text-primary" />}
      label={data.label as string}
      accent="bg-primary/15"
      selected={selected}
    >
      {forEach && <span>for_each: {forEach}</span>}
      {parallelism !== null && <span>parallelism: {parallelism}</span>}
    </BaseNode>
  );
});
