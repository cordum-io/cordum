import { useState, useRef, useEffect, useMemo } from "react";
import { Info, Loader2 } from "lucide-react";
import { Card } from "../ui/Card";
import { useJobs } from "../../hooks/useJobs";
import type { Job } from "../../api/types";

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

interface ImpactPreviewProps {
  capabilities: string[];
  riskTags: string[];
  logic: string;
  decisionType: string;
}

// ---------------------------------------------------------------------------
// Decision colors (mirrors existing palette)
// ---------------------------------------------------------------------------

const DECISION_COLORS: Record<string, { bg: string; text: string; label: string }> = {
  allow: { bg: "bg-success/10", text: "text-success", label: "Allow" },
  deny: { bg: "bg-danger/10", text: "text-danger", label: "Deny" },
  require_approval: { bg: "bg-warning/10", text: "text-warning", label: "Require Approval" },
  throttle: { bg: "bg-accent/10", text: "text-accent", label: "Throttle" },
};

// ---------------------------------------------------------------------------
// Client-side matching
// ---------------------------------------------------------------------------

function matchesRule(
  job: Job,
  capabilities: string[],
  riskTags: string[],
  logic: string,
): boolean {
  // Empty criteria matches nothing (no conditions = no impact)
  if (capabilities.length === 0 && riskTags.length === 0) return false;

  const jobCaps = job.capabilities ?? [];
  const jobTags = job.riskTags ?? [];

  const capMatch =
    capabilities.length === 0 ||
    capabilities.some((c) => jobCaps.includes(c));
  const tagMatch =
    riskTags.length === 0 || riskTags.some((t) => jobTags.includes(t));

  if (logic === "AND") return capMatch && tagMatch;
  return capMatch || tagMatch;
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function ImpactPreview({
  capabilities,
  riskTags,
  logic,
  decisionType,
}: ImpactPreviewProps) {
  const { data: jobsData, isLoading: jobsLoading } = useJobs({ limit: 100 });
  const jobs = jobsData?.items ?? [];

  // Debounce criteria changes (500ms)
  const [debouncedCriteria, setDebouncedCriteria] = useState({
    capabilities,
    riskTags,
    logic,
    decisionType,
  });
  const [isDebouncing, setIsDebouncing] = useState(false);
  const debounceRef = useRef<ReturnType<typeof setTimeout>>();

  useEffect(() => {
    setIsDebouncing(true);
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => {
      setDebouncedCriteria({ capabilities, riskTags, logic, decisionType });
      setIsDebouncing(false);
    }, 500);
    return () => {
      if (debounceRef.current) clearTimeout(debounceRef.current);
    };
  }, [capabilities, riskTags, logic, decisionType]);

  // Compute matched jobs
  const { matched, total } = useMemo(() => {
    const matched = jobs.filter((j) =>
      matchesRule(
        j,
        debouncedCriteria.capabilities,
        debouncedCriteria.riskTags,
        debouncedCriteria.logic,
      ),
    );
    return { matched, total: jobs.length };
  }, [jobs, debouncedCriteria]);

  const matchedCount = matched.length;
  const isAnalyzing = isDebouncing || jobsLoading;

  // Build breakdown: matched jobs get the NEW decision, rest are "unaffected"
  const decisionKeys = ["allow", "deny", "require_approval", "throttle"] as const;

  return (
    <Card>
      <div className="space-y-4">
        {/* Header */}
        <div className="flex items-center gap-2">
          <h4 className="text-xs font-semibold uppercase tracking-widest text-muted">
            Impact Preview
          </h4>
          <span className="group relative">
            <Info className="h-3.5 w-3.5 text-muted" />
            <span className="pointer-events-none absolute bottom-full left-1/2 z-10 mb-1.5 -translate-x-1/2 whitespace-nowrap rounded-lg bg-ink px-2.5 py-1.5 text-[11px] text-white opacity-0 shadow-lg transition group-hover:opacity-100">
              Shows how this rule would affect recent jobs
            </span>
          </span>
          {isAnalyzing && (
            <span className="flex items-center gap-1 text-[11px] text-muted">
              <Loader2 className="h-3 w-3 animate-spin" />
              Analyzing...
            </span>
          )}
        </div>

        {/* Empty state */}
        {!jobsLoading && total === 0 && (
          <p className="text-sm text-muted">No recent jobs to analyze.</p>
        )}

        {/* Summary + breakdown */}
        {total > 0 && (
          <>
            {/* Summary line */}
            <p className="text-sm text-ink">
              This rule would affect{" "}
              <span className="font-semibold">
                {matchedCount}
              </span>{" "}
              of{" "}
              <span className="font-semibold">{total}</span>{" "}
              recent jobs
            </p>

            {/* Breakdown cards */}
            <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
              {decisionKeys.map((key) => {
                const colors = DECISION_COLORS[key];
                const count =
                  key === debouncedCriteria.decisionType ? matchedCount : 0;
                return (
                  <div
                    key={key}
                    className={`rounded-xl ${colors.bg} px-3 py-2.5 text-center`}
                  >
                    <p className={`text-lg font-bold ${colors.text}`}>
                      {count}
                    </p>
                    <p className="text-[11px] font-medium text-muted">
                      {colors.label}
                    </p>
                  </div>
                );
              })}
            </div>

            {/* Collapsible affected jobs list */}
            {matchedCount > 0 && (
              <AffectedJobsList jobs={matched} />
            )}
          </>
        )}
      </div>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Collapsible affected jobs detail
// ---------------------------------------------------------------------------

function AffectedJobsList({ jobs }: { jobs: Job[] }) {
  const [expanded, setExpanded] = useState(false);
  const shown = expanded ? jobs : jobs.slice(0, 5);

  return (
    <div>
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        className="text-[11px] font-semibold text-accent hover:underline"
      >
        {expanded ? "Hide" : "Show"} affected jobs ({jobs.length})
      </button>
      {(expanded || jobs.length <= 5) && (
        <div className="mt-2 max-h-48 space-y-1 overflow-y-auto">
          {shown.map((j) => (
            <div
              key={j.id}
              className="flex items-center justify-between rounded-lg border border-border px-2.5 py-1.5 text-[11px]"
            >
              <span className="font-mono text-muted">
                {j.id.slice(0, 12)}...
              </span>
              <span className="text-muted">
                {(j.capabilities ?? []).slice(0, 2).join(", ") || "no caps"}
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
