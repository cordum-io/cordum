import { useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import {
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Legend,
  Line,
  LineChart,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { DecisionType } from "@/api/generated/model/decisionType";
import type { Decision } from "@/api/generated/model/decision";

const THROTTLE_INTERVAL_MS = 1_000;

const DECISION_TYPE_ORDER: ReadonlyArray<DecisionType> = [
  DecisionType.allow,
  DecisionType.allow_with_constraints,
  DecisionType.throttle,
  DecisionType.redact,
  DecisionType.require_human,
  DecisionType.quarantine,
  DecisionType.deny,
];

const DECISION_TYPE_TOKEN: Record<DecisionType, string> = {
  [DecisionType.allow]: "var(--color-success)",
  [DecisionType.allow_with_constraints]: "var(--color-info)",
  [DecisionType.throttle]: "var(--color-warning)",
  [DecisionType.redact]: "var(--color-info)",
  [DecisionType.require_human]: "var(--color-warning)",
  [DecisionType.quarantine]: "var(--color-warning)",
  [DecisionType.deny]: "var(--destructive)",
};

interface DecisionsChartsPanelProps {
  decisions: Decision[];
}

interface ChartFrame {
  total: number;
  distribution: Array<{ type: DecisionType; count: number }>;
  topRules: Array<{ rule_id: string; count: number }>;
  perMinute: Array<{ minute: string; count: number }>;
  byScope: Array<Record<string, string | number>>;
  scopeKeys: string[];
}

/**
 * D9b — DecisionsChartsPanel.
 *
 * Renders four compact Recharts charts above the Decisions DataTable:
 *   (1) Decision distribution  — PieChart of DecisionType counts.
 *   (2) Top firing rules       — horizontal BarChart, top 10, each rule
 *                                 also rendered as an accessible
 *                                 cross-link anchor for the spec's
 *                                 "click rule -> rule editor" contract.
 *   (3) Decisions/min (live)   — LineChart, last 60 minutes.
 *   (4) Decisions by scope     — stacked BarChart, x=source (job/edge),
 *                                 stacks=DecisionType. Decision shape
 *                                 lacks scope_kind; source is the closest
 *                                 truthful proxy until Backend 5e.
 *
 * Live mode applies a 1Hz throttle on the chart-data computation so a
 * 100-event/s burst from useDecisionsStream cannot trigger more than
 * one Recharts recompute per second (plan risk #2).
 */
export function DecisionsChartsPanel({ decisions }: DecisionsChartsPanelProps) {
  const throttledDecisions = useThrottledValue(decisions, THROTTLE_INTERVAL_MS);
  const frame = useMemo<ChartFrame>(
    () => buildFrame(throttledDecisions),
    [throttledDecisions],
  );

  return (
    <div
      className="grid grid-cols-1 gap-4 rounded-2xl border border-border/60 bg-surface-1 p-4 sm:grid-cols-2"
      data-testid="decisions-charts-panel"
    >
      <DistributionChart frame={frame} />
      <TopRulesChart frame={frame} />
      <PerMinuteChart frame={frame} />
      <ByScopeChart frame={frame} />
    </div>
  );
}

function DistributionChart({ frame }: { frame: ChartFrame }) {
  const isEmpty = frame.total === 0;
  return (
    <ChartCard
      title="Decision distribution"
      testId="decisions-chart-distribution"
      ariaLabel={`Decision distribution: ${frame.total} decisions across ${frame.distribution.length} outcome types`}
      decisionCount={frame.total}
    >
      {isEmpty ? (
        <ChartEmpty />
      ) : (
        <ResponsiveContainer width="100%" height={200}>
          <PieChart>
            <Pie
              data={frame.distribution}
              dataKey="count"
              nameKey="type"
              cx="50%"
              cy="50%"
              outerRadius={70}
              isAnimationActive={false}
            >
              {frame.distribution.map((row) => (
                <Cell key={row.type} fill={DECISION_TYPE_TOKEN[row.type]} />
              ))}
            </Pie>
            <Tooltip />
            <Legend />
          </PieChart>
        </ResponsiveContainer>
      )}
    </ChartCard>
  );
}

function TopRulesChart({ frame }: { frame: ChartFrame }) {
  const isEmpty = frame.topRules.length === 0;
  return (
    <ChartCard
      title="Top firing rules"
      testId="decisions-chart-top-rules"
      ariaLabel={`Top firing rules: ${frame.topRules.length} rules ranked by decision volume`}
      decisionCount={frame.total}
    >
      {isEmpty ? (
        <ChartEmpty />
      ) : (
        <>
          <ResponsiveContainer width="100%" height={200}>
            <BarChart data={frame.topRules} layout="vertical">
              <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
              <XAxis type="number" stroke="var(--muted-foreground)" />
              <YAxis
                type="category"
                dataKey="rule_id"
                stroke="var(--muted-foreground)"
                width={120}
              />
              <Tooltip />
              <Bar
                dataKey="count"
                fill="var(--color-cordum)"
                isAnimationActive={false}
              />
            </BarChart>
          </ResponsiveContainer>
          <ul className="mt-2 space-y-1 text-xs">
            {frame.topRules.slice(0, 5).map((row) => (
              <li key={row.rule_id} className="flex items-center justify-between">
                <Link
                  to={`/policies?rule=${encodeURIComponent(row.rule_id)}&open=editor`}
                  data-row-action="cross-link-decisions-rule"
                  aria-label={`Open rule ${row.rule_id} in editor`}
                  className="font-mono truncate text-foreground hover:text-cordum"
                >
                  {row.rule_id}
                </Link>
                <span className="font-mono tabular-nums text-muted-foreground">
                  {row.count}
                </span>
              </li>
            ))}
          </ul>
        </>
      )}
    </ChartCard>
  );
}

function PerMinuteChart({ frame }: { frame: ChartFrame }) {
  const isEmpty = frame.perMinute.length === 0;
  return (
    <ChartCard
      title="Decisions per minute"
      testId="decisions-chart-per-min"
      ariaLabel={`Decisions per minute over the last hour: ${frame.total} decisions in ${frame.perMinute.length} buckets`}
      decisionCount={frame.total}
    >
      {isEmpty ? (
        <ChartEmpty />
      ) : (
        <ResponsiveContainer width="100%" height={200}>
          <LineChart data={frame.perMinute}>
            <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
            <XAxis dataKey="minute" stroke="var(--muted-foreground)" hide />
            <YAxis allowDecimals={false} stroke="var(--muted-foreground)" />
            <Tooltip />
            <Line
              dataKey="count"
              type="monotone"
              stroke="var(--color-cordum)"
              strokeWidth={2}
              dot={false}
              isAnimationActive={false}
            />
          </LineChart>
        </ResponsiveContainer>
      )}
    </ChartCard>
  );
}

function ByScopeChart({ frame }: { frame: ChartFrame }) {
  const isEmpty = frame.byScope.length === 0;
  return (
    <ChartCard
      title="Decisions by source"
      testId="decisions-chart-by-scope"
      ariaLabel={`Decisions stacked by source (job vs edge), broken down by decision type — ${frame.total} decisions`}
      decisionCount={frame.total}
    >
      {isEmpty ? (
        <ChartEmpty />
      ) : (
        <ResponsiveContainer width="100%" height={200}>
          <BarChart data={frame.byScope}>
            <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
            <XAxis dataKey="scope" stroke="var(--muted-foreground)" />
            <YAxis allowDecimals={false} stroke="var(--muted-foreground)" />
            <Tooltip />
            <Legend />
            {frame.scopeKeys.map((key) => (
              <Bar
                key={key}
                dataKey={key}
                stackId="decisions"
                fill={DECISION_TYPE_TOKEN[key as DecisionType] ?? "var(--muted-foreground)"}
                isAnimationActive={false}
              />
            ))}
          </BarChart>
        </ResponsiveContainer>
      )}
    </ChartCard>
  );
}

function ChartCard({
  title,
  testId,
  ariaLabel,
  decisionCount,
  children,
}: {
  title: string;
  testId: string;
  ariaLabel: string;
  decisionCount: number;
  children: React.ReactNode;
}) {
  return (
    <section
      data-testid={testId}
      data-decision-count={String(decisionCount)}
      aria-label={ariaLabel}
      className="rounded-md border border-border/60 bg-surface-2/30 p-3"
    >
      <h3 className="mb-2 font-display text-sm font-semibold text-ink">{title}</h3>
      {children}
    </section>
  );
}

function ChartEmpty() {
  return (
    <div
      data-testid="decisions-chart-empty"
      className="flex h-[200px] items-center justify-center text-xs italic text-muted-foreground"
    >
      No decisions in the current window.
    </div>
  );
}

export default DecisionsChartsPanel;

function buildFrame(decisions: Decision[]): ChartFrame {
  const total = decisions.length;
  const distribution = computeDistribution(decisions);
  const topRules = computeTopRules(decisions, 10);
  const perMinute = computePerMinute(decisions);
  const { rows: byScope, keys: scopeKeys } = computeByScope(decisions);
  return { total, distribution, topRules, perMinute, byScope, scopeKeys };
}

function computeDistribution(decisions: Decision[]): Array<{ type: DecisionType; count: number }> {
  const counts = new Map<DecisionType, number>();
  for (const d of decisions) {
    counts.set(d.type, (counts.get(d.type) ?? 0) + 1);
  }
  return DECISION_TYPE_ORDER
    .filter((t) => (counts.get(t) ?? 0) > 0)
    .map((t) => ({ type: t, count: counts.get(t) ?? 0 }));
}

function computeTopRules(decisions: Decision[], topN: number): Array<{ rule_id: string; count: number }> {
  const counts = new Map<string, number>();
  for (const d of decisions) {
    if (!d.rule_id) continue;
    counts.set(d.rule_id, (counts.get(d.rule_id) ?? 0) + 1);
  }
  return Array.from(counts.entries())
    .map(([rule_id, count]) => ({ rule_id, count }))
    .sort((a, b) => b.count - a.count)
    .slice(0, topN);
}

function computePerMinute(decisions: Decision[]): Array<{ minute: string; count: number }> {
  if (decisions.length === 0) return [];
  const buckets = new Map<string, number>();
  for (const d of decisions) {
    const ts = Date.parse(d.timestamp);
    if (Number.isNaN(ts)) continue;
    // Floor to the minute (UTC); use the ISO minute slug as the key so
    // sort + display is deterministic.
    const minute = new Date(Math.floor(ts / 60_000) * 60_000).toISOString();
    buckets.set(minute, (buckets.get(minute) ?? 0) + 1);
  }
  return Array.from(buckets.entries())
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([minute, count]) => ({ minute, count }));
}

function computeByScope(decisions: Decision[]): {
  rows: Array<Record<string, string | number>>;
  keys: string[];
} {
  // Decision shape lacks scope_kind. Use `source` (job|edge) as the
  // closest truthful proxy until Backend 5e exposes scope on the
  // unified Decision envelope.
  const grouped = new Map<string, Map<DecisionType, number>>();
  const seenTypes = new Set<DecisionType>();
  for (const d of decisions) {
    const scope = String(d.source);
    seenTypes.add(d.type);
    let m = grouped.get(scope);
    if (!m) {
      m = new Map();
      grouped.set(scope, m);
    }
    m.set(d.type, (m.get(d.type) ?? 0) + 1);
  }
  const rows = Array.from(grouped.entries()).map(([scope, m]) => {
    const row: Record<string, string | number> = { scope };
    for (const t of seenTypes) {
      row[t] = m.get(t) ?? 0;
    }
    return row;
  });
  const keys = DECISION_TYPE_ORDER.filter((t) => seenTypes.has(t));
  return { rows, keys };
}

// 1Hz throttle: emits leading-edge value immediately, then schedules a
// trailing-edge update after `intervalMs` so a high-frequency burst of
// upstream changes ends with the latest snapshot. A second burst within
// the same window cancels the pending trailing fire and re-schedules.
function useThrottledValue<T>(value: T, intervalMs: number): T {
  const [throttled, setThrottled] = useState<T>(value);
  const lastEmittedAtRef = useRef<number | null>(null);
  const pendingTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const latestValueRef = useRef<T>(value);

  useEffect(() => {
    latestValueRef.current = value;
    const now = Date.now();
    if (lastEmittedAtRef.current === null) {
      lastEmittedAtRef.current = now;
      setThrottled(value);
      return;
    }
    const elapsed = now - lastEmittedAtRef.current;
    if (elapsed >= intervalMs) {
      lastEmittedAtRef.current = now;
      setThrottled(value);
      return;
    }
    if (pendingTimerRef.current !== null) {
      clearTimeout(pendingTimerRef.current);
    }
    const wait = intervalMs - elapsed;
    pendingTimerRef.current = setTimeout(() => {
      lastEmittedAtRef.current = Date.now();
      pendingTimerRef.current = null;
      setThrottled(latestValueRef.current);
    }, wait);
  }, [value, intervalMs]);

  useEffect(() => {
    return () => {
      if (pendingTimerRef.current !== null) {
        clearTimeout(pendingTimerRef.current);
        pendingTimerRef.current = null;
      }
    };
  }, []);

  return throttled;
}
