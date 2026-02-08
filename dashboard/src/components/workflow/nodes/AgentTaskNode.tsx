import { memo } from "react";
import type { NodeProps } from "reactflow";
import { MessageSquare } from "lucide-react";
import { BaseNode } from "./BaseNode";

export const AgentTaskNode = memo(function AgentTaskNode({ data, selected }: NodeProps) {
  const config = (data.config ?? {}) as Record<string, unknown>;
  const topic = (data.topic as string) ?? (typeof config.topic === "string" ? config.topic : "");
  const input = (data.input ?? {}) as Record<string, unknown>;
  const prompt = typeof input.prompt === "string" ? input.prompt : (typeof config.prompt === "string" ? config.prompt : "");
  return (
    <BaseNode
      icon={<MessageSquare className="h-4 w-4 text-blue-600" />}
      label={data.label as string}
      accent="bg-blue-50"
      selected={selected}
    >
      {topic && (
        <span className="block truncate max-w-[160px]">topic: {topic}</span>
      )}
      {prompt && (
        <span className="block truncate max-w-[160px]">
          {prompt.length > 30 ? prompt.slice(0, 30) + "\u2026" : prompt}
        </span>
      )}
    </BaseNode>
  );
});
