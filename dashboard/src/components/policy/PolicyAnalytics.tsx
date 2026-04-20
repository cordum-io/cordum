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
import {
  POLICY_STATS_SUPPORTED,
  usePolicyAudit,
  usePolicyBundles,
  usePolicyRules,
} from "../../hooks/usePolicies";
import { useJobs } from "../../hooks/useJobs";
import { exportPdf, type PdfSection } from "../../lib/pdfExport";
import { buildComplianceReportData } from "../../lib/compliance-report";
import {
  CompliancePdfExportError,
  exportCompliancePdf,
  type CompliancePdfProgress,
} from "../../lib/exportCompliancePdf";
import { useAuth } from "../../hooks/useAuth";
import { chartColors, tooltipProps, axisProps, gridProps } from "../../lib/chart-theme";

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
  allow: "var(--success)",
  deny: "var(--danger)",
  require_approval: "var(--warning)",
  throttle: "var(--info)",
};

const LATENCY_COLORS: Record<string, string> = {
  p50: "var(--chart-1)",
  p95: "var(--chart-3)",
  p99: "var(--chart-4)",
};

const REASON_PALETTE = chartColors;

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

function describeCompliancePdfProgress(progress: CompliancePdfProgress): string {
  if (progress.phase === "preparing") return "Preparing compliance report...";
  if (progress.phase === "rendering") {
    return `Rendering page ${progress.currentPage} of ${progress.totalPages}...`;
  }
  return "Saving compliance report...";
}

function compliancePdfErrorMessage(error: unknown): string {
  if (error instanceof CompliancePdfExportError) {
    if (error.code === "invalid_data") {
      return "Compliance report data is incomplete. Refresh policy data and try again.";
    }
    if (error.code === "render_timeout") {
      return "Compliance PDF rendering timed out. Try again with a smaller time range.";
    }
    return "Failed to render compliance PDF. Please retry.";
  }
  return "Failed to export compliance PDF. Please try again.";
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
    <div className="rounded-md border border-border bg-surface-2 px-3 py-2 shadow-lg text-[11px] space-y-1">
      {label && <p className="font-bold uppercase tracking-wider text-muted mb-1">{formatTime(label)}</p>}
      {payload.map((entry) => (
        <div key={entry.name} className="flex items-center justify-between gap-4">
          <div className="flex items-center gap-2">
            <span
              className="inline-block h-2 w-2 rounded-sm"
              style={{ background: entry.color }}
            />
            <span className="text-muted font-medium">{entry.name}:</span>
          </div>
          <span className="font-mono font-bold text-ink">{fmt(entry.value)}</span>
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
  if (pct >= 80) return "var(--success)";
  if (pct >= 50) return "var(--warning)";
  return "var(--danger)";
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

  // Coverage + compliance export data
  const {
    data: policyRulesData,
    isLoading: isLoadingPolicyRules,
    isError: isPolicyRulesError,
  } = usePolicyRules();
  const { data: policyBundlesData } = usePolicyBundles();

  const policyRules = policyRulesData?.items ?? [];
  const { data: jobsData } = useJobs({ limit: 100 });
  const recentJobs = jobsData?.items ?? [];
  const { rows: coverageRows, overall: overallCoverage } = useMemo(
    () => buildCoverageData(recentJobs),
    [recentJobs],
  );

  const bundleVersionLabel = useMemo(() => {
    const versions = (policyBundlesData?.items ?? [])
      .map((bundle) => bundle.version)
      .filter((version): version is number => typeof version === "number" && Number.isFinite(version));
    if (versions.length === 0) return undefined;
    return `v${Math.max(...versions)}`;
  }, [policyBundlesData]);

  const complianceReportData = useMemo(
    () =>
      buildComplianceReportData(policyRules, {
        organizationName: tenantId ?? undefined,
        bundleVersion: bundleVersionLabel,
      }),
    [policyRules, tenantId, bundleVersionLabel],
  );

  const complianceDataFallbackMessage = useMemo(() => {
    if (isLoadingPolicyRules) return "Loading policy rules for compliance export...";
    if (isPolicyRulesError) return "Compliance export is unavailable because policy rules failed to load.";
    if (policyRules.length === 0) return "Compliance export requires at least one policy rule.";
    return null;
  }, [isLoadingPolicyRules, isPolicyRulesError, policyRules.length]);

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

  const [complianceExporting, setComplianceExporting] = useState(false);
  const [complianceExportProgress, setComplianceExportProgress] = useState<string | null>(null);
  const [complianceExportError, setComplianceExportError] = useState<string | null>(null);

  const handleExportCompliancePdf = useCallback(async () => {
    setComplianceExportError(null);

    if (complianceDataFallbackMessage) {
      setComplianceExportProgress(null);
      setComplianceExportError(complianceDataFallbackMessage);
      return;
    }

    setComplianceExporting(true);
    setComplianceExportProgress("Preparing compliance report...");

    try {
      await exportCompliancePdf({
        report: complianceReportData,
        filename: `cordum-policy-compliance-${range}`,
        onProgress: (progress) => {
          setComplianceExportProgress(describeCompliancePdfProgress(progress));
        },
      });
      setComplianceExportProgress("Compliance report exported successfully.");
    } catch (error) {
      setComplianceExportProgress(null);
      setComplianceExportError(compliancePdfErrorMessage(error));
    } finally {
      setComplianceExporting(false);
    }
  }, [complianceDataFallbackMessage, complianceReportData, range]);

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

  const complianceFeedbackMessage =
    complianceExportError ?? complianceExportProgress ?? complianceDataFallbackMessage;

  return (
    <div className="space-y-6">
      {/* Controls */}
      <div className="space-y-2">
        <div className="flex items-center justify-between gap-4">
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
            <Button
              variant="outline"
              size="sm"
              onClick={handleExportCompliancePdf}
              disabled={complianceExporting || Boolean(complianceDataFallbackMessage)}
            >
              {complianceExporting ? (
                <Loader className="h-3.5 w-3.5 animate-spin" />
              ) : (
                <Download className="h-3.5 w-3.5" />
              )}
              {complianceExporting ? "Exporting…" : "Export Compliance PDF"}
            </Button>
          </div>
        </div>

        {complianceFeedbackMessage && (
          <p
            className={cn(
              "text-xs",
              complianceExportError ? "text-danger" : "text-muted",
            )}
            aria-live="polite"
          >
            {complianceFeedbackMessage}
          </p>
        )}
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
                <CartesianGrid {...gridProps} />
                <XAxis
                  dataKey="time"
                  tickFormatter={formatTime}
                  {...axisProps}
                />
                <YAxis
                  {...axisProps}
                />
                <Tooltip {...tooltipProps} content={<ChartTooltip />} />
                <Legend
                  wrapperStyle={{ fontSize: 10, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.05em' }}
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
                <CartesianGrid {...gridProps} />
                <XAxis
                  dataKey="time"
                  tickFormatter={formatTime}
                  {...axisProps}
                />
                <YAxis
                  {...axisProps}
                />
                <Tooltip {...tooltipProps} content={<ChartTooltip />} />
                <Legend wrapperStyle={{ fontSize: 10, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.05em' }} />
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
                  <CartesianGrid {...gridProps} horizontal={false} vertical={true} />
                  <XAxis
                    type="number"
                    domain={[0, 100]}
                    {...axisProps}
                    tickFormatter={(v: number) => `${v}%`}
                  />
                  <YAxis
                    type="category"
                    dataKey="topic"
                    width={140}
                    {...axisProps}
                    tick={{ ...axisProps.tick, fill: "var(--text)" }}
                  />
                  <Tooltip
                    {...tooltipProps}
                    content={<ChartTooltip valueFormatter={(v) => `${v.toFixed(1)}%`} />}
                    cursor={{ fill: "var(--surface-2)", opacity: 0.4 }}
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
                <CartesianGrid {...gridProps} horizontal={false} vertical={true} />
                <XAxis
                  type="number"
                  {...axisProps}
                />
                <YAxis
                  type="category"
                  dataKey="ruleName"
                  width={140}
                  {...axisProps}
                  tick={{ ...axisProps.tick, fill: "var(--text)" }}
                />
                <Tooltip
                  {...tooltipProps}
                  content={<ChartTooltip />}
                  cursor={{ fill: "var(--surface-2)", opacity: 0.4 }}
                />
                <Bar dataKey="count" fill="var(--chart-1)" radius={[0, 4, 4, 0]} barSize={20} />
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
                <CartesianGrid {...gridProps} />
                <XAxis
                  dataKey="time"
                  tickFormatter={formatTime}
                  {...axisProps}
                />
                <YAxis
                  tickFormatter={(v: number) => formatMs(v)}
                  {...axisProps}
                />
                <Tooltip {...tooltipProps} content={<ChartTooltip valueFormatter={formatMs} />} />
                <Legend wrapperStyle={{ fontSize: 10, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.05em' }} />
                <ReferenceLine
                  y={100}
                  stroke="var(--danger)"
                  strokeDasharray="5 5"
                  label={{ value: "SLA 100ms", position: "right", fill: "var(--danger)", fontSize: 10, fontWeight: 700 }}
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
