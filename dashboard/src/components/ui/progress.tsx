/*
 * DESIGN: "Control Surface" — Progress Bar
 * Matches cordumds-gj5mw4zm.manus.space showcase patterns
 */
import { cn } from "@/lib/utils";

interface ProgressProps {
  value: number;
  max?: number;
  className?: string;
  indicatorClassName?: string;
}

export function Progress({ value, max = 100, className, indicatorClassName }: ProgressProps) {
  const pct = Math.min(100, Math.max(0, (value / max) * 100));
  return (
    <div className={cn("h-1.5 w-full rounded-full bg-surface-2 overflow-hidden", className)}>
      <div
        className={cn("h-full rounded-full bg-cordum transition-all duration-500", indicatorClassName)}
        style={{ width: `${pct}%` }}
      />
    </div>
  );
}
