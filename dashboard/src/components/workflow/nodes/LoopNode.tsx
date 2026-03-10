import { memo } from "react";
import type { NodeProps } from "reactflow";
import { Repeat } from "lucide-react";
import { BaseNode, type OutputHandle } from "./BaseNode";

const LOOP_OUTPUTS: OutputHandle[] = [
  { id: "loop-body", label: "Body", position: "right" },
  { id: "done", label: "Done", position: "bottom" },
];

export const LoopNode = memo(function LoopNode({ data, selected }: NodeProps) {
  const config = (data.config ?? {}) as Record<string, unknown>;
  const input = (data.input ?? config.input ?? {}) as Record<string, unknown>;
  const bodyStep =
    (typeof input.body_step === "string" && input.body_step.trim()
      ? input.body_step
      : typeof input.body === "string" && input.body.trim()
        ? input.body
        : typeof config.bodyStep === "string"
          ? config.bodyStep
          : "");
  const maxIterations =
    (typeof input.max_iterations === "number" ? input.max_iterations : null) ??
    (typeof input.maxIterations === "number" ? input.maxIterations : null) ??
    (typeof config.maxIterations === "number" ? config.maxIterations : null);
  return (
    <BaseNode
      icon={<Repeat className="h-4 w-4 text-[var(--color-warning)]" />}
      label={data.label as string}
      accent="bg-[var(--color-warning)]/10"
      selected={selected}
      outputs={LOOP_OUTPUTS}
    >
      {bodyStep && <span className="block truncate max-w-[140px]">body: {bodyStep}</span>}
      {maxIterations != null && <span className="block truncate max-w-[140px]">max: {maxIterations}</span>}
    </BaseNode>
  );
});
