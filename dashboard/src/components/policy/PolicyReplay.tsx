import { useState, useMemo, useCallback } from "react";
import { RefreshCw } from "lucide-react";
import { useMutation } from "@tanstack/react-query";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { Input } from "../ui/Input";
import { useJobs } from "../../hooks/useJobs";
import { post } from "../../api/client";
import { cn } from "../../lib/utils";
import type { Job } from "../../api/types";
import type { SimulateResult } from "../../hooks/usePolicies";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface ReplayRow {
  jobId: string;
  topic: string;
  originalDecision: string;
  newDecision: string;
  changed: boolean;
}

type JobLimit = 10 | 50 | 100;
type TimeRange = "1h" | "6h" | "24h" | "7d" | "custom";

const LIMITS: JobLimit[] = [10, 50, 100];
const TIME_RANGES: { value: TimeRange; label: string }[] = [
  { value: "1h", label: "1h" },
  { value: "6h", label: "6h" },
  { value: "24h", label: "24h" },
  { value: "7d", label: "7d" },
  { value: "custom", label: "Custom" },
];

// ---------------------------------------------------------------------------
// Decision badge
// ---------------------------------------------------------------------------

const decisionBadge: Record<string, "success" | "danger" | "warning" | "info" | "default"> = {
  allow: "success",
  deny: "danger",
  require_approval: "warning",
  throttle: "info",
};

// ---------------------------------------------------------------------------
// Change arrow
// ---------------------------------------------------------------------------

function ChangeArrow({ from, to }: { from: string; to: string }) {
  return (
    <span className="inline-flex items-center gap-1 text-xs">
      <Badge variant={decisionBadge[from] ?? "default"}>{from}</Badge>
      <span className="text-muted-foreground">&rarr;</span>
      <Badge variant={decisionBadge[to] ?? "default"}>{to}</Badge>
    </span>
  );
}

// ---------------------------------------------------------------------------
// Summary stats
// ---------------------------------------------------------------------------

function ImpactSummary({ rows }: { rows: ReplayRow[] }) {
  const changed = rows.filter((r) => r.changed);
  if (changed.length === 0) {
    return (
      <Card className="border-2 border-success/40 bg-[color:rgba(31,122,87,0.06)]">
        <p className="text-sm font-semibold text-success">
          No impact — all {rows.length} jobs evaluate the same.
        </p>
      </Card>
    );
  }

  // Count transitions
  const transitions: Record<string, number> = {};
  for (const row of changed) {
    const key = `${row.originalDecision} → ${row.newDecision}`;
    transitions[key] = (transitions[key] ?? 0) + 1;
  }

  return (
    <Card className="border-2 border-warning/40 bg-[color:rgba(197,138,28,0.06)]">
      <div className="space-y-2">
        <p className="text-sm font-semibold text-warning">
          {changed.length} of {rows.length} jobs would change
        </p>
        <div className="flex flex-wrap gap-3">
          {Object.entries(transitions).map(([label, count]) => (
            <span key={label} className="text-xs text-ink">
              <span className="font-mono font-semibold">{count}</span>{" "}
              {label}
            </span>
          ))}
        </div>
      </div>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// PolicyReplay
// ---------------------------------------------------------------------------

function matchesTimeRange(createdAt: string, range: TimeRange, customStart: string, customEnd: string): boolean {
  if (range === "custom") {
    const t = new Date(createdAt).getTime();
    if (customStart && t < new Date(customStart).getTime()) return false;
    if (customEnd && t > new Date(customEnd + "T23:59:59").getTime()) return false;
    return true;
  }
  const ms = Date.now() - new Date(createdAt).getTime();
  const limits: Record<string, number> = {
    "1h": 60 * 60 * 1000,
    "6h": 6 * 60 * 60 * 1000,
    "24h": 24 * 60 * 60 * 1000,
    "7d": 7 * 24 * 60 * 60 * 1000,
  };
  return ms <= (limits[range] ?? Infinity);
}

export function PolicyReplay({ bundleId }: { bundleId: string }) {
  const [limit, setLimit] = useState<JobLimit>(10);
  const [timeRange, setTimeRange] = useState<TimeRange>("24h");
  const [customStart, setCustomStart] = useState("");
  const [customEnd, setCustomEnd] = useState("");

  // Fetch recent jobs
  const { data: jobsData, isLoading: jobsLoading } = useJobs({ limit });
  const allJobs = jobsData?.items ?? [];

  // Filter by time range
  const jobs = useMemo(
    () => allJobs.filter((j) => matchesTimeRange(j.createdAt, timeRange, customStart, customEnd)),
    [allJobs, timeRange, customStart, customEnd],
  );

  // Batch simulate mutation
  const replayMutation = useMutation<ReplayRow[], Error, Job[]>({
    mutationFn: async (jobList) => {
      const results: ReplayRow[] = [];
      // Simulate each job against the current bundle
      for (const job of jobList) {
        const original = job.safetyDecision?.type ?? "allow";
        try {
          const result = await post<SimulateResult>("/policy/simulate", {
            bundleId,
            payload: {
              capabilities: job.capabilities ?? [],
              risk_tags: job.riskTags ?? [],
              metadata: job.metadata ?? {},
              topic: job.topic,
              type: job.type,
            },
          });
          results.push({
            jobId: job.id,
            topic: job.topic,
            originalDecision: original,
            newDecision: result.decision,
            changed: original !== result.decision,
          });
        } catch {
          results.push({
            jobId: job.id,
            topic: job.topic,
            originalDecision: original,
            newDecision: "error",
            changed: true,
          });
        }
      }
      return results;
    },
  });

  const handleReplay = useCallback(() => {
    if (jobs.length > 0) {
      replayMutation.mutate(jobs);
    }
  }, [jobs, replayMutation]);

  const rows = replayMutation.data ?? [];
  const changedRows = useMemo(() => rows.filter((r) => r.changed), [rows]);

  return (
    <div className="space-y-4">
      <Card>
        <div className="space-y-4">
          <h3 className="font-display text-lg font-semibold text-ink">
            Replay Mode
          </h3>
          <p className="text-xs text-muted-foreground">
            Re-evaluate recent jobs against the current draft policy to see
            impact.
          </p>

          <div className="flex items-center gap-3">
            <div className="flex rounded-full border border-border">
              {LIMITS.map((l) => (
                <button
                  key={l}
                  type="button"
                  className={cn(
                    "px-4 py-1.5 text-xs font-semibold transition",
                    l === LIMITS[0] && "rounded-l-full",
                    l === LIMITS[LIMITS.length - 1] && "rounded-r-full",
                    limit === l
                      ? "bg-accent/15 text-accent"
                      : "text-muted-foreground hover:text-ink",
                  )}
                  onClick={() => setLimit(l)}
                >
                  {l}
                </button>
              ))}
            </div>
            <span className="text-xs text-muted-foreground">jobs</span>
          </div>

          {/* Time range filter */}
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-xs font-semibold text-muted-foreground">Time range:</span>
            <div className="flex rounded-full border border-border">
              {TIME_RANGES.map(({ value, label }) => (
                <button
                  key={value}
                  type="button"
                  onClick={() => setTimeRange(value)}
                  className={cn(
                    "px-3 py-1 text-xs font-semibold transition first:rounded-l-full last:rounded-r-full",
                    timeRange === value
                      ? "bg-accent/15 text-accent"
                      : "text-muted-foreground hover:text-ink",
                  )}
                >
                  {label}
                </button>
              ))}
            </div>
          </div>

          {/* Custom date inputs */}
          {timeRange === "custom" && (
            <div className="flex items-center gap-2">
              <Input
                type="date"
                value={customStart}
                onChange={(e) => setCustomStart(e.target.value)}
                className="!w-auto !py-1 text-xs"
              />
              <span className="text-xs text-muted-foreground">to</span>
              <Input
                type="date"
                value={customEnd}
                onChange={(e) => setCustomEnd(e.target.value)}
                className="!w-auto !py-1 text-xs"
              />
            </div>
          )}

          <Button
            onClick={handleReplay}
            disabled={replayMutation.isPending || jobsLoading || jobs.length === 0}
          >
            <RefreshCw
              className={cn(
                "h-4 w-4",
                replayMutation.isPending && "animate-spin",
              )}
            />
            {replayMutation.isPending
              ? "Replaying..."
              : `Replay ${jobs.length} jobs`}
          </Button>

          {replayMutation.isError && (
            <p className="text-xs text-danger">
              {replayMutation.error.message}
            </p>
          )}
        </div>
      </Card>

      {/* Impact summary */}
      {rows.length > 0 && <ImpactSummary rows={rows} />}

      {/* Diff table */}
      {rows.length > 0 && (
        <div className="surface-card overflow-hidden rounded-2xl">
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="border-b border-border">
                <tr>
                  <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                    Job ID
                  </th>
                  <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                    Topic
                  </th>
                  <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                    Original
                  </th>
                  <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                    New
                  </th>
                  <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                    Status
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {rows.map((row) => (
                  <tr
                    key={row.jobId}
                    className={cn(
                      "transition-colors",
                      row.changed
                        ? "bg-[color:rgba(197,138,28,0.04)]"
                        : "hover:bg-surface2/60",
                    )}
                  >
                    <td className="px-4 py-3 font-mono text-xs text-ink">
                      {row.jobId.slice(0, 12)}
                    </td>
                    <td className="px-4 py-3 text-xs text-muted-foreground">
                      {row.topic}
                    </td>
                    <td className="px-4 py-3">
                      <Badge variant={decisionBadge[row.originalDecision] ?? "default"}>
                        {row.originalDecision}
                      </Badge>
                    </td>
                    <td className="px-4 py-3">
                      <Badge variant={decisionBadge[row.newDecision] ?? "default"}>
                        {row.newDecision}
                      </Badge>
                    </td>
                    <td className="px-4 py-3">
                      {row.changed ? (
                        <ChangeArrow
                          from={row.originalDecision}
                          to={row.newDecision}
                        />
                      ) : (
                        <span className="text-xs text-muted-foreground">unchanged</span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Changed-only highlight */}
      {changedRows.length > 0 && changedRows.length < rows.length && (
        <p className="text-xs text-muted-foreground">
          Showing {rows.length} total &middot;{" "}
          <span className="font-semibold text-warning">
            {changedRows.length} changed
          </span>
        </p>
      )}
    </div>
  );
}
