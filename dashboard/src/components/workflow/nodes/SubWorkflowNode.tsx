import { memo } from "react";
import type { NodeProps } from "reactflow";
import { Workflow } from "lucide-react";
import { BaseNode } from "./BaseNode";

export const SubWorkflowNode = memo(function SubWorkflowNode({ data, selected }: NodeProps) {
  const config = (data.config ?? {}) as Record<string, unknown>;
  const workflowName = typeof config.workflowName === "string" ? config.workflowName : "";
  const workflowId = typeof config.workflowId === "string" ? config.workflowId : "";
  const display = workflowName || (workflowId ? workflowId.slice(0, 12) : "");
  return (
    <BaseNode
      icon={<Workflow className="h-4 w-4 text-cyan-600" />}
      label={data.label as string}
      accent="bg-cyan-50"
      selected={selected}
    >
      {display && <span className="block truncate max-w-[140px]">{display}</span>}
    </BaseNode>
  );
});
