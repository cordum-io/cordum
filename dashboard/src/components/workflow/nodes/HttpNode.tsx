import { memo } from "react";
import type { NodeProps } from "reactflow";
import { Globe } from "lucide-react";
import { BaseNode } from "./BaseNode";

function truncate(str: string, max: number): string {
  return str.length > max ? str.slice(0, max) + "\u2026" : str;
}

export const HttpNode = memo(function HttpNode({ data, selected }: NodeProps) {
  const config = (data.config ?? {}) as Record<string, unknown>;
  const input = (data.input ?? {}) as Record<string, unknown>;
  const method = (typeof input.method === "string" ? input.method : "") || (typeof config.method === "string" ? config.method : "");
  const url = (typeof input.url === "string" ? input.url : "") || (typeof config.url === "string" ? config.url : "");
  const display = method && url ? `${method} ${truncate(url, 60)}` : method || (url ? truncate(url, 60) : "");
  return (
    <BaseNode
      icon={<Globe className="h-4 w-4 text-primary" />}
      label={data.label as string}
      accent="bg-primary/5"
      selected={selected}
    >
      {display && <span className="block truncate max-w-[180px]">{display}</span>}
    </BaseNode>
  );
});
