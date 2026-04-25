import type { GlobalPolicyInputDecision } from "@/types/policy";
import { PolicyField } from "./PolicyField";

interface PolicyDecisionSelectProps {
  value: GlobalPolicyInputDecision;
  onChange: (next: GlobalPolicyInputDecision) => void;
  inputId?: string;
  required?: boolean;
  error?: string;
  helperByDecision?: Partial<Record<GlobalPolicyInputDecision, string>>;
}

const DEFAULT_DECISION_HELP: Partial<Record<GlobalPolicyInputDecision, string>> = {
  allow: "Allows matching requests with no extra constraints.",
  deny: "Blocks matching requests. Consider adding remediations for operators.",
  require_approval: "Requires human approval before execution.",
  allow_with_constraints: "Allows request with constraints such as runtime/tooling limits.",
  throttle: "Allows request under throttling and optional constraints.",
};

export function PolicyDecisionSelect({
  value,
  onChange,
  inputId = "policy-decision-select",
  required = true,
  error,
  helperByDecision = DEFAULT_DECISION_HELP,
}: PolicyDecisionSelectProps) {
  const helperText = helperByDecision[value];

  return (
    <div className="space-y-2">
      <PolicyField
        label="Decision"
        inputId={inputId}
        required={required}
        error={error}
        helpText="Decision returned when this rule matches."
      >
        <select
          className="mt-1 h-8 w-full rounded-xl border border-border bg-surface-2 px-3 text-xs text-foreground"
          value={value}
          onChange={(event) => onChange(event.target.value as GlobalPolicyInputDecision)}
        >
          <option value="allow">allow</option>
          <option value="deny">deny</option>
          <option value="require_approval">require_approval</option>
          <option value="allow_with_constraints">allow_with_constraints</option>
          <option value="throttle">throttle</option>
        </select>
      </PolicyField>
      {helperText && (
        <p className="rounded-xl border border-cordum/30 bg-cordum/10 px-2 py-1 text-xs text-cordum-foreground">
          {helperText}
        </p>
      )}
    </div>
  );
}

