import { Line, LineChart } from "recharts";
import { cn } from "@/lib/utils";

interface RuleFiringSparklineProps {
  values: number[];
  className?: string;
}

function normalizeValues(values: number[]): number[] {
  const lastSeven = values.slice(-7);
  if (lastSeven.length >= 7) return lastSeven;
  return [...Array.from({ length: 7 - lastSeven.length }, () => 0), ...lastSeven];
}

export function RuleFiringSparkline({ values, className }: RuleFiringSparklineProps) {
  const normalized = normalizeValues(values);
  const total = normalized.reduce((sum, value) => sum + value, 0);
  const data = normalized.map((value, index) => ({ day: index, value }));

  return (
    <span
      aria-label={`${total} firings over the last 7 days`}
      className={cn("inline-flex items-center justify-end gap-2", className)}
    >
      <span
        aria-hidden
        className="h-6 w-20"
        data-testid="rule-firing-sparkline"
      >
        <LineChart
          data={data}
          height={24}
          margin={{ top: 3, right: 0, bottom: 3, left: 0 }}
          width={80}
        >
          <Line
            dataKey="value"
            dot={false}
            isAnimationActive={false}
            stroke="var(--accent)"
            strokeWidth={2}
            type="monotone"
          />
        </LineChart>
      </span>
      <span className="tabular-nums">{total}</span>
    </span>
  );
}
