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
  pending: "#5a6a70",
  queued: "#5a6a70",
  running: "#0f7f7a",
  in_progress: "#0f7f7a",
  succeeded: "#1f7a57",
  completed: "#1f7a57",
  failed: "#b83a3a",
  timed_out: "#d4833a",
  cancelled: "#5a6a70",
  blocked: "#c58a1c",
};

const blockingColor = "rgba(25, 36, 40, 0.12)";

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
    <div className="rounded-xl border border-border bg-card px-3 py-2 shadow-lg text-xs">
      <p className="font-semibold text-ink">{bar.name}</p>
      <p className="text-muted-foreground">
        Status: <span className="capitalize">{bar.status.replace(/_/g, " ")}</span>
      </p>
      <p className="text-muted-foreground">Duration: {formatMs(bar.duration)}</p>
      {bar.blockingTime > 0 && (
        <p className="text-muted-foreground">Wait: {formatMs(bar.blockingTime)}</p>
      )}
      {bar.safetyDecision && (
        <p className="text-muted-foreground">Safety: {bar.safetyDecision}</p>
      )}
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
      <div className="rounded-2xl border border-dashed border-border px-6 py-8 text-center text-xs text-muted-foreground">
        No step timing data available.
      </div>
    );
  }

  const chartHeight = Math.max(bars.length * 44 + 40, 160);

  return (
    <div className="surface-card rounded-2xl p-4">
      <h3 className="mb-3 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
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
            fontSize={10}
            stroke="#5a6a70"
          />
          <YAxis
            type="category"
            dataKey="name"
            width={120}
            fontSize={11}
            stroke="#5a6a70"
            tick={{ fill: "#1a2b32" }}
          />
          <Tooltip content={<GanttTooltip />} />

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
                fill={statusColor[bar.status] ?? "#5a6a70"}
              />
            ))}
          </Bar>
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}
