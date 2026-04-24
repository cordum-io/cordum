import { DollarSign } from "lucide-react";
import type { PolicyConstraints } from "@/api/types";

type Budgets = NonNullable<PolicyConstraints["budgets"]>;

export interface WorkflowPolicyOverridesBudgetsProps {
  budgets: Budgets | null;
  readOnly: boolean;
  onChange: (next: Budgets) => void;
}

const BUDGET_FIELDS: Array<{ key: keyof Budgets; label: string; unit: string }> = [
  { key: "max_runtime_ms", label: "Max runtime", unit: "ms" },
  { key: "max_retries", label: "Max retries", unit: "" },
  { key: "max_artifact_bytes", label: "Max artifact size", unit: "bytes" },
  { key: "max_concurrent_jobs", label: "Max concurrent jobs", unit: "" },
];

export function WorkflowPolicyOverridesBudgets({ budgets, readOnly, onChange }: WorkflowPolicyOverridesBudgetsProps) {
  const current = budgets ?? {};
  const hasValues = Object.values(current).some((v) => v !== undefined && v !== null);

  return (
    <div className="rounded-xl border border-border bg-surface-0 p-3">
      <div className="flex items-center gap-2 mb-2">
        <DollarSign className="w-3.5 h-3.5 text-muted-foreground" />
        <span className="text-xs font-semibold text-foreground">Budget Constraints</span>
        {!hasValues && (
          <span className="text-xs font-mono text-muted-foreground ml-auto">global defaults</span>
        )}
      </div>
      <div className="grid grid-cols-2 gap-2">
        {BUDGET_FIELDS.map(({ key, label, unit }) => (
          <div key={key} className="flex flex-col gap-0.5">
            <label className="text-xs text-muted-foreground" htmlFor={`budget-${key}`}>
              {label} {unit && <span className="font-mono">({unit})</span>}
            </label>
            {readOnly ? (
              <span className="text-xs font-mono text-foreground">
                {current[key] !== undefined && current[key] !== null
                  ? String(current[key])
                  : "—"}
              </span>
            ) : (
              <input
                id={`budget-${key}`}
                type="number"
                min={0}
                className="h-7 rounded-xl border border-border bg-surface-2 px-2 text-xs font-mono text-foreground w-full"
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
    </div>
  );
}

