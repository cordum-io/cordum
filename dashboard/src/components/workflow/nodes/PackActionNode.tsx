import { memo } from "react";
import type { NodeProps } from "reactflow";
import { Package } from "lucide-react";
import { BaseNode } from "./BaseNode";

function truncate(str: string, max: number): string {
  return str.length > max ? str.slice(0, max) + "\u2026" : str;
}

export const PackActionNode = memo(function PackActionNode({ data, selected }: NodeProps) {
  const config = (data.config ?? {}) as Record<string, unknown>;
  const meta = (data.meta ?? {}) as Record<string, unknown>;
  const packId = (typeof meta.pack_id === "string" ? meta.pack_id : "") || (typeof config.packId === "string" ? config.packId : "");
  const action = typeof config.action === "string" ? config.action : "";
  const display = packId && action ? `${packId}: ${action}` : packId || action;
  return (
    <BaseNode
      icon={<Package className="h-4 w-4 text-primary" />}
      label={data.label as string}
      accent="bg-primary/5"
      selected={selected}
    >
      {display && (
        <span className="block truncate max-w-[160px]">{truncate(display, 40)}</span>
      )}
    </BaseNode>
  );
});
