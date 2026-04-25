/*
 * DESIGN: "Policy Forensics" — Historical Replay Dashboard.
 * Single-column layout: form at top, dense results below.
 * Three result sections: summary cards, changes table, rule hits chart.
 */
import { useState, useMemo, useCallback } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { motion, AnimatePresence } from "framer-motion";
import {
  History,
  Play,
  ArrowUp,
  ArrowDown,
  Minus,
  CheckCircle2,
  XCircle,
  Filter,
  Search,
} from "lucide-react";
import {
  BarChart,
  Bar,
  ResponsiveContainer,
  XAxis,
  YAxis,
  Tooltip,
  CartesianGrid,
  Cell,
} from "recharts";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { InfoBanner } from "@/components/ui/InfoBanner";
import { ChartTooltipCompact as ChartTooltip } from "@/components/ui/ChartTooltip";
import { useReplayPolicy } from "@/hooks/usePolicies";
import type {
  PolicyReplayRequest,
  PolicyReplayResponse,
  PolicyReplayChange,
  PolicyReplayRuleHit,
} from "@/api/types";

// ---------------------------------------------------------------------------
// Exported constants for testing
// ---------------------------------------------------------------------------

export const REPLAY_PAGE_SECTIONS = [
  "form",
  "summary",
  "warnings",
  "changes",
  "rule-hits",
] as const;

export const MAX_REPLAY_RANGE_DAYS = 7;
export const DEFAULT_MAX_JOBS = 500;
export const MAX_JOBS_LIMIT = 1000;

const DECISION_OPTIONS = [
  { value: "", label: "All decisions" },
  { value: "ALLOW", label: "Allow" },
  { value: "ALLOW_WITH_CONSTRAINTS", label: "Allow with constraints" },
  { value: "DENY", label: "Deny" },
  { value: "REQUIRE_APPROVAL", label: "Require approval" },
  { value: "THROTTLE", label: "Throttle" },
];

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function toISODatetimeLocal(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function defaultFrom(): string {
  const d = new Date();
  d.setHours(d.getHours() - 24);
  return toISODatetimeLocal(d);
}

function defaultTo(): string {
  return toISODatetimeLocal(new Date());
}

/** Validate time range: from < to, max 7 days. Returns error message or null. */
export function validateTimeRange(from: string, to: string): string | null {
  if (!from || !to) return "Both from and to dates are required.";
  const fromDate = new Date(from);
  const toDate = new Date(to);
  if (isNaN(fromDate.getTime()) || isNaN(toDate.getTime()))
    return "Invalid date format.";
  if (fromDate >= toDate) return "Start date must be before end date.";
  const diffMs = toDate.getTime() - fromDate.getTime();
  const diffDays = diffMs / (1000 * 60 * 60 * 24);
  if (diffDays > MAX_REPLAY_RANGE_DAYS)
    return `Time range cannot exceed ${MAX_REPLAY_RANGE_DAYS} days.`;
  return null;
}

/** Build replay request from form state. */
export function buildReplayRequest(state: {
  from: string;
  to: string;
  tenant: string;
  topicPattern: string;
  originalDecision: string;
  useCurrentPolicy: boolean;
  candidateYaml: string;
  maxJobs: number;
}): PolicyReplayRequest {
  const req: PolicyReplayRequest = {
    from: new Date(state.from).toISOString(),
    to: new Date(state.to).toISOString(),
    use_current_policy: state.useCurrentPolicy,
    max_jobs: Math.min(state.maxJobs, MAX_JOBS_LIMIT),
  };
  const filters: PolicyReplayRequest["filters"] = {};
  if (state.tenant.trim()) filters.tenant = state.tenant.trim();
  if (state.topicPattern.trim())
    filters.topic_pattern = state.topicPattern.trim();
  if (state.originalDecision)
    filters.original_decision = state.originalDecision;
  if (Object.keys(filters).length > 0) req.filters = filters;
  if (!state.useCurrentPolicy && state.candidateYaml.trim()) {
    req.candidate_content = state.candidateYaml.trim();
  }
  return req;
}

// ---------------------------------------------------------------------------
// Direction badge
// ---------------------------------------------------------------------------

function DirectionBadge({
  direction,
}: {
  direction: "escalated" | "relaxed" | "unchanged";
}) {
  const config = {
    escalated: {
      icon: ArrowUp,
      label: "Escalated",
      variant: "warning" as const,
    },
    relaxed: {
      icon: ArrowDown,
      label: "Relaxed",
      variant: "info" as const,
    },
    unchanged: {
      icon: Minus,
      label: "Unchanged",
      variant: "muted" as const,
    },
  };
  const { icon: Icon, label, variant } = config[direction];
  return (
    <StatusBadge variant={variant}>
      <Icon className="h-3 w-3 mr-0.5" />
      {label}
    </StatusBadge>
  );
}

// ---------------------------------------------------------------------------
// Decision badge
// ---------------------------------------------------------------------------

function DecisionBadge({ decision }: { decision: string }) {
  const upper = decision.toUpperCase();
  const variant =
    upper === "ALLOW"
      ? "healthy"
      : upper === "ALLOW_WITH_CONSTRAINTS"
        ? "cordum"
        : upper === "DENY"
          ? "danger"
          : upper === "REQUIRE_APPROVAL"
            ? "warning"
            : upper === "THROTTLE"
              ? "governance"
              : "muted";
  return <StatusBadge variant={variant}>{decision}</StatusBadge>;
}

// ---------------------------------------------------------------------------
// Summary cards
// ---------------------------------------------------------------------------

function SummaryCards({
  summary,
}: {
  summary: PolicyReplayResponse["summary"];
}) {
  const cards = [
    {
      label: "Total replayed",
      value: summary.total_jobs,
      variant: "muted" as const,
    },
    {
      label: "Unchanged",
      value: summary.unchanged,
      variant: "muted" as const,
    },
    {
      label: "Escalated",
      value: summary.escalated,
      variant: "warning" as const,
      icon: ArrowUp,
    },
    {
      label: "Relaxed",
      value: summary.relaxed,
      variant: "info" as const,
      icon: ArrowDown,
    },
    {
      label: "Errors",
      value: summary.errored,
      variant: "danger" as const,
      icon: XCircle,
    },
  ];

  return (
    <div className="grid grid-cols-2 sm:grid-cols-5 gap-3">
      {cards.map((card) => {
        const pct =
          summary.total_jobs > 0
            ? ((card.value / summary.total_jobs) * 100).toFixed(1)
            : "0.0";
        return (
          <div
            key={card.label}
            className="rounded-xl border border-border/60 bg-card/80 p-4 space-y-1"
          >
            <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
              {card.icon && <card.icon className="h-3 w-3" />}
              {card.label}
            </div>
            <div className="text-2xl font-semibold tabular-nums text-foreground">
              {card.value.toLocaleString()}
            </div>
            <div className="text-[10px] text-muted-foreground/70">
              {pct}% of total
            </div>
          </div>
        );
      })}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Changes table
// ---------------------------------------------------------------------------

type SortField = "job_id" | "topic" | "direction";
type SortDir = "asc" | "desc";

function ChangesTable({ changes }: { changes: PolicyReplayChange[] }) {
  const [search, setSearch] = useState("");
  const [dirFilter, setDirFilter] = useState("");
  const [sortField, setSortField] = useState<SortField>("direction");
  const [sortDir, setSortDir] = useState<SortDir>("asc");

  const filtered = useMemo(() => {
    let items = changes;
    if (search.trim()) {
      const q = search.toLowerCase();
      items = items.filter(
        (c) =>
          c.job_id.toLowerCase().includes(q) ||
          c.topic.toLowerCase().includes(q) ||
          c.tenant.toLowerCase().includes(q),
      );
    }
    if (dirFilter) {
      items = items.filter((c) => c.direction === dirFilter);
    }
    return [...items].sort((a, b) => {
      const fieldA = a[sortField] ?? "";
      const fieldB = b[sortField] ?? "";
      const cmp = fieldA < fieldB ? -1 : fieldA > fieldB ? 1 : 0;
      return sortDir === "asc" ? cmp : -cmp;
    });
  }, [changes, search, dirFilter, sortField, sortDir]);

  const toggleSort = useCallback(
    (field: SortField) => {
      if (sortField === field) {
        setSortDir(sortDir === "asc" ? "desc" : "asc");
      } else {
        setSortField(field);
        setSortDir("asc");
      }
    },
    [sortField, sortDir],
  );

  if (changes.length === 0) {
    return (
      <EmptyState
        icon={<CheckCircle2 className="h-8 w-8" />}
        title="No decision changes"
        description="The candidate policy produces identical results for all replayed jobs."
      />
    );
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-3">
        <div className="relative flex-1 min-w-[200px]">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground" />
          <input
            type="text"
            placeholder="Search job ID, topic, tenant..."
            className="w-full rounded-xl border border-border bg-background pl-8 pr-3 py-1.5 text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            aria-label="Search changes"
          />
        </div>
        <select
          className="rounded-xl border border-border bg-background px-2 py-1.5 text-sm"
          value={dirFilter}
          onChange={(e) => setDirFilter(e.target.value)}
          aria-label="Filter by direction"
        >
          <option value="">All changes</option>
          <option value="escalated">Escalated</option>
          <option value="relaxed">Relaxed</option>
        </select>
      </div>
      <div className="overflow-x-auto rounded-xl border border-border/60">
        <table className="w-full text-sm" role="table">
          <thead>
            <tr className="border-b border-border/40 bg-muted/20">
              {([
                ["job_id", "Job ID"],
                ["topic", "Topic"],
              ] as const).map(([field, label]) => (
                <th
                  key={field}
                  className="text-left py-2.5 px-3 text-xs font-medium text-muted-foreground uppercase tracking-wider cursor-pointer select-none hover:text-foreground"
                  onClick={() => toggleSort(field)}
                >
                  {label}
                  {sortField === field && (
                    <span className="ml-1">
                      {sortDir === "asc" ? "\u25B2" : "\u25BC"}
                    </span>
                  )}
                </th>
              ))}
              <th className="text-left py-2.5 px-3 text-xs font-medium text-muted-foreground uppercase tracking-wider">
                Tenant
              </th>
              <th className="text-left py-2.5 px-3 text-xs font-medium text-muted-foreground uppercase tracking-wider">
                Original
              </th>
              <th className="text-left py-2.5 px-3 text-xs font-medium text-muted-foreground uppercase tracking-wider">
                New
              </th>
              <th
                className="text-left py-2.5 px-3 text-xs font-medium text-muted-foreground uppercase tracking-wider cursor-pointer select-none hover:text-foreground"
                onClick={() => toggleSort("direction")}
              >
                Direction
                {sortField === "direction" && (
                  <span className="ml-1">
                    {sortDir === "asc" ? "\u25B2" : "\u25BC"}
                  </span>
                )}
              </th>
            </tr>
          </thead>
          <tbody>
            {filtered.map((change, i) => (
              <tr
                key={change.job_id + i}
                className="border-b border-border/20 last:border-0 hover:bg-muted/10"
              >
                <td className="py-2 px-3 font-mono text-xs">
                  <Link
                    to={`/jobs/${change.job_id}`}
                    className="text-[var(--color-cordum)] hover:underline"
                  >
                    {change.job_id.length > 16
                      ? change.job_id.slice(0, 16) + "..."
                      : change.job_id}
                  </Link>
                </td>
                <td className="py-2 px-3 text-foreground/80">
                  {change.topic}
                </td>
                <td className="py-2 px-3 text-muted-foreground">
                  {change.tenant || "—"}
                </td>
                <td className="py-2 px-3">
                  <DecisionBadge decision={change.original_decision} />
                </td>
                <td className="py-2 px-3">
                  <DecisionBadge decision={change.new_decision} />
                </td>
                <td className="py-2 px-3">
                  <DirectionBadge direction={change.direction} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="text-xs text-muted-foreground">
        Showing {filtered.length} of {changes.length} changes
      </p>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Rule hits chart
// ---------------------------------------------------------------------------

function RuleHitsChart({ ruleHits }: { ruleHits: PolicyReplayRuleHit[] }) {
  if (ruleHits.length === 0) {
    return (
      <EmptyState
        icon={<History className="h-8 w-8" />}
        title="No rule hits"
        description="No rules matched during the replay."
      />
    );
  }

  const data = ruleHits
    .slice()
    .sort((a, b) => b.count - a.count)
    .map((rh) => ({
      rule: rh.rule_id.length > 24 ? rh.rule_id.slice(0, 24) + "..." : rh.rule_id,
      fullRule: rh.rule_id,
      count: rh.count,
      decision: rh.decision,
    }));

  const barHeight = Math.max(data.length * 32, 120);

  return (
    <ResponsiveContainer width="100%" height={barHeight}>
      <BarChart data={data} layout="vertical" margin={{ left: 10, right: 20 }}>
        <CartesianGrid
          strokeDasharray="3 3"
          stroke="rgba(255,255,255,0.04)"
          horizontal={false}
        />
        <XAxis
          type="number"
          tick={{ fontSize: 10, fill: "#5a6a70" }}
          axisLine={false}
          tickLine={false}
        />
        <YAxis
          type="category"
          dataKey="rule"
          tick={{ fontSize: 10, fill: "#5a6a70" }}
          axisLine={false}
          tickLine={false}
          width={180}
        />
        <Tooltip content={<ChartTooltip />} cursor={{ fill: "var(--surface-2)" }} />
        <Bar dataKey="count" name="Hits" radius={[0, 3, 3, 0]}>
          {data.map((entry, i) => (
            <Cell
              key={i}
              fill={
                entry.decision === "DENY"
                  ? "#ef4444"
                  : entry.decision === "REQUIRE_APPROVAL"
                    ? "#f59e0b"
                    : entry.decision === "THROTTLE"
                      ? "#8b5cf6"
                      : entry.decision === "ALLOW_WITH_CONSTRAINTS"
                        ? "#06b6d4"
                        : "#10b981"
              }
            />
          ))}
        </Bar>
      </BarChart>
    </ResponsiveContainer>
  );
}

// ---------------------------------------------------------------------------
// Main page
// ---------------------------------------------------------------------------

export default function ReplayPage({
  hideHeader,
}: {
  hideHeader?: boolean;
} = {}) {
  const [searchParams] = useSearchParams();
  const replayMutation = useReplayPolicy();
  const result = replayMutation.data;

  // Form state — seeded once from the inbound URL (deep-link into the
  // replay page). We do not re-sync from searchParams after mount: once
  // the user starts editing the form we must not wipe their input just
  // because the URL changed (e.g. the browser pushed a history entry as
  // other controls updated). A URL change that should reset the form is
  // handled by the caller via a routed remount.
  const [from, setFrom] = useState(
    searchParams.get("from") || defaultFrom(),
  );
  const [to, setTo] = useState(searchParams.get("to") || defaultTo());
  const [tenant, setTenant] = useState(searchParams.get("tenant") || "");
  const [topicPattern, setTopicPattern] = useState(
    searchParams.get("topic") || "",
  );
  const [originalDecision, setOriginalDecision] = useState(
    searchParams.get("decision") || "",
  );
  const [useCurrentPolicy, setUseCurrentPolicy] = useState(true);
  const [candidateYaml, setCandidateYaml] = useState("");
  const [maxJobs, setMaxJobs] = useState(DEFAULT_MAX_JOBS);

  const rangeError = useMemo(() => validateTimeRange(from, to), [from, to]);
  const canSubmit =
    !rangeError &&
    !replayMutation.isPending &&
    (useCurrentPolicy || candidateYaml.trim().length > 0);

  const handleSubmit = useCallback(() => {
    if (!canSubmit) return;
    const req = buildReplayRequest({
      from,
      to,
      tenant,
      topicPattern,
      originalDecision,
      useCurrentPolicy,
      candidateYaml,
      maxJobs,
    });
    replayMutation.mutate(req);
  }, [
    canSubmit,
    from,
    to,
    tenant,
    topicPattern,
    originalDecision,
    useCurrentPolicy,
    candidateYaml,
    maxJobs,
    replayMutation,
  ]);

  const content = (
    <div className={hideHeader ? "space-y-6" : "max-w-6xl mx-auto w-full px-4 py-6 space-y-6"}>
        {/* Form section */}
        <div className="rounded-xl border border-border/60 bg-card/80 p-5 space-y-5">
          {/* Time range */}
          <div>
            <label className="block text-xs font-medium text-muted-foreground uppercase tracking-wider mb-2">
              Time range
            </label>
            <div className="flex flex-wrap items-center gap-3">
              <div className="space-y-1">
                <label
                  htmlFor="replay-from"
                  className="text-[11px] text-muted-foreground"
                >
                  From
                </label>
                <input
                  id="replay-from"
                  type="datetime-local"
                  className="rounded-xl border border-border bg-background px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-ring"
                  value={from}
                  onChange={(e) => setFrom(e.target.value)}
                />
              </div>
              <div className="space-y-1">
                <label
                  htmlFor="replay-to"
                  className="text-[11px] text-muted-foreground"
                >
                  To
                </label>
                <input
                  id="replay-to"
                  type="datetime-local"
                  className="rounded-xl border border-border bg-background px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-ring"
                  value={to}
                  onChange={(e) => setTo(e.target.value)}
                />
              </div>
              <div className="space-y-1">
                <label
                  htmlFor="replay-max-jobs"
                  className="text-[11px] text-muted-foreground"
                >
                  Max jobs
                </label>
                <input
                  id="replay-max-jobs"
                  type="number"
                  min={1}
                  max={MAX_JOBS_LIMIT}
                  className="w-24 rounded-xl border border-border bg-background px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-ring"
                  value={maxJobs}
                  onChange={(e) =>
                    setMaxJobs(
                      Math.min(
                        MAX_JOBS_LIMIT,
                        Math.max(1, Number(e.target.value) || 1),
                      ),
                    )
                  }
                />
              </div>
            </div>
            {rangeError && (
              <p className="mt-1.5 text-xs text-destructive">{rangeError}</p>
            )}
          </div>

          {/* Filters */}
          <div>
            <label className="block text-xs font-medium text-muted-foreground uppercase tracking-wider mb-2">
              <Filter className="inline h-3 w-3 mr-1" />
              Filters
            </label>
            <div className="flex flex-wrap items-center gap-3">
              <input
                type="text"
                placeholder="Tenant"
                className="w-40 rounded-xl border border-border bg-background px-3 py-1.5 text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                value={tenant}
                onChange={(e) => setTenant(e.target.value)}
                aria-label="Tenant filter"
              />
              <input
                type="text"
                placeholder="Topic pattern (glob)"
                className="w-52 rounded-xl border border-border bg-background px-3 py-1.5 text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                value={topicPattern}
                onChange={(e) => setTopicPattern(e.target.value)}
                aria-label="Topic pattern filter"
              />
              <select
                className="rounded-xl border border-border bg-background px-3 py-1.5 text-sm"
                value={originalDecision}
                onChange={(e) => setOriginalDecision(e.target.value)}
                aria-label="Original decision filter"
              >
                {DECISION_OPTIONS.map((opt) => (
                  <option key={opt.value} value={opt.value}>
                    {opt.label}
                  </option>
                ))}
              </select>
            </div>
          </div>

          {/* Policy input */}
          <div>
            <label className="block text-xs font-medium text-muted-foreground uppercase tracking-wider mb-2">
              Candidate policy
            </label>
            <label className="inline-flex items-center gap-2 text-sm cursor-pointer">
              <input
                type="checkbox"
                checked={useCurrentPolicy}
                onChange={(e) => setUseCurrentPolicy(e.target.checked)}
                className="rounded border-border"
              />
              Use current published policy
            </label>
            <AnimatePresence>
              {!useCurrentPolicy && (
                <motion.div
                  initial={{ opacity: 0, height: 0 }}
                  animate={{ opacity: 1, height: "auto" }}
                  exit={{ opacity: 0, height: 0 }}
                  className="mt-3 overflow-hidden"
                >
                  <div className="instrument-card p-0">
                    <textarea
                      aria-label="Candidate policy YAML"
                      className="h-[280px] w-full resize-none rounded-xl bg-surface-0 p-4 font-mono text-xs text-foreground outline-none focus:ring-2 focus:ring-cordum/30"
                      placeholder="# Paste candidate policy YAML here..."
                      value={candidateYaml}
                      onChange={(e) => setCandidateYaml(e.target.value)}
                    />
                  </div>
                </motion.div>
              )}
            </AnimatePresence>
          </div>

          {/* Submit */}
          <div className="flex items-center gap-3">
            <Button
              variant="default"
              size="sm"
              onClick={handleSubmit}
              disabled={!canSubmit}
              aria-label="Run policy replay"
            >
              <Play className="h-4 w-4 mr-1.5" />
              {replayMutation.isPending ? "Running..." : "Run Replay"}
            </Button>
            {replayMutation.isError && (
              <span className="text-xs text-destructive">
                {replayMutation.error?.message || "Replay failed"}
              </span>
            )}
          </div>
        </div>

        {/* Loading state */}
        {replayMutation.isPending && (
          <div className="space-y-3">
            <div className="grid grid-cols-2 sm:grid-cols-5 gap-3">
              {[...Array(5)].map((_, i) => (
                <SkeletonCard key={i} />
              ))}
            </div>
            <SkeletonCard />
            <SkeletonCard />
          </div>
        )}

        {/* Results */}
        {result && !replayMutation.isPending && (
          <motion.div
            initial={{ opacity: 0, y: 8 }}
            animate={{ opacity: 1, y: 0 }}
            className="space-y-6"
          >
            {/* Summary cards */}
            <section aria-label="Replay summary">
              <h2 className="text-xs font-medium text-muted-foreground uppercase tracking-wider mb-3">
                Summary
              </h2>
              {result.summary.total_jobs === 0 ? (
                <EmptyState
                  icon={<History className="h-8 w-8" />}
                  title="No jobs matched"
                  description="No jobs matched the selected filters and time range. Try widening the range or relaxing filters."
                />
              ) : (
                <SummaryCards summary={result.summary} />
              )}
            </section>

            {/* Warnings */}
            {(result.warnings ?? []).length > 0 && (
              <section aria-label="Replay warnings">
                <div className="space-y-2">
                  {(result.warnings ?? []).map((w, i) => (
                    <InfoBanner key={i} variant="warning">
                      {w}
                    </InfoBanner>
                  ))}
                </div>
              </section>
            )}

            {/* Errors */}
            {(result.errors ?? []).length > 0 && (
              <section aria-label="Replay errors">
                <details className="rounded-xl border border-destructive/30 bg-destructive/5 p-3">
                  <summary className="text-xs font-medium text-destructive cursor-pointer">
                    {(result.errors ?? []).length} job(s) failed to replay
                  </summary>
                  <ul className="mt-2 space-y-1 text-xs text-destructive/80">
                    {(result.errors ?? []).map((e, i) => (
                      <li key={i} className="font-mono">
                        {e}
                      </li>
                    ))}
                  </ul>
                </details>
              </section>
            )}

            {/* Changes table */}
            {result.summary.total_jobs > 0 && (
              <section aria-label="Decision changes">
                <h2 className="text-xs font-medium text-muted-foreground uppercase tracking-wider mb-3">
                  Decision changes
                </h2>
                <ChangesTable changes={result.changes} />
              </section>
            )}

            {/* Rule hits chart */}
            {result.summary.total_jobs > 0 && (
              <section aria-label="Rule hits">
                <h2 className="text-xs font-medium text-muted-foreground uppercase tracking-wider mb-3">
                  Rule hits
                </h2>
                <div className="rounded-xl border border-border/60 bg-card/80 p-4">
                  <RuleHitsChart ruleHits={result.rule_hits} />
                </div>
              </section>
            )}

            {/* Replay metadata */}
            <div className="text-xs text-muted-foreground/60 flex flex-wrap gap-4">
              <span>
                Replay ID:{" "}
                <code className="font-mono">{result.replay_id}</code>
              </span>
              <span>
                Policy snapshot:{" "}
                <code className="font-mono">
                  {result.policy_snapshot.slice(0, 16)}...
                </code>
              </span>
              <span>
                Range: {result.time_range.from} — {result.time_range.to}
              </span>
            </div>
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
        title="Replay & Compare"
        subtitle="Re-run historical jobs against the current or a candidate policy to see what decisions would change."
        label="Govern"
      />
      {content}
    </div>
  );
}

