import { useEffect, useMemo, useState } from "react";
import { PolicyField } from "@/components/policy/studio-primitives/PolicyField";

interface TenantTagListEditorProps {
  inputId: string;
  label: string;
  helpText: string;
  hint?: string;
  values: string[];
  onChange: (next: string[]) => void;
  readOnly?: boolean;
}

function toCsv(values: string[]): string {
  return values.join(", ");
}

function parseCsv(raw: string): string[] {
  return raw
    .split(",")
    .map((value) => value.trim())
    .filter(Boolean);
}

function normalizeTag(value: string): string {
  return value.trim().toLowerCase();
}

export function sanitizeTenantTags(values: string[]): {
  unique: string[];
  duplicates: string[];
} {
  const seen = new Set<string>();
  const duplicates = new Set<string>();
  const unique: string[] = [];

  for (const value of values) {
    const trimmed = value.trim();
    if (!trimmed) continue;
    const normalized = normalizeTag(trimmed);
    if (seen.has(normalized)) {
      duplicates.add(trimmed);
      continue;
    }
    seen.add(normalized);
    unique.push(trimmed);
  }

  return { unique, duplicates: [...duplicates] };
}

export function TenantTagListEditor({
  inputId,
  label,
  helpText,
  hint,
  values,
  onChange,
  readOnly = false,
}: TenantTagListEditorProps) {
  const [draftValue, setDraftValue] = useState(toCsv(values));
  const [duplicates, setDuplicates] = useState<string[]>([]);

  useEffect(() => {
    setDraftValue(toCsv(values));
    setDuplicates([]);
  }, [values]);

  const duplicateText = useMemo(
    () =>
      duplicates.length > 0
        ? `Duplicate entries removed: ${duplicates.join(", ")}.`
        : undefined,
    [duplicates],
  );

  return (
    <PolicyField
      inputId={inputId}
      label={label}
      helpText={helpText}
      hint={hint}
      error={duplicateText}
    >
      <input
        id={inputId}
        className="mt-1 h-8 w-full rounded-xl border border-border bg-surface-2 px-3 text-xs text-foreground disabled:cursor-not-allowed disabled:opacity-70"
        value={draftValue}
        disabled={readOnly}
        onChange={(event) => {
          const nextDraft = event.target.value;
          setDraftValue(nextDraft);
          const parsed = parseCsv(nextDraft);
          const sanitized = sanitizeTenantTags(parsed);
          setDuplicates(sanitized.duplicates);
          onChange(sanitized.unique);
        }}
      />
    </PolicyField>
  );
}

