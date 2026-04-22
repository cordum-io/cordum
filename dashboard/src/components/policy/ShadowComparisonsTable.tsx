import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { ListFilter, TrendingUp, TrendingDown, AlertCircle, Check } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonTable } from "@/components/ui/Skeleton";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { cn, formatRelativeTime } from "@/lib/utils";
import { useShadowResultsComparisons } from "@/hooks/useShadowPolicy";
import type { ShadowDiff } from "@/api/types";

const DIFF_CHIPS: { id: ShadowDiff | "all"; label: string; icon?: typeof TrendingUp }[] = [
  { id: "all", label: "All" },
  { id: "escalated", label: "Escalated", icon: TrendingUp },
  { id: "relaxed", label: "Relaxed", icon: TrendingDown },
  { id: "approval_differ", label: "Approval differ", icon: AlertCircle },
  { id: "unchanged", label: "Unchanged", icon: Check },
];

export interface ShadowComparisonsTableProps {
  bundleID: string;
  fromMs?: number;
  untilMs?: number;
}

/**
 * ShadowComparisonsTable lists job-level comparisons of active vs
 * shadow verdicts. Infinite-scroll via useShadowResultsComparisons;
 * filter chips narrow by diff classification. Rows link to
 * JobDetailPage so an operator can see the full job context behind a
 * surprising verdict.
 */
export function ShadowComparisonsTable({ bundleID, fromMs, untilMs }: ShadowComparisonsTableProps) {
  const [diff, setDiff] = useState<ShadowDiff | "all">("all");
  const navigate = useNavigate();

  const query = useShadowResultsComparisons({
    bundleID,
    fromMs,
    untilMs,
    diff: diff === "all" ? undefined : diff,
    pageSize: 50,
  });

  const allEntries = useMemo(
    () => query.data?.pages.flatMap((p) => p.entries ?? []) ?? [],
    [query.data],
  );

  return (
    <div className="instrument-card p-4" data-testid="shadow-comparisons-table">
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-2">
          <ListFilter className="w-4 h-4 text-cordum" />
          <span className="text-xs font-mono uppercase tracking-widest text-muted-foreground">
            Drill-down
          </span>
        </div>
        {!query.isLoading && !query.isError && (
          <span className="text-xs text-muted-foreground">
            {allEntries.length.toLocaleString()}
            {query.hasNextPage ? "+" : ""} shown
          </span>
        )}
      </div>

      <div className="flex flex-wrap items-center gap-1 mb-3" role="radiogroup" aria-label="Diff filter">
        {DIFF_CHIPS.map((chip) => {
          const Icon = chip.icon;
          const active = diff === chip.id;
          return (
            <button
              key={chip.id}
              type="button"
              onClick={() => setDiff(chip.id)}
              role="radio"
              aria-checked={active}
              className={cn(
                "inline-flex items-center gap-1 rounded-full border px-2.5 py-0.5 text-xs transition-colors",
                active
                  ? "border-cordum bg-cordum/10 text-cordum"
                  : "border-border text-muted-foreground hover:text-foreground",
              )}
            >
              {Icon && <Icon className="w-3 h-3" />}
              {chip.label}
            </button>
          );
        })}
      </div>

      {query.isLoading && <SkeletonTable rows={5} />}

      {query.isError && (
        <ErrorBanner
          message={
            query.error instanceof Error ? query.error.message : "Failed to load comparisons"
          }
          onRetry={() => void query.refetch()}
        />
      )}

      {!query.isLoading && !query.isError && allEntries.length === 0 && (
        <EmptyState
          title="No comparisons in range"
          description={
            diff === "all"
              ? "No shadow evaluations produced a comparison yet. Results appear as jobs flow."
              : `No ${diff.replaceAll("_", " ")} results in the selected window.`
          }
        />
      )}

      {allEntries.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full text-xs font-mono">
            <thead>
              <tr className="border-b border-border text-muted-foreground">
                <th scope="col" className="py-2 pr-3 text-left">Time</th>
                <th scope="col" className="py-2 pr-3 text-left">Job</th>
                <th scope="col" className="py-2 pr-3 text-left">Agent</th>
                <th scope="col" className="py-2 pr-3 text-left">Active</th>
                <th scope="col" className="py-2 pr-3 text-left">Shadow</th>
                <th scope="col" className="py-2 pr-3 text-left">Diff</th>
                <th scope="col" className="py-2 text-left">Rule active → shadow</th>
              </tr>
            </thead>
            <tbody>
              {allEntries.map((e) => (
                <tr
                  key={`${e.seq ?? 0}-${e.ts_ms}-${e.job_id}`}
                  className="border-b border-border/50 hover:bg-surface-1 cursor-pointer"
                  onClick={() =>
                    navigate(
                      `/jobs/${encodeURIComponent(e.job_id)}?seq=${e.seq ?? 0}&highlightShadow=true`,
                    )
                  }
                >
                  <td className="py-2 pr-3 text-muted-foreground">{formatRelativeTime(new Date(e.ts_ms).toISOString())}</td>
                  <td className="py-2 pr-3 truncate max-w-[180px]" title={e.job_id}>{e.job_id}</td>
                  <td className="py-2 pr-3 truncate max-w-[140px]" title={e.agent_id}>{e.agent_id}</td>
                  <td className="py-2 pr-3">{e.active_verdict}</td>
                  <td className="py-2 pr-3">{e.shadow_verdict}</td>
                  <td className="py-2 pr-3">
                    <DiffBadge diff={e.diff} />
                  </td>
                  <td className="py-2 truncate max-w-[220px]">
                    {(e.active_rule_id ?? "—") + " → " + (e.shadow_rule_id ?? "—")}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>

          {query.hasNextPage && (
            <div className="flex justify-center pt-3">
              <Button
                variant="outline"
                size="sm"
                disabled={query.isFetchingNextPage}
                onClick={() => void query.fetchNextPage()}
              >
                {query.isFetchingNextPage ? "Loading..." : "Load more"}
              </Button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function DiffBadge({ diff }: { diff: ShadowDiff }) {
  const styles: Record<ShadowDiff, string> = {
    escalated: "bg-orange-500/10 text-orange-400 border-orange-500/30",
    relaxed: "bg-emerald-500/10 text-emerald-400 border-emerald-500/30",
    approval_differ: "bg-amber-500/10 text-amber-400 border-amber-500/30",
    unchanged: "bg-muted text-muted-foreground border-border",
  };
  return (
    <span
      className={cn(
        "inline-flex items-center rounded border px-1.5 py-0.5 text-[10px] uppercase tracking-wider",
        styles[diff],
      )}
    >
      {diff.replaceAll("_", " ")}
    </span>
  );
}
