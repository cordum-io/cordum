import { useCallback } from "react";
import { Trash2, Plus } from "lucide-react";
import { Input } from "./Input";
import { Button } from "./Button";
import { cn } from "../../lib/utils";

interface KeyValuePair {
  key: string;
  value: string;
}

interface KeyValueEditorProps {
  value: KeyValuePair[];
  onChange: (pairs: KeyValuePair[]) => void;
  maxPairs?: number;
  keyMaxLength?: number;
  valueMaxLength?: number;
  keyPlaceholder?: string;
  valuePlaceholder?: string;
  className?: string;
}

export function KeyValueEditor({
  value,
  onChange,
  maxPairs = 50,
  keyMaxLength = 64,
  valueMaxLength = 256,
  keyPlaceholder = "Key",
  valuePlaceholder = "Value",
  className,
}: KeyValueEditorProps) {
  const updatePair = useCallback(
    (idx: number, field: "key" | "value", text: string) => {
      const next = value.map((p, i) => (i === idx ? { ...p, [field]: text } : p));
      onChange(next);
    },
    [value, onChange],
  );

  const removePair = useCallback(
    (idx: number) => {
      onChange(value.filter((_, i) => i !== idx));
    },
    [value, onChange],
  );

  const addPair = useCallback(() => {
    if (value.length >= maxPairs) return;
    onChange([...value, { key: "", value: "" }]);
  }, [value, onChange, maxPairs]);

  return (
    <div className={cn("space-y-2", className)}>
      {value.map((pair, idx) => (
        <div key={idx} className="flex items-center gap-2">
          <Input
            value={pair.key}
            onChange={(e) => updatePair(idx, "key", e.target.value)}
            maxLength={keyMaxLength}
            placeholder={keyPlaceholder}
            className="flex-1"
          />
          <Input
            value={pair.value}
            onChange={(e) => updatePair(idx, "value", e.target.value)}
            maxLength={valueMaxLength}
            placeholder={valuePlaceholder}
            className="flex-1"
          />
          <button
            type="button"
            onClick={() => removePair(idx)}
            className="flex-shrink-0 rounded-full p-2 text-muted-foreground transition hover:bg-danger/10 hover:text-danger"
          >
            <Trash2 className="h-4 w-4" />
          </button>
        </div>
      ))}
      <Button
        type="button"
        variant="ghost"
        size="sm"
        onClick={addPair}
        disabled={value.length >= maxPairs}
      >
        <Plus className="h-4 w-4" />
        Add
      </Button>
    </div>
  );
}
