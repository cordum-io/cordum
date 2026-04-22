import { useMemo } from "react";
import {
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
  type DotProps,
} from "recharts";
import { ChartTooltip } from "@/components/ui/ChartTooltip";
import { EmptyState } from "@/components/ui/EmptyState";
import type { EvalRun } from "@/api/types";

interface ChartRow {
  runId: string;
  startedAt: string;
  score: number | null;
  regression: boolean;
}

interface RegressionDotProps extends DotProps {
  payload?: ChartRow;
}

function RegressionDot(props: RegressionDotProps) {
  const { cx, cy, payload } = props;
  if (typeof cx !== "number" || typeof cy !== "number") return null;
  if (payload?.regression) {
    return <circle cx={cx} cy={cy} r={5} fill="var(--color-danger, #ef4444)" stroke="none" />;
  }
  return <circle cx={cx} cy={cy} r={3} fill="var(--color-cordum, #10b981)" stroke="none" />;
}

export function ScoreTrendChart({ runs }: { runs: EvalRun[] }) {
  const rows = useMemo<ChartRow[]>(() => {
    return runs
      .slice(0, 30)
      .filter((r) => r.summary.scorePercent !== null)
      .map((r) => ({
        runId: r.runId,
        startedAt: new Date(r.startedAt).toLocaleDateString(undefined, {
          month: "short",
          day: "numeric",
        }),
        score: r.summary.scorePercent,
        regression: (r.summary.regressions ?? 0) > 0,
      }))
      .reverse();
  }, [runs]);

  if (rows.length === 0) {
    return (
      <div className="rounded-2xl border border-border bg-surface-1 p-4">
        <EmptyState title="No run history yet" description="Trigger a run to start tracking score." />
      </div>
    );
  }

  return (
    <div className="rounded-2xl border border-border bg-surface-1 p-4">
      <div className="mb-3">
        <h3 className="font-display text-sm font-semibold text-foreground">Score trend</h3>
        <p className="text-xs text-muted-foreground">
          Last {rows.length} run{rows.length === 1 ? "" : "s"}. Regressions marked in red.
        </p>
      </div>
      <ResponsiveContainer width="100%" height={200} className="sm:!h-[280px] lg:!h-[320px]">
        <LineChart data={rows} margin={{ top: 10, right: 16, left: 0, bottom: 0 }}>
          <CartesianGrid strokeDasharray="3 3" stroke="var(--border-color)" />
          <XAxis dataKey="startedAt" fontSize={11} tick={{ fill: "var(--muted-foreground)" }} />
          <YAxis
            domain={[0, 100]}
            fontSize={11}
            tick={{ fill: "var(--muted-foreground)" }}
            width={32}
          />
          <Tooltip content={<ChartTooltip />} />
          <Line
            type="monotone"
            dataKey="score"
            name="Score %"
            stroke="var(--color-cordum, #10b981)"
            strokeWidth={2}
            dot={<RegressionDot />}
            activeDot={{ r: 6 }}
            isAnimationActive={false}
          />
        </LineChart>
      </ResponsiveContainer>
    </div>
  );
}
