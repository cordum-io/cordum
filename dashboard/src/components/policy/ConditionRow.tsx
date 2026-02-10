import { useCallback, useState } from "react";
import { Plus, Trash2, X } from "lucide-react";
import { Input } from "../ui/Input";
import { Select } from "../ui/Select";
import { Button } from "../ui/Button";
import type { Condition, ConditionField, ConditionOperator } from "./conditionTypes";

const FIELD_OPTIONS: { value: ConditionField; label: string }[] = [
  { value: "capability", label: "Capability" },
  { value: "riskTag", label: "Risk Tag" },
  { value: "topic", label: "Topic" },
  { value: "agentPool", label: "Agent Pool" },
];

const OPERATOR_OPTIONS: { value: ConditionOperator; label: string }[] = [
  { value: "equals", label: "equals" },
  { value: "contains", label: "contains" },
  { value: "in", label: "in" },
  { value: "not_in", label: "not in" },
];

const isMultiValueOp = (op: ConditionOperator) => op === "in" || op === "not_in";

interface ConditionRowProps {
  condition: Condition;
  onChange: (updated: Condition) => void;
  onRemove: () => void;
}

export function ConditionRow({ condition, onChange, onRemove }: ConditionRowProps) {
  const [draft, setDraft] = useState("");
  const multiValue = isMultiValueOp(condition.operator);
  const values = Array.isArray(condition.value) ? condition.value : [];

  const handleFieldChange = useCallback(
    (e: React.ChangeEvent<HTMLSelectElement>) => {
      onChange({ ...condition, field: e.target.value as ConditionField });
    },
    [condition, onChange],
  );

  const handleOperatorChange = useCallback(
    (e: React.ChangeEvent<HTMLSelectElement>) => {
      const op = e.target.value as ConditionOperator;
      const newMulti = isMultiValueOp(op);
      const val = newMulti
        ? Array.isArray(condition.value)
          ? condition.value
          : condition.value
            ? [condition.value]
            : []
        : Array.isArray(condition.value)
          ? condition.value.join(", ")
          : condition.value;
      onChange({ ...condition, operator: op, value: val });
    },
    [condition, onChange],
  );

  const handleSingleValueChange = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      onChange({ ...condition, value: e.target.value });
    },
    [condition, onChange],
  );

  const addTag = useCallback(() => {
    const trimmed = draft.trim();
    if (trimmed && !values.includes(trimmed)) {
      onChange({ ...condition, value: [...values, trimmed] });
    }
    setDraft("");
  }, [draft, values, condition, onChange]);

  const removeTag = useCallback(
    (tag: string) => {
      onChange({ ...condition, value: values.filter((v) => v !== tag) });
    },
    [values, condition, onChange],
  );

  return (
    <div className="flex flex-wrap items-start gap-2">
      <Select
        value={condition.field}
        onChange={handleFieldChange}
        className="w-32 !py-1.5 text-xs"
      >
        {FIELD_OPTIONS.map((opt) => (
          <option key={opt.value} value={opt.value}>
            {opt.label}
          </option>
        ))}
      </Select>

      <Select
        value={condition.operator}
        onChange={handleOperatorChange}
        className="w-28 !py-1.5 text-xs"
      >
        {OPERATOR_OPTIONS.map((opt) => (
          <option key={opt.value} value={opt.value}>
            {opt.label}
          </option>
        ))}
      </Select>

      {multiValue ? (
        <div className="flex min-w-[180px] flex-1 flex-col gap-1.5">
          <div className="flex gap-1.5">
            <Input
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  addTag();
                }
              }}
              placeholder="Add value..."
              className="flex-1 !py-1.5 text-xs"
            />
            <Button variant="outline" size="sm" type="button" onClick={addTag}>
              <Plus className="h-3 w-3" />
            </Button>
          </div>
          {values.length > 0 && (
            <div className="flex flex-wrap gap-1">
              {values.map((v) => (
                <button
                  key={v}
                  type="button"
                  onClick={() => removeTag(v)}
                  className="inline-flex items-center gap-0.5 rounded-full border border-border px-2 py-0.5 text-[11px] font-medium text-ink transition hover:border-danger hover:text-danger"
                >
                  {v}
                  <X className="h-2.5 w-2.5" />
                </button>
              ))}
            </div>
          )}
        </div>
      ) : (
        <Input
          value={typeof condition.value === "string" ? condition.value : ""}
          onChange={handleSingleValueChange}
          placeholder="Value"
          className="min-w-[180px] flex-1 !py-1.5 text-xs"
        />
      )}

      <Button
        variant="ghost"
        size="sm"
        type="button"
        onClick={onRemove}
        className="text-danger"
      >
        <Trash2 className="h-3.5 w-3.5" />
      </Button>
    </div>
  );
}
