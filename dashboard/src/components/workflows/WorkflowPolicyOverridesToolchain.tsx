import { Wrench } from "lucide-react";
import type { PolicyConstraints } from "@/api/types";

type Toolchain = NonNullable<PolicyConstraints["toolchain"]>;

export interface WorkflowPolicyOverridesToolchainProps {
  toolchain: Toolchain | null;
  readOnly: boolean;
  onChange: (next: Toolchain) => void;
}

function formatList(items: string[] | undefined): string {
  if (!items || items.length === 0) return "—";
  return items.join(", ");
}

function parseList(value: string): string[] {
  return value
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
}

const TOOLCHAIN_FIELDS: Array<{ key: keyof Toolchain; label: string }> = [
  { key: "allowed_tools", label: "Allowed tools" },
  { key: "allowed_commands", label: "Allowed commands" },
];

export function WorkflowPolicyOverridesToolchain({ toolchain, readOnly, onChange }: WorkflowPolicyOverridesToolchainProps) {
  const current = toolchain ?? {};
  const hasValues =
    (current.allowed_tools && current.allowed_tools.length > 0) ||
    (current.allowed_commands && current.allowed_commands.length > 0);

  return (
    <div className="rounded-lg border border-border bg-surface-0 p-3">
      <div className="flex items-center gap-2 mb-2">
        <Wrench className="w-3.5 h-3.5 text-muted-foreground" />
        <span className="text-xs font-semibold text-foreground">Toolchain Constraints</span>
        {!hasValues && (
          <span className="text-[10px] font-mono text-muted-foreground ml-auto">global defaults</span>
        )}
      </div>
      <div className="space-y-2">
        {TOOLCHAIN_FIELDS.map(({ key, label }) => (
          <div key={key} className="flex flex-col gap-0.5">
            <label className="text-[10px] text-muted-foreground" htmlFor={`toolchain-${key}`}>
              {label} <span className="font-mono">(comma-separated)</span>
            </label>
            {readOnly ? (
              <span className="text-xs font-mono text-foreground break-all">
                {formatList(current[key])}
              </span>
            ) : (
              <input
                id={`toolchain-${key}`}
                type="text"
                className="h-7 rounded-md border border-border bg-surface-2 px-2 text-xs font-mono text-foreground w-full"
                value={current[key]?.join(", ") ?? ""}
                placeholder="inherit"
                onChange={(e) => {
                  const list = parseList(e.target.value);
                  onChange({ ...current, [key]: list.length > 0 ? list : undefined });
                }}
              />
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
