import { FileDiff } from "lucide-react";
import type { PolicyConstraints } from "@/api/types";

type Diff = NonNullable<PolicyConstraints["diff"]>;

export interface WorkflowPolicyOverridesDiffProps {
  diff: Diff | null;
  readOnly: boolean;
  onChange: (next: Diff) => void;
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

const NUM_FIELDS: Array<{ key: "max_files" | "max_lines"; label: string }> = [
  { key: "max_files", label: "Max files" },
  { key: "max_lines", label: "Max lines" },
];

export function WorkflowPolicyOverridesDiff({ diff, readOnly, onChange }: WorkflowPolicyOverridesDiffProps) {
  const current = diff ?? {};
  const hasValues =
    current.max_files !== undefined ||
    current.max_lines !== undefined ||
    (current.deny_path_globs && current.deny_path_globs.length > 0);

  return (
    <div className="rounded-lg border border-border bg-surface-0 p-3">
      <div className="flex items-center gap-2 mb-2">
        <FileDiff className="w-3.5 h-3.5 text-muted-foreground" />
        <span className="text-xs font-semibold text-foreground">Diff Constraints</span>
        {!hasValues && (
          <span className="text-[10px] font-mono text-muted-foreground ml-auto">global defaults</span>
        )}
      </div>
      <div className="space-y-2">
        <div className="grid grid-cols-2 gap-2">
          {NUM_FIELDS.map(({ key, label }) => (
            <div key={key} className="flex flex-col gap-0.5">
              <label className="text-[10px] text-muted-foreground" htmlFor={`diff-${key}`}>
                {label}
              </label>
              {readOnly ? (
                <span className="text-xs font-mono text-foreground">
                  {current[key] !== undefined && current[key] !== null
                    ? String(current[key])
                    : "—"}
                </span>
              ) : (
                <input
                  id={`diff-${key}`}
                  type="number"
                  min={0}
                  className="h-7 rounded-md border border-border bg-surface-2 px-2 text-xs font-mono text-foreground w-full"
                  value={current[key] ?? ""}
                  placeholder="inherit"
                  onChange={(e) => {
                    const val = e.target.value.trim();
                    const num = val === "" ? undefined : Number(val);
                    onChange({ ...current, [key]: num && Number.isFinite(num) && num > 0 ? num : undefined });
                  }}
                />
              )}
            </div>
          ))}
        </div>

        <div className="flex flex-col gap-0.5">
          <label className="text-[10px] text-muted-foreground" htmlFor="diff-deny-paths">
            Deny path globs <span className="font-mono">(comma-separated)</span>
          </label>
          {readOnly ? (
            <span className="text-xs font-mono text-foreground break-all">
              {formatList(current.deny_path_globs)}
            </span>
          ) : (
            <input
              id="diff-deny-paths"
              type="text"
              className="h-7 rounded-md border border-border bg-surface-2 px-2 text-xs font-mono text-foreground w-full"
              value={current.deny_path_globs?.join(", ") ?? ""}
              placeholder="inherit"
              onChange={(e) => {
                const list = parseList(e.target.value);
                onChange({ ...current, deny_path_globs: list.length > 0 ? list : undefined });
              }}
            />
          )}
        </div>
      </div>
    </div>
  );
}
