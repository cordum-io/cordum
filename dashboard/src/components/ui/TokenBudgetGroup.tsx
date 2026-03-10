import { useCallback } from "react";
import { Input } from "./Input";
import { cn } from "../../lib/utils";

interface TokenBudgetGroupProps {
  inputTokens: number;
  outputTokens: number;
  onInputChange: (v: number) => void;
  onOutputChange: (v: number) => void;
  maxTotal?: number;
  step?: number;
  className?: string;
}

export function TokenBudgetGroup({
  inputTokens,
  outputTokens,
  onInputChange,
  onOutputChange,
  maxTotal = 1_000_000,
  step = 100,
  className,
}: TokenBudgetGroupProps) {
  const total = inputTokens + outputTokens;
  const inputPct = total > 0 ? (inputTokens / total) * 100 : 50;
  const outputPct = total > 0 ? (outputTokens / total) * 100 : 50;

  const clamp = useCallback(
    (raw: string) => {
      const n = parseInt(raw, 10);
      if (isNaN(n)) return 0;
      return Math.max(0, Math.min(n, maxTotal));
    },
    [maxTotal],
  );

  return (
    <div className={cn("space-y-3", className)}>
      <div className="grid grid-cols-3 gap-3">
        <label className="flex flex-col gap-1">
          <span className="text-xs font-medium text-muted-foreground">Input tokens</span>
          <Input
            type="number"
            min={0}
            max={maxTotal}
            step={step}
            value={inputTokens}
            onChange={(e) => onInputChange(clamp(e.target.value))}
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-xs font-medium text-muted-foreground">Output tokens</span>
          <Input
            type="number"
            min={0}
            max={maxTotal}
            step={step}
            value={outputTokens}
            onChange={(e) => onOutputChange(clamp(e.target.value))}
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-xs font-medium text-muted-foreground">Total</span>
          <Input type="number" value={total} disabled className="opacity-70" />
        </label>
      </div>

      {/* Ratio bar */}
      <div className="h-2 overflow-hidden rounded-full bg-surface2 flex">
        <div
          className="h-full bg-accent transition-all duration-200"
          style={{ width: `${inputPct}%` }}
        />
        <div
          className="h-full bg-info transition-all duration-200"
          style={{ width: `${outputPct}%` }}
        />
      </div>

      <div className="flex justify-between text-[10px] text-muted-foreground">
        <span>Input {inputPct.toFixed(0)}%</span>
        <span>Output {outputPct.toFixed(0)}%</span>
      </div>
    </div>
  );
}
