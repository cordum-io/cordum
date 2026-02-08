import { useState, useMemo, useCallback, useRef } from "react";
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
  ReferenceLine,
  Cell,
} from "recharts";
import { Download, Loader } from "lucide-react";
import { get } from "../../api/client";
import { Button } from "../ui/Button";
import { Card, CardHeader, CardTitle } from "../ui/Card";
import { cn } from "../../lib/utils";
import { POLICY_STATS_SUPPORTED, usePolicyAudit, usePolicyRules } from "../../hooks/usePolicies";
import { useJobs } from "../../hooks/useJobs";
import { exportPdf, captureElement, type PdfSection } from "../../lib/pdfExport";
import { useAuth } from "../../hooks/useAuth";

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
    enabled: POLICY_STATS_SUPPORTED,
    initialData: { decisions: [], topRules: [], latency: [] },
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

const REASON_PALETTE = [
  "#ef4444", "#f59e0b", "#8b5cf6", "#ec4899",
  "#06b6d4", "#84cc16", "#f97316", "#6366f1",
];

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

function buildDeniedReasonsCsv(data: { time: string; [reason: string]: string | number }[]): string {
  if (data.length === 0) return "";
  const keys = Object.keys(data[0]).filter((k) => k !== "time");
  const header = ["time", ...keys].join(",");
  const rows = data.map((d) => [d.time, ...keys.map((k) => d[k] ?? 0)].join(","));
  return [header, ...rows].join("\n");
}

function buildCoverageCsv(data: { topic: string; coverage: number }[]): string {
  const header = "topic,coverage_pct";
  const rows = data.map((d) => `${d.topic},${d.coverage.toFixed(1)}`);
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
// Denied by reason data builder
// ---------------------------------------------------------------------------

function buildDeniedByReasonData(
  auditEntries: { action: string; timestamp: string; details?: Record<string, unknown> }[],
  range: TimeRange,
): { data: { time: string; [reason: string]: string | number }[]; reasons: string[] } {
  // Filter deny actions
  const denyEntries = auditEntries.filter((e) => e.action.toLowerCase().includes("deny"));
  if (denyEntries.length === 0) return { data: [], reasons: [] };

  // Extract reasons
  const reasonCounts = new Map<string, number>();
  for (const entry of denyEntries) {
    const reason = (entry.details?.message as string) ?? "Unknown";
    reasonCounts.set(reason, (reasonCounts.get(reason) ?? 0) + 1);
  }

  // Top 8 reasons
  const sorted = [...reasonCounts.entries()].sort((a, b) => b[1] - a[1]);
  const topReasons = sorted.slice(0, 8).map(([r]) => r);
  const hasOther = sorted.length > 8;

  // Bucket by time
  const buckets = new Map<string, Record<string, number>>();
  const bucketMs = range === "1h" ? 5 * 60_000 : range === "24h" ? 60 * 60_000 : range === "7d" ? 24 * 60 * 60_000 : 7 * 24 * 60 * 60_000;

  for (const entry of denyEntries) {
    const t = new Date(entry.timestamp).getTime();
    const bucketKey = new Date(Math.floor(t / bucketMs) * bucketMs).toISOString();
    if (!buckets.has(bucketKey)) buckets.set(bucketKey, {});
    const bucket = buckets.get(bucketKey)!;
    const reason = (entry.details?.message as string) ?? "Unknown";
    const key = topReasons.includes(reason) ? reason : hasOther ? "Other" : reason;
    bucket[key] = (bucket[key] ?? 0) + 1;
  }

  const allReasons = hasOther ? [...topReasons, "Other"] : topReasons;
  const data = [...buckets.entries()]
    .sort((a, b) => a[0].localeCompare(b[0]))
    .map(([time, counts]) => {
      const point: { time: string; [reason: string]: string | number } = { time };
      for (const r of allReasons) point[r] = counts[r] ?? 0;
      return point;
    });

  return { data, reasons: allReasons };
}

// ---------------------------------------------------------------------------
// Coverage data builder
// ---------------------------------------------------------------------------

interface CoverageRow {
  topic: string;
  coverage: number;
  total: number;
  covered: number;
}

function buildCoverageData(
  jobs: { topic: string; safetyDecision?: { type: string } }[],
): { rows: CoverageRow[]; overall: number } {
  if (jobs.length === 0) return { rows: [], overall: 0 };

  const byTopic = new Map<string, { total: number; covered: number }>();
  for (const job of jobs) {
    const topic = job.topic || "unknown";
    const entry = byTopic.get(topic) ?? { total: 0, covered: 0 };
    entry.total++;
    if (job.safetyDecision) entry.covered++;
    byTopic.set(topic, entry);
  }

  const rows: CoverageRow[] = [...byTopic.entries()]
    .map(([topic, { total, covered }]) => ({
      topic,
      total,
      covered,
      coverage: total > 0 ? (covered / total) * 100 : 0,
    }))
    .sort((a, b) => b.total - a.total)
    .slice(0, 15);

  const totalJobs = jobs.length;
  const coveredJobs = jobs.filter((j) => j.safetyDecision).length;
  const overall = totalJobs > 0 ? (coveredJobs / totalJobs) * 100 : 0;

  return { rows, overall };
}

function coverageColor(pct: number): string {
  if (pct >= 80) return "#22c55e";
  if (pct >= 50) return "#f59e0b";
  return "#ef4444";
}

// ---------------------------------------------------------------------------
// PolicyAnalytics
// ---------------------------------------------------------------------------

export function PolicyAnalytics() {
  const [range, setRange] = useState<TimeRange>("24h");
  const { tenantId } = useAuth();
  const { data: stats, isLoading, isError } = usePolicyStats(range);

  // Chart refs for PDF capture
  const decisionsChartRef = useRef<HTMLDivElement>(null);
  const deniedChartRef = useRef<HTMLDivElement>(null);
  const coverageChartRef = useRef<HTMLDivElement>(null);
  const rulesChartRef = useRef<HTMLDivElement>(null);
  const latencyChartRef = useRef<HTMLDivElement>(null);

  const decisions = stats?.decisions ?? [];
  const topRules = stats?.topRules ?? [];
  const latency = stats?.latency ?? [];

  // Sort top rules by count descending, limit to 10
  const sortedRules = useMemo(
    () => [...topRules].sort((a, b) => b.count - a.count).slice(0, 10),
    [topRules],
  );

  // Denied by reason data
  const { data: auditData } = usePolicyAudit();
  const auditEntries = auditData?.items ?? [];
  const { data: deniedData, reasons: deniedReasons } = useMemo(
    () => buildDeniedByReasonData(auditEntries, range),
    [auditEntries, range],
  );

  // Coverage data
  usePolicyRules(); // ensure rules are cached
  const { data: jobsData } = useJobs({ limit: 100 });
  const recentJobs = jobsData?.items ?? [];
  const { rows: coverageRows, overall: overallCoverage } = useMemo(
    () => buildCoverageData(recentJobs),
    [recentJobs],
  );

  const handleExport = useCallback(() => {
    downloadCsv(`policy-decisions-${range}.csv`, buildDecisionsCsv(decisions));
    if (sortedRules.length > 0) {
      downloadCsv(`policy-top-rules-${range}.csv`, buildRulesCsv(sortedRules));
    }
    if (latency.length > 0) {
      downloadCsv(`policy-latency-${range}.csv`, buildLatencyCsv(latency));
    }
    if (deniedData.length > 0) {
      downloadCsv(`policy-denied-reasons-${range}.csv`, buildDeniedReasonsCsv(deniedData));
    }
    if (coverageRows.length > 0) {
      downloadCsv(`policy-rule-coverage-${range}.csv`, buildCoverageCsv(coverageRows));
    }
  }, [range, decisions, sortedRules, latency, deniedData, coverageRows]);

  const [pdfExporting, setPdfExporting] = useState(false);
  const handleExportPdf = useCallback(async () => {
    setPdfExporting(true);
    try {
      const sections: PdfSection[] = [];
      sections.push({ type: "heading", content: "Policy Analytics Report" });
      sections.push({ type: "text", content: `Time range: ${range}` });

      const refs = [
        { ref: decisionsChartRef, label: "Decisions Over Time" },
        { ref: deniedChartRef, label: "Denied By Reason" },
        { ref: coverageChartRef, label: "Rule Coverage" },
        { ref: rulesChartRef, label: "Most-Hit Rules" },
        { ref: latencyChartRef, label: "Eval Latency Trends" },
      ];

      for (const { ref, label } of refs) {
        if (ref.current) {
          sections.push({ type: "image", content: ref.current, label });
        }
      }

      await exportPdf({
        title: "Policy Analytics",
        tenantName: tenantId ?? undefined,
        sections,
      });
    } finally {
      setPdfExporting(false);
    }
  }, [range, tenantId]);

  if (!POLICY_STATS_SUPPORTED) {
    return (
      <div className="rounded-2xl border border-dashed border-border px-6 py-12 text-center text-sm text-muted">
        Policy analytics are not available in this deployment.
      </div>
    );
  }

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

        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={handleExport}>
            <Download className="h-3.5 w-3.5" />
            Export CSV
          </Button>
          <Button variant="outline" size="sm" onClick={handleExportPdf} disabled={pdfExporting}>
            <Download className="h-3.5 w-3.5" />
            {pdfExporting ? "Exporting…" : "Export PDF"}
          </Button>
        </div>
      </div>

      {/* Decisions over time */}
      <div ref={decisionsChartRef}>
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
      </div>

      {/* Denied by Reason */}
      <div ref={deniedChartRef}>
      <Card>
        <CardHeader>
          <CardTitle>Denied By Reason</CardTitle>
        </CardHeader>
        {deniedData.length === 0 ? (
          <p className="px-4 pb-4 text-sm text-muted">No deny data for this range.</p>
        ) : (
          <div style={{ width: "100%", height: 280 }}>
            <ResponsiveContainer width="100%" height="100%">
              <BarChart data={deniedData} margin={{ top: 8, right: 16, bottom: 8, left: 0 }}>
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
                <Legend wrapperStyle={{ fontSize: 11 }} />
                {deniedReasons.map((reason, i) => (
                  <Bar
                    key={reason}
                    dataKey={reason}
                    stackId="denied"
                    fill={REASON_PALETTE[i % REASON_PALETTE.length]}
                  />
                ))}
              </BarChart>
            </ResponsiveContainer>
          </div>
        )}
      </Card>
      </div>

      {/* Rule Coverage */}
      <div ref={coverageChartRef}>
      <Card>
        <CardHeader>
          <CardTitle>Rule Coverage</CardTitle>
          <span className="text-xs text-muted">% of recent jobs evaluated by policy</span>
        </CardHeader>
        {coverageRows.length === 0 ? (
          <p className="px-4 pb-4 text-sm text-muted">No job data to compute coverage.</p>
        ) : (
          <>
            {/* Overall metric */}
            <div className="px-4 pb-3">
              <div className="flex items-baseline gap-2">
                <span
                  className="text-3xl font-bold"
                  style={{ color: coverageColor(overallCoverage) }}
                >
                  {overallCoverage.toFixed(0)}%
                </span>
                <span className="text-sm text-muted">overall coverage</span>
              </div>
            </div>
            <div style={{ width: "100%", height: Math.max(coverageRows.length * 32 + 40, 160) }}>
              <ResponsiveContainer width="100%" height="100%">
                <BarChart
                  data={coverageRows}
                  layout="vertical"
                  margin={{ top: 8, right: 24, bottom: 8, left: 8 }}
                >
                  <CartesianGrid strokeDasharray="3 3" stroke="#e2e8f0" horizontal={false} />
                  <XAxis
                    type="number"
                    domain={[0, 100]}
                    tick={{ fontSize: 10, fill: "#94a3b8" }}
                    axisLine={false}
                    tickLine={false}
                    tickFormatter={(v: number) => `${v}%`}
                  />
                  <YAxis
                    type="category"
                    dataKey="topic"
                    width={140}
                    tick={{ fontSize: 11, fill: "#1e293b" }}
                    axisLine={false}
                    tickLine={false}
                  />
                  <Tooltip
                    content={<ChartTooltip valueFormatter={(v) => `${v.toFixed(1)}%`} />}
                    cursor={{ fill: "rgba(0,0,0,0.03)" }}
                  />
                  <Bar dataKey="coverage" radius={[0, 4, 4, 0]} barSize={20}>
                    {coverageRows.map((row) => (
                      <Cell key={row.topic} fill={coverageColor(row.coverage)} />
                    ))}
                  </Bar>
                </BarChart>
              </ResponsiveContainer>
            </div>
          </>
        )}
      </Card>
      </div>

      {/* Most-hit rules */}
      <div ref={rulesChartRef}>
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
      </div>

      {/* Eval latency trends (enhanced with SLA line) */}
      <div ref={latencyChartRef}>
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
                <ReferenceLine
                  y={100}
                  stroke="#ef4444"
                  strokeDasharray="5 5"
                  label={{ value: "SLA 100ms", position: "right", fill: "#ef4444", fontSize: 10 }}
                />
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
    </div>
  );
}
