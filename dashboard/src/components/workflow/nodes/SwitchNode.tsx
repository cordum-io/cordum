import { memo, useMemo } from "react";
import type { NodeProps } from "reactflow";
import { GitMerge } from "lucide-react";
import { BaseNode, type OutputHandle } from "./BaseNode";

export const SwitchNode = memo(function SwitchNode({ data, selected }: NodeProps) {
  const config = (data.config ?? {}) as Record<string, unknown>;
  const cases = Array.isArray(config.cases) ? (config.cases as Array<{ value: string; label?: string }>) : [];

  // Build dynamic outputs: one per case + default
  const outputs: OutputHandle[] = useMemo(() => {
    const handles: OutputHandle[] = cases.map((c, i) => ({
      id: `case-${i}`,
      label: c.label ?? c.value,
      position: i % 2 === 0 ? "right" : "left",
    }));
    handles.push({ id: "default", label: "Default", position: "bottom" });
    return handles;
  }, [cases]);

  return (
    <BaseNode
      icon={<GitMerge className="h-4 w-4 text-teal-600" />}
      label={data.label as string}
      accent="bg-teal-50"
      selected={selected}
      outputs={outputs}
    >
      {cases.length > 0 && (
        <span className="block truncate max-w-[140px]">
          {cases.length} case{cases.length !== 1 ? "s" : ""} + default
        </span>
      )}
    </BaseNode>
  );
});
