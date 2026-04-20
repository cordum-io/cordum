import { useMemo } from "react";
import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  Tooltip,
  Cell,
  ResponsiveContainer,
  type TooltipProps,
} from "recharts";
import { tooltipProps, axisProps, gridProps } from "../../lib/chart-theme";
import type { WorkflowStep } from "../../api/types";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface GanttBar {
  name: string;
  stepId: string;
  status: string;
  start: number;
  duration: number;
  blockingTime: number;
  safetyDecision?: string;
}

// ---------------------------------------------------------------------------
// Status colors
// ---------------------------------------------------------------------------

const statusColor: Record<string, string> = {
  pending: "var(--muted)",
  queued: "var(--muted)",
  running: "var(--accent)",
  in_progress: "var(--accent)",
  succeeded: "var(--success)",
  completed: "var(--success)",
  failed: "var(--danger)",
  timed_out: "var(--warning)",
  cancelled: "var(--muted)",
  blocked: "var(--warning)",
};

const blockingColor = "var(--surface-2)";

// ---------------------------------------------------------------------------
// Build bars from steps
// ---------------------------------------------------------------------------

function buildBars(steps: WorkflowStep[]): { bars: GanttBar[]; minTime: number } {
  // Find earliest start
  const startTimes = steps
    .map((s) => (s.startedAt ? new Date(s.startedAt).getTime() : Infinity))
    .filter((t) => t !== Infinity);

  const minTime = startTimes.length > 0 ? Math.min(...startTimes) : Date.now();

  // Sort steps by start time (pending steps at end)
  const sorted = [...steps].sort((a, b) => {
    const aT = a.startedAt ? new Date(a.startedAt).getTime() : Infinity;
    const bT = b.startedAt ? new Date(b.startedAt).getTime() : Infinity;
    return aT - bT;
  });

  let prevEnd = minTime;

  return {
    bars: sorted.map((step) => {
      const startMs = step.startedAt ? new Date(step.startedAt).getTime() : prevEnd;
      const endMs = step.completedAt
        ? new Date(step.completedAt).getTime()
        : startMs + 1000; // Show 1s bar for in-progress/pending

      const blockingTime = Math.max(0, startMs - prevEnd);
      const duration = Math.max(endMs - startMs, 100); // At least 100ms visible

      if (endMs > prevEnd) prevEnd = endMs;

      const safetyDecision =
        typeof step.output?.safetyDecision === "string"
          ? step.output.safetyDecision
          : undefined;

      return {
        name: step.name || step.id,
        stepId: step.id,
        status: step.status ?? "pending",
        start: startMs - minTime,
        duration,
        blockingTime,
        safetyDecision,
      };
    }),
    minTime,
  };
}

// ---------------------------------------------------------------------------
// Format ms to human-readable
// ---------------------------------------------------------------------------

function formatMs(ms: number): string {
  if (ms < 1_000) return `${Math.round(ms)}ms`;
  const secs = ms / 1_000;
  if (secs < 60) return `${secs.toFixed(1)}s`;
  const mins = Math.floor(secs / 60);
  const remSecs = Math.round(secs % 60);
  return `${mins}m ${remSecs}s`;
}

// ---------------------------------------------------------------------------
// Custom tooltip
// ---------------------------------------------------------------------------

function GanttTooltip({ active, payload }: TooltipProps<number, string>) {
  if (!active || !payload?.length) return null;
  const bar = payload[0]?.payload as GanttBar | undefined;
  if (!bar) return null;

  return (
    <div className="rounded-md border border-border bg-surface-2 px-3 py-2 shadow-lg text-[11px] space-y-1.5">
      <p className="font-bold text-ink uppercase tracking-tight">{bar.name}</p>
      <div className="space-y-1 text-muted">
        <p className="flex justify-between gap-4">
          <span className="uppercase text-[9px] font-bold tracking-tighter">Status</span>
          <span className="capitalize font-medium text-ink">{bar.status.replace(/_/g, " ")}</span>
        </p>
        <p className="flex justify-between gap-4">
          <span className="uppercase text-[9px] font-bold tracking-tighter">Duration</span>
          <span className="font-mono text-ink">{formatMs(bar.duration)}</span>
        </p>
        {bar.blockingTime > 0 && (
          <p className="flex justify-between gap-4">
            <span className="uppercase text-[9px] font-bold tracking-tighter">Wait</span>
            <span className="font-mono text-ink">{formatMs(bar.blockingTime)}</span>
          </p>
        )}
        {bar.safetyDecision && (
          <p className="flex justify-between gap-4">
            <span className="uppercase text-[9px] font-bold tracking-tighter">Safety</span>
            <span className="font-medium text-ink">{bar.safetyDecision}</span>
          </p>
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// GanttTimeline
// ---------------------------------------------------------------------------

export function GanttTimeline({ steps }: { steps: WorkflowStep[] }) {
  const { bars } = useMemo(() => buildBars(steps), [steps]);

  if (bars.length === 0) {
    return (
      <div className="rounded-2xl border border-dashed border-border px-6 py-8 text-center text-xs text-muted">
        No step timing data available.
      </div>
    );
  }

  const chartHeight = Math.max(bars.length * 44 + 40, 160);

  return (
    <div className="surface-card rounded-2xl p-4">
      <h3 className="mb-3 text-xs font-semibold uppercase tracking-wide text-muted">
        Execution Timeline
      </h3>
      <ResponsiveContainer width="100%" height={chartHeight}>
        <BarChart
          data={bars}
          layout="vertical"
          margin={{ top: 4, right: 20, bottom: 4, left: 20 }}
          barGap={0}
          barCategoryGap="20%"
        >
          <XAxis
            type="number"
            tickFormatter={(v: number) => formatMs(v)}
            {...axisProps}
          />
          <YAxis
            type="category"
            dataKey="name"
            width={120}
            {...axisProps}
            tick={{ ...axisProps.tick, fill: "var(--text)" }}
          />
          <Tooltip content={<GanttTooltip />} cursor={{ fill: 'var(--surface-2)', opacity: 0.4 }} />

          {/* Blocking time (wait before step) */}
          <Bar dataKey="blockingTime" stackId="a" radius={[4, 0, 0, 4]}>
            {bars.map((bar) => (
              <Cell key={`block-${bar.stepId}`} fill={blockingColor} />
            ))}
          </Bar>

          {/* Execution duration */}
          <Bar dataKey="duration" stackId="a" radius={[0, 4, 4, 0]}>
            {bars.map((bar) => (
              <Cell
                key={`dur-${bar.stepId}`}
                fill={statusColor[bar.status] ?? "#94a3b8"}
              />
            ))}
          </Bar>
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}
