import { StatusBadge } from "@/components/ui/StatusBadge";
import type { GlobalPolicyDefaultDecision } from "@/types/policy";

interface InputRulesDefaultDecisionProps {
  value: GlobalPolicyDefaultDecision;
  readOnly?: boolean;
  onChange?: (next: GlobalPolicyDefaultDecision) => void;
}

export function InputRulesDefaultDecision({
  value,
  readOnly = false,
  onChange,
}: InputRulesDefaultDecisionProps) {
  if (readOnly) {
    return (
      <div className="flex items-center gap-2">
        <span className="text-xs text-muted-foreground">default_decision</span>
        <StatusBadge variant={value === "deny" ? "danger" : "warning"}>{value}</StatusBadge>
      </div>
    );
  }

  return (
    <label className="text-xs text-muted-foreground">
      default_decision
      <select
        className="ml-2 h-8 rounded-md border border-border bg-surface-2 px-2 text-xs text-foreground"
        value={value}
        onChange={(event) => onChange?.(event.target.value as GlobalPolicyDefaultDecision)}
      >
        <option value="deny">deny (recommended)</option>
        <option value="allow">allow</option>
      </select>
    </label>
  );
}
