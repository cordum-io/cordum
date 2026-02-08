import { useCallback, useState } from "react";
import { usePolicyRules } from "../../hooks/usePolicies";
import { Badge } from "../ui/Badge";
import { cn } from "../../lib/utils";
import type { PolicyRule } from "../../api/types";

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const decisionVariant: Record<string, "success" | "danger" | "warning" | "info" | "default"> = {
  allow: "success",
  deny: "danger",
  require_approval: "warning",
  throttle: "info",
};

const decisionLabel: Record<string, string> = {
  allow: "Allow",
  deny: "Deny",
  require_approval: "Approval",
  throttle: "Throttle",
};

const DECISION_FILTERS = ["all", "allow", "deny", "require_approval", "throttle"] as const;


// ---------------------------------------------------------------------------
// Match criteria badges
// ---------------------------------------------------------------------------

function MatchCriteria({ criteria }: { criteria: Record<string, unknown> }) {
  const caps = Array.isArray(criteria.capabilities) ? (criteria.capabilities as string[]) : [];
  const tags = Array.isArray(criteria.riskTags) ? (criteria.riskTags as string[]) : [];
  const items = [
    ...caps.map((c) => ({ label: c, variant: "info" as const })),
    ...tags.map((t) => ({ label: t, variant: "warning" as const })),
  ];
  if (items.length === 0) {
    return <span className="text-xs text-muted">any</span>;
  }
  return (
    <div className="flex flex-wrap gap-1">
      {items.map((item, idx) => (
        <Badge key={idx} variant={item.variant} className="text-[10px] px-2 py-0.5">
          {item.label}
        </Badge>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Skeleton
// ---------------------------------------------------------------------------

function SkeletonRows({ count = 6 }: { count?: number }) {
  return (
    <>
      {Array.from({ length: count }, (_, i) => (
        <tr key={i} className="animate-pulse">
          {Array.from({ length: 6 }, (_, j) => (
            <td key={j} className="px-4 py-3">
              <div className="h-4 rounded bg-surface2 w-3/4" />
            </td>
          ))}
        </tr>
      ))}
    </>
  );
}

// ---------------------------------------------------------------------------
// RulesTable
// ---------------------------------------------------------------------------

export function RulesTable({ onSelectRule }: { onSelectRule?: (rule: PolicyRule) => void }) {
  const { data, isLoading, isError } = usePolicyRules();
  const [filter, setFilter] = useState<string>("all");

  const allRules = data?.items ?? [];

  // Filter
  const filtered = filter === "all"
    ? allRules
    : allRules.filter((r) => r.decisionType === filter);

  const sorted = [...filtered];

  const handleRowClick = useCallback(
    (rule: PolicyRule) => {
      onSelectRule?.(rule);
    },
    [onSelectRule],
  );

  return (
    <div className="space-y-3">
      {/* Filter */}
      <div className="flex items-center gap-1 rounded-xl border border-border p-0.5 w-fit">
        {DECISION_FILTERS.map((f) => (
          <button
            key={f}
            type="button"
            onClick={() => setFilter(f)}
            className={cn(
              "rounded-lg px-3 py-1 text-xs font-medium capitalize transition",
              filter === f
                ? "bg-accent text-white"
                : "text-muted hover:text-ink hover:bg-surface2/60",
            )}
          >
            {f === "require_approval" ? "Approval" : f}
          </button>
        ))}
      </div>

      <div className="surface-card overflow-hidden rounded-2xl">
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="border-b border-border">
              <tr>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted">
                  ID
                </th>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted">
                  Match Criteria
                </th>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted">
                  Decision
                </th>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted">
                  Source
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {isLoading && <SkeletonRows />}

              {!isLoading && isError && (
                <tr>
                  <td colSpan={6} className="px-4 py-12 text-center text-muted">
                    Failed to load policy rules.
                  </td>
                </tr>
              )}

              {!isLoading && !isError && sorted.length === 0 && (
                <tr>
                  <td colSpan={6} className="px-4 py-12 text-center text-muted">
                    No rules match the selected filter.
                  </td>
                </tr>
              )}

              {!isLoading &&
                sorted.map((rule: PolicyRule) => (
                  <tr
                    key={rule.id}
                    className="cursor-pointer transition-colors hover:bg-surface2/60"
                    onClick={() => handleRowClick(rule)}
                  >
                    <td className="px-4 py-3 font-mono text-xs text-ink">
                      {rule.id.slice(0, 8)}
                    </td>
                    <td className="px-4 py-3 max-w-xs">
                      <MatchCriteria criteria={rule.matchCriteria} />
                    </td>
                    <td className="px-4 py-3">
                      <Badge variant={decisionVariant[rule.decisionType] ?? "default"}>
                        {decisionLabel[rule.decisionType] ?? rule.decisionType}
                      </Badge>
                    </td>
                    <td className="px-4 py-3 text-xs text-muted">
                      {rule.source && "fragment_id" in rule.source
                        ? String((rule.source as Record<string, unknown>).fragment_id ?? "—")
                        : "\u2014"}
                    </td>
                  </tr>
                ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
