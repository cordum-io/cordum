import { memo } from "react";
import type { NodeProps } from "reactflow";
import { Layers } from "lucide-react";
import { BaseNode } from "./BaseNode";

export const ParallelNode = memo(function ParallelNode({ data, selected }: NodeProps) {
  const config = (data.config ?? {}) as Record<string, unknown>;
  const input = (data.input ?? {}) as Record<string, unknown>;
  const strategy =
    (typeof input.strategy === "string" && input.strategy.trim()
      ? input.strategy
      : typeof config.completionStrategy === "string" && config.completionStrategy.trim()
        ? config.completionStrategy
        : "all") ?? "all";
  const steps = Array.isArray(input.steps)
    ? (input.steps as unknown[]).map((entry) => String(entry).trim()).filter(Boolean)
    : Array.isArray(config.parallelSteps)
      ? (config.parallelSteps as unknown[]).map((entry) => String(entry).trim()).filter(Boolean)
      : [];
  const required =
    (typeof input.required === "number" ? input.required : null) ??
    (typeof config.requiredCount === "number" ? config.requiredCount : null);
  const maxParallel =
    (typeof data.max_parallel === "number" ? data.max_parallel : null) ??
    (typeof config.parallelism === "number" ? config.parallelism : null);

  return (
    <BaseNode
      icon={<Layers className="h-4 w-4 text-sky-600" />}
      label={data.label as string}
      accent="bg-sky-50"
      selected={selected}
    >
      <span>{steps.length} child step{steps.length === 1 ? "" : "s"}</span>
      <span>strategy: {strategy}</span>
      {strategy === "n_of_m" && required !== null && <span>required: {required}</span>}
      {maxParallel !== null && <span>max_parallel: {maxParallel}</span>}
    </BaseNode>
  );
});
