import { useState, useMemo, useCallback } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  AreaChart,
  Area,
  BarChart,
  Bar,
  LineChart,
  Line,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
  CartesianGrid,
  Legend,
} from "recharts";
import { Download, Loader } from "lucide-react";
import { get } from "../../api/client";
import { Button } from "../ui/Button";
import { Card, CardHeader, CardTitle } from "../ui/Card";
import { cn } from "../../lib/utils";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type TimeRange = "1h" | "24h" | "7d" | "30d";

interface DecisionPoint {
  time: string;
  allow: number;
  deny: number;
  require_approval: number;
  throttle: number;
}

interface RuleHit {
  ruleId: string;
  ruleName?: string;
  count: number;
}

interface LatencyPoint {
  time: string;
  p50: number;
  p95: number;
  p99: number;
}

interface PolicyStats {
  decisions: DecisionPoint[];
  topRules: RuleHit[];
  latency: LatencyPoint[];
}

// ---------------------------------------------------------------------------
// Hook
// ---------------------------------------------------------------------------

function usePolicyStats(range: TimeRange) {
  return useQuery<PolicyStats>({
    queryKey: ["policy-stats", range],
    queryFn: () => get<PolicyStats>(`/policy/stats?range=${range}`),
    staleTime: 30_000,
  });
}

// ---------------------------------------------------------------------------
// Time range selector
// ---------------------------------------------------------------------------

const RANGES: { value: TimeRange; label: string }[] = [
  { value: "1h", label: "1h" },
  { value: "24h", label: "24h" },
  { value: "7d", label: "7d" },
  { value: "30d", label: "30d" },
];

// ---------------------------------------------------------------------------
// Chart colors
// ---------------------------------------------------------------------------

const DECISION_COLORS: Record<string, string> = {
  allow: "#22c55e",
  deny: "#ef4444",
  require_approval: "#f59e0b",
  throttle: "#6366f1",
};

const LATENCY_COLORS: Record<string, string> = {
  p50: "#3b82f6",
  p95: "#f59e0b",
  p99: "#ef4444",
};

// ---------------------------------------------------------------------------
// CSV export
// ---------------------------------------------------------------------------

function downloadCsv(filename: string, csvContent: string) {
  const blob = new Blob([csvContent], { type: "text/csv;charset=utf-8;" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
}

function buildDecisionsCsv(data: DecisionPoint[]): string {
  const header = "time,allow,deny,require_approval,throttle";
  const rows = data.map(
    (d) => `${d.time},${d.allow},${d.deny},${d.require_approval},${d.throttle}`,
  );
  return [header, ...rows].join("\n");
}

function buildRulesCsv(data: RuleHit[]): string {
  const header = "rule_id,rule_name,hit_count";
  const rows = data.map((r) => `${r.ruleId},${r.ruleName ?? ""},${r.count}`);
  return [header, ...rows].join("\n");
}

function buildLatencyCsv(data: LatencyPoint[]): string {
  const header = "time,p50_ms,p95_ms,p99_ms";
  const rows = data.map((l) => `${l.time},${l.p50},${l.p95},${l.p99}`);
  return [header, ...rows].join("\n");
}

// ---------------------------------------------------------------------------
// Format helpers
// ---------------------------------------------------------------------------

function formatTime(iso: string): string {
  try {
    const d = new Date(iso);
    return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
  } catch {
    return iso;
  }
}

function formatMs(ms: number): string {
  if (ms < 1) return `${(ms * 1000).toFixed(0)}us`;
  if (ms < 1000) return `${ms.toFixed(0)}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

// ---------------------------------------------------------------------------
// Custom tooltip
// ---------------------------------------------------------------------------

interface TooltipEntry {
  name: string;
  value: number;
  color: string;
}

function ChartTooltip({
  active,
  payload,
  label,
  valueFormatter,
}: {
  active?: boolean;
  payload?: TooltipEntry[];
  label?: string;
  valueFormatter?: (v: number) => string;
}) {
  if (!active || !payload?.length) return null;
  const fmt = valueFormatter ?? String;
  return (
    <div className="rounded-xl border border-border bg-white px-3 py-2 shadow-lg text-xs space-y-1">
      {label && <p className="font-semibold text-ink">{formatTime(label)}</p>}
      {payload.map((entry) => (
        <div key={entry.name} className="flex items-center gap-2">
          <span
            className="inline-block h-2 w-2 rounded-full"
            style={{ background: entry.color }}
          />
          <span className="text-muted">{entry.name}:</span>
          <span className="font-medium text-ink">{fmt(entry.value)}</span>
        </div>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// PolicyAnalytics
// ---------------------------------------------------------------------------

export function PolicyAnalytics() {
  const [range, setRange] = useState<TimeRange>("24h");
  const { data: stats, isLoading, isError } = usePolicyStats(range);

  const decisions = stats?.decisions ?? [];
  const topRules = stats?.topRules ?? [];
  const latency = stats?.latency ?? [];

  // Sort top rules by count descending, limit to 10
  const sortedRules = useMemo(
    () => [...topRules].sort((a, b) => b.count - a.count).slice(0, 10),
    [topRules],
  );

  const handleExport = useCallback(() => {
    downloadCsv(`policy-decisions-${range}.csv`, buildDecisionsCsv(decisions));
    if (sortedRules.length > 0) {
      downloadCsv(`policy-top-rules-${range}.csv`, buildRulesCsv(sortedRules));
    }
    if (latency.length > 0) {
      downloadCsv(`policy-latency-${range}.csv`, buildLatencyCsv(latency));
    }
  }, [range, decisions, sortedRules, latency]);

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-16 text-sm text-muted">
        <Loader className="mr-2 h-4 w-4 animate-spin" />
        Loading analytics...
      </div>
    );
  }

  if (isError) {
    return (
      <div className="py-16 text-center text-sm text-danger">
        Failed to load policy analytics.
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Controls */}
      <div className="flex items-center justify-between">
        <div className="flex rounded-full border border-border">
          {RANGES.map((r) => (
            <button
              key={r.value}
              type="button"
              className={cn(
                "px-4 py-1.5 text-xs font-semibold transition first:rounded-l-full last:rounded-r-full",
                range === r.value
                  ? "bg-accent/15 text-accent"
                  : "text-muted hover:text-ink",
              )}
              onClick={() => setRange(r.value)}
            >
              {r.label}
            </button>
          ))}
        </div>

        <Button variant="outline" size="sm" onClick={handleExport}>
          <Download className="h-3.5 w-3.5" />
          Export CSV
        </Button>
      </div>

      {/* Decisions over time */}
      <Card>
        <CardHeader>
          <CardTitle>Decisions Over Time</CardTitle>
        </CardHeader>
        {decisions.length === 0 ? (
          <p className="px-4 pb-4 text-sm text-muted">No decision data for this range.</p>
        ) : (
          <div style={{ width: "100%", height: 280 }}>
            <ResponsiveContainer width="100%" height="100%">
              <AreaChart data={decisions} margin={{ top: 8, right: 16, bottom: 8, left: 0 }}>
                <CartesianGrid strokeDasharray="3 3" stroke="#e2e8f0" />
                <XAxis
                  dataKey="time"
                  tickFormatter={formatTime}
                  tick={{ fontSize: 10, fill: "#94a3b8" }}
                  axisLine={false}
                  tickLine={false}
                />
                <YAxis
                  tick={{ fontSize: 10, fill: "#94a3b8" }}
                  axisLine={false}
                  tickLine={false}
                />
                <Tooltip content={<ChartTooltip />} />
                <Legend
                  wrapperStyle={{ fontSize: 11 }}
                  formatter={(value: string) => value.replace(/_/g, " ")}
                />
                <Area
                  type="monotone"
                  dataKey="allow"
                  stackId="1"
                  stroke={DECISION_COLORS.allow}
                  fill={DECISION_COLORS.allow}
                  fillOpacity={0.6}
                />
                <Area
                  type="monotone"
                  dataKey="deny"
                  stackId="1"
                  stroke={DECISION_COLORS.deny}
                  fill={DECISION_COLORS.deny}
                  fillOpacity={0.6}
                />
                <Area
                  type="monotone"
                  dataKey="require_approval"
                  stackId="1"
                  stroke={DECISION_COLORS.require_approval}
                  fill={DECISION_COLORS.require_approval}
                  fillOpacity={0.6}
                />
                <Area
                  type="monotone"
                  dataKey="throttle"
                  stackId="1"
                  stroke={DECISION_COLORS.throttle}
                  fill={DECISION_COLORS.throttle}
                  fillOpacity={0.6}
                />
              </AreaChart>
            </ResponsiveContainer>
          </div>
        )}
      </Card>

      {/* Most-hit rules */}
      <Card>
        <CardHeader>
          <CardTitle>Most-Hit Rules</CardTitle>
          <span className="text-xs text-muted">Top 10</span>
        </CardHeader>
        {sortedRules.length === 0 ? (
          <p className="px-4 pb-4 text-sm text-muted">No rule hit data for this range.</p>
        ) : (
          <div style={{ width: "100%", height: Math.max(sortedRules.length * 36 + 40, 160) }}>
            <ResponsiveContainer width="100%" height="100%">
              <BarChart
                data={sortedRules}
                layout="vertical"
                margin={{ top: 8, right: 24, bottom: 8, left: 8 }}
              >
                <CartesianGrid strokeDasharray="3 3" stroke="#e2e8f0" horizontal={false} />
                <XAxis
                  type="number"
                  tick={{ fontSize: 10, fill: "#94a3b8" }}
                  axisLine={false}
                  tickLine={false}
                />
                <YAxis
                  type="category"
                  dataKey="ruleName"
                  width={140}
                  tick={{ fontSize: 11, fill: "#1e293b" }}
                  axisLine={false}
                  tickLine={false}
                />
                <Tooltip
                  content={<ChartTooltip />}
                  cursor={{ fill: "rgba(0,0,0,0.03)" }}
                />
                <Bar dataKey="count" fill="#6366f1" radius={[0, 4, 4, 0]} barSize={20} />
              </BarChart>
            </ResponsiveContainer>
          </div>
        )}
      </Card>

      {/* Eval latency trends */}
      <Card>
        <CardHeader>
          <CardTitle>Eval Latency Trends</CardTitle>
        </CardHeader>
        {latency.length === 0 ? (
          <p className="px-4 pb-4 text-sm text-muted">No latency data for this range.</p>
        ) : (
          <div style={{ width: "100%", height: 280 }}>
            <ResponsiveContainer width="100%" height="100%">
              <LineChart data={latency} margin={{ top: 8, right: 16, bottom: 8, left: 0 }}>
                <CartesianGrid strokeDasharray="3 3" stroke="#e2e8f0" />
                <XAxis
                  dataKey="time"
                  tickFormatter={formatTime}
                  tick={{ fontSize: 10, fill: "#94a3b8" }}
                  axisLine={false}
                  tickLine={false}
                />
                <YAxis
                  tickFormatter={(v: number) => formatMs(v)}
                  tick={{ fontSize: 10, fill: "#94a3b8" }}
                  axisLine={false}
                  tickLine={false}
                />
                <Tooltip content={<ChartTooltip valueFormatter={formatMs} />} />
                <Legend wrapperStyle={{ fontSize: 11 }} />
                <Line
                  type="monotone"
                  dataKey="p50"
                  stroke={LATENCY_COLORS.p50}
                  strokeWidth={2}
                  dot={false}
                />
                <Line
                  type="monotone"
                  dataKey="p95"
                  stroke={LATENCY_COLORS.p95}
                  strokeWidth={2}
                  dot={false}
                />
                <Line
                  type="monotone"
                  dataKey="p99"
                  stroke={LATENCY_COLORS.p99}
                  strokeWidth={2}
                  dot={false}
                />
              </LineChart>
            </ResponsiveContainer>
          </div>
        )}
      </Card>
    </div>
  );
}
