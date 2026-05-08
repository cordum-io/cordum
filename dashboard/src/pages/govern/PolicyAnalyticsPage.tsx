/*
 * DESIGN: "Rule Quality Forensics" — Policy Analytics Dashboard.
 * Identifies false positives (approval fatigue) and rule quality issues.
 * Single-column: controls at top, rule table, fatigue chart, FP highlights.
 */
import { useState, useMemo, useCallback } from "react";
import { Link } from "react-router-dom";
import { motion } from "framer-motion";
import {
  TrendingUp,
  Play,
  AlertTriangle,
  Search,
  ExternalLink,
  BarChart3,
} from "lucide-react";
import {
  AreaChart,
  Area,
  ResponsiveContainer,
  XAxis,
  YAxis,
  Tooltip,
  CartesianGrid,
} from "recharts";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { InfoBanner } from "@/components/ui/InfoBanner";
import { InstrumentCard } from "@/components/ui/InstrumentCard";
import { Input } from "@/components/ui/Input";
import { LabeledField } from "@/components/ui/LabeledField";
import { ChartTooltipCompact as ChartTooltip } from "@/components/ui/ChartTooltip";
import { usePolicyAnalytics } from "@/hooks/usePolicies";
import type {
  PolicyAnalyticsRequest,
  RuleAnalytics,
} from "@/api/types";

// ---------------------------------------------------------------------------
// Exported constants for testing
// ---------------------------------------------------------------------------

export const ANALYTICS_PAGE_SECTIONS = [
  "controls",
  "rule-table",
  "fatigue-chart",
  "fp-highlights",
] as const;

export const OVERRIDE_WARNING_THRESHOLD = 0.5;
export const MAX_ANALYTICS_RANGE_DAYS = 7;
const REPLAY_COMPARE_LINK =
  "/govern/overview?tab=evaluation&mode=replay&topic=*&decision=REQUIRE_APPROVAL";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function toISODatetimeLocal(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function defaultFrom(): string {
  const d = new Date();
  d.setDate(d.getDate() - 7);
  return toISODatetimeLocal(d);
}

function defaultTo(): string {
  return toISODatetimeLocal(new Date());
}

export function validateAnalyticsRange(from: string, to: string): string | null {
  if (!from || !to) return "Both from and to dates are required.";
  const f = new Date(from);
  const t = new Date(to);
  if (isNaN(f.getTime()) || isNaN(t.getTime())) return "Invalid date format.";
  if (f >= t) return "Start date must be before end date.";
  const days = (t.getTime() - f.getTime()) / (1000 * 60 * 60 * 24);
  if (days > MAX_ANALYTICS_RANGE_DAYS)
    return `Time range cannot exceed ${MAX_ANALYTICS_RANGE_DAYS} days.`;
  return null;
}

function formatLatency(ms: number): string {
  if (ms <= 0) return "—";
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`;
  return `${Math.floor(ms / 60_000)}m ${Math.floor((ms % 60_000) / 1000)}s`;
}

function formatRate(rate: number): string {
  return `${(rate * 100).toFixed(1)}%`;
}

// ---------------------------------------------------------------------------
// Sparkline: tiny inline bar chart for daily hits
// ---------------------------------------------------------------------------

function Sparkline({ data }: { data: number[] }) {
  if (!data || data.length === 0) return <span className="text-muted-foreground">—</span>;
  const max = Math.max(...data, 1);
  return (
    <svg
      width={data.length * 6}
      height={18}
      viewBox={`0 0 ${data.length * 6} 18`}
      className="inline-block align-middle"
      aria-label="Daily hits trend"
    >
      {data.map((v, i) => {
        const h = Math.max((v / max) * 16, 1);
        return (
          <rect
            key={i}
            x={i * 6}
            y={18 - h}
            width={4}
            height={h}
            rx={1}
            className="fill-cordum"
            opacity={0.7 + 0.3 * (v / max)}
          />
        );
      })}
    </svg>
  );
}

// ---------------------------------------------------------------------------
// Rule quality table
// ---------------------------------------------------------------------------

type SortField = "rule_id" | "hit_count" | "override_rate" | "avg_approval_latency_ms";
type SortDir = "asc" | "desc";

function RuleTable({ rules }: { rules: RuleAnalytics[] }) {
  const [search, setSearch] = useState("");
  const [sortField, setSortField] = useState<SortField>("override_rate");
  const [sortDir, setSortDir] = useState<SortDir>("desc");

  const filtered = useMemo(() => {
    let items = rules;
    if (search.trim()) {
      const q = search.toLowerCase();
      items = items.filter((r) => r.rule_id.toLowerCase().includes(q));
    }
    return [...items].sort((a, b) => {
      const av = a[sortField] ?? 0;
      const bv = b[sortField] ?? 0;
      const cmp = av < bv ? -1 : av > bv ? 1 : 0;
      return sortDir === "asc" ? cmp : -cmp;
    });
  }, [rules, search, sortField, sortDir]);

  const toggleSort = useCallback(
    (field: SortField) => {
      if (sortField === field) {
        setSortDir(sortDir === "asc" ? "desc" : "asc");
      } else {
        setSortField(field);
        setSortDir("desc");
      }
    },
    [sortField, sortDir],
  );

  if (rules.length === 0) {
    return (
      <EmptyState
        icon={<BarChart3 className="h-8 w-8" />}
        title="No rule activity"
        description="No rules matched during the selected time range."
      />
    );
  }

  const columns: { field: SortField; label: string }[] = [
    { field: "rule_id", label: "Rule ID" },
    { field: "hit_count", label: "Hits (7d)" },
    { field: "override_rate", label: "Override Rate" },
    { field: "avg_approval_latency_ms", label: "Avg Latency" },
  ];

  return (
    <div className="space-y-3">
      <div className="relative max-w-sm">
        <Input
          type="text"
          icon={<Search className="h-3.5 w-3.5" />}
          placeholder="Search rule ID..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          aria-label="Search rules"
        />
      </div>
      <div className="overflow-x-auto rounded-xl border border-border/60">
        <table className="w-full text-sm" role="table">
          <thead>
            <tr className="border-b border-border/40 bg-muted/20">
              {columns.map((col) => (
                <th
                  key={col.field}
                  className="text-left py-2.5 px-3 text-xs font-medium text-muted-foreground uppercase tracking-wider cursor-pointer select-none hover:text-foreground"
                  onClick={() => toggleSort(col.field)}
                >
                  {col.label}
                  {sortField === col.field && (
                    <span className="ml-1">
                      {sortDir === "asc" ? "\u25B2" : "\u25BC"}
                    </span>
                  )}
                </th>
              ))}
              <th className="text-left py-2.5 px-3 text-xs font-medium text-muted-foreground uppercase tracking-wider">
                Approvals
              </th>
              <th className="text-left py-2.5 px-3 text-xs font-medium text-muted-foreground uppercase tracking-wider">
                Overrides
              </th>
              <th className="text-left py-2.5 px-3 text-xs font-medium text-muted-foreground uppercase tracking-wider">
                Trend
              </th>
              <th className="text-right py-2.5 px-3 text-xs font-medium text-muted-foreground uppercase tracking-wider">
                Action
              </th>
            </tr>
          </thead>
          <tbody>
            {filtered.map((rule) => {
              const highOverride = rule.override_rate >= OVERRIDE_WARNING_THRESHOLD;
              return (
                <tr
                  key={rule.rule_id}
                  className={`border-b border-border/20 last:border-0 hover:bg-muted/10 ${
                    highOverride ? "bg-warning/5" : ""
                  }`}
                >
                  <td className="py-2 px-3 font-mono text-xs text-foreground/90">
                    {rule.rule_id}
                    {highOverride && (
                      <AlertTriangle className="inline h-3 w-3 ml-1.5 text-warning" />
                    )}
                  </td>
                  <td className="py-2 px-3 tabular-nums">{rule.hit_count}</td>
                  <td className="py-2 px-3">
                    <StatusBadge
                      variant={
                        highOverride
                          ? "warning"
                          : rule.override_rate > 0.2
                            ? "info"
                            : "muted"
                      }
                    >
                      {formatRate(rule.override_rate)}
                    </StatusBadge>
                  </td>
                  <td className="py-2 px-3 text-muted-foreground tabular-nums">
                    {formatLatency(rule.avg_approval_latency_ms)}
                  </td>
                  <td className="py-2 px-3 tabular-nums">{rule.approval_count}</td>
                  <td className="py-2 px-3 tabular-nums">{rule.override_count}</td>
                  <td className="py-2 px-3">
                    <Sparkline data={rule.daily_hits} />
                  </td>
                  <td className="py-2 px-3 text-right">
                    <Link
                      to={REPLAY_COMPARE_LINK}
                      className="inline-flex items-center gap-1 text-xs text-cordum hover:underline"
                    >
                      What-if
                      <ExternalLink className="h-3 w-3" />
                    </Link>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      <p className="text-xs text-muted-foreground">
        Showing {filtered.length} of {rules.length} rules
      </p>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Approval fatigue chart
// ---------------------------------------------------------------------------

function ApprovalFatigueChart({ rules }: { rules: RuleAnalytics[] }) {
  const chartRules = rules
    .filter((r) => r.approval_count > 0)
    .slice(0, 10); // Top 10 by hits (already sorted)

  if (chartRules.length === 0) {
    return (
      <EmptyState
        icon={<TrendingUp className="h-8 w-8" />}
        title="No approval activity"
        description="No rules triggered approvals in this period."
      />
    );
  }

  // Build daily series data.
  const numDays = chartRules[0]?.daily_hits?.length ?? 7;
  const data = Array.from({ length: numDays }, (_, dayIdx) => {
    const point: Record<string, number | string> = { day: `Day ${dayIdx + 1}` };
    for (const rule of chartRules) {
      point[rule.rule_id] = (rule.daily_hits ?? [])[dayIdx] ?? 0;
    }
    return point;
  });

  const colors = ["#0f7f7a", "#f59e0b", "#ef4444", "#8b5cf6", "#06b6d4", "#10b981", "#ec4899", "#6366f1", "#84cc16", "#f97316"];

  return (
    <ResponsiveContainer width="100%" height={280}>
      <AreaChart data={data}>
        <CartesianGrid strokeDasharray="3 3" stroke="rgba(255,255,255,0.04)" />
        <XAxis dataKey="day" tick={{ fontSize: 10, fill: "#5a6a70" }} axisLine={false} tickLine={false} />
        <YAxis tick={{ fontSize: 10, fill: "#5a6a70" }} axisLine={false} tickLine={false} />
        <Tooltip content={<ChartTooltip />} />
        {chartRules.map((rule, i) => (
          <Area
            key={rule.rule_id}
            type="monotone"
            dataKey={rule.rule_id}
            stackId="1"
            stroke={colors[i % colors.length]}
            fill={colors[i % colors.length]}
            fillOpacity={0.3}
          />
        ))}
      </AreaChart>
    </ResponsiveContainer>
  );
}

// ---------------------------------------------------------------------------
// False positive highlights
// ---------------------------------------------------------------------------

function FalsePositiveHighlights({ rules }: { rules: RuleAnalytics[] }) {
  const fpRules = rules.filter((r) => r.override_rate >= OVERRIDE_WARNING_THRESHOLD);

  if (fpRules.length === 0) {
    return (
      <InfoBanner variant="success">
        No rules with high override rates. Your policy looks well-tuned.
      </InfoBanner>
    );
  }

  return (
    <div className="space-y-3">
      {fpRules.map((rule) => (
        <div
          key={rule.rule_id}
          className="rounded-xl border border-warning/30 bg-warning/5 p-4 space-y-2"
        >
          <div className="flex items-start justify-between gap-3">
            <div className="flex items-center gap-2">
              <AlertTriangle className="h-4 w-4 text-warning shrink-0" />
              <span className="font-mono text-sm font-medium">{rule.rule_id}</span>
              <StatusBadge variant="warning">
                {formatRate(rule.override_rate)} override rate
              </StatusBadge>
            </div>
            <Link
              to={REPLAY_COMPARE_LINK}
              className="text-xs text-cordum hover:underline shrink-0"
            >
              Test with replay &rarr;
            </Link>
          </div>
          <p className="text-xs text-muted-foreground">
            This rule may be too aggressive — humans override it{" "}
            {formatRate(rule.override_rate)} of the time ({rule.override_count} of{" "}
            {rule.approval_count} approvals). Average approval latency:{" "}
            {formatLatency(rule.avg_approval_latency_ms)}.
          </p>
        </div>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Main page
// ---------------------------------------------------------------------------

export default function PolicyAnalyticsPage({
  hideHeader,
}: {
  hideHeader?: boolean;
} = {}) {
  const analyticsMutation = usePolicyAnalytics();
  const result = analyticsMutation.data;

  const [from, setFrom] = useState(defaultFrom());
  const [to, setTo] = useState(defaultTo());

  const rangeError = useMemo(() => validateAnalyticsRange(from, to), [from, to]);
  const canSubmit = !rangeError && !analyticsMutation.isPending;

  const handleSubmit = useCallback(() => {
    if (!canSubmit) return;
    const req: PolicyAnalyticsRequest = {
      from: new Date(from).toISOString(),
      to: new Date(to).toISOString(),
    };
    analyticsMutation.mutate(req);
  }, [canSubmit, from, to, analyticsMutation]);

  const content = (
    <div className={hideHeader ? "space-y-6" : "max-w-6xl mx-auto w-full px-4 py-6 space-y-6"}>
        {/* Controls */}
        <InstrumentCard accent="info" className="p-5">
          <div className="flex flex-wrap items-end gap-4">
            <LabeledField label="From">
              <Input
                id="analytics-from"
                type="datetime-local"
                value={from}
                onChange={(e) => setFrom(e.target.value)}
              />
            </LabeledField>
            <LabeledField label="To">
              <Input
                id="analytics-to"
                type="datetime-local"
                value={to}
                onChange={(e) => setTo(e.target.value)}
              />
            </LabeledField>
            <Button
              variant="default"
              size="sm"
              onClick={handleSubmit}
              disabled={!canSubmit}
              aria-label="Run analytics"
            >
              <Play className="h-4 w-4 mr-1.5" />
              {analyticsMutation.isPending ? "Analyzing..." : "Analyze"}
            </Button>
          </div>
          {rangeError && (
            <p className="mt-2 text-xs text-destructive">{rangeError}</p>
          )}
        </InstrumentCard>

        {/* Loading */}
        {analyticsMutation.isPending && (
          <div className="space-y-3">
            <SkeletonCard />
            <SkeletonCard />
          </div>
        )}

        {/* Error */}
        {analyticsMutation.isError && (
          <InfoBanner variant="error">
            {analyticsMutation.error?.message ?? "Failed to run analytics"}
          </InfoBanner>
        )}

        {/* Results */}
        {result && !analyticsMutation.isPending && (
          <motion.div
            initial={{ opacity: 0, y: 8 }}
            animate={{ opacity: 1, y: 0 }}
            className="space-y-6"
          >
            {/* Summary strip */}
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
              {[
                { label: "Rules analyzed", value: result.summary.total_rules },
                { label: "Total hits", value: result.summary.total_hits },
                { label: "Total overrides", value: result.summary.total_overrides },
                {
                  label: "Highest override",
                  value: result.summary.highest_override_rule_id || "—",
                  mono: true,
                },
              ].map((card) => (
                <div
                  key={card.label}
                  className="rounded-xl border border-border/60 bg-card/80 p-3 space-y-0.5"
                >
                  <div className="text-xs text-muted-foreground">{card.label}</div>
                  <div
                    className={`text-lg font-semibold tabular-nums ${
                      card.mono ? "font-mono text-sm" : ""
                    }`}
                  >
                    {typeof card.value === "number"
                      ? card.value.toLocaleString()
                      : card.value}
                  </div>
                </div>
              ))}
            </div>

            {/* Rule quality table */}
            <section aria-label="Rule quality table">
              <h2 className="text-xs font-medium text-muted-foreground uppercase tracking-wider mb-3">
                Rule quality
              </h2>
              <RuleTable rules={result.rules} />
            </section>

            {/* Approval fatigue chart */}
            {result.rules.length > 0 && (
              <section aria-label="Approval fatigue chart">
                <h2 className="text-xs font-medium text-muted-foreground uppercase tracking-wider mb-3">
                  Approval volume by rule (daily)
                </h2>
                <div className="rounded-xl border border-border/60 bg-card/80 p-4">
                  <ApprovalFatigueChart rules={result.rules} />
                </div>
              </section>
            )}

            {/* False positive highlights */}
            <section aria-label="False positive highlights">
              <h2 className="text-xs font-medium text-muted-foreground uppercase tracking-wider mb-3">
                False positive analysis
              </h2>
              <FalsePositiveHighlights rules={result.rules} />
            </section>
          </motion.div>
        )}
      </div>
  );

  if (hideHeader) {
    return content;
  }

  return (
    <div className="flex flex-col h-full overflow-y-auto">
      <PageHeader
        title="Rule Quality Analytics"
        subtitle="See which rules generate the most volume, overrides, and review fatigue before you replay or retune them."
        label="Govern"
      />
      {content}
    </div>
  );
}

