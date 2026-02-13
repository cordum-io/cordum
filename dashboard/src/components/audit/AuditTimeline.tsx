import { useMemo, useState, useCallback, useRef } from "react";
import {
  ScatterChart,
  Scatter,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
  ReferenceArea,
} from "recharts";
import { ZoomOut } from "lucide-react";
import { Button } from "../ui/Button";
import { Badge } from "../ui/Badge";
import type { AuditEntry, AuditCategory, AuditSeverity } from "../../api/types";

// ---------------------------------------------------------------------------
// Category → Y-axis mapping
// ---------------------------------------------------------------------------

const CATEGORY_Y: Record<AuditCategory, number> = {
  safety_decision: 3,
  human_action: 2,
  system_event: 1,
  access_event: 0,
};

const CATEGORY_LABELS: Record<number, string> = {
  3: "Safety",
  2: "Human",
  1: "System",
  0: "Access",
};

// ---------------------------------------------------------------------------
// Color mapping from action
// ---------------------------------------------------------------------------

type EventColor = "red" | "yellow" | "blue" | "green";

function eventColor(entry: AuditEntry): EventColor {
  const action = (entry.action || entry.eventType || "").toLowerCase();
  if (action.includes("deny") || action.includes("fail") || action.includes("error") || action.includes("reject")) {
    return "red";
  }
  if (action.includes("approve") || action.includes("warn") || action.includes("require")) {
    return "yellow";
  }
  if (action.includes("success") || action.includes("allow") || action.includes("complete")) {
    return "green";
  }
  return "blue";
}

const COLOR_HEX: Record<EventColor, string> = {
  red: "#ef4444",
  yellow: "#eab308",
  blue: "#3b82f6",
  green: "#22c55e",
};

// ---------------------------------------------------------------------------
// Severity → dot size
// ---------------------------------------------------------------------------

const SEVERITY_SIZE: Record<AuditSeverity, number> = {
  high: 80,
  medium: 50,
  low: 30,
};

// ---------------------------------------------------------------------------
// Data point type
// ---------------------------------------------------------------------------

interface TimelinePoint {
  x: number;
  y: number;
  color: string;
  size: number;
  entry: AuditEntry;
  isCluster?: boolean;
  clusterCount?: number;
  clusterEntries?: AuditEntry[];
}

// ---------------------------------------------------------------------------
// Clustering
// ---------------------------------------------------------------------------

function clusterEvents(events: AuditEntry[], rangeMs: number): TimelinePoint[] {
  if (events.length === 0) return [];

  const threshold = rangeMs * 0.01;
  const sorted = [...events].sort(
    (a, b) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime(),
  );

  const points: TimelinePoint[] = [];
  let cluster: AuditEntry[] = [sorted[0]];

  for (let i = 1; i < sorted.length; i++) {
    const prevT = new Date(cluster[cluster.length - 1].timestamp).getTime();
    const currT = new Date(sorted[i].timestamp).getTime();

    if (currT - prevT <= threshold) {
      cluster.push(sorted[i]);
    } else {
      points.push(...flushCluster(cluster));
      cluster = [sorted[i]];
    }
  }
  points.push(...flushCluster(cluster));
  return points;
}

function flushCluster(entries: AuditEntry[]): TimelinePoint[] {
  if (entries.length === 1) {
    const e = entries[0];
    const cat = e.category ?? "system_event";
    const sev = e.severity ?? "low";
    return [{
      x: new Date(e.timestamp).getTime(),
      y: CATEGORY_Y[cat],
      color: COLOR_HEX[eventColor(e)],
      size: SEVERITY_SIZE[sev],
      entry: e,
    }];
  }

  const midEntry = entries[Math.floor(entries.length / 2)];
  const cat = midEntry.category ?? "system_event";
  return [{
    x: new Date(midEntry.timestamp).getTime(),
    y: CATEGORY_Y[cat],
    color: "#8b5cf6",
    size: Math.min(200, 40 + entries.length * 10),
    entry: midEntry,
    isCluster: true,
    clusterCount: entries.length,
    clusterEntries: entries,
  }];
}

// ---------------------------------------------------------------------------
// Axis tick formatter
// ---------------------------------------------------------------------------

function formatAxisTick(ms: number, rangeMs: number): string {
  const d = new Date(ms);
  const hh = d.getHours().toString().padStart(2, "0");
  const mm = d.getMinutes().toString().padStart(2, "0");
  const ss = d.getSeconds().toString().padStart(2, "0");

  if (rangeMs < 60_000) {
    return `${hh}:${mm}:${ss}.${d.getMilliseconds().toString().padStart(3, "0")}`;
  }
  if (rangeMs < 3_600_000) return `${hh}:${mm}:${ss}`;
  if (rangeMs < 86_400_000) return `${hh}:${mm}`;
  return `${(d.getMonth() + 1).toString().padStart(2, "0")}/${d.getDate().toString().padStart(2, "0")} ${hh}:${mm}`;
}

// ---------------------------------------------------------------------------
// Custom tooltip
// ---------------------------------------------------------------------------

function TimelineTooltip({ active, payload }: { active?: boolean; payload?: Array<{ payload: TimelinePoint }> }) {
  if (!active || !payload?.length) return null;
  const point = payload[0].payload;

  if (point.isCluster) {
    return (
      <div className="rounded-lg border border-border bg-surface px-3 py-2 shadow-lg text-xs space-y-1">
        <p className="font-semibold text-ink">{point.clusterCount} events</p>
        <p className="text-muted">Click to zoom into this cluster</p>
      </div>
    );
  }

  const e = point.entry;
  return (
    <div className="rounded-lg border border-border bg-surface px-3 py-2 shadow-lg text-xs space-y-1 max-w-xs">
      <div className="flex items-center gap-2">
        <Badge variant="info">{e.eventType || e.action}</Badge>
        {e.severity && e.severity !== "low" && (
          <Badge variant={e.severity === "high" ? "danger" : "warning"}>
            {e.severity}
          </Badge>
        )}
      </div>
      <p className="text-ink font-medium truncate">
        {e.message || `${e.action} on ${e.resourceType}`}
      </p>
      <p className="text-muted font-mono">
        {new Date(e.timestamp).toISOString().replace("T", " ").replace("Z", "")}
      </p>
      <p className="text-muted">Actor: {e.actor}</p>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Custom dot shape
// ---------------------------------------------------------------------------

function DotShape(props: { cx?: number; cy?: number; payload?: TimelinePoint }) {
  const { cx = 0, cy = 0, payload } = props;
  if (!payload) return null;

  const r = Math.sqrt(payload.size) / 2;

  if (payload.isCluster) {
    return (
      <g>
        <circle cx={cx} cy={cy} r={r + 2} fill={payload.color} opacity={0.3} />
        <circle cx={cx} cy={cy} r={r} fill={payload.color} opacity={0.8} />
        <text
          x={cx}
          y={cy}
          textAnchor="middle"
          dominantBaseline="central"
          className="fill-white text-[9px] font-bold"
        >
          {payload.clusterCount}
        </text>
      </g>
    );
  }

  return (
    <circle cx={cx} cy={cy} r={r} fill={payload.color} opacity={0.8} className="cursor-pointer" />
  );
}

// ---------------------------------------------------------------------------
// Legend
// ---------------------------------------------------------------------------

function TimelineLegend() {
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-[10px] text-muted">
      <span className="flex items-center gap-1">
        <span className="h-2.5 w-2.5 rounded-full bg-red-500" /> Deny/Fail
      </span>
      <span className="flex items-center gap-1">
        <span className="h-2.5 w-2.5 rounded-full bg-yellow-500" /> Approve/Warn
      </span>
      <span className="flex items-center gap-1">
        <span className="h-2.5 w-2.5 rounded-full bg-blue-500" /> Normal
      </span>
      <span className="flex items-center gap-1">
        <span className="h-2.5 w-2.5 rounded-full bg-green-500" /> Success
      </span>
      <span className="flex items-center gap-1">
        <span className="h-2.5 w-2.5 rounded-full bg-purple-500" /> Cluster
      </span>
    </div>
  );
}

// ---------------------------------------------------------------------------
// AuditTimeline
// ---------------------------------------------------------------------------

interface AuditTimelineProps {
  events: AuditEntry[];
  onEventClick: (id: string) => void;
}

export function AuditTimeline({ events, onEventClick }: AuditTimelineProps) {
  const fullRange = useMemo(() => {
    if (events.length === 0) return { min: Date.now() - 86_400_000, max: Date.now() };
    const times = events.map((e) => new Date(e.timestamp).getTime());
    const min = Math.min(...times);
    const max = Math.max(...times);
    const pad = Math.max((max - min) * 0.02, 1000);
    return { min: min - pad, max: max + pad };
  }, [events]);

  const [zoomRange, setZoomRange] = useState<{ min: number; max: number } | null>(null);
  const range = zoomRange ?? fullRange;
  const rangeMs = range.max - range.min;
  const isZoomed = !!zoomRange;

  const [brushStart, setBrushStart] = useState<number | null>(null);
  const [brushEnd, setBrushEnd] = useState<number | null>(null);

  const points = useMemo(() => clusterEvents(events, rangeMs), [events, rangeMs]);
  const visiblePoints = useMemo(
    () => points.filter((p) => p.x >= range.min && p.x <= range.max),
    [points, range],
  );

  const resetZoom = useCallback(() => setZoomRange(null), []);

  const chartRef = useRef<HTMLDivElement>(null);

  const handleWheel = useCallback(
    (e: React.WheelEvent) => {
      e.preventDefault();
      const r = zoomRange ?? fullRange;
      const center = (r.min + r.max) / 2;
      const halfRange = (r.max - r.min) / 2;
      const factor = e.deltaY > 0 ? 1.3 : 0.7;
      const newHalf = Math.max(500, halfRange * factor);
      const newMin = Math.max(fullRange.min, center - newHalf);
      const newMax = Math.min(fullRange.max, center + newHalf);

      if (newMax - newMin >= fullRange.max - fullRange.min) {
        setZoomRange(null);
      } else {
        setZoomRange({ min: newMin, max: newMax });
      }
    },
    [zoomRange, fullRange],
  );

  const handleClick = useCallback(
    (data: { payload?: TimelinePoint }) => {
      if (!data?.payload) return;
      const p = data.payload;
      if (p.isCluster && p.clusterEntries) {
        const times = p.clusterEntries.map((e) => new Date(e.timestamp).getTime());
        const min = Math.min(...times);
        const max = Math.max(...times);
        const pad = Math.max((max - min) * 0.1, 1000);
        setZoomRange({ min: min - pad, max: max + pad });
      } else {
        onEventClick(p.entry.id);
      }
    },
    [onEventClick],
  );

  const handleMouseDown = useCallback(
    (e: { activeLabel?: string }) => {
      if (e?.activeLabel) setBrushStart(Number(e.activeLabel));
    },
    [],
  );

  const handleMouseMove = useCallback(
    (e: { activeLabel?: string }) => {
      if (brushStart != null && e?.activeLabel) setBrushEnd(Number(e.activeLabel));
    },
    [brushStart],
  );

  const handleMouseUp = useCallback(() => {
    if (brushStart != null && brushEnd != null && brushStart !== brushEnd) {
      const min = Math.min(brushStart, brushEnd);
      const max = Math.max(brushStart, brushEnd);
      if (max - min > 100) setZoomRange({ min, max });
    }
    setBrushStart(null);
    setBrushEnd(null);
  }, [brushStart, brushEnd]);

  if (events.length === 0) {
    return <p className="py-8 text-center text-sm text-muted">No events to display.</p>;
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <TimelineLegend />
        <div className="flex items-center gap-2">
          <span className="text-[10px] text-muted">
            {visiblePoints.length} points
          </span>
          {isZoomed && (
            <Button variant="outline" size="sm" onClick={resetZoom}>
              <ZoomOut className="mr-1 h-3 w-3" />
              Reset
            </Button>
          )}
        </div>
      </div>

      <div
        ref={chartRef}
        className="rounded-xl border border-border bg-surface2/30 p-2"
        onWheel={handleWheel}
      >
        <ResponsiveContainer width="100%" height={280}>
          <ScatterChart
            margin={{ top: 20, right: 20, bottom: 20, left: 60 }}
            onMouseDown={handleMouseDown}
            onMouseMove={handleMouseMove}
            onMouseUp={handleMouseUp}
          >
            <XAxis
              type="number"
              dataKey="x"
              domain={[range.min, range.max]}
              tickFormatter={(v: number) => formatAxisTick(v, rangeMs)}
              tick={{ fontSize: 10, fill: "#888" }}
              tickCount={6}
              name="Time"
            />
            <YAxis
              type="number"
              dataKey="y"
              domain={[-0.5, 3.5]}
              ticks={[0, 1, 2, 3]}
              tickFormatter={(v: number) => CATEGORY_LABELS[v] ?? ""}
              tick={{ fontSize: 10, fill: "#888" }}
              width={50}
            />
            <Tooltip content={<TimelineTooltip />} cursor={false} />
            {brushStart != null && brushEnd != null && (
              <ReferenceArea
                x1={Math.min(brushStart, brushEnd)}
                x2={Math.max(brushStart, brushEnd)}
                fill="#6366f1"
                fillOpacity={0.1}
                stroke="#6366f1"
                strokeOpacity={0.3}
              />
            )}
            <Scatter data={visiblePoints} shape={<DotShape />} onClick={handleClick} />
          </ScatterChart>
        </ResponsiveContainer>
      </div>

      <p className="text-[10px] text-muted text-center">
        {events.length} total events{isZoomed && " (zoomed) · scroll to zoom · click clusters to expand"}
      </p>
    </div>
  );
}
