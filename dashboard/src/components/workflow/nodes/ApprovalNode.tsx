import { memo } from "react";
import type { NodeProps } from "reactflow";
import { ShieldCheck } from "lucide-react";
import { BaseNode } from "./BaseNode";

export const ApprovalNode = memo(function ApprovalNode({ data, selected }: NodeProps) {
  const config = (data.config ?? {}) as Record<string, unknown>;
  const timeout = (typeof data.timeout_sec === "number" ? `${data.timeout_sec}s` : "") || (typeof config.timeout === "string" ? config.timeout : "");
  return (
    <BaseNode
      icon={<ShieldCheck className="h-4 w-4 text-[var(--color-warning)]" />}
      label={data.label as string}
      accent="bg-[var(--color-warning)]/5"
      selected={selected}
    >
      {timeout && (
        <span>timeout: {timeout}</span>
      )}
    </BaseNode>
  );
});
