import { useEffect, useState } from "react";
import { PolicyField } from "./PolicyField";

interface PolicyTagInputProps {
  inputId: string;
  label: string;
  helpText: string;
  hint?: string;
  value: string[];
  onChange: (next: string[]) => void;
  error?: string;
  disabled?: boolean;
}

function parseCsv(raw: string): string[] {
  return raw
    .split(",")
    .map((entry) => entry.trim())
    .filter(Boolean);
}

function toCsv(values: string[]): string {
  return values.join(", ");
}

export function PolicyTagInput({
  inputId,
  label,
  helpText,
  hint,
  value,
  onChange,
  error,
  disabled = false,
}: PolicyTagInputProps) {
  const [draftValue, setDraftValue] = useState(toCsv(value));

  useEffect(() => {
    setDraftValue(toCsv(value));
  }, [value]);

  return (
    <PolicyField
      inputId={inputId}
      label={label}
      helpText={helpText}
      hint={hint}
      error={error}
    >
      <input
        className="mt-1 h-8 w-full rounded-md border border-border bg-surface-2 px-3 text-xs text-foreground disabled:cursor-not-allowed disabled:opacity-70"
        value={draftValue}
        disabled={disabled}
        onChange={(event) => {
          const next = event.target.value;
          setDraftValue(next);
          onChange(parseCsv(next));
        }}
      />
    </PolicyField>
  );
}

export const __policyTagInputInternal = {
  parseCsv,
  toCsv,
};
