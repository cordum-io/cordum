import { Lock } from "lucide-react";
import type { PolicyConstraints } from "@/api/types";

type Sandbox = NonNullable<PolicyConstraints["sandbox"]>;

export interface WorkflowPolicyOverridesSandboxProps {
  sandbox: Sandbox | null;
  readOnly: boolean;
  onChange: (next: Sandbox) => void;
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

const LIST_FIELDS: Array<{ key: keyof Sandbox; label: string }> = [
  { key: "network_allowlist", label: "Network allowlist" },
  { key: "fs_read_only", label: "Read-only paths" },
  { key: "fs_read_write", label: "Read-write paths" },
];

export function WorkflowPolicyOverridesSandbox({ sandbox, readOnly, onChange }: WorkflowPolicyOverridesSandboxProps) {
  const current = sandbox ?? {};
  const hasValues =
    current.isolated !== undefined ||
    (current.network_allowlist && current.network_allowlist.length > 0) ||
    (current.fs_read_only && current.fs_read_only.length > 0) ||
    (current.fs_read_write && current.fs_read_write.length > 0);

  return (
    <div className="rounded-lg border border-border bg-surface-0 p-3">
      <div className="flex items-center gap-2 mb-2">
        <Lock className="w-3.5 h-3.5 text-muted-foreground" />
        <span className="text-xs font-semibold text-foreground">Sandbox Constraints</span>
        {!hasValues && (
          <span className="text-[10px] font-mono text-muted-foreground ml-auto">global defaults</span>
        )}
      </div>

      <div className="space-y-2">
        <div className="flex items-center gap-2">
          <label className="text-[10px] text-muted-foreground" htmlFor="sandbox-isolated">
            Isolated execution
          </label>
          {readOnly ? (
            <span className="text-xs font-mono text-foreground">
              {current.isolated === true ? "yes" : current.isolated === false ? "no" : "—"}
            </span>
          ) : (
            <select
              id="sandbox-isolated"
              className="h-7 rounded-md border border-border bg-surface-2 px-2 text-xs text-foreground"
              value={current.isolated === true ? "true" : current.isolated === false ? "false" : ""}
              onChange={(e) => {
                const val = e.target.value;
                onChange({
                  ...current,
                  isolated: val === "true" ? true : val === "false" ? false : undefined,
                });
              }}
            >
              <option value="">inherit</option>
              <option value="true">yes</option>
              <option value="false">no</option>
            </select>
          )}
        </div>

        {LIST_FIELDS.map(({ key, label }) => (
          <div key={key} className="flex flex-col gap-0.5">
            <label className="text-[10px] text-muted-foreground" htmlFor={`sandbox-${key}`}>
              {label} <span className="font-mono">(comma-separated)</span>
            </label>
            {readOnly ? (
              <span className="text-xs font-mono text-foreground break-all">
                {formatList(current[key] as string[] | undefined)}
              </span>
            ) : (
              <input
                id={`sandbox-${key}`}
                type="text"
                className="h-7 rounded-md border border-border bg-surface-2 px-2 text-xs font-mono text-foreground w-full"
                value={(current[key] as string[] | undefined)?.join(", ") ?? ""}
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
