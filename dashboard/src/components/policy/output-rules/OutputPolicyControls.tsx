import type { GlobalPolicyOutputPolicy } from "@/types/policy";

interface OutputPolicyControlsProps {
  outputPolicy: GlobalPolicyOutputPolicy;
  readOnly: boolean;
  onChange: (next: GlobalPolicyOutputPolicy) => void;
}

export function OutputPolicyControls({
  outputPolicy,
  readOnly,
  onChange,
}: OutputPolicyControlsProps) {
  return (
    <section className="rounded-xl border border-border bg-surface-0 p-4 space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h3 className="font-display text-sm font-semibold text-foreground">output_policy</h3>
        <label className="inline-flex items-center gap-2 text-xs text-muted-foreground">
          <input
            type="checkbox"
            checked={outputPolicy.enabled}
            disabled={readOnly}
            onChange={(event) =>
              onChange({
                ...outputPolicy,
                enabled: event.target.checked,
              })
            }
          />
          enabled
        </label>
      </div>

      <label className="text-xs text-muted-foreground block">
        fail_mode
        <select
          className="ml-2 h-8 rounded-xl border border-border bg-surface-2 px-2 text-xs text-foreground disabled:cursor-not-allowed disabled:opacity-70"
          value={outputPolicy.failMode}
          disabled={readOnly}
          onChange={(event) =>
            onChange({
              ...outputPolicy,
              failMode: event.target.value as GlobalPolicyOutputPolicy["failMode"],
            })
          }
        >
          <option value="closed">closed (recommended)</option>
          <option value="open">open</option>
        </select>
      </label>

      <div className="rounded border border-border bg-surface-1 p-2 text-xs text-muted-foreground">
        <p>
          <span className="font-medium text-foreground">fail-closed:</span> on scanner failure, block or quarantine output by default.
        </p>
        <p className="mt-1">
          <span className="font-medium text-foreground">fail-open:</span> on scanner failure, allow output delivery and continue telemetry.
        </p>
      </div>
    </section>
  );
}

